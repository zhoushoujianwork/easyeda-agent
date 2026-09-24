import { projectOpen, projectExport } from './project-transfer';
/**
 * Typed-action dispatch. Each action maps to exactly one (occasionally a small
 * cluster of) `eda.*` call(s), serializes the result to plain JSON, and returns
 * an `ActionResult`. Errors from `eda.*` are wrapped as `ActionError` with the
 * original message preserved in `detail`.
 */

import { type BeautifyOptions, runBeautify } from './beautify';
import { armDeadline } from './deadlines';
import { exactJSON, preservedInstance } from './preserve-instance';
import { barePcbRuleConfiguration, pcbRulesEqual, planPcbConfig } from './pcb-config';
import { pcbNetColorSet } from './pcb-net-color';
import { documentTypeLabel, readResponseContext } from './eda-context';
import { readProjectFootprintSourceArchive, readProjectNativeSourceArchive } from './native-footprint-source';
import {
	assertLegacySimpleWireOperation,
	classifyWireContact,
	classifyWireSegment,
	isDegenerateWireSegment,
	physicalWireIslands,
	readObservedWireSegments,
	WIRE_CONTACT_EPS,
	wirePointOnSegment,
	type ObservedWireSegment,
} from './schematic-wire-topology';
import {
	ActionError,
	type ActionResult,
	ErrorCodes,
	type ResponseArtifact,
} from './protocol';
import {
	asPayload,
	blobToBase64,
	classifyPinConnectivity,
	classifyWireConnectivity,
	describeThrown,
	filterExactLcsc,
	footprintMatchesInstance,
	isLcscQuery,
	newArtifactId,
	type NamedLibItem,
	normalizeRegion,
	normalizeWirePoints,
	optionalBoolean,
	optionalNumber,
	optionalString,
	pickNamedCandidate,
	projectSchematicAttributeInventory,
	readDeviceFootprint,
	readNativeFootprintSource,
	requireNumber,
	requireString,
	requireStringArray,
	uint8ToBase64,
	type NativeSchematicAttribute,
} from './util';

type Payload = Record<string, unknown>;
type Handler = (payload: Payload) => Promise<ActionResult>;

/**
 * The schematic component primitive type, derived from the API return type so
 * we do not depend on the internal `$1`-suffixed class name emitted by the SDK.
 */
type SchComponent = NonNullable<Awaited<ReturnType<typeof eda.sch_PrimitiveComponent.create>>>;

/** The schematic component pin primitive type, derived from the API. */
type SchPin = NonNullable<Awaited<ReturnType<typeof eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId>>>[number];

/** The PCB component primitive type, derived from the API. */
type PcbComponent = NonNullable<Awaited<ReturnType<typeof eda.pcb_PrimitiveComponent.getAll>>>[number];

/** The PCB component pad primitive type, derived from the API. */
type PcbPad = NonNullable<Awaited<ReturnType<typeof eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId>>>[number];

/**
 * Wrap an unknown error thrown by an `eda.*` call into a structured ActionError.
 *
 * @param err - the thrown value
 * @param message - human-readable summary
 * @returns an ActionError carrying EDA_CALL_FAILED and the original detail
 */
function edaError(err: unknown, message: string): ActionError {
	const detail = describeThrown(err);
	return new ActionError(ErrorCodes.EDA_CALL_FAILED, message, detail);
}

// ─── Serialization helpers ───────────────────────────────────────────

/**
 * Serialize a schematic component primitive to plain JSON using its public
 * getState_* accessors.
 *
 * NOTE on identity fields:
 *   - `uniqueId`, `symbol`, `footprint` are placed-INSTANCE identifiers
 *     (sub-primitive ids of this specific placement). They are NOT device-library
 *     uuids — replaying one into `sch place` makes `sch_PrimitiveComponent.create` hang.
 *   - `device` is the device-library identity of the placed part, taken from
 *     `getState_Component()` — WARNING: its `uuid` is a 16-char placed-symbol id, NOT
 *     the 32-char device uuid (resolve via `resolvePlacedDeviceIdentity`). Its
 *     `uuid` is the device uuid that `lib_Device.search` reports and `sch place
 *     --uuid` expects. CAVEAT: imported devices (Altium/KiCad → EasyEDA) often
 *     carry an EMPTY `libraryUuid` on the placed instance; when empty, resolve it
 *     via `lib search` / `lib by-lcsc` or `resolvePlacedDeviceIdentity`
 *     before feeding it back into `sch place`. Exposing this lets an image-tracing
 *     flow lock onto the exact symbol variant of a golden design instead of
 *     re-searching by LCSC C-number (which can hit a different pin-numbering variant).
 *   - `component` is kept for backward compatibility (raw `getState_Component()`).
 *
 * @param component - the component primitive object
 * @returns a plain JSON record
 */
/**
 * Project the raw `getState_Component()` value into a stable, structured device
 * identity { libraryUuid, uuid, name }. This is the device-library identity of the
 * placed part (same shape `sch place` / rebind consume), NOT a placed-instance id.
 *
 * `libraryUuid` may be an empty string for imported devices — that is reported
 * faithfully (no reverse look-up here to keep list a pure read); resolve it via
 * `lib search` / `lib by-lcsc` before replaying into `sch place`.
 *
 * @param raw - the value from `getState_Component()` ({ libraryUuid, uuid })
 * @param name - the device name from `getState_Name()`
 * @returns { libraryUuid, uuid, name }
 */
export function normalizeDeviceRef(raw: unknown, name: unknown): Record<string, unknown> {
	const ref = (raw ?? {}) as Partial<DeviceRef>;
	return {
		libraryUuid: typeof ref.libraryUuid === 'string' ? ref.libraryUuid : '',
		uuid: typeof ref.uuid === 'string' ? ref.uuid : '',
		name: typeof name === 'string' ? name : '',
	};
}

export function serializeComponent(component: SchComponent): Record<string, unknown> {
	return {
		primitiveId: component.getState_PrimitiveId(),
		componentType: component.getState_ComponentType(),
		designator: component.getState_Designator(),
		name: component.getState_Name(),
		x: component.getState_X(),
		y: component.getState_Y(),
		rotation: component.getState_Rotation(),
		mirror: component.getState_Mirror(),
		net: component.getState_Net(),
		subPartName: component.getState_SubPartName(),
		addIntoBom: component.getState_AddIntoBom(),
		addIntoPcb: component.getState_AddIntoPcb(),
		uniqueId: component.getState_UniqueId(),
		manufacturer: component.getState_Manufacturer(),
		manufacturerId: component.getState_ManufacturerId(),
		supplier: component.getState_Supplier(),
		supplierId: component.getState_SupplierId(),
		component: component.getState_Component(),
		device: normalizeDeviceRef(component.getState_Component(), component.getState_Name()),
		symbol: component.getState_Symbol(),
		footprint: component.getState_Footprint(),
		otherProperty: component.getState_OtherProperty(),
	};
}

/**
 * Serialize a single component pin primitive to plain JSON.
 *
 * @param pin - the component pin primitive object
 * @returns a plain JSON record
 */
function serializePin(pin: SchPin): Record<string, unknown> {
	const readOtherProperty = (pin as unknown as { getState_OtherProperty?: () => Record<string, string | number | boolean> | undefined }).getState_OtherProperty;
	return {
		primitiveId: pin.getState_PrimitiveId(),
		pinNumber: pin.getState_PinNumber(),
		pinName: pin.getState_PinName(),
		x: pin.getState_X(),
		y: pin.getState_Y(),
		rotation: pin.getState_Rotation(),
		noConnected: pin.getState_NoConnected(),
		// V4 exposes display/text metadata on the pin itself. Preserve it in
		// snapshots so diff/copy/rebuild code cannot silently erase information
		// that did not exist in the old public type package. The capability guard
		// keeps historical recorded fixtures readable.
		otherProperty: typeof readOtherProperty === 'function' ? readOtherProperty.call(pin) : undefined,
	};
}

/** Read-only connectivity inventory for the page that was active at request time. */
export interface SchematicConnectivitySummary {
	scope: 'activePage';
	wires: number;
	buses: number;
	netflags: number;
	netports: number;
	netlabels: number;
	shortSymbols: number;
}

/**
 * Count the page-level primitives used by layout preflight.
 *
 * This helper intentionally accepts already-scoped arrays: callers must read
 * them with the SDK's active-page `getAll()` overload, never the all-pages
 * component overload.
 */
export function summarizeActivePageConnectivity(
	componentTypes: ReadonlyArray<unknown>,
	wires: ReadonlyArray<unknown>,
	buses: ReadonlyArray<unknown>,
): SchematicConnectivitySummary {
	const summary: SchematicConnectivitySummary = {
		scope: 'activePage',
		wires: wires.length,
		buses: buses.length,
		netflags: 0,
		netports: 0,
		netlabels: 0,
		shortSymbols: 0,
	};
	for (const rawType of componentTypes) {
		switch (String(rawType ?? '')) {
			case 'netflag': summary.netflags++; break;
			case 'netport': summary.netports++; break;
			case 'netlabel': summary.netlabels++; break;
			case 'short_symbol': summary.shortSymbols++; break;
		}
	}
	return summary;
}

/**
 * Read a fail-closed connectivity inventory for the currently active page.
 * An SDK response that is missing its array is unavailable data, not zero
 * primitives: layout preflight must never mistake a failed read for a clean page.
 */
async function readActivePageConnectivitySummary(): Promise<SchematicConnectivitySummary> {
	try {
		const components = await eda.sch_PrimitiveComponent.getAll();
		const wires = await eda.sch_PrimitiveWire.getAll();
		const buses = await eda.sch_PrimitiveBus.getAll();
		if (!Array.isArray(components)) throw new Error('component getAll() did not return an array');
		if (!Array.isArray(wires)) throw new Error('wire getAll() did not return an array');
		if (!Array.isArray(buses)) throw new Error('bus getAll() did not return an array');
		return summarizeActivePageConnectivity(
			components.map(component => component.getState_ComponentType()),
			wires,
			buses,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to summarize connectivity on the active schematic page.');
	}
}

/**
 * Serialize a PCB component primitive to plain JSON via its public getState_*
 * accessors. Unlike a schematic component, a PCB component is layer-bound
 * (TOP/BOTTOM) and carries no net flags — connectivity lives on its pads.
 *
 * @param component - the PCB component primitive object
 * @returns a plain JSON record
 */
function serializePcbComponent(component: PcbComponent): Record<string, unknown> {
	return {
		primitiveId: component.getState_PrimitiveId(),
		// uniqueId is the SAME namespace the schematic side reports (serializeComponent
		// already exposes it): a component keeps one `gge*` id across both documents,
		// minted by the platform at first sch→PCB import. primitiveId does NOT — each
		// document mints its own — so uniqueId is the only reliable schematic↔PCB join
		// key. `pcb sync-designators` uses it to repair placeholder designators
		// (U? / C? / RF?) on boards wiped by the old attrs_backfill Designator-key bug.
		uniqueId: component.getState_UniqueId(),
		designator: component.getState_Designator(),
		name: component.getState_Name(),
		layer: component.getState_Layer(),
		x: component.getState_X(),
		y: component.getState_Y(),
		rotation: component.getState_Rotation(),
		locked: component.getState_PrimitiveLock(),
		addIntoBom: component.getState_AddIntoBom(),
		manufacturerId: component.getState_ManufacturerId(),
		supplierId: component.getState_SupplierId(),
	};
}

/** Return an axis-aligned bbox envelope for supported native pad shapes. */
export function pcbPadExtent(shape: unknown, rotation: unknown): { width: number; height: number } | null {
	if (!Array.isArray(shape) || shape.length < 2 || typeof shape[0] !== 'string') return null;
	if (typeof rotation !== 'number' || !Number.isFinite(rotation)) return null;
	const rot = rotation;
	const angle = rot * Math.PI / 180;
	const c = Math.abs(Math.cos(angle));
	const s = Math.abs(Math.sin(angle));
	const positive = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value > 0;

	if (shape[0] === 'NGON') {
		// The first numeric field is diameter; the second is the side count, not
		// height.  Its circumcircle is a safe exact bbox envelope at any rotation.
		if (!positive(shape[1])) return null;
		return { width: shape[1], height: shape[1] };
	}
	if (!positive(shape[1]) || !positive(shape[2])) return null;
	const w = shape[1], h = shape[2];
	switch (shape[0]) {
	case 'ELLIPSE':
		return {
			width: Math.hypot(w * c, h * s),
			height: Math.hypot(w * s, h * c),
		};
	case 'OVAL': {
		// OVAL is a stadium: a center segment of |w-h| capped by circles whose
		// diameter is min(w,h).
		const d = Math.min(w, h);
		if (w >= h) return { width: c * (w - h) + d, height: s * (w - h) + d };
		return { width: s * (h - w) + d, height: c * (h - w) + d };
	}
	case 'RECT':
		return { width: w * c + h * s, height: w * s + h * c };
	default:
		return null;
	}
}

function padExtent(pad: PcbPad): { width: number; height: number } | null {
	try {
		const special = pad.getState_SpecialPad?.();
		if (Array.isArray(special) && special.length > 0) return null;
		return pcbPadExtent(pad.getState_Pad?.(), pad.getState_Rotation?.());
	}
	catch { return null; }
}

/**
 * Serialize a single PCB component pad to plain JSON. Pads carry the
 * net-by-name connectivity model that replaces schematic net flags.
 * width/height are the REAL axis-aligned copper extents (mil) from the pad's
 * shape tuple — so Go-side checks/routing stop guessing a nominal pad size —
 * omitted for complex-polygon pads (consumers keep their fallback).
 *
 * @param pad - the PCB component pad primitive object
 * @returns a plain JSON record
 */
export function serializePcbPad(pad: PcbPad): Record<string, unknown> {
	let shape: unknown = null;
	let specialPad: unknown = null;
	try { shape = pad.getState_Pad?.() ?? null; } catch { /* unreadable stays null */ }
	try { specialPad = pad.getState_SpecialPad?.() ?? null; } catch { /* unreadable stays null */ }
	const rotation = pad.getState_Rotation();
	const record: Record<string, unknown> = {
		primitiveId: pad.getState_PrimitiveId(),
		padNumber: pad.getState_PadNumber(),
		net: pad.getState_Net(),
		layer: pad.getState_Layer(),
		x: pad.getState_X(),
		y: pad.getState_Y(),
		rotation,
		padType: pad.getState_PadType(),
		// Keep the source tuple.  width/height are only a bbox for general PCB
		// clearance consumers; connectivity evidence must reason from shape.
		shape,
		specialPad,
	};
	const ext = Array.isArray(specialPad) && specialPad.length > 0 ? null : pcbPadExtent(shape, rotation);
	if (ext) {
		record.width = ext.width;
		record.height = ext.height;
	}
	return record;
}

/**
 * Build a base64 inline artifact from a Blob/File.
 *
 * @param blob - the binary payload
 * @param kind - artifact kind label
 * @param fileName - file name to suggest to the daemon
 * @param fallbackMime - mime type used when blob.type is empty
 * @returns a response artifact carrying inlineBase64
 */
async function blobToArtifact(
	blob: Blob,
	kind: string,
	fileName: string,
	fallbackMime: string,
): Promise<ResponseArtifact> {
	const inlineBase64 = await blobToBase64(blob);
	return {
		id: newArtifactId(),
		kind,
		mimeType: blob.type || fallbackMime,
		fileName,
		inlineBase64,
	};
}

// ─── Project / document ──────────────────────────────────────────────

const projectCurrent: Handler = async () => {
	let project;
	try {
		project = await eda.dmt_Project.getCurrentProjectInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to read current project info.');
	}
	if (!project) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'No current project is open.');
	}
	return {
		result: {
			uuid: project.uuid,
			name: project.name,
			friendlyName: project.friendlyName,
			teamUuid: project.teamUuid,
			description: project.description,
		},
	};
};

// Preserve the official current-project archive byte-for-byte for offline
// inspection. The archive is evidence, not a parsed sheet-border measurement.
const MAX_PROJECT_SOURCE_BYTES = 8 << 20;
const projectExportSource: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	if (typeof eda.sys_FileManager?.getProjectFile !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'Official project-file getter is unavailable in this EasyEDA build.');
	}
	let file: File | undefined;
	try {
		const before = await withTimeout(eda.dmt_Project.getCurrentProjectInfo(), 7000,
			'project source getCurrentProjectInfo timed out before export after 7000ms');
		if (!before || before.uuid !== uuid) throw new ActionError(ErrorCodes.INVALID_STATE, 'Current project UUID does not match the requested project.');
		file = await withTimeout(eda.sys_FileManager.getProjectFile('easyeda-agent-project.epro2', undefined, 'epro2'), 15000,
			'project source getProjectFile timed out after 15000ms');
		const after = await withTimeout(eda.dmt_Project.getCurrentProjectInfo(), 7000,
			'project source getCurrentProjectInfo timed out after export after 7000ms');
		if (!after || after.uuid !== uuid) throw new ActionError(ErrorCodes.INVALID_STATE, 'Current project changed during source export.');
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to export the native project file.');
	}
	if (!file || !Number.isSafeInteger(file.size) || file.size <= 0 || file.size > MAX_PROJECT_SOURCE_BYTES) {
		throw new ActionError(ErrorCodes.INVALID_STATE,
			`Official project file is missing, empty, or exceeds the ${MAX_PROJECT_SOURCE_BYTES}-byte transfer limit.`);
	}
	const artifact = await blobToArtifact(file, 'project_source', 'project-source.epro2', 'application/octet-stream');
	return { result: { uuid, fileType: 'epro2', size: file.size, artifactId: artifact.id }, artifacts: [artifact] };
};

/** Reconcile a possibly partial create using only the official project read APIs. */
const projectFind: Handler = async (payload) => {
	const friendlyName = requireString(payload, 'friendlyName');
	const teamUuid = optionalString(payload, 'teamUuid') || undefined;
	const scope = teamUuid ? 'team-root' : 'unscoped';
	const matches: Array<{ uuid: string; friendlyName: string; teamUuid: string; folderUuid: string | null }> = [];
	const unreadableUuids: string[] = [];
	let uuids: string[];
	try {
		const listed = await eda.dmt_Project.getAllProjectsUuid(teamUuid);
		if (!Array.isArray(listed) || listed.some(uuid => typeof uuid !== 'string' || !uuid)) {
			return { result: {
				friendlyName, teamUuid: teamUuid ?? null, presence: 'unknown', matches,
				enumeration: { scope, complete: false, reason: 'invalid_uuid_list', uuidCount: 0, readableCount: 0, unreadableUuids },
			} };
		}
		uuids = listed;
	}
	catch (err) {
		return { result: {
			friendlyName, teamUuid: teamUuid ?? null, presence: 'unknown', matches,
			enumeration: { scope, complete: false, reason: 'uuid_list_failed', uuidCount: 0, readableCount: 0, unreadableUuids },
		}, warnings: [`Project UUID enumeration failed: ${describeThrown(err)}`] };
	}

	const uniqueUuids = [...new Set(uuids)];
	let readableCount = 0;
	let teamScopeMismatch = false;
	for (const uuid of uniqueUuids) {
		try {
			const info = await eda.dmt_Project.getProjectInfo(uuid);
			if (!info || info.uuid !== uuid || typeof info.friendlyName !== 'string' || typeof info.teamUuid !== 'string') {
				unreadableUuids.push(uuid);
				continue;
			}
			readableCount++;
			if (teamUuid && info.teamUuid !== teamUuid) {
				teamScopeMismatch = true;
				continue;
			}
			if (info.friendlyName === friendlyName) {
				matches.push({ uuid, friendlyName: info.friendlyName, teamUuid: info.teamUuid, folderUuid: info.folderUuid ?? null });
			}
		}
		catch { unreadableUuids.push(uuid); }
	}
	// The SDK defaults a team lookup without folderUuid to the team root.
	// An empty list has historically been returned for populated local workspaces.
	const reason = uuids.length === 0 ? 'empty_uuid_list'
		: uuids.length !== uniqueUuids.length ? 'duplicate_uuids'
		: unreadableUuids.length > 0 ? 'project_info_incomplete'
		: teamScopeMismatch ? 'team_scope_mismatch'
		: !teamUuid ? 'team_not_specified'
		: null;
	const complete = reason === null;
	return { result: {
		friendlyName, teamUuid: teamUuid ?? null,
		presence: matches.length > 0 ? 'found' : complete ? 'absent' : 'unknown',
		matches,
		enumeration: { scope, complete, reason, uuidCount: uuids.length, readableCount, unreadableUuids },
	} };
};

/** Create a project through the official project API, with optional immediate open. */
const projectCreate: Handler = async (payload) => {
	const friendlyName = requireString(payload, 'friendlyName');
	const projectName = optionalString(payload, 'projectName');
	const teamUuid = optionalString(payload, 'teamUuid');
	const folderUuid = optionalString(payload, 'folderUuid');
	const description = optionalString(payload, 'description');
	const open = optionalBoolean(payload, 'open') === true;
	let uuid: string | undefined;
	try {
		uuid = await eda.dmt_Project.createProject(
			friendlyName, projectName, teamUuid, folderUuid, description,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to create EasyEDA project.');
	}
	if (!uuid) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Project creation returned no UUID.');
	}
	let opened = false;
	if (open) {
		try { opened = await eda.dmt_Project.openProject(uuid); }
		catch (err) {
			return {
				result: { partial: true, uuid, friendlyName, projectName: projectName ?? null, created: true, opened: false },
				warnings: [`Project was created, but opening it failed: ${describeThrown(err)}`],
			};
		}
		if (!opened) {
			return {
				result: { partial: true, uuid, friendlyName, projectName: projectName ?? null, created: true, opened: false },
				warnings: ['Project was created, but EasyEDA returned false while opening it; open the created project explicitly before creating documents.'],
			};
		}
	}
	return { result: { uuid, friendlyName, projectName: projectName ?? null, created: true, opened } };
};

const documentCurrent: Handler = async () => {
	let doc;
	try {
		doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to read current document info.');
	}
	if (!doc) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'No active document.');
	}
	return {
		result: {
			uuid: doc.uuid,
			tabId: doc.tabId,
			documentType: documentTypeLabel(doc.documentType),
			documentTypeCode: doc.documentType,
			parentProjectUuid: doc.parentProjectUuid,
		},
	};
};

/** Reload the whole Web editor after its response has left the connector queue. */
const systemPageReload: Handler = async (payload) => {
	const projectUuid = requireString(payload, 'projectUuid');
	const documentUuid = requireString(payload, 'documentUuid');
	const context = await readResponseContext();
	if (context.projectUuid !== projectUuid || context.documentUuid !== documentUuid) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			'Web page reload refused: current project/document differs from the exact requested UUIDs.');
	}
	const delayMs = 500;
	try {
		// EasyEDA shadows both `window` and `Function` in typed handler scope.
		// AsyncFunction's scope resolves the browser global, as in clickImportConfirm.
		// This fixed, payload-free script was verified in the Web host.
		const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
			new (body: string): () => Promise<void>;
		};
		const schedule = new AsyncFunction(`
			const page = window.top;
			if (!page || typeof page.location.reload !== 'function') {
				throw new Error('top-level page reload is unavailable');
			}
			setTimeout(() => page.location.reload(), ${delayMs});
		`);
		await schedule();
	}
	catch (err) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			'Web page reload is unavailable in this connector context.', describeThrown(err));
	}
	return { result: { scheduled: true, delayMs, projectUuid, documentUuid }, context };
};

// ─── Schematic pages ─────────────────────────────────────────────────

const schematicPagesList: Handler = async () => {
	let schematics;
	let pages;
	try {
		schematics = await eda.dmt_Schematic.getAllSchematicsInfo();
		pages = await eda.dmt_Schematic.getAllSchematicPagesInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to list schematics/pages.');
	}
	return {
		result: {
			schematics: schematics.map(s => ({
				uuid: s.uuid,
				name: s.name,
				parentProjectUuid: s.parentProjectUuid,
				page: s.page.map(p => ({ uuid: p.uuid, name: p.name, parentSchematicUuid: p.parentSchematicUuid })),
			})),
			pages: pages.map(p => ({
				uuid: p.uuid,
				name: p.name,
				parentSchematicUuid: p.parentSchematicUuid,
			})),
		},
	};
};

const schematicPageOpen: Handler = async (payload) => {
	const schematicPageUuid = requireString(payload, 'schematicPageUuid');
	let tabId;
	try {
		tabId = await eda.dmt_EditorControl.openDocument(schematicPageUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to open schematic page.');
	}
	if (tabId === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to open schematic page "${schematicPageUuid}".`);
	}
	// openDocument returns before the page's primitives finish loading; wait for
	// the data to settle so a read fired right after isn't empty/stale (#67).
	const ready = await waitSchematicPageSettle();
	return { result: { tabId, ready } };
};

// ─── Schematic / page管理 + 明细表 (title block) ───────────────────────
// All map to eda.dmt_Schematic.*. The "明细表" (title block / parts list on the
// drawing sheet) is the closest thing to "纸张属性" the public API exposes —
// EasyEDA Pro has no set-paper-size (A4/A3) call. Page management = rename /
// create / delete pages and rename the schematic document itself.

/** Read a page's title-block state (show flag + field data). Defaults to the focused page. */
const schematicTitleBlockGet: Handler = async (payload) => {
	const pageUuid = optionalString(payload, 'pageUuid');
	let info;
	try {
		info = pageUuid
			? await eda.dmt_Schematic.getSchematicPageInfo(pageUuid)
			: await eda.dmt_Schematic.getCurrentSchematicPageInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic page title block.');
	}
	if (!info) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'No schematic page found (open a page, or pass a valid pageUuid).');
	}
	return {
		result: {
			pageUuid: info.uuid,
			name: info.name,
			parentSchematicUuid: info.parentSchematicUuid,
			showTitleBlock: info.showTitleBlock,
			titleBlockData: info.titleBlockData,
		},
	};
};

/** 明细表单项的可写子字段 —— 与官方 modifySchematicPageTitleBlock 入参结构一致。 */
type TitleBlockPatch = { showTitle?: boolean; showValue?: boolean; value?: unknown };

/**
 * 逐子字段判定一个明细项是否已落到位。未请求的子字段不参与判定。
 *
 * `value` 比对经 String() 归一化:平台会把数字回读成字符串(反之亦然),
 * 那是格式归一化而非丢弃,不能算 notApplied(同 #151 的 number→string 教训)。
 */
function titleBlockFieldApplied(actual: TitleBlockPatch | undefined, want: TitleBlockPatch): boolean {
	if (!actual || typeof actual !== 'object') return false;
	if (want.value !== undefined && String(actual.value ?? '') !== String(want.value)) return false;
	if (want.showTitle !== undefined && actual.showTitle !== want.showTitle) return false;
	if (want.showValue !== undefined && actual.showValue !== want.showValue) return false;
	return true;
}

/** 读当前聚焦页的明细表状态;读不到返回 undefined(调用方降级为 verified:false)。 */
async function readFocusedTitleBlock(): Promise<
	{ showTitleBlock?: boolean; titleBlockData: Record<string, TitleBlockPatch> } | undefined
> {
	try {
		const info = await eda.dmt_Schematic.getCurrentSchematicPageInfo();
		if (!info) return undefined;
		return {
			showTitleBlock: info.showTitleBlock,
			titleBlockData: (info.titleBlockData ?? {}) as Record<string, TitleBlockPatch>,
		};
	}
	catch {
		return undefined;
	}
}

/**
 * Modify the focused page's 明细表 (title block): toggle visibility and/or patch
 * fields.
 *
 * **平台契约(官方 @beta remarks 原文)**:「任何无法识别的明细项将被忽略」,且
 * 「如若存在无法识别的明细项但程序并未出错,将返回 `true` 的结果」——
 * **这个 API 对写不进去的字段返回成功**,与 platform-delete-lies 同族。
 * 早期直接透传 `ok` 的实现因此会把「改了个根本不存在的明细项」报成成功;
 * audit log 实测该 action 32 次调用 0 次成功(20 次真抛错 + 12 次无连接),
 * 失败 payload 是拿 Size/Width/Height 当纸张属性写 —— 那些不是明细项,
 * 而旧实现既不能证伪也说不出原因。
 *
 * 因此这里走 #151 的三态契约:改前快照 → 写 → 回读逐项比对,产出
 * applied / alreadySet / notApplied / unknownKeys。`unknownKeys` 是本 action
 * 特有的诊断:改前明细表里就没有的 key,直接告诉调用方「这不是明细项」。
 *
 * 平台限制:官方签名无 pageUuid 参数,**只能改当前聚焦页**(titleblock.get 反而
 * 支持 pageUuid,两者不对称)。调用前请自行确认聚焦页是目标页。
 */
/**
 * 明细表(标题栏)里**不许写**的键 —— 图纸的结构与平台投影,不是给人填的文本。
 *
 * 为什么是黑名单而不是白名单:**文本项的键名由图框模板决定**,不是固定集合
 * (实测默认 A4 用 `Name`/`Drawed`,另一些模板用 `Title`/`Designer`)。用白名单
 * 会把自定义图框的合法字段全拒掉。反过来,下面这些结构键是 EasyEDA 图框
 * **数据模型**的固有部分,与模板无关,可以稳定枚举。
 *
 * 真机字段分类(EasyEDA 3.2.x,`titleblock.get` 33 项):
 *   - 图框身份:`Device` / `Symbol`(值是符号**名**如 "Drawing-Symbol_A4")、`ID`
 *   - 纸张几何:`Size` / `Page Size` / `Width` / `Height` / `Blade Width` /
 *     `Region Start` / `X Region Count` / `Y Region Count` / `Title Block Position`
 *   - 开关:`Border` / `Title Block` / `Color`
 *   - `@` 前缀:平台自动投影(页名/页号/工程名/创建时间…),只读,写了被丢弃
 *
 * 写前两类会**损毁文档**(#186 真机复现:符号名被灌进 sheet 的 component/device/
 * symbol UUID 引用位 → EasyEDA 报「器件/符号属性有误」→ 保存后重启拒载 = 图框丢失)。
 */
const TITLE_BLOCK_STRUCTURAL_FIELDS: ReadonlySet<string> = new Set([
	'Device', 'Symbol', 'ID',
	'Size', 'Page Size', 'Width', 'Height', 'Blade Width',
	'Region Start', 'X Region Count', 'Y Region Count', 'Title Block Position',
	'Border', 'Title Block', 'Color',
]);

/** `@` 前缀是平台自动投影的只读项,与上表同样不许下发。 */
function isTitleBlockStructuralKey(key: string): boolean {
	return key.startsWith('@') || TITLE_BLOCK_STRUCTURAL_FIELDS.has(key);
}

export const schematicTitleBlockModify: Handler = async (payload) => {
	const showTitleBlock = optionalBoolean(payload, 'showTitleBlock');
	const titleBlockData = payload.titleBlockData;
	if (
		titleBlockData !== undefined
		&& (typeof titleBlockData !== 'object' || titleBlockData === null || Array.isArray(titleBlockData))
	) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Field "titleBlockData" must be an object.');
	}
	if (showTitleBlock === undefined && titleBlockData === undefined) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Pass at least one of "showTitleBlock" or "titleBlockData".');
	}
	const requested = (titleBlockData ?? {}) as Record<string, TitleBlockPatch>;

	// 改前快照:平台静默忽略不认识的明细项,「哪些 key 本来就存在」「哪些本来
	// 就等于期望值」是区分 applied / alreadySet / unknownKeys 的唯一依据。
	// 它同时是下面「结构键有没有被改动」判定的基准。
	const before = await readFocusedTitleBlock();

	// #186:只把**图签文本字段**下发给平台,结构/投影键一律不传。
	//
	// 真机复现(EasyEDA 3.2.186,社区 issue #186):调用方按最自然的用法——
	// `titleblock.get` 拿完整 titleBlockData → 只改 `Name` → 整包传回 `modify`
	// ——平台把 `Device`/`Symbol` 的 value(`"Drawing-Symbol_A4"`,那是**符号的
	// 名字**)写进了 sheet 的 component/device/symbol **UUID 引用位**,
	// `Border`/`Title Block` 从 1 变 0,EasyEDA 当场报「器件/符号属性有误」,
	// 保存后重启即拒载 = **图框丢失**。这与 attrs 回填那次把库占位 Designator
	// 灌进 otherProperty 是同一类事故:**读回来的投影字段不许原样写回去**。
	//
	// 所以这里用**白名单**(而不是黑名单):明细表里未知的新键风险不可预估,
	// 宁可拒绝也不试写。要改纸张尺寸/边框/图框符号,那不是明细项,得走各自的
	// 专用路径(换图框走 prim-delete --allow-sheet + place)。
	const writableKeys = Object.keys(requested).filter(k => !isTitleBlockStructuralKey(k));
	const structuralKeys = Object.keys(requested).filter(isTitleBlockStructuralKey);
	// 结构键分两种:值与当前一致 = 调用方只是把 get 的结果原样带回来了(无意改动,
	// 静默丢弃即可);值不一致 = 真的想改它,那是会损毁文档的操作,**零变异拒绝**。
	const attemptedStructuralEdits = structuralKeys.filter(
		k => !titleBlockFieldApplied(before?.titleBlockData?.[k], requested[k]),
	);
	if (attemptedStructuralEdits.length > 0) {
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			`明细表 modify 拒绝改这些非文本字段: ${attemptedStructuralEdits.join(', ')}。`
			+ '它们是图纸结构/平台投影字段(图框符号、纸张尺寸、边框开关、@自动字段),'
			+ '写它们会把符号名灌进图框的 UUID 引用位 → EasyEDA 报「器件/符号属性有误」,'
			+ '保存后重启拒载 = 图框丢失(#186)。**本次一个字节都没写。**'
			+ '明细项(图签上给人填的文本,键名随图框模板而异)照常可改。'
			+ '换图框/改纸张请走 `sch prim-delete --allow-sheet` + `sch place` 重放图框符号。',
		);
	}
	if (writableKeys.length === 0 && showTitleBlock === undefined) {
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			'没有可写的明细项:传入的 key 全是结构/投影字段(已按 #186 过滤),且未传 showTitleBlock。'
			+ '先跑 `sch titleblock-get` 看本页有哪些明细项。本次未做任何修改。',
		);
	}
	// 下发给平台的只有白名单键 —— 结构键连传都不传(传了就会被写)。
	const wanted: Record<string, TitleBlockPatch> = {};
	for (const k of writableKeys) wanted[k] = requested[k];
	const wantedKeys = writableKeys;
	const ignoredKeys = structuralKeys;

	let ok;
	try {
		ok = await eda.dmt_Schematic.modifySchematicPageTitleBlock(
			showTitleBlock,
			wanted as Parameters<typeof eda.dmt_Schematic.modifySchematicPageTitleBlock>[1],
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to modify schematic page title block.');
	}
	if (ok === false) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			'EasyEDA rejected the title-block modify (returned false).',
		);
	}

	// 写后回读必须**等它落定**:平台提交明细表是异步的,写完立刻读会读到旧值。
	//
	// #186 复验实测:一次把 `Name` 写成 "TB-BOOL-TEST" 的调用,回执报
	// `nothing was applied: Name`(硬失败),而三秒后再读,值就好端端在那儿 ——
	// 也就是**成功的写被报成了失败**。这条误报的代价不小:它让「图签写不进去」
	// 成了流程里的既定结论(design-flow 因此禁用图签写入、`gate --strict` 的
	// missing-titleblock 变成结构性不可达),而事实并非如此。
	//
	// 所以这里轮询回读:一旦所有请求项都对上就立刻返回(常见路径零额外延迟),
	// 否则短暂退避重试。仍然对不上才判 notApplied —— 那时它是真的没生效。
	let after = await readFocusedTitleBlock();
	for (let attempt = 0; attempt < 4 && after; attempt++) {
		const settled = wantedKeys.every(key => titleBlockFieldApplied(after!.titleBlockData[key], wanted[key]))
			&& (showTitleBlock === undefined || after.showTitleBlock === showTitleBlock);
		if (settled) break;
		await new Promise<void>(resolve => setTimeout(resolve, 250));
		const retry = await readFocusedTitleBlock();
		if (!retry) break;
		after = retry;
	}
	if (!after) {
		// 写调用已返回成功但回读不可用:画布可能已变,绝不降级成 ok:false
		// (那会丢掉 autosave)。如实报 verified:false 交调用方判断(#151 同款)。
		return {
			result: { ok: true, verified: false, requestedKeys: wantedKeys, ...(ignoredKeys.length ? { ignoredKeys } : {}) },
			warnings: ['明细表已下发但回读不可用,无法验证是否真的写入(verified:false)。'],
		};
	}

	const visibilityApplied = showTitleBlock === undefined || after.showTitleBlock === showTitleBlock;
	// 改前不存在的 key = 平台不认识的明细项(官方 remarks 明说会被忽略)。
	// 单列出来,因为它的修法是「换个 key」而不是「重试」。
	const unknownKeys = before ? wantedKeys.filter(key => !(key in before.titleBlockData)) : [];
	const notApplied = wantedKeys.filter(key => !titleBlockFieldApplied(after.titleBlockData[key], wanted[key]));
	// 回读命中的 key 再按 before 二分:改前就等于期望值的无法证明本次写入
	// (写入同值与被丢弃回读不可区分),归 alreadySet — 不计 applied,
	// 也不豁免下面的全失败硬门(#151 review 的结论)。
	const alreadySet = before
		? wantedKeys.filter(key =>
			!notApplied.includes(key) && titleBlockFieldApplied(before.titleBlockData[key], wanted[key]))
		: [];
	const applied = wantedKeys.filter(key => !notApplied.includes(key) && !alreadySet.includes(key));

	// 「有请求项没落地」与「无一项可证明写入」是两个独立判断:前者决定要不要
	// 报 partial,后者决定这是不是一次彻头彻尾的假成功。alreadySet 不算证据
	// (写入同值与被丢弃回读不可区分),所以它不参与 nothingProven。
	const somethingFailed = notApplied.length > 0 || !visibilityApplied;
	const nothingProven = applied.length === 0 && !(showTitleBlock !== undefined && visibilityApplied);
	if (somethingFailed && nothingProven) {
		// 画布确实没变,假成功必须报错(回读铁律)。
		const failed = [...notApplied, ...(visibilityApplied ? [] : ['showTitleBlock'])];
		const hint = unknownKeys.length > 0
			? ` 其中 ${unknownKeys.join(', ')} 不是本页的明细项(先跑 sch titleblock-get 看可用 key)。`
			: ' 先跑 sch titleblock-get 确认 key 拼写与大小写。';
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Title block modify returned success but nothing was applied: ${failed.join(', ')}.${hint}`,
		);
	}
	if (somethingFailed) {
		return {
			result: {
				ok: true,
				partial: true,
				verified: true,
				applied,
				alreadySet,
				notApplied,
				unknownKeys,
				visibilityApplied,
				...(ignoredKeys.length ? { ignoredKeys } : {}),
				titleBlockBefore: before?.titleBlockData ?? {},
			},
			warnings: [
				`明细表部分未生效: ${[...notApplied, ...(visibilityApplied ? [] : ['showTitleBlock'])].join(', ')}(平台静默忽略)。`
				+ (applied.length > 0 ? `已应用子集(${applied.join(', ')})已在画布并照常 autosave;` : '')
				+ (unknownKeys.length > 0
					? `${unknownKeys.join(', ')} 不是本页明细项 —— 明细表改不了纸张尺寸,先 sch titleblock-get 看可用 key。`
					: '先 sch titleblock-get 确认 key 拼写与大小写。'),
			],
		};
	}
	return {
		result: {
			ok: true,
			verified: true,
			applied,
			alreadySet,
			visibilityApplied,
			...(ignoredKeys.length ? { ignoredKeys } : {}),
			titleBlockBefore: before?.titleBlockData ?? {},
		},
	};
};

/** Create a new schematic page under a schematic document. */
const schematicPageCreate: Handler = async (payload) => {
	const schematicUuid = requireString(payload, 'schematicUuid');
	let uuid;
	try {
		uuid = await eda.dmt_Schematic.createSchematicPage(schematicUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to create schematic page.');
	}
	if (uuid === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to create schematic page (check schematicUuid).');
	}
	return { result: { pageUuid: uuid } };
};

/**
 * Rename a schematic page.
 *
 * Platform quirk (issue #55): `modifySchematicPageName` returns ok=true, but the
 * new name does NOT immediately show up in `getAllSchematicPagesInfo()` — the
 * platform's page-metadata cache only refreshes after some later write op fires,
 * so an immediate `doc ls` reads the STALE old name. This is the same family of
 * platform-async traps as the getState_Rotation echo (schematic-layout-conventions.md).
 *
 * We can't fix the platform, so we do a write-after read-back verification here:
 * retry `getAllSchematicPagesInfo()` a few times with small delays and confirm the
 * target page's name is actually the new value. Report the truth to the caller via
 * `verified` (+ a `warning` when it never settles) instead of blindly echoing ok.
 */
const schematicPageRename: Handler = async (payload) => {
	const pageUuid = requireString(payload, 'pageUuid');
	const name = requireString(payload, 'name');
	let ok;
	try {
		ok = await eda.dmt_Schematic.modifySchematicPageName(pageUuid, name);
	}
	catch (err) {
		throw edaError(err, 'Failed to rename schematic page.');
	}
	// Write-after self-verification: poll the page list until the new name lands,
	// so callers doing an immediate `doc ls` don't read the stale old name.
	const verified = await verifySchematicPageName(pageUuid, name);
	if (verified) {
		return { result: { ok, verified: true } };
	}
	const warning = '重命名已提交，但页面列表元数据尚未同步为新名（EasyEDA 平台异步缓存，issue #55）；'
		+ '请稍后重试或触发任意其他写操作后再用 doc ls 确认。';
	return {
		// result.warning 保留兼容旧调用方;顶层 warnings 让 CLI stderr
		// choke-point(#151)也能渲染,与 modify 的 verified:false 降级同款形状。
		result: { ok, verified: false, warning },
		warnings: [warning],
	};
};

/**
 * Poll `getAllSchematicPagesInfo()` up to a few times until the target page's name
 * equals `expected`. Returns true once observed, false if it never settles.
 * Best-effort: read errors are swallowed and treated as "not yet settled".
 */
async function verifySchematicPageName(pageUuid: string, expected: string): Promise<boolean> {
	const delays = [0, 120, 250, 500]; // ~0.87s worst case, small enough to stay snappy
	for (const wait of delays) {
		if (wait > 0) {
			await new Promise<void>(resolve => setTimeout(resolve, wait));
		}
		try {
			const pages = await eda.dmt_Schematic.getAllSchematicPagesInfo();
			const hit = pages.find(p => p.uuid === pageUuid);
			if (hit && hit.name === expected) return true;
		}
		catch {
			/* best-effort — treat as not-yet-settled and keep polling */
		}
	}
	return false;
}

/**
 * Wait for a just-opened schematic page's data to settle. `openDocument`
 * resolves as soon as the tab exists — BEFORE the page's primitives finish
 * (re)loading — so a read fired right after would sample a half-loaded page
 * (empty findings, stale mixed-page data — issue #67). The SDK exposes no
 * load-complete signal, so we poll the active page's component count and treat
 * two identical consecutive reads as settled. A non-empty stable count settles
 * immediately; a stable 0 only settles after the full delay window, so a page
 * mid-load (0 → N) is not mistaken for a genuinely empty page. Returns true if
 * it settled, false on timeout — best-effort, read errors keep polling.
 */
async function waitSchematicPageSettle(): Promise<boolean> {
	const delays = [0, 200, 300, 400, 500, 600]; // ~2s worst case
	let last: number | undefined;
	let sawStableEmpty = 0;
	for (const wait of delays) {
		if (wait > 0) {
			await new Promise<void>(resolve => setTimeout(resolve, wait));
		}
		let count: number | undefined;
		try {
			const comps = await eda.sch_PrimitiveComponent.getAll();
			count = Array.isArray(comps) ? comps.length : undefined;
		}
		catch {
			count = undefined; // treat as not-yet-settled, keep polling
		}
		if (count === undefined) continue;
		if (last !== undefined && last === count) {
			if (count > 0) return true;
			sawStableEmpty++;
			if (sawStableEmpty >= 2) return true; // stable-empty confirmed
		}
		last = count;
	}
	return false;
}

/** Delete a schematic page. */
const schematicPageDelete: Handler = async (payload) => {
	const pageUuid = requireString(payload, 'pageUuid');
	let ok;
	try {
		ok = await eda.dmt_Schematic.deleteSchematicPage(pageUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete schematic page.');
	}
	return { result: { ok } };
};

/** Rename a schematic document (the whole sheet, not a single page). */
const schematicRename: Handler = async (payload) => {
	const schematicUuid = requireString(payload, 'schematicUuid');
	const name = requireString(payload, 'name');
	let ok;
	try {
		ok = await eda.dmt_Schematic.modifySchematicName(schematicUuid, name);
	}
	catch (err) {
		throw edaError(err, 'Failed to rename schematic.');
	}
	return { result: { ok } };
};

// ─── Components ───────────────────────────────────────────────────────

// tagComponentPages attributes each component to its owning schematic page by
// visiting every page in turn (the EDA API exposes no per-component page accessor
// and getAll only takes a boolean allPages flag). It restores the originally
// active page before returning. Whole-project inventories require complete
// evidence; local page hints may remain best-effort.
async function tagComponentPages(requireComplete = false): Promise<Map<string, { pageUuid: string; pageName: string }>> {
	const byId = new Map<string, { pageUuid: string; pageName: string }>();
	const failures: string[] = [];
	let current: Awaited<ReturnType<typeof eda.dmt_SelectControl.getCurrentDocumentInfo>> | undefined;
	try {
		current = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (requireComplete && !current?.uuid) throw new Error('original active page is unavailable');
		const pages = await eda.dmt_Schematic.getAllSchematicPagesInfo();
		if (requireComplete && (!Array.isArray(pages) || !pages.length)) throw new Error('schematic page inventory is unavailable or empty');
		for (const page of pages) {
			// Per-page isolation: one unloadable page must not abort the others —
			// and, via the finally below, must never skip the foreground restore
			// (callers write to the PCB right after this; leaving a random
			// schematic page foregrounded would land those writes wrong).
			try {
				await eda.dmt_EditorControl.openDocument(page.uuid);
				if (requireComplete && (await eda.dmt_SelectControl.getCurrentDocumentInfo())?.uuid !== page.uuid) {
					throw new Error('page did not become active');
				}
				// getAll() with no allPages flag returns only the ACTIVE page's parts.
				const parts = await eda.sch_PrimitiveComponent.getAll();
				if (!Array.isArray(parts)) throw new Error('component inventory is unavailable');
				for (const c of parts) {
					byId.set(c.getState_PrimitiveId(), { pageUuid: page.uuid, pageName: page.name });
				}
			}
			catch (err) { failures.push(`page ${page.uuid}: ${String(err)}`); }
		}
	}
	catch (err) { failures.push(String(err)); }
	finally {
		// Restore the page the caller was on — unconditionally.
		try {
			if (current?.uuid) {
				await eda.dmt_EditorControl.openDocument(current.uuid);
				if (requireComplete && (await eda.dmt_SelectControl.getCurrentDocumentInfo())?.uuid !== current.uuid) {
					throw new Error('original page did not become active');
				}
			}
		}
		catch (err) { failures.push(`restore original page: ${String(err)}`); }
	}
	if (requireComplete && failures.length) {
		throw edaError(new Error(failures.join('; ')), 'Full-project component inventory is incomplete.');
	}
	return byId;
}

/** Fixed, read-only Designator geometry inventory for schematic source measurements. */
export const schematicDesignatorsList: Handler = async () => {
	try {
		const before = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (!before?.uuid) throw new Error('active schematic page identity unavailable');
		// The unscoped attribute getAll() can return [] on a populated page.
		// Enumerate only current-page parts, then query each parent explicitly.
		const components = await eda.sch_PrimitiveComponent.getAll(undefined, false);
		if (!Array.isArray(components)) throw new Error('current-page component inventory unavailable');
		const parts = components.filter(c => String(c.getState_ComponentType()) === 'part');
		const partIds = new Set<string>();
		const refs = new Set<string>();
		const attributeIds = new Set<string>();
		const designators: Array<Record<string, unknown>> = [];
		for (const part of parts) {
			const parentId = part.getState_PrimitiveId();
			const ref = part.getState_Designator();
			if (typeof parentId !== 'string' || !parentId.trim() || partIds.has(parentId)) throw new Error(`missing/duplicate part primitiveId: ${String(parentId)}`);
			if (typeof ref !== 'string' || !ref.trim() || refs.has(ref)) throw new Error(`missing/duplicate part designator: ${String(ref)}`);
			partIds.add(parentId); refs.add(ref);
			const attrs = await eda.sch_PrimitiveAttribute.getAll(parentId);
			if (!Array.isArray(attrs)) throw new Error(`Designator inventory unavailable for ${ref} (${parentId})`);
			const candidates = attrs.filter(a => a.getState_Key() === 'Designator');
			if (candidates.length !== 1) throw new Error(`${ref} (${parentId}) requires exactly one Designator attribute, got ${candidates.length}`);
			const attr = candidates[0];
			const id = attr.getState_PrimitiveId();
			const actualParent = attr.getState_ParentPrimitiveId();
			const value = attr.getState_Value();
			const visible = attr.getState_ValueVisible();
			if (typeof id !== 'string' || !id.trim() || attributeIds.has(id)) throw new Error(`${ref} has missing/duplicate Designator attribute ID`);
			if (actualParent !== parentId) throw new Error(`${ref} Designator parent mismatch: ${String(actualParent)}`);
			if (value !== ref) throw new Error(`${ref} Designator value mismatch: ${String(value)}`);
			if (visible !== true) throw new Error(`${ref} Designator is hidden or visibility is unknown`);
			attributeIds.add(id);
			const measured = await eda.sch_Primitive.getPrimitivesBBox([id]);
			const bbox = measured as { minX?: unknown; minY?: unknown; maxX?: unknown; maxY?: unknown } | null;
			if (!bbox || ![bbox.minX, bbox.minY, bbox.maxX, bbox.maxY].every(n => typeof n === 'number' && Number.isFinite(n))
				|| (bbox.maxX as number) <= (bbox.minX as number) || (bbox.maxY as number) <= (bbox.minY as number)) {
				throw new Error(`${ref} Designator has missing or invalid official bbox`);
			}
			designators.push({ id, parentId, key: 'Designator', value, visible: true,
				bbox: { minX: bbox.minX, minY: bbox.minY, maxX: bbox.maxX, maxY: bbox.maxY },
				source: 'eda.sch_PrimitiveAttribute.getAll(parentId)+eda.sch_Primitive.getPrimitivesBBox([attributeId])' });
		}
		const after = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (after?.uuid !== before.uuid) throw new Error('active schematic page changed during Designator measurement');
		designators.sort((a, b) => String(a.parentId).localeCompare(String(b.parentId)));
		return { result: { documentId: before.uuid, count: designators.length, designators } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Could not measure every current-page Designator.');
	}
};

export const schematicComponentsList: Handler = async (payload) => {
	const allPages = optionalBoolean(payload, 'allPages') === true;
	const includePins = optionalBoolean(payload, 'includePins') === true;
	// Geometry guards need real pins, not a full netlist compile before every wire.
	// Omitted nets stay null (unknown), never an invented floating-net fact.
	const includePinNets = optionalBoolean(payload, 'includePinNets') !== false;
	// getState_Component().uuid is a 16-char placed-instance id, while
	// schematic.component.place requires the 32-char device-library uuid.
	// Connectivity export opts into this hydration so an IR snapshot is
	// replayable instead of sending an instance id that makes create() hang.
	const includeDeviceIdentity = optionalBoolean(payload, 'includeDeviceIdentity') === true;
	const identityReadContext = includeDeviceIdentity ? await readResponseContext() : undefined;
	// A fail-closed, read-only preflight inventory. It is deliberately captured
	// before tagPages can cycle documents and always describes the page that was
	// active when the request started, even when allPages=true.
	const includeConnectivitySummary = optionalBoolean(payload, 'includeConnectivitySummary') === true;
	const connectivitySummary = includeConnectivitySummary
		? await readActivePageConnectivitySummary()
		: null;
	// includeBBox attaches each component's rendered extent {minX,minY,maxX,maxY}
	// (via eda.sch_Primitive.getPrimitivesBBox) so the agent / `sch layout-lint`
	// can reason about size, spacing, and overlap — mirrors pcb.components.list.
	const includeBBox = optionalBoolean(payload, 'includeBBox') === true;
	// includeWires attaches existing wire segments {x0,y0,x1,y1,net} so `sch
	// autoconnect` can hard-reject any candidate stub that would touch a foreign-net
	// wire — EasyEDA merges nets at an endpoint-on-wire junction, a silent short the
	// post-hoc DRC can't catch. See issue #64.
	const includeWires = optionalBoolean(payload, 'includeWires') === true;
	const includePagePrimitives = optionalBoolean(payload, 'includePagePrimitives') === true;
	if (includePagePrimitives && allPages) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'includePagePrimitives requires one active page.');
	// tagPages attributes each component to its owning page (pageUuid/pageName).
	// Opt-in because it briefly cycles the active page; autoconnect requests it so
	// its off-page error can point at the exact `doc switch` target.
	const tagPages = optionalBoolean(payload, 'tagPages') === true;
	// Tag pages BEFORE the main getAll so the active-page cycling doesn't disturb
	// the component set we ultimately serialize.
	const pageById = tagPages ? await tagComponentPages(allPages) : null;
	let components;
	try {
		components = await eda.sch_PrimitiveComponent.getAll(undefined, allPages);
	}
	catch (err) {
		throw edaError(err, 'Failed to list schematic components.');
	}

	// When pins are requested, also resolve each pin's CURRENT net from the
	// JSON-authoritative netlist (same source as schematic.read). This is the data
	// plane `sch autoconnect` needs to be idempotent: without a per-pin net it can't
	// tell "already connected to the target net" (skip) from "connected to a DIFFERENT
	// net" (conflict) from "floating" (new connect). See issue #50.
	let pinNetsByDesignator: Map<string, Map<string, string>> | null = null;
	// Designators that resolve to MORE THAN ONE distinct device identity across
	// the whole document (issue #136: cross-page designator collision). The
	// netlist is keyed by designator.pin DOCUMENT-wide, so a collided designator's
	// pin→net attribution is poisoned — autoconnect would misread "already
	// connected to <some other page's net>". For those we report net:null
	// (unknown) + netAmbiguous:true instead of a confidently-wrong net. Sub-parts
	// of one physical device (U1.A/U1.B) share the device identity and are NOT
	// flagged.
	const ambiguousDesignators = new Set<string>();
	// Whether the netlist behind every pin's `net` was actually fetched+parsed. When
	// it was not, `net` is FORCED to null on every pin of every component. Reporting
	// that flag is not cosmetic: a run of nulls used to read as "all these pins are
	// unconnected", which is a conclusion about the DESIGN drawn from a failed READ
	// (reported 2026-09-20: a reviewer concluded "net unreachable from the platform"
	// for block instances while the real cause was a muted netlist export).
	let pinNetsAvailable = false;
	let pinNetsError: string | undefined;
	if (includePins && includePinNets) {
		try {
			const collected = await collectNetlistPinNets();
			pinNetsByDesignator = collected.byDesignator;
			pinNetsAvailable = collected.available;
			pinNetsError = collected.error;
		}
		catch (err) {
			pinNetsByDesignator = null;
			pinNetsError = describeThrown(err);
		}
		if (pinNetsByDesignator) {
			try {
				const everywhere = allPages ? components : await eda.sch_PrimitiveComponent.getAll(undefined, true);
				const identByDesig = new Map<string, Set<string>>();
				for (const c of everywhere ?? []) {
					let type = '';
					try { type = String(c.getState_ComponentType?.() ?? ''); }
					catch { continue; }
					if (type !== 'part' && type !== '') continue;
					const d = String(c.getState_Designator?.() ?? '');
					if (!d) continue;
					let ident = '';
					try { ident = JSON.stringify(c.getState_Component?.() ?? '') || String(c.getState_Name?.() ?? ''); }
					catch { ident = ''; }
					const set = identByDesig.get(d) ?? new Set<string>();
					set.add(ident);
					identByDesig.set(d, set);
				}
				for (const [d, idents] of identByDesig) {
					if (idents.size > 1) ambiguousDesignators.add(d);
				}
			}
			catch { /* best-effort — no ambiguity info degrades to prior behavior */ }
		}
	}

	// Keep one fresh official source inventory per list request, never across
	// requests/pages. Individual identity resolution still validates its own entry.
	let nativeFootprintInventory: Promise<NativeFootprintInventory> | undefined;
	const getNativeFootprints = () => nativeFootprintInventory ??= loadNativeFootprintInventory(identityReadContext);
	// Request-local only: ref/position do not affect identity, but every resolver
	// input (including instance provenance and stable name) must match exactly.
	const identityCache = new Map<string, DeviceResolution>();
	const serialized: Array<Record<string, unknown>> = [];
	for (const component of components) {
		const record = serializeComponent(component);
		if (includeDeviceIdentity && record.componentType === 'part') {
			const rawDevice = record.device as Record<string, unknown> | undefined;
			const rawUuid = typeof rawDevice?.uuid === 'string' ? rawDevice.uuid : '';
			// A valid library uuid is already authoritative; only resolve the
			// suspicious instance-shaped identity to avoid needless API calls.
			if (rawUuid.length !== 32) {
				const identityKey = JSON.stringify([record.device, record.component, record.footprint, record.name, record.supplierId, record.manufacturerId]);
				let resolved = identityCache.get(identityKey);
				if (!resolved) {
					resolved = await resolvePlacedDevice(record, getNativeFootprints);
					identityCache.set(identityKey, resolved);
				}
				if (resolved.device) {
					record.placedDevice = rawDevice;
					record.device = {
						libraryUuid: resolved.device.libraryUuid,
						uuid: resolved.device.uuid,
						name: record.name ?? '',
					};
					record.deviceResolution = {
						via: resolved.device.via,
						...(resolved.device.via === 'lcsc-footprint-source' ? { sameFootprintUUID: true } : {}),
						...(resolved.footprintSource ? { footprintSource: resolved.footprintSource } : {}),
						...(resolved.lcsc ? { lcsc: resolved.lcsc } : {}),
						...(resolved.deviceFootprint ? { footprint: resolved.deviceFootprint } : {}),
					};
				} else {
					record.deviceIdentityError = resolved.reason ?? 'no exact library match';
					if (resolved.candidates?.length) record.deviceIdentityCandidates = resolved.candidates;
				}
			}
		}
		if (pageById) {
			const page = pageById.get(component.getState_PrimitiveId());
			if (page) {
				record.pageUuid = page.pageUuid;
				record.pageName = page.pageName;
			}
		}
		if (includeBBox) {
			try {
				const box = await eda.sch_Primitive.getPrimitivesBBox([component.getState_PrimitiveId()]);
				if (box) record.bbox = box;
			}
			catch { /* bbox is optional */ }
		}
		if (includePins) {
			try {
				const pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(
					component.getState_PrimitiveId(),
				);
				if (!Array.isArray(pins)) throw new Error('Pin API did not return an array.');
				const designator = String(component.getState_Designator?.() ?? '');
				const ambiguous = ambiguousDesignators.has(designator);
				if (ambiguous) record.netAmbiguous = true;
				const netByNumber = ambiguous ? null : (pinNetsByDesignator?.get(designator) ?? null);
				record.pins = pins.map((pin) => {
					const rec = serializePin(pin);
					// null (not '') distinguishes "known floating" from "netlist unavailable"
					// — and a cross-page-collided designator's nets are FORCED to null
					// (netAmbiguous) rather than confidently wrong (issue #136).
					// A MISSING netlist forces null for the same reason: with no netlist
					// there is no way to tell "no net" from "not read", so claiming a net
					// is unconnected would be a design conclusion drawn from a failed read.
					rec.net = netByNumber ? (netByNumber.get(String(rec.pinNumber ?? '')) ?? '') : null;
					return rec;
				});
				record.pinsAvailable = true;
			}
			catch (err) {
				record.pinsAvailable = false;
				record.pinsError = describeThrown(err);
			}
			if (includePins && includePinNets) {
				record.netlistAvailable = pinNetsAvailable;
				if (!pinNetsAvailable) record.netlistError = pinNetsError ?? 'netlist unavailable';
			}
		}
		serialized.push(record);
	}

	// Existing wire geometry for the autoconnect scorer (issue #64). Flatten every
	// wire's polyline into per-edge segments tagged with the wire's net, so the Go
	// side can hard-reject a stub that would touch a foreign-net wire.
	const wires: Array<{ x0: number; y0: number; x1: number; y1: number; net: string; primitiveId: string; segmentIndex: number; rawLine: number[] | number[][]; rawEncoding: string }> = [];
	let wiresAvailable = false;
	let wiresError: string | undefined;
	if (includeWires) {
		try {
			const rawWires = await eda.sch_PrimitiveWire.getAll();
			if (!Array.isArray(rawWires)) throw new Error('Wire API did not return an array.');
			for (const s of collectWireSegments(rawWires)) {
				wires.push({ x0: s.seg[0], y0: s.seg[1], x1: s.seg[2], y1: s.seg[3], net: s.net, primitiveId: s.wirePrimitiveId, segmentIndex: s.segmentIndex, rawLine: s.rawLine, rawEncoding: s.rawEncoding });
			}
			wiresAvailable = true;
		} catch (err) {
			wires.length = 0;
			wiresError = describeThrown(err);
		}
	}
	const pagePrimitives = includePagePrimitives ? await readSchPagePrimitiveState() : undefined;

	return {
		result: {
			components: serialized,
			count: serialized.length,
			wires,
			...(includeWires ? { wiresAvailable, ...(wiresError ? { wiresError } : {}) } : {}),
			...(includePagePrimitives ? { pagePrimitives } : {}),
			// Netlist trust flag: without it a caller cannot tell "every pin is
			// genuinely unconnected" from "the netlist read failed, so every pin's net
			// is null". Emitted only when the caller asked for pin nets at all.
			...(includePins && includePinNets
				? { pinNetsAvailable, ...(pinNetsAvailable ? {} : { pinNetsError: pinNetsError ?? 'netlist unavailable' }) }
				: {}),
			...(connectivitySummary ? { connectivitySummary } : {}),
		},
	};
};

/**
 * PROJECTED-STATE keys must NEVER be merged into a primitive's otherProperty
 * from a library record: the platform projects them from top-level primitive
 * state (designator, uniqueId, name, addIntoBom, manufacturerId, supplierId),
 * it does not store them in otherProperty. Two failure modes, both live-verified:
 *   - `Designator`: the library record carries its own PLACEHOLDER ("C?"), and
 *     the platform syncs otherProperty.Designator INTO the primitive's
 *     designator on modify — one sync-attrs run wiped 166/166 real designators
 *     to U?/C?/RF? (each part flipping to its own library's placeholder
 *     prefix); deterministic on a quiet 6-part board.
 *   - The rest: writes are silently DROPPED (never appear in the next
 *     getState_OtherProperty), so merging them re-"fills" the same keys every
 *     run — a lying report and wasted writes, never a real backfill.
 *
 * ONE ruler for both sides: the PCB `sync-attrs` backfill and the schematic
 * place-time backfill (#186) share this set. A second copy would be a second
 * ruler, and the failure it guards against is designator-wiping.
 */
export const PROJECTED_STATE_KEYS: ReadonlySet<string> = new Set([
	'Designator', 'Unique ID', 'Name', 'Add into BOM',
	'Manufacturer', 'Manufacturer Part', 'Supplier', 'Supplier Part',
]);

/**
 * Plan an otherProperty backfill: which library values may land on an instance.
 *
 * Pure so the rule is unit-testable without an EasyEDA runtime. Policy:
 *   - projected-state keys are skipped (see PROJECTED_STATE_KEYS);
 *   - empty/nullish library values are skipped (nothing to carry);
 *   - a key the instance already fills is left alone (never overwrite a real
 *     value — hand edits and later standardization both outrank the library);
 *   - `onlyExistingKeys` (the place-time default) additionally refuses to
 *     INTRODUCE keys the instance doesn't already carry. The platform copies
 *     the device's key structure at create time with empty values, so the
 *     existing key set is exactly "what this part is supposed to have" — and
 *     not inventing keys makes it structurally impossible to leak a library
 *     placeholder onto a fresh instance.
 * Also scrubs a stale placeholder `Designator` that an OLDER backfill leaked
 * in, since any future whole-otherProperty write would re-wipe the designator.
 */
export function planOtherPropertyBackfill(
	current: Record<string, unknown>,
	source: Record<string, unknown>,
	opts: { overwrite?: boolean; onlyExistingKeys?: boolean } = {},
): { merged: Record<string, unknown>; filled: Array<string> } {
	const { overwrite = false, onlyExistingKeys = false } = opts;
	const filled: Array<string> = [];
	const merged: Record<string, unknown> = { ...current };
	for (const [key, value] of Object.entries(source)) {
		if (PROJECTED_STATE_KEYS.has(key)) continue;
		if (value === undefined || value === null || value === '') continue;
		if (onlyExistingKeys && !(key in current)) continue;
		const existing = current[key];
		if (!overwrite && existing !== undefined && existing !== null && existing !== '') continue;
		if (existing === value) continue;
		merged[key] = value;
		filled.push(key);
	}
	if (typeof merged['Designator'] === 'string' && merged['Designator'].includes('?')) {
		delete merged['Designator'];
		filled.push('Designator (stale placeholder removed)');
	}
	return { merged, filled };
}

/**
 * #186: `sch_PrimitiveComponent.create` copies the device record's otherProperty
 * KEY STRUCTURE onto the instance but leaves every value empty — live-verified
 * on a C0805: the instance carries `Value` / `Tolerance` / `Voltage Rating` /
 * `Datasheet` / `Description` as keys, all `""`, while the device record has
 * `Value: "10uF"`, `Tolerance: "±10%"`, `Voltage Rating: "50V"`, … So the BOM's
 * value column and the 器件标准化 panel stayed empty until the PCB-side
 * `sync-attrs` pass filled them much later in the flow — even though the very
 * same record is in hand at place time.
 *
 * Backfills at place time under the shared rule (see planOtherPropertyBackfill),
 * restricted to keys the instance already carries. Best-effort: placement never
 * fails because a backfill did — same contract as backfillSupplierId (#157).
 */
async function backfillOtherProperty(
	component: SchComponent,
	device: { libraryUuid: string; uuid: string },
): Promise<{ component: SchComponent; filled?: Array<string>; warning?: string }> {
	let current: Record<string, unknown> = {};
	try { current = (component.getState_OtherProperty() as Record<string, unknown>) ?? {}; }
	catch { return { component }; }
	if (!Object.keys(current).length) return { component };

	let source: Record<string, unknown> = {};
	try {
		const item = await eda.lib_Device.get(device.uuid, device.libraryUuid) as
			{ property?: { otherProperty?: unknown } } | undefined;
		const op = item?.property?.otherProperty;
		if (op && typeof op === 'object') source = op as Record<string, unknown>;
	}
	catch { return { component }; }
	if (!Object.keys(source).length) return { component };

	const { merged, filled } = planOtherPropertyBackfill(current, source, { onlyExistingKeys: true });
	if (!filled.length) return { component };

	// Re-assert the identity fields in the SAME call. A whole-otherProperty write
	// re-projects top-level state from the library record, so anything already
	// backfilled onto the instance is reset unless it is restated here — both
	// failures are live-verified:
	//   - `designator`: the 166/166 wipe to U?/C?/RF? described above;
	//   - `supplierId`: dropping it undid #157 — the instance fell back to the
	//     platform default `<MPN>.1`, which is exactly the unorderable BOM value
	//     #157 exists to prevent. Caught by reading the instance back after the
	//     first build of this backfill; the response had claimed success.
	let designator = '';
	try { designator = String(component.getState_Designator() ?? ''); }
	catch { /* optional */ }
	let supplierId = '';
	try { supplierId = String(component.getState_SupplierId() ?? ''); }
	catch { /* optional */ }
	try {
		const m = await eda.sch_PrimitiveComponent.modify(component.getState_PrimitiveId(), {
			...(designator ? { designator } : {}),
			...(/^C\d+$/.test(supplierId) ? { supplierId } : {}),
			otherProperty: merged as Record<string, string | number | boolean>,
		});
		if (m) return { component: m, filled };
	}
	catch { /* fall through */ }
	return {
		component,
		warning: `otherProperty backfill (${filled.join(', ')}) failed — the instance keeps the platform's empty values; `
			+ 'the PCB-side `sync-attrs` pass will still fill them later.',
	};
}

/**
 * #157: sch_PrimitiveComponent.create defaults the placed instance's supplierId
 * to `<MPN>.1` (the subPartName), NOT the device's real LCSC C-number — which
 * flags every part in the official 器件标准化 panel and makes the BOM's
 * Supplier Part column unorderable. Backfill the REAL C-number from the
 * device-library record right after create. Only a `^C\d+$` value from the
 * device is written (outsourced placeholder devices without a C-number are
 * left untouched), and only when the instance doesn't already carry one.
 * Best-effort: placement itself never fails on a backfill problem.
 */
async function backfillSupplierId(
	component: SchComponent,
	device: { libraryUuid: string; uuid: string },
): Promise<{ component: SchComponent; backfilled?: string; warning?: string }> {
	let current = '';
	try { current = String(component.getState_SupplierId() ?? ''); }
	catch { /* ignore */ }
	if (/^C\d+$/.test(current)) return { component };
	let real = '';
	try {
		const item = await eda.lib_Device.get(device.uuid, device.libraryUuid) as
			{ property?: { supplierId?: unknown } } | undefined;
		real = String(item?.property?.supplierId ?? '');
	}
	catch { /* fall through */ }
	if (!/^C\d+$/.test(real)) return { component };
	try {
		const m = await eda.sch_PrimitiveComponent.modify(component.getState_PrimitiveId(), { supplierId: real });
		if (m) return { component: m, backfilled: real };
	}
	catch { /* fall through */ }
	return {
		component,
		warning: `supplierId backfill to ${real} failed — the instance keeps the platform default (subPartName); fix with \`sch modify --patch '{"supplierId":"${real}"}'\`.`,
	};
}

export const schematicComponentPlace: Handler = async (payload) => {
	const libraryUuid = requireString(payload, 'libraryUuid');
	const uuid = requireString(payload, 'uuid');
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const subPartName = optionalString(payload, 'subPartName');
	const rotation = optionalNumber(payload, 'rotation');
	const mirror = optionalBoolean(payload, 'mirror');
	const addIntoBom = optionalBoolean(payload, 'addIntoBom');
	const addIntoPcb = optionalBoolean(payload, 'addIntoPcb');
	const designator = optionalString(payload, 'designator');

	// Read-only V4 model guard must run before sch_PrimitiveComponent.create.
	await preflightV4DeviceVariant(libraryUuid, uuid);

	let component;
	try {
		component = await eda.sch_PrimitiveComponent.create(
			{ libraryUuid, uuid },
			x,
			y,
			subPartName,
			rotation,
			mirror,
			addIntoBom,
			addIntoPcb,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to place component.');
	}
	if (!component) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Component placement returned no primitive.');
	}

	// Atomically assign the final designator on the connector side so batch
	// placement skips the place→list→modify round-trip (issue #68). The freshly
	// placed component keeps a placeholder designator (e.g. U?/C?); the modify
	// return is the authoritative post-assignment state, so overwrite the local
	// reference with it before serializing.
	if (designator) {
		const primitiveId = component.getState_PrimitiveId();
		let modified;
		try {
			modified = await eda.sch_PrimitiveComponent.modify(primitiveId, { designator });
		}
		catch (err) {
			throw edaError(err, `Placed component "${primitiveId}" but failed to assign designator "${designator}".`);
		}
		if (!modified) {
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Placed component "${primitiveId}" but designator assignment "${designator}" returned no primitive.`,
			);
		}
		component = modified;
	}

	// #157: carry the device's real LCSC C-number onto the instance.
	const backfill = await backfillSupplierId(component, { libraryUuid, uuid });
	component = backfill.component;

	// #186: carry the device's attribute VALUES (Value / Tolerance / …) onto the
	// instance — create copies their keys but leaves them empty. Runs after the
	// designator assignment above so the re-assert writes the real designator.
	const attrs = await backfillOtherProperty(component, { libraryUuid, uuid });
	component = attrs.component;

	const warnings = [backfill.warning, attrs.warning].filter((w): w is string => Boolean(w));
	return {
		result: {
			primitiveId: component.getState_PrimitiveId(),
			component: serializeComponent(component),
			...(backfill.backfilled ? { supplierIdBackfilled: backfill.backfilled } : {}),
			...(attrs.filled?.length ? { otherPropertyBackfilled: attrs.filled.sort() } : {}),
		},
		...(warnings.length ? { warnings } : {}),
	};
};

type SchematicPropertyValue = string | number | boolean;

/**
 * eda.sch_PrimitiveComponent.modify 接受的全部顶层 patch 键 — 逐字抄自
 * @jlceda/pro-api-types 的 modify 签名(14 键),外加本连接器的兼容别名
 * customAttributes(下方归一化为 otherProperty)。SDK 对未知顶层键**静默丢弃**
 * (#150/#151 根因),事后回读无从归因,所以在任何 eda.* 调用之前就拒绝
 * (#120 前置拒绝范式:零变异、精确归因)。
 */
const SCH_MODIFY_PATCH_KEYS: ReadonlySet<string> = new Set([
	'x', 'y', 'rotation', 'mirror', 'addIntoBom', 'addIntoPcb',
	'designator', 'name', 'uniqueId', 'manufacturer', 'manufacturerId',
	'supplier', 'supplierId', 'otherProperty',
	'customAttributes',
]);

/**
 * 逐字段回读判定:平台会把数字属性规范化成字符串(10 → "10"),String() 强转
 * 容忍比较避免把规范化误判成未应用(false-partial)。缺键恒为未应用。
 */
function propertyApplied(
	actual: Record<string, SchematicPropertyValue>,
	key: string,
	expected: SchematicPropertyValue,
): boolean {
	if (!(key in actual)) return false;
	const got = actual[key];
	return got === expected || String(got) === String(expected);
}

/**
 * 校验原理图器件自定义属性补丁，避免 SDK 静默忽略不支持的嵌套值。
 */
function requireSchematicPropertyPatch(
	value: unknown,
	field: 'customAttributes' | 'otherProperty',
): Record<string, SchematicPropertyValue> {
	if (typeof value !== 'object' || value === null || Array.isArray(value)) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Component patch field "${field}" must be an object.`,
		);
	}
	const out: Record<string, SchematicPropertyValue> = {};
	for (const [key, item] of Object.entries(value)) {
		if (typeof item !== 'string' && typeof item !== 'number' && typeof item !== 'boolean') {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Component property "${field}.${key}" must be a string, number, or boolean.`,
			);
		}
		out[key] = item;
	}
	return out;
}

export const schematicComponentModify: Handler = async (payload) => {
	const primitiveId = requireString(payload, 'primitiveId');
	const patch = payload.patch;
	if (typeof patch !== 'object' || patch === null || Array.isArray(patch)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required object field "patch".');
	}

	// 未知顶层键在任何 eda.* 调用前拒绝:SDK 会静默丢弃它们,回读也无从
	// 区分「键不支持」和「值被平台拒绝」;前置拒绝是零变异的精确失败(#151)。
	const unknownKeys = Object.keys(patch as Record<string, unknown>)
		.filter(key => !SCH_MODIFY_PATCH_KEYS.has(key));
	if (unknownKeys.length > 0) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Unknown component patch field(s): ${unknownKeys.join(', ')}. `
			+ `Allowed: ${[...SCH_MODIFY_PATCH_KEYS]
				.map(k => k === 'customAttributes' ? 'customAttributes (alias of otherProperty — use one, not both)' : k)
				.join(', ')}.`,
		);
	}

	const normalizedPatch = { ...(patch as Record<string, unknown>) };
	if (optionalBoolean(payload, 'preserveInstance') === true) {
		if (Object.keys(normalizedPatch).some(k => !['x', 'y', 'rotation', 'mirror'].includes(k))) {
			throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'preserveInstance permits geometry-only patches; instance properties cannot be changed.');
		}
		const current = await getComponentOrThrow(primitiveId);
		let source: Record<string, unknown>;
		try { source = preservedInstance(serializeComponent(current)); }
		catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, describeThrown(err)); }
		const safePatch = { ...normalizedPatch };
		for (const key of ['designator', 'uniqueId', 'name', 'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'addIntoBom', 'addIntoPcb', 'otherProperty']) safePatch[key] = source[key];
		let modified;
		try { modified = await eda.sch_PrimitiveComponent.modify(primitiveId, safePatch as Parameters<typeof eda.sch_PrimitiveComponent.modify>[1]); }
		catch (err) { throw edaError(err, 'Failed to move preserved component.'); }
		if (!modified) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Preserved component modify returned no instance.');
		try {
			const fresh = await getComponentOrThrow(primitiveId);
			const actual = preservedInstance(serializeComponent(fresh));
			const notApplied = Object.keys(source).filter(k => exactJSON(source[k]) !== exactJSON(actual[k]));
			return { result: { component: serializeComponent(fresh), instancePreserved: notApplied.length === 0,
				...(notApplied.length ? { partial: true, notApplied, instanceBefore: source } : {}) } };
		}
		catch (err) {
			return { result: { component: serializeComponent(modified), instancePreserved: false, verified: false, partial: true,
				notApplied: ['instance-readback'], instanceBefore: source }, warnings: [describeThrown(err)] };
		}
	}
	const hasCustomAttributes = Object.prototype.hasOwnProperty.call(normalizedPatch, 'customAttributes');
	const hasOtherProperty = Object.prototype.hasOwnProperty.call(normalizedPatch, 'otherProperty');
	if (hasCustomAttributes && hasOtherProperty) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Use either "customAttributes" or "otherProperty" in one component patch, not both.',
		);
	}

	let expectedProperties: Record<string, SchematicPropertyValue> | undefined;
	let propertiesBefore: Record<string, SchematicPropertyValue> | undefined;
	let preservedPropertyKeys: Array<string> | undefined;
	if (hasCustomAttributes || hasOtherProperty) {
		const field = hasCustomAttributes ? 'customAttributes' : 'otherProperty';
		expectedProperties = requireSchematicPropertyPatch(normalizedPatch[field], field);

		// EasyEDA SDK 只接受 otherProperty，而且会整体替换该对象；先合并现有值，
		// 使 CLI 文档中的 customAttributes 别名可用，同时避免修改 Value 时清空其他属性。
		// before 快照同时兑现审计 before/after 约定,部分应用时随 result 返回。
		const current = await getComponentOrThrow(primitiveId);
		propertiesBefore = cleanOtherProperty(
			current.getState_OtherProperty() as Record<string, unknown> | undefined,
		) ?? {};
		normalizedPatch.otherProperty = { ...propertiesBefore, ...expectedProperties };
		delete normalizedPatch.customAttributes;
	}
	else {
		// #175: 平台 modify 对 otherProperty 是整体重写语义 — patch 不带
		// otherProperty 时,平台会把现有自定义属性**整体清空**(实测:166 件填好
		// Value 后单独 patch supplierId,Value 全被清成 "";ok=true 无任何告警)。
		// read-preserve-write:回读现有自定义属性并在同一次 modify 里原样写回,
		// 让「merge 语义」对顶层字段补丁同样成立。现有属性为空时不加
		// otherProperty 键(无数据可保,也不做无谓的整体写 — 见 attrs_backfill
		// 处对整体写 otherProperty 平台副作用的实测记录)。
		const current = await getComponentOrThrow(primitiveId);
		const existing = cleanOtherProperty(
			current.getState_OtherProperty() as Record<string, unknown> | undefined,
		);
		if (existing) {
			propertiesBefore = existing;
			preservedPropertyKeys = Object.keys(existing);
			normalizedPatch.otherProperty = { ...existing };
		}
	}

	let component;
	try {
		component = await eda.sch_PrimitiveComponent.modify(
			primitiveId,
			normalizedPatch as Parameters<typeof eda.sch_PrimitiveComponent.modify>[1],
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to modify component.');
	}
	if (!component) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to modify component "${primitiveId}".`);
	}

	if (expectedProperties || preservedPropertyKeys) {
		// SDK 某些字段会返回成功但静默 no-op；必须用新句柄回读实际画布状态。
		// modify 已成功返回 ⇒ 画布可能已变;此后回读通道自身的失败绝不能再变成
		// ok:false —— daemon 只对 ok:true 排 autosave,抛错会让已落画布的变更
		// 只留内存、reload 即丢(issue #151)。回读失败重试一次后降级为
		// verified:false + warning(schematicPageRename 同款先例)。
		let fresh: SchComponent | undefined;
		try {
			fresh = await getComponentOrThrow(primitiveId);
		}
		catch { /* transient — retry once below */ }
		if (!fresh) {
			await new Promise<void>(resolve => setTimeout(resolve, 250));
			try {
				fresh = await getComponentOrThrow(primitiveId);
			}
			catch { /* fall through to verified:false */ }
		}
		if (!fresh) {
			return {
				result: {
					component: serializeComponent(component),
					verified: false,
					// 恰是最需要 before 快照支撑恢复的场景(画布状态未经验证)
					propertiesBefore: propertiesBefore ?? {},
				},
				warnings: [
					`修改已提交,但回读校验组件 "${primitiveId}" 失败(重试一次仍失败);`
					+ '画布状态未经逐字段验证,请用 schematic.component.get 复核(issue #151)。',
				],
			};
		}
		const actual = cleanOtherProperty(
			fresh.getState_OtherProperty() as Record<string, unknown> | undefined,
		) ?? {};
		const before = propertiesBefore ?? {};
		if (!expectedProperties) {
			// #175 preserve-only:patch 未动自定义属性,只需验证保留写回没弄丢键。
			// 丢键 = 静默数据丢失,绝不许无声通过:复用 partial/notApplied 结构
			// (CLI `sch modify` 对 notApplied 非空非零退出,错误信号不丢),
			// propertiesBefore 支撑重放恢复。
			const lost = Object.keys(before).filter(key => !propertyApplied(actual, key, before[key]));
			if (lost.length > 0) {
				return {
					result: {
						component: serializeComponent(fresh),
						partial: true,
						notApplied: lost,
						propertiesBefore: before,
					},
					warnings: [
						`组件 "${primitiveId}" 顶层字段补丁已生效,但自定义属性保留写回后仍丢失: ${lost.join(', ')}`
						+ '(平台 modify 整体重写 otherProperty,写回未被接受);'
						+ '重放 result.propertiesBefore 可恢复丢失键(issue #175)。',
					],
				};
			}
			return {
				result: {
					component: serializeComponent(fresh),
					// 显式回报整体重写中被原样保留的键 + before 快照(审计约定):
					// 调用方能看见「哪些属性被这次顶层补丁连带重写过」,不再有静默面。
					propertiesPreserved: preservedPropertyKeys,
					propertiesBefore: before,
				},
			};
		}
		const expectedKeys = Object.keys(expectedProperties);
		const notApplied = expectedKeys
			.filter(key => !propertyApplied(actual, key, expectedProperties[key]));
		// 回读命中的键按 before 快照再二分:before 本就等于期望值的键无法证明
		// 本次写入(SDK 丢弃与写入同值回读不可区分),归入 alreadySet,不计入
		// applied、也不豁免全失败硬门 —— 否则「一个已满足键 + 其余全丢」会绕过
		// #150 的假成功检测。
		const alreadySet = expectedKeys.filter(key =>
			!notApplied.includes(key) && propertyApplied(before, key, expectedProperties[key]));
		const applied = expectedKeys.filter(key =>
			!notApplied.includes(key) && !alreadySet.includes(key));
		const patchedBeyondProperties = Object.keys(normalizedPatch).some(key => key !== 'otherProperty');
		if (notApplied.length > 0 && applied.length === 0 && !patchedBeyondProperties) {
			// 纯属性 patch 且无一可证明写入:画布确未变(alreadySet 键本来就是
			// 期望值),假成功必须报错(回读铁律,此时 ok:false 不 arm autosave
			// 是正确行为 — 没有东西需要落盘)。
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Component "${primitiveId}" modify returned success but did not apply properties: ${notApplied.join(', ')}.`,
			);
		}
		if (notApplied.length > 0) {
			// 部分应用:已写进画布的子集是既成事实,报结构化成功(ok:true)让
			// daemon 照常 arm autosave 落盘;调用方从 notApplied/warnings 拿到
			// 未生效清单(同文件 set_no_connect #134 的 notApplied 同款语义)。
			// CLI 侧 `sch modify` 对 notApplied 非空非零退出,错误信号不丢。
			// merge 语义删不掉键:重放 propertiesBefore 只能恢复被覆盖键的原值,
			// 本次新增键(addedKeys)须编辑器手工删除 — 文案如实说明,不谎报可恢复。
			const addedKeys = applied.filter(key => !(key in before));
			return {
				result: {
					component: serializeComponent(fresh),
					partial: true,
					applied,
					alreadySet,
					notApplied,
					addedKeys,
					propertiesBefore: before,
				},
				warnings: [
					`组件 "${primitiveId}" 部分属性未生效: ${notApplied.join(', ')}(SDK 静默忽略)。`
					+ (applied.length > 0
						? `已应用子集(${applied.join(', ')})已保留在画布并照常 autosave;`
						: '属性均未生效,但几何/元数据 patch 可能已生效(照常 autosave);')
					+ '重放 result.propertiesBefore 仅恢复被覆盖键的原值'
					+ (addedKeys.length > 0
						? `,本次新增键(${addedKeys.join(', ')})无法经 modify 移除,需编辑器手工删除`
						: '')
					+ '(issue #151)。',
				],
			};
		}
		component = fresh;
	}
	return {
		result: {
			component: serializeComponent(component),
			// 审计 before/after:属性修改的 before 快照随全量成功一并返回
			// (纯几何 patch 无属性修改,不引入空对象噪音)。
			...(propertiesBefore !== undefined ? { propertiesBefore } : {}),
		},
	};
};

// ─── Component-delete cascade (ADR-0004 Decision 5) ──────────────────────
//
// Deleting a part leaves its stub wires + netflags behind; the residue is a
// ghost-connection boobytrap — the next part placed there silently inherits
// the stray net name (v1.0.1's orphan-tree rule only catches it AFTER the
// fact). The cascade removes each EXCLUSIVE wire tree (touches only the
// deleted parts' pins) plus every net marker anchored on it. A tree that
// still touches any SURVIVING part's pin is shared: never deleted.

/** One planned cascade tree: its wires, the markers riding it, and which of
 *  the delete-target components touch it (owners). */
export interface SchDeleteCascadeTree {
	wireIds: Array<string>;
	flagIds: Array<string>;
	ownerIds: Array<string>;
}

/**
 * Pure cascade planner (exported for tests): group actual segments by physical
 * contact (bare interior X does not join), then keep the
 * trees that touch at least one target pin and NO survivor pin. Marker/pin
 * anchoring is point-on-SEGMENT, not vertex proximity (merged collinear wires
 * swallow flags mid-span — issue #135).
 */
export function planSchDeleteCascadeTrees(
	targets: Array<{ id: string; pins: Array<{ x: number; y: number }> }>,
	wires: Array<{ id: string; points: unknown }>,
	markers: Array<{ id: string; x: number; y: number }>,
	survivorPins: Array<{ x: number; y: number }>,
): Array<SchDeleteCascadeTree> {
	const segments = collectWireSegments(wires.map(w => ({
		getState_Line: () => w.points, getState_PrimitiveId: () => w.id,
	})));
	const anchors = [...targets.flatMap(t => t.pins), ...survivorPins, ...markers];
	if (anchors.some(p => !Number.isFinite(p.x) || !Number.isFinite(p.y))) throw new Error('Cascade anchor geometry unavailable.');
	const islands = physicalWireIslands(segments, anchors).map(indices => {
		const segs = indices.map(i => segments[i]);
		const onTree = (p: { x: number; y: number }) => segs.some(s => wirePointOnSegment(p, s.seg));
		return {
			wireIds: [...new Set(segs.map(s => s.wirePrimitiveId))],
			flagIds: [...new Set(markers.filter(onTree).map(m => m.id))],
			ownerIds: targets.filter(t => t.pins.some(onTree)).map(t => t.id),
			hasSurvivor: survivorPins.some(onTree),
		};
	});
	// The official delete API deletes whole primitives. A single primitive can
	// span independent physical islands; deleting its target-exclusive arm must
	// not delete a survivor's arm or any island outside the cascade scope.
	const eligible = islands.map(t => t.ownerIds.length > 0 && !t.hasSurvivor);
	const protectedIds = new Set(islands.flatMap((t, i) => eligible[i] ? [] : t.wireIds));
	// If a candidate contains a protected primitive, keep its entire tree and
	// propagate protection: another shared primitive must not partly erase it.
	let changed = true;
	while (changed) {
		changed = false;
		islands.forEach((t, i) => {
			if (eligible[i] && t.wireIds.some(id => protectedIds.has(id))) {
				eligible[i] = false; t.wireIds.forEach(id => protectedIds.add(id)); changed = true;
			}
		});
	}
	return islands.filter((_, i) => eligible[i]).map(({ wireIds, flagIds, ownerIds }) => ({ wireIds, flagIds, ownerIds }));
}

/** Read everything the cascade planner needs BEFORE the components are deleted
 *  (their pins vanish with them). Throws on read failure — the caller treats
 *  that as "skip the cascade with a warning", never as "block the delete". */
async function collectSchDeleteCascadePlan(ids: Array<string>): Promise<Array<SchDeleteCascadeTree>> {
	const idSet = new Set(ids);
	const components = await eda.sch_PrimitiveComponent.getAll();
	const rawWires = await eda.sch_PrimitiveWire.getAll();
	if (!Array.isArray(components) || !Array.isArray(rawWires)) {
		throw new Error('component/wire enumeration did not return arrays');
	}
	const wires: Array<{ id: string; points: unknown }> = [];
	for (const w of rawWires) {
		wires.push({ id: String(w.getState_PrimitiveId?.() ?? ''), points: w.getState_Line() });
	}
	const NET_MARKER_TYPES = new Set(['netflag', 'netport', 'netlabel', 'short_symbol']);
	const targets: Array<{ id: string; pins: Array<{ x: number; y: number }> }> = [];
	const markers: Array<{ id: string; x: number; y: number }> = [];
	const survivorPins: Array<{ x: number; y: number }> = [];
	for (const c of components) {
		let type = '';
		let pid = '';
		try { type = String(c.getState_ComponentType?.() ?? ''); pid = String(c.getState_PrimitiveId?.() ?? ''); }
		catch (err) { throw new Error(`Cascade component identity unavailable: ${describeThrown(err)}`); }
		if (NET_MARKER_TYPES.has(type)) {
			// A marker that is itself a delete target goes away with the component
			// delete — exclude it so it is not double-deleted by the cascade.
			if (idSet.has(pid)) continue;
			try { markers.push({ id: pid, x: c.getState_X(), y: c.getState_Y() }); }
			catch (err) { throw new Error(`Cascade marker geometry unavailable: ${describeThrown(err)}`); }
			continue;
		}
		if (type === SCH_SHEET_TYPE) continue;
		let pins: Array<{ x: number; y: number }> = [];
		try {
			const raw = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(pid);
			if (!Array.isArray(raw)) throw new Error('Pin API did not return an array.');
			pins = raw.map(p => ({ x: p.getState_X(), y: p.getState_Y() }));
		}
		catch (err) { throw new Error(`Cascade pin geometry unavailable: ${describeThrown(err)}`); }
		if (idSet.has(pid)) targets.push({ id: pid, pins });
		else survivorPins.push(...pins);
	}
	return planSchDeleteCascadeTrees(targets, wires, markers, survivorPins);
}

const schematicComponentDelete: Handler = async (payload) => {
	const primitiveIds = payload.primitiveIds;
	if (
		!(typeof primitiveIds === 'string')
		&& !(Array.isArray(primitiveIds) && primitiveIds.every(id => typeof id === 'string'))
	) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "primitiveIds" (string or string[]).',
		);
	}
	const ids = typeof primitiveIds === 'string' ? [primitiveIds] : primitiveIds;
	// ADR-0004 Decision 5: cascade the part's exclusive stub trees + riding
	// flags. `cascade:false` opts out (a caller managing whole trees itself —
	// e.g. the move kernel — must keep the old semantics).
	const cascade = optionalBoolean(payload, 'cascade') !== false;
	const warnings: Array<string> = [];
	let plannedTrees: Array<SchDeleteCascadeTree> = [];
	if (cascade) {
		// Plan BEFORE deleting: the target pins vanish with the components.
		try {
			plannedTrees = await collectSchDeleteCascadePlan(ids);
		}
		catch (err) {
			warnings.push(warnText('cascade cleanup skipped (could not read the scene before deleting)', err));
		}
	}

	// Chunked + verified: the platform's delete silently no-ops on a large batch and
	// returns true anyway (see SCH_DELETE_BATCH), so neither the call nor its return
	// value can be trusted — only a re-read can say what actually went away.
	try {
		for (let i = 0; i < ids.length; i += SCH_DELETE_BATCH) {
			await eda.sch_PrimitiveComponent.delete(ids.slice(i, i + SCH_DELETE_BATCH));
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to delete components.');
	}
	let survived: Array<string> = [];
	try {
		const alive = new Set((await eda.sch_PrimitiveComponent.getAll()).map(c => c.getState_PrimitiveId()));
		survived = ids.filter(id => alive.has(id));
	}
	catch { /* verification is best-effort; fall through reporting what we asked for */ }

	// Execute the cascade — but only for trees whose touching targets are ALL
	// proven gone: deleting the stubs of a component that survived would
	// disconnect a live part.
	let cascaded: { wires: Array<string>; flags: Array<string> } | undefined;
	const notApplied: Array<{ kind: string; id: string }> = [];
	if (cascade) {
		const survivedSet = new Set(survived);
		const wireIds: Array<string> = [];
		const flagIds: Array<string> = [];
		for (const tree of plannedTrees) {
			if (tree.ownerIds.some(id => survivedSet.has(id))) {
				warnings.push(`cascade skipped a stub tree (${tree.wireIds.join(', ')}): its component(s) survived the delete`);
				continue;
			}
			wireIds.push(...tree.wireIds);
			flagIds.push(...tree.flagIds);
		}
		if (wireIds.length || flagIds.length) {
			// The canvas already changed (components are gone) — a cascade failure
			// must degrade to warnings + notApplied, never throw (#151).
			try {
				if (wireIds.length) await deleteSchGroup('wires', wireIds);
				if (flagIds.length) await deleteSchGroup('components', flagIds);
			}
			catch (err) {
				warnings.push(warnText('cascade delete failed', err));
			}
			// Read back: only proven-removed ids are claimed; survivors are
			// structured notApplied (the platform's delete lies — SCH_DELETE_BATCH).
			const surviving = await survivingSchPrimitives({ wires: wireIds, components: flagIds });
			const wiresLeft = new Set(surviving.wires ?? []);
			const flagsLeft = new Set(surviving.components ?? []);
			cascaded = {
				wires: wireIds.filter(id => !wiresLeft.has(id)),
				flags: flagIds.filter(id => !flagsLeft.has(id)),
			};
			notApplied.push(
				...[...wiresLeft].map(id => ({ kind: 'wire', id })),
				...[...flagsLeft].map(id => ({ kind: 'flag', id })),
			);
			if (notApplied.length) {
				warnings.push(
					`cascade cleanup: ${notApplied.length} primitive(s) survived the delete `
					+ `(${notApplied.map(n => `${n.kind}:${n.id}`).join(', ')}) — re-read before assuming they are gone.`,
				);
			}
		}
		else {
			cascaded = { wires: [], flags: [] };
		}
	}

	return {
		result: {
			deleted: survived.length === 0,
			requested: ids.length,
			removed: ids.length - survived.length,
			...(survived.length ? { survived } : {}),
			...(cascaded ? { cascaded } : {}),
			...(notApplied.length ? { partial: true, notApplied } : {}),
			...(warnings.length ? { warnings } : {}),
		},
		...(warnings.length ? { warnings } : {}),
	};
};

// ─── Page clear / generalized primitive delete ────────────────────────

/** A schematic primitive exposing its id — the only field clear/delete needs. */
interface SchPrimitiveLike { getState_PrimitiveId(): string }

/**
 * Page-level schematic primitive classes that own standalone primitives — each
 * exposes `getAll()` (current page) and `delete(ids)`. Components are handled
 * separately because they carry a componentType (and the sheet/title block).
 * Pins and attributes are intentionally excluded: they belong to a parent
 * primitive, not the page.
 */
const SCH_PAGE_PRIMITIVE_KINDS: Array<{
	key: string;
	getAll: () => Promise<Array<SchPrimitiveLike>>;
	del: (ids: Array<string>) => Promise<boolean>;
}> = [
	{ key: 'wires', getAll: () => eda.sch_PrimitiveWire.getAll(), del: ids => eda.sch_PrimitiveWire.delete(ids) },
	{ key: 'buses', getAll: () => eda.sch_PrimitiveBus.getAll(), del: ids => eda.sch_PrimitiveBus.delete(ids) },
	{ key: 'arcs', getAll: () => eda.sch_PrimitiveArc.getAll(), del: ids => eda.sch_PrimitiveArc.delete(ids) },
	{ key: 'circles', getAll: () => eda.sch_PrimitiveCircle.getAll(), del: ids => eda.sch_PrimitiveCircle.delete(ids) },
	{ key: 'rectangles', getAll: () => eda.sch_PrimitiveRectangle.getAll(), del: ids => eda.sch_PrimitiveRectangle.delete(ids) },
	{ key: 'polygons', getAll: () => eda.sch_PrimitivePolygon.getAll(), del: ids => eda.sch_PrimitivePolygon.delete(ids) },
	{ key: 'texts', getAll: () => eda.sch_PrimitiveText.getAll(), del: ids => eda.sch_PrimitiveText.delete(ids) },
];

// The exact state of every class cleared by schematic.page.clear. This is a
// typed read through the official getState_* accessors, with no editor script.
// Fixed keys (including empty arrays) make failed/partial enumeration distinct
// from a genuinely empty class. A missing accessor fails before any deletion.
const SCH_PAGE_STATE_FIELDS: Record<string, Array<string>> = {
	wires: ['Line', 'Net', 'Color', 'LineWidth', 'LineType'],
	buses: ['BusName', 'Line', 'Color', 'LineWidth', 'LineType'],
	arcs: ['StartX', 'StartY', 'ReferenceX', 'ReferenceY', 'EndX', 'EndY', 'Color', 'FillColor', 'LineWidth', 'LineType'],
	circles: ['CenterX', 'CenterY', 'Radius', 'Color', 'FillColor', 'LineWidth', 'LineType', 'FillStyle'],
	rectangles: ['TopLeftX', 'TopLeftY', 'Width', 'Height', 'CornerRadius', 'Rotation', 'Color', 'FillColor', 'LineWidth', 'LineType', 'FillStyle'],
	polygons: ['Line', 'Color', 'FillColor', 'LineWidth', 'LineType'],
	texts: ['X', 'Y', 'Content', 'Rotation', 'TextColor', 'FontName', 'FontSize', 'Bold', 'Italic', 'UnderLine', 'AlignMode'],
	attributes: ['X', 'Y', 'Rotation', 'Color', 'FontName', 'FontSize', 'Bold', 'Italic', 'UnderLine', 'AlignMode', 'FillColor', 'Key', 'Value', 'KeyVisible', 'ValueVisible', 'ParentPrimitiveId'],
	objects: ['Content', 'StartX', 'StartY', 'Width', 'Height', 'Rotation', 'Mirror', 'FileName'],
};

function schPrimitiveStateRecord(primitive: SchPrimitiveLike, kind: string, native?: NativeSchematicAttribute): Record<string, unknown> {
	const primitiveId = primitive.getState_PrimitiveId();
	const record: Record<string, unknown> = { primitiveId };
	for (const field of SCH_PAGE_STATE_FIELDS[kind]) {
		const getter = (primitive as unknown as Record<string, unknown>)[`getState_${field}`];
		if (typeof getter !== 'function') throw new Error(`Page ${kind}.${field} accessor unavailable for ${primitiveId}.`);
		let value = (getter as () => unknown).call(primitive);
		if (value === undefined && kind === 'attributes' && native) {
			if (field === 'KeyVisible') value = native.keyVisible;
			if (field === 'ValueVisible') value = native.valueVisible;
		}
		if (value === undefined || (typeof value === 'number' && !Number.isFinite(value)) || (typeof File !== 'undefined' && value instanceof File)) throw new Error(`Page ${kind}.${field} state unavailable or unsupported for ${primitiveId}.`);
		record[field] = value;
	}
	return record;
}

async function readCurrentNativePageAttributes(): Promise<Map<string, NativeSchematicAttribute>> {
	if (typeof eda.sys_FileManager?.getProjectFile !== 'function') throw new Error('Official native project export is unavailable for attribute visibility recovery.');
	const before = await readResponseContext();
	if (!before.projectUuid || !before.documentUuid || before.documentType !== 'schematic') throw new Error('Current schematic identity is unavailable for native attribute recovery.');
	const file = await withTimeout(eda.sys_FileManager.getProjectFile('easyeda-agent-attribute-source.epro2', undefined, 'epro2'), 15000,
		'Native attribute project export timed out after 15000ms');
	const after = await readResponseContext();
	if (after.projectUuid !== before.projectUuid || after.documentUuid !== before.documentUuid || after.documentType !== before.documentType || after.tabId !== before.tabId) {
		throw new Error('Current schematic identity changed during native attribute recovery.');
	}
	if (!file || !Number.isSafeInteger(file.size) || file.size <= 0 || file.size > MAX_PROJECT_SOURCE_BYTES) {
		throw new Error('Official native project export is missing, empty, or exceeds the 8 MiB attribute recovery limit.');
	}
	return projectSchematicAttributeInventory(await readProjectNativeSourceArchive(file), before.documentUuid);
}

async function schAttributeStateRecord(
	attribute: SchPrimitiveLike,
	nativeReader?: () => Promise<Map<string, NativeSchematicAttribute>>,
): Promise<Record<string, unknown>> {
	try { return schPrimitiveStateRecord(attribute, 'attributes'); }
	catch (firstError) {
		const id = attribute.getState_PrimitiveId();
		const reread = await eda.sch_PrimitiveAttribute.get(id);
		if (!reread || reread.getState_PrimitiveId() !== id) {
			throw new Error(`Page attribute ${id} cannot be reread after ${describeThrown(firstError)}.`);
		}
		try { return schPrimitiveStateRecord(reread, 'attributes'); }
		catch (rereadError) {
			if (nativeReader) {
				const native = (await nativeReader()).get(id);
				if (!native) throw new Error(`Page attribute ${id} is absent from the current native SCH_PAGE source.`);
				const item = reread as unknown as Record<string, () => unknown>;
				if (item.getState_Key?.() !== native.key || item.getState_ParentPrimitiveId?.() !== native.parentId
					|| item.getState_Value?.() !== native.value) {
					throw new Error(`Page attribute ${id} identity/value differs from the current native SCH_PAGE source.`);
				}
				try { return schPrimitiveStateRecord(reread, 'attributes', native); }
				catch (nativeError) { throw new Error(`Page attribute ${id} remains unreadable after exact native SCH_PAGE recovery: ${describeThrown(nativeError)}.`); }
			}
			throw new Error(`Page attribute ${id} remains unreadable after typed get(id): ${describeThrown(rereadError)}.`);
		}
	}
}

function attributeVisibilityValue(value: unknown): Record<string, unknown> {
	if (value === undefined) return { type: 'undefined', readable: false };
	if (value === null) return { type: 'null', value: null, readable: true };
	if (typeof value === 'boolean') return { type: 'boolean', value, readable: true };
	return { type: typeof value, value: typeof value === 'string' || typeof value === 'number' ? value : String(value), readable: false };
}

function attributeVisibilityRecord(attribute: Awaited<ReturnType<typeof eda.sch_PrimitiveAttribute.getAll>>[number] | undefined): Record<string, unknown> {
	if (!attribute) return {
		found: false,
		KeyVisible: { type: 'unavailable', readable: false },
		ValueVisible: { type: 'unavailable', readable: false },
	};
	return {
		found: true,
		primitiveId: attribute.getState_PrimitiveId(),
		KeyVisible: attributeVisibilityValue(attribute.getState_KeyVisible()),
		ValueVisible: attributeVisibilityValue(attribute.getState_ValueVisible()),
	};
}

async function schematicAttributeInspect(payload: Payload): Promise<ActionResult> {
	const primitiveId = requireString(payload, 'primitiveId');
	const identityBefore = await readResponseContext();
	if (!identityBefore.projectUuid || !identityBefore.documentUuid || identityBefore.documentType !== 'schematic') {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Current schematic document identity is unavailable.');
	}
	const all = await eda.sch_PrimitiveAttribute.getAll();
	if (!Array.isArray(all)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Attribute inventory is unavailable.');
	const matches = all.filter(attribute => attribute.getState_PrimitiveId() === primitiveId);
	if (matches.length > 1) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Attribute ${primitiveId} appears more than once in the current page inventory.`);
	const bulkRecord = attributeVisibilityRecord(matches[0]);
	const direct = await eda.sch_PrimitiveAttribute.get(primitiveId);
	if (direct && direct.getState_PrimitiveId() !== primitiveId) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Attribute get(${primitiveId}) returned a different primitive ID.`);
	}
	const directRecord = attributeVisibilityRecord(direct);
	const asyncAttribute = direct?.toAsync();
	if (asyncAttribute && asyncAttribute.getState_PrimitiveId() !== primitiveId) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Attribute toAsync(${primitiveId}) returned a different primitive ID.`);
	}
	const reset = asyncAttribute ? await asyncAttribute.reset() : undefined;
	if (reset && reset.getState_PrimitiveId() !== primitiveId) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Attribute reset(${primitiveId}) returned a different primitive ID.`);
	}
	const resetRecord = attributeVisibilityRecord(reset);
	const identityAfter = await readResponseContext();
	if (identityAfter.projectUuid !== identityBefore.projectUuid || identityAfter.documentUuid !== identityBefore.documentUuid
		|| identityAfter.documentType !== identityBefore.documentType || identityAfter.tabId !== identityBefore.tabId) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Current document identity changed while inspecting attribute ${primitiveId}.`);
	}
	return { result: {
		primitiveId,
		identityBefore,
		identityAfter,
		getAll: bulkRecord,
		get: directRecord,
		reset: resetRecord,
	} };
}

async function readSchPagePrimitiveState(): Promise<Record<string, Array<Record<string, unknown>>>> {
	const out: Record<string, Array<Record<string, unknown>>> = {};
	const components = await eda.sch_PrimitiveComponent.getAll();
	if (!Array.isArray(components)) throw new Error('Page component inventory unavailable.');
	out.components = components.map(c => {
		const record = JSON.parse(JSON.stringify(serializeComponent(c))) as Record<string, unknown>;
		if (record.componentType === SCH_SHEET_TYPE && record.otherProperty && typeof record.otherProperty === 'object') {
			const props = record.otherProperty as Record<string, unknown>;
			delete props['@Update Date'];
			delete props['@Update Time'];
		}
		return record;
	});
	for (const kind of SCH_PAGE_PRIMITIVE_KINDS) {
		const primitives = await kind.getAll();
		if (!Array.isArray(primitives)) throw new Error(`Page ${kind.key} inventory unavailable.`);
		out[kind.key] = primitives.map(primitive => schPrimitiveStateRecord(primitive, kind.key));
		out[kind.key].sort((a, b) => String(a.primitiveId).localeCompare(String(b.primitiveId)));
	}
	// preserveParts clear can also delete orphan attributes and embedded objects.
	// Mirror its global + per-parent inventory so a newly visible orphan cannot
	// slip into the destructive pass after the snapshot was taken.
	const attributes = new Map<string, Awaited<ReturnType<typeof eda.sch_PrimitiveAttribute.getAll>>[number]>();
	const globalAttributes = await eda.sch_PrimitiveAttribute.getAll();
	if (!Array.isArray(globalAttributes)) throw new Error('Page attribute inventory unavailable.');
	for (const attribute of globalAttributes) attributes.set(attribute.getState_PrimitiveId(), attribute);
	const globalAttributeIds = await eda.sch_PrimitiveAttribute.getAllPrimitiveId();
	if (!Array.isArray(globalAttributeIds)) throw new Error('Page global attribute ID inventory unavailable.');
	for (const id of globalAttributeIds) {
		if (typeof id !== 'string' || !id) throw new Error('Page global attribute ID unavailable.');
		if (!attributes.has(id)) {
			const attribute = await eda.sch_PrimitiveAttribute.get(id);
			if (!attribute) throw new Error(`Page attribute ${id} cannot be read.`);
			attributes.set(id, attribute);
		}
	}
	const globalSet = new Set([...globalAttributes.map(a => a.getState_PrimitiveId()), ...globalAttributeIds]);
	for (const component of components) {
		const owned = await eda.sch_PrimitiveAttribute.getAll(component.getState_PrimitiveId());
		if (!Array.isArray(owned)) throw new Error('Page per-component attribute inventory unavailable.');
		for (const attribute of owned) {
			const id = attribute.getState_PrimitiveId();
			if (!globalSet.has(id)) throw new Error('Page global attribute inventory is incomplete; orphan absence cannot be proven.');
			attributes.set(id, attribute);
		}
	}
	const sheets = new Set(components.filter(c => c.getState_ComponentType() === SCH_SHEET_TYPE).map(c => c.getState_PrimitiveId()));
	out.attributes = [];
	const attributeValues = [...attributes.values()];
	let nativePromise: Promise<Map<string, NativeSchematicAttribute>> | undefined;
	// The SDK may expose generated, fully readable attributes absent from the
	// persisted source. Native source is evidence only for an unreadable getter,
	// never an alternate inventory of everything the editor exposes.
	const nativeReader = () => nativePromise ??= readCurrentNativePageAttributes();
	for (let offset = 0; offset < attributeValues.length; offset += 4) {
		const batch = await Promise.all(attributeValues.slice(offset, offset + 4).map(async attribute => {
			const record = await schAttributeStateRecord(attribute, nativeReader);
			if (sheets.has(String(record.ParentPrimitiveId)) && (record.Key === '@Update Date' || record.Key === '@Update Time')) record.Value = null;
			return record;
		}));
		out.attributes.push(...batch);
	}
	out.attributes.sort((a, b) => String(a.primitiveId).localeCompare(String(b.primitiveId)));
	const objects = await eda.sch_PrimitiveObject.getAll();
	if (!Array.isArray(objects)) throw new Error('Page embedded-object inventory unavailable.');
	out.objects = objects.map(object => schPrimitiveStateRecord(object, 'objects'));
	out.objects.sort((a, b) => String(a.primitiveId).localeCompare(String(b.primitiveId)));
	out.components.sort((a, b) => String(a.primitiveId).localeCompare(String(b.primitiveId)));
	return out;
}

function independentSchPagePrimitives(page: Record<string, Array<Record<string, unknown>>>): { orphanAttributes: Array<string>; objects: Array<string> } {
	const parents = new Set(page.components.map(c => String(c.primitiveId)));
	return {
		orphanAttributes: page.attributes.filter(a => !parents.has(String(a.ParentPrimitiveId))).map(a => String(a.primitiveId)),
		objects: page.objects.map(o => String(o.primitiveId)),
	};
}

async function verifyExpectedSchPagePrimitives(payload: Record<string, unknown>): Promise<Record<string, Array<Record<string, unknown>>> | undefined> {
	if (payload.expectedPagePrimitives === undefined) return undefined;
	let expected: unknown;
	try { expected = JSON.parse(requireString(payload, 'expectedPagePrimitives')); }
	catch { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Invalid expectedPagePrimitives JSON.'); }
	const current = await readSchPagePrimitiveState();
	if (exactJSON(expected) !== exactJSON(current)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'No primitives deleted: page drawing/object state differs from protected snapshot.');
	return current;
}

/** Map a component's getState_ComponentType() to a stable result-count key. */
const SCH_COMPONENT_TYPE_KEY: Record<string, string> = {
	part: 'components',
	netflag: 'netflags',
	netport: 'netports',
	netlabel: 'netlabels',
	nonElectrical_symbol: 'nonElectricalFlags',
	short_symbol: 'shortCircuitFlags',
	sheet: 'sheets',
};

/** componentType value for the drawing sheet / title block (图框). */
const SCH_SHEET_TYPE = 'sheet';

/** Format a caught error for a non-fatal warning entry. */
function warnText(label: string, err: unknown): string {
	return `${label}: ${describeThrown(err)}`;
}

/** Delete a group of ids via its owning class (components fall through to the component class). */
// SCH_DELETE_BATCH caps how many ids go into one delete call. The platform's
// delete SILENTLY NO-OPS on a large batch: it returns true having removed nothing.
// Measured on one page: 1/5/20/50 ids → every one removed; 58 → all removed;
// 134 in a single call → ZERO removed, return value still `true`. It is a size
// ceiling, not a poison-id problem (the 58 that survived the failed 134-call
// deleted fine on their own). Chunking is the only reliable way to delete a page's
// worth of primitives — and since the call lies about success, callers must
// re-read to confirm rather than trust the return.
const SCH_DELETE_BATCH = 50;

/**
 * Re-enumerate the page and report which of the requested ids are STILL there,
 * grouped by the same kind keys the caller deleted under.
 *
 * A delete that returns true is not evidence: large batches silently no-op
 * (SCH_DELETE_BATCH) and some primitive classes keep the primitive outright
 * (issue #164 — zone-draw's text/rectangle labels reported deleted, then came
 * back). Only a re-read can say what actually went away. Best-effort per kind:
 * a class whose getAll throws is treated as "cannot verify", and its ids are
 * NOT claimed as survivors.
 */
export async function survivingSchPrimitives(
	idsByKey: Record<string, Array<string>>,
): Promise<Record<string, Array<string>>> {
	const out: Record<string, Array<string>> = {};
	for (const [key, ids] of Object.entries(idsByKey)) {
		if (!ids.length) continue;
		const kind = SCH_PAGE_PRIMITIVE_KINDS.find(k => k.key === key);
		try {
			const live = kind ? await kind.getAll() : await eda.sch_PrimitiveComponent.getAll();
			const alive = new Set((live ?? []).map(p => p.getState_PrimitiveId()));
			out[key] = ids.filter(id => alive.has(id));
		}
		catch {
			out[key] = []; // unverifiable → do not invent survivors
		}
	}
	return out;
}

async function deleteSchGroup(key: string, ids: Array<string>): Promise<void> {
	const kind = SCH_PAGE_PRIMITIVE_KINDS.find(k => k.key === key);
	for (let i = 0; i < ids.length; i += SCH_DELETE_BATCH) {
		const batch = ids.slice(i, i + SCH_DELETE_BATCH);
		if (!kind) {
			await eda.sch_PrimitiveComponent.delete(batch);
			continue;
		}
		// Page primitives go through the GENERIC object class, not their own —
		// see deleteSchPagePrimitives.
		if (!await deleteSchPagePrimitives(batch)) await kind.del(batch);
	}
}

/**
 * Delete page primitives through `eda.sch_PrimitiveObject` (the generic
 * primitive class), returning false when that class is unavailable so the
 * caller can fall back to the per-class delete.
 *
 * WHY not the per-class delete (issue #164): `sch_PrimitiveText.delete()`
 * removes the text from the in-memory/render index ONLY — it never reaches the
 * persisted document model. Live-measured on a real page: delete → getAll says
 * gone → `schematic.save` → `doc reload` → **all of the original ids are back**.
 * Worse, `modify` on a text is dropped the same way, so a text was effectively
 * frozen at creation. Rectangles and wires do persist their per-class delete,
 * which is why this looked like a text-only curiosity for so long.
 *
 * `sch_PrimitiveObject.delete()` persists across a reload for every page class
 * (verified on a mixed batch of 6 texts + 1 rectangle + 1 wire: all zero after
 * reload, including labels stranded by earlier sessions). It is also
 * type-agnostic, so one call can carry a mixed batch.
 */
async function deleteSchPagePrimitives(ids: Array<string>): Promise<boolean> {
	const generic = (eda as unknown as {
		sch_PrimitiveObject?: { delete?: (ids: Array<string>) => Promise<boolean> };
	}).sch_PrimitiveObject;
	if (typeof generic?.delete !== 'function') return false;
	await generic.delete(ids);
	return true;
}

/**
 * Clear the ACTIVE schematic page — delete every page-level primitive, not just
 * components. `schematic.component.delete` leaves wires/buses/graphics behind
 * (forcing a fall back to raw `debug.exec_js`); this enumerates every
 * `sch_Primitive*` class so a page reset is actually clean. `preserveSheet`
 * (default true) keeps the sheet/title block; `dryRun` counts without deleting.
 * No undo.
 */
// MAX_CLEAR_PASSES bounds the clear→re-enumerate loop below. One pass is NOT
// enough: deleting a component cascades into primitives that were enumerated in
// the same sweep (their ids go stale mid-delete), so a single pass reliably leaves
// debris behind — measured on a 92-primitive page, pass 1 left 58 alive while still
// reporting "total: 92" (that number was the ENUMERATED count, never the deleted
// one). Callers reasonably read a successful clear as "the page is empty", and a
// sweep script that clears between runs silently accumulated three blocks' parts.
const MAX_CLEAR_PASSES = 6;

// enumerateSchPagePrimitives lists every deletable page primitive, grouped by the
// delete-group key. Enumeration failures are collected as warnings rather than
// thrown so one bad class cannot block clearing the rest.
async function enumerateSchPagePrimitives(
	preserveSheet: boolean,
	warnings: Array<string>,
): Promise<Record<string, Array<string>>> {
	const idsByKey: Record<string, Array<string>> = {};

	// Components — net flags/ports/labels are components too, so this single class
	// covers them all. Honor preserveSheet by skipping the sheet.
	let components;
	try {
		components = await eda.sch_PrimitiveComponent.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to enumerate schematic components.');
	}
	for (const c of components) {
		const type = String(c.getState_ComponentType());
		if (preserveSheet && type === SCH_SHEET_TYPE) continue;
		const key = SCH_COMPONENT_TYPE_KEY[type] ?? 'otherComponents';
		(idsByKey[key] ??= []).push(c.getState_PrimitiveId());
	}

	// Wires, buses, and graphics — each its own class.
	for (const kind of SCH_PAGE_PRIMITIVE_KINDS) {
		try {
			for (const p of await kind.getAll()) (idsByKey[kind.key] ??= []).push(p.getState_PrimitiveId());
		}
		catch (err) {
			warnings.push(warnText(`enumerate ${kind.key}`, err));
		}
	}
	return idsByKey;
}

function countIds(idsByKey: Record<string, Array<string>>): number {
	let n = 0;
	for (const ids of Object.values(idsByKey)) n += ids.length;
	return n;
}

// Explicit identity-set contract: never infer a complete protected set from a
// partial caller inventory. All collection succeeds before the first delete.
const schematicPageClearPreservingParts: Handler = async (payload) => {
	if (optionalBoolean(payload, 'preserveSheet') === false) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'preserveParts requires preserving the sheet.');
	const requested = requireStringArray(payload, 'preservePartIds');
	if (!requested.length || requested.some(id => !id.trim()) || new Set(requested).size !== requested.length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'preservePartIds requires a complete unique nonempty part ID set.');
	const ids = [...requested].sort();
	const dryRun = optionalBoolean(payload, 'dryRun') === true;
	let source: Record<string, Record<string, unknown>> | undefined;
	let protectedAttributes: Array<string> | undefined;
	let attributeCoverage = 'existing-parents';
	const collect = async (): Promise<Record<string, Array<string>>> => {
		const components = await eda.sch_PrimitiveComponent.getAll();
		const parts = components.filter(c => String(c.getState_ComponentType()) === 'part');
		const actualIds = parts.map(c => c.getState_PrimitiveId()).sort();
		if (exactJSON(actualIds) !== exactJSON(ids)) throw new Error('preserveParts: fresh part set differs from explicit preservePartIds');
		const sheets = components.filter(c => String(c.getState_ComponentType()) === SCH_SHEET_TYPE);
		if (sheets.length !== 1) throw new Error('preserveParts requires exactly one existing sheet');
		const current: Record<string, Record<string, unknown>> = {};
		const nativeIds = new Set<string>();
		for (const part of parts) {
			const state = preservedInstance(serializeComponent(part), true);
			const nativeId = String(state.uniqueId);
			if (nativeIds.has(nativeId)) throw new Error('preserveParts: duplicate native uniqueId');
			nativeIds.add(nativeId); current[part.getState_PrimitiveId()] = state;
		}
		if (source && exactJSON(source) !== exactJSON(current)) throw new Error('preserveParts: original instance state changed during clear');
		if (!source) source = current;
		const warnings: Array<string> = [];
		const groups = await enumerateSchPagePrimitives(true, warnings);
		if (warnings.length) throw new Error(warnings.join('; '));
		if (exactJSON([...(groups.components ?? [])].sort()) !== exactJSON(ids)) throw new Error('preserveParts: part set changed during enumeration');
		delete groups.components;
		const protectedParents = new Set([...ids, sheets[0].getState_PrimitiveId()]);
		const protectedIds = new Set(protectedParents);
		// Component-owned attributes are preserved; all other attributes are
		// part of the authorized drawing rebuild, including orphan remnants.
		const attributes = new Map<string, Awaited<ReturnType<typeof eda.sch_PrimitiveAttribute.getAll>>[number]>();
		const globalAttributes = await eda.sch_PrimitiveAttribute.getAll();
		for (const attribute of globalAttributes) attributes.set(attribute.getState_PrimitiveId(), attribute);
		// Some official builds return [] for unscoped getAll despite populated
		// component properties. Enumerate every actual parent; [] globally is
		// never sufficient evidence that protected attributes do not exist.
		for (const component of components) {
			for (const attribute of await eda.sch_PrimitiveAttribute.getAll(component.getState_PrimitiveId())) attributes.set(attribute.getState_PrimitiveId(), attribute);
		}
		const globalIds = new Set(globalAttributes.map(a => a.getState_PrimitiveId()));
		attributeCoverage = attributes.size > 0 && [...attributes.keys()].every(id => globalIds.has(id)) ? 'global-and-existing-parents' : 'existing-parents';
		const owned: Array<string> = [];
		for (const attribute of attributes.values()) {
			const id = attribute.getState_PrimitiveId();
			if (protectedParents.has(attribute.getState_ParentPrimitiveId())) { protectedIds.add(id); owned.push(id); }
			else (groups.orphanAttributes ??= []).push(id);
		}
		owned.sort();
		if (protectedAttributes && exactJSON(protectedAttributes) !== exactJSON(owned)) throw new Error('preserveParts: original owned attribute IDs changed during clear');
		if (!protectedAttributes) protectedAttributes = owned;
		// Object.getAll is the embedded-object inventory, NOT an inventory of
		// all schematic primitives. An empty result makes no broader claim.
		for (const object of await eda.sch_PrimitiveObject.getAll()) {
			const id = object.getState_PrimitiveId();
			if (!protectedIds.has(id) && !Object.values(groups).some(group => group.includes(id))) (groups.objects ??= []).push(id);
		}
		return groups;
	};
	let first: Record<string, Array<string>>;
	try { first = await collect(); }
	catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `No primitives deleted: ${describeThrown(err)}`); }
	await verifyExpectedSchPagePrimitives(payload);
	let live = first, passes = 0;
	const warnings: Array<string> = [];
	if (!dryRun) {
		while (countIds(live) > 0 && passes < MAX_CLEAR_PASSES) {
			passes++;
			try {
				for (const [key, group] of Object.entries(live)) {
					if (key === 'orphanAttributes' || key === 'objects') {
						for (let i = 0; i < group.length; i += SCH_DELETE_BATCH) {
							const batch = group.slice(i, i + SCH_DELETE_BATCH);
							if (!await deleteSchPagePrimitives(batch)) {
								if (key === 'orphanAttributes') throw new Error('Generic persisted deletion is required for orphan attributes.');
								else await eda.sch_PrimitiveObject.delete(batch);
							}
						}
					} else await deleteSchGroup(key, group);
				}
				live = await collect();
			}
			catch (err) {
				warnings.push(describeThrown(err));
				return { result: { preserveParts: true, preservedPartIds: ids, instancesPreserved: false, dryRun,
					deletedIds: first, remaining: countIds(live), passes, warnings, partial: true, notApplied: ['preserved-clear-verification'] } };
			}
		}
	}
	const remaining = countIds(live);
	const deleted: Record<string, number> = {};
	for (const [key, group] of Object.entries(first)) deleted[key] = dryRun ? group.length : group.length - (live[key]?.length ?? 0);
	return { result: { preserveParts: true, preserveSheet: true, preservedPartIds: ids, instancesPreserved: true,
		deleted, deletedIds: first, total: dryRun ? countIds(first) : countIds(first) - remaining, remaining, passes, dryRun,
		attributeCoverage, preservedAttributeIds: protectedAttributes,
		...(attributeCoverage === 'existing-parents' ? { coverageNotes: ['Unscoped attribute inventory is not proven complete; attributes without a discoverable existing parent remain unverified.'] } : {}),
		...(!dryRun && remaining ? { partial: true, notApplied: ['remaining-primitives'], warnings: [`${remaining} drawing primitives remain`] } : {}) } };
};

const schematicPageClear: Handler = async (payload) => {
	if (optionalBoolean(payload, 'preserveParts') === true) return schematicPageClearPreservingParts(payload);
	if (payload.preservePartIds != null) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'preservePartIds requires preserveParts:true.');
	const preserveSheet = optionalBoolean(payload, 'preserveSheet') !== false;
	const dryRun = optionalBoolean(payload, 'dryRun') === true;
	const warnings: Array<string> = [];

	const firstPass = await enumerateSchPagePrimitives(preserveSheet, warnings);
	if (payload.expectedPagePrimitives !== undefined && warnings.length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `No primitives deleted: ${warnings.join('; ')}`);
	const protectedPage = await verifyExpectedSchPagePrimitives(payload);
	if (protectedPage) {
		const independent = independentSchPagePrimitives(protectedPage);
		if (independent.orphanAttributes.length || independent.objects.length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`No primitives deleted: ordinary clear cannot prove removal of ${independent.orphanAttributes.length} orphan attributes and ${independent.objects.length} embedded objects.`);
	}
	if (payload.requireCompleteInventory === true) {
		const current = protectedPage ?? await readSchPagePrimitiveState();
		const independent = independentSchPagePrimitives(current);
		if (independent.orphanAttributes.length || independent.objects.length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`Page is not empty: ${independent.orphanAttributes.length} orphan attributes and ${independent.objects.length} embedded objects remain.`);
	}
	const initialTotal = countIds(firstPass);

	if (dryRun) {
		const planned: Record<string, number> = {};
		for (const [key, ids] of Object.entries(firstPass)) planned[key] = ids.length;
		return {
			result: {
				deleted: planned, total: initialTotal, deletedIds: firstPass,
				passes: 0, remaining: initialTotal, preserveSheet, dryRun,
				...(warnings.length ? { warnings } : {}),
			},
		};
	}

	// Delete → RE-ENUMERATE → repeat until the page stops shrinking. Re-reading is
	// the whole point: it is the only way to tell "cleared" from "the delete call
	// returned without throwing".
	let live = firstPass;
	let passes = 0;
	let stalls = 0;
	while (countIds(live) > 0 && passes < MAX_CLEAR_PASSES) {
		passes++;
		const before = countIds(live);
		for (const [key, ids] of Object.entries(live)) {
			if (!ids.length) continue;
			try {
				await deleteSchGroup(key, ids);
			}
			catch (err) {
				warnings.push(warnText(`delete ${key} (pass ${passes})`, err));
			}
		}
		live = await enumerateSchPagePrimitives(preserveSheet, warnings);
		if (countIds(live) >= before) {
			// No progress. Primitives created moments ago can briefly resist deletion
			// while the page settles after a batch of edits — clearing immediately
			// after `block-apply` reproducibly stalled with ~20 survivors, while the
			// same clear run by hand seconds later emptied the page. So pause and try
			// once more before giving up, but never spin: two consecutive stalls means
			// something is genuinely undeletable (locked / platform-protected).
			if (++stalls >= 2) break;
			await new Promise(resolve => setTimeout(resolve, 600));
		}
		else stalls = 0;
	}

	const remaining = countIds(live);
	if (remaining > 0) {
		warnings.push(`page NOT fully cleared: ${remaining} primitive(s) survived ${passes} pass(es) `
			+ `— they may be locked or platform-protected; inspect with \`easyeda sch list\``);
	}

	// deleted/total report what actually went away, per group.
	const deleted: Record<string, number> = {};
	for (const [key, ids] of Object.entries(firstPass)) {
		deleted[key] = ids.length - (live[key]?.length ?? 0);
	}

	return {
		result: {
			deleted,
			total: initialTotal - remaining,
			enumerated: initialTotal,
			remaining,
			passes,
			deletedIds: firstPass,
			preserveSheet,
			dryRun,
			...(warnings.length ? { warnings } : {}),
		},
	};
};

/**
 * Delete schematic primitives of any type by id — generalizes
 * `schematic.component.delete` beyond components (wires, buses, graphics, flags).
 * Each id is routed to its owning `sch_Primitive*` class. With no `primitiveIds`,
 * the current selection is deleted (select first via `schematic.select`). No undo.
 */
const schematicPrimitivesDelete: Handler = async (payload) => {
	const raw = payload.primitiveIds;
	let requested: Array<string> | null;
	if (raw === undefined) {
		requested = null;
	}
	else if (typeof raw === 'string') {
		requested = [raw];
	}
	else if (Array.isArray(raw) && raw.every(id => typeof id === 'string')) {
		requested = raw as Array<string>;
	}
	else {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'"primitiveIds" must be a string or string[] (omit it to delete the current selection).',
		);
	}

	// Build an id → owning-kind index across components + every page class.
	const index = new Map<string, string>();
	try {
		for (const c of await eda.sch_PrimitiveComponent.getAll()) index.set(c.getState_PrimitiveId(), 'components');
	}
	catch (err) {
		throw edaError(err, 'Failed to enumerate schematic components.');
	}
	for (const kind of SCH_PAGE_PRIMITIVE_KINDS) {
		try {
			for (const p of await kind.getAll()) index.set(p.getState_PrimitiveId(), kind.key);
		}
		catch { /* a missing class type is non-fatal for id routing */ }
	}

	// Resolve targets: explicit ids, or the current selection.
	let targets = requested;
	if (targets === null) {
		try {
			targets = (await eda.sch_SelectControl.getAllSelectedPrimitives_PrimitiveId()) ?? [];
		}
		catch (err) {
			throw edaError(err, 'Failed to read the current selection.');
		}
	}

	const idsByKey: Record<string, Array<string>> = {};
	const notFound: Array<string> = [];
	for (const id of targets) {
		const key = index.get(id);
		if (!key) { notFound.push(id); continue; }
		(idsByKey[key] ??= []).push(id);
	}

	const warnings: Array<string> = [];
	for (const [key, ids] of Object.entries(idsByKey)) {
		if (!ids.length) continue;
		try {
			await deleteSchGroup(key, ids);
		}
		catch (err) {
			warnings.push(warnText(`delete ${key}`, err));
		}
	}

	// Verify by re-reading, never by counting what we asked for. The platform's
	// delete returns true on batches it silently no-ops (SCH_DELETE_BATCH), and
	// this handler used to report `deleted[key] = ids.length` straight from the
	// REQUEST — the same "enumerated count reported as the deleted count" bug
	// page.clear was already fixed for, and what let issue #164's zone-draw
	// labels report a clean sweep while every one of them survived.
	const survivedByKey = await survivingSchPrimitives(idsByKey);
	const deleted: Record<string, number> = {};
	const deletedIds: Record<string, Array<string>> = {};
	let total = 0;
	let survivedTotal = 0;
	for (const [key, ids] of Object.entries(idsByKey)) {
		const survived = survivedByKey[key] ?? [];
		const gone = ids.filter(id => !survived.includes(id));
		deleted[key] = gone.length;
		if (gone.length) deletedIds[key] = gone;
		total += gone.length;
		survivedTotal += survived.length;
	}
	const survived = Object.fromEntries(
		Object.entries(survivedByKey).filter(([, ids]) => ids.length),
	);
	if (survivedTotal) {
		warnings.push(
			`${survivedTotal} primitive(s) survived the delete and are still on the page `
			+ `(${Object.entries(survived).map(([k, v]) => `${k}:${v.length}`).join(', ')}). `
			+ 'Re-read before assuming they are gone; some primitive classes accept the '
			+ 'delete call and keep the primitive (issue #164).',
		);
	}

	// #151 convention: the canvas already changed, so this is a structured
	// partial success, not a throw — the caller gets the ids that actually went
	// away plus the ones that did not.
	return {
		result: {
			deleted,
			total,
			requested: targets.length,
			deletedIds,
			...(survivedTotal ? { partial: true, survived, survivedTotal } : {}),
			...(notFound.length ? { notFound } : {}),
			...(warnings.length ? { warnings } : {}),
		},
		...(warnings.length ? { warnings } : {}),
	};
};

// ─── Wire ─────────────────────────────────────────────────────────────

const schematicWireCreate: Handler = async (payload) => {
	// EDA's `sch_PrimitiveWire.create` only accepts a flat `number[]`
	// (`[x1,y1,x2,y2,...]`). Callers may pass either flat or nested
	// (`[[x1,y1],[x2,y2],...]`) points; normalize to flat at this single source
	// of truth so CLI / `call` / sch.py / debug.exec_js all work. See issue #5.
	const points = normalizeWirePoints(payload.points);
	const net = optionalString(payload, 'net');
	const color = optionalString(payload, 'color') ?? null;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? null;
	const lineType = (payload.lineType as ESCH_PrimitiveLineType | undefined) ?? null;

	let wire;
	try {
		wire = await eda.sch_PrimitiveWire.create(
			points,
			net,
			color,
			lineWidth,
			lineType,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to create wire.');
	}
	if (!wire) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Wire creation returned no primitive.');
	}
	return {
		result: {
			primitiveId: wire.getState_PrimitiveId(),
			net: wire.getState_Net(),
			line: wire.getState_Line(),
		},
	};
};

// ─── Group move (virtual grouping — no native EasyEDA "组合" API exists) ────
// Investigated 2026-07-07: EasyEDA Pro's UI has a real "组合"(Combination) field
// on the component property panel (and a matching left-panel tree tab), but it
// is 100% UI-only — ESCH_PrimitiveType has no Group/Combination member, no
// sch_PrimitiveComponent getter/setter touches it, and it isn't smuggled into
// OtherProperty either. There is no way for an extension to read, write, or
// query it. So this does NOT use or persist that native field; it is a
// stateless "move this ad-hoc bag of primitives together" primitive — the
// caller (typically an agent that just placed the assembly) supplies the full
// member list each time. Components translate via a plain x/y modify; wires
// have no modify-in-place (see the delete-then-create note on
// schematicComponentModify above) so they are deleted and recreated at the
// shifted endpoints, preserving net/color/width/lineType. Rotation is
// untouched — a pure translation cannot disturb each member's own orientation
// or the assembly's internal relative layout, which is the entire point.

const schematicGroupMove: Handler = async (payload) => {
	const raw = payload.primitiveIds;
	if (!Array.isArray(raw) || raw.length === 0 || !raw.every(id => typeof id === 'string')) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required field "primitiveIds" (non-empty string[]).');
	}
	const wantIds = new Set(raw as Array<string>);
	const dx = requireNumber(payload, 'dx');
	const dy = requireNumber(payload, 'dy');

	// Resolve via getAll() + local filter, NOT a per-id .get(id) call: a
	// component created earlier in the SAME session/batch can 404 on a direct
	// .get(id) immediately after creation (observed live, 2026-07-07) despite
	// being fully present in getAll() — the same "pull fresh via a list call"
	// caution this codebase already applies elsewhere (rip_up, route delete).
	let allComponents, allWires;
	try {
		allComponents = await eda.sch_PrimitiveComponent.getAll();
		allWires = await eda.sch_PrimitiveWire.getAll();
	}
	catch (err) {
		throw edaError(err, 'group-move: failed to read components/wires for id resolution.');
	}
	if (!Array.isArray(allComponents) || !Array.isArray(allWires)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'group-move: component/wire inventory unavailable.');
	let wirePlans: Map<string, Seg>;
	try {
		wirePlans = assertLegacySimpleWireOperation(collectWireSegments(allWires), wantIds, { x: dx, y: dy });
	} catch (err) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `group-move: refused before mutation: ${describeThrown(err)}`);
	}

	const movedComponents: Array<Record<string, unknown>> = [];
	const movedFlags: Array<Record<string, unknown>> = [];
	const movedWires: Array<Record<string, unknown>> = [];
	const notFound: Array<string> = [];
	const seen = new Set<string>();

	// ── Pre-flight classification. `sch_PrimitiveComponent.modify` is ELEMENT-ONLY:
	// calling it on a netflag/netport throws「仅当器件类型为元件时允许使用该函数进行修改」,
	// and since the platform has no transaction the old element-loop died on the
	// first flag with earlier members already moved (live 2026-08-12: R1 translated
	// three half-runs in a row while C5 + every wire never moved). Flags must move
	// by DELETE + re-CREATE instead — so resolve every flag's recreate parameters
	// UP FRONT and abort with ZERO mutations if any member is unresolvable.
	type flagPlan = {
		id: string; kind: 'netflag' | 'netport'; createArg: string; net: string;
		x: number; y: number; rotation: number; mirror: boolean;
	};
	const elements: Array<{ id: string; comp: (typeof allComponents)[number] }> = [];
	const flagPlans: Array<flagPlan> = [];
	for (const comp of allComponents) {
		const id = comp.getState_PrimitiveId();
		if (!wantIds.has(id)) continue;
		seen.add(id);
		const ctype = String(comp.getState_ComponentType?.() ?? '');
		if (ctype === 'netflag' || ctype === 'netport') {
			const rawName = (comp.getState_Component?.() as { name?: string } | undefined)?.name ?? comp.getState_Name?.() ?? '';
			const name = String(rawName).toLowerCase();
			let createArg = '';
			if (ctype === 'netflag') {
				if (name.includes('analog')) createArg = 'AnalogGround';
				else if (name.includes('protect')) createArg = 'ProtectGround';
				else if (name.startsWith('ground')) createArg = 'Ground';
				else if (name.startsWith('power')) createArg = 'Power';
			}
			else {
				if (name.endsWith('-bi')) createArg = 'BI';
				else if (name.endsWith('-in')) createArg = 'IN';
				else if (name.endsWith('-out')) createArg = 'OUT';
			}
			if (!createArg) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`group-move: cannot derive recreate parameters for ${ctype} ${id} (symbol "${rawName}") — aborted BEFORE any mutation. Exclude it from the set or move it manually.`);
			}
			flagPlans.push({
				id, kind: ctype, createArg,
				net: String(comp.getState_Net?.() ?? ''),
				x: comp.getState_X(), y: comp.getState_Y(),
				rotation: Number(comp.getState_Rotation?.() ?? 0),
				mirror: Boolean(comp.getState_Mirror?.() ?? false),
			});
		}
		else if (ctype === 'netlabel' || ctype === 'short_symbol') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`group-move: ${ctype} ${id} cannot be moved (no create API to recreate it) — aborted BEFORE any mutation. Exclude it from the set.`);
		}
		else {
			elements.push({ id, comp });
		}
	}

	for (const { id, comp } of elements) {
		const from = { x: comp.getState_X(), y: comp.getState_Y() };
		const to = { x: from.x + dx, y: from.y + dy };
		let moved;
		try { moved = await eda.sch_PrimitiveComponent.modify(id, { x: to.x, y: to.y }); }
		catch (err) { throw edaError(err, `group-move: failed to translate component ${id}.`); }
		if (!moved) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: modify returned no primitive for component ${id}.`);
		movedComponents.push({ primitiveId: id, designator: moved.getState_Designator?.() ?? null, from, to });
	}

	// Flags: delete + recreate at the shifted anchor. Rotation passes through
	// appliedRotation so the STORED rotation is preserved on negating builds
	// (same compensation connect_pin uses).
	for (const f of flagPlans) {
		try { await deleteSchGroup('components', [f.id]); }
		catch (err) { throw edaError(err, `group-move: failed to remove old ${f.kind} ${f.id} before recreating it shifted.`); }
		const applied = await appliedRotation(f.rotation);
		let created;
		try {
			created = f.kind === 'netflag'
				? await eda.sch_PrimitiveComponent.createNetFlag(f.createArg as 'Power' | 'Ground' | 'AnalogGround' | 'ProtectGround', f.net, f.x + dx, f.y + dy, applied, f.mirror)
				: await eda.sch_PrimitiveComponent.createNetPort(f.createArg as 'IN' | 'OUT' | 'BI', f.net, f.x + dx, f.y + dy, applied, f.mirror);
		}
		catch (err) { throw edaError(err, `group-move: failed to recreate ${f.kind} ${f.id} ("${f.net}") at the shifted position (original was deleted — recreate manually at (${f.x + dx},${f.y + dy})).`); }
		if (!created) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: recreating ${f.kind} ${f.id} ("${f.net}") returned no primitive (original was deleted).`);
		movedFlags.push({ oldPrimitiveId: f.id, newPrimitiveId: created.getState_PrimitiveId(), kind: f.kind, net: f.net });
	}

	for (const wire of allWires) {
		const id = wire.getState_PrimitiveId();
		if (!wantIds.has(id)) continue;
		seen.add(id);
		const line = wirePlans.get(id)!;
		const shifted = line.map((v, i) => (i % 2 === 0 ? v + dx : v + dy));
		const net = wire.getState_Net();
		const color = wire.getState_Color();
		const lineWidth = wire.getState_LineWidth();
		const lineType = wire.getState_LineType();
		try { await eda.sch_PrimitiveWire.delete([id]); }
		catch (err) { throw edaError(err, `group-move: failed to remove old wire ${id} before recreating it shifted.`); }
		// Preflight proved this is one observed segment before any component moved.
		let created;
		try { created = await eda.sch_PrimitiveWire.create(shifted, net, color, lineWidth, lineType); }
		catch (err) { throw edaError(err, `group-move: failed to recreate wire ${id} at the shifted position (original was deleted — rerun with the same spec to retry).`); }
		if (!created) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: recreating wire ${id} returned no primitive (original was deleted).`);
		movedWires.push({ oldPrimitiveId: id, newPrimitiveId: created.getState_PrimitiveId(), net });
	}

	for (const id of wantIds) {
		if (!seen.has(id)) notFound.push(id);
	}

	if (!movedComponents.length && !movedFlags.length && !movedWires.length) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: none of the ${wantIds.size} id(s) resolved to a component, flag, or wire. Pull fresh ids first.`);
	}

	return {
		result: {
			dx, dy,
			movedComponents,
			movedFlags,
			movedWires,
			count: movedComponents.length + movedFlags.length + movedWires.length,
			...(notFound.length ? { notFound } : {}),
		},
	};
};

// ─── Net flags ────────────────────────────────────────────────────────

type NetFlagIdentification = 'Power' | 'Ground' | 'AnalogGround' | 'ProtectGround';
type NetPortDirection = 'IN' | 'OUT' | 'BI';

const NET_FLAG_KINDS: Record<string, NetFlagIdentification> = {
	power: 'Power',
	ground: 'Ground',
	analog_ground: 'AnalogGround',
	protective_ground: 'ProtectGround',
	protect_ground: 'ProtectGround',
};

const NET_PORT_KINDS: Record<string, NetPortDirection> = {
	net_port_in: 'IN',
	net_port_out: 'OUT',
	net_port_bi: 'BI',
};
const NET_LABEL_KIND = 'net_label';

const schematicNetflagCreate: Handler = async (payload) => {
	const kind = requireString(payload, 'kind');
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const rotation = optionalNumber(payload, 'rotation');
	const mirror = optionalBoolean(payload, 'mirror');

	let component;
	try {
		if (kind in NET_FLAG_KINDS) {
			const net = requireString(payload, 'net');
			component = await eda.sch_PrimitiveComponent.createNetFlag(
				NET_FLAG_KINDS[kind],
				net,
				x,
				y,
				rotation,
				mirror,
			);
		}
		else if (kind in NET_PORT_KINDS) {
			const net = requireString(payload, 'net');
			component = await eda.sch_PrimitiveComponent.createNetPort(
				NET_PORT_KINDS[kind],
				net,
				x,
				y,
				rotation,
				mirror,
			);
		}
		else if (kind === NET_LABEL_KIND) {
			const net = requireString(payload, 'net');
			component = await eda.sch_PrimitiveAttribute.createNetLabel(x, y, net);
		}
		else if (kind === 'short_circuit') {
			component = await eda.sch_PrimitiveComponent.createShortCircuitFlag(x, y, rotation, mirror);
		}
		else {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown netflag kind "${kind}". Expected one of: ${[...Object.keys(NET_FLAG_KINDS), ...Object.keys(NET_PORT_KINDS), 'short_circuit'].join(', ')}.`,
			);
		}
	}
	catch (err) {
		if (err instanceof ActionError) {
			throw err;
		}
		throw edaError(err, 'Failed to create net flag.');
	}
	if (!component) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to create net flag of kind "${kind}".`);
	}
	return {
		result: {
			primitiveId: component.getState_PrimitiveId(),
			...(kind === NET_LABEL_KIND ? {} : { component: serializeComponent(component as SchComponent) }),
		},
	};
};

// ─── No-connect flag (非连接标识) ───────────────────────────────────────
//
// A no-connect mark is NOT a standalone primitive — it is a PIN STATE.
// `pin.setState_NoConnected(true)` stages the X marker and tells DRC the pin is
// intentionally unconnected (so it stops reporting the "un-connected pin"
// error). The staged pin state MUST be committed with `await pin.done()`.
// Resolve the component by designator, re-fetch its live instance, then obtain
// its pins through the live-verified `component.getAllPins()` path. Pass
// noConnected=false to clear.
export const schematicPinSetNoConnect: Handler = async (payload) => {
	const designator = requireString(payload, 'designator');
	const rawPins = payload.pins;
	if (
		!Array.isArray(rawPins)
		|| rawPins.length === 0
		|| !rawPins.every(p => typeof p === 'string' || typeof p === 'number')
	) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "pins" (non-empty array of pin numbers).',
		);
	}
	const wantPins = rawPins.map(String);
	// Default to setting the flag; only an explicit false clears it.
	const value = optionalBoolean(payload, 'noConnected') === false ? false : true;

	let components;
	try {
		components = await eda.sch_PrimitiveComponent.getAll(undefined, true);
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic components.');
	}
	const target = (components ?? []).find(c => c.getState_Designator() === designator);
	if (!target) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`No component with designator "${designator}" on the schematic.`,
		);
	}
	const cid = target.getState_PrimitiveId();

	let component: SchComponent | undefined;
	try {
		component = await eda.sch_PrimitiveComponent.get(cid);
	}
	catch (err) {
		throw edaError(err, `Failed to read component instance "${designator}".`);
	}
	if (!component) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Component instance "${designator}" (${cid}) is no longer available.`,
		);
	}

	let pins;
	try {
		pins = await component.getAllPins();
	}
	catch (err) {
		throw edaError(err, `Failed to read pins of "${designator}".`);
	}
	const byNumber = new Map((pins ?? []).map(p => [p.getState_PinNumber(), p]));

	const missing = wantPins.filter(n => !byNumber.has(n));
	if (missing.length) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`"${designator}" has no pin(s): ${missing.join(', ')}. Available: ${[...byNumber.keys()].join(', ')}.`,
		);
	}

	for (const n of wantPins) {
		try {
			const pin = byNumber.get(n)!;
			pin.setState_NoConnected(value);
			await pin.done();
		}
		catch (err) {
			throw edaError(err, `Failed to apply no-connect on ${designator} pin ${n}.`);
		}
	}

	// Re-fetch the live component and its pins to confirm the STORED state. The
	// just-mutated pin handle only proves the staged value, not that done() stuck.
	let freshComponent: SchComponent | undefined;
	try {
		freshComponent = await eda.sch_PrimitiveComponent.get(cid);
	}
	catch (err) {
		throw edaError(err, `Failed to verify component instance "${designator}".`);
	}
	if (!freshComponent) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Could not re-fetch component instance "${designator}" (${cid}) for verification.`,
		);
	}
	let freshPins;
	try {
		freshPins = await freshComponent.getAllPins();
	}
	catch (err) {
		throw edaError(err, `Failed to verify pins of "${designator}".`);
	}
	const freshByNumber = new Map((freshPins ?? []).map(p => [p.getState_PinNumber(), p]));
	const confirmed = wantPins.map(n => ({
		pin: n,
		noConnected: freshByNumber.get(n)?.getState_NoConnected() ?? null,
	}));

	const notApplied = confirmed.filter(c => c.noConnected !== value);
	if (notApplied.length === wantPins.length) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`EasyEDA did not persist no-connect on ${designator} pin(s) ${wantPins.join(', ')} `
			+ 'after pin.done() (verified by fresh component readback).',
		);
	}

	return {
		result: {
			designator,
			primitiveId: cid,
			noConnected: value,
			pins: confirmed,
			// Per-pin pass/fail so partial application (should a future build allow
			// it) is visible rather than masked by the top-level noConnected.
			notApplied: notApplied.map(c => c.pin),
		},
	};
};

// ─── Select ───────────────────────────────────────────────────────────

const schematicSelect: Handler = async (payload) => {
	const primitiveIds = payload.primitiveIds;
	if (
		!(typeof primitiveIds === 'string')
		&& !(Array.isArray(primitiveIds) && primitiveIds.every(id => typeof id === 'string'))
	) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "primitiveIds" (string or string[]).',
		);
	}
	let selected;
	try {
		await eda.sch_SelectControl.doSelectPrimitives(primitiveIds);
		selected = await eda.sch_SelectControl.getAllSelectedPrimitives_PrimitiveId();
	}
	catch (err) {
		throw edaError(err, 'Failed to select primitives.');
	}
	return { result: { selectedPrimitiveIds: selected } };
};

// ─── Snapshot ─────────────────────────────────────────────────────────

/**
 * Best-effort count of the live primitives on the current schematic page
 * (components + every standalone page primitive). It is the cheap anti-stale
 * signal the snapshot caller compares across frames: if `primitiveCount`
 * changed between two snapshots but the image bytes (sha256) did NOT, the
 * EasyEDA canvas did not redraw and the latest frame is STALE (issue #2).
 * Wrapped so a count failure never blocks the actual image capture.
 */
async function countLivePagePrimitives(): Promise<number | null> {
	try {
		let count = (await eda.sch_PrimitiveComponent.getAll()).length;
		for (const kind of SCH_PAGE_PRIMITIVE_KINDS) {
			count += (await kind.getAll()).length;
		}
		return count;
	}
	catch {
		return null;
	}
}

/**
 * Wait for the canvas to settle (commit any pending viewport change + redraw)
 * before we read a frame. EasyEDA does NOT synchronously repaint after an
 * `eda.*` view call, so `getCurrentRenderedAreaImage` issued back-to-back can
 * return the PREVIOUS frame (issue #20: `view region` followed immediately by
 * `snapshot --no-fit` captures the stale, pre-region viewport). The `--fit`
 * path only "worked" because `zoomToAllPrimitives` happened to nudge a redraw;
 * `--no-fit` had no such nudge. Two animation frames straddle a paint, and the
 * timeout is the fallback for runtimes where rAF never fires (e.g. a
 * backgrounded tab). Best-effort: never throws.
 */
async function waitForCanvasSettle(): Promise<void> {
	const raf: typeof requestAnimationFrame | undefined
		= typeof requestAnimationFrame === 'function' ? requestAnimationFrame : undefined;
	await new Promise<void>((resolve) => {
		let done = false;
		const settle = () => {
			if (done) return;
			done = true;
			resolve();
		};
		// Fallback so we never hang if rAF is throttled/unavailable.
		setTimeout(settle, 120);
		if (raf) {
			raf(() => raf(settle));
		}
	});
}

/**
 * Hex SHA-256 of an image blob, used to tell whether two captures are the
 * byte-identical STALE frame (issue #2/#20). Best-effort: returns null when
 * SubtleCrypto is unavailable rather than blocking the capture.
 */
async function blobSha256(blob: Blob): Promise<string | null> {
	try {
		if (typeof crypto === 'undefined' || !crypto.subtle) return null;
		const buf = await blob.arrayBuffer();
		const digest = await crypto.subtle.digest('SHA-256', buf);
		return Array.from(new Uint8Array(digest))
			.map(b => b.toString(16).padStart(2, '0'))
			.join('');
	}
	catch {
		return null;
	}
}

// (schematic.snapshot removed 2026-08-12 — viewport captures are stale-prone and
// fully superseded by schematic.export.image, which renders document data
// viewport-free. The PCB-side pcb.snapshot keeps the frame-capture machinery.)

// ─── DRC ──────────────────────────────────────────────────────────────

// DRC severity buckets. `fatal`/`error` are the must-fix class the design-flow
// S5 gate counts ("0 fatal"); `warn`/`info` are tolerable. `unknown` is the
// fallback when the SDK string doesn't classify.
type DrcSeverity = 'fatal' | 'error' | 'warn' | 'info' | 'unknown';

// One normalized violation. Keeps the EDA raw object under `raw` so nothing is
// lost; the typed fields are a best-effort projection across the shapes the SDK
// returns (flat `{count,type}` aggregates AND PCB-style nested `{name,list:[…]}`).
interface DrcViolation {
	level: DrcSeverity;
	type?: string;
	rule?: string;
	message?: string;
	primitiveIds?: Array<string>;
	designators?: Array<string>;
	x?: number;
	y?: number;
	count?: number; // present when the SDK only gave an aggregate count, no per-item detail
	raw: unknown;
}

interface DrcSummary {
	fatal: number;
	error: number;
	warn: number;
	info: number;
	unknown: number;
	total: number;
}

/** Map an arbitrary SDK severity string to a DrcSeverity bucket. */
function classifyDrcSeverity(raw: unknown): DrcSeverity {
	const s = String(raw ?? '').toLowerCase();
	if (s.includes('fatal')) return 'fatal';
	if (s.includes('error') || s === 'err') return 'error';
	if (s.includes('warn')) return 'warn';
	if (s.includes('info') || s.includes('note') || s.includes('tip')) return 'info';
	return 'unknown';
}

function firstString(obj: Record<string, unknown>, keys: Array<string>): string | undefined {
	for (const k of keys) {
		const v = obj[k];
		if (typeof v === 'string' && v.length > 0) return v;
		if (typeof v === 'number' && Number.isFinite(v)) return String(v);
	}
	return undefined;
}

function firstNumber(obj: Record<string, unknown>, keys: Array<string>): number | undefined {
	for (const k of keys) {
		const v = obj[k];
		if (typeof v === 'number' && Number.isFinite(v)) return v;
	}
	return undefined;
}

/** Collect id-like / designator-like fields into a string array. */
function collectStrings(obj: Record<string, unknown>, keys: Array<string>): Array<string> | undefined {
	const out: Array<string> = [];
	for (const k of keys) {
		const v = obj[k];
		if (typeof v === 'string' && v.length > 0) out.push(v);
		else if (Array.isArray(v)) {
			for (const e of v) {
				if (typeof e === 'string' && e.length > 0) out.push(e);
				else if (e && typeof e === 'object') {
					const id = firstString(e as Record<string, unknown>, ['primitiveId', 'id', 'designator', 'name']);
					if (id) out.push(id);
				}
			}
		}
	}
	return out.length > 0 ? out : undefined;
}

/**
 * Flatten whatever `sch_Drc.check` returns into per-violation leaves. The SDK is
 * untyped here and ships at least two shapes: schematic returns flat aggregates
 * `[{count, type}]` while PCB nests `[{name, list:[{name, list:[{errorType,…}]}]}]`.
 * This walks any `list` containers recursively, inheriting group type/rule, and
 * emits a leaf per terminal node — so we expand detail when the build provides it
 * and degrade to a per-group aggregate (with `count`) when it doesn't.
 */
function flattenDrcNodes(
	node: unknown,
	inherited: { type?: string; rule?: string },
	out: Array<DrcViolation>,
): void {
	if (Array.isArray(node)) {
		for (const n of node) flattenDrcNodes(n, inherited, out);
		return;
	}
	if (node == null || typeof node !== 'object') return;
	const obj = node as Record<string, unknown>;

	const type = firstString(obj, ['type', 'level', 'severity', 'errorType']) ?? inherited.type;
	const rule = firstString(obj, ['rule', 'ruleName', 'title', 'name', 'errorType']) ?? inherited.rule;

	// Container node: recurse into nested violations, carrying group context down.
	const list = obj.list;
	if (Array.isArray(list) && list.length > 0) {
		flattenDrcNodes(list, { type, rule }, out);
		return;
	}

	// Leaf node — project the known fields, keep the raw object intact.
	const message = firstString(obj, ['message', 'text', 'desc', 'description', 'detail', 'info', 'tip']);
	const primitiveIds = collectStrings(obj, ['primitiveIds', 'primitiveId', 'objs', 'obj1', 'obj2', 'ids']);
	const designators = collectStrings(obj, ['designators', 'designator', 'components']);
	const x = firstNumber(obj, ['x', 'posX']);
	const y = firstNumber(obj, ['y', 'posY']);
	const count = firstNumber(obj, ['count']);

	out.push({
		level: classifyDrcSeverity(type),
		type,
		rule,
		message,
		primitiveIds,
		designators,
		x,
		y,
		// Only surface `count` when this leaf is an aggregate-only node (no per-item
		// detail) so the human view can still say "warn × N" without faking coords.
		count: count !== undefined && message === undefined && x === undefined ? count : undefined,
		raw: node,
	});
}

/** Normalize a raw DRC result into `{passed, fatal, summary, violations}`. */
function normalizeDrc(raw: unknown): {
	passed: boolean;
	fatal: number;
	summary: DrcSummary;
	violations: Array<DrcViolation>;
	raw: unknown;
} {
	if (typeof raw === 'boolean') {
		const summary: DrcSummary = raw
			? { fatal: 0, error: 0, warn: 0, info: 0, unknown: 0, total: 0 }
			: { fatal: 0, error: 0, warn: 0, info: 0, unknown: 1, total: 1 };
		const violations: Array<DrcViolation> = raw ? [] : [{
			level: 'unknown',
			type: 'boolean-fail',
			rule: 'sch_Drc.check',
			message: 'EDA SDK returned false without per-item detail',
			count: 1,
			raw,
		}];
		return { passed: raw, fatal: 0, summary, violations, raw };
	}
	const violations = Array.isArray(raw) ? raw : [];
	const leaves: Array<DrcViolation> = [];
	flattenDrcNodes(violations, {}, leaves);

	const summary: DrcSummary = { fatal: 0, error: 0, warn: 0, info: 0, unknown: 0, total: 0 };
	for (const v of leaves) {
		const n = v.count !== undefined && v.count > 0 ? v.count : 1;
		summary[v.level] += n;
		summary.total += n;
	}
	// `fatal` (the S5 gate input) = the must-fix class: fatal + error severities.
	const fatal = summary.fatal + summary.error;
	return { passed: leaves.length === 0, fatal, summary, violations: leaves, raw };
}

const schematicDrcCheck: Handler = async (payload) => {
	const strict = optionalBoolean(payload, 'strict') === true;
	const includeVerbose = optionalBoolean(payload, 'includeVerboseError') !== false;
	let raw: unknown;
	let nativePassed: boolean;
	try {
		if (includeVerbose) raw = await eda.sch_Drc.check(strict, false, true);
		// Only the boolean overload supplies the host's strict-mode verdict.
		// Aggregate warnings must not silently turn non-strict success into failure.
		nativePassed = await eda.sch_Drc.check(strict, false, false);
	}
	catch (err) {
		throw edaError(err, 'Failed to run DRC.');
	}
	if (typeof nativePassed !== 'boolean') {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'DRC returned no boolean verdict.');
	}
	if (!includeVerbose || typeof raw === 'boolean') {
		return { result: { passed: nativePassed, nativePassed, strict,
			fatal: null, summary: null, violations: [],
			countsAvailable: false, detailsAvailable: false,
			raw: includeVerbose ? raw : nativePassed } };
	}
	if (!Array.isArray(raw)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'DRC returned an unsupported detailed result; counts are unavailable.');
	}
	const normalized = normalizeDrc(raw);
	return { result: { ...normalized, passed: nativePassed, nativePassed, strict,
		countsAvailable: true,
		detailsAvailable: raw.length === 0 || (normalized.violations.length > 0 && normalized.violations.every(v => v.count === undefined)),
		// These are two sequential SDK reads, not an atomic snapshot.
		verdictSource: 'boolean-overload',
	} };
};

// ─── Design check (reconstructed detail the SDK DRC can't expose) ────────────
//
// eda.sch_Drc.check() returns ONLY an aggregate {count,type} for schematic — the
// per-item detail the UI DRC panel shows (which pins float, …) is NOT exposed by
// the official API (verified: absent from check()'s return, sys_Log, sch_Event,
// and every eda.* namespace; it's built inside the EasyEDA UI). This is an EDA
// SDK limitation, not a connector one — PCB's pcb_Drc DOES return nested detail.
//
// So we RECONSTRUCT the actionable findings from primitives we CAN read. Rule 1:
// floating pins — geometric connectivity. A pin is connected iff a wire touches
// its coordinate (endpoints on pins / stubs from connect_pin / pass-through), which
// matches EasyEDA's own "引脚悬空" definition. Output is by designator + pin number
// — the exact input schematic.pin.set_no_connect takes, so "find floating → mark
// NC" is one loop. More rules (empty value, standardization) can be added here.

// Per-pin detail attached to a floating-pin finding so the report is actionable
// without a second lookup: which pin (number+name) on which primitive, and where.
interface CheckPinDetail {
	number: string;
	name?: string;
	x: number;
	y: number;
}

// One reconstructed design-check finding. Reuses the DRC severity buckets.
interface CheckFinding {
	type: string; // 'floating-pin' | 'geom-net-mismatch' | 'wire-crossing' | 'wire-over-pin' | 'net-marker-mismatch' | 'multi-net-wire' | 'zero-length-wire' | 'dangling-wire' | 'polarity-convention-outlier'
	level: DrcSeverity;
	designator?: string;
	primitiveId?: string; // owning component (floating-pin / wire-over-pin)
	wirePrimitiveId?: string;
	markerPrimitiveId?: string;
	wireNet?: string;
	markerNet?: string;
	nets?: Array<string>;
	pins?: Array<string>; // pin numbers — kept flat for `sch no-connect`
	pinDetails?: Array<CheckPinDetail>; // number+name+coords for each pin
	count?: number; // rule-specific slot: floating-pin 悬空脚数 / multi-net-wire 异名数 / polarity-convention-outlier 同页多数派票数(majorityCount)
	message?: string;
	at?: { x: number; y: number }; // location of a crossing / through-pin
	segments?: Array<{ primitiveId: string; segmentIndex: number; rawEncoding: string }>;
}

// Geometry tolerance in schematic units. Pin and wire-endpoint coords come off
// the same grid, so they match exactly; a small epsilon absorbs float noise and
// catches a pin sitting ON a pass-through segment.
const CHECK_EPS = 0.05;

// EasyEDA Pro snaps a created netflag/netport's CONNECTION PIN to a 5-unit grid
// (measured live: a flag requested at (337,-383) lands its pin at (335,-385); the
// anchor keeps the input). connect_pin aligns its stub endpoint to the SAME grid so
// the two coincide (see the snap in schematicPowerConnectPin). Must be 5, not 10 —
// many real footprints (e.g. ESP32-S3-WROOM-1) have pins on the odd 5-grid (y=-385),
// and a 10-snap would move endY off the pin → a diagonal stub that fails to create.
const SCH_GRID = 5;

// FP residue tolerance for the on-grid test (issue #143). A pin coordinate is
// anchor + a rotated offset; the rotation math introduces floating-point noise
// (a pin that should be 650 reads back 649.9999999). Distinct from CHECK_EPS
// (0.05, a geometric coincidence tolerance): this is strictly for grid
// snap-back and must stay << SCH_GRID/2 so a genuinely off-grid pin (half-grid,
// 2.5 away) is never swallowed.
const GRID_EPS = 0.01;

// nearestGrid rounds v to the closest SCH_GRID multiple.
const nearestGrid = (v: number): number => Math.round(v / SCH_GRID) * SCH_GRID;

// True if (px,py) lies on the segment (x1,y1)-(x2,y2) — endpoints included.
function pointOnSegment(px: number, py: number, x1: number, y1: number, x2: number, y2: number): boolean {
	if (px < Math.min(x1, x2) - CHECK_EPS || px > Math.max(x1, x2) + CHECK_EPS) return false;
	if (py < Math.min(y1, y2) - CHECK_EPS || py > Math.max(y1, y2) + CHECK_EPS) return false;
	const cross = (px - x1) * (y2 - y1) - (py - y1) * (x2 - x1);
	return Math.abs(cross) <= CHECK_EPS * Math.max(1, Math.hypot(x2 - x1, y2 - y1));
}

type CheckWireSegment = ObservedWireSegment;

interface NetlistPinInfo {
	net?: string;
}

interface NetlistComponentInfo {
	props?: Record<string, unknown>;
	pinInfoMap?: Record<string, NetlistPinInfo>;
}

// Only observed geometry uses this decoder. Authored create points continue to
// use normalizeWirePoints, whose vertices describe a single requested path.
export function collectWireSegments(wires: Parameters<typeof readObservedWireSegments>[0]): Array<CheckWireSegment> {
	return readObservedWireSegments(wires);
}

/** A whole primitive with no real geometry: every observed segment is zero-length.
 * The platform really does leave these behind (live B04_2_1 2026-09-20: 31 such
 * primitives on one page), and they carry no connectivity at all.
 *
 * The "every" is deliberate and is the conservative half of the rule: if a
 * primitive mixes a degenerate record with a real one, dropping the degenerate
 * record could silently change what that wire connects to, so such a primitive is
 * left completely alone. Verified against real data before relying on it — all 48
 * degenerate primitives in that project were single-segment, so none mixed.
 */
function isWhollyDegenerateWire(own: CheckWireSegment[]): boolean {
	return own.length > 0 && own.every(s => isDegenerateWireSegment(s.seg));
}

/**
 * Wire segments usable for CONTACT reasoning, degenerates removed.
 *
 * `schematic.check` and `schematic.bridgeCheck` both build their connectivity model
 * from this. Before this filter, a single zero-length primitive anywhere on the
 * page made `physicalWireIslands` throw `Unknown wire contact.`, which took BOTH
 * commands down for the whole page (and the `check` stage of `sch gate` with them)
 * — even though `zero-length-wire` was already a documented WARN rule that the code
 * could never reach. Dropping them here, at one shared point, is what lets that
 * existing rule report them instead of aborting the run.
 */
export function connectivityWireSegments(segments: CheckWireSegment[]): CheckWireSegment[] {
	const byWire = new Map<string, CheckWireSegment[]>();
	for (const s of segments) {
		const own = byWire.get(s.wirePrimitiveId) ?? [];
		own.push(s);
		byWire.set(s.wirePrimitiveId, own);
	}
	const dropped = new Set<string>();
	for (const [wirePid, own] of byWire) {
		if (isWhollyDegenerateWire(own)) dropped.add(wirePid);
	}
	return segments.filter(s => !dropped.has(s.wirePrimitiveId));
}

// Result of reading the JSON-authoritative netlist. `available` distinguishes
// "netlist fetched+parsed" (trust its pin→net facts, even the ABSENCE of a net)
// from "couldn't fetch/parse" (netlist muted → geometry alone decides). Without
// this flag an uncompiled/missing netlist would look like "every pin has no net"
// and manufacture geom-net-mismatch false reports.
interface NetlistPinNets {
	byDesignator: Map<string, Map<string, string>>;
	available: boolean;
	/** Why the netlist could not be fetched/parsed. Set only when available===false,
	 * so callers can report a CAUSE instead of silently degrading to "no nets". */
	error?: string;
}

async function collectNetlistPinNets(_allPages = false): Promise<NetlistPinNets> {
	const byDesignator = new Map<string, Map<string, string>>();
	const muted = (error?: string): NetlistPinNets =>
		({ byDesignator, available: false, ...(error ? { error } : {}) });
	let file: File | undefined;
	try { file = await eda.sch_ManufactureData.getNetlistFile(); }
	catch (err) { return muted(describeThrown(err)); }
	if (!file) return muted('netlist export returned no file');
	let parsed: unknown;
	try { parsed = JSON.parse(await file.text()); }
	catch (err) { return muted(`netlist JSON parse failed: ${describeThrown(err)}`); }
	const components = (parsed as { components?: Record<string, NetlistComponentInfo> })?.components;
	if (!components || typeof components !== 'object') return muted('netlist JSON carries no components map');
	for (const comp of Object.values(components)) {
		const designator = String(comp.props?.Designator ?? '');
		if (!designator || !comp.pinInfoMap) continue;
		const pins = byDesignator.get(designator) ?? new Map<string, string>();
		for (const pin of Object.values(comp.pinInfoMap)) {
			const number = String((pin as { number?: unknown })?.number ?? '');
			const net = String(pin?.net ?? '');
			if (number && net) pins.set(number, net);
		}
		byDesignator.set(designator, pins);
	}
	return { byDesignator, available: true };
}

// ─── Polarity-convention outlier (#183 phase 1) ─────────────────────────────
//
// Pure helpers so the rule is unit-testable without an EasyEDA runtime (project
// test law: handlers may touch the `eda` global, tests touch pure functions).
//
// Issue #183: a tantalum cap wired positive-to-GND sailed through `sch check`
// (51 WARNs, none about polarity) until the board came back and thermally ran
// away. The mechanically detectable core of that incident needs NO domain
// knowledge: on one page, peers of the same class wired as {one pin on a power
// rail, the other on GND} overwhelmingly agree on WHICH pin number carries the
// rail — a part contradicting that majority is worth a look.

// Ground-ish net names: GND family (incl. full spelling GROUND) + VSS + earth.
// Prefix match on purpose so GNDA / DGND / PGND / GND_1 classify while longer
// unrelated names do not.
export function isGroundLikeNet(net: string): boolean {
	return /^(gnd|agnd|dgnd|pgnd|vss|earth|ground)/i.test(net);
}

// Power-rail-ish net names: numeric-volt prefixes (+5V / 3V3 / 12V0) and common
// V-rail families (VCC/VDD/VPP/VBAT/VBUS/VIN/VOUT/VSYS…). Prefix match on
// purpose — suffixed rails like VBAT_RAW / VSYS_5V must still classify.
export function isPowerRailNet(net: string): boolean {
	return /^[+-]?\d+(\.\d+)?v/i.test(net)
		|| /^v(cc|dd|pp|bat|bus|in|out|sys|usb|mic|aux)/i.test(net);
}

export interface PolarityCapCandidate {
	designator: string;
	primitiveId?: string;
	pins: Array<{ number: string; net: string }>; // the component's exactly-2 physical pins with netlist nets
}

export interface PolarityConventionOutlier {
	designator: string;
	primitiveId?: string;
	powerPin: string; // this part's rail-side pin number
	gndPin: string;
	powerNet: string;
	gndNet: string;
	majorityPowerPin: string; // the page convention this part contradicts
	majorityCount: number;
	totalMatched: number; // parts entering the statistics (gnd+power pattern)
}

// A candidate enters the statistics only when its two pins classify as exactly
// {power rail, GND} — series/signal caps (both pins on signal nets) are excluded
// because their pin order carries no polarity meaning.
const POLARITY_MIN_GROUP = 3; // fewer matched parts than this ⇒ no page convention to violate
const POLARITY_MAJORITY_FRACTION = 0.75; // --strict promotes WARNs to blocking, so "the page convention" must be supermajority, not a coin flip

export function detectPolarityConventionOutliers(
	candidates: Array<PolarityCapCandidate>,
): Array<PolarityConventionOutlier> {
	const matched: Array<Omit<PolarityConventionOutlier, 'majorityPowerPin' | 'majorityCount' | 'totalMatched'>> = [];
	for (const c of candidates) {
		if (!c.pins || c.pins.length !== 2) continue;
		const [a, b] = c.pins;
		const cls = (net: string): 'gnd' | 'pwr' | 'other' =>
			isGroundLikeNet(net) ? 'gnd' : isPowerRailNet(net) ? 'pwr' : 'other';
		if (cls(a.net) === 'pwr' && cls(b.net) === 'gnd') {
			matched.push({ designator: c.designator, primitiveId: c.primitiveId, powerPin: a.number, gndPin: b.number, powerNet: a.net, gndNet: b.net });
		}
		else if (cls(a.net) === 'gnd' && cls(b.net) === 'pwr') {
			matched.push({ designator: c.designator, primitiveId: c.primitiveId, powerPin: b.number, gndPin: a.number, powerNet: b.net, gndNet: a.net });
		}
	}
	if (matched.length < POLARITY_MIN_GROUP) return [];
	const byPowerPin = new Map<string, number>();
	for (const m of matched) byPowerPin.set(m.powerPin, (byPowerPin.get(m.powerPin) ?? 0) + 1);
	const ranked = [...byPowerPin.entries()].sort((x, y) => y[1] - x[1]);
	if (ranked.length < 2) return []; // unanimous — nothing to flag
	const [majorityPin, majorityCount] = ranked[0];
	if (ranked[1][1] === majorityCount) return []; // tie — no established convention
	if (majorityCount < Math.ceil(matched.length * POLARITY_MAJORITY_FRACTION)) return [];
	return matched
		.filter(m => m.powerPin !== majorityPin)
		.map(m => ({ ...m, majorityPowerPin: majorityPin, majorityCount, totalMatched: matched.length }));
}

type Seg = [number, number, number, number];

// Intersection point of two (properly crossing) segments; null if near-parallel.
function segIntersection(s1: Seg, s2: Seg): { x: number; y: number } | null {
	const [x1, y1, x2, y2] = s1;
	const [x3, y3, x4, y4] = s2;
	const den = (x1 - x2) * (y3 - y4) - (y1 - y2) * (x3 - x4);
	if (Math.abs(den) < 1e-9) return null;
	const t = ((x1 - x3) * (y3 - y4) - (y1 - y3) * (x3 - x4)) / den;
	return { x: Math.round((x1 + t * (x2 - x1)) * 100) / 100, y: Math.round((y1 + t * (y2 - y1)) * 100) / 100 };
}

// True if (px,py) lies on the segment but NOT at either endpoint — i.e. the wire
// passes THROUGH the point. EasyEDA trims+connects a wire at any pin it crosses, so
// a pin in a wire's interior is an unintended-connection hazard.
function interiorOnSegment(px: number, py: number, s: Seg): boolean {
	if (!pointOnSegment(px, py, s[0], s[1], s[2], s[3])) return false;
	const endTol = CHECK_EPS * 8;
	return Math.hypot(px - s[0], py - s[1]) > endTol && Math.hypot(px - s[2], py - s[3]) > endTol;
}

const schematicCheck: Handler = async (payload) => {
	const allPages = optionalBoolean(payload, 'allPages') === true;
	let components, wires;
	const { byDesignator: netlistPinNets, available: netlistAvailable } = await collectNetlistPinNets();
	try {
		components = await eda.sch_PrimitiveComponent.getAll(undefined, allPages);
		wires = await eda.sch_PrimitiveWire.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic for design check.');
	}
	if (!Array.isArray(components) || !Array.isArray(wires)) throw new Error('Incomplete design-check component/wire enumeration.');
	// Degenerate (zero-length) primitives are removed BEFORE any contact reasoning;
	// see connectivityWireSegments. They are still counted and reported below by the
	// zero-length-wire rule, which reads this same collection.
	const wireSegs = collectWireSegments(wires);
	const connectivitySegs = connectivityWireSegments(wireSegs);
	const segs = connectivitySegs.map(w => w.seg);

	// Connection anchors that legitimately terminate a stub but are NOT real pins:
	// netflag / netport / netlabel components. A pin sitting on one of these (e.g. an
	// overlapping `sch connect` stub that EasyEDA auto-merged into a collinear wire)
	// is intentionally connected — it must be excluded from wire-over-pin so the
	// check agrees with the official DRC instead of flagging the merged stub endpoint.
	const NET_MARKER_TYPES = new Set(['netflag', 'netport', 'netlabel', 'short_symbol']);
	const connectionMarkers: Array<{ x: number; y: number; net: string; primitiveId: string; componentType: string }> = [];
	for (const c of components ?? []) {
		let type: string;
		try { type = String(c.getState_ComponentType?.() ?? ''); }
		catch (err) { throw new Error(`Check component identity unavailable: ${describeThrown(err)}`); }
		if (!NET_MARKER_TYPES.has(type)) continue;
		try {
			connectionMarkers.push({
				x: c.getState_X(),
				y: c.getState_Y(),
				net: String(c.getState_Net?.() ?? ''),
				primitiveId: String(c.getState_PrimitiveId?.() ?? ''),
				componentType: type,
			});
		}
		catch (err) { throw new Error(`Check marker geometry unavailable: ${describeThrown(err)}`); }
	}
	// Every wire vertex (segment endpoint). A pin coincident with a wire endpoint is
	// a legitimate termination/junction even if a merged collinear wire also runs
	// through it — that's a connection, not a pass-through short.
	const wireEndpoints: Array<{ x: number; y: number }> = [];
	for (const s of segs) {
		wireEndpoints.push({ x: s[0], y: s[1] }, { x: s[2], y: s[3] });
	}
	const COINCIDE_TOL = CHECK_EPS * 8;
	const coincidesWithAnchor = (x: number, y: number): boolean =>
		connectionMarkers.some(m => Math.hypot(x - m.x, y - m.y) <= COINCIDE_TOL)
		|| wireEndpoints.some(e => Math.hypot(x - e.x, y - e.y) <= COINCIDE_TOL);

	const findings: Array<CheckFinding> = [];
	let netMarkerMismatches = 0;
	let multiNetWires = 0;

	// Rule 0: net marker/port/label names must agree with the wire they touch.
	// The UI DRC reports this as "网络标识 X 的名称与所连导线 Y 名称不一致", but
	// eda.sch_Drc.check does not expose it through the SDK on current builds.
	const wireMarkerNets = new Map<string, Array<{ net: string; markerPrimitiveId: string; at: { x: number; y: number } }>>();
	const seenMarkerWire = new Set<string>();
	const seenMismatch = new Set<string>();
	for (const m of connectionMarkers) {
		if (!m.net) continue;
		for (const ws of wireSegs) {
			if (!wirePointOnSegment(m, ws.seg)) continue;
			if (ws.wirePrimitiveId) {
				const markerWireKey = `${ws.wirePrimitiveId}\u0000${m.primitiveId || m.x + ',' + m.y}\u0000${m.net}`;
				if (!seenMarkerWire.has(markerWireKey)) {
					seenMarkerWire.add(markerWireKey);
					const arr = wireMarkerNets.get(ws.wirePrimitiveId) ?? [];
					arr.push({ net: m.net, markerPrimitiveId: m.primitiveId, at: { x: m.x, y: m.y } });
					wireMarkerNets.set(ws.wirePrimitiveId, arr);
				}
			}
			if (ws.net && ws.net !== m.net) {
				const mismatchKey = `${ws.wirePrimitiveId}\u0000${m.primitiveId || m.x + ',' + m.y}\u0000${ws.net}\u0000${m.net}`;
				if (seenMismatch.has(mismatchKey)) continue;
				seenMismatch.add(mismatchKey);
				netMarkerMismatches++;
				findings.push({
					type: 'net-marker-mismatch',
					level: 'warn',
					wirePrimitiveId: ws.wirePrimitiveId || undefined,
					markerPrimitiveId: m.primitiveId || undefined,
					wireNet: ws.net,
					markerNet: m.net,
					at: { x: m.x, y: m.y },
					message: `网络标识 ${m.net} 与所连导线 ${ws.net} 名称不一致`,
				});
			}
		}
	}
	for (const [wireId, refs] of wireMarkerNets) {
		const nets = refs.map(r => r.net).filter(Boolean);
		if (nets.length <= 1) continue;
		const unique = [...new Set(nets)];
		// Only distinct net names on one wire is a real short (异名并线).
		// Repeated same-name flags (e.g. ["GND","GND"] from共线合并的 stub) are
		// legal — collapse to unique so we don't drown 3 real shorts under 83 dupes.
		if (unique.length > 1) {
			multiNetWires++;
			findings.push({
				type: 'multi-net-wire',
				level: 'warn',
				wirePrimitiveId: wireId,
				nets: unique,
				count: unique.length,
				message: `导线有多个网络名: ${unique.join('、')}`,
			});
		}
	}

	let floatingTotal = 0;
	let componentsWithFloating = 0;
	// Geometry says the pin is wired but the authoritative netlist puts it on no net
	// — a suspected MISSED report (the cross-check's "补漏报" direction).
	let geomNetMismatches = 0;
	// All component pins, kept for the wire-over-pin rule below.
	const allPins: Array<{ designator: string; number: string; x: number; y: number }> = [];
	// #183 phase 1: two-pin capacitor candidates for the polarity-convention rule
	// (collected during the loop below; analyzed right after it — pure fn).
	const polarityCandidates: Array<PolarityCapCandidate> = [];

	for (const c of components ?? []) {
		// Net flags/ports/labels are components too but have no real pins to float
		// — getAllPinsByPrimitiveId returns empty for them, so they're skipped.
		const primitiveId = c.getState_PrimitiveId();
		let pins;
		try { pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(primitiveId); }
		catch (err) { throw new Error(`Check pin geometry unavailable: ${describeThrown(err)}`); }
		if (!Array.isArray(pins)) throw new Error('Check pin API did not return an array.');
		if (pins.length === 0) continue;
		const designator = c.getState_Designator?.() ?? '';

		// #183 phase 1: candidate collection — capacitor-designated (C+digits; the
		// digit keeps CN power terminals / CR diodes out of the cap vote — their
		// rail-pin numbering follows no cap convention), exactly two physical pins,
		// both with netlist nets (netlist muted ⇒ no rail facts, skip).
		if (/^c\d/i.test(designator) && pins.length === 2 && netlistAvailable) {
			const pinNets = pins.map(p => ({
				number: String(p.getState_PinNumber?.() ?? ''),
				net: netlistPinNets.get(designator)?.get(String(p.getState_PinNumber?.() ?? '')) ?? '',
			}));
			if (pinNets.every(pn => pn.number !== '' && pn.net !== '')) {
				polarityCandidates.push({ designator, primitiveId, pins: pinNets });
			}
		}

		// Rule 1: floating pins (geometric connectivity).
		const floating: Array<string> = [];
		const floatingDetails: Array<CheckPinDetail> = [];
		for (const p of pins) {
			const px = p.getState_X();
			const py = p.getState_Y();
			const num = String(p.getState_PinNumber?.() ?? '');
			allPins.push({ designator, number: num, x: px, y: py });
			// Intentionally-NC pins (the X marker) are not "floating" — skip them.
			try { if (p.getState_NoConnected && p.getState_NoConnected()) continue; }
			catch { /* treat as not-NC */ }
			const netlistNet = netlistPinNets.get(designator)?.get(num) ?? '';
			// Pure-geometry connectivity: a wire touches the pin, or it sits on a
			// netflag/netport/netlabel anchor.
			const geomConnected = segs.some(s => pointOnSegment(px, py, s[0], s[1], s[2], s[3]))
				|| connectionMarkers.some(m => Math.hypot(px - m.x, py - m.y) <= COINCIDE_TOL);
			// Cross-check geometry against the JSON-authoritative netlist:
			//   connected         → netlist has a net (drops #15-class false positives),
			//                        or netlist muted + geometry wires it
			//   floating          → neither source connects it (real floating pin)
			//   geom-net-mismatch → geometry wires it but netlist has NO net (补漏报)
			// Designator-less primitives (netflags/netports/netlabels DO expose a
			// pin "1" on this build, despite the note above) can never appear in the
			// netlist's components map — mute the netlist for them so geometry alone
			// decides, else every flag pin becomes a geom-net-mismatch false report
			// (probe round #3: 64/64 stubs flagged on a fully-connected page).
			const status = classifyPinConnectivity(Boolean(netlistNet), geomConnected, netlistAvailable && Boolean(designator));
			if (status === 'floating') {
				floating.push(num);
				const name = (() => {
					try { return String(p.getState_PinName?.() ?? ''); }
					catch { return ''; }
				})();
				floatingDetails.push({ number: num, name: name || undefined, x: px, y: py });
			}
			else if (status === 'geom-net-mismatch') {
				geomNetMismatches++;
				findings.push({
					type: 'geom-net-mismatch',
					level: 'warn',
					designator,
					primitiveId,
					pins: [num],
					at: { x: px, y: py },
					message: '导线触碰该引脚但网表未将其归入任何 net(疑似漏连:未编译网表或仅几何贴合未真正连通)',
				});
			}
		}
		if (floating.length > 0) {
			floatingTotal += floating.length;
			componentsWithFloating++;
			findings.push({
				type: 'floating-pin',
				level: 'warn',
				designator,
				primitiveId,
				pins: floating,
				pinDetails: floatingDetails,
				count: floating.length,
				message: `${floating.length} 个引脚悬空(无导线连接,未打 NC 标识)`,
			});
		}
	}

	// Rule 1.5: polarity-convention-outlier (#183 phase 1) — same-page same-class
	// rail-pin consistency. A tantalum cap wired positive-to-GND once passed this
	// check silently (issue #183): DRC cannot see polarity and static measurements
	// look fine until thermal runaway. Peers wired {rail pin, GND pin} that agree
	// on the rail-side pin number form a convention; a contradicting part is the
	// 8:1 signal the issue describes. WARN-only by design — MLCC numbering is
	// arbitrary, so this is a consistency hint, not a polarity assertion; the
	// ERROR-level rule needs footprint/symbol evidence (issue #183 phase 2).
	// Known v1 scope limit: `--all-pages` pools candidates across pages into ONE
	// convention instead of per-page stats (the connector loop is page-agnostic);
	// default single-page runs — the gate path — are unaffected.
	let polarityConventionOutliers = 0;
	{
		const outliers = detectPolarityConventionOutliers(polarityCandidates);
		polarityConventionOutliers = outliers.length;
		// --all-pages pools candidates across pages into ONE convention (scope limit
		// above) — say so in the finding instead of letting the reader assume
		// per-page stats.
		const poolingNote = allPages ? ';注意 --all-pages 下统计为跨页池化而非逐页约定' : '';
		for (const o of outliers) {
			findings.push({
				type: 'polarity-convention-outlier',
				level: 'warn',
				designator: o.designator,
				primitiveId: o.primitiveId,
				pins: [o.powerPin, o.gndPin],
				nets: [o.powerNet, o.gndNet],
				count: o.majorityCount, // rule-specific slot: majority vote count (see CheckFinding.count)
				message: `极性脚约定与同页多数不一致:该电容电源侧接在 pin ${o.powerPin}(${o.gndNet} 侧为 pin ${o.gndPin}),同页 ${o.totalMatched} 颗同类中 ${o.majorityCount} 颗电源侧为 pin ${o.majorityPowerPin} —— 钽/电解电容反接有热失控风险务必核对;MLCC 无极性可忽略 (#183)${poolingNote}`,
			});
		}
	}

	// The supported host keeps bare orthogonal interior Xs electrically separate.
	// INFO is earned from complete raw geometry, measured anchors, and fresh
	// pin→net witnesses on BOTH physical islands. A name alone is not proof.
	if ([...allPins, ...connectionMarkers].some(p => !Number.isFinite(p.x) || !Number.isFinite(p.y))) throw new Error('Incomplete check anchor coordinates.');
	const crossingAnchors = [...allPins, ...connectionMarkers];
	const physicalIslands = physicalWireIslands(wireSegs, crossingAnchors);
	const verifiedSegments = new Set<number>();
	for (const island of physicalIslands) {
		const own = island.map(i => wireSegs[i]);
		const onIsland = (p: { x: number; y: number }) => own.some(w => wirePointOnSegment(p, w.seg));
		const witnesses = allPins.filter(p => p.designator && onIsland(p));
		const markers = connectionMarkers.filter(onIsland);
		const rawNets = own.map(w => w.net).filter(Boolean);
		const pinNets = witnesses.map(p => netlistPinNets.get(p.designator)?.get(p.number) ?? '');
		const markerNets = markers.map(m => m.net);
		// Some supported host builds return an empty getState_Net() even for a
		// compiled wire. Treat it as missing evidence, not as a contradictory
		// network. A bare X is waived only when every pin/marker witness is
		// present and all non-empty raw+pin+marker facts name exactly one net.
		// At least one official pin witness is mandatory; marker names alone do
		// not prove that the physical island made it into the compiled netlist.
		const evidence = new Set([...rawNets, ...pinNets.filter(Boolean), ...markerNets.filter(Boolean)]);
		const verified = netlistAvailable && !allPages
			&& witnesses.length > 0 && pinNets.every(Boolean)
			&& markerNets.every(Boolean) && evidence.size === 1;
		if (verified) island.forEach(i => verifiedSegments.add(i));
	}
	const CROSS_CAP = 50;
	let crossingTotal = 0;
	for (let i = 0; i < segs.length; i++) {
		for (let j = i + 1; j < segs.length; j++) {
			const relation = classifyWireContact(segs[i], segs[j]);
			const a = wireSegs[i], b = wireSegs[j];
			if ((relation === 'endpoint-touch' || relation === 'collinear-overlap') && a.net && b.net && a.net !== b.net) {
				findings.push({ type: 'wire-contact', level: 'error', nets: [a.net, b.net],
					message: '异网导线存在端点/T接/共线重叠的真实电气接触',
					segments: [a, b].map(s => ({ primitiveId: s.wirePrimitiveId, segmentIndex: s.segmentIndex, rawEncoding: s.rawEncoding })) });
			}
			if (relation !== 'proper-cross') continue;
			crossingTotal++;
			if (findings.filter(f => f.type === 'wire-crossing').length >= CROSS_CAP) continue;
			const anchorAtCrossing = [...crossingAnchors, ...wireEndpoints].some(p => wirePointOnSegment(p, segs[i]) && wirePointOnSegment(p, segs[j]));
			const verified = !anchorAtCrossing && verifiedSegments.has(i) && verifiedSegments.has(j);
			findings.push({
				type: 'wire-crossing',
				level: verified ? 'info' : 'error',
				count: 1,
				at: segIntersection(segs[i], segs[j]) ?? undefined,
				segments: [a, b].map(s => ({ primitiveId: s.wirePrimitiveId, segmentIndex: s.segmentIndex, rawEncoding: s.rawEncoding })),
				message: verified
					? '已验证无接点正交内部交叉：原始段端不在交点、无引脚/标记，两个物理线岛的逐pin网络证据完整'
					: '交叉未获得无接点证据：存在引脚/标记或逐pin网络/原始几何不完整，不能豁免',
			});
		}
	}

	// Rule 3: wire-over-pin — a pin sits in a wire's INTERIOR (the wire passes
	// through it). EasyEDA trims+connects there, an unintended connection.
	// EXCLUDE intended connections: a pin coincident with a wire endpoint or a
	// netflag/netport/netlabel anchor is the legitimate terminus of its own stub.
	// When EasyEDA auto-merges collinear touching stubs into one long wire, an inner
	// pin lands in that merged wire's interior even though it's connected at its own
	// stub endpoint/marker — without this guard those merged stubs produce the
	// wire-over-pin false positives the official DRC does not report.
	let overPinTotal = 0;
	for (const p of allPins) {
		if (coincidesWithAnchor(p.x, p.y)) continue;
		const hit = segs.some(s => interiorOnSegment(p.x, p.y, s));
		if (hit) {
			overPinTotal++;
			findings.push({
				type: 'wire-over-pin',
				level: 'warn',
				designator: p.designator,
				pins: [p.number],
				at: { x: p.x, y: p.y },
				message: '导线穿过该引脚(EasyEDA 会在此处截断并连接 — 非预期短接)',
			});
		}
	}

	// Rule 4 + 5: stray wires neither the SDK DRC nor layout-lint reports —
	// zero-length segments and orphaned ("dangling") wires that connect to nothing.
	// A stub whose pin/flag was deleted leaves a floating empty-net wire that
	// silently pollutes the page (the ESP32 reference board accumulated 149/204
	// zero-length wires this way). A wire is dangling when NONE of its vertices
	// touches a pin, a netflag/netport/netlabel, or a DIFFERENT wire.
	let zeroLengthWires = 0;
	let danglingWires = 0;
	const STRAY_CAP = 50;
	const byWire = new Map<string, CheckWireSegment[]>();
	for (const s of wireSegs) {
		const own = byWire.get(s.wirePrimitiveId) ?? [];
		own.push(s); byWire.set(s.wirePrimitiveId, own);
	}
	for (const [wirePid, own] of byWire) {
		const verts: Array<[number, number]> = own.flatMap(s => [[s.seg[0], s.seg[1]], [s.seg[2], s.seg[3]]] as Array<[number, number]>);
		const wnet = own[0].net;

		// Zero-length: every vertex coincides with the first (within eps).
		const isZero = verts.every(v => Math.hypot(v[0] - verts[0][0], v[1] - verts[0][1]) <= CHECK_EPS);
		if (isZero) {
			zeroLengthWires++;
			if (findings.filter(f => f.type === 'zero-length-wire').length < STRAY_CAP) {
				findings.push({
					type: 'zero-length-wire',
					level: 'warn',
					wirePrimitiveId: wirePid || undefined,
					at: { x: verts[0][0], y: verts[0][1] },
					message: '零长度导线(首尾坐标相同,不连接任何东西,应删除)',
				});
			}
			continue;
		}

		// Classify each FREE END of the wire separately: does it touch a pin, and
		// does it touch a marker or a DIFFERENT wire? Two subtleties, both live-bitten:
		// (1) issue #51 — per-end classification (not verts.some) so a pin-anchored
		//     stub with a floating far end is caught;
		// (2) 2026-08-12 — the platform MERGES same-net wires that share endpoints
		//     into ONE primitive whose line is a SEGMENT ARRAY ((x1,y1,x2,y2)×N in
		//     arbitrary order), NOT a polyline. Reading verts[0]/verts[last] as "the
		//     two ends" then picks interior corner points and a perfectly-connected
		//     3-segment L-run reports dangling. Free ends are the DEGREE-1 vertices
		//     of the original segment graph; never infer a second encoding here.
		const endpoints: Array<[number, number]> = (() => {
			const segs: Array<[[number, number], [number, number]]> = own.map(s => [[s.seg[0], s.seg[1]], [s.seg[2], s.seg[3]]]);
			const deg = new Map<string, { v: [number, number]; n: number }>();
			const keyOf = (v: [number, number]) => `${Math.round(v[0] * 100)}:${Math.round(v[1] * 100)}`;
			for (const [a, b] of segs) {
				for (const v of [a, b]) {
					const k = keyOf(v);
					const e = deg.get(k);
					if (e) e.n++;
					else deg.set(k, { v, n: 1 });
				}
			}
			const free: Array<[number, number]> = [];
			for (const { v, n } of deg.values()) {
				if (n === 1) free.push(v);
			}
			// A closed loop (no degree-1 vertex) degrades to the old first/last pick.
			return free.length > 0 ? free : [verts[0], verts[verts.length - 1]];
		})();
		const touchOf = (v: [number, number]) => ({
			touchesPin: allPins.some(p => Math.hypot(v[0] - p.x, v[1] - p.y) <= COINCIDE_TOL),
			touchesMarker: connectionMarkers.some(m => Math.hypot(v[0] - m.x, v[1] - m.y) <= COINCIDE_TOL)
				|| wireSegs.some(ws => ws.wirePrimitiveId !== wirePid
					&& pointOnSegment(v[0], v[1], ws.seg[0], ws.seg[1], ws.seg[2], ws.seg[3])),
		});
		const ends = endpoints.map(touchOf);
		const verdict = classifyWireConnectivity(ends, wnet);
		if (verdict !== 'connected') {
			danglingWires++;
			if (findings.filter(f => f.type === 'dangling-wire').length < STRAY_CAP) {
				const orphan = verdict === 'orphan-stub';
				findings.push({
					type: 'dangling-wire',
					level: 'warn',
					wirePrimitiveId: wirePid || undefined,
					wireNet: orphan ? wnet : undefined,
					at: { x: verts[0][0], y: verts[0][1] },
					message: orphan
						? `疑似孤儿 stub(一端连引脚、另一端游离,网名 ${wnet} 为 EasyEDA 自动生成 — flag/port 疑似已删除,wire 残留;用 sch disconnect 清除)`
						: `悬挂导线(两端不接任何引脚/标识/导线${wnet ? '' : '、空网名'},孤立残留)`,
				});
			}
		}
	}

	const summary = {
		floatingPins: floatingTotal,
		componentsWithFloating,
		geomNetMismatches,
		netMarkerMismatches,
		multiNetWires,
		wireCrossings: crossingTotal,
		wireOverPins: overPinTotal,
		zeroLengthWires,
		danglingWires,
		polarityConventionOutliers,
		total: findings.length,
	};
	return { result: { passed: findings.every(f => f.level === 'info'), summary, findings } };
};

// ─── Bridge check (tree-granularity net-vs-copper consistency) ─────────
//
// `sch check`'s multi-net-wire rule works per SINGLE wire primitive, but when
// EasyEDA merges two collinear touching stubs of DIFFERENT nets into one tree,
// the short spans SEVERAL wires — no single wire carries two net names, so the
// per-wire rule under-reports. bridge-check groups actual segments by physical
// endpoint/T/overlap contact (bare X stays separate) and aggregates wire/marker
// anchored on that tree:
//   • len(set(nets)) > 1  → BRIDGE (real short, ERROR)
//   • nets empty & tree touches a SINGLE pin → ORPHAN (dangling stub, WARN)
//   • tree touches NO pin at all → ORPHAN_TREE (move-residue flag+stub or bare
//     dead wire, WARN) — the form both ORPHAN and ORPHAN_FLAG were blind to
// Read-only: reports wire ids / flag ids / touched pins (designator:pin) per
// problem tree so the fix (integral-tree delete + occupancy-aware reconnect)
// can be driven by hand or a later --repair pass (issue #73).

interface BridgeTree {
	kind: 'BRIDGE' | 'ORPHAN' | 'ORPHAN_FLAG' | 'ORPHAN_TREE';
	wireIds: Array<string>;
	flagIds: Array<string>;
	pins: Array<string>; // "designator:pin"
	nets: Array<string>;
	segments?: Array<{ primitiveId: string; segmentIndex: number; rawEncoding: string }>;
}

const schematicBridgeCheck: Handler = async (payload) => {
	const allPages = optionalBoolean(payload, 'allPages') === true;
	let components, wires;
	try {
		components = await eda.sch_PrimitiveComponent.getAll(undefined, allPages);
		wires = await eda.sch_PrimitiveWire.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic for bridge check.');
	}
	if (!Array.isArray(components) || !Array.isArray(wires)) throw new Error('Incomplete bridge component/wire enumeration.');
	// Same degenerate-primitive removal as `schematic.check`: without it a single
	// zero-length wire made the whole bridge check throw. See connectivityWireSegments.
	const wireSegs = connectivityWireSegments(collectWireSegments(wires));
	const COINCIDE_TOL = CHECK_EPS * 8;

	// Netflags/netports/netlabels carry the net name we aggregate per tree.
	const NET_MARKER_TYPES = new Set(['netflag', 'netport', 'netlabel', 'short_symbol']);
	const markers: Array<{ x: number; y: number; net: string; primitiveId: string }> = [];
	const pins: Array<{ designator: string; number: string; x: number; y: number }> = [];
	for (const c of components ?? []) {
		let type: string;
		try { type = String(c.getState_ComponentType?.() ?? ''); }
		catch (err) { throw new Error(`Bridge component identity unavailable: ${describeThrown(err)}`); }
		if (NET_MARKER_TYPES.has(type)) {
			try {
				markers.push({ x: c.getState_X(), y: c.getState_Y(), net: String(c.getState_Net?.() ?? ''), primitiveId: String(c.getState_PrimitiveId?.() ?? '') });
			}
			catch (err) { throw new Error(`Bridge marker geometry unavailable: ${describeThrown(err)}`); }
			continue;
		}
		const primitiveId = c.getState_PrimitiveId();
		let compPins;
		try { compPins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(primitiveId); }
		catch (err) { throw new Error(`Bridge pin geometry unavailable: ${describeThrown(err)}`); }
		if (!Array.isArray(compPins)) throw new Error('Bridge pin API did not return an array.');
		if (compPins.length === 0) continue;
		const designator = String(c.getState_Designator?.() ?? '');
		for (const p of compPins) {
			try { pins.push({ designator, number: String(p.getState_PinNumber?.() ?? ''), x: p.getState_X(), y: p.getState_Y() }); }
			catch (err) { throw new Error(`Bridge pin coordinates unavailable: ${describeThrown(err)}`); }
		}
	}
	if ([...pins, ...markers].some(p => !Number.isFinite(p.x) || !Number.isFinite(p.y))) throw new Error('Non-finite bridge anchor geometry.');

	// Physical segment islands, never primitive-ID unions. An anchor at X is
	// explicitly conductive and cannot use the bare-crossing exception.
	const treeMap = new Map(physicalWireIslands(wireSegs, [...pins, ...markers]).map((indices, i) => [i, {
		wireIds: new Set(indices.map(j => wireSegs[j].wirePrimitiveId)),
		segs: indices.map(j => wireSegs[j].seg),
		wireNets: new Set(indices.map(j => wireSegs[j].net).filter(Boolean)),
		sources: indices.map(j => ({ primitiveId: wireSegs[j].wirePrimitiveId, segmentIndex: wireSegs[j].segmentIndex, rawEncoding: wireSegs[j].rawEncoding })),
	}]));
	const distToSeg = (px: number, py: number, x0: number, y0: number, x1: number, y1: number): number =>
		wirePointOnSegment({ x: px, y: py }, [x0, y0, x1, y1]) ? 0 : Number.POSITIVE_INFINITY;

	const trees: Array<BridgeTree> = [];
	const anchoredMarkers = new Set<string>();
	for (const t of treeMap.values()) {
		const onTree = (x: number, y: number) => t.segs.some(s => distToSeg(x, y, s[0], s[1], s[2], s[3]) <= COINCIDE_TOL);
		const flagIds: Array<string> = [];
		const nets = new Set<string>(t.wireNets);
		for (const m of markers) {
			if (!onTree(m.x, m.y)) continue;
			if (m.primitiveId) { flagIds.push(m.primitiveId); anchoredMarkers.add(m.primitiveId); }
			if (m.net) nets.add(m.net);
		}
		const touchedPins: Array<string> = [];
		for (const p of pins) {
			if (onTree(p.x, p.y)) touchedPins.push(`${p.designator}:${p.number}`);
		}
		const netList = [...nets];
		if (netList.length > 1) {
			trees.push({ kind: 'BRIDGE', wireIds: [...t.wireIds], flagIds, pins: [...new Set(touchedPins)], nets: netList, segments: t.sources });
		}
		else if (flagIds.length === 0 && touchedPins.length > 0) {
			// A flagless tree touching 2+ DISTINCT pins is a legal pin-to-pin direct
			// connection (the net just gets an auto name like $2N1792) — only a tree
			// stuck on a SINGLE pin is a dangling stub (live 2026-08-12: the LED
			// direct-wire replacing a face-to-face netport pair was false-flagged).
			const uniquePins = [...new Set(touchedPins)];
			if (uniquePins.length < 2) {
				trees.push({ kind: 'ORPHAN', wireIds: [...t.wireIds], flagIds, pins: uniquePins, nets: netList, segments: t.sources });
			}
		}
		else if (touchedPins.length === 0) {
			// A wire tree that touches NO pin at all: either flag(s)+stub left behind
			// by a component move (live 2026-08-18: two GND flag+stub trees survived
			// C4/SW2 moves — ORPHAN requires touched pins and ORPHAN_FLAG requires no
			// wire, so BOTH were structurally blind to this form), or a bare wire tree
			// with neither flags nor pins (dead copper). netList.length===1 lands here
			// too — a single-net tree with zero pins contributes nothing electrically.
			trees.push({ kind: 'ORPHAN_TREE', wireIds: [...t.wireIds], flagIds, pins: [], nets: netList, segments: t.sources });
		}
	}

	// ── Orphan FLAGS: a netflag/netport attached to NO wire at all (issue #137).
	// These are left behind when a merged wire is deleted out from under its flag
	// (or a half-failed connect). They are invisible boobytraps: the next wire
	// drawn through that point silently inherits the stray net name.
	for (const m of markers) {
		if (!m.primitiveId || anchoredMarkers.has(m.primitiveId)) continue;
		let onAnyWire = false;
		for (const t of treeMap.values()) {
			if (t.segs.some(s => distToSeg(m.x, m.y, s[0], s[1], s[2], s[3]) <= COINCIDE_TOL)) { onAnyWire = true; break; }
		}
		if (onAnyWire) continue;
		// A flag sitting directly ON a pin (no wire) is the classic fake-connection
		// anti-pattern — report the pin so the fix is obvious.
		const touchedPins = pins.filter(p => Math.hypot(p.x - m.x, p.y - m.y) <= COINCIDE_TOL)
			.map(p => `${p.designator}:${p.number}`);
		trees.push({ kind: 'ORPHAN_FLAG', wireIds: [], flagIds: [m.primitiveId], pins: touchedPins, nets: m.net ? [m.net] : [] });
	}

	const bridges = trees.filter(t => t.kind === 'BRIDGE').length;
	const orphans = trees.filter(t => t.kind === 'ORPHAN').length;
	const orphanFlags = trees.filter(t => t.kind === 'ORPHAN_FLAG').length;
	const orphanTrees = trees.filter(t => t.kind === 'ORPHAN_TREE').length;
	const summary = { trees: trees.length, bridges, orphans, orphanFlags, orphanTrees, wireTreesTotal: treeMap.size };
	return { result: { passed: trees.length === 0, summary, trees } };
};

// ─── Save ─────────────────────────────────────────────────────────────

const schematicSave: Handler = async () => {
	let saved;
	try {
		saved = await eda.sch_Document.save();
	}
	catch (err) {
		throw edaError(err, 'Failed to save schematic.');
	}
	return { result: { saved } };
};

// ─── Export ───────────────────────────────────────────────────────────

const schematicExportNetlist: Handler = async (payload) => {
	const fileName = optionalString(payload, 'fileName');
	const netlistType = payload.netlistType as ESYS_NetlistType | undefined;
	let file;
	try {
		file = await eda.sch_ManufactureData.getNetlistFile(fileName, netlistType);
	}
	catch (err) {
		throw edaError(err, 'Failed to export netlist.');
	}
	if (!file) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Netlist export returned no file.');
	}
	const artifact = await blobToArtifact(
		file,
		'schematic_netlist',
		file.name || `${fileName ?? 'netlist'}.net`,
		'text/plain',
	);
	return { result: { artifactId: artifact.id, netlistType: netlistType ?? null }, artifacts: [artifact] };
};

/**
 * `object` values `getExportDocumentFile` ACTUALLY accepts.
 *
 * ⚠️ The published type declaration is WRONG. `@jlceda/pro-api-types` declares
 * `'All Schematic' | 'Current Schematic' | 'Current Schematic Page'` — **none of
 * those three work**. The real literals, read out of the shipped `sch-main.js`
 * (`function k$(i){return["Current Page","Current Page Selected Items"].includes(i)}`),
 * are the ones below.
 *
 * Passing a declared-but-wrong literal does not throw: `k$()` returns false, the
 * value falls through to `Z.pureSchematics[<bad key>].sort`, that TypeErrors,
 * the rejection is never caught internally, and **the promise you awaited neither
 * resolves nor rejects** — the editor just shows a 1% progress toast forever
 * (live-verified: two hung sessions, 90s+, no console error beyond a bare
 * `Uncaught (in promise)`). Hence the timeout guard in schematicExportImage.
 *
 * DO NOT "fix" these strings to match the .d.ts.
 */
const SCH_EXPORT_OBJECT: Record<string, string> = {
	selection: 'Current Page Selected Items',
	page: 'Current Page',
	project: 'Project',
};

const SCH_EXPORT_FORMAT: Record<string, { fileType: string; ext: string; mime: string }> = {
	svg: { fileType: 'SVG', ext: 'svg', mime: 'image/svg+xml' },
	png: { fileType: 'PNG', ext: 'png', mime: 'image/png' },
	pdf: { fileType: 'PDF', ext: 'pdf', mime: 'application/pdf' },
};

/** Upper bound for one export. A correct call is ~100-200ms; anything near this
 *  means the platform swallowed the request, and we must not hold the action
 *  queue hostage waiting on a promise that will never settle. */
const SCH_EXPORT_TIMEOUT_MS = 30_000;

/**
 * Export the active schematic page — or just the SELECTED primitives — as
 * SVG / PNG / PDF (issue #166).
 *
 * The selection scope is the point: a full-page snapshot of a dense sheet is
 * unreadable, and the pre-existing `view region` + `snapshot --no-fit` path is
 * viewport-dependent (a backgrounded tab never repaints, so it silently returns
 * the previous full-page frame). This renders the requested primitives directly:
 * no viewport, no foreground requirement, no dialog — and SVG is vector, so the
 * agent can zoom without resampling.
 */
const schematicExportImage: Handler = async (payload) => {
	const format = (optionalString(payload, 'format') ?? 'svg').toLowerCase();
	const spec = SCH_EXPORT_FORMAT[format];
	if (!spec) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Unsupported format "${format}". Use one of: ${Object.keys(SCH_EXPORT_FORMAT).join(', ')}.`,
		);
	}

	const rawIds = payload.primitiveIds;
	let ids: Array<string> | undefined;
	if (typeof rawIds === 'string') ids = [rawIds];
	else if (Array.isArray(rawIds) && rawIds.every(id => typeof id === 'string')) ids = rawIds as Array<string>;
	else if (rawIds !== undefined) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, '"primitiveIds" must be a string or string[].');
	}

	const scope = optionalString(payload, 'scope') ?? (ids && ids.length ? 'selection' : 'page');
	const objectLiteral = SCH_EXPORT_OBJECT[scope];
	if (!objectLiteral) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Unknown scope "${scope}". Use one of: ${Object.keys(SCH_EXPORT_OBJECT).join(', ')}.`,
		);
	}

	// Explicit ids drive the selection; otherwise a 'selection' scope exports
	// whatever the user/agent selected earlier.
	if (ids && ids.length) {
		try {
			await eda.sch_SelectControl.doSelectPrimitives(ids);
		}
		catch (err) {
			throw edaError(err, 'Failed to select the primitives to export.');
		}
	}
	let selected: Array<string> = [];
	try {
		selected = (await eda.sch_SelectControl.getAllSelectedPrimitives_PrimitiveId()) ?? [];
	}
	catch { /* selection read is advisory */ }
	if (scope === 'selection' && selected.length === 0) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			'Nothing is selected, so a selection export would be empty. Pass primitiveIds, or use scope "page".',
		);
	}

	const fileName = optionalString(payload, 'fileName') ?? `schematic-export.${spec.ext}`;
	const typeParams = {
		theme: (optionalString(payload, 'theme') ?? 'Default') as 'Default',
		lineWidth: (optionalString(payload, 'lineWidth') ?? 'Default') as 'Default',
	};

	let file: File | undefined;
	try {
		file = await withTimeout(
			eda.sch_ManufactureData.getExportDocumentFile(
				fileName,
				spec.fileType as ESCH_ExportDocumentFileType,
				typeParams,
				objectLiteral,
			),
			SCH_EXPORT_TIMEOUT_MS,
			`Export did not settle within ${SCH_EXPORT_TIMEOUT_MS}ms. The platform drops the request without rejecting when it dislikes an argument — the editor will show a stuck progress toast; reload the document to clear it.`,
		);
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to export the schematic image.');
	}
	finally {
		// The ManufactureData export pipeline LEAKS its progress toast: the export
		// resolves (2-3s, file delivered) but the editor's progress bar stays stuck
		// at 99% until the user closes it by hand (live-reported twice). Tear it
		// down explicitly — destroyProgressBar/destroyLoading are @public and
		// idempotent (safe with no bar showing), verified live via debug exec:
		// showProgressBar(99) → destroyProgressBar() clears the stuck toast.
		// Delay past the platform's own settle so we don't race its teardown; on
		// the timeout path this also clears the stuck-at-1% toast the error
		// message used to tell the user to clear by reloading.
		setTimeout(() => {
			try { eda.sys_LoadingAndProgressBar.destroyProgressBar(); } catch { /* best-effort */ }
			try { eda.sys_LoadingAndProgressBar.destroyLoading(); } catch { /* best-effort */ }
		}, 400);
	}
	if (!file) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Export returned no file.');
	}

	const artifact = await blobToArtifact(file, 'schematic_export', file.name || fileName, spec.mime);
	return {
		result: {
			artifactId: artifact.id,
			format,
			scope,
			selectedCount: selected.length,
			bytes: file.size,
			fileName: file.name || fileName,
		},
		artifacts: [artifact],
	};
};

/**
 * Reject after ms rather than await forever.
 *
 * 截止时间走 `deadlines.ts`,**不是**裸 setTimeout:后台窗口里主线程定时器会被
 * 节流/冻结,那正是 2026-08-24 真机上「7s 的 per-op 闸门在 6 分钟里没响、队首
 * 一直堵着」的根因(详见 deadlines.ts 头注释)。快路径仍是 setTimeout,保底路径
 * 是 transport 的 worker tick。
 */
function withTimeout<T>(p: Promise<T>, ms: number, message: string): Promise<T> {
	return new Promise<T>((resolve, reject) => {
		const handle = armDeadline(ms, () => reject(new ActionError(ErrorCodes.EDA_CALL_FAILED, message)));
		p.then(
			(v) => { handle.cancel(); resolve(v); },
			(e) => { handle.cancel(); reject(e); },
		);
	});
}

// schematic.read — ONE call that returns a coherent semantic snapshot of the
// circuit, so the agent doesn't stitch components.list + netlist + check itself.
// Components (with each pin's net, from the JSON-authoritative netlist — reuses
// the #1 collectNetlistPinNets logic), nets (net → connected pins + degree +
// global flag), floating pins, and the geometric design check (schematicCheck;
// pass includeCheck:false to skip it for a faster read).
const schematicRead: Handler = async (payload) => {
	const allPages = optionalBoolean(payload, 'allPages') === true;
	const includeCheck = optionalBoolean(payload, 'includeCheck') !== false; // default true

	let comps;
	try {
		comps = await eda.sch_PrimitiveComponent.getAll(undefined, allPages);
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic components.');
	}

	// JSON-authoritative pin→net per designator (same source as schematic.check).
	const { byDesignator: pinNets, available: netlistAvailable, error: netlistError } = await collectNetlistPinNets(allPages);

	const netToPins = new Map<string, Array<string>>();
	const floating: Array<string> = [];
	const components: Array<Record<string, unknown>> = [];

	for (const c of comps ?? []) {
		const designator = String(c.getState_Designator?.() ?? '');
		const pinNetMap = pinNets.get(designator) ?? new Map<string, string>();
		const pins: Array<Record<string, unknown>> = [];
		try {
			const pinPrims = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(c.getState_PrimitiveId());
			for (const p of pinPrims ?? []) {
				const number = String(p.getState_PinNumber?.() ?? '');
				const net = pinNetMap.get(number) ?? '';
				if (net) {
					const key = `${designator}.${number}`;
					const list = netToPins.get(net) ?? [];
					list.push(key);
					netToPins.set(net, list);
				}
				else if (designator && number) {
					floating.push(`${designator}.${number}`);
				}
				pins.push({ number, name: p.getState_PinName?.() ?? '', net: net || null });
			}
		}
		catch { /* pins best-effort */ }
		components.push({
			designator,
			// The MUTATION handle: what select / component.delete / modify /
			// prim-delete / replace / rebind consume. Was MISSING from read's
			// output, so agents grabbed the only id-shaped field — uniqueId
			// ("gge…") — and every by-id mutation came back notFound/empty
			// (live-incident on motobox 2026-08-05). Keep both, never mix.
			primitiveId: c.getState_PrimitiveId?.() ?? '',
			componentType: c.getState_ComponentType?.() ?? '',
			name: c.getState_Name?.() ?? '',
			uniqueId: c.getState_UniqueId?.() ?? '', // sch↔PCB link key (for pcb.add_component) — NOT a primitiveId
			footprint: c.getState_Footprint?.() ?? '',
			supplierId: c.getState_SupplierId?.() ?? '', // LCSC C-number when present
			x: c.getState_X?.(),
			y: c.getState_Y?.(),
			pins,
		});
	}

	const nets = [...netToPins.entries()]
		.map(([net, pins]) => ({ net, pins, degree: pins.length, isGlobal: isGlobalNetName(net) }))
		.sort((a, b) => a.net.localeCompare(b.net));

	let check: unknown = null;
	if (includeCheck) {
		try {
			check = (await schematicCheck(payload)).result;
		}
		catch (err) {
			check = { error: describeThrown(err) };
		}
	}

	return {
		result: {
			components,
			componentCount: components.length,
			nets,
			netCount: nets.length,
			floatingPins: floating,
			floatingPinCount: floating.length,
			// When the netlist is muted, EVERY pin lands in floatingPins and every
			// per-pin net is null. That is not a statement about the design; the flag
			// (and the cause) have to travel with the data so nobody reads it as one.
			netlistAvailable,
			...(netlistAvailable ? {} : { netlistError: netlistError ?? 'netlist unavailable' }),
			check,
		},
	};
};

const schematicExportBom: Handler = async (payload) => {
	const fileName = optionalString(payload, 'fileName');
	const fileType = (optionalString(payload, 'fileType') as 'xlsx' | 'csv' | undefined) ?? 'xlsx';
	const template = optionalString(payload, 'template');
	const filterOptions = payload.filterOptions as Parameters<typeof eda.sch_ManufactureData.getBomFile>[3] | undefined;
	const statistics = payload.statistics as Array<string> | undefined;
	const property = payload.property as Array<string> | undefined;
	const columns = payload.columns as Parameters<typeof eda.sch_ManufactureData.getBomFile>[6] | undefined;

	let file;
	try {
		file = await eda.sch_ManufactureData.getBomFile(
			fileName,
			fileType,
			template,
			filterOptions,
			statistics,
			property,
			columns,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to export BOM.');
	}
	if (!file) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'BOM export returned no file.');
	}
	const fallbackMime = fileType === 'csv'
		? 'text/csv'
		: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';
	const artifact = await blobToArtifact(
		file,
		'schematic_bom',
		file.name || `${fileName ?? 'bom'}.${fileType}`,
		fallbackMime,
	);
	return { result: { artifactId: artifact.id, fileType }, artifacts: [artifact] };
};

// ─── Library search ──────────────────────────────────────────────────

const schematicLibrarySearch: Handler = async (payload) => {
	const query = requireString(payload, 'query');
	const limit = optionalNumber(payload, 'limit') ?? 10;
	const allowFuzzy = optionalBoolean(payload, 'allowFuzzy') ?? false;
	const libraryUuid = optionalString(payload, 'libraryUuid');

	let raw: Array<unknown>;
	try {
		raw = await eda.lib_Device.search(query, libraryUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to search device library.');
	}
	if (!Array.isArray(raw)) {
		return { result: { count: 0, components: [] } };
	}

	// Exact LCSC mode. When the query is itself a bare C-number (e.g. "C5665"),
	// EasyEDA's free-text search still ranks by keyword — so "C5665" surfaces the
	// op-amp CLC5665IMX (name contains "5665") over the real part whose LCSC id
	// equals C5665. Strictly filter the raw results by the lcsc/supplierId field so
	// batch selection never silently binds the wrong device. Opt out with
	// allowFuzzy to fall through to the ranked free-text path below.
	if (!allowFuzzy && isLcscQuery(query)) {
		const exact = filterExactLcsc(raw as Array<Record<string, unknown>>, query)
			.slice(0, limit)
			.map((r) => {
				const otherProperty = (r.otherProperty as Record<string, unknown> | undefined) ?? {};
				return {
					uuid: r.uuid,
					libraryUuid: r.libraryUuid,
					name: r.name,
					value: otherProperty.Value,
					footprintName: r.footprintName,
					symbolName: r.symbolName,
					lcsc: r.supplierId ?? otherProperty['Supplier Part'],
					manufacturer: r.manufacturer ?? otherProperty.Manufacturer,
					manufacturerId: r.manufacturerId ?? otherProperty['Manufacturer Part'],
					description: typeof r.description === 'string' ? r.description.slice(0, 200) : r.description,
				};
			});
		if (exact.length === 0) {
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`No device exactly matches LCSC id "${query}". The raw search returned ${raw.length} `
				+ 'fuzzy candidate(s) whose LCSC field differs — re-run with allowFuzzy (CLI: --allow-fuzzy) '
				+ 'to see them, or use "lib by-lcsc" for a deterministic lookup.',
			);
		}
		return { result: { count: exact.length, query, exactMatch: true, components: exact } };
	}

	// Relevance rerank. EasyEDA's raw order often surfaces the wrong category first
	// (e.g. "100nF 0402" returns resistors before the capacitor). Score each
	// candidate by how many query terms hit its fields — weighted name/value/MPN >
	// footprint/symbol/manufacturer > description — then stable-sort (original order
	// breaks ties, so a zero-match query degrades gracefully to EasyEDA's order).
	const terms = query.toLowerCase().split(/[\s,]+/).filter(Boolean);
	const norm = (s: unknown) => String(s ?? '').toLowerCase();
	const scoreOf = (r: Record<string, unknown>): number => {
		const op = (r.otherProperty as Record<string, unknown> | undefined) ?? {};
		const strong = `${norm(r.name)} ${norm(op.Value)} ${norm(r.manufacturerId)}`;
		const mid = `${norm(r.footprintName)} ${norm(r.symbolName)} ${norm(r.manufacturer)}`;
		const weak = norm(r.description);
		let s = 0;
		for (const t of terms) {
			if (strong.includes(t)) s += 3;
			else if (mid.includes(t)) s += 2;
			else if (weak.includes(t)) s += 1;
		}
		return s;
	};
	const ranked = (raw as Array<Record<string, unknown>>)
		.map((d, i) => ({ d, i, s: scoreOf(d) }))
		.sort((a, b) => (b.s - a.s) || (a.i - b.i))
		.slice(0, limit);

	const components = ranked.map(({ d: r, s }) => {
		const otherProperty = (r.otherProperty as Record<string, unknown> | undefined) ?? {};
		return {
			uuid: r.uuid,
			libraryUuid: r.libraryUuid,
			name: r.name,
			value: otherProperty.Value,
			footprintName: r.footprintName,
			symbolName: r.symbolName,
			lcsc: r.supplierId,
			manufacturer: r.manufacturer,
			manufacturerId: r.manufacturerId,
			score: s,
			description: typeof r.description === 'string' ? r.description.slice(0, 200) : r.description,
		};
	});

	return { result: { count: components.length, query, components } };
};

/**
 * Resolve one or more LCSC C-numbers directly to device-library identity via
 * `eda.lib_Device.getByLcscIds` — the deterministic counterpart to the free-text
 * search. Returns the same projected component shape ({ libraryUuid, uuid, … })
 * that `schematic.component.place` consumes, plus a `notFound` list for any
 * requested C-number the library did not resolve.
 */
const schematicLibraryGetByLcscIds: Handler = async (payload) => {
	const rawIds = payload.lcscIds;
	let lcscIds: Array<string>;
	if (typeof rawIds === 'string') lcscIds = [rawIds];
	else if (Array.isArray(rawIds) && rawIds.every(id => typeof id === 'string')) lcscIds = rawIds as Array<string>;
	else {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "lcscIds" (a string or string[] of LCSC C-numbers, e.g. "C6186").',
		);
	}

	let raw: Array<unknown>;
	try {
		// The array overload returns Array<ILIB_DeviceSearchItem> (same record
		// shape as lib_Device.search).
		raw = await eda.lib_Device.getByLcscIds(lcscIds, undefined, true);
	}
	catch (err) {
		throw edaError(err, 'Failed to look up devices by LCSC id.');
	}
	if (!Array.isArray(raw)) {
		return { result: { count: 0, requested: lcscIds, components: [], notFound: lcscIds } };
	}

	const components = (raw as Array<Record<string, unknown>>).map((r) => {
		const otherProperty = (r.otherProperty as Record<string, unknown> | undefined) ?? {};
		// supplierId / manufacturer(Id) are deprecated top-level fields, moved into
		// otherProperty (canonical EasyEDA property names). Read top-level first —
		// current builds still emit it — then fall back to otherProperty.
		return {
			uuid: r.uuid,
			libraryUuid: r.libraryUuid,
			name: r.name,
			value: otherProperty.Value,
			footprintName: r.footprintName,
			symbolName: r.symbolName,
			lcsc: r.supplierId ?? otherProperty['Supplier Part'],
			manufacturer: r.manufacturer ?? otherProperty.Manufacturer,
			manufacturerId: r.manufacturerId ?? otherProperty['Manufacturer Part'],
			description: typeof r.description === 'string' ? r.description.slice(0, 200) : r.description,
		};
	});

	// notFound must never INVERT: if no C-number could be read back (e.g. a future
	// build stops emitting supplierId), report nothing missing rather than falsely
	// claiming every resolved part is missing.
	const found = new Set(components.map(c => String(c.lcsc ?? '')).filter(Boolean));
	const notFound = found.size ? lcscIds.filter(id => !found.has(id)) : [];
	return {
		result: {
			count: components.length,
			requested: lcscIds,
			components,
			...(notFound.length ? { notFound } : {}),
		},
	};
};

// ─── Library asset authoring: symbol + footprint + device ────────────────

const libraryList: Handler = async () => {
	try {
		const [libraries, personalLibraryUuid, projectLibraryUuid, systemLibraryUuid] = await Promise.all([
			eda.lib_LibrariesList.getAllLibrariesList(),
			eda.lib_LibrariesList.getPersonalLibraryUuid(),
			eda.lib_LibrariesList.getProjectLibraryUuid(),
			eda.lib_LibrariesList.getSystemLibraryUuid(),
		]);
		return {
			result: {
				libraries: Array.isArray(libraries) ? libraries : [],
				personalLibraryUuid: personalLibraryUuid ?? null,
				projectLibraryUuid: projectLibraryUuid ?? null,
				systemLibraryUuid: systemLibraryUuid ?? null,
			},
		};
	}
	catch (err) {
		throw edaError(err, 'Failed to read EasyEDA library list.');
	}
};

function optionalStringArray(payload: Record<string, unknown>, key: string): Array<string> | undefined {
	const value = payload[key];
	if (value === undefined) return undefined;
	if (!Array.isArray(value) || !value.every(item => typeof item === 'string')) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `"${key}" must be a string array.`);
	}
	return value as Array<string>;
}

async function resolveWritableLibraryUuid(payload: Record<string, unknown>): Promise<string> {
	const explicit = optionalString(payload, 'libraryUuid');
	if (explicit) return explicit;
	const scope = optionalString(payload, 'scope') ?? 'personal';
	let uuid: string | undefined;
	if (scope === 'personal') uuid = await eda.lib_LibrariesList.getPersonalLibraryUuid();
	else if (scope === 'project') uuid = await eda.lib_LibrariesList.getProjectLibraryUuid();
	else throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, '"scope" must be "personal" or "project".');
	if (!uuid) throw new ActionError(ErrorCodes.INVALID_STATE, `EasyEDA did not expose a ${scope} library UUID.`);
	return uuid;
}

function normalizeLibraryNamePart(value: string, fallback: string): string {
	const normalized = value.normalize('NFKC').trim().toUpperCase()
		.replace(/[^\p{L}\p{N}]+/gu, '_')
		.replace(/^_+|_+$/g, '')
		.replace(/_+/g, '_');
	return normalized || fallback;
}

/** Keep agent-authored personal-library assets reusable and visibly separate. */
async function namespacedLibraryAssetName(requestedName: string): Promise<{ name: string; requestedName: string; namespace: string }> {
	const assetMark = normalizeLibraryNamePart(requestedName, 'ASSET');
	const namespace = 'EA_AGENT';
	const prefix = `${namespace}__`;
	return {
		name: requestedName.toUpperCase().startsWith(prefix) ? requestedName : `${prefix}${assetMark}`,
		requestedName,
		namespace,
	};
}

const libraryFootprintCreate: Handler = async (payload) => {
	const requestedName = requireString(payload, 'name');
	const description = optionalString(payload, 'description');
	const classification = optionalStringArray(payload, 'classification');
	try {
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const uuid = await eda.lib_Footprint.create(libraryUuid, name, classification, description);
		if (!uuid) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after creating the footprint.');
		const footprint = await eda.lib_Footprint.get(uuid, libraryUuid);
		if (!footprint) {
			return { result: { partial: true, created: { uuid, libraryUuid, name }, verified: false }, warnings: ['Footprint was created but immediate readback returned no record; do not retry blindly.'] };
		}
		return { result: { uuid, libraryUuid, name, requestedName, namespace, verified: true, footprint } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create footprint library asset.');
	}
};

const libraryFootprintGet: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = optionalString(payload, 'libraryUuid');
	try {
		const footprint = await eda.lib_Footprint.get(uuid, libraryUuid);
		if (!footprint) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Footprint "${uuid}" was not found.`);
		return { result: { footprint } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to read footprint library asset.');
	}
};

const libraryFootprintCopy: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const sourceLibraryUuid = requireString(payload, 'sourceLibraryUuid');
	const requestedName = requireString(payload, 'name');
	const classification = optionalStringArray(payload, 'classification');
	try {
		const source = await eda.lib_Footprint.get(uuid, sourceLibraryUuid);
		if (!source) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Source footprint "${uuid}" was not found.`);
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const copiedUuid = await eda.lib_Footprint.copy(uuid, sourceLibraryUuid, libraryUuid, classification, name);
		if (!copiedUuid) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after copying the footprint.');
		const footprint = await eda.lib_Footprint.get(copiedUuid, libraryUuid);
		if (!footprint) return {
			result: { partial: true, created: { uuid: copiedUuid, libraryUuid, name }, source: { uuid, libraryUuid: sourceLibraryUuid }, verified: false },
			warnings: ['Footprint copy returned a UUID but immediate readback was empty; do not retry blindly.'],
		};
		return { result: { uuid: copiedUuid, libraryUuid, name, requestedName, namespace, source: { uuid, libraryUuid: sourceLibraryUuid }, verified: true, footprint } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to copy footprint library asset.'); }
};

const libraryFootprintDelete: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const expectedName = requireString(payload, 'expectedName');
	try {
		const before = await eda.lib_Footprint.get(uuid, libraryUuid);
		if (!before) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Footprint "${uuid}" was not found; nothing was deleted.`);
		if (before.name !== expectedName) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Footprint name mismatch: expected "${expectedName}", found "${before.name}".`);
		if (!await eda.lib_Footprint.delete(uuid, libraryUuid)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `EasyEDA refused to delete footprint "${expectedName}".`);
		let after;
		try { after = await eda.lib_Footprint.get(uuid, libraryUuid); }
		catch { after = undefined; /* SDK throws an opaque object for a missing footprint. */ }
		if (after) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Footprint "${expectedName}" still exists after delete returned success.`);
		return { result: { uuid, libraryUuid, name: expectedName, deleted: true, verified: true } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to delete footprint library asset.'); }
};

type FootprintPadSpec = {
	number: string; layer: number; x: number; y: number; rotation: number;
	shape: TPCB_PrimitivePadShape; hole: TPCB_PrimitivePadHole | null;
	metallization: boolean; padType: EPCB_PrimitivePadType;
};
type FootprintLineSpec = {
	layer: number; startX: number; startY: number; endX: number; endY: number; width: number;
};

function finiteField(raw: Record<string, unknown>, key: string, label: string): number {
	const value = raw[key];
	if (typeof value !== 'number' || !Number.isFinite(value)) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `${label}.${key} must be a finite number.`);
	}
	return value;
}

function parseFootprintBuildSpec(payload: Record<string, unknown>): { pads: FootprintPadSpec[]; lines: FootprintLineSpec[] } {
	const rawPads = payload.pads ?? [];
	const rawLines = payload.lines ?? [];
	if (!Array.isArray(rawPads) || !Array.isArray(rawLines) || rawPads.length + rawLines.length === 0) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Footprint build requires a non-empty pads[] and/or lines[] array.');
	}
	const pads = rawPads.map((value, index): FootprintPadSpec => {
		if (!value || typeof value !== 'object' || Array.isArray(value)) {
			throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pads[${index}] must be an object.`);
		}
		const p = value as Record<string, unknown>;
		if (typeof p.number !== 'string' || !p.number) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pads[${index}].number is required.`);
		if (!Array.isArray(p.shape) || p.shape.length < 3) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pads[${index}].shape must be an official pad-shape tuple.`);
		if (p.hole !== undefined && p.hole !== null && !Array.isArray(p.hole)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pads[${index}].hole must be null or a hole tuple.`);
		return {
			number: p.number,
			layer: finiteField(p, 'layer', `pads[${index}]`),
			x: finiteField(p, 'x', `pads[${index}]`), y: finiteField(p, 'y', `pads[${index}]`),
			rotation: p.rotation === undefined ? 0 : finiteField(p, 'rotation', `pads[${index}]`),
			shape: p.shape as TPCB_PrimitivePadShape,
			hole: (p.hole ?? null) as TPCB_PrimitivePadHole | null,
			metallization: p.metallization === undefined ? true : Boolean(p.metallization),
			padType: (p.padType === undefined ? 0 : finiteField(p, 'padType', `pads[${index}]`)) as EPCB_PrimitivePadType,
		};
	});
	const numbers = new Set<string>();
	for (const p of pads) {
		if (numbers.has(p.number)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Duplicate pad number "${p.number}".`);
		numbers.add(p.number);
	}
	const lines = rawLines.map((value, index): FootprintLineSpec => {
		if (!value || typeof value !== 'object' || Array.isArray(value)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `lines[${index}] must be an object.`);
		const l = value as Record<string, unknown>;
		return {
			layer: finiteField(l, 'layer', `lines[${index}]`),
			startX: finiteField(l, 'startX', `lines[${index}]`), startY: finiteField(l, 'startY', `lines[${index}]`),
			endX: finiteField(l, 'endX', `lines[${index}]`), endY: finiteField(l, 'endY', `lines[${index}]`),
			width: finiteField(l, 'width', `lines[${index}]`),
		};
	});
	return { pads, lines };
}

function primitiveIdOf(primitive: unknown): string {
	if (!primitive || typeof primitive !== 'object') return '';
	const getter = (primitive as { getState_PrimitiveId?: () => unknown }).getState_PrimitiveId;
	const id = typeof getter === 'function' ? getter.call(primitive) : undefined;
	return typeof id === 'string' ? id : '';
}

type LibraryBuildInventoryReader = {
	key: string;
	getAllPrimitiveId: () => Promise<Array<string>>;
};

/**
 * Open and focus a library canvas, then prove that subsequent primitive calls
 * will address the requested asset.  `lib_*.openInEditor()` can return a tab id
 * while leaving the previously focused library tab active (observed in the Web
 * editor while creating the 260919 M3 footprint), so a returned tab id alone is
 * not safe evidence for a write.
 */
async function activateLibraryDocument(
	uuid: string,
	libraryUuid: string,
	libraryType: ELIB_LibraryType.SYMBOL | ELIB_LibraryType.FOOTPRINT,
	expectedDocumentType: EDMT_EditorDocumentType.SYMBOL_COMPONENT | EDMT_EditorDocumentType.FOOTPRINT,
	label: string,
): Promise<string> {
	let splitScreenId: string | undefined;
	try {
		const before = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (before?.tabId) splitScreenId = await eda.dmt_EditorControl.getSplitScreenIdByTabId(before.tabId);
	}
	catch { /* opening without a split id is supported */ }

	const tabId = await eda.dmt_EditorControl.openLibraryDocument(libraryUuid, libraryType, uuid, splitScreenId);
	if (!tabId) throw new ActionError(ErrorCodes.INVALID_STATE, `EasyEDA did not open the ${label} editor.`);
	const activated = await eda.dmt_EditorControl.activateDocument(tabId);
	if (!activated) throw new ActionError(ErrorCodes.INVALID_STATE, `EasyEDA opened ${label} "${uuid}" but did not activate its tab.`);

	let actual: Awaited<ReturnType<typeof eda.dmt_SelectControl.getCurrentDocumentInfo>> | undefined;
	for (let attempt = 0; attempt < 20; attempt++) {
		try { actual = await eda.dmt_SelectControl.getCurrentDocumentInfo(); }
		catch { actual = undefined; }
		if (
			actual?.uuid === uuid &&
			actual.parentLibraryUuid === libraryUuid &&
			actual.documentType === expectedDocumentType
		) return tabId;
		await new Promise<void>(resolve => setTimeout(resolve, 50));
	}
	const actualSummary = actual
		? `uuid=${actual.uuid}, parentLibraryUuid=${actual.parentLibraryUuid ?? '<missing>'}, documentType=${actual.documentType}`
		: 'no current document';
	throw new ActionError(
		ErrorCodes.INVALID_STATE,
		`EasyEDA did not focus the requested ${label} before mutation (${actualSummary}); no geometry was created.`,
	);
}

/**
 * `library.*.build` is a one-shot authoring operation, not append/replace.
 * Opening the editor is read-only; every supported primitive inventory must be
 * proven empty before the first create call.  Unknown/incomplete inventory is
 * refused too, because guessing "empty" would recreate #204's invisible
 * duplicate pads/pins.
 */
async function requireEmptyLibraryBuildTarget(label: string, readers: Array<LibraryBuildInventoryReader>): Promise<void> {
	let inventories: Array<{ key: string; ids: Array<string> }>;
	try {
		inventories = await Promise.all(readers.map(async reader => {
			const ids = await reader.getAllPrimitiveId();
			if (!Array.isArray(ids) || ids.some(id => typeof id !== 'string' || !id)) {
				throw new Error(`${reader.key} inventory returned an invalid primitive-id list`);
			}
			return { key: reader.key, ids };
		}));
	}
	catch (err) {
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			`${label} build could not prove the opened asset is empty; no geometry was created. Inventory error: ${describeThrown(err)}`,
		);
	}
	const occupied = inventories.filter(item => item.ids.length > 0);
	if (!occupied.length) return;
	const count = occupied.reduce((sum, item) => sum + item.ids.length, 0);
	const evidence = occupied.map(item => `${item.key}=${item.ids.length}`).join(', ');
	throw new ActionError(
		ErrorCodes.PRECONDITION_REFUSED,
		`${label} build requires an empty target, but the opened asset already contains ${count} primitive(s) (${evidence}); no geometry was created. Create a fresh asset instead of replaying build.`,
	);
}

const libraryFootprintBuild: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const { pads, lines } = parseFootprintBuildSpec(payload); // validate everything before mutation
	let tabId: string | undefined;
	const createdPads: string[] = [];
	const createdLines: string[] = [];
	try {
		tabId = await activateLibraryDocument(
			uuid, libraryUuid,
			'4' as ELIB_LibraryType.FOOTPRINT, 4 as EDMT_EditorDocumentType.FOOTPRINT,
			'footprint',
		);
		await requireEmptyLibraryBuildTarget('Footprint', [
			{ key: 'pads', getAllPrimitiveId: () => eda.pcb_PrimitivePad.getAllPrimitiveId() },
			{ key: 'polylines', getAllPrimitiveId: () => eda.pcb_PrimitivePolyline.getAllPrimitiveId() },
		]);
		for (const p of pads) {
			const primitive = await eda.pcb_PrimitivePad.create(
				p.layer as TPCB_LayersOfPad, p.number, p.x, p.y, p.rotation, p.shape,
				'', p.hole, 0, 0, 0, p.metallization, p.padType,
			);
			const id = primitiveIdOf(primitive);
			if (!primitive || !id) throw new Error(`pad ${p.number} create returned no persistent primitive id`);
			createdPads.push(id);
		}
		for (const l of lines) {
			// In a footprint editor pcb_PrimitiveLine.create() rejects graphic layers
			// such as top silkscreen (observed on layer 3).  A UI-drawn footprint
			// graphic is represented by a polyline, so turn each public line segment
			// into the official polygon object before creating its primitive.
			const source = [l.startX, l.startY, 'L', l.endX, l.endY] as unknown as TPCB_PolygonSourceArray;
			const polygon = eda.pcb_MathPolygon.createPolygon(source);
			if (!polygon) throw new Error('line polygon creation returned no geometry');
			const primitive = await eda.pcb_PrimitivePolyline.create('', l.layer as TPCB_LayersOfLine, polygon, l.width, false);
			const id = primitiveIdOf(primitive);
			if (!primitive || !id) throw new Error('line create returned no persistent primitive id');
			createdLines.push(id);
		}
		await eda.pcb_Document.save();
		const [padReadback, lineReadback] = await Promise.all([
			createdPads.length ? eda.pcb_PrimitivePad.get(createdPads) : Promise.resolve([]),
			createdLines.length ? eda.pcb_PrimitivePolyline.get(createdLines) : Promise.resolve([]),
		]);
		const verified = padReadback.length === createdPads.length && lineReadback.length === createdLines.length;
		return {
			result: { uuid, libraryUuid, tabId, created: { pads: createdPads, lines: createdLines }, verified },
			...(verified ? {} : { warnings: ['Footprint geometry was created but immediate primitive readback was incomplete; do not retry blindly.'] }),
		};
	}
	catch (err) {
		if (err instanceof ActionError && createdPads.length + createdLines.length === 0) throw err;
		// Mutation has started: never throw and lose autosave/partial-state semantics.
		const rollback = { pads: false, lines: false };
		try { rollback.pads = createdPads.length === 0 || await eda.pcb_PrimitivePad.delete(createdPads); } catch { /* report below */ }
		try { rollback.lines = createdLines.length === 0 || await eda.pcb_PrimitivePolyline.delete(createdLines); } catch { /* report below */ }
		return {
			result: {
				partial: true, uuid, libraryUuid, tabId: tabId ?? null,
				created: { pads: createdPads, lines: createdLines }, rollback,
				error: describeThrown(err), verified: false,
			},
			warnings: [
				'Footprint build failed after mutation began; inspect rollback and the opened footprint before retrying.',
				...(rollback.pads && rollback.lines
					? ['The platform error may be transient; re-read the target inventory and retry only after it is proven empty.']
					: []),
			],
		};
	}
};

const REGION_GEOMETRY_EPSILON = 1e-6;

function sameRegionVertex(a: [number, number], b: [number, number]): boolean {
	return Math.abs(a[0] - b[0]) <= REGION_GEOMETRY_EPSILON
		&& Math.abs(a[1] - b[1]) <= REGION_GEOMETRY_EPSILON;
}

/**
 * Decode a single linear closed contour for semantic region readback checks.
 *
 * EasyEDA may rotate the first vertex, reverse the winding, emit one `L` for
 * every edge, and repeat the closing vertex more than once after save/reload.
 * Those are source-encoding differences, not geometry changes.  Curves,
 * multiple contours, malformed sources, and changed vertex sequences remain
 * fail-closed because a region with different geometry must be rolled back.
 */
function linearRegionRing(source: unknown): Array<[number, number]> | null {
	let candidate = source;
	if (Array.isArray(candidate) && candidate.length === 1 && Array.isArray(candidate[0])) {
		candidate = candidate[0];
	}
	const decoded = polygonSourceToPoints(candidate);
	if (!decoded.points || decoded.format !== 'polyline') return null;
	const points = decoded.points.map(point => [point[0], point[1]] as [number, number]);
	while (points.length > 3 && sameRegionVertex(points[0], points[points.length - 1])) points.pop();
	return points.length >= 3 ? points : null;
}

function equivalentLinearRegionGeometry(requested: unknown, actual: unknown): boolean {
	const expected = linearRegionRing(requested);
	const observed = linearRegionRing(actual);
	if (!expected || !observed || expected.length !== observed.length) return false;
	for (let observedStart = 0; observedStart < observed.length; observedStart++) {
		if (!sameRegionVertex(expected[0], observed[observedStart])) continue;
		for (const direction of [1, -1]) {
			let matches = true;
			for (let i = 1; i < expected.length; i++) {
				const observedIndex = (observedStart + direction * i + observed.length) % observed.length;
				if (!sameRegionVertex(expected[i], observed[observedIndex])) {
					matches = false;
					break;
				}
			}
			if (matches) return true;
		}
	}
	return false;
}

/** Add a persistent placement/copper rule region inside an existing footprint. */
const libraryFootprintRegionCreate: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const poly = closedPolygonFromPoints(payload.points); // validate before opening/mutating
	const layer = (optionalNumber(payload, 'layer') ?? 12) as unknown as TPCB_LayersOfRegion;
	const ruleTypes = parseRegionRuleTypes(payload.ruleType ?? payload.ruleTypes ?? ['no-components']);
	const regionName = optionalString(payload, 'name');
	const lineWidth = optionalNumber(payload, 'lineWidth');
	const lock = optionalBoolean(payload, 'locked') !== false;
	const requestedSource = poly.getSource();
	let tabId: string | undefined;
	let primitiveId = '';
	let actualState: Record<string, unknown> | null = null;
	try {
		const systemLibraryUuid = await eda.lib_LibrariesList.getSystemLibraryUuid();
		if (systemLibraryUuid && libraryUuid === systemLibraryUuid) {
			throw new ActionError(
				ErrorCodes.PRECONDITION_REFUSED,
				`Footprint "${uuid}" belongs to EasyEDA's immutable system library; no editor was opened and no geometry was created. `
				+ 'Keep the existing device/footprint/3D-model binding and use a parameterized PCB instance region, '
				+ 'or explicitly author a verified writable-library variant when changing the binding is intended.',
			);
		}
		tabId = await activateLibraryDocument(
			uuid, libraryUuid,
			'4' as ELIB_LibraryType.FOOTPRINT, 4 as EDMT_EditorDocumentType.FOOTPRINT,
			'footprint',
		);
		const region = await eda.pcb_PrimitiveRegion.create(
			layer, poly,
			ruleTypes as unknown as Array<EPCB_PrimitiveRegionRuleType>,
			regionName, lineWidth, lock,
		);
		if (!region) throw new Error('region create returned no primitive');
		primitiveId = region.getState_PrimitiveId();
		const saved = await eda.pcb_Document.save();
		if (!saved) throw new Error('pcb_Document.save returned false after region creation');
		const actual = await eda.pcb_PrimitiveRegion.get(primitiveId);
		if (!actual) throw new Error('region readback returned no primitive');
		actualState = {
			primitiveId: actual.getState_PrimitiveId(),
			layer: Number(actual.getState_Layer()),
			ruleType: [...actual.getState_RuleType()].map(Number).sort((a, b) => a - b),
			name: actual.getState_RegionName() ?? null,
			lineWidth: actual.getState_LineWidth(),
			locked: actual.getState_PrimitiveLock(),
			source: actual.getState_ComplexPolygon().getSource(),
		};
		const expectedRuleTypes = [...ruleTypes].sort((a, b) => a - b);
		const differences: string[] = [];
		const warnings: string[] = [];
		const requestedName = regionName ?? null;
		const actualName = actualState.name;
		const requestedNameIsAbsent = requestedName === null || requestedName === '';
		const actualNameIsAbsent = actualName === null || actualName === '';
		const nameIgnoredByHost = !requestedNameIsAbsent && actualNameIsAbsent;
		if (actualState.primitiveId !== primitiveId) differences.push('primitiveId');
		if (actualState.layer !== Number(layer)) differences.push('layer');
		if (exactJSON(actualState.ruleType) !== exactJSON(expectedRuleTypes)) differences.push('ruleType');
		if (!nameIgnoredByHost
			&& !(requestedNameIsAbsent && actualNameIsAbsent)
			&& actualName !== requestedName) differences.push('name');
		if (lineWidth !== undefined && actualState.lineWidth !== lineWidth) differences.push('lineWidth');
		if (actualState.locked !== lock) differences.push('locked');
		if (!equivalentLinearRegionGeometry(requestedSource, actualState.source)) differences.push('source');
		if (differences.length) throw new Error(`region readback differs for: ${differences.join(', ')}`);
		if (nameIgnoredByHost) {
			warnings.push(
				`Footprint region name was requested as ${JSON.stringify(requestedName)}, but host readback returned `
				+ `${JSON.stringify(actualName)}. The verified rule region was kept because name is optional metadata.`,
			);
		}
		return {
			result: {
				uuid, libraryUuid, tabId, primitiveId, saved: true, verified: true,
				requested: { layer: Number(layer), ruleType: expectedRuleTypes, name: requestedName, lineWidth: lineWidth ?? null, locked: lock, source: requestedSource },
				actual: actualState,
				...(nameIgnoredByHost ? { readbackDifferences: ['name'], namePersisted: false } : {}),
			},
			...(warnings.length ? { warnings } : {}),
		};
	}
	catch (err) {
		if (primitiveId) {
			let deleteRolledBack = false;
			let saveRolledBack = false;
			let absentAfterRollback = false;
			const rollbackErrors: string[] = [];
			try { deleteRolledBack = await eda.pcb_PrimitiveRegion.delete(primitiveId); }
			catch (rollbackErr) { rollbackErrors.push(`delete: ${describeThrown(rollbackErr)}`); }
			try { saveRolledBack = await eda.pcb_Document.save(); }
			catch (rollbackErr) { rollbackErrors.push(`save: ${describeThrown(rollbackErr)}`); }
			try { absentAfterRollback = !(await eda.pcb_PrimitiveRegion.get(primitiveId)); }
			catch (rollbackErr) { rollbackErrors.push(`readback: ${describeThrown(rollbackErr)}`); }
			const rolledBack = saveRolledBack && absentAfterRollback;
			return {
				result: {
					partial: true, uuid, libraryUuid, tabId: tabId ?? null, primitiveId,
					verified: false, actual: actualState, deleteRolledBack, saveRolledBack,
					absentAfterRollback, rolledBack, rollbackErrors, error: describeThrown(err),
				},
				warnings: ['Footprint region creation failed after mutation began; inspect the opened footprint before retrying.'],
			};
		}
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create a rule region inside the footprint editor.');
	}
};

const librarySymbolCreate: Handler = async (payload) => {
	const requestedName = requireString(payload, 'name');
	const description = optionalString(payload, 'description');
	const classification = optionalStringArray(payload, 'classification');
	const symbolType = optionalNumber(payload, 'symbolType');
	try {
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const uuid = await eda.lib_Symbol.create(
			libraryUuid, name, classification,
			symbolType as ELIB_SymbolType | undefined, description,
		);
		if (!uuid) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after creating the symbol.');
		const symbol = await eda.lib_Symbol.get(uuid, libraryUuid);
		if (!symbol) {
			return { result: { partial: true, created: { uuid, libraryUuid, name }, verified: false }, warnings: ['Symbol was created but immediate readback returned no record; do not retry blindly.'] };
		}
		return { result: { uuid, libraryUuid, name, requestedName, namespace, verified: true, symbol } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create symbol library asset.');
	}
};

const librarySymbolGet: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = optionalString(payload, 'libraryUuid');
	try {
		const symbol = await eda.lib_Symbol.get(uuid, libraryUuid);
		if (!symbol) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Symbol "${uuid}" was not found.`);
		return { result: { symbol } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to read symbol library asset.');
	}
};

// The official symbol-file getter returns the native archive, not rendered
// geometry. Keep it intact for offline inspection of sheet-symbol internals.
// 8 MiB becomes < 11 MiB after base64, below the daemon's 32 MiB WS limit.
const MAX_SYMBOL_SOURCE_BYTES = 8 << 20;
const librarySymbolExportSource: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	if (typeof eda.sys_FileManager?.getSymbolFileBySymbolUuid !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'Official symbol-file getter is unavailable in this EasyEDA build.');
	}
	let file: File | undefined;
	try {
		file = await eda.sys_FileManager.getSymbolFileBySymbolUuid(uuid, libraryUuid, 'elibz2');
	}
	catch (err) {
		throw edaError(err, 'Failed to export the native symbol file.');
	}
	if (!file || !Number.isSafeInteger(file.size) || file.size <= 0 || file.size > MAX_SYMBOL_SOURCE_BYTES) {
		throw new ActionError(ErrorCodes.INVALID_STATE,
			`Official symbol file is missing, empty, or exceeds the ${MAX_SYMBOL_SOURCE_BYTES}-byte transfer limit.`);
	}
	const artifact = await blobToArtifact(file, 'library_symbol_source', 'symbol-source.elibz2', 'application/octet-stream');
	return { result: { uuid, libraryUuid, fileType: 'elibz2', size: file.size, artifactId: artifact.id }, artifacts: [artifact] };
};

type SymbolPinSpec = {
	x: number; y: number; number: string; name: string; rotation: number;
	length: number; shape: ESCH_PrimitivePinShape; pinType: ESCH_PrimitivePinType;
};
type SymbolCircleSpec = { centerX: number; centerY: number; radius: number; lineWidth: number };

const librarySymbolBuild: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	if (!Array.isArray(payload.pins) || payload.pins.length === 0) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Symbol build requires a non-empty pins[] array.');
	}
	if (!Array.isArray(payload.outline) || payload.outline.length < 8 || payload.outline.some(v => typeof v !== 'number' || !Number.isFinite(v))) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Symbol build requires outline[] with at least four finite x/y points.');
	}
	const pins = payload.pins.map((raw, index): SymbolPinSpec => {
		if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pins[${index}] must be an object.`);
		const p = raw as Record<string, unknown>;
		if (typeof p.number !== 'string' || !p.number || typeof p.name !== 'string') throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `pins[${index}] requires number and name strings.`);
		return {
			x: finiteField(p, 'x', `pins[${index}]`), y: finiteField(p, 'y', `pins[${index}]`),
			number: p.number, name: p.name,
			rotation: p.rotation === undefined ? 0 : finiteField(p, 'rotation', `pins[${index}]`),
			length: p.length === undefined ? 20 : finiteField(p, 'length', `pins[${index}]`),
			shape: (p.shape ?? 'None') as ESCH_PrimitivePinShape,
			pinType: (p.pinType ?? 'Passive') as ESCH_PrimitivePinType,
		};
	});
	const rawCircles = payload.circles ?? [];
	if (!Array.isArray(rawCircles)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'circles must be an array.');
	const circles = rawCircles.map((raw, index): SymbolCircleSpec => {
		if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `circles[${index}] must be an object.`);
		const c = raw as Record<string, unknown>;
		const radius = finiteField(c, 'radius', `circles[${index}]`);
		if (radius <= 0) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `circles[${index}].radius must be greater than zero.`);
		return { centerX: finiteField(c, 'centerX', `circles[${index}]`), centerY: finiteField(c, 'centerY', `circles[${index}]`), radius, lineWidth: c.lineWidth === undefined ? 1 : finiteField(c, 'lineWidth', `circles[${index}]`) };
	});
	const createdPins: string[] = [];
	const createdCircles: string[] = [];
	let outlineId = '';
	let tabId: string | undefined;
	try {
		tabId = await activateLibraryDocument(
			uuid, libraryUuid,
			'2' as ELIB_LibraryType.SYMBOL, 2 as EDMT_EditorDocumentType.SYMBOL_COMPONENT,
			'symbol',
		);
		await requireEmptyLibraryBuildTarget('Symbol', [
			{ key: 'pins', getAllPrimitiveId: () => eda.sch_PrimitivePin.getAllPrimitiveId() },
			{ key: 'polygons', getAllPrimitiveId: () => eda.sch_PrimitivePolygon.getAllPrimitiveId() },
			{ key: 'circles', getAllPrimitiveId: () => eda.sch_PrimitiveCircle.getAllPrimitiveId() },
		]);
		const outline = await eda.sch_PrimitivePolygon.create(payload.outline as number[], null, 'none', 1, null);
		outlineId = primitiveIdOf(outline);
		if (!outlineId) throw new Error('symbol outline create returned no persistent primitive id');
		for (const p of pins) {
			const primitive = await eda.sch_PrimitivePin.create(p.x, p.y, p.number, p.name, p.rotation, p.length, null, p.shape, p.pinType);
			const id = primitiveIdOf(primitive);
			if (!id) throw new Error(`symbol pin ${p.number} create returned no persistent primitive id`);
			createdPins.push(id);
		}
		for (const c of circles) {
			const primitive = await eda.sch_PrimitiveCircle.create(c.centerX, c.centerY, c.radius, null, 'none', c.lineWidth, null, null);
			const id = primitiveIdOf(primitive);
			if (!id) throw new Error('symbol circle create returned no persistent primitive id');
			createdCircles.push(id);
		}
		await eda.sch_Document.save();
		const [pinReadback, outlineReadback, circleReadback] = await Promise.all([
			eda.sch_PrimitivePin.get(createdPins), eda.sch_PrimitivePolygon.get(outlineId),
			createdCircles.length ? eda.sch_PrimitiveCircle.get(createdCircles) : Promise.resolve([]),
		]);
		const verified = pinReadback.length === createdPins.length && Boolean(outlineReadback) && circleReadback.length === createdCircles.length;
		return { result: { uuid, libraryUuid, tabId, created: { pins: createdPins, outline: outlineId, circles: createdCircles }, verified } };
	}
	catch (err) {
		if (err instanceof ActionError && createdPins.length + createdCircles.length === 0 && !outlineId) throw err;
		try { if (createdPins.length) await eda.sch_PrimitivePin.delete(createdPins); } catch { /* report partial */ }
		try { if (createdCircles.length) await eda.sch_PrimitiveCircle.delete(createdCircles); } catch { /* report partial */ }
		try { if (outlineId) await eda.sch_PrimitivePolygon.delete(outlineId); } catch { /* report partial */ }
		return { result: { partial: true, uuid, libraryUuid, tabId: tabId ?? null, created: { pins: createdPins, outline: outlineId, circles: createdCircles }, error: describeThrown(err), verified: false }, warnings: ['Symbol build failed; created geometry was rolled back where possible.'] };
	}
};

const librarySymbolDelete: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const expectedName = requireString(payload, 'expectedName');
	try {
		const before = await eda.lib_Symbol.get(uuid, libraryUuid);
		if (!before) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Symbol "${uuid}" was not found; nothing was deleted.`);
		if (before.name !== expectedName) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Symbol name mismatch: expected "${expectedName}", found "${before.name}".`);
		if (!await eda.lib_Symbol.delete(uuid, libraryUuid)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `EasyEDA refused to delete symbol "${expectedName}".`);
		let after;
		try { after = await eda.lib_Symbol.get(uuid, libraryUuid); }
		catch { after = undefined; }
		if (after) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Symbol "${expectedName}" still exists after delete returned success.`);
		return { result: { uuid, libraryUuid, name: expectedName, deleted: true, verified: true } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to delete symbol library asset.'); }
};

const libraryModel3DCreate: Handler = async (payload) => {
	const requestedName = requireString(payload, 'name');
	const dataBase64 = requireString(payload, 'dataBase64');
	const description = optionalString(payload, 'description');
	const classification = optionalStringArray(payload, 'classification');
	const unitRaw = optionalString(payload, 'unit') ?? 'mm';
	if (!['mm', 'cm', 'm', 'mil', 'inch'].includes(unitRaw)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'unit must be one of mm, cm, m, mil, inch.');
	const unit = unitRaw as ESYS_Unit.MILLIMETER | ESYS_Unit.CENTIMETER | ESYS_Unit.METER | ESYS_Unit.MIL | ESYS_Unit.INCH;
	try {
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const bytes = Uint8Array.from(atob(dataBase64), c => c.charCodeAt(0));
		if (!bytes.length) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, '3D model file is empty.');
		const fileName = optionalString(payload, 'fileName') ?? `${name}.step`;
		const file = new File([bytes], fileName, { type: optionalString(payload, 'mimeType') ?? 'application/step' });
		const uuids = await eda.lib_3DModel.create(libraryUuid, file, classification, unit);
		if (!uuids?.length) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after importing the 3D model.');
		const uuid = uuids[0];
		if (!await eda.lib_3DModel.modify(uuid, libraryUuid, name, classification, description)) {
			return { result: { partial: true, uuid, uuids, libraryUuid, name, verified: false }, warnings: ['3D model imported but metadata rename failed; inspect before retrying.'] };
		}
		const model = await eda.lib_3DModel.get(uuid, libraryUuid);
		const verified = Boolean(model && model.name === name);
		return { result: { uuid, uuids, libraryUuid, name, requestedName, namespace, model, verified } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to import 3D model library asset.'); }
};

const libraryModel3DGet: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = optionalString(payload, 'libraryUuid');
	try {
		const model = await eda.lib_3DModel.get(uuid, libraryUuid);
		if (!model) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `3D model "${uuid}" was not found.`);
		return { result: { model } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to read 3D model library asset.'); }
};

const libraryModel3DSearch: Handler = async (payload) => {
	const query = requireString(payload, 'query');
	const libraryUuid = optionalString(payload, 'libraryUuid');
	const classification = optionalStringArray(payload, 'classification');
	const limit = payload.limit === undefined ? 20 : finiteField(payload, 'limit', 'payload');
	if (!Number.isInteger(limit) || limit < 1 || limit > 100) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'limit must be an integer from 1 to 100.');
	try {
		const models = await eda.lib_3DModel.search(query, libraryUuid, classification, limit, 1);
		return { result: { query, libraryUuid: libraryUuid ?? null, models, count: models.length } };
	}
	catch (err) { throw edaError(err, 'Failed to search 3D model library assets.'); }
};

const libraryModel3DCopy: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const sourceLibraryUuid = requireString(payload, 'sourceLibraryUuid');
	const requestedName = requireString(payload, 'name');
	const classification = optionalStringArray(payload, 'classification');
	try {
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const copiedUuid = await eda.lib_3DModel.copy(uuid, sourceLibraryUuid, libraryUuid, classification, name);
		if (!copiedUuid) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after copying the 3D model.');
		const model = await eda.lib_3DModel.get(copiedUuid, libraryUuid);
		if (!model) return { result: { partial: true, uuid: copiedUuid, libraryUuid, name, verified: false }, warnings: ['3D model copy returned a UUID but immediate readback was empty; do not retry blindly.'] };
		return { result: { uuid: copiedUuid, libraryUuid, name, requestedName, namespace, source: { uuid, libraryUuid: sourceLibraryUuid }, model, verified: true } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to copy 3D model library asset.'); }
};

const libraryDeviceSetModel3D: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const expectedName = requireString(payload, 'expectedName');
	const clear = payload.clear === true;
	const model3D = clear ? null : requireLibraryRef(payload, 'model3D');
	try {
		const before = await eda.lib_Device.get(uuid, libraryUuid);
		if (!before) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Device "${uuid}" was not found.`);
		if (before.name !== expectedName) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Device name mismatch: expected "${expectedName}", found "${before.name}".`);
		if (!clear && !await eda.lib_3DModel.get(model3D!.uuid, model3D!.libraryUuid)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `3D model "${model3D!.uuid}" was not found.`);
		if (!await eda.lib_Device.modify(uuid, libraryUuid, undefined, undefined, { model3D })) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA refused the Device 3D-model association update.');
		const device = await eda.lib_Device.get(uuid, libraryUuid);
		const association = device?.association as unknown as Record<string, unknown> | undefined;
		const actual = association?.model3D as { uuid?: string; libraryUuid?: string } | undefined;
		const legacyUuid = association?.model3DUuid;
		const verified = clear ? !actual && !legacyUuid : actual?.uuid === model3D!.uuid && actual?.libraryUuid === model3D!.libraryUuid;
		if (!verified) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Device update returned success but 3D-model association readback did not match.');
		return { result: { uuid, libraryUuid, name: expectedName, model3D, cleared: clear, verified: true, device } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to update Device 3D-model association.'); }
};

const libraryModel3DDelete: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const expectedName = requireString(payload, 'expectedName');
	try {
		const before = await eda.lib_3DModel.get(uuid, libraryUuid);
		if (!before || before.name !== expectedName) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `3D model name mismatch or missing: expected "${expectedName}".`);
		if (!await eda.lib_3DModel.delete(uuid, libraryUuid)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `EasyEDA refused to delete 3D model "${expectedName}".`);
		let after; try { after = await eda.lib_3DModel.get(uuid, libraryUuid); } catch { after = undefined; }
		if (after) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `3D model "${expectedName}" still exists after delete returned success.`);
		return { result: { uuid, libraryUuid, name: expectedName, deleted: true, verified: true } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to delete 3D model library asset.'); }
};

function requireLibraryRef(payload: Record<string, unknown>, key: string): { uuid: string; libraryUuid: string } {
	const raw = payload[key];
	if (!raw || typeof raw !== 'object') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `Missing required object "${key}" ({uuid, libraryUuid}).`);
	}
	const ref = raw as Record<string, unknown>;
	if (typeof ref.uuid !== 'string' || !ref.uuid || typeof ref.libraryUuid !== 'string' || !ref.libraryUuid) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `"${key}" must contain non-empty uuid and libraryUuid strings.`);
	}
	return { uuid: ref.uuid, libraryUuid: ref.libraryUuid };
}

const libraryDeviceCreate: Handler = async (payload) => {
	for (const key of ['symbols', 'footprints', 'devices', 'symbolVariants', 'footprintVariants', 'deviceVariants']) {
		if (payload[key] !== undefined) {
			throw new ActionError(
				ErrorCodes.PRECONDITION_REFUSED,
				`V4 multi-variant field "${key}" is not representable by the current canonical Device schema. Refusing before mutation instead of selecting the first variant.`,
			);
		}
	}
	const requestedName = requireString(payload, 'name');
	const symbol = requireLibraryRef(payload, 'symbol');
	const footprint = payload.footprint === undefined ? undefined : requireLibraryRef(payload, 'footprint');
	const model3D = payload.model3D === undefined ? undefined : requireLibraryRef(payload, 'model3D');
	const description = optionalString(payload, 'description');
	const classification = optionalStringArray(payload, 'classification');
	const property = payload.property;
	if (property !== undefined && (!property || typeof property !== 'object' || Array.isArray(property))) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, '"property" must be an object.');
	}
	try {
		const { name, namespace } = await namespacedLibraryAssetName(requestedName);
		const libraryUuid = await resolveWritableLibraryUuid(payload);
		const uuid = await eda.lib_Device.create(
			libraryUuid, name, classification,
			{ symbol, ...(footprint ? { footprint } : {}), ...(model3D ? { model3D } : {}) },
			description,
			property as ILIB_DeviceExtendPropertyItem | undefined,
		);
		if (!uuid) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'EasyEDA returned no UUID after creating the device.');
		const device = await eda.lib_Device.get(uuid, libraryUuid);
		if (!device) {
			return { result: { partial: true, created: { uuid, libraryUuid, name }, verified: false }, warnings: ['Device was created but immediate readback returned no record; do not retry blindly.'] };
		}
		return { result: { uuid, libraryUuid, name, requestedName, namespace, verified: true, device } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create device library asset.');
	}
};

/**
 * V4 can return plural symbol/device/footprint associations that the public
 * type package still models as one association. Detect plural runtime shapes
 * conservatively and stop before placement; choosing index 0 would be silent
 * data loss. Traditional multi-unit symbols use subPartNames plus an explicit
 * subPartName and remain supported — they are not the new plural association.
 */
function unsupportedV4DeviceVariant(device: unknown): string | undefined {
	if (!device || typeof device !== 'object') return undefined;
	const root = device as Record<string, unknown>;
	const association = root.association && typeof root.association === 'object'
		? root.association as Record<string, unknown>
		: {};
	for (const [owner, record] of [['device', root], ['association', association]] as const) {
		for (const key of ['symbols', 'footprints', 'devices', 'symbolVariants', 'footprintVariants', 'deviceVariants']) {
			const value = record[key];
			if (Array.isArray(value) && value.length > 1) return `${owner}.${key} has ${value.length} variants`;
		}
		for (const key of ['symbol', 'footprint', 'device']) {
			const value = record[key];
			if (Array.isArray(value) && value.length > 1) return `${owner}.${key} has ${value.length} variants`;
		}
	}
	return undefined;
}

async function preflightV4DeviceVariant(libraryUuid: string, uuid: string): Promise<void> {
	const api = (eda as unknown as { lib_Device?: { get?: (uuid: string, libraryUuid: string) => Promise<unknown> } }).lib_Device;
	if (typeof api?.get !== 'function') return;
	let device: unknown;
	try { device = await api.get(uuid, libraryUuid); }
	catch { return; } // identity/readback code retains its existing error handling
	const issue = unsupportedV4DeviceVariant(device);
	if (issue) {
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			`V4 multi-variant device is unsupported (${issue}). Placement was not attempted; add a canonical variant selector and typed API first.`,
		);
	}
}

const libraryDeviceGet: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = optionalString(payload, 'libraryUuid');
	try {
		const device = await eda.lib_Device.get(uuid, libraryUuid);
		if (!device) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Device "${uuid}" was not found.`);
		return { result: { device } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to read device library asset.');
	}
};

const libraryDeviceDelete: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const libraryUuid = requireString(payload, 'libraryUuid');
	const expectedName = requireString(payload, 'expectedName');
	try {
		const before = await eda.lib_Device.get(uuid, libraryUuid);
		if (!before) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Device "${uuid}" was not found; nothing was deleted.`);
		if (before.name !== expectedName) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Device name mismatch: expected "${expectedName}", found "${before.name}".`);
		if (!await eda.lib_Device.delete(uuid, libraryUuid)) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `EasyEDA refused to delete Device "${expectedName}".`);
		let after;
		try { after = await eda.lib_Device.get(uuid, libraryUuid); }
		catch { after = undefined; }
		if (after) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Device "${expectedName}" still exists after delete returned success.`);
		return { result: { uuid, libraryUuid, name: expectedName, deleted: true, verified: true } };
	}
	catch (err) { if (err instanceof ActionError) throw err; throw edaError(err, 'Failed to delete Device library asset.'); }
};

// ─── Rebind: swap a placed component's footprint / symbol ─────────────

/** A device-library identity ({ libraryUuid, uuid }) as reported by getState_Component(). */
interface DeviceRef { libraryUuid: string; uuid: string }

/** Keep only the string|number|boolean entries of an otherProperty map (modify's accepted shape). */
function cleanOtherProperty(
	op: Record<string, unknown> | undefined,
): Record<string, string | number | boolean> | undefined {
	if (!op) return undefined;
	const out: Record<string, string | number | boolean> = {};
	for (const [k, v] of Object.entries(op)) {
		if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') out[k] = v;
	}
	return Object.keys(out).length ? out : undefined;
}

// Keep every platform mutation well inside the daemon's 90s rebind budget.
// withTimeout is backed by the transport worker's deadline sweep; a bare
// setTimeout is not reliable when the Web EDA tab is backgrounded.
const REBIND_OP_TIMEOUT_MS = 15_000;

type RebindPresence = {
	present: boolean | null;
	component?: SchComponent;
	error?: string;
};

async function readRebindComponent(primitiveId: string): Promise<RebindPresence> {
	try {
		const component = await withTimeout(
			Promise.resolve(eda.sch_PrimitiveComponent.get(primitiveId)),
			REBIND_OP_TIMEOUT_MS,
			`Rebind readback for component "${primitiveId}" timed out after ${REBIND_OP_TIMEOUT_MS}ms.`,
		);
		return { present: Boolean(component), ...(component ? { component } : {}) };
	}
	catch (err) {
		return { present: null, error: describeThrown(err) };
	}
}

function rebindInstanceDifferences(
	expected: Record<string, unknown>,
	actual: Record<string, unknown>,
): Array<string> {
	const differences: Array<string> = [];
	const exactFields = [
		'designator', 'uniqueId', 'subPartName', 'mirror', 'addIntoBom', 'addIntoPcb',
		'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'otherProperty',
	];
	for (const field of exactFields) {
		if (exactJSON(actual[field]) !== exactJSON(expected[field])) differences.push(field);
	}
	for (const field of ['x', 'y', 'rotation']) {
		const want = expected[field];
		const got = actual[field];
		if (typeof want !== 'number' || typeof got !== 'number' || !Number.isFinite(want)
			|| !Number.isFinite(got) || Math.abs(want - got) > 1e-6) differences.push(field);
	}
	return differences;
}

function rebindFactsDetail(facts: Record<string, unknown>): string {
	return `rebind transaction facts: ${JSON.stringify(facts)}`;
}

function deviceAssociationRef(value: unknown, kind: 'footprint' | 'symbol'): DeviceRef | undefined {
	const record = identityRecord(value);
	const association = identityRecord(record.association);
	const raw = identityRecord(association[kind] ?? record[kind]);
	return typeof raw.uuid === 'string' && typeof raw.libraryUuid === 'string'
		? { uuid: raw.uuid, libraryUuid: raw.libraryUuid }
		: undefined;
}

function sameDeviceRef(a: DeviceRef | undefined, b: DeviceRef | undefined): boolean {
	return Boolean(a && b && a.uuid === b.uuid && a.libraryUuid === b.libraryUuid);
}

async function readRebindDeviceAssociation(
	device: DeviceRef,
	kind: 'footprint' | 'symbol',
): Promise<{ ref?: DeviceRef; error?: string }> {
	try {
		const detail = await withTimeout(
			Promise.resolve(eda.lib_Device.get(device.uuid, device.libraryUuid)),
			REBIND_OP_TIMEOUT_MS,
			`Device association readback for "${device.uuid}" timed out after ${REBIND_OP_TIMEOUT_MS}ms.`,
		);
		const ref = deviceAssociationRef(detail, kind);
		return ref ? { ref } : { error: `device.get returned no ${kind} association` };
	}
	catch (err) { return { error: describeThrown(err) }; }
}

/**
 * Diagnose a `get(primitiveId)` miss: is the component genuinely absent, or
 * merely on another page?
 *
 * `eda.sch_PrimitiveComponent.get` is ACTIVE-PAGE scoped, while
 * `getAll(undefined, true)` and `component.delete` see the whole document.
 * Reporting the miss as "no component found" therefore sent callers hunting a
 * phantom deletion — issue #162 caught `replace` refusing an id twice and
 * `delete` accepting the very same id 17 seconds later, with nothing but a page
 * switch in between.
 *
 * Runs only on the error path, so the page-tagging round trip (which cycles the
 * active document) is affordable.
 */
async function componentMissError(primitiveId: string): Promise<ActionError> {
	let existsElsewhere = false;
	try {
		const all = await eda.sch_PrimitiveComponent.getAll(undefined, true);
		existsElsewhere = (all ?? []).some(c => c.getState_PrimitiveId() === primitiveId);
	}
	catch { /* fall through to the plain not-found message */ }

	if (!existsElsewhere) {
		return new ActionError(
			ErrorCodes.INVALID_STATE,
			`No schematic component found with primitiveId "${primitiveId}" on any page.`,
		);
	}

	const page = (await tagComponentPages()).get(primitiveId);
	const where = page ? `page "${page.pageName}" (${page.pageUuid})` : 'another page';
	const fix = page
		? `easyeda doc switch ${page.pageName}`
		: 'easyeda doc ls  →  easyeda doc switch <page>';
	return new ActionError(
		ErrorCodes.INVALID_STATE,
		`Component "${primitiveId}" is on ${where}, not the ACTIVE page — this action only reaches the active page. Switch to it first: ${fix}`,
	);
}

/**
 * Fetch the placed component primitive by id, or throw a NOT-FOUND-style error
 * that names the real cause (see componentMissError).
 */
export async function getComponentOrThrow(primitiveId: string): Promise<SchComponent> {
	let component: SchComponent | undefined;
	try {
		component = await eda.sch_PrimitiveComponent.get(primitiveId);
	}
	catch (err) {
		throw edaError(err, `Failed to read component "${primitiveId}".`);
	}
	if (!component) {
		throw await componentMissError(primitiveId);
	}
	return component;
}

/**
 * The "five-step binding method" for swapping a placed component's footprint OR
 * symbol, exposed as a typed action so the operation no longer needs `debug.exec_js`.
 *
 * WHY re-create (not a plain modify): `sch_PrimitiveComponent.modify` cannot change
 * the symbol/footprint reference of an already-placed instance (see
 * docs/reviews/2026-07-marketplace-coverage.md). The reference lives on the
 * DEVICE-library record, so we:
 *   1. resolve the device's real library UUID (imported devices carry an empty one),
 *   2. `lib_Device.modify` the device association to the new footprint/symbol,
 *   3. `create` and freshly read a candidate while the original still exists,
 *   4. only then `delete` and freshly confirm absence of the stale instance,
 *   5. restore designator / uniqueId / manufacturer / supplier / otherProperty and
 *      freshly verify those plus position, rotation, mirror and BOM flags.
 *
 * Original state is captured up front. Failures report the exact phase, both instance
 * presences and verified rollback facts; rollback errors are evidence, never swallowed.
 *
 * CAVEAT (surface in the CLI help / PR): delete-then-create mints a NEW primitiveId,
 * so wires that were attached to the old instance's pins may need re-drawing — run
 * `sch drc` / `sch check` after a rebind to confirm connectivity survived.
 *
 * @param kind - 'footprint' or 'symbol'
 * @returns a Handler
 */
function makeRebindHandler(kind: 'footprint' | 'symbol'): Handler {
	return async (payload) => {
		const primitiveId = requireString(payload, 'primitiveId');
		// Two ways to name the target: a free-text name to search, or an explicit
		// { uuid, libraryUuid } pair that bypasses search (deterministic, no ambiguity).
		const targetName = optionalString(payload, kind);
		const explicitUuid = optionalString(payload, `${kind}Uuid`);
		const explicitLibraryUuid = optionalString(payload, `${kind}LibraryUuid`);
		// Scope for the name search; defaults to 'project' (where the device lives).
		const scope = optionalString(payload, 'scope') ?? 'project';
		if (!targetName && !explicitUuid) {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Provide either "${kind}" (a name to search) or "${kind}Uuid" (+ optional "${kind}LibraryUuid").`,
			);
		}

		// Step-level diagnostics via sys_Log (readable back even on daemon timeout).
		const rlog = (m: string) => { try { eda.sys_Log.add(`[rebind] ${m}`); } catch { /* ignore */ } };
		rlog(`start ${kind} ${primitiveId}`);
		const component = await getComponentOrThrow(primitiveId);
		const snapshot = serializeComponent(component);
		const oldSymbol = component.getState_Symbol() as DeviceRef | undefined;
		const oldFootprint = component.getState_Footprint() as DeviceRef | undefined;
		const originalUniqueId = snapshot.uniqueId;
		if (typeof originalUniqueId !== 'string' || !originalUniqueId.trim()) {
			throw new ActionError(
				ErrorCodes.PRECONDITION_REFUSED,
				`Component "${primitiveId}" has no stable non-empty uniqueId. Rebind was refused before any library or canvas mutation because sch↔PCB identity could not be restored and verified.`,
			);
		}
		// The REAL 32-char device identity — getState_Component().uuid is a 16-char
		// placed-symbol id that lib_Device.modify/create reject (live-verified).
		const device = await resolvePlacedDeviceIdentity(snapshot);
		rlog(`identity via=${device.via} ${device.uuid}`);

		// Resolve the target footprint/symbol identity.
		let target: NamedLibItem;
		if (explicitUuid) {
			target = { uuid: explicitUuid, libraryUuid: explicitLibraryUuid ?? device.libraryUuid, name: targetName ?? explicitUuid };
		}
		else {
			let results: Array<NamedLibItem>;
			try {
				const searcher = kind === 'footprint' ? eda.lib_Footprint : eda.lib_Symbol;
				results = (await searcher.search(targetName as string, scope)) as unknown as Array<NamedLibItem>;
			}
			catch (err) {
				throw edaError(err, `Failed to search ${kind} library for "${targetName}".`);
			}
			const match = pickNamedCandidate(targetName as string, Array.isArray(results) ? results : []);
			if (match.kind === 'none') {
				throw new ActionError(
					ErrorCodes.INVALID_STATE,
					`No ${kind} named "${targetName}" found in scope "${scope}". `
					+ `Pass "${kind}Uuid" (+ "${kind}LibraryUuid") to bind directly, or check the name.`,
				);
			}
			if (match.kind === 'ambiguous') {
				const uuids = match.matches.map(m => m.uuid).join(', ');
				throw new ActionError(
					ErrorCodes.INVALID_STATE,
					`${match.matches.length} ${kind}s named "${targetName}" match (uuids: ${uuids}). `
					+ `Pass "${kind}Uuid" to pick one.`,
				);
			}
			target = match.item;
		}

		// Build the new + rollback associations for lib_Device.modify.
		const newAssoc = kind === 'footprint'
			? { footprint: { uuid: target.uuid, libraryUuid: target.libraryUuid } }
			: { symbol: { uuid: target.uuid, libraryUuid: target.libraryUuid } };
		const oldRef = kind === 'footprint' ? oldFootprint : oldSymbol;
		if (!oldRef?.uuid || !oldRef.libraryUuid) {
			throw new ActionError(
				ErrorCodes.PRECONDITION_REFUSED,
				`Component "${primitiveId}" has no complete original ${kind} identity. Rebind was refused before mutation because the device association could not be rolled back exactly.`,
			);
		}
		const rollbackAssoc = oldRef
			? (kind === 'footprint'
				? { footprint: { uuid: oldRef.uuid, libraryUuid: oldRef.libraryUuid } }
				: { symbol: { uuid: oldRef.uuid, libraryUuid: oldRef.libraryUuid } })
			: undefined;

		// Replay helpers so both the happy path and rollback re-place identically.
		const x = typeof snapshot.x === 'number' ? snapshot.x : 0;
		const y = typeof snapshot.y === 'number' ? snapshot.y : 0;
		const subPartName = typeof snapshot.subPartName === 'string' ? snapshot.subPartName : undefined;
		const rotation = typeof snapshot.rotation === 'number' ? snapshot.rotation : undefined;
		const mirror = typeof snapshot.mirror === 'boolean' ? snapshot.mirror : undefined;
		const addIntoBom = typeof snapshot.addIntoBom === 'boolean' ? snapshot.addIntoBom : undefined;
		const addIntoPcb = typeof snapshot.addIntoPcb === 'boolean' ? snapshot.addIntoPcb : undefined;
		const restoreProps = {
			...(typeof snapshot.designator === 'string' ? { designator: snapshot.designator } : {}),
			...(typeof snapshot.uniqueId === 'string' ? { uniqueId: snapshot.uniqueId } : {}),
			...(typeof snapshot.manufacturer === 'string' ? { manufacturer: snapshot.manufacturer } : {}),
			...(typeof snapshot.manufacturerId === 'string' ? { manufacturerId: snapshot.manufacturerId } : {}),
			...(typeof snapshot.supplier === 'string' ? { supplier: snapshot.supplier } : {}),
			...(typeof snapshot.supplierId === 'string' ? { supplierId: snapshot.supplierId } : {}),
			...(cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined)
				? { otherProperty: cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined) }
				: {}),
		};

		const createPlaced = async (
			dev: DeviceRef,
			purpose: 'candidate' | 'rollback-original',
			onCreatedId?: (primitiveId: string) => void,
		): Promise<SchComponent> => {
			const created = await withTimeout(
				Promise.resolve(eda.sch_PrimitiveComponent.create(
					{ libraryUuid: dev.libraryUuid, uuid: dev.uuid },
					x, y, subPartName, rotation, mirror, addIntoBom, addIntoPcb,
				)),
				REBIND_OP_TIMEOUT_MS,
				`${purpose} create did not settle within ${REBIND_OP_TIMEOUT_MS}ms. The operation may still land late; do not retry or run PCB import-changes until a fresh schematic read proves the actual instances.`,
			);
			if (!created) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `${purpose} create returned no primitive.`);
			}
			const createdId = created.getState_PrimitiveId();
			if (!createdId) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `${purpose} create returned a primitive without an id.`);
			onCreatedId?.(createdId);
			const fresh = await readRebindComponent(createdId);
			if (fresh.present !== true || !fresh.component) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`${purpose} "${createdId}" was not proven present by a fresh readback.`,
					fresh.error,
				);
			}
			return fresh.component;
		};

		const verifyPlacedBinding = async (
			placed: SchComponent,
			expectedDevice: DeviceRef,
			expectedRef: DeviceRef,
			purpose: 'replacement' | 'rollback-original',
		): Promise<void> => {
			const observed = serializeComponent(placed);
			let resolved: DeviceRef;
			try {
				resolved = await resolvePlacedDeviceIdentity(observed);
			}
			catch (err) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`${purpose} device identity could not be proven from fresh placed-instance data.`,
					rebindFactsDetail({ purpose, expectedDevice, expectedRef, cause: describeThrown(err) }),
				);
			}
			if (!sameDeviceRef(resolved, expectedDevice)) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`${purpose} resolved to a different device than the one requested.`,
					rebindFactsDetail({ purpose, expectedDevice, actualDevice: resolved, expectedRef }),
				);
			}
			const association = await readRebindDeviceAssociation(expectedDevice, kind);
			if (!sameDeviceRef(association.ref, expectedRef)) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`${purpose} device ${kind} association does not match the requested binding.`,
					rebindFactsDetail({ purpose, expectedDevice, expectedRef, actualRef: association.ref ?? null, associationError: association.error }),
				);
			}
		};

		const restoreAndVerify = async (
			primitiveIdToRestore: string,
			expectedDevice: DeviceRef,
			expectedRef: DeviceRef,
			purpose: 'replacement' | 'rollback-original',
		): Promise<SchComponent> => {
			await withTimeout(
				Promise.resolve(eda.sch_PrimitiveComponent.modify(primitiveIdToRestore, restoreProps)),
				REBIND_OP_TIMEOUT_MS,
				`Identity/property restore for component "${primitiveIdToRestore}" did not settle within ${REBIND_OP_TIMEOUT_MS}ms. The write may still land late; fresh readback is mandatory.`,
			);
			const fresh = await readRebindComponent(primitiveIdToRestore);
			if (fresh.present !== true || !fresh.component) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`Restored component "${primitiveIdToRestore}" was not proven present by fresh readback.`,
					fresh.error,
				);
			}
			const actual = serializeComponent(fresh.component);
			const differences = rebindInstanceDifferences(snapshot, actual);
			if (differences.length) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`Restored component "${primitiveIdToRestore}" does not exactly match the original instance (${differences.join(', ')}).`,
					rebindFactsDetail({ phase: 'restore-verify', differences, expectedUniqueId: originalUniqueId, actualUniqueId: actual.uniqueId }),
				);
			}
			await verifyPlacedBinding(fresh.component, expectedDevice, expectedRef, purpose);
			return fresh.component;
		};

		// Auto-confirm the 符号/封装另存为 conflict dialogs that lib_Device.copy opens
		// when the personal library already holds same-named symbol/footprint
		// documents (a PREVIOUS clone of the same family). The copy promise HANGS
		// while a dialog is open (live-verified: 90s dispatch timeout), and the
		// platform opens ONE DIALOG PER conflicting document in sequence — so the
		// dialogs must be clicked concurrently, #124 style. A setTimeout POLLING
		// loop does NOT work here: MCP/CLI-driven editor tabs are BACKGROUND tabs,
		// where Chrome throttles chained timers to ~1/min (live-verified — the
		// loop never fired while the dialog sat open). MutationObserver callbacks
		// are not throttled, so the clicker is armed once and fires on DOM change.
		// The pre-selected option 使用已有的库 (reuse the existing documents) is
		// exactly the semantics we want; we only click 确认.
		const armLibConflictAutoClicker = async (): Promise<() => number> => {
			const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
				new (body: string): () => Promise<() => number>;
			};
			const arm = new AsyncFunction(`
				let clicks = 0;
				const tryClick = () => {
					const modals = Array.from(document.querySelectorAll('.arco-modal, [class*=modal]'))
						.filter(e => e.offsetParent !== null && /另存为|同名库/.test(e.innerText || ''));
					if (!modals.length) return;
					const btn = modals.flatMap(m => Array.from(m.querySelectorAll('button')))
						.find(b => (b.innerText || '').replace(/\\s+/g, '') === '确认' && b.offsetParent !== null);
					if (btn) { btn.click(); clicks++; }
				};
				tryClick();
				const obs = new MutationObserver(tryClick);
				obs.observe(document.body, { childList: true, subtree: true, attributes: true });
				return () => { obs.disconnect(); return clicks; };
			`);
			return arm();
		};

		// Step 2: point the device at the new footprint/symbol — in place when the
		// library record is writable (project/personal-library devices), else via a
		// personal-library CLONE (system-library records reject lib_Device.modify
		// unconditionally, live-verified with the true 32-char uuid).
		let inPlaceCall: 'returned-true' | 'returned-false' | 'rejected' = 'returned-false';
		let inPlaceCallError: string | undefined;
		try {
			const returned = await withTimeout(
				Promise.resolve(eda.lib_Device.modify(device.uuid, device.libraryUuid, undefined, undefined, newAssoc)),
				REBIND_OP_TIMEOUT_MS,
				`Device association update did not settle within ${REBIND_OP_TIMEOUT_MS}ms. It may land late; the canvas is still unchanged, but do not retry the rebind or run PCB import-changes until fresh library and schematic readback.`,
			);
			inPlaceCall = returned === false ? 'returned-false' : 'returned-true';
		}
		catch (err) {
			if (err instanceof ActionError && /did not settle/.test(err.message)) throw err;
			inPlaceCall = 'rejected';
			inPlaceCallError = describeThrown(err);
		}
		const applied = await readRebindDeviceAssociation(device, kind);
		const inPlaceOk = sameDeviceRef(applied.ref, target);
		const associationStillOriginal = sameDeviceRef(applied.ref, oldRef);
		rlog(`in-place modify call=${inPlaceCall} readback=${inPlaceOk ? 'target' : associationStillOriginal ? 'original' : 'unknown'}`);
		if (!inPlaceOk && !associationStillOriginal) {
			const rollbackErrors: Array<string> = [];
			try {
				await withTimeout(
					Promise.resolve(eda.lib_Device.modify(device.uuid, device.libraryUuid, undefined, undefined, rollbackAssoc)),
					REBIND_OP_TIMEOUT_MS,
					`Rollback after ambiguous in-place ${kind} association did not settle within ${REBIND_OP_TIMEOUT_MS}ms.`,
				);
			}
			catch (err) { rollbackErrors.push(describeThrown(err)); }
			const rolledBack = await readRebindDeviceAssociation(device, kind);
			const oldPresence = await readRebindComponent(primitiveId);
			const rollbackVerified = sameDeviceRef(rolledBack.ref, oldRef)
				&& oldPresence.present === true && rollbackErrors.length === 0;
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Device ${kind} mutation outcome was ambiguous after fresh association readback. The original instance was not deleted.`,
				rebindFactsDetail({
					phase: 'bind-device-association',
					call: { outcome: inPlaceCall, ...(inPlaceCallError ? { error: inPlaceCallError } : {}) },
					expectedRef: { uuid: target.uuid, libraryUuid: target.libraryUuid },
					oldRef,
					actualRef: applied.ref ?? null,
					associationError: applied.error,
					oldPrimitive: { primitiveId, present: oldPresence.present, ...(oldPresence.error ? { error: oldPresence.error } : {}) },
					replacement: { primitiveId: null, present: false },
					rollback: { attempted: true, verified: rollbackVerified, actualRef: rolledBack.ref ?? null, errors: rollbackErrors },
				}),
			);
		}

		let effectiveDevice: DeviceRef = device;
		let clonedDevice: { uuid: string; libraryUuid: string; name: string; owned: boolean; previousRef?: DeviceRef } | undefined;
		if (!inPlaceOk) {
			let personalLib: string | undefined;
			try { personalLib = await eda.lib_LibrariesList.getPersonalLibraryUuid(); }
			catch { /* handled below */ }
			if (!personalLib) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`lib_Device.modify rejected the ${kind} rebind (read-only library record) and no personal library is available for the clone fallback.`,
				);
			}
			let baseName = '';
			try { baseName = (await eda.lib_Device.get(device.uuid, device.libraryUuid))?.name ?? ''; }
			catch { /* fall through */ }
			if (!baseName) {
				baseName = (typeof snapshot.manufacturerId === 'string' && snapshot.manufacturerId) || device.uuid.slice(0, 8);
			}
			// Clone-name suffix: prefer the human footprint/symbol name; an
			// explicit-uuid call has no name, so use a short uuid prefix.
			const suffix = target.name && target.name !== target.uuid ? target.name : `${kind}-${target.uuid.slice(0, 8)}`;
			const cloneName = `${baseName}_${suffix}`;
			// The observer clicker guards the ENTIRE clone segment: BOTH
			// lib_Device.copy and the clone's lib_Device.modify open 符号/封装另存为
			// conflict dialogs (live-verified — the modify-side one was the second
			// 90s hang) and their promises stall until the dialog is answered.
			const stopClicker = await armLibConflictAutoClicker();
			let copied: string | undefined;
			let copiedByThisAction = false;
			let reusedClonePreviousRef: DeviceRef | undefined;
			try {
				const attemptCopy = async (name: string): Promise<string | undefined> => {
					rlog(`copy start ${name}`);
					const got = await withTimeout(
						eda.lib_Device
							.copy(device.uuid, device.libraryUuid, personalLib as string, undefined, name)
							.catch((e) => { rlog(`copy rejected: ${String(e && (e as Error).message || e).slice(0, 80)}`); return undefined; })
							.then(v => v ?? undefined),
						REBIND_OP_TIMEOUT_MS,
						`Device clone "${name}" did not settle within ${REBIND_OP_TIMEOUT_MS}ms. The clone may still land late; do not retry the rebind until a fresh library and schematic read establishes the actual state.`,
					);
					if (got) { copiedByThisAction = true; return got; }
					// The promise may resolve late (or unreliably) after the dialog —
					// the ground truth is whether the clone landed in the PERSONAL
					// library (search's 2nd arg scopes the library; the default
					// scope does NOT surface personal-library devices).
					try {
						const hits = (await eda.lib_Device.search(name, personalLib)) as unknown as Array<Record<string, unknown>>;
						const hit = (Array.isArray(hits) ? hits : []).find(h => h.name === name && typeof h.uuid === 'string');
						rlog(`landed check: ${hit ? 'found' : 'missing'}`);
						return hit ? hit.uuid as string : undefined;
					}
					catch { return undefined; }
				};
				// Reuse an existing same-named clone (repeat rebinds of the same
				// family) — skips the copy entirely; the clone is modified to the
				// target association right below either way.
				try {
					const pre = (await eda.lib_Device.search(cloneName, personalLib)) as unknown as Array<Record<string, unknown>>;
					const hit = (Array.isArray(pre) ? pre : []).find(h => h.name === cloneName && typeof h.uuid === 'string');
					if (hit) {
						const detail = await eda.lib_Device.get(hit.uuid as string, personalLib);
						const previousRef = deviceAssociationRef(detail, kind);
						if (previousRef) {
							copied = hit.uuid as string;
							reusedClonePreviousRef = previousRef;
							rlog(`clone reused: ${copied}`);
						}
					}
				}
				catch { /* fall through to copy */ }
				if (!copied) copied = await attemptCopy(cloneName);
				if (!copied) copied = await attemptCopy(`${cloneName}_2`);
				if (!copied) {
					throw new ActionError(
						ErrorCodes.EDA_CALL_FAILED,
						`The device record is read-only and cloning it into the personal library failed (tried "${cloneName}"). Canvas unchanged.`,
					);
				}
				let cloneModOk = false;
				try {
					cloneModOk = (await withTimeout(
						Promise.resolve(eda.lib_Device.modify(copied, personalLib, undefined, undefined, newAssoc)),
						REBIND_OP_TIMEOUT_MS,
						`Device clone association update did not settle within ${REBIND_OP_TIMEOUT_MS}ms. It may land late; do not retry the rebind until fresh library and schematic readback.`,
					)) !== false;
				}
				catch (err) {
					if (err instanceof ActionError && /did not settle/.test(err.message)) throw err;
					cloneModOk = false;
				}
				rlog(`clone modify: ${cloneModOk}`);
				if (!cloneModOk) {
					try { await eda.lib_Device.delete(copied, personalLib); } catch { /* best-effort */ }
					throw new ActionError(
						ErrorCodes.EDA_CALL_FAILED,
						`Failed to bind the new ${kind} onto the personal-library clone "${cloneName}" (clone removed, canvas unchanged).`,
					);
				}
			}
			finally {
				const clicks = stopClicker();
				rlog(`clone segment done (dialog clicks: ${clicks})`);
			}
			effectiveDevice = { uuid: copied, libraryUuid: personalLib };
			clonedDevice = {
				uuid: copied,
				libraryUuid: personalLib,
				name: cloneName,
				owned: copiedByThisAction,
				...(reusedClonePreviousRef ? { previousRef: reusedClonePreviousRef } : {}),
			};
			rlog(`clone bound: ${copied}`);
		}

		let phase = 'candidate-create';
		let candidateId: string | undefined;
		let oldDeleted = false;
		let candidateMayLandLate = false;

		const rollback = async (): Promise<Record<string, unknown>> => {
			const errors: Array<string> = [];
			let candidatePresence: RebindPresence = candidateId
				? await readRebindComponent(candidateId)
				: { present: candidateMayLandLate ? null : false };
			if (candidateId && candidatePresence.present !== false) {
				try {
					await withTimeout(
						Promise.resolve(eda.sch_PrimitiveComponent.delete(candidateId)),
						REBIND_OP_TIMEOUT_MS,
						`Rollback delete for replacement "${candidateId}" did not settle within ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
				}
				catch (err) { errors.push(`candidate delete: ${describeThrown(err)}`); }
				candidatePresence = await readRebindComponent(candidateId);
				if (candidatePresence.present !== false) {
					errors.push(`candidate removal unverified (present=${String(candidatePresence.present)}${candidatePresence.error ? `: ${candidatePresence.error}` : ''})`);
				}
			}

			let deviceAssociationRestored = !inPlaceOk;
			if (inPlaceOk) {
				try {
					await withTimeout(
						Promise.resolve(eda.lib_Device.modify(device.uuid, device.libraryUuid, undefined, undefined, rollbackAssoc)),
						REBIND_OP_TIMEOUT_MS,
						`Rollback of the original device association did not settle within ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					const detail = await withTimeout(
						Promise.resolve(eda.lib_Device.get(device.uuid, device.libraryUuid)),
						REBIND_OP_TIMEOUT_MS,
						`Rollback device-association readback timed out after ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					deviceAssociationRestored = sameDeviceRef(deviceAssociationRef(detail, kind), oldRef);
					if (!deviceAssociationRestored) errors.push('original device association readback does not match the pre-rebind identity');
				}
				catch (err) { errors.push(`device association rollback: ${describeThrown(err)}`); }
			}

			let reusedCloneRestored = true;
			if (clonedDevice && !clonedDevice.owned && clonedDevice.previousRef) {
				const previousAssoc = kind === 'footprint'
					? { footprint: clonedDevice.previousRef }
					: { symbol: clonedDevice.previousRef };
				try {
					await withTimeout(
						Promise.resolve(eda.lib_Device.modify(clonedDevice.uuid, clonedDevice.libraryUuid, undefined, undefined, previousAssoc)),
						REBIND_OP_TIMEOUT_MS,
						`Rollback of the reused clone association did not settle within ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					const detail = await withTimeout(
						Promise.resolve(eda.lib_Device.get(clonedDevice.uuid, clonedDevice.libraryUuid)),
						REBIND_OP_TIMEOUT_MS,
						`Reused-clone rollback readback timed out after ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					reusedCloneRestored = sameDeviceRef(deviceAssociationRef(detail, kind), clonedDevice.previousRef);
					if (!reusedCloneRestored) errors.push('reused clone association readback does not match its pre-rebind identity');
				}
				catch (err) { reusedCloneRestored = false; errors.push(`reused clone rollback: ${describeThrown(err)}`); }
			}

			let oldPresence = await readRebindComponent(primitiveId);
			let restoredOriginalId: string | undefined;
			let restoredOriginalVerified = oldPresence.present === true;
			if (oldPresence.present === false && deviceAssociationRestored) {
				try {
					const restored = await createPlaced(device, 'rollback-original', id => { restoredOriginalId = id; });
					await restoreAndVerify(restored.getState_PrimitiveId(), device, oldRef, 'rollback-original');
					restoredOriginalVerified = true;
				}
				catch (err) { errors.push(`original instance restore: ${describeThrown(err)}`); }
			}
			else if (oldPresence.present === null) {
				errors.push(`original instance presence unverified${oldPresence.error ? `: ${oldPresence.error}` : ''}`);
			}

			let ownedCloneRemoved = !clonedDevice?.owned;
			if (clonedDevice?.owned) {
				try {
					await withTimeout(
						Promise.resolve(eda.lib_Device.delete(clonedDevice.uuid, clonedDevice.libraryUuid)),
						REBIND_OP_TIMEOUT_MS,
						`Rollback delete for owned clone "${clonedDevice.uuid}" did not settle within ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					const after = await withTimeout(
						Promise.resolve(eda.lib_Device.get(clonedDevice.uuid, clonedDevice.libraryUuid)),
						REBIND_OP_TIMEOUT_MS,
						`Owned-clone delete readback timed out after ${REBIND_OP_TIMEOUT_MS}ms.`,
					);
					ownedCloneRemoved = !after;
					if (!ownedCloneRemoved) errors.push('owned clone still exists after rollback delete');
				}
				catch (err) { errors.push(`owned clone rollback: ${describeThrown(err)}`); }
			}

			oldPresence = await readRebindComponent(primitiveId);
			const rollbackVerified = candidatePresence.present === false
				&& (oldPresence.present === true || restoredOriginalVerified)
				&& deviceAssociationRestored && reusedCloneRestored && ownedCloneRemoved && errors.length === 0;
			return {
				attempted: true,
				verified: rollbackVerified,
				oldPrimitive: { primitiveId, present: oldPresence.present, ...(oldPresence.error ? { error: oldPresence.error } : {}) },
				replacement: { primitiveId: candidateId ?? null, present: candidatePresence.present, mayLandLate: candidateMayLandLate },
				...(restoredOriginalId ? { restoredOriginal: { primitiveId: restoredOriginalId, verified: restoredOriginalVerified } } : {}),
				deviceAssociationRestored,
				reusedCloneRestored,
				ownedCloneRemoved,
				errors,
			};
		};

		const failWithRollback = async (err: unknown): Promise<never> => {
			const failedPhase = phase;
			const rollbackFacts = await rollback();
			const oldNow = await readRebindComponent(primitiveId);
			const candidateNow = candidateId ? await readRebindComponent(candidateId) : { present: candidateMayLandLate ? null : false };
			const facts = {
				phase: failedPhase,
				oldPrimitive: { primitiveId, present: oldNow.present, ...(oldNow.error ? { error: oldNow.error } : {}) },
				replacement: { primitiveId: candidateId ?? null, present: candidateNow.present, mayLandLate: candidateMayLandLate },
				rollback: rollbackFacts,
				cause: describeThrown(err),
			};
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Schematic ${kind} rebind failed during phase "${failedPhase}". Do not blindly retry or run PCB import-changes; use a fresh schematic read to reconcile the old and replacement instances first.`,
				rebindFactsDetail(facts),
			);
		};

		// Create and freshly read the replacement while the original still exists.
		// A hung/failed create can therefore never be followed by deleting the only
		// known-good instance.
		let candidate: SchComponent;
		try {
			candidate = await createPlaced(effectiveDevice, 'candidate', id => { candidateId = id; });
		}
		catch (err) {
			candidateMayLandLate = /may still land late|did not settle/.test(describeThrown(err));
			return failWithRollback(err);
		}
		candidateId = candidate.getState_PrimitiveId();
		rlog(`candidate verified ${candidateId}`);

		phase = 'delete-original';
		try {
			await withTimeout(
				Promise.resolve(eda.sch_PrimitiveComponent.delete(primitiveId)),
				REBIND_OP_TIMEOUT_MS,
				`Original-instance delete did not settle within ${REBIND_OP_TIMEOUT_MS}ms. It may have applied; do not retry until fresh readback.`,
			);
			const oldAfterDelete = await readRebindComponent(primitiveId);
			if (oldAfterDelete.present !== false) {
				throw new ActionError(
					ErrorCodes.EDA_CALL_FAILED,
					`Original instance "${primitiveId}" was not proven absent after delete.`,
					oldAfterDelete.error,
				);
			}
			oldDeleted = true;
		}
		catch (err) { return failWithRollback(err); }
		rlog('old instance deletion verified');

		phase = 'restore-and-verify';
		let created: SchComponent;
		try {
			created = await restoreAndVerify(candidateId, effectiveDevice, target, 'replacement');
		}
		catch (err) { return failWithRollback(err); }
		rlog(`replacement identity verified ${created.getState_PrimitiveId()}`);

		const warnings = [
			`Re-placing minted a new primitiveId; wires on the old instance's pins may need re-drawing — run \`sch drc\` / \`sch check\` to confirm connectivity.`,
		];
		if (clonedDevice) {
			warnings.push(
				`The original device record is read-only (system library), so the component now references a personal-library clone "${clonedDevice.name}" carrying the new ${kind}. BOM identity (manufacturer/supplier) was restored from the original part.`,
			);
		}
		return {
			result: {
				primitiveId: created.getState_PrimitiveId(),
				previousPrimitiveId: primitiveId,
				rebound: kind,
				mode: clonedDevice ? 'cloned-to-personal-library' : 'in-place',
				device: { uuid: effectiveDevice.uuid, libraryUuid: effectiveDevice.libraryUuid },
				...(clonedDevice ? {
					clonedDevice: { uuid: clonedDevice.uuid, libraryUuid: clonedDevice.libraryUuid, name: clonedDevice.name },
					originalDevice: { uuid: device.uuid, libraryUuid: device.libraryUuid },
				} : {}),
				[kind]: { uuid: target.uuid, libraryUuid: target.libraryUuid, name: target.name },
				component: serializeComponent(created),
				transaction: {
					order: ['candidate-create', 'candidate-readback', 'delete-original', 'restore-and-verify'],
					oldDeleted,
					verified: true,
				},
			},
			warnings,
		};
	};
}

const schematicRebindFootprint: Handler = makeRebindHandler('footprint');
const schematicRebindSymbol: Handler = makeRebindHandler('symbol');

// ─── Text primitives: read-only enumeration (#156) ────────────────────

/**
 * List all text primitives on the ACTIVE schematic page — the missing typed
 * read that previously forced `debug.exec_js` for cleaning up stale zone-draw
 * labels (#156). Read-only; pair with `schematic.primitives.delete` to remove
 * orphans. Page-lazy-load law applies: only the active page's texts are seen.
 */
const schematicTextList: Handler = async () => {
	let texts;
	try { texts = await eda.sch_PrimitiveText.getAll(); }
	catch (err) { throw edaError(err, 'Failed to list schematic text primitives.'); }
	const items = (Array.isArray(texts) ? texts : []).map(t => ({
		primitiveId: t.getState_PrimitiveId(),
		content: t.getState_Content(),
		x: t.getState_X(),
		y: t.getState_Y(),
		rotation: t.getState_Rotation(),
		fontSize: t.getState_FontSize(),
		fontName: t.getState_FontName(),
		color: t.getState_TextColor(),
		bold: t.getState_Bold(),
		italic: t.getState_Italic(),
	}));
	return { result: { count: items.length, scope: 'activePage', texts: items } };
};

// ─── Replace: swap a placed component's DEVICE(器件标准化「使用推荐器件」)───

/** Pin identity snapshot used for the before/after diff of a device replace. */
interface PinSnapshot { pinNumber: string; pinName: string; x: number; y: number }

/** Best-effort read of a component's pins as plain snapshots (undefined = read failed). */
async function readPinSnapshots(primitiveId: string): Promise<Array<PinSnapshot> | undefined> {
	try {
		const pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(primitiveId);
		if (!Array.isArray(pins)) return undefined;
		return pins.map(p => ({
			pinNumber: String(p.getState_PinNumber() ?? ''),
			pinName: String(p.getState_PinName() ?? ''),
			x: p.getState_X(),
			y: p.getState_Y(),
		}));
	}
	catch { return undefined; }
}

/**
 * Diff two pin snapshots by pinNumber. `moved` compares absolute canvas
 * coordinates — valid because the replacement is re-placed at the SAME x/y as
 * the original, so any coordinate delta is a real symbol-geometry difference.
 */
export function diffPins(
	oldPins: Array<PinSnapshot>,
	newPins: Array<PinSnapshot>,
): { removed: Array<PinSnapshot>; added: Array<PinSnapshot>; moved: Array<{ pinNumber: string; pinName: string; from: { x: number; y: number }; to: { x: number; y: number } }> } {
	const oldByNumber = new Map(oldPins.map(p => [p.pinNumber, p]));
	const newByNumber = new Map(newPins.map(p => [p.pinNumber, p]));
	const removed = oldPins.filter(p => !newByNumber.has(p.pinNumber));
	const added = newPins.filter(p => !oldByNumber.has(p.pinNumber));
	const moved: Array<{ pinNumber: string; pinName: string; from: { x: number; y: number }; to: { x: number; y: number } }> = [];
	for (const [num, oldPin] of oldByNumber) {
		const newPin = newByNumber.get(num);
		if (newPin && (newPin.x !== oldPin.x || newPin.y !== oldPin.y)) {
			moved.push({ pinNumber: num, pinName: newPin.pinName, from: { x: oldPin.x, y: oldPin.y }, to: { x: newPin.x, y: newPin.y } });
		}
	}
	return { removed, added, moved };
}

/**
 * Resolve a placed component's REAL 32-char device-library uuid. Needed because
 * `getState_Component().uuid` is a 16-char placed-SYMBOL id, NOT the device
 * uuid `sch_PrimitiveComponent.create` consumes (verified live 2026-08-04:
 * placing device 96b39256… read back as component.uuid 125464da5fa306a1) — so
 * a rollback that replays it would fail to re-create the original part.
 * Resolution chain: LCSC C-number (deterministic) → manufacturerId search
 * narrowed by footprint name → device-name search in the PROJECT library (the
 * home of imported Altium/KiCad devices, which have neither a C-number nor a
 * library-searchable MPN). Throws when unresolvable — the caller must abort
 * BEFORE touching the canvas rather than run without a working rollback.
 */
/** A resolution candidate surfaced when the safe chain refuses to pick. */
interface LcscCandidate {
 name: string; lcsc: string; footprintName: string; uuid: string;
 libraryUuid: string; footprintUuid: string; footprintLibraryUuid: string;
}

/** Structured outcome of the safe placed-part → device resolution (#158). */
interface DeviceResolution {
	device?: DeviceRef & { via: string };
	lcsc?: string;
	deviceFootprint?: string;
	footprintSource?: { instanceUuid: string; uuid: string; libraryUuid: string; sourceKind?: 'project-epro2' };
	reason?: string;
	candidates?: Array<LcscCandidate>;
}

const asCandidate = (r: Record<string, unknown>): LcscCandidate => {
 const footprint = readDeviceFootprint(r);
 return {
  name: String(r.name ?? ''), lcsc: String(r.supplierId ?? ''), uuid: String(r.uuid ?? ''),
  libraryUuid: String(r.libraryUuid ?? ''), footprintName: footprint.name,
  footprintUuid: footprint.uuid, footprintLibraryUuid: footprint.libraryUuid,
 };
};

const identityRecord = (value: unknown): Record<string, unknown> =>
	value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
const identityText = (value: unknown): string => typeof value === 'string' ? value : '';
const instanceIdentityUuid = (value: unknown): boolean => /^[0-9a-f]{16}$/i.test(identityText(value));
const libraryIdentityUuid = (value: unknown): boolean => /^[0-9a-f]{32}$/i.test(identityText(value));
const stableIdentityName = (value: unknown): string => {
	const name = identityText(value);
	return name && !name.startsWith('=') ? name : '';
};

interface NativeFootprintInventory { entries?: unknown; error?: string }
async function loadNativeFootprintInventory(expectedContext?: { projectUuid?: string; documentUuid?: string }): Promise<NativeFootprintInventory> {
	try {
		const before = await readResponseContext();
		if (!before.projectUuid || !before.documentUuid) return { error: 'native footprint source requires a known current project and document' };
		if (expectedContext && (before.projectUuid !== expectedContext.projectUuid || before.documentUuid !== expectedContext.documentUuid)) return { error: 'project/document changed since the component identity snapshot' };
		let entries: unknown;
		let documentError = '';
		try {
			entries = await withTimeout(eda.sys_FileManager.getDocumentFootprintSources(), 7000, 'identity getDocumentFootprintSources timed out after 7000ms');
		} catch (err) { documentError = describeThrown(err); }
		if (!Array.isArray(entries) || entries.length === 0) {
			// The SDK's document-footprint API returns [] in schematic editors.
			// The official current-project epro2 archive retains DOCHEAD/META.source.
			if (typeof eda.sys_FileManager?.getProjectFile !== 'function') return { error: `official footprint source inventory unavailable; project export unavailable${documentError ? ` (${documentError})` : ''}` };
			const archive = await withTimeout(eda.sys_FileManager.getProjectFile('easyeda-agent-identity.epro2', undefined, 'epro2'), 10000, 'identity getProjectFile timed out after 10000ms');
			if (!archive) return { error: 'official project export did not return a source archive' };
			entries = await withTimeout(readProjectFootprintSourceArchive(archive, before.documentUuid), 7000, 'identity source archive decoding timed out after 7000ms');
		}
		if (!Array.isArray(entries) || entries.length > 2048) return { error: 'official footprint source inventory is incomplete or exceeds 2048 entries' };
		const after = await readResponseContext();
		if (after.projectUuid !== before.projectUuid || after.documentUuid !== before.documentUuid) return { error: 'project/document changed while reading native footprint source' };
		return { entries };
	} catch (err) {
		return { error: `official footprint source query failed: ${describeThrown(err)}` };
	}
}

/**
 * EasyEDA can expose 16-hex project instance refs for BOTH device and footprint.
 * They cannot equal the 32-hex library refs. This narrow recovery is deliberately
 * separate from footprintMatchesInstance, whose asset-UUID comparisons stay strict.
 * Native DOCHEAD/META.source supplies the actual 16→32 footprint origin. A full
 * LCSC candidate set and official get() association must identify that exact
 * source asset. Matching package names without native source is insufficient
 * for reconstruction and never hydrates a replayable device identity.
 */
async function resolveInstanceFootprintDevice(
	snapshot: Record<string, unknown>,
	getNativeFootprints: () => Promise<NativeFootprintInventory>,
): Promise<DeviceResolution> {
	const instanceFp = readDeviceFootprint(snapshot);
	const supplierId = identityText(snapshot.supplierId);
	const mpn = identityText(snapshot.manufacturerId);
	const sourceName = stableIdentityName(identityRecord(snapshot.component).name)
		|| stableIdentityName(snapshot.name) || stableIdentityName(identityRecord(snapshot.device).name);
	const context = `instance footprint uuid=${JSON.stringify(instanceFp.uuid)}, libraryUuid=${JSON.stringify(instanceFp.libraryUuid)}`;
	const failure = (reason: string, candidates: Array<LcscCandidate> = []): DeviceResolution => ({
		reason: `${reason}; ${context}; instance/library UUID domains require native META.source plus exact LCSC/model and official asset-library evidence`,
		...(candidates.length ? { candidates: candidates.slice(0, 5) } : {}),
	});
	if (!/^C\d+$/.test(supplierId) || !instanceFp.libraryUuid || (!mpn && !sourceName)) {
		return failure('incomplete identity evidence for 16-hex instance footprint');
	}
	const inventory = await getNativeFootprints();
	if (inventory.error) return failure(inventory.error);
	const native = readNativeFootprintSource(inventory.entries, instanceFp.uuid);
	if (!native.source) return failure(native.error ?? 'native footprint source is missing; name-only evidence cannot establish reconstruction identity');
	if (native.source.libraryUuid !== instanceFp.libraryUuid) return failure('native footprint source library conflicts with the placed footprint provenance');
	let raw: Array<Record<string, unknown>>;
	try {
		// Without allowMultiMatch=true the API may hide an equally valid device.
		const queried = await withTimeout(eda.lib_Device.getByLcscIds([supplierId], undefined, true), 7000, `identity getByLcscIds ${supplierId} timed out after 7000ms`);
		if (!Array.isArray(queried)) return failure('complete LCSC candidate inventory unavailable');
		raw = queried as unknown as Array<Record<string, unknown>>;
	} catch (err) {
		return failure(`complete LCSC candidate query failed: ${describeThrown(err)}`);
	}
	const candidates = raw.map(asCandidate);
	const matches = new Map<string, Record<string, unknown>>();
	const unresolvedEvidence: Array<string> = [];
	for (const hit of raw) {
		const searchProperties = identityRecord(hit.otherProperty);
		const searchLcsc = identityText(hit.supplierId ?? searchProperties['Supplier Part']);
		const searchMpn = identityText(hit.manufacturerId ?? searchProperties['Manufacturer Part']);
		const searchName = stableIdentityName(hit.name);
		// A known different model is not an ambiguity. Missing/conflicting detail
		// evidence for a plausible candidate must never be discarded to pick a first hit.
		if (searchLcsc && searchLcsc !== supplierId) continue;
		if (mpn ? searchMpn && searchMpn !== mpn : searchName && searchName !== sourceName) continue;
		const searchFp = readDeviceFootprint(hit);
		if (!libraryIdentityUuid(hit.uuid) || !identityText(hit.libraryUuid) || !libraryIdentityUuid(searchFp.uuid)) {
			unresolvedEvidence.push('candidate lacks a library device/footprint identity');
			continue;
		}
		let detail: Record<string, unknown>;
		try {
			detail = identityRecord(await withTimeout(eda.lib_Device.get(hit.uuid as string, hit.libraryUuid as string), 7000, `identity device.get ${hit.uuid} timed out after 7000ms`));
		} catch (err) {
			unresolvedEvidence.push(`device.get ${hit.uuid} failed: ${describeThrown(err)}`);
			continue;
		}
		const property = identityRecord(detail.property);
		const associationFp = readDeviceFootprint({ association: detail.association });
		if (detail.uuid !== hit.uuid || detail.libraryUuid !== hit.libraryUuid
			|| !libraryIdentityUuid(associationFp.uuid) || !associationFp.libraryUuid
			|| associationFp.uuid !== searchFp.uuid
			|| (searchFp.libraryUuid && searchFp.libraryUuid !== associationFp.libraryUuid)) {
			unresolvedEvidence.push(`device.get ${hit.uuid} identity/footprint association is missing or conflicts with the search candidate`);
			continue;
		}
		if (property.supplierId !== supplierId || (mpn
			? property.manufacturerId !== mpn
			: (stableIdentityName(detail.name) !== sourceName && stableIdentityName(property.name) !== sourceName))) {
			unresolvedEvidence.push(`device.get ${hit.uuid} lacks matching exact LCSC and model/name evidence`);
			continue;
		}
		if (associationFp.uuid !== native.source.uuid || associationFp.libraryUuid !== native.source.libraryUuid) continue;
		const proven = { ...hit, footprint: { ...searchFp, libraryUuid: associationFp.libraryUuid } };
		matches.set(JSON.stringify([hit.libraryUuid, hit.uuid]), proven);
	}
	if (unresolvedEvidence.length) return failure(unresolvedEvidence.join('; '), candidates);
	if (matches.size !== 1) return failure(matches.size
		? `${matches.size} distinct devices match exact LCSC/model and native footprint source — ambiguous`
		: 'no device matches exact LCSC/model and native footprint source (package-variant mismatch)', candidates);
	const hit = [...matches.values()][0];
	return {
		device: { uuid: hit.uuid as string, libraryUuid: hit.libraryUuid as string, via: 'lcsc-footprint-source' },
		lcsc: supplierId,
		deviceFootprint: readDeviceFootprint(hit).name,
		footprintSource: native.source,
	};
}

/**
 * Safe structured resolver behind resolvePlacedDeviceIdentity — NEVER falls
 * back to an unrelated first hit (#158: a bare `search("U.FL-R-SMT-1(01)")`
 * returns fragment-matched garbage — a ferrite bead ranked first — and a
 * take-r[0] caller silently swaps an antenna socket for C1017). Match rules:
 * exact-field equality only (manufacturerId / name), and when the instance
 * knows its footprint the match MUST carry the same footprint asset — a lone
 * hit with a DIFFERENT footprint is a package-variant mismatch (SMBJ33A LS5.4
 * vs LS5.3 family), reported as unresolved WITH candidates instead of picked.
 *
 * "Same footprint asset" is decided by footprintMatchesInstance: footprint uuid
 * when both sides have one, otherwise a trimmed case-INSENSITIVE name compare.
 * The platform lower-cases an instance's footprint name (`r0603`) while the
 * library keeps the authored one (`R0603`), so a `===` compare mis-reported the
 * whole board as package-variant mismatches (T-3: 26/26 parts unresolved;
 * T-16: `sch replace` refused, since it resolves the OLD device through here).
 */
async function resolvePlacedDevice(
	snapshot: Record<string, unknown>,
	getNativeFootprints: () => Promise<NativeFootprintInventory> = loadNativeFootprintInventory,
): Promise<DeviceResolution> {
	const instanceFp = readDeviceFootprint(snapshot);
	if (instanceIdentityUuid(identityRecord(snapshot.device).uuid) && instanceIdentityUuid(instanceFp.uuid)) {
		return resolveInstanceFootprintDevice(snapshot, getNativeFootprints);
	}
	const fpIdentity = instanceFp.name || instanceFp.uuid || instanceFp.libraryUuid;
	const finish = (hits: Array<Record<string, unknown>>, via: string): DeviceResolution | undefined => {
		let pool = hits.filter(r => typeof r.uuid === 'string' && typeof r.libraryUuid === 'string');
		const before = pool;
		if (fpIdentity) pool = pool.filter(r => footprintMatchesInstance(instanceFp, r));
		if (pool.length === 1) {
			const hit = pool[0];
			return {
				device: { uuid: hit.uuid as string, libraryUuid: hit.libraryUuid as string, via },
				lcsc: /^C\d+$/.test(String(hit.supplierId ?? '')) ? String(hit.supplierId) : undefined,
				deviceFootprint: readDeviceFootprint(hit).name,
			};
		}
		if (before.length > 0) {
			return {
				reason: pool.length === 0
					? `matched ${before.length} device(s) by ${via} but NONE carries the instance footprint "${fpIdentity}" (package-variant mismatch; instance footprint uuid=${JSON.stringify(instanceFp.uuid)}, libraryUuid=${JSON.stringify(instanceFp.libraryUuid)}; UUID/library identity takes priority over name)`
					: `${pool.length} devices match by ${via} + footprint — ambiguous`,
				candidates: before.slice(0, 5).map(asCandidate),
			};
		}
		return undefined;
	};

	const supplierId = typeof snapshot.supplierId === 'string' ? snapshot.supplierId : '';
	if (/^C\d+$/.test(supplierId)) {
		try {
			const raw = (await eda.lib_Device.getByLcscIds([supplierId], undefined, true)) as unknown as Array<Record<string, unknown>>;
			const r = finish(Array.isArray(raw) ? raw : [], 'lcsc');
			if (r) return r;
		}
		catch { /* fall through to the MPN chain */ }
	}
	const mpn = typeof snapshot.manufacturerId === 'string' ? snapshot.manufacturerId : '';
	if (mpn) {
		let raw: Array<Record<string, unknown>> = [];
		try { raw = (await eda.lib_Device.search(mpn)) as unknown as Array<Record<string, unknown>>; }
		catch { /* handled below */ }
		const r = finish((Array.isArray(raw) ? raw : []).filter(h => h.manufacturerId === mpn), 'mpn');
		if (r) return r;
	}
	// Imported (Altium/KiCad) devices live in the project library under their
	// device name, with no C-number and no online-searchable MPN.
	const name = typeof snapshot.name === 'string' ? snapshot.name : '';
	if (name && !name.startsWith('={')) {
		let raw: Array<Record<string, unknown>> = [];
		try { raw = (await eda.lib_Device.search(name, 'project')) as unknown as Array<Record<string, unknown>>; }
		catch { /* handled below */ }
		const r = finish((Array.isArray(raw) ? raw : []).filter(h => h.name === name), 'project-name');
		if (r) return r;
	}
	return { reason: 'no exact LCSC/MPN/project-name library match' };
}

async function resolvePlacedDeviceIdentity(
	snapshot: Record<string, unknown>,
): Promise<DeviceRef & { via: string }> {
	const res = await resolvePlacedDevice(snapshot);
	if (res.device) return res.device;
	throw new ActionError(
		ErrorCodes.INVALID_STATE,
		`Cannot resolve the placed component's 32-char device-library uuid (${res.reason ?? 'no match'}) — `
		+ 'aborting BEFORE any canvas change because rollback would be impossible. '
		+ 'Note: the component\'s own device.uuid is a 16-char placed-symbol id and cannot re-create the part.',
	);
}

/**
 * schematic.component.resolve_lcsc (#158) — batch-resolve every placed part on
 * the ACTIVE page to its device's REAL LCSC C-number, deterministically:
 * exact-match chain only (instance C# → MPN → project-name), footprint must
 * agree, NEVER pick an unrelated first hit; anything not uniquely provable is
 * reported as unresolved WITH candidates for a human call. apply=true writes
 * the resolved C-number back onto instances whose supplierId is not a real C#
 * (the platform's subPartName default, #157) — the one-command version of the
 * 166-part supplierId repair that motivated the issue.
 */
const schematicComponentResolveLcsc: Handler = async (payload) => {
	const onlyId = optionalString(payload, 'primitiveId');
	const apply = optionalBoolean(payload, 'apply') === true;

	let comps;
	try { comps = await eda.sch_PrimitiveComponent.getAll(); }
	catch (err) { throw edaError(err, 'Failed to list schematic components.'); }
	const parts = (comps ?? []).filter((c) => {
		try {
			if (String(c.getState_ComponentType()) !== 'part') return false;
			return !onlyId || c.getState_PrimitiveId() === onlyId;
		}
		catch { return false; }
	});
	if (onlyId && parts.length === 0) {
		throw new ActionError(ErrorCodes.INVALID_STATE, `No part with primitiveId "${onlyId}" on the active page.`);
	}

	// Reuse only identical resolver inputs, including the project-name fallback
	// and footprint asset identity; same-named package variants are not aliases.
	const cache = new Map<string, DeviceResolution>();
	const items: Array<Record<string, unknown>> = [];
	const unresolved: Array<Record<string, unknown>> = [];
	let appliedCount = 0;

	for (const comp of parts) {
		const snapshot = serializeComponent(comp);
		const designator = String(snapshot.designator ?? '');
		const current = typeof snapshot.supplierId === 'string' ? snapshot.supplierId : '';
		const footprint = readDeviceFootprint(snapshot);
		const fp = footprint.name;
		if (/^C\d+$/.test(current)) {
			items.push({ designator, lcsc: current, via: 'instance', footprint: fp });
			continue;
		}
		const mpn = typeof snapshot.manufacturerId === 'string' ? snapshot.manufacturerId : '';
		const cacheKey = JSON.stringify([
			current, mpn, typeof snapshot.name === 'string' ? snapshot.name : '',
			footprint.uuid, footprint.libraryUuid, footprint.name.toLowerCase(),
		]);
		let res = cache.get(cacheKey);
		if (!res) {
			res = await resolvePlacedDevice(snapshot);
			cache.set(cacheKey, res);
		}
		if (!res.lcsc) {
			unresolved.push({
				designator,
				mpn,
				footprint: fp,
				reason: res.reason ?? (res.device ? 'device resolved but carries no LCSC C-number' : 'no match'),
				...(res.candidates ? { candidates: res.candidates } : {}),
			});
			continue;
		}
		const item: Record<string, unknown> = { designator, lcsc: res.lcsc, via: res.device?.via, footprint: fp, previousSupplierId: current };
		if (apply) {
			try {
				const m = await eda.sch_PrimitiveComponent.modify(comp.getState_PrimitiveId(), { supplierId: res.lcsc });
				item.applied = Boolean(m);
				if (m) appliedCount++;
			}
			catch { item.applied = false; }
		}
		items.push(item);
	}

	return {
		result: {
			total: parts.length,
			resolvedCount: items.length,
			unresolvedCount: unresolved.length,
			...(apply ? { appliedCount } : {}),
			items,
			...(unresolved.length ? { unresolved } : {}),
			scope: 'activePage',
		},
		...(unresolved.length
			? { warnings: [`${unresolved.length} part(s) could not be resolved deterministically — review result.unresolved (candidates included); nothing was guessed.`] }
			: {}),
	};
};

/**
 * Replace a placed component with a DIFFERENT device (真正的「换型号」) — the
 * programmatic equivalent of the 器件标准化 panel's「使用推荐器件」button, which
 * has no extension API of its own (the panel only exposes an open-tab enum).
 *
 * WHY delete-then-create: the official API has NO rebind-device primitive —
 * `sch_PrimitiveComponent.modify` cannot change the placed instance's device
 * binding (no libraryUuid/uuid in its property table, no setState_Device).
 * So this follows the rebind template minus the `lib_Device.modify` step:
 *   1. capture the original state + pin table,
 *   2. resolve BOTH device identities (old for rollback, new from
 *      lcsc / deviceUuid / query),
 *   3. delete the old instance,
 *   4. create the new device at the same x/y/rotation/mirror/BOM flags,
 *   5. modify to restore designator + uniqueId (kept so sch↔PCB import-changes
 *      UPDATES the footprint instead of delete+add).
 *
 * Deliberately NOT restored: name / manufacturer(Id) / supplier(Id) — those
 * identify the PART, and carrying the old part's identity onto the new device
 * would defeat the replacement. otherProperty is only restored with
 * keepProperties=true (old custom attrs may describe the old part).
 *
 * Any failure after the delete rolls back by re-creating the ORIGINAL device
 * with its full state (including part-identity fields).
 */
const schematicComponentReplace: Handler = async (payload) => {
	const primitiveId = requireString(payload, 'primitiveId');
	const lcsc = optionalString(payload, 'lcsc');
	const explicitUuid = optionalString(payload, 'deviceUuid');
	const explicitLibraryUuid = optionalString(payload, 'deviceLibraryUuid');
	const query = optionalString(payload, 'query');
	const keepProperties = optionalBoolean(payload, 'keepProperties') === true;
	const selectors = [lcsc, explicitUuid, query].filter(Boolean).length;
	if (selectors !== 1) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Provide exactly ONE target selector: "lcsc" (C-number), "deviceUuid" (+ "deviceLibraryUuid"), or "query" (device name, must match uniquely).',
		);
	}

	const component = await getComponentOrThrow(primitiveId);
	const snapshot = serializeComponent(component);
	const oldName = typeof snapshot.name === 'string' ? snapshot.name : undefined;
	// Resolve the OLD device's REAL 32-char library identity up front — it is the
	// rollback target AND the same-device guard; getState_Component() only carries
	// a 16-char placed-symbol id, so an unresolvable identity aborts here, before
	// any canvas change.
	const oldDevice = await resolvePlacedDeviceIdentity(snapshot);
	const oldPins = await readPinSnapshots(primitiveId);

	// Resolve the NEW device identity.
	let target: { uuid: string; libraryUuid: string; name?: string };
	if (explicitUuid) {
		if (!explicitLibraryUuid) {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				'"deviceUuid" requires "deviceLibraryUuid" (both come from schematic.library.search / get_by_lcsc).',
			);
		}
		target = { uuid: explicitUuid, libraryUuid: explicitLibraryUuid };
	}
	else if (lcsc) {
		let raw: Array<Record<string, unknown>>;
		try {
			raw = (await eda.lib_Device.getByLcscIds([lcsc], undefined, true)) as unknown as Array<Record<string, unknown>>;
		}
		catch (err) {
			throw edaError(err, `Failed to resolve LCSC id "${lcsc}".`);
		}
		const hits = (Array.isArray(raw) ? raw : []).filter(r => typeof r.uuid === 'string' && typeof r.libraryUuid === 'string');
		if (hits.length === 0) {
			throw new ActionError(ErrorCodes.INVALID_STATE, `LCSC id "${lcsc}" did not resolve to any library device.`);
		}
		if (hits.length > 1) {
			const uuids = hits.map(h => h.uuid).join(', ');
			throw new ActionError(
				ErrorCodes.INVALID_STATE,
				`LCSC id "${lcsc}" resolved to ${hits.length} devices (uuids: ${uuids}). Pass "deviceUuid" to pick one.`,
			);
		}
		target = { uuid: hits[0].uuid as string, libraryUuid: hits[0].libraryUuid as string, name: typeof hits[0].name === 'string' ? hits[0].name as string : undefined };
	}
	else {
		let raw: Array<NamedLibItem>;
		try {
			raw = (await eda.lib_Device.search(query as string)) as unknown as Array<NamedLibItem>;
		}
		catch (err) {
			throw edaError(err, `Failed to search the device library for "${query}".`);
		}
		const match = pickNamedCandidate(query as string, Array.isArray(raw) ? raw : []);
		if (match.kind === 'none') {
			throw new ActionError(
				ErrorCodes.INVALID_STATE,
				`No device named "${query}" found. Use schematic.library.search to explore candidates, then pass "deviceUuid" or "lcsc".`,
			);
		}
		if (match.kind === 'ambiguous') {
			const uuids = match.matches.map(m => m.uuid).join(', ');
			throw new ActionError(
				ErrorCodes.INVALID_STATE,
				`${match.matches.length} devices named "${query}" match (uuids: ${uuids}). Pass "deviceUuid" to pick one.`,
			);
		}
		target = match.item;
	}
	if (target.uuid === oldDevice.uuid) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			`Target device is the SAME as the placed one (${target.uuid}) — nothing to replace. Use schematic.component.modify for property-only changes.`,
		);
	}

	// Replay geometry + BOM flags into create for both the new device and rollback.
	const x = typeof snapshot.x === 'number' ? snapshot.x : 0;
	const y = typeof snapshot.y === 'number' ? snapshot.y : 0;
	const rotation = typeof snapshot.rotation === 'number' ? snapshot.rotation : undefined;
	const mirror = typeof snapshot.mirror === 'boolean' ? snapshot.mirror : undefined;
	const addIntoBom = typeof snapshot.addIntoBom === 'boolean' ? snapshot.addIntoBom : undefined;
	const addIntoPcb = typeof snapshot.addIntoPcb === 'boolean' ? snapshot.addIntoPcb : undefined;
	// Identity-preserving props for the NEW device: designator + uniqueId only.
	const carryProps = {
		...(typeof snapshot.designator === 'string' ? { designator: snapshot.designator } : {}),
		...(typeof snapshot.uniqueId === 'string' ? { uniqueId: snapshot.uniqueId } : {}),
		...(keepProperties && cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined)
			? { otherProperty: cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined) }
			: {}),
	};
	// Full restore props for ROLLBACK (the original part keeps its identity).
	const rollbackProps = {
		...carryProps,
		...(typeof snapshot.manufacturer === 'string' ? { manufacturer: snapshot.manufacturer } : {}),
		...(typeof snapshot.manufacturerId === 'string' ? { manufacturerId: snapshot.manufacturerId } : {}),
		...(typeof snapshot.supplier === 'string' ? { supplier: snapshot.supplier } : {}),
		...(typeof snapshot.supplierId === 'string' ? { supplierId: snapshot.supplierId } : {}),
		...(cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined)
			? { otherProperty: cleanOtherProperty(snapshot.otherProperty as Record<string, unknown> | undefined) }
			: {}),
	};

	const place = async (dev: DeviceRef, props: Record<string, unknown>): Promise<SchComponent | undefined> => {
		let c = await eda.sch_PrimitiveComponent.create(
			{ libraryUuid: dev.libraryUuid, uuid: dev.uuid },
			x, y, undefined, rotation, mirror, addIntoBom, addIntoPcb,
		);
		if (c && Object.keys(props).length) {
			// Prefer modify's returned primitive: serializing the create-time object
			// echoes PRE-restore state (live-verified: designator read back "C?"
			// while the canvas already showed the restored value).
			try {
				const m = await eda.sch_PrimitiveComponent.modify(c.getState_PrimitiveId(), props);
				if (m) c = m;
			}
			catch { /* best-effort restore */ }
		}
		// #157: carry the device's real LCSC C-number onto the instance.
		if (c) c = (await backfillSupplierId(c, dev)).component;
		return c ?? undefined;
	};

	// Step 3: delete the old instance.
	try {
		await eda.sch_PrimitiveComponent.delete(primitiveId);
	}
	catch (err) {
		throw edaError(err, `Failed to delete the old instance "${primitiveId}" (canvas unchanged).`);
	}

	// Step 4 + 5: place the new device and carry identity over.
	let created: SchComponent | undefined;
	let placeError: unknown;
	try {
		created = await place({ libraryUuid: target.libraryUuid, uuid: target.uuid }, carryProps);
	}
	catch (err) { placeError = err; }
	if (!created) {
		// Roll back: re-create the ORIGINAL device with its full state.
		let restored = false;
		try { restored = Boolean(await place(oldDevice, rollbackProps)); }
		catch { /* fall through to the structured error */ }
		const rollbackNote = restored
			? 'rolled back — the original component was re-created (with a NEW primitiveId; verify wires)'
			: 'ROLLBACK FAILED — the original component is GONE; re-place it manually';
		if (placeError) throw edaError(placeError, `Failed to place the replacement device (${rollbackNote}).`);
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Placing the replacement device returned no primitive (${rollbackNote}).`,
		);
	}

	const newId = created.getState_PrimitiveId();
	const newPins = await readPinSnapshots(newId);
	const pinDiff = oldPins && newPins
		? (() => {
			const d = diffPins(oldPins, newPins);
			return {
				available: true,
				oldCount: oldPins.length,
				newCount: newPins.length,
				removed: d.removed.slice(0, 20),
				added: d.added.slice(0, 20),
				moved: d.moved.slice(0, 20),
				removedCount: d.removed.length,
				addedCount: d.added.length,
				movedCount: d.moved.length,
			};
		})()
		: { available: false as const };

	const warnings = [
		'Re-placing minted a new primitiveId; wires on the old pins may need re-drawing — run `sch drc` / `sch check` to confirm connectivity.',
	];
	if (pinDiff.available && (pinDiff.removedCount || pinDiff.addedCount || pinDiff.movedCount)) {
		warnings.push(
			`Pin geometry differs from the old device (removed=${pinDiff.removedCount}, added=${pinDiff.addedCount}, moved=${pinDiff.movedCount}) — existing wires will NOT line up; re-wire the affected pins.`,
		);
	}

	return {
		result: {
			primitiveId: newId,
			previousPrimitiveId: primitiveId,
			previousDevice: { uuid: oldDevice.uuid, libraryUuid: oldDevice.libraryUuid, resolvedVia: oldDevice.via, ...(oldName ? { name: oldName } : {}) },
			device: { uuid: target.uuid, libraryUuid: target.libraryUuid, ...(target.name ? { name: target.name } : {}) },
			pinDiff,
			component: serializeComponent(created),
		},
		warnings,
	};
};

// ─── Composite: pin → wire → netflag/netport in one call ────────────

type Direction = 'up' | 'down' | 'left' | 'right';

/**
 * Compute the grid-snapped far endpoint for a connect_pin stub.
 *
 * Schematic coordinates are y-UP: visual up increases y and visual down
 * decreases y. Keep this pure helper in the handler path so the TypeScript
 * contract test and the Go autoconnect planner lock the same geometry.
 */
export function connectPinEndpoint(
	pinX: number,
	pinY: number,
	offset: number,
	direction: string,
): { x: number; y: number } {
	let endX = pinX;
	let endY = pinY;
	switch (direction) {
		case 'up': endY = pinY + offset; break;
		case 'down': endY = pinY - offset; break;
		case 'left': endX = pinX - offset; break;
		case 'right': endX = pinX + offset; break;
		default:
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown direction "${direction}"; expected up/down/left/right.`,
			);
	}
	return { x: nearestGrid(endX), y: nearestGrid(endY) };
}

/**
 * Default direction by kind. Power flows up to a + rail, ground falls down
 * to a 0V rail, an IN port comes from the left (the producer), an OUT/BI
 * port goes to the right (the consumer / shared bus). These match the §3.3
 * conventions in schematic-layout-conventions.md.
 */
function defaultDirection(kind: string): Direction {
	if (['ground', 'analog_ground', 'protective_ground', 'protect_ground'].includes(kind)) return 'down';
	if (kind === 'power') return 'up';
	if (kind === 'net_port_in') return 'left';
	return 'right'; // net_port_out, net_port_bi default
}

/**
 * Orientation rule: the flag body must point OUTWARD along the stub direction
 * (顺着导线方向), so the wire enters the flag from the circuit side and the
 * symbol never overlaps the wire/circuit.
 *
 * The whole table is DERIVED from four facts — the +90° body cycle and the body
 * direction at rotation 0 for each family. These are the SINGLE SOURCE OF TRUTH
 * mirrored in orientation.json; the lint harness
 * (tests/run.py) asserts that file derives the identical
 * table, so this writer and the linter's check can never drift. Re-validate the
 * anchors against live getPrimitivesBBox via calibrate.js
 * after importing a new .eext. See schematic-layout-conventions.md §3.5.
 */
// 2026-06-29 VERTICAL ORIENTATION CALIBRATION: the visual body cycle was confirmed
// as up→right→down→left, with the power/ground rot=0 anchors below. This calibration
// is independent of endpoint coordinate signs. Verified via getPrimitivesBBox on
// real settled flags; keep byte-identical to orientation.json
// (rotationCycle + bodyAnchorAtRot0) and orient.py.
// 2026-08-12 re-calibration (see orientation.json _doc): the 06-29 fix reversed
// the cycle and compensated with swapped anchors — horizontal lined up, vertical
// stayed inverted (an autoconnect-placed 3V3 stored 180 rendered upside-down and
// the linter blessed it). Counter-clockwise cycle + NATURAL anchors reproduces
// all five live measurements; left/right numbers are byte-identical to before.
const ROTATION_CYCLE: Direction[] = ['up', 'left', 'down', 'right'];
const BODY_ANCHOR_AT_ROT0: Record<'power' | 'ground' | 'port', Direction> = {
	power: 'up',
	ground: 'down',
	port: 'right',
};

// rotation that makes the body point `direction` = (idx(direction) - idx(anchor))
// mod 4, times 90 — keep this byte-equivalent to orient.py:derive().
function deriveBodyRotation(): Record<'power' | 'ground' | 'port', Record<Direction, number>> {
	const out = {} as Record<'power' | 'ground' | 'port', Record<Direction, number>>;
	for (const family of ['power', 'ground', 'port'] as const) {
		const anchorIdx = ROTATION_CYCLE.indexOf(BODY_ANCHOR_AT_ROT0[family]);
		const table = {} as Record<Direction, number>;
		for (const dir of ROTATION_CYCLE) {
			table[dir] = (((ROTATION_CYCLE.indexOf(dir) - anchorIdx) % 4) + 4) % 4 * 90;
		}
		out[family] = table;
	}
	return out;
}

const BODY_ROTATION = deriveBodyRotation();

function flagFamily(kind: string): 'power' | 'ground' | 'port' {
	if (['ground', 'analog_ground', 'protective_ground', 'protect_ground'].includes(kind)) return 'ground';
	if (kind.startsWith('net_port')) return 'port';
	return 'power';
}

function rotationFor(kind: string, direction: Direction): number {
	return BODY_ROTATION[flagFamily(kind)][direction];
}

// EasyEDA's createNetFlag/createNetPort STORES rotation negated relative to the value
// passed, on some connector/webview builds (empirically verified 2026-06: pass 90 →
// getState_Rotation re-pull reads 270 → a 'left' power flag renders pointing RIGHT).
// The earlier "identity, pass it straight" assumption produced backward HORIZONTAL
// flags (up/down are symmetric at 0/180 so it went unnoticed). We detect the behavior
// once at runtime and compensate, so orientation is correct whichever way the API
// behaves. DO NOT hard-revert to identity without re-checking connect_pin's RENDERED
// output (place a left flag, confirm it points left).
let rotationNegates: boolean | null = null;
async function detectRotationNegation(): Promise<boolean> {
	if (rotationNegates !== null) {
		return rotationNegates;
	}
	try {
		const probe = await eda.sch_PrimitiveComponent.createNetFlag('Power', '__ROTPROBE__', 990000, 990000, 90);
		if (!probe) {
			rotationNegates = false;
			return false;
		}
		const pid = probe.getState_PrimitiveId();
		// Re-pull (fresh getAll), NOT the immediate getState — the immediate value can
		// echo the input while the persisted value is the negated one.
		let stored = 90;
		for (const c of await eda.sch_PrimitiveComponent.getAll()) {
			if (c.getState_PrimitiveId() === pid) {
				stored = c.getState_Rotation();
				break;
			}
		}
		await deleteProbeVerified(pid);
		rotationNegates = stored === 270;
	}
	catch {
		rotationNegates = false;
	}
	return rotationNegates;
}

// Delete the one-shot rotation probe and PROVE it is gone.
//
// A bare `delete([pid])` is not enough: the platform's delete LIES (it resolves
// truthy while the primitive survives — the same defect that forces batched
// deletes + read-back everywhere else in this file). When it lied here, the
// probe flag stayed on the canvas forever, and `sch bridge-check` counted it as
// an orphan-flag — which made `sch gate --strict` (the S5 per-page gate) FAIL on
// an electrically perfect board (2026-08-26, esp32MiniRequire E2E).
//
// So: delete, re-read, retry. Give up loudly via sys_Log rather than silently —
// the daemon-side classifier (sch_tool_probe_residue.go) still recognises the
// residue by net name and tells the user how to clear it, but a probe that
// leaked is a connector bug we want visible in the log too.
async function deleteProbeVerified(pid: string): Promise<void> {
	const log = (m: string) => { try { eda.sys_Log.add(`[rotprobe] ${m}`); } catch { /* ignore */ } };
	const stillThere = async (): Promise<boolean> => {
		for (const c of await eda.sch_PrimitiveComponent.getAll()) {
			if (c.getState_PrimitiveId() === pid) return true;
		}
		return false;
	};
	for (let attempt = 1; attempt <= 3; attempt++) {
		try { await eda.sch_PrimitiveComponent.delete([pid]); }
		catch (e) { log(`delete attempt ${attempt} threw: ${e}`); }
		if (!(await stillThere())) {
			if (attempt > 1) log(`probe ${pid} deleted on attempt ${attempt}`);
			return;
		}
		log(`delete reported success but probe ${pid} survived (attempt ${attempt})`);
	}
	log(`probe ${pid} LEAKED after 3 attempts — clear it with \`sch prim-delete --ids ${pid}\``);
}

// Value to PASS so the flag's STORED rotation equals `desired` (what the linter reads).
async function appliedRotation(desired: number): Promise<number> {
	return (await detectRotationNegation()) ? (((360 - desired) % 360) + 360) % 360 : desired;
}

// Per-op timeout for connect_pin's platform mutations. The EasyEDA API
// intermittently DROPS a create request without ever resolving OR rejecting
// (same failure mode as schematicExportImage's SCH_EXPORT_TIMEOUT_MS — "platform
// drops the request without rejecting", leaving a stuck progress toast). With no
// per-call timeout the handler's `await` hangs forever; the daemon then kills the
// REQUEST at its ~18s dispatch budget with "connector did not respond", so a
// batch (block-apply autoconnect) freezes ~18s on that one pin → the user sees
// the layout progress stall at ~99% (observed ≈<1/400 connect_pin calls). Racing
// each mutation against this timeout converts the rare indefinite hang into a
// fast, clean rejection that flows into the existing wire-retry / rollback path,
// so the batch keeps moving and no half-built stub lands late. Kept well under
// the daemon's dispatch budget so the caller gets a structured error, not a raw
// dispatch timeout: worst realistic case (one wire retry + flag) ≈ 3×7s < 18s.
const CONNECT_PIN_OP_TIMEOUT_MS = 7000;

const schematicPowerConnectPin: Handler = async (payload) => {
	const pinX = requireNumber(payload, 'pinX');
	const pinY = requireNumber(payload, 'pinY');
	const kind = requireString(payload, 'kind');
	const net = requireString(payload, 'net');
	const offset = optionalNumber(payload, 'offset') ?? 30;
	const direction = (optionalString(payload, 'direction') ?? defaultDirection(kind)) as Direction;
	// Orientation follows the stub direction; an explicit rotation overrides it.
	// `rotation` is the desired STORED rotation (what the linter/calibrate read back);
	// `applied` is what we actually pass, compensated for this connector's stored-
	// rotation negation (see detectRotationNegation). Verify rendered orientation if
	// in doubt — the negation is connector-build-dependent.
	const rotation = optionalNumber(payload, 'rotation') ?? rotationFor(kind, direction);
	// Native labels accept only (x, y, net). Do not make label creation depend on
	// an unrelated power-flag calibration, which can itself hang on the host (#191).
	const applied = kind === NET_LABEL_KIND ? rotation : await appliedRotation(rotation);

	// `--direction` is the VISUAL outward direction. The schematic canvas is
	// y-UP (+y renders upward), so 'up' increases y and 'down' decreases it.
	// Flag-body rotation remains independent: rotationFor() is calibrated in
	// visual directions and appliedRotation() handles build-specific negation.
	const endpoint = connectPinEndpoint(pinX, pinY, offset, direction);
	const endX = endpoint.x;
	const endY = endpoint.y;

	// Snap the stub endpoint (and thus the flag) to the schematic connection grid
	// (SCH_GRID=5). EasyEDA snaps a created netflag/netport's connection pin to that
	// grid, so an OFF-grid endpoint (pin + a non-grid offset like 18 → 338) leaves the
	// flag's pin a grid-step from the stub end: the stub connects the pin to an empty
	// point, the flag floats unconnected, its net NAME never applies, and same-named
	// flags NEVER merge — every pin becomes its own auto-named 1-pin net ($1N1, …).
	// Snapping to the SAME grid makes the stub end and the snapped flag pin coincide.
	// Snapping the perpendicular axis too is safe because a pin sits ON the grid (this
	// is why 5 not 10: ESP32 pins at y=-385 stay put under a 5-snap, but a 10-snap
	// would jog endY to -380 → a diagonal stub EasyEDA refuses to create).
	// Snap the pin-side vertex to the exact grid too (issue #143). The pin coord
	// carries rotation FP residue (e.g. 649.9999999); the stub end is on the grid
	// (650), so a raw (649.9999999 → 650) vertex makes the stub a 0.0001 diagonal
	// — EasyEDA refuses it or the flag floats. Snapping the pin vertex to the same
	// grid keeps the stub perfectly axis-aligned while staying < GRID_EPS from the
	// real pin, so EasyEDA still treats it as connected to the pin.
	const pinGX = nearestGrid(pinX);
	const pinGY = nearestGrid(pinY);

	if (endX === pinGX && endY === pinGY) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`offset must be non-zero (got ${offset}); pin and netflag would overlap.`,
		);
	}

	// An OFF-grid pin cannot get a valid stub at all: the snapped endpoint jogs
	// the perpendicular axis, turning the stub diagonal (EasyEDA refuses to
	// create it) — and un-snapping would leave the flag floating instead. Fail
	// with the actionable cause (probe round #3: autolayout's fractional zone
	// centers put every pin off-grid → 53/64 cryptic stub failures). The test
	// tolerates FP residue (GRID_EPS): a pin within 0.01 of a grid point is
	// on-grid (rotation noise), only a genuinely off-grid pin (≥ half-grid) fails.
	const offGridX = Math.abs(pinX - pinGX) > GRID_EPS;
	const offGridY = Math.abs(pinY - pinGY) > GRID_EPS;
	if ((direction === 'left' || direction === 'right') ? offGridY : offGridX) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Pin (${pinX}, ${pinY}) sits OFF the ${SCH_GRID}-unit schematic grid on the stub's cross axis — the snapped endpoint would make the stub diagonal (EasyEDA refuses it) or leave the flag floating. Re-place the part so its anchor lands on the ${SCH_GRID}-grid (sch autolayout does this automatically), then reconnect.`,
		);
	}

	// Stub wire pin → endpoint. Creation is observed to fail transiently (issue
	// #137: "Failed to create pin-stub wire" on the first call, identical retry
	// succeeds — e.g. right after a batch of deletes, or when a stray primitive
	// occupies the endpoint) — so retry ONCE after a short settle before failing,
	// and include the exact endpoint in the terminal error so the caller can
	// inspect what occupies it.
	let wire;
	let wireErr: unknown;
	for (let attempt = 0; attempt < 2 && !wire; attempt++) {
		if (attempt > 0) await new Promise((r) => setTimeout(r, 250));
		try {
			wire = await withTimeout(
				eda.sch_PrimitiveWire.create([pinGX, pinGY, endX, endY]),
				CONNECT_PIN_OP_TIMEOUT_MS,
				`Stub-wire create did not settle within ${CONNECT_PIN_OP_TIMEOUT_MS}ms — the platform dropped the request without rejecting (the stuck-at-99% hang). Failing this attempt so the retry/caller can re-issue instead of hanging until the dispatch timeout.`,
			);
			wireErr = undefined;
		}
		catch (err) {
			wireErr = err;
		}
	}
	if (wireErr) {
		throw edaError(wireErr, `Failed to create pin-stub wire (${pinGX},${pinGY})→(${endX},${endY}) after retry — check for a primitive already occupying the endpoint.`);
	}
	if (!wire) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Wire creation returned no primitive for (${pinGX},${pinGY})→(${endX},${endY}).`);
	}

	// Netflag/netport at the far end (NOT at the pin — that would be the bug we are
	// preventing). If the flag fails AFTER the wire was created, ROLL BACK the wire
	// (issue #137): a half-built stub (wire without its flag) is an orphan-stub the
	// caller has no id for, and the next retry plans around the debris.
	const rollbackWire = async () => {
		try { await deleteSchGroup('wires', [wire.getState_PrimitiveId()]); }
		catch { /* best-effort — bridge-check's orphan-stub rule is the backstop */ }
	};
	let flag;
	try {
		if (kind in NET_FLAG_KINDS) {
			// Promise.resolve() collapses the API's overloaded union-of-promises
			// (Promise<A>|Promise<B>) into a single Promise<A|B> that withTimeout<T> accepts.
			flag = await withTimeout(
				Promise.resolve(eda.sch_PrimitiveComponent.createNetFlag(NET_FLAG_KINDS[kind], net, endX, endY, applied)),
				CONNECT_PIN_OP_TIMEOUT_MS,
				`Netflag create did not settle within ${CONNECT_PIN_OP_TIMEOUT_MS}ms — the platform dropped the request without rejecting (the stuck-at-99% hang). Rolling back the stub wire and failing fast so the caller can retry this pin.`,
			);
		}
		else if (kind in NET_PORT_KINDS) {
			flag = await withTimeout(
				Promise.resolve(eda.sch_PrimitiveComponent.createNetPort(NET_PORT_KINDS[kind], net, endX, endY, applied)),
				CONNECT_PIN_OP_TIMEOUT_MS,
				`Netport create did not settle within ${CONNECT_PIN_OP_TIMEOUT_MS}ms — the platform dropped the request without rejecting (the stuck-at-99% hang). Rolling back the stub wire and failing fast so the caller can retry this pin.`,
			);
		}
		else if (kind === NET_LABEL_KIND) {
			flag = await withTimeout(
				Promise.resolve(eda.sch_PrimitiveAttribute.createNetLabel(endX, endY, net)),
				CONNECT_PIN_OP_TIMEOUT_MS,
				`Netlabel create did not settle within ${CONNECT_PIN_OP_TIMEOUT_MS}ms — rolling back the stub wire.`,
			);
		}
		else {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown kind "${kind}"; expected one of: ${[...Object.keys(NET_FLAG_KINDS), 'ground', ...Object.keys(NET_PORT_KINDS)].join(', ')}.`,
			);
		}
	}
	catch (err) {
		await rollbackWire();
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create netflag/netport at wire end (stub wire rolled back).');
	}
	if (!flag) {
		await rollbackWire();
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to create ${kind} (stub wire rolled back).`);
	}

	return {
		result: {
			wirePrimitiveId: wire.getState_PrimitiveId(),
			flagPrimitiveId: flag.getState_PrimitiveId(),
			endPoint: { x: endX, y: endY },
			direction,
			offset,
			rotation,
			appliedRotation: applied,
		},
	};
};

// ─── schematic.pin.disconnect ────────────────────────────────────────
// Symmetric inverse of schematic.power.connect_pin. connect_pin builds a
// "pin → stub wire → netflag/netport" triplet; deleting only the flag (via
// schematic.primitives.delete) leaves the stub wire dangling with an EasyEDA
// auto-named single-pin net ($3N…). This action locates that stub — the wire
// whose one endpoint sits ON the target pin — plus any netflag/netport/netlabel
// on the wire's OTHER endpoint, and deletes wire + flag together (issue #51).
//
// Target the pin by either `designator`+`pin`, or a known `flagPrimitiveId` /
// `wirePrimitiveId` (whatever connect_pin returned). At least one locator required.
export const schematicPinDisconnect: Handler = async (payload) => {
	const designator = optionalString(payload, 'designator');
	const pinNumber = optionalString(payload, 'pin');
	const flagPrimitiveId = optionalString(payload, 'flagPrimitiveId');
	const wirePrimitiveId = optionalString(payload, 'wirePrimitiveId');
	// Coordinate locator: `sch autoconnect --replace` (issue #50) already resolved
	// the pin's (x,y) from the scene, so it can target the stub directly without a
	// designator round-trip.
	const payloadPinX = optionalNumber(payload, 'pinX');
	const payloadPinY = optionalNumber(payload, 'pinY');
	const hasCoord = payloadPinX !== undefined && payloadPinY !== undefined;
	if (!flagPrimitiveId && !wirePrimitiveId && !hasCoord && !(designator && pinNumber)) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Provide "designator"+"pin", "pinX"+"pinY", or a "flagPrimitiveId"/"wirePrimitiveId" to disconnect.',
		);
	}

	let components;
	let wires;
	try {
		components = await eda.sch_PrimitiveComponent.getAll();
		wires = await eda.sch_PrimitiveWire.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read schematic primitives.');
	}
	if (!Array.isArray(components) || !Array.isArray(wires)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'disconnect: component/wire inventory unavailable.');
	let observed: CheckWireSegment[];
	try { observed = collectWireSegments(wires); }
	catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `disconnect: unknown observed wire geometry; no mutation: ${describeThrown(err)}`); }

	// Resolve the target pin coordinate. Prefer an explicit pinX/pinY, then
	// designator+pin; else derive it from the located stub's pin-side endpoint
	// further below.
	let pinX: number | undefined = payloadPinX;
	let pinY: number | undefined = payloadPinY;
	if (!hasCoord && designator && pinNumber) {
		for (const c of components ?? []) {
			if ((c.getState_Designator?.() ?? '') !== designator) continue;
			let pins;
			try { pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(c.getState_PrimitiveId()); }
			catch { continue; }
			for (const p of pins ?? []) {
				if (String(p.getState_PinNumber?.() ?? '') === pinNumber) {
					pinX = p.getState_X();
					pinY = p.getState_Y();
				}
			}
		}
		if (pinX === undefined || pinY === undefined) {
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Pin ${designator}:${pinNumber} not found on the current schematic.`,
			);
		}
	}

	// Match the physical contact classifier; nearby but separate wires are not stubs.
	const TOL = WIRE_CONTACT_EPS;
	// Refuse rich target topology before deriving any ambiguous "opposite end".
	const targetAnchors: Array<{x:number;y:number}> = [];
	if (pinX !== undefined && pinY !== undefined) targetAnchors.push({x:pinX,y:pinY});
	if (flagPrimitiveId) {
		const flag = components.find(c=>c.getState_PrimitiveId()===flagPrimitiveId);
		if (flag) targetAnchors.push({x:flag.getState_X(),y:flag.getState_Y()});
	}
	if (targetAnchors.some(p=>!Number.isFinite(p.x)||!Number.isFinite(p.y))) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,'disconnect: target coordinates unavailable.');
	const candidateIds = new Set(observed.filter(s=>s.wirePrimitiveId===wirePrimitiveId || targetAnchors.some(p=>wirePointOnSegment(p,s.seg))).map(s=>s.wirePrimitiveId));
	try { assertLegacySimpleWireOperation(observed,candidateIds); }
	catch(err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,`disconnect: refused before mutation: ${describeThrown(err)}`); }
	const endpointsOf = (w: { getState_PrimitiveId: () => string }): Array<[number, number]> => {
		const own=observed.filter(s=>s.wirePrimitiveId===w.getState_PrimitiveId());
		if(own.length!==1) return [];
		const s=own[0].seg;
		return [[s[0],s[1]],[s[2],s[3]]];
	};

	// Locate the stub wire(s). A pin can host SEVERAL stubs (one per flag, or a
	// stub merged into a shared/collinear tree) — taking only the first one left
	// the rest behind while the action still reported disconnected:true (real
	// machine: R5:1 / R5:2 / C4:2 stayed wired). Collect every wire that touches
	// the target, delete them together, and verify below.
	const stubWires: Array<{ pid: string; ends: Array<[number, number]> }> = [];
	const addStub = (pid: string, ends: Array<[number, number]>): void => {
		if (!pid) return;
		if (!stubWires.some(s => s.pid === pid)) stubWires.push({ pid, ends });
	};
	if (wirePrimitiveId) {
		for (const w of wires ?? []) {
			if (String(w.getState_PrimitiveId?.() ?? '') === wirePrimitiveId) {
				addStub(wirePrimitiveId, endpointsOf(w));
			}
		}
	}
	// Derive pin coordinate from a flag if that's all we were given.
	if (stubWires.length === 0 && flagPrimitiveId && (pinX === undefined || pinY === undefined)) {
		for (const c of components ?? []) {
			if (String(c.getState_PrimitiveId?.() ?? '') === flagPrimitiveId) {
				// The wire endpoint that coincides with THIS flag is the free end;
				// the opposite endpoint is the pin. Collect EVERY wire touching the flag.
				const fx = c.getState_X();
				const fy = c.getState_Y();
				for (const w of wires ?? []) {
					const ends = endpointsOf(w);
					if (ends.length !== 2) continue;
					const [a, b] = ends;
					const aFlag = Math.hypot(a[0] - fx, a[1] - fy) <= TOL;
					const bFlag = Math.hypot(b[0] - fx, b[1] - fy) <= TOL;
					if (aFlag || bFlag) {
						const pinEnd = aFlag ? b : a;
						if (pinX === undefined || pinY === undefined) {
							pinX = pinEnd[0];
							pinY = pinEnd[1];
						}
						addStub(String(w.getState_PrimitiveId?.() ?? ''), ends);
					}
				}
			}
		}
	}
	// With a pin coordinate but no wire yet, collect EVERY wire with an endpoint
	// on the pin — a pin with multiple stubs needs all of them removed (no break).
	if (stubWires.length === 0 && pinX !== undefined && pinY !== undefined) {
		for (const w of wires ?? []) {
			const ends = endpointsOf(w);
			if (ends.length !== 2) continue;
			const onPin = ends.some(e => Math.hypot(e[0] - pinX!, e[1] - pinY!) <= TOL);
			if (onPin) {
				addStub(String(w.getState_PrimitiveId?.() ?? ''), ends);
			}
		}
	}

	if (stubWires.length === 0) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			'No stub wire found on the target pin — nothing to disconnect (already clean?).',
		);
	}

	// A supported straight stub may still serve a mid-span marker or another pin.
	// Sweep only the decoded segments and report every affected pin. Multi-record
	// primitives were rejected above, before any destructive operation.
	const stubPids = new Set(stubWires.map(s => s.pid));
	try { assertLegacySimpleWireOperation(observed,stubPids); }
	catch(err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,`disconnect: refused before mutation: ${describeThrown(err)}`); }
	const wireSegsAll = observed.filter(s=>stubPids.has(s.wirePrimitiveId)).map(s=>s.seg);
	const onWire = (x: number, y: number): boolean => wireSegsAll.some(s => wirePointOnSegment({x,y},s));

	const NET_MARKER_TYPES = new Set(['netflag', 'netport', 'netlabel', 'short_symbol']);
	const flagIds: Array<string> = [];
	const alsoDisconnectedPins: Array<string> = [];
	for (const c of components ?? []) {
		let type: string;
		try { type = String(c.getState_ComponentType?.() ?? ''); }
		catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `disconnect: component identity unavailable: ${describeThrown(err)}`); }
		if (NET_MARKER_TYPES.has(type)) {
			let cx: number;
			let cy: number;
			try { cx = c.getState_X(); cy = c.getState_Y(); }
			catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `disconnect: marker coordinates unavailable: ${describeThrown(err)}`); }
			if(!Number.isFinite(cx)||!Number.isFinite(cy)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,'disconnect: marker coordinates unavailable.');
			if (onWire(cx, cy)) flagIds.push(String(c.getState_PrimitiveId?.() ?? ''));
			continue;
		}
		// Component pins riding the same wire — the deletion disconnects them too.
		let compPins;
		try { compPins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(c.getState_PrimitiveId()); }
		catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `disconnect: pin geometry unavailable: ${describeThrown(err)}`); }
		if(!Array.isArray(compPins)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,'disconnect: pin inventory unavailable.');
		const cDesig = String(c.getState_Designator?.() ?? '');
		for (const p of compPins ?? []) {
			let px: number;
			let py: number;
			try { px = p.getState_X(); py = p.getState_Y(); }
			catch (err) { throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `disconnect: pin coordinates unavailable: ${describeThrown(err)}`); }
			if(!Number.isFinite(px)||!Number.isFinite(py)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,'disconnect: pin coordinates unavailable.');
			if (pinX !== undefined && pinY !== undefined && Math.hypot(px - pinX, py - pinY) <= TOL) continue; // the target pin itself
			if (onWire(px, py)) alsoDisconnectedPins.push(`${cDesig}:${String(p.getState_PinNumber?.() ?? '')}`);
		}
	}

	// Delete wires + any flags together via the same routed delete used elsewhere.
	const wireIds = [...stubPids].filter(Boolean);
	const validFlags = [...new Set(flagIds.filter(Boolean))];
	try {
		if (wireIds.length) {
			await deleteSchGroup('wires', wireIds);
		}
		if (validFlags.length) {
			await deleteSchGroup('components', validFlags);
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to delete stub wire / flag.');
	}

	// A delete that returned true is NOT evidence — the platform silently
	// no-ops on wires merged into shared trees / collinear segments yet still
	// reports success (real machine: R5:1, R5:2, C4:2 stayed connected after a
	// "disconnected:true"). Re-read and report survivors as a structured partial
	// (partial-application convention: ok stays true, the canvas HAS changed for
	// whatever really got deleted; nothing is claimed applied without proof).
	const surviving = await survivingSchPrimitives({ wires: wireIds, components: validFlags });
	const survivedWireIds = surviving.wires ?? [];
	const survivedFlagIds = surviving.components ?? [];
	const survivedIds = [...survivedWireIds, ...survivedFlagIds];
	const notApplied = [
		...survivedWireIds.map(id => ({ kind: 'wire', id })),
		...survivedFlagIds.map(id => ({ kind: 'flag', id })),
	];
	const fullyApplied = survivedIds.length === 0;

	return {
		result: {
			// True only when every targeted primitive is PROVEN gone by re-read.
			disconnected: fullyApplied,
			...(fullyApplied ? {} : { partial: true }),
			pin: designator && pinNumber ? `${designator}:${pinNumber}` : undefined,
			at: pinX !== undefined && pinY !== undefined ? { x: pinX, y: pinY } : undefined,
			// Only ids verified gone — never the mere delete-call arguments.
			deletedWires: wireIds.filter(id => !survivedWireIds.includes(id)),
			deletedFlags: validFlags.filter(id => !survivedFlagIds.includes(id)),
			// Survivors of the delete call (platform silently kept them): the pin
			// may still be electrically connected. Empty on full success.
			notApplied,
			survivedIds,
			// Non-empty when the deleted wire was a merged tree serving other pins:
			// those pins are now floating and need reconnecting (issue #137).
			alsoDisconnectedPins: [...new Set(alsoDisconnectedPins)],
		},
		...(fullyApplied ? {} : {
			warnings: [
				`disconnect only partially applied: ${survivedIds.length} primitive(s) survived deletion `
				+ `(${survivedIds.join(', ')}) — the pin may still be connected; verify with the netlist `
				+ `and retry or delete the survivors explicitly.`,
			],
		}),
	};
};

// ─── Generic document open ───────────────────────────────────────────

/**
 * Open any document (schematic page or PCB) by UUID. A generalization of
 * schematic.page.open that works for all document types the editor supports.
 */
const documentOpen: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	// Reload callers capture the target tab's split BEFORE closing it. Prefer
	// that stable destination over inferring a split from the post-close active
	// tab, which can temporarily be the host's about:blank/loading tab.
	const requestedSplitScreenId = optionalString(payload, 'splitScreenId')?.trim() || undefined;
	// After closing a tab, the host's implicit destination can be stale even
	// though a remaining tab is active. Resolve its split through the official
	// API only when the caller did not preserve the target split. Older hosts
	// without this metadata keep the original one-argument open path.
	let splitScreenId = requestedSplitScreenId;
	if (splitScreenId === undefined) {
		try {
			const current = await eda.dmt_SelectControl.getCurrentDocumentInfo();
			if (typeof current?.tabId === 'string' && current.tabId.trim()) {
				const split = await eda.dmt_EditorControl.getSplitScreenIdByTabId(current.tabId);
				if (typeof split === 'string' && split.trim()) {
					splitScreenId = split;
				}
			}
		}
		catch { /* split metadata is optional; never guess a destination ID */ }
	}
	let tabId;
	try {
		tabId = splitScreenId === undefined
			? await eda.dmt_EditorControl.openDocument(uuid)
			: await eda.dmt_EditorControl.openDocument(uuid, splitScreenId);
	}
	catch (err) {
		throw edaError(err, 'Failed to open document.');
	}
	if (tabId === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to open document "${uuid}".`);
	}
	// A returned tab ID does not prove the target is active. Confirm identity
	// before declaring readiness; schematic pages additionally retain their
	// primitive-settle check (#67). For PCB this only confirms activation, not
	// that every primitive has loaded (the CLI's doc switch polls PCB data).
	let ready = false;
	try {
		const doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (doc?.uuid === uuid) {
			ready = documentTypeLabel(doc.documentType) === 'schematic'
				? await waitSchematicPageSettle()
				: true;
		}
	}
	catch {
		/* An unavailable identity probe cannot establish readiness. */
	}
	return { result: { tabId, ready } };
};

/**
 * Close one explicitly identified active document through the official editor
 * API. Requiring both identities prevents a stale reload plan from closing the
 * tab the user switched to after the CLI's earlier read. Capture the split
 * before close so document.open can restore the same editor pane.
 */
const documentClose: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	const tabId = requireString(payload, 'tabId');
	let current;
	try {
		current = await eda.dmt_SelectControl.getCurrentDocumentInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to verify the active document before close.');
	}
	if (!current || current.uuid !== uuid || current.tabId !== tabId) {
		throw new ActionError(ErrorCodes.INVALID_STATE,
			`Refusing to close document: expected active uuid/tabId ${uuid}/${tabId}, got ${current?.uuid ?? '<none>'}/${current?.tabId ?? '<none>'}.`);
	}

	let splitScreenId: string | null = null;
	try {
		const split = await eda.dmt_EditorControl.getSplitScreenIdByTabId(tabId);
		if (typeof split === 'string' && split.trim()) splitScreenId = split.trim();
	}
	catch { /* split metadata is optional; the explicit tab identity remains authoritative */ }

	let closed;
	try {
		closed = await eda.dmt_EditorControl.closeDocument(tabId);
	}
	catch (err) {
		throw edaError(err, `Failed to close document "${uuid}".`);
	}
	if (closed !== true) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`Closing document "${uuid}" returned no success.`);
	}
	return { result: { closed: true, uuid, tabId, splitScreenId } };
};

// ─── PCB (Phase 2 — read-only skeleton) ──────────────────────────────

/**
 * List all PCB documents in the current project. Returns uuid + name for each
 * PCB, which can be passed to document.open to switch to that board.
 */
const pcbDocumentsList: Handler = async () => {
	let pcbs;
	try {
		pcbs = await eda.dmt_Pcb.getAllPcbsInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB documents.');
	}
	if (!Array.isArray(pcbs)) {
		return { result: { pcbs: [], count: 0 } };
	}
	return {
		result: {
			pcbs: pcbs.map(p => ({
				uuid: p.uuid,
				name: p.name,
				parentProjectUuid: p.parentProjectUuid,
			})),
			count: pcbs.length,
		},
	};
};

/**
 * List placed components on the active PCB. Optionally filter by layer and
 * include each component's pads (the net-by-name connectivity surface).
 */
const pcbComponentsList: Handler = async (payload) => {
	const layer = payload.layer as TPCB_LayersOfComponent | undefined;
	const includePads = optionalBoolean(payload, 'includePads') === true;
	// includeBBox attaches each component's rendered extent {minX,minY,maxX,maxY}
	// so the agent can reason about size, spacing, and courtyard/overlap.
	const includeBBox = optionalBoolean(payload, 'includeBBox') === true;
	let components;
	try {
		components = await eda.pcb_PrimitiveComponent.getAll(layer);
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB components.');
	}

	const serialized: Array<Record<string, unknown>> = [];
	for (const component of components) {
		const record = serializePcbComponent(component);
		if (includeBBox) {
			try {
				const box = await eda.pcb_Primitive.getPrimitivesBBox([component.getState_PrimitiveId()]);
				if (box) record.bbox = box;
			}
			catch { /* bbox is optional */ }
		}
		if (includePads) {
			try {
				const pads = await eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId(
					component.getState_PrimitiveId(),
				);
				record.pads = (pads ?? []).map(serializePcbPad);
			}
			catch { /* pads are optional */ }
		}
		serialized.push(record);
	}

	return { result: { components: serialized, count: serialized.length } };
};

/**
 * List all layers of the active PCB, plus the current layer and copper count.
 * `IPCB_LayerItem` is a plain data object, so it serializes directly.
 */
// Well-known PCB layer ids + name aliases. TOP/BOTTOM copper = 1/2, silks = 3/4
// (mirrors PCB_TOP_SILK/PCB_BOTTOM_SILK below); inner-copper ids are higher and
// resolved by NAME from getAllLayers (e.g. "Inner1"). Used by set_current /
// visibility / view.side to accept id | name | top | bottom | inner1.
const PCB_LAYER_ALIASES: Record<string, number> = {
	top: 1, topcopper: 1, toplayer: 1,
	bottom: 2, bottomcopper: 2, bottomlayer: 2,
	topsilk: 3, topsilkscreen: 3,
	bottomsilk: 4, bottomsilkscreen: 4,
};

// Resolve a layer id|name|alias to its numeric layer id. Numbers/numeric strings
// pass through; known aliases map directly; otherwise match a layer's name from
// getAllLayers (case-insensitive, whitespace-insensitive) — that's how inner
// layers (Inner1…) resolve. Throws MISSING_PAYLOAD_FIELD if nothing matches.
function resolveLayerId(spec: unknown, layers: Array<IPCB_LayerItem>): number {
	if (typeof spec === 'number' && Number.isFinite(spec)) return spec;
	if (typeof spec === 'string') {
		const raw = spec.trim();
		if (/^\d+$/.test(raw)) return Number(raw);
		const key = raw.toLowerCase().replace(/[\s_-]+/g, '');
		if (key in PCB_LAYER_ALIASES) return PCB_LAYER_ALIASES[key];
		for (const l of layers) {
			if (l.name.toLowerCase().replace(/[\s_-]+/g, '') === key) {
				return l.id as unknown as number;
			}
		}
	}
	throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD,
		`could not resolve layer "${String(spec)}" — pass a numeric id, top|bottom, or a layer name from pcb.layers.list`);
}

const pcbLayersList: Handler = async () => {
	// Ensure the PCB tab is the foreground/active document before reading
	// getCurrentLayer — a null currentLayer in the issue (#40) traced to the PCB
	// not being the active tab, so the sync getCurrentLayer returned undefined.
	try {
		const cur = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (cur?.tabId) await eda.dmt_EditorControl.activateDocument(cur.tabId);
	}
	catch { /* best-effort activation */ }

	let layers;
	try {
		layers = await eda.pcb_Layer.getAllLayers();
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB layers.');
	}
	// getCurrentLayer is synchronous; copper count is best-effort.
	let currentLayer: unknown = null;
	try {
		currentLayer = eda.pcb_Layer.getCurrentLayer() ?? null;
	}
	catch { /* best-effort */ }
	let copperLayerCount: unknown = null;
	try {
		copperLayerCount = await eda.pcb_Layer.getTheNumberOfCopperLayers();
	}
	catch { /* best-effort */ }

	// Fallback display-state evidence (#40 acceptance #3): when getCurrentLayer is
	// empty (new board with no manual layer pick), surface the set of currently
	// SHOWN layers (layerStatus === SHOW) so the caller can still reason about
	// what's on screen.
	let visibleLayers: unknown = null;
	if (currentLayer == null && Array.isArray(layers)) {
		visibleLayers = layers
			.filter(l => l.layerStatus === 1 /* EPCB_LayerStatus.SHOW */)
			.map(l => ({ id: l.id, name: l.name }));
	}

	return { result: { layers, currentLayer, visibleLayers, copperLayerCount, count: layers.length } };
};

// pcb.layers.set_current — switch the active/edit layer (#40 acceptance #1/#4).
// Wraps eda.pcb_Layer.selectLayer; accepts id | name | top | bottom | inner1.
const pcbLayerSetCurrent: Handler = async (payload) => {
	const layers = await eda.pcb_Layer.getAllLayers();
	const id = resolveLayerId(payload.layer, layers);
	let ok: boolean;
	try {
		ok = await eda.pcb_Layer.selectLayer(id as unknown as TPCB_LayersInTheSelectable);
	}
	catch (err) {
		throw edaError(err, `Failed to select layer ${id}.`);
	}
	let currentLayer: unknown = null;
	try { currentLayer = eda.pcb_Layer.getCurrentLayer() ?? null; }
	catch { /* best-effort */ }
	await waitForCanvasSettle();
	return { result: { ok, requested: payload.layer ?? null, layer: id, currentLayer } };
};

// pcb.layers.visibility — show/hide/focus a layer set. `preset` gives one-shot
// focus views (top-only|bottom-only|copper-only|silk-only); or pass explicit
// show[]/hide[] layer specs. `exclusive` (default true for presets) hides every
// other layer so the snapshot shows only the requested set (#40 acceptance #2).
const VISIBILITY_PRESETS: Record<string, number[]> = {
	'top-only': [1, 3],
	'bottom-only': [2, 4],
	'copper-only': [1, 2],
	'silk-only': [3, 4],
};

const pcbLayerVisibility: Handler = async (payload) => {
	const layers = await eda.pcb_Layer.getAllLayers();
	const preset = optionalString(payload, 'preset');
	const shown: number[] = [];
	const hidden: number[] = [];

	if (preset) {
		const key = preset.trim().toLowerCase();
		const ids = VISIBILITY_PRESETS[key];
		if (!ids) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD,
				`unknown preset "${preset}" — use top-only|bottom-only|copper-only|silk-only, or show/hide`);
		}
		try {
			await eda.pcb_Layer.setLayerVisible(ids as unknown as TPCB_LayersInTheSelectable[], true);
		}
		catch (err) {
			throw edaError(err, `Failed to apply visibility preset "${preset}".`);
		}
		shown.push(...ids);
	}
	else {
		const show = Array.isArray(payload.show) ? payload.show : [];
		const hide = Array.isArray(payload.hide) ? payload.hide : [];
		if (show.length === 0 && hide.length === 0) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD,
				'nothing to do — pass preset, or show[] / hide[] layer specs');
		}
		const exclusive = optionalBoolean(payload, 'exclusive') === true;
		if (show.length > 0) {
			const ids = show.map(s => resolveLayerId(s, layers));
			try { await eda.pcb_Layer.setLayerVisible(ids as unknown as TPCB_LayersInTheSelectable[], exclusive); }
			catch (err) { throw edaError(err, 'Failed to show layers.'); }
			shown.push(...ids);
		}
		if (hide.length > 0) {
			const ids = hide.map(s => resolveLayerId(s, layers));
			try { await eda.pcb_Layer.setLayerInvisible(ids as unknown as TPCB_LayersInTheSelectable[], false); }
			catch (err) { throw edaError(err, 'Failed to hide layers.'); }
			hidden.push(...ids);
		}
	}

	await waitForCanvasSettle();
	const after = await eda.pcb_Layer.getAllLayers();
	return { result: { preset: preset ?? null, shown, hidden, layers: after } };
};

// pcb.view.side — one-shot switch to the top or bottom side for snapshots / QA.
// Selects that side's copper as the current layer AND focuses the side's layer
// set (copper + silk) so a subsequent pcb.snapshot shows that side (#40 #1/#2).
// NOTE: EasyEDA Pro exposes no native canvas flip/mirror-view API, so this is a
// layer-focus approximation, not a physical board flip.
const pcbViewSide: Handler = async (payload) => {
	const side = (optionalString(payload, 'side') ?? '').trim().toLowerCase();
	if (side !== 'top' && side !== 'bottom') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'side must be "top" or "bottom"');
	}
	const copperId = side === 'top' ? 1 : 2;
	const focusIds = side === 'top' ? [1, 3] : [2, 4];
	// Ensure the PCB tab is active so selectLayer/visibility land on it.
	try {
		const cur = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (cur?.tabId) await eda.dmt_EditorControl.activateDocument(cur.tabId);
	}
	catch { /* best-effort */ }
	try {
		await eda.pcb_Layer.selectLayer(copperId as unknown as TPCB_LayersInTheSelectable);
	}
	catch (err) {
		throw edaError(err, `Failed to select ${side} copper layer.`);
	}
	try {
		await eda.pcb_Layer.setLayerVisible(focusIds as unknown as TPCB_LayersInTheSelectable[], true);
	}
	catch (err) {
		throw edaError(err, `Failed to focus ${side}-side layers.`);
	}
	await waitForCanvasSettle();
	let currentLayer: unknown = null;
	try { currentLayer = eda.pcb_Layer.getCurrentLayer() ?? null; }
	catch { /* best-effort */ }
	return {
		result: {
			side, currentLayer, focusedLayers: focusIds,
			note: 'Layer-focus approximation (no native canvas flip API). Take pcb.snapshot next; thread its sha256 back as previousSha256 to defeat a stale frame.',
		},
	};
};

// pcb.stackup.set — configure the board stackup: set the copper layer count
// (2/4/6/…, eda.pcb_Layer.setTheNumberOfCopperLayers) and/or set inner layers'
// type (SIGNAL vs PLANE/内电层, via modifyLayer). PLANE inner layers are the clean
// way to distribute GND + power on 4+ layer boards (each net gets a dedicated
// plane instead of fighting over one layer — see the ceshi 2-layer pour conflict).
const STACKUP_LAYER_TYPE: Record<string, string> = {
	signal: 'SIGNAL',
	plane: 'PLANE', 'internal-electrical': 'PLANE', 'internal': 'PLANE',
	power: 'PLANE', ground: 'PLANE', gnd: 'PLANE',
};

export const pcbStackupSet: Handler = async (payload) => {
	const count = optionalNumber(payload, 'count');
	const beforeLayers = await eda.pcb_Layer.getAllLayers();
	const beforeCount = await eda.pcb_Layer.getTheNumberOfCopperLayers();
	let setCount: boolean | null = null;
	if (count != null) {
		const allowed = [2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 32];
		if (!allowed.includes(count)) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `count must be an even number 2..32, got ${count}`);
		}
		try {
			if (beforeCount !== count) {
				setCount = await eda.pcb_Layer.setTheNumberOfCopperLayers(count as 2 | 4 | 6 | 8 | 10 | 12 | 14 | 16);
			}
		}
		catch (err) {
			throw edaError(err, `Failed to set copper layer count to ${count}.`);
		}
	}

	// Optional per-inner-layer type/name changes. Each entry: {id|layer, type?, name?}.
	const modified: Array<Record<string, unknown>> = [];
	const layers = payload.layers;
	if (Array.isArray(layers)) {
		for (const spec of layers) {
			if (!spec || typeof spec !== 'object') {
				continue;
			}
			const s = spec as Record<string, unknown>;
			const id = (s.id ?? s.layer) as number;
			if (typeof id !== 'number') {
				throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each layers[] entry needs a numeric id/layer');
			}
			const prop: { type?: string; name?: string } = {};
			if (typeof s.type === 'string') {
				const mapped = STACKUP_LAYER_TYPE[s.type.trim().toLowerCase()];
				if (!mapped) {
					throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `layer type must be signal|plane, got "${String(s.type)}"`);
				}
				prop.type = mapped;
			}
			if (typeof s.name === 'string') {
				prop.name = s.name;
			}
			const before = beforeLayers.find(layer => Number(layer.id) === id);
			const alreadyMatches = (!prop.type || before?.type === prop.type) && (!prop.name || before?.name === prop.name);
			let written: boolean | null = null;
			try {
				if (!alreadyMatches) {
					written = await eda.pcb_Layer.modifyLayer(id as unknown as TPCB_LayersInTheSelectable, prop as { type?: TPCB_LayerTypesOfInnerLayer; name?: string });
				}
			}
			catch (err) {
				throw edaError(err, `Failed to modify layer ${id} (only inner layers accept a type change).`);
			}
			modified.push({ layer: id, before: before ?? null, written, ...prop });
		}
	}

	const allLayers = await eda.pcb_Layer.getAllLayers();
	const copperLayerCount = await eda.pcb_Layer.getTheNumberOfCopperLayers();
	const countVerified = count == null || copperLayerCount === count;
	for (const item of modified) {
		const actual = allLayers.find(layer => Number(layer.id) === item.layer) ?? null;
		item.actual = actual;
		item.verified = !!actual && (item.type == null || actual.type === item.type) && (item.name == null || actual.name === item.name);
		item.changed = !!actual && !!item.before && (actual.type !== (item.before as any).type || actual.name !== (item.before as any).name);
	}
	const layersVerified = modified.every(item => item.verified === true);
	const verified = countVerified && layersVerified;
	const changed = copperLayerCount !== beforeCount || modified.some(item => item.changed === true);
	const partial = changed && !verified;
	return {
		result: {
			before: { copperLayerCount: beforeCount, layers: beforeLayers },
			requested: { ...(count == null ? {} : { copperLayerCount: count }), ...(Array.isArray(layers) ? { layers } : {}) },
			copperLayerCount, setCount, modified, layers: allLayers,
			countVerified, layersVerified, changed, verified, partial,
		},
		...(verified ? {} : { warnings: ['PCB stackup was not fully applied; inspect each layer written/actual field before continuing.'] }),
	};
};

// pcb.silk.align — reposition every component's DESIGNATOR silkscreen to a clean,
// consistent spot (centered above/below the footprint bbox, --offset mil away). The
// designator is a component-bound attribute (not a free string) — reachable via
// pcb_PrimitiveAttribute.getAllPrimitiveId(componentId) + .modify(id,{x,y}); no
// per-designator-position setter exists on the component itself. Verified live: R2's
// designator moved exactly to the requested (x,y).
type silkRect = { minX: number; minY: number; maxX: number; maxY: number };
type silkItem = {
	cid: string; desig: string; cb: silkRect; attrId: string;
	w: number; h: number; offx: number; offy: number;
};

function silkOverlap(a: silkRect, b: silkRect, m: number): boolean {
	return a.minX < b.maxX + m && a.maxX > b.minX - m && a.minY < b.maxY + m && a.maxY > b.minY - m;
}

// ── silk-align geometry helpers (module scope) ──
type silkObs = { rect: silkRect; kind: string; owner: string; m: number };
const silkCenter = (r: silkRect) => ({ x: (r.minX + r.maxX) / 2, y: (r.minY + r.maxY) / 2 });
const silkInflate = (r: silkRect, m: number): silkRect => ({ minX: r.minX - m, minY: r.minY - m, maxX: r.maxX + m, maxY: r.maxY + m });
const silkUnion = (a: silkRect, b: silkRect): silkRect => ({ minX: Math.min(a.minX, b.minX), minY: Math.min(a.minY, b.minY), maxX: Math.max(a.maxX, b.maxX), maxY: Math.max(a.maxY, b.maxY) });
const silkInside = (inner: silkRect, outer: silkRect): boolean => inner.minX >= outer.minX && inner.minY >= outer.minY && inner.maxX <= outer.maxX && inner.maxY <= outer.maxY;
// min rect-to-rect gap (0 if overlapping/touching).
function silkGap(a: silkRect, b: silkRect): number {
	const dx = Math.max(0, Math.max(a.minX - b.maxX, b.minX - a.maxX));
	const dy = Math.max(0, Math.max(a.minY - b.maxY, b.minY - a.maxY));
	return Math.hypot(dx, dy);
}
// boardOutlineIds collects every BOARD_OUTLINE-layer(11) primitive id — lines, arcs,
// AND polylines (a rounded/closed outline is often a single pcb_PrimitivePolyline).
async function boardOutlineIds(): Promise<string[]> {
	const ids: string[] = [];
	for (const l of (await eda.pcb_PrimitiveLine.getAll()) ?? []) if (Number(l.getState_Layer()) === 11) ids.push(l.getState_PrimitiveId());
	for (const a of (await eda.pcb_PrimitiveArc.getAll()) ?? []) if (Number(a.getState_Layer()) === 11) ids.push(a.getState_PrimitiveId());
	try { for (const p of (await eda.pcb_PrimitivePolyline.getAll()) ?? []) if (Number(p.getState_Layer()) === 11) ids.push(p.getState_PrimitiveId()); } catch { /* polyline API optional */ }
	return ids;
}

// clearance marching from body `cb` along unit dir to the first HARD obstacle in a
// perpendicular corridor (excludes own body/pads); capped at MAX_SCAN.
function silkCorridor(cb: silkRect, dx: number, dy: number, obs: silkObs[], self: string, perp: number, MAX_SCAN: number): number {
	const c = silkCenter(cb);
	let best = MAX_SCAN;
	for (const o of obs) {
		if (o.owner === self) continue;
		if (o.kind !== 'BODY' && o.kind !== 'PAD') continue;
		if (dx !== 0) {
			if (o.rect.maxY < c.y - perp / 2 || o.rect.minY > c.y + perp / 2) continue;
			const gap = dx > 0 ? o.rect.minX - cb.maxX : cb.minX - o.rect.maxX;
			if (gap >= 0 && gap < best) best = gap;
		}
		else {
			if (o.rect.maxX < c.x - perp / 2 || o.rect.minX > c.x + perp / 2) continue;
			const gap = dy > 0 ? o.rect.minY - cb.maxY : cb.minY - o.rect.maxY;
			if (gap >= 0 && gap < best) best = gap;
		}
	}
	return best;
}

const pcbSilkAlign: Handler = async (payload) => {
	// Position-aware auto-placement of component designators: for each part pick the
	// best of up/down/left/right by LOCAL FREE SPACE + board position + crowd axis,
	// avoiding other parts' PADS (the #1 fix — a label over exposed copper is clipped),
	// bodies, keep-out regions, the board edge, and other labels. Rotation stays 0
	// (upright, keeps `pcb check` clean); bottom parts go to bottom silk + mirror.
	const side = (optionalString(payload, 'side') ?? '').toLowerCase();
	const refs = Array.isArray(payload.refs) ? (payload.refs as unknown[]).map(String) : null;
	// spacing coefficient scales the drift distance so labels sit further from the
	// footprint (assembly / hand-solder room). Cassembly is the HARD minimum gap the
	// label keeps from its OWN pads (the body is inflated by it) so a designator never
	// crowds the copper you solder to; other-pad margin Cpad is larger still.
	const spacing = optionalNumber(payload, 'spacing') ?? 1.5;
	const baseOffset = (optionalNumber(payload, 'offset') ?? 15) * spacing;

	const Cpad = 12, Cedge = 15, Cregion = 6, Clabel = 6, Cbody = 6, HALO = 2, Cassembly = 10;
	const STEP = 22, R_MAX = 6, MAX_SCAN = 200, GAP_CAP = 120;

	let comps;
	try { comps = await eda.pcb_PrimitiveComponent.getAll(); }
	catch (err) { throw edaError(err, 'Failed to list components for silk-align.'); }
	comps = comps ?? [];

	const bbox1 = async (id: string): Promise<silkRect | null> => {
		try { return (await eda.pcb_Primitive.getPrimitivesBBox([id])) as silkRect; } catch { return null; }
	};

	// board-outline safeArea (containment box, shrunk by Cedge).
	let safeArea: silkRect | null = null;
	{
		const olIds = await boardOutlineIds();
		if (olIds.length) {
			try { const b = (await eda.pcb_Primitive.getPrimitivesBBox(olIds)) as silkRect; if (b) safeArea = silkInflate(b, -Cedge); } catch { /* no outline */ }
		}
	}
	const boardCenter = safeArea ? silkCenter(safeArea) : null;

	// ── one-time obstacle build: pads (by owner) + bodies (pad-union) + regions + frozen silk ──
	const OBS: silkObs[] = [];
	const BODY: Record<string, silkRect> = {};
	for (const c of comps) {
		const cid = c.getState_PrimitiveId();
		let pads: Array<{ getState_PrimitiveId(): string; getState_X?(): number; getState_Y?(): number }> = [];
		try { pads = (await eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId(cid)) ?? []; } catch { pads = []; }
		let body: silkRect | null = null;
		for (const p of pads) {
			let pr = await bbox1(p.getState_PrimitiveId());
			if (!pr) { const x = p.getState_X?.() ?? 0, y = p.getState_Y?.() ?? 0; pr = { minX: x - 15, minY: y - 15, maxX: x + 15, maxY: y + 15 }; }
			OBS.push({ rect: silkInflate(pr, Cpad), kind: 'PAD', owner: cid, m: 0 });
			body = body ? silkUnion(body, pr) : pr;
		}
		if (!body) body = await bbox1(cid);
		if (body) { BODY[cid] = body; OBS.push({ rect: body, kind: 'BODY', owner: cid, m: Cbody }); }
	}
	for (const r of (await eda.pcb_PrimitiveRegion.getAll()) ?? []) {
		const rb = await bbox1(r.getState_PrimitiveId());
		if (!rb) continue;
		const rules = (r.getState_RuleType?.() ?? []) as unknown as number[];
		OBS.push({ rect: rb, kind: rules.includes(2) ? 'REGION_H' : 'REGION_S', owner: '', m: Cregion });
	}
	for (const s of (await eda.pcb_PrimitiveString.getAll()) ?? []) {
		const ly = Number(s.getState_Layer?.());
		if (ly !== 3 && ly !== 4) continue;
		const sb = await bbox1(s.getState_PrimitiveId());
		if (sb) OBS.push({ rect: sb, kind: 'FROZEN', owner: '', m: Clabel });
	}

	// ── build items (in-scope designators) + seed placed-label boxes; freeze the rest ──
	type Item = { c: typeof comps[number]; cid: string; desig: string; attrId: string; cb: silkRect; w: number; h: number; offx: number; offy: number; layer: number; curLayer: number; curMirror: boolean };
	const items: Item[] = [];
	const skipped: Array<Record<string, unknown>> = [];
	const LAB: Record<string, silkRect> = {};
	for (const c of comps) {
		const cid = c.getState_PrimitiveId();
		const desig = c.getState_Designator?.() ?? '';
		if (!desig) continue;
		const cb = BODY[cid];
		if (!cb) { skipped.push({ designator: desig, reason: 'no component body' }); continue; }
		let attrId: string | null = null;
		try {
			const ids = await eda.pcb_PrimitiveAttribute.getAllPrimitiveId(cid);
			for (const id of ids ?? []) {
				const a = await eda.pcb_PrimitiveAttribute.get(id);
				if (a && (String(a.getState_Key?.() ?? '').toLowerCase().includes('desig') || a.getState_Value?.() === desig)) { attrId = id; break; }
			}
		} catch { /* skip below */ }
		if (!attrId) { if (!refs || refs.includes(desig)) skipped.push({ designator: desig, reason: 'no designator attribute found' }); continue; }
		const a = await eda.pcb_PrimitiveAttribute.get(attrId);
		const db = await bbox1(attrId);
		if (!a || !db) { skipped.push({ designator: desig, reason: 'designator attribute not readable' }); continue; }
		// out-of-scope designators are frozen obstacles (still block in-scope placement).
		if (refs && !refs.includes(desig)) { OBS.push({ rect: db, kind: 'FROZEN', owner: '', m: Clabel }); continue; }
		const ax = a.getState_X() ?? 0, ay = a.getState_Y() ?? 0;
		const bc = silkCenter(db);
		items.push({
			c, cid, desig, attrId, cb, w: db.maxX - db.minX, h: db.maxY - db.minY,
			offx: bc.x - ax, offy: bc.y - ay, layer: Number(c.getState_Layer?.() ?? 1),
			curLayer: Number(a.getState_Layer?.() ?? 3), curMirror: !!a.getState_Mirror?.(),
		});
		LAB[attrId] = db;
	}

	// ── most-constrained-first order (MRV): fewest free sides / closest to edge first ──
	const N = [0, 1], S = [0, -1], E = [1, 0], W = [-1, 0];
	const diags = [[1, 1], [-1, 1], [1, -1], [-1, -1]];
	const prefBase: Record<string, number> = { '0,1': 1.0, '0,-1': 0.85, '1,0': 0.6, '-1,0': 0.6 };
	const sideDir = side === 'bottom' ? S : side === 'left' ? W : side === 'right' ? E : side === 'top' ? N : null;
	for (const it of items) {
		let free = 0;
		for (const [dx, dy] of [N, S, E, W]) {
			const perp = dx !== 0 ? it.h + 2 * Cbody : it.w + 2 * Cbody;
			if (silkCorridor(it.cb, dx, dy, OBS, it.cid, perp, MAX_SCAN) >= it.h + baseOffset) free++;
		}
		(it as unknown as { free: number }).free = free;
	}
	const edgeProx = (it: Item) => safeArea ? Math.min(
		silkCenter(it.cb).x - safeArea.minX, safeArea.maxX - silkCenter(it.cb).x,
		silkCenter(it.cb).y - safeArea.minY, safeArea.maxY - silkCenter(it.cb).y) : 1e9;
	items.sort((p, q) => {
		const fp = (p as unknown as { free: number }).free, fq = (q as unknown as { free: number }).free;
		if (fp !== fq) return fp - fq;
		const ep = edgeProx(p), eq = edgeProx(q);
		if (Math.abs(ep - eq) > 1) return ep - eq;
		const ap = (p.cb.maxX - p.cb.minX) * (p.cb.maxY - p.cb.minY), aq = (q.cb.maxX - q.cb.minX) * (q.cb.maxY - q.cb.minY);
		if (ap !== aq) return aq - ap;
		return p.desig < q.desig ? -1 : 1;
	});

	// per-item: rank the 4 sides, then place via the ladder.
	const rankSides = (it: Item): number[][] => {
		const cc = silkCenter(it.cb);
		// crowded axis = bearing to nearest OTHER body.
		let near: silkRect | null = null, nd = Infinity;
		for (const o of OBS) {
			if (o.kind !== 'BODY' || o.owner === it.cid) continue;
			const oc = silkCenter(o.rect); const d = Math.hypot(oc.x - cc.x, oc.y - cc.y);
			if (d < nd) { nd = d; near = o.rect; }
		}
		const crowdVertical = near ? Math.abs(silkCenter(near).y - cc.y) >= Math.abs(silkCenter(near).x - cc.x) : false;
		let u = 0.5, v = 0.5;
		if (safeArea) { u = (cc.x - safeArea.minX) / Math.max(1, safeArea.maxX - safeArea.minX); v = (cc.y - safeArea.minY) / Math.max(1, safeArea.maxY - safeArea.minY); }
		const edgeness = Math.max(0, Math.min(1, 2 * Math.max(Math.abs(u - 0.5), Math.abs(v - 0.5))));
		const toCenter = boardCenter ? { x: boardCenter.x - cc.x, y: boardCenter.y - cc.y } : { x: 0, y: 0 };
		const tcLen = Math.hypot(toCenter.x, toCenter.y) || 1;
		const scored = [N, S, E, W].map(([dx, dy]) => {
			const perp = dx !== 0 ? it.h + 2 * Cbody : it.w + 2 * Cbody;
			const clr = silkCorridor(it.cb, dx, dy, OBS, it.cid, perp, MAX_SCAN);
			const Pfree = Math.min(clr, GAP_CAP) / GAP_CAP;
			const Ppos = ((dx * toCenter.x + dy * toCenter.y) / tcLen + 1) / 2;
			const Pref = (sideDir && sideDir[0] === dx && sideDir[1] === dy) ? 1.0 : (prefBase[`${dx},${dy}`] ?? 0.3);
			const crowdBonus = ((crowdVertical && dx !== 0) || (!crowdVertical && dy !== 0)) ? 1 : 0;
			// disqualify base slot that would leave the board.
			let off = false;
			if (safeArea) {
				const lx = cc.x + dx * ((it.cb.maxX - it.cb.minX) / 2 + baseOffset + it.w / 2);
				const ly = cc.y + dy * ((it.cb.maxY - it.cb.minY) / 2 + baseOffset + it.h / 2);
				off = !silkInside({ minX: lx - it.w / 2, minY: ly - it.h / 2, maxX: lx + it.w / 2, maxY: ly + it.h / 2 }, safeArea);
			}
			const score = off ? -Infinity : 0.50 * Pfree + 0.35 * edgeness * Ppos + 0.15 * Pref + 0.20 * crowdBonus;
			return { dir: [dx, dy], score };
		});
		scored.sort((a, b) => b.score - a.score);
		return scored.map(s => s.dir).concat(diags);
	};

	const scoreSlot = (L: silkRect, it: Item, rank: number): number => {
		let padH = 0, ownPadH = 0, off = 0, khard = 0, lab = 0, oBody = 0, ksoft = 0, minClr = Infinity;
		if (safeArea && !silkInside(L, safeArea)) off = 1;
		for (const o of OBS) {
			if (o.kind === 'PAD') { if (o.owner !== it.cid) { if (silkOverlap(L, o.rect, 0)) padH++; } else if (silkOverlap(L, o.rect, 0)) ownPadH++; }
			else if (o.kind === 'BODY') { if (o.owner !== it.cid && silkOverlap(L, o.rect, o.m)) oBody++; }
			else if (o.kind === 'REGION_H') { if (silkOverlap(L, o.rect, o.m)) khard++; }
			else if (o.kind === 'REGION_S') { if (silkOverlap(L, o.rect, o.m)) ksoft++; }
			else if (o.kind === 'FROZEN') { if (silkOverlap(L, o.rect, o.m)) lab++; }
			if (o.kind === 'BODY' && o.owner === it.cid) continue;
			const g = silkGap(L, o.rect); if (g < minClr) minClr = g;
		}
		for (const [id, lb] of Object.entries(LAB)) { if (id !== it.attrId && silkOverlap(L, lb, Clabel)) lab++; }
		const reward = -25 * Math.min(minClr, 30) / 30;
		return 1e9 * padH + 1e8 * off + 1e6 * khard + 4e3 * ownPadH + 1e4 * lab + 5e3 * oBody + 100 * ksoft + rank * 25 + reward;
	};

	const aligned: Array<Record<string, unknown>> = [];
	const unresolved: Array<Record<string, unknown>> = [];
	for (const it of items) {
		const cc = silkCenter(it.cb);
		// offset from the body inflated by the assembly-clearance floor, so the label
		// keeps ≥ Cassembly from its OWN pads (never crowds the copper).
		const cbP = silkInflate(it.cb, Cassembly);
		const hw = (cbP.maxX - cbP.minX) / 2, hh = (cbP.maxY - cbP.minY) / 2;
		const pref = rankSides(it);
		let best: { lx: number; ly: number; L: silkRect; cost: number } | null = null;
		for (let ring = 0; ring < R_MAX && !(best && best.cost < 1e4); ring++) {
			const d = baseOffset + ring * STEP;
			for (let i = 0; i < pref.length; i++) {
				const [dx, dy] = pref[i];
				const lx = cc.x + dx * (hw + d + it.w / 2);
				const ly = cc.y + dy * (hh + d + it.h / 2);
				const L = silkInflate({ minX: lx - it.w / 2, minY: ly - it.h / 2, maxX: lx + it.w / 2, maxY: ly + it.h / 2 }, HALO);
				const cost = scoreSlot(L, it, i < 4 ? i : 3);
				if (!best || cost < best.cost) best = { lx, ly, L, cost };
				if (cost < 1e4) break;
			}
		}
		if (!best || best.cost >= 1e8) {
			unresolved.push({ designator: it.desig, reason: best && best.cost >= 1e9 ? 'pad-collision' : 'boxed-in-or-off-board', bestCost: best ? best.cost : null });
			continue;
		}
		const layer = it.layer === 2 ? 4 : 3, mirror = it.layer === 2;
		const mod: Record<string, unknown> = { x: best.lx - it.offx, y: best.ly - it.offy, rotation: 0 };
		if (layer !== it.curLayer) mod.layer = layer;
		if (mirror !== it.curMirror) mod.mirror = mirror;
		try {
			let r;
			try { r = await eda.pcb_PrimitiveAttribute.modify(it.attrId, mod as never); }
			catch (e) {
				if ('mirror' in mod || 'layer' in mod) { delete mod.mirror; delete mod.layer; r = await eda.pcb_PrimitiveAttribute.modify(it.attrId, mod as never); }
				else throw e;
			}
			LAB[it.attrId] = best.L;
			aligned.push({ designator: it.desig, x: Math.round(best.lx * 100) / 100, y: Math.round(best.ly * 100) / 100, side: pref[0], clean: best.cost < 1e4, warnBodyOverlap: best.cost >= 5e3 && best.cost < 1e4, ok: !!r });
		}
		catch (err) { skipped.push({ designator: it.desig, reason: `modify failed: ${String(err)}` }); }
	}

	const warned = aligned.filter(a => a.warnBodyOverlap === true).length;
	return { result: { aligned: aligned.length, warned, unresolved: unresolved.length, skipped: skipped.length, details: aligned, unresolvedDetails: unresolved, skippedDetails: skipped } };
};

// pcb.silk.list — enumerate every SILKSCREEN TEXT primitive with its layer +
// mirror flag, so the Go-side `pcb check` can flag flipped/back-side silkscreen
// (放反). Two sources: component-bound designator/value ATTRIBUTES
// (pcb_PrimitiveAttribute) and free STRINGS (pcb_PrimitiveString). Silk layers
// only: TOP_SILKSCREEN=3 / BOTTOM_SILKSCREEN=4. For attributes we also resolve the
// parent component's side (TOP=1 / BOTTOM=2) so the check can verify a designator
// sits on the same side as its footprint.
const PCB_TOP_SILK = 3, PCB_BOTTOM_SILK = 4;
const pcbSilkList: Handler = async () => {
	const isSilk = (l: number) => l === PCB_TOP_SILK || l === PCB_BOTTOM_SILK;

	// component primitiveId → side layer (TOP=1 / BOTTOM=2), for attribute parents.
	const compLayer = new Map<string, number>();
	try {
		for (const c of (await eda.pcb_PrimitiveComponent.getAll()) ?? []) {
			compLayer.set(c.getState_PrimitiveId(), Number(c.getState_Layer()));
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to list components for silk-list.');
	}

	const texts: Array<Record<string, unknown>> = [];
	// Real rendered extent per text (#155): the stored x/y is the BOTTOM-LEFT
	// anchor, so any consumer that centers an estimated box on it is off by half
	// a text — the bbox removes anchor/char-width/rotation guessing entirely.
	const silkBBox = async (id: string): Promise<Record<string, number> | null> => {
		try {
			const b = (await eda.pcb_Primitive.getPrimitivesBBox([id])) as { minX: number; minY: number; maxX: number; maxY: number } | null;
			return b ? { minX: b.minX, minY: b.minY, maxX: b.maxX, maxY: b.maxY } : null;
		}
		catch { return null; }
	};

	// 1. designator / value attributes (component-bound silk text)
	try {
		for (const a of (await eda.pcb_PrimitiveAttribute.getAll()) ?? []) {
			const layer = Number(a.getState_Layer());
			if (!isSilk(layer)) {
				continue;
			}
			const pid = a.getState_ParentPrimitiveId?.() ?? '';
			texts.push({
				primitiveId: a.getState_PrimitiveId(),
				bbox: await silkBBox(a.getState_PrimitiveId()),
				kind: 'attribute',
				text: a.getState_Value?.() ?? '',
				key: a.getState_Key?.() ?? '',
				layer,
				mirror: !!a.getState_Mirror?.(),
				reverse: !!a.getState_Reverse?.(),
				rotation: Number(a.getState_Rotation?.() ?? 0),
				fontFamily: a.getState_FontFamily?.() ?? '',
				fontSize: Number(a.getState_FontSize?.() ?? 0) || 0,
				componentId: pid,
				componentLayer: compLayer.get(pid) ?? 0,
				x: a.getState_X() ?? 0,
				y: a.getState_Y() ?? 0,
			});
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to enumerate silkscreen attributes.');
	}

	// 2. free silk strings (board labels, logos, notes)
	try {
		for (const s of (await eda.pcb_PrimitiveString.getAll()) ?? []) {
			const layer = Number(s.getState_Layer());
			if (!isSilk(layer)) {
				continue;
			}
			texts.push({
				primitiveId: s.getState_PrimitiveId(),
				bbox: await silkBBox(s.getState_PrimitiveId()),
				kind: 'string',
				text: s.getState_Text?.() ?? '',
				layer,
				mirror: !!s.getState_Mirror?.(),
				reverse: !!s.getState_Reverse?.(),
				rotation: Number(s.getState_Rotation?.() ?? 0),
				fontFamily: s.getState_FontFamily?.() ?? '',
				fontSize: Number(s.getState_FontSize?.() ?? 0) || 0,
				componentId: '',
				componentLayer: 0,
				x: s.getState_X() ?? 0,
				y: s.getState_Y() ?? 0,
			});
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to enumerate silkscreen strings.');
	}

	return { result: { texts, count: texts.length } };
};

// pcb.silk.add — create a free silkscreen STRING (board marking / credit / note)
// with full config (layer, font size, stroke width, rotation). Default layer is
// TOP_SILKSCREEN(3); font 40 mil / stroke 6 mil is a legible JLCPCB-safe default
// (below ~32 mil height or a stroke that's a large fraction of the height smears).
const pcbSilkAdd: Handler = async (payload) => {
	const text = requireString(payload, 'text');
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const layer = (optionalNumber(payload, 'layer') ?? PCB_TOP_SILK) as unknown as TPCB_LayersOfImage;
	const fontSize = optionalNumber(payload, 'fontSize') ?? 40;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 6;
	const rotation = optionalNumber(payload, 'rotation') ?? 0;
	const fontFamily = optionalString(payload, 'fontFamily') ?? 'default';
	if (fontFamily !== 'default') {
		try {
			const fonts = (await eda.sys_FontManager.getFontsList()) ?? [];
			if (!fonts.some(f => f.toLowerCase() === fontFamily.toLowerCase())) {
				const added = await eda.sys_FontManager.addFont(fontFamily);
				if (!added) throw new Error(`font manager did not add ${fontFamily}`);
			}
		}
		catch (err) {
			throw edaError(err, `Font ${fontFamily} is not available to EasyEDA.`);
		}
	}
	let s;
	try {
		s = await eda.pcb_PrimitiveString.create(
			layer, x, y, text, fontFamily, fontSize, lineWidth,
			1 as EPCB_PrimitiveStringAlignMode, rotation, false, 0, false, false,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to create silkscreen string.');
	}
	if (!s) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'silkscreen string create returned no primitive.');
	}
	const id = s.getState_PrimitiveId();
	let bbox;
	try { bbox = await eda.pcb_Primitive.getPrimitivesBBox([id]); }
	catch { /* bbox optional */ }
	return { result: { primitiveId: id, layer: Number(layer), x, y, fontFamily: s.getState_FontFamily?.() ?? fontFamily, fontSize, lineWidth, rotation, bbox } };
};

// pcb.silk.import_svg — create a FILLED silkscreen graphic from a pre-parsed
// complex polygon (an SVG flattened to contours by the Go CLI). The connector is
// deliberately thin: all SVG parsing / curve flattening / viewBox scaling happens
// CLI-side (internal/pcb/svgimport), and here we just hand the contour arrays to
// eda.pcb_PrimitiveImage.create on the silk layer. `polygons` is the complex
// polygon (array of TPCB_PolygonSourceArray, each [x0,y0,'L',x1,y1,…] in mil, with
// even-odd holes). (x,y) is where the artwork's top-left lands.
const pcbSilkImportSvg: Handler = async (payload) => {
	const raw = payload.polygons;
	if (!Array.isArray(raw) || raw.length === 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'polygons must be a non-empty array of contour arrays.');
	}
	for (const c of raw) {
		if (!Array.isArray(c) || c.length < 3) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each contour must be an array like [x0,y0,"L",x1,y1,…].');
		}
	}
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const layer = (optionalNumber(payload, 'layer') ?? PCB_TOP_SILK) as unknown as TPCB_LayersOfImage;
	const width = optionalNumber(payload, 'width');
	const height = optionalNumber(payload, 'height');
	const rotation = optionalNumber(payload, 'rotation') ?? 0;
	const mirror = optionalBoolean(payload, 'mirror') === true;

	// A single contour is passed as one TPCB_PolygonSourceArray; multiple contours
	// (outer + holes / disjoint shapes) are passed as an Array<TPCB_PolygonSourceArray>.
	const complexPolygon = (raw.length === 1 ? raw[0] : raw) as unknown as TPCB_PolygonSourceArray | Array<TPCB_PolygonSourceArray>;

	let img;
	try {
		img = await eda.pcb_PrimitiveImage.create(
			x, y, complexPolygon, layer,
			width, height, rotation, mirror,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to create silkscreen image from SVG.');
	}
	if (!img) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'pcb_PrimitiveImage.create returned no primitive (check the polygon contours are valid closed shapes).');
	}
	const id = img.getState_PrimitiveId();
	let bbox;
	try { bbox = await eda.pcb_Primitive.getPrimitivesBBox([id]); }
	catch { /* bbox optional */ }
	return { result: { primitiveId: id, layer: Number(layer), x, y, rotation, mirror, contours: raw.length, bbox } };
};

// pcb.silk.set — reconfigure existing silkscreen primitive(s) in one batch:
// designator/value ATTRIBUTES and free STRINGS. Any of x/y/rotation/fontSize/
// lineWidth/text may be set; only the provided keys change. Uses the reliable
// `.modify(id, props)` (setState_* alone does NOT persist for rotation).
// resolveSilkRefBBox returns the bbox of an alignment reference: "board"/"outline"
// (all BOARD_OUTLINE-layer primitives), "fill" (all copper fills combined), or a
// component designator (its footprint bbox). null if it can't be resolved.
async function resolveSilkRefBBox(ref: string): Promise<silkRect | null> {
	const r = ref.trim().toLowerCase();
	if (r === 'board' || r === 'outline') {
		const ids = await boardOutlineIds();
		return ids.length ? (await eda.pcb_Primitive.getPrimitivesBBox(ids) as silkRect) : null;
	}
	if (r === 'fill') {
		const ids = ((await eda.pcb_PrimitiveFill.getAll()) ?? []).map(f => f.getState_PrimitiveId());
		return ids.length ? (await eda.pcb_Primitive.getPrimitivesBBox(ids) as silkRect) : null;
	}
	for (const c of (await eda.pcb_PrimitiveComponent.getAll()) ?? []) {
		if (c.getState_Designator?.() === ref) return await eda.pcb_Primitive.getPrimitivesBBox([c.getState_PrimitiveId()]) as silkRect;
	}
	return null;
}

const pcbSilkSet: Handler = async (payload) => {
	const raw = payload.primitiveIds ?? payload.ids;
	let ids: Array<string>;
	if (typeof raw === 'string') ids = [raw];
	else if (Array.isArray(raw) && raw.every(v => typeof v === 'string')) ids = raw as Array<string>;
	else throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing "primitiveIds" (string or string[]).');

	const baseProps: Record<string, unknown> = {};
	for (const k of ['x', 'y', 'rotation', 'fontFamily', 'fontSize', 'lineWidth', 'text'] as const) {
		if (payload[k] !== undefined && payload[k] !== null) baseProps[k] = payload[k];
	}

	// Optional ALIGN: reposition each silk relative to a reference bbox (a component
	// designator, "board"/"outline", or "fill"). Modes: center|mid (both axes),
	// centerx|centery, left|right|top|bottom (edge-align). Computes per-silk from its
	// own bbox, so the CENTER/edge lands exactly on the reference.
	const align = (optionalString(payload, 'align') ?? '').trim().toLowerCase();
	let refBox: silkRect | null = null;
	if (align) {
		const ref = optionalString(payload, 'ref') ?? 'board';
		refBox = await resolveSilkRefBBox(ref);
		if (!refBox) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `could not resolve align --ref "${ref}" (use a designator, "board", or "fill").`);
		}
	}
	if (Object.keys(baseProps).length === 0 && !align) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'nothing to do — provide x/y/rotation/fontFamily/fontSize/lineWidth/text, and/or --align (+ --ref).');
	}
	if (typeof baseProps.fontFamily === 'string' && baseProps.fontFamily !== 'default') {
		try {
			const fontFamily = baseProps.fontFamily;
			const fonts = (await eda.sys_FontManager.getFontsList()) ?? [];
			if (!fonts.some(f => f.toLowerCase() === fontFamily.toLowerCase())) {
				const added = await eda.sys_FontManager.addFont(fontFamily);
				if (!added) throw new Error(`font manager did not add ${fontFamily}`);
			}
		}
		catch (err) { throw edaError(err, `Font ${String(baseProps.fontFamily)} is not available to EasyEDA.`); }
	}

	const attrIds = new Set<string>((await eda.pcb_PrimitiveAttribute.getAll() ?? []).map(a => a.getState_PrimitiveId()));
	const results: Array<Record<string, unknown>> = [];
	for (const id of ids) {
		try {
			const isAttr = attrIds.has(id);
			const props: Record<string, unknown> = { ...baseProps };

			if (align && refBox) {
				// Anchor offset: the stored (x,y) vs the rendered bbox min corner.
				const cur = isAttr ? await eda.pcb_PrimitiveAttribute.get(id) : await eda.pcb_PrimitiveString.get(id);
				const sb = await eda.pcb_Primitive.getPrimitivesBBox([id]) as silkRect;
				if (cur && sb) {
					const offx = (cur.getState_X() ?? sb.minX) - sb.minX;
					const offy = (cur.getState_Y() ?? sb.minY) - sb.minY;
					const w = sb.maxX - sb.minX, h = sb.maxY - sb.minY;
					const rcx = (refBox.minX + refBox.maxX) / 2, rcy = (refBox.minY + refBox.maxY) / 2;
					let tMinX = sb.minX, tMinY = sb.minY;
					if (align === 'center' || align === 'mid' || align === 'centerx') tMinX = rcx - w / 2;
					if (align === 'center' || align === 'mid' || align === 'centery') tMinY = rcy - h / 2;
					if (align === 'left') tMinX = refBox.minX;
					if (align === 'right') tMinX = refBox.maxX - w;
					if (align === 'top') tMinY = refBox.maxY - h;
					if (align === 'bottom') tMinY = refBox.minY;
					props.x = tMinX + offx;
					props.y = tMinY + offy;
				}
			}

			if (Object.keys(props).length === 0) {
				results.push({ primitiveId: id, ok: false, error: 'nothing to set for this id' });
				continue;
			}
			let modified;
			if (isAttr) {
				if ('text' in props) { props.value = props.text; delete props.text; }
				modified = await eda.pcb_PrimitiveAttribute.modify(id, props as never);
			}
			else {
				modified = await eda.pcb_PrimitiveString.modify(id, props as never);
			}
			if (!modified) {
				results.push({ primitiveId: id, ok: false, verified: false, error: 'modify returned no primitive' });
				continue;
			}
			const actual = isAttr ? await eda.pcb_PrimitiveAttribute.get(id) : await eda.pcb_PrimitiveString.get(id);
			if (!actual) {
				results.push({ primitiveId: id, ok: false, verified: false, error: 'modified primitive was not found by readback' });
				continue;
			}
			const requestedFont = typeof props.fontFamily === 'string' ? props.fontFamily : null;
			const actualFont = actual.getState_FontFamily?.() ?? null;
			const fontVerified = requestedFont === null
				|| (typeof actualFont === 'string' && actualFont.toLowerCase() === requestedFont.toLowerCase());
			results.push({
				primitiveId: id, ok: fontVerified, verified: fontVerified,
				requested: { ...props }, actual: {
					x: actual.getState_X?.() ?? null, y: actual.getState_Y?.() ?? null,
					fontFamily: actualFont,
				},
				fontFamily: actualFont,
				...(fontVerified ? {} : { error: `fontFamily readback differs: requested=${requestedFont} actual=${String(actualFont)}` }),
			});
		}
		catch (err) {
			results.push({ primitiveId: id, ok: false, error: String(err) });
		}
	}
	return { result: { align: align || undefined, count: results.length, results } };
};

/**
 * Auto-generate network-name silkscreen labels for nets with pads in a zone.
 * Reads the list of nets and their pads, filters by zone_rect, computes
 * collision-free positions, and creates free silkscreen STRING primitives.
 * The core geometric layout is computed in the Go daemon; connector just
 * fetches raw data and creates the strings.
 */
const pcbSilkNetnames: Handler = async (payload) => {
	const zoneRect = payload.zone_rect as Record<string, number> | undefined;
	if (!zoneRect || typeof zoneRect.left !== 'number' || typeof zoneRect.top !== 'number' ||
		typeof zoneRect.right !== 'number' || typeof zoneRect.bottom !== 'number') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing "zone_rect" ({left, top, right, bottom} in mil).');
	}

	const layer = optionalNumber(payload, 'layer') ?? 3; // 3=TOP_SILKSCREEN
	const align = optionalString(payload, 'align') ?? 'left';
	const fontSize = optionalNumber(payload, 'fontSize') ?? 40;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 6;
	const excludeNets = (Array.isArray(payload.exclude_nets) ? payload.exclude_nets : []) as string[];

	// Read nets and pads
	let netNames: string[] = [];
	let pads: Array<{ primitiveId: string; net: string; x: number; y: number; width: number; height: number }> = [];

	try {
		netNames = (await eda.pcb_Net.getAllNetsName()) ?? [];
	} catch (err) {
		throw edaError(err, 'Failed to read PCB net names.');
	}

	try {
		const allPads = (await eda.pcb_PrimitivePad.getAll()) ?? [];
		for (const pad of allPads) {
			const net = pad.getState_Net?.() ?? null;
			const padId = pad.getState_PrimitiveId?.() ?? null;
			const x = pad.getState_X?.() ?? 0;
			const y = pad.getState_Y?.() ?? 0;
			if (net && padId) {
				// Real pad extent from the shape tuple; nominal 50mil only for
				// complex-polygon pads that carry no cheap extent.
				const ext = padExtent(pad as unknown as PcbPad);
				const width = ext?.width ?? 50, height = ext?.height ?? 50;
				pads.push({ primitiveId: padId, net, x, y, width, height });
			}
		}
	} catch (err) {
		throw edaError(err, 'Failed to read PCB pads.');
	}

	// Filter nets: exclude those with no pads in zone, exclude manually excluded nets
	const excludeSet = new Set(excludeNets);
	const activeNets: Array<{ name: string; pads: typeof pads }> = [];
	for (const netName of netNames) {
		if (excludeSet.has(netName)) continue;
		const netPads = pads.filter(p => p.net === netName);
		const inZone = netPads.some(p => p.x >= zoneRect.left && p.x <= zoneRect.right && p.y >= zoneRect.bottom && p.y <= zoneRect.top);
		if (inZone && netPads.length > 0) {
			activeNets.push({ name: netName, pads: netPads });
		}
	}

	// Sort nets by alignment (left=left-to-right by min pad x, right=right-to-left by max pad x)
	activeNets.sort((a, b) => {
		const minA = Math.min(...a.pads.map(p => p.x));
		const minB = Math.min(...b.pads.map(p => p.x));
		return align === 'right' ? minB - minA : minA - minB;
	});

	// Compute layout: place each net's label in free space around its zone-closest pad
	const created: Array<Record<string, unknown>> = [];
	const failed: Array<Record<string, unknown>> = [];
	const occupied: Array<silkRect> = [];

	// Helper: check if a rectangle overlaps with any occupied space or zone boundary
	const canPlaceLabel = (labelBbox: silkRect, margin: number = 5): boolean => {
		// Check zone boundary
		if (labelBbox.minX < zoneRect.left - margin || labelBbox.maxX > zoneRect.right + margin ||
			labelBbox.minY < zoneRect.bottom - margin || labelBbox.maxY > zoneRect.top + margin) {
			return false;
		}
		// Check collision with occupied space
		for (const occ of occupied) {
			if (silkOverlap(labelBbox, occ, margin)) return false;
		}
		return true;
	};

	// Helper: estimate label bbox (approximate — actual bbox from create is more accurate)
	const estimateLabelBbox = (x: number, y: number): silkRect => {
		// Font 40mil, ~60% width (rough estimate for monospace)
		const textLen = Math.max(5, 2) * fontSize * 0.6; // min 5 chars
		return { minX: x, minY: y - fontSize, maxX: x + textLen, maxY: y };
	};

	for (const net of activeNets) {
		try {
			// Find closest pad to zone center
			const zoneCx = (zoneRect.left + zoneRect.right) / 2;
			const zoneCy = (zoneRect.top + zoneRect.bottom) / 2;
			const padInZone = net.pads.filter(p =>
				p.x >= zoneRect.left && p.x <= zoneRect.right &&
				p.y >= zoneRect.bottom && p.y <= zoneRect.top
			);
			const padsToTry = padInZone.length > 0 ? padInZone : net.pads;

			let best: { x: number; y: number } | null = null;

			// Try each pad
			for (const pad of padsToTry) {
				if (best) break; // Found a placement, done
				const margin = 10;
				const candidates = [
					{ x: pad.x + pad.width / 2 + margin, y: pad.y + margin, dir: 'right' },
					{ x: pad.x - pad.width / 2 - margin, y: pad.y + margin, dir: 'left' },
					{ x: pad.x + margin, y: pad.y + pad.height / 2 + margin, dir: 'top' },
					{ x: pad.x + margin, y: pad.y - pad.height / 2 - margin, dir: 'bottom' },
				];

				// Try each direction
				for (const cand of candidates) {
					const estBbox = estimateLabelBbox(cand.x, cand.y);
					if (canPlaceLabel(estBbox)) {
						best = { x: cand.x, y: cand.y };
						break;
					}
				}
			}

			if (!best) {
				failed.push({ net: net.name, reason: 'no free space found (zone fully occupied)' });
				continue;
			}

			// Native text requires a registered font and LEFT_TOP (1); the collision
			// estimate extends right/down from this same anchor.
			const created_prim = await eda.pcb_PrimitiveString.create(
				layer as unknown as TPCB_LayersOfImage,
				best.x,
				best.y,
				net.name,
				'default',
				fontSize,
				lineWidth,
				1 as EPCB_PrimitiveStringAlignMode,
				0, // rotation
				false, // reverse
				0, // expansion
				false, // isMirror
				false, // primitiveLock
			);

			if (!created_prim) {
				failed.push({ net: net.name, reason: 'create returned no primitive' });
				continue;
			}

			const bbox = await eda.pcb_Primitive.getPrimitivesBBox([created_prim.getState_PrimitiveId?.() ?? '']) as silkRect | null;
			if (bbox) occupied.push(bbox);

			created.push({
				net: net.name,
				primitiveId: created_prim.getState_PrimitiveId?.() ?? '',
				x: best.x,
				y: best.y,
				fontSize,
				lineWidth,
				bbox,
			});
		} catch (err) {
			failed.push({ net: net.name, reason: String(err) });
		}
	}

	return {
		result: {
			created,
			total: created.length,
			failed: failed.length > 0 ? failed : undefined,
		},
	};
};

/**
 * Label component pads with pin numbers and/or net names. Useful for large
 * connectors (J2, U1, etc.) to mark each pin's function. Reads pad coordinates
 * + nets, computes collision-free positions around each pad, creates labels.
 */
const pcbSilkLabelPads: Handler = async (payload) => {
	const refs = (Array.isArray(payload.refs) ? payload.refs : []) as string[];
	if (refs.length === 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing "refs" ([]string of designators, e.g. ["J2", "U1"]).');
	}

	const layer = optionalNumber(payload, 'layer') ?? 3;
	const fontSize = optionalNumber(payload, 'fontSize') ?? 30;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 4;
	const content = optionalString(payload, 'content') ?? 'both'; // pin-number|net-name|both
	const userSide = optionalString(payload, 'side') ?? 'auto'; // auto|right|below|above|left
	const userAlignAxis = optionalString(payload, 'align_axis') ?? 'auto'; // auto|x|y
	const excludeNets = (Array.isArray(payload.exclude_nets) ? payload.exclude_nets : []) as string[];
	const excludeSet = new Set(excludeNets);

	const created: Array<Record<string, unknown>> = [];
	const failed: Array<Record<string, unknown>> = [];
	const occupied: Array<silkRect> = [];

	// Helper: check if label can be placed (no collision, within reasonable distance)
	const canPlaceLabel = (labelBbox: silkRect, margin: number = 5): boolean => {
		for (const occ of occupied) {
			if (silkOverlap(labelBbox, occ, margin)) return false;
		}
		return true;
	};

	// Estimate label bbox
	const estimateLabelBbox = (x: number, y: number, text: string): silkRect => {
		const textLen = Math.max(2, text.length) * fontSize * 0.6;
		return { minX: x, minY: y - fontSize, maxX: x + textLen, maxY: y };
	};

	// Read all components
	let components: NonNullable<Awaited<ReturnType<typeof eda.pcb_PrimitiveComponent.getAll>>> = [];
	try {
		components = (await eda.pcb_PrimitiveComponent.getAll()) ?? [];
	} catch (err) {
		throw edaError(err, 'Failed to read PCB components.');
	}

	// Filter to specified refs
	const refSet = new Set(refs);
	const targetComps = components.filter(c => {
		const des = c.getState_Designator?.() ?? '';
		return refSet.has(des);
	});

	if (targetComps.length === 0) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `No components found with refs: ${refs.join(', ')}`);
	}

	// Determine side and align-axis ONCE for all components
	let chosenSide: 'right' | 'below' | 'above' | 'left' = 'right';
	let chosenAlignAxis: 'x' | 'y' = 'x'; // x=vertical pins (all same X), y=horizontal pins (all same Y)

	if (userSide !== 'auto' && ['right', 'below', 'above', 'left'].includes(userSide)) {
		chosenSide = userSide as 'right' | 'below' | 'above' | 'left';
	}
	if (userAlignAxis !== 'auto' && ['x', 'y'].includes(userAlignAxis)) {
		chosenAlignAxis = userAlignAxis as 'x' | 'y';
	}

	// Auto-detect based on first component's bbox if both are 'auto'
	if ((userSide === 'auto' || userAlignAxis === 'auto') && targetComps.length > 0) {
		try {
			const firstCompId = targetComps[0].getState_PrimitiveId?.() ?? '';
			const firstBbox = (await eda.pcb_Primitive.getPrimitivesBBox?.([firstCompId])) as silkRect | null;
			if (firstBbox) {
				const width = firstBbox.maxX - firstBbox.minX;
				const height = firstBbox.maxY - firstBbox.minY;
				// Tall component (height > width) → vertical pins → X-axis align, labels on right/left
				// Wide component (width > height) → horizontal pins → Y-axis align, labels on top/below
				if (userAlignAxis === 'auto') {
					chosenAlignAxis = height > width ? 'x' : 'y';
				}
				if (userSide === 'auto') {
					chosenSide = height > width ? 'right' : 'below';
				}
			}
		} catch {
			// If auto-detect fails, use defaults
		}
	}

	// Process each component's pads
	for (const comp of targetComps) {
		const compId = comp.getState_PrimitiveId?.() ?? '';
		const designator = comp.getState_Designator?.() ?? '';
		let pins: NonNullable<Awaited<ReturnType<typeof eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId>>> = [];

		try {
			pins = (await eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId?.(compId)) ?? [];
		} catch (err) {
			failed.push({ ref: designator, pin: 'all', reason: `Failed to read pins: ${String(err)}` });
			continue;
		}

		// Get component bbox to place labels outside the component
		let compBbox: silkRect | null = null;
		try {
			compBbox = (await eda.pcb_Primitive.getPrimitivesBBox?.([compId])) as silkRect | null;
		} catch {
			// bbox is optional, continue without it
		}

		// Collect all valid pins (excluding those in exclude_nets)
		const validPins = pins.filter(pin => {
			const net = pin.getState_Net?.() ?? '';
			return !(net && excludeSet.has(net));
		});

		if (validPins.length === 0) continue;

		// Label each pin, placed PARALLEL at each pin's Y coordinate
		// All labels aligned on the chosen side, each at its corresponding pin's height
		let pinIndex = 0;

		for (const pin of validPins) {
			try {
				const pinNum = pin.getState_PadNumber?.() ?? '?';
				const net = pin.getState_Net?.() ?? '';
				const pinX = pin.getState_X?.() ?? 0;
				const pinY = pin.getState_Y?.() ?? 0;

				// Generate label text
				let labelText = '';
				if (content === 'pin-number') labelText = String(pinNum);
				else if (content === 'net-name') labelText = net || 'NC';
				else labelText = `${pinNum}(${net || 'NC'})`;

				// Place label on the chosen side, with alignment based on pin orientation
				let bestPos: { x: number; y: number } | null = null;
				const margin = 20;

				if (compBbox) {
					if (chosenAlignAxis === 'x') {
						// X-axis align: all labels same X, Y = pin's Y (vertical pin array)
						if (chosenSide === 'right') {
							bestPos = { x: compBbox.maxX + margin, y: pinY };
						} else if ((chosenSide as string) === 'left') {
							bestPos = { x: compBbox.minX - margin, y: pinY };
						} else {
							bestPos = { x: compBbox.maxX + margin, y: pinY }; // fallback to right
						}
					} else {
						// Y-axis align: all labels same Y, X = pin's X (horizontal pin array)
						if (chosenSide === 'below') {
							bestPos = { x: pinX, y: compBbox.minY - margin };
						} else if (chosenSide === 'above') {
							bestPos = { x: pinX, y: compBbox.maxY + margin };
						} else {
							bestPos = { x: pinX, y: compBbox.minY - margin }; // fallback to below
						}
					}
				} else {
					bestPos = { x: pinX + 30, y: pinY };
				}
				pinIndex++;

				if (!bestPos) {
					failed.push({ ref: designator, pin: String(pinNum), reason: 'no space found' });
					continue;
				}

				// Verify placement doesn't collide
				const estBbox = estimateLabelBbox(bestPos.x, bestPos.y, labelText);
				if (!canPlaceLabel(estBbox)) {
					failed.push({ ref: designator, pin: String(pinNum), reason: 'collision detected' });
					continue;
				}

				// Create label
				const label = await eda.pcb_PrimitiveString.create(
					layer as unknown as TPCB_LayersOfImage,
					bestPos.x,
					bestPos.y,
					labelText,
					'default',
					fontSize,
					lineWidth,
					1 as EPCB_PrimitiveStringAlignMode,
					0, // rotation
					false, // reverse
					0, // expansion
					false, // isMirror
					false, // primitiveLock
				);

				if (!label) {
					failed.push({ ref: designator, pin: String(pinNum), reason: 'create returned no primitive' });
					continue;
				}

				const bbox = await eda.pcb_Primitive.getPrimitivesBBox([label.getState_PrimitiveId?.() ?? '']) as silkRect | null;
				if (bbox) occupied.push(bbox);

				created.push({
					ref: designator,
					pin: String(pinNum),
					net: net || '',
					primitiveId: label.getState_PrimitiveId?.() ?? '',
					x: bestPos.x,
					y: bestPos.y,
					bbox,
				});
			} catch (err) {
				failed.push({ ref: designator, pin: 'unknown', reason: String(err) });
			}
		}
	}

	return {
		result: {
			created,
			total: created.length,
			side_chosen: chosenSide,
			align_axis_chosen: chosenAlignAxis,
			failed: failed.length > 0 ? failed : undefined,
		},
	};
};

/**
 * List all nets on the active PCB. `IPCB_NetInfo` ({ net, color, length }) is a
 * plain data object and serializes directly.
 */
const pcbNetsList: Handler = async () => {
	let nets;
	try {
		nets = await eda.pcb_Net.getAllNets();
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB nets.');
	}
	return { result: { nets, count: nets.length } };
};

/**
 * Read-only PCB design report driven by per-net copper length:
 *   - nets[]                  — every net + its routed length (mil)
 *   - netClasses[]            — each class's member nets + aggregate length
 *   - differentialPairs[]     — P/N lengths + skew (|lenP − lenN|)
 *   - equalLengthNetGroups[]  — per-net lengths + spread (max − min)
 * Each sub-read is best-effort: a failing query degrades to a `*Error` field
 * rather than failing the whole report. The pcb_Drc.* reads may require the PCB
 * to be the active/foreground tab (same constraint as pcb.drc.check).
 */
const pcbReport: Handler = async () => {
	const result: Record<string, unknown> = {};

	// Per-net length, cached so the differential/equal-length views reuse it.
	const lengthOf = new Map<string, number | null>();
	const len = async (net: string): Promise<number | null> => {
		if (lengthOf.has(net)) return lengthOf.get(net) ?? null;
		let l: number | null = null;
		try { l = (await eda.pcb_Net.getNetLength(net)) ?? null; }
		catch { /* per-net length best-effort */ }
		lengthOf.set(net, l);
		return l;
	};

	try {
		const names = (await eda.pcb_Net.getAllNetsName()) ?? [];
		const nets: Array<{ net: string; length: number | null }> = [];
		for (const net of names) nets.push({ net, length: await len(net) });
		result.nets = nets;
		result.netCount = nets.length;
	}
	catch (err) {
		result.netsError = describeThrown(err);
	}

	try {
		const classes = (await eda.pcb_Drc.getAllNetClasses()) ?? [];
		result.netClasses = await Promise.all(classes.map(async (c) => {
			let total = 0, measured = 0;
			for (const n of c.nets ?? []) { const l = await len(n); if (typeof l === 'number') { total += l; measured++; } }
			// null (not 0) when nothing measured — consistent with equalLength spread.
			return { name: c.name, nets: c.nets, totalLength: measured ? total : null };
		}));
	}
	catch (err) { result.netClassesError = describeThrown(err); }

	try {
		const pairsRaw = await eda.pcb_Drc.getAllDifferentialPairs();
		// Since EDA v3.4 this may return an object map instead of an array (a
		// documented breaking change) — normalize both shapes to a list of pairs.
		const pairs = (Array.isArray(pairsRaw) ? pairsRaw : Object.values(pairsRaw ?? {}))
			.filter((p): p is { name: string; positiveNet: string; negativeNet: string } =>
				!!p && typeof p === 'object' && 'positiveNet' in p && 'negativeNet' in p);
		result.differentialPairs = await Promise.all(pairs.map(async (p) => {
			const lp = await len(p.positiveNet);
			const ln = await len(p.negativeNet);
			const skew = (typeof lp === 'number' && typeof ln === 'number') ? Math.abs(lp - ln) : null;
			return { name: p.name, positiveNet: p.positiveNet, negativeNet: p.negativeNet, positiveLength: lp, negativeLength: ln, skew };
		}));
	}
	catch (err) { result.differentialPairsError = describeThrown(err); }

	try {
		const groups = (await eda.pcb_Drc.getAllEqualLengthNetGroups()) ?? [];
		result.equalLengthNetGroups = await Promise.all(groups.map(async (g) => {
			const members: Array<{ net: string; length: number | null }> = [];
			const vals: Array<number> = [];
			for (const n of g.nets ?? []) { const l = await len(n); members.push({ net: n, length: l }); if (typeof l === 'number') vals.push(l); }
			const spread = vals.length ? Math.max(...vals) - Math.min(...vals) : null;
			return { name: g.name, members, spread };
		}));
	}
	catch (err) { result.equalLengthNetGroupsError = describeThrown(err); }

	return { result };
};

// ─── PCB length constraints: differential pairs + equal-length groups (#176) ─
//
// `pcb.report` could already READ these, but nothing could CREATE one — so on
// any board driven purely by this CLI the report returned empty arrays forever.
// The platform exposes the full surface (@beta; live-probed on 3.2.149:
// create/delete/getAll all return true and read back correctly).
//
// House rules applied to every mutation below:
//   - PRE-VALIDATE net names against the board. A constraint pointing at a
//     non-existent net is silently useless — the platform accepts it anyway.
//     Refuse before touching anything (#120 前置拒绝: zero mutation, exact blame).
//   - READ BACK after every write and report `verified`. The platform's boolean
//     is not proof — this repo has been burned by ok-but-not-applied enough
//     times that回执不算数、回读才算数 is a standing rule.
//   - IDEMPOTENT: creating an existing name reports `alreadyExists` instead of
//     failing, so replaying a spec (or a playbook) is safe.

interface DiffPairItem { name: string; positiveNet: string; negativeNet: string }
interface EqLenGroupItem { name: string; nets: Array<string> }

// Since EDA v3.4 getAllDifferentialPairs may return an object map instead of an
// array (documented breaking change) — normalize both shapes, same as the report.
export function constraintList<T>(raw: unknown): Array<T> {
	return (Array.isArray(raw) ? raw : Object.values((raw ?? {}) as Record<string, T>)) as Array<T>;
}

async function readDiffPairs(): Promise<Array<DiffPairItem>> {
	const raw = await eda.pcb_Drc.getAllDifferentialPairs();
	return constraintList<DiffPairItem>(raw).filter(p =>
		!!p && typeof p === 'object' && 'positiveNet' in p && 'negativeNet' in p);
}

async function readEqLenGroups(): Promise<Array<EqLenGroupItem>> {
	const raw = await eda.pcb_Drc.getAllEqualLengthNetGroups();
	return constraintList<EqLenGroupItem>(raw).filter(g => !!g && typeof g === 'object' && 'name' in g);
}

/** Refuse up front when a requested net isn't on this board. */
async function assertNetsExist(nets: Array<string>, what: string): Promise<void> {
	let known: Array<string> = [];
	try { known = (await eda.pcb_Net.getAllNetsName()) ?? []; }
	catch { return; } // can't read the board's nets — don't block on our own check
	if (!known.length) return;
	const missing = nets.filter(n => !known.includes(n));
	if (!missing.length) return;
	throw new ActionError(
		ErrorCodes.PRECONDITION_REFUSED,
		`${what}: net(s) not on this PCB: ${missing.join(', ')}. `
		+ `Nothing was created. List the board's nets with \`easyeda pcb nets\` and use those exact names `
		+ `(net names are case-sensitive and come from the schematic).`,
	);
}

const pcbConstraintList: Handler = async () => {
	const [pairs, groups] = await Promise.all([
		readDiffPairs().catch(err => { throw edaError(err, 'Failed to read differential pairs.'); }),
		readEqLenGroups().catch(err => { throw edaError(err, 'Failed to read equal-length groups.'); }),
	]);
	return { result: { differentialPairs: pairs, equalLengthGroups: groups, count: pairs.length + groups.length } };
};

const pcbDiffPairCreate: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const positiveNet = requireString(payload, 'positiveNet');
	const negativeNet = requireString(payload, 'negativeNet');
	if (positiveNet === negativeNet) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`differential pair "${name}": positiveNet and negativeNet are both "${positiveNet}" — a pair needs two different nets.`);
	}
	const existing = await readDiffPairs();
	const already = existing.find(p => p.name === name);
	if (already) {
		// Idempotent only when it already describes the SAME pair; a name clash
		// on different nets is a real conflict the caller must resolve.
		if (already.positiveNet === positiveNet && already.negativeNet === negativeNet) {
			return { result: { name, positiveNet, negativeNet, alreadyExists: true, verified: true } };
		}
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`differential pair "${name}" already exists on ${already.positiveNet}/${already.negativeNet}, `
			+ `not ${positiveNet}/${negativeNet}. Delete it first (\`easyeda pcb diff-pair delete --name ${name}\`) or pick another name.`);
	}
	await assertNetsExist([positiveNet, negativeNet], `differential pair "${name}"`);

	let ok = false;
	try { ok = await eda.pcb_Drc.createDifferentialPair(name, positiveNet, negativeNet); }
	catch (err) { throw edaError(err, `Failed to create differential pair "${name}".`); }

	const after = (await readDiffPairs()).find(p => p.name === name);
	const verified = !!after && after.positiveNet === positiveNet && after.negativeNet === negativeNet;
	if (!verified) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`createDifferentialPair("${name}") returned ${ok} but the pair is not on the board when read back. `
			+ `Check that the PCB document is the foreground tab, then retry.`);
	}
	return { result: { name, positiveNet, negativeNet, created: true, verified } };
};

const pcbDiffPairDelete: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const before = await readDiffPairs();
	if (!before.some(p => p.name === name)) {
		return {
			result: { name, deleted: false, notFound: true, verified: true },
			warnings: [`differential pair "${name}" does not exist — nothing to delete (known: ${before.map(p => p.name).join(', ') || 'none'}).`],
		};
	}
	let ok = false;
	try { ok = await eda.pcb_Drc.deleteDifferentialPair(name); }
	catch (err) { throw edaError(err, `Failed to delete differential pair "${name}".`); }
	const stillThere = (await readDiffPairs()).some(p => p.name === name);
	if (stillThere) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`deleteDifferentialPair("${name}") returned ${ok} but the pair is still on the board when read back.`);
	}
	return { result: { name, deleted: true, verified: true } };
};

const pcbDiffPairRename: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const newName = requireString(payload, 'newName');
	const before = await readDiffPairs();
	if (!before.some(p => p.name === name)) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`differential pair "${name}" does not exist (known: ${before.map(p => p.name).join(', ') || 'none'}).`);
	}
	if (before.some(p => p.name === newName)) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `a differential pair named "${newName}" already exists.`);
	}
	let ok = false;
	try { ok = await eda.pcb_Drc.modifyDifferentialPairName(name, newName); }
	catch (err) { throw edaError(err, `Failed to rename differential pair "${name}" → "${newName}".`); }
	const after = await readDiffPairs();
	const verified = after.some(p => p.name === newName) && !after.some(p => p.name === name);
	if (!verified) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`modifyDifferentialPairName("${name}","${newName}") returned ${ok} but the rename is not visible on read-back.`);
	}
	return { result: { name: newName, previousName: name, renamed: true, verified } };
};

const pcbEqGroupCreate: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const nets = requireStringArray(payload, 'nets');
	if (nets.length < 2) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`equal-length group "${name}" needs at least 2 nets (got ${nets.length}) — a one-net group constrains nothing.`);
	}
	const existing = await readEqLenGroups();
	const already = existing.find(g => g.name === name);
	if (already) {
		const same = already.nets?.length === nets.length && nets.every(n => already.nets.includes(n));
		if (same) return { result: { name, nets, alreadyExists: true, verified: true } };
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`equal-length group "${name}" already exists with nets [${(already.nets ?? []).join(', ')}]. `
			+ `Add to it with \`easyeda pcb eq-group add --name ${name} --nets …\`, or delete it first.`);
	}
	await assertNetsExist(nets, `equal-length group "${name}"`);

	let ok = false;
	try {
		// color is required by the signature but the platform accepts undefined
		// (live-probed: the group comes back with a default color).
		ok = await eda.pcb_Drc.createEqualLengthNetGroup(name, nets, undefined as never);
	}
	catch (err) { throw edaError(err, `Failed to create equal-length group "${name}".`); }

	const after = (await readEqLenGroups()).find(g => g.name === name);
	const verified = !!after && nets.every(n => (after.nets ?? []).includes(n));
	if (!verified) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`createEqualLengthNetGroup("${name}") returned ${ok} but the group is not on the board when read back.`);
	}
	return { result: { name, nets: after.nets ?? nets, created: true, verified } };
};

const pcbEqGroupAddNets: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const nets = requireStringArray(payload, 'nets');
	const before = (await readEqLenGroups()).find(g => g.name === name);
	if (!before) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`equal-length group "${name}" does not exist — create it first with \`easyeda pcb eq-group create --name ${name} --nets …\`.`);
	}
	const fresh = nets.filter(n => !(before.nets ?? []).includes(n));
	if (!fresh.length) {
		return { result: { name, nets: before.nets ?? [], added: [], alreadyMembers: nets, verified: true } };
	}
	await assertNetsExist(fresh, `equal-length group "${name}"`);

	let ok = false;
	try { ok = await eda.pcb_Drc.addNetToEqualLengthNetGroup(name, fresh); }
	catch (err) { throw edaError(err, `Failed to add nets to equal-length group "${name}".`); }

	const after = (await readEqLenGroups()).find(g => g.name === name);
	const landed = fresh.filter(n => (after?.nets ?? []).includes(n));
	const notApplied = fresh.filter(n => !landed.includes(n));
	if (notApplied.length) {
		// Canvas already changed for the ones that landed — report partial success
		// rather than throwing (the 部分应用 convention, #151).
		return {
			result: { name, nets: after?.nets ?? [], added: landed, partial: true, notApplied, verified: false },
			warnings: [`addNetToEqualLengthNetGroup("${name}") returned ${ok} but ${notApplied.join(', ')} `
				+ `did not appear on read-back — retry those, or check they are real nets on this board.`],
		};
	}
	return { result: { name, nets: after?.nets ?? [], added: landed, verified: true } };
};

const pcbEqGroupDelete: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const before = await readEqLenGroups();
	if (!before.some(g => g.name === name)) {
		return {
			result: { name, deleted: false, notFound: true, verified: true },
			warnings: [`equal-length group "${name}" does not exist — nothing to delete (known: ${before.map(g => g.name).join(', ') || 'none'}).`],
		};
	}
	let ok = false;
	try { ok = await eda.pcb_Drc.deleteEqualLengthNetGroup(name); }
	catch (err) { throw edaError(err, `Failed to delete equal-length group "${name}".`); }
	if ((await readEqLenGroups()).some(g => g.name === name)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`deleteEqualLengthNetGroup("${name}") returned ${ok} but the group is still on the board when read back.`);
	}
	return { result: { name, deleted: true, verified: true } };
};

// ─── PCB layout (Phase 2 — schematic→PCB sync + component layout) ─────

/**
 * Read the current Board (the schematic↔PCB linkage) and current PCB — the
 * prerequisite context for pcb.import_changes. IDMT_BoardItem / IDMT_PcbItem are
 * plain data objects.
 */
const pcbBoardInfo: Handler = async () => {
	let board;
	try {
		board = await eda.dmt_Board.getCurrentBoardInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to read current Board info.');
	}
	let pcb;
	try {
		pcb = await eda.dmt_Pcb.getCurrentPcbInfo();
	}
	catch { /* best-effort */ }
	return {
		result: {
			linked: !!board,
			board: board ? serializeBoard(board) : null,
			pcb: pcb ? { uuid: pcb.uuid, name: pcb.name } : null,
		},
	};
};

// ─── Board (板子/组合 — schematic↔PCB binding) ─────────────────────────
// A Board groups one schematic + one PCB and is identified by NAME (not uuid).

type BoardItem = NonNullable<Awaited<ReturnType<typeof eda.dmt_Board.getCurrentBoardInfo>>>;

/**
 * Serialize a Board to the {name, schematic, pcb, parentProjectUuid} shape.
 * A Board can legitimately hold only a PCB or only a schematic (e.g. after
 * `new-board` moves a schematic out, or a standalone PCB board) — so read
 * schematic/pcb defensively. Reading `board.schematic.uuid` unconditionally
 * crashed `board list` with "Cannot read properties of undefined (reading 'uuid')".
 */
function serializeBoard(board: BoardItem): Record<string, unknown> {
	return {
		name: board.name,
		schematicUuid: board.schematic?.uuid ?? null,
		schematicName: board.schematic?.name ?? null,
		pcbUuid: board.pcb?.uuid ?? null,
		pcbName: board.pcb?.name ?? null,
		parentProjectUuid: board.parentProjectUuid,
	};
}

/** List all Boards (组合) in the current project. */
const boardList: Handler = async () => {
	let boards;
	try {
		boards = await eda.dmt_Board.getAllBoardsInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to list Boards.');
	}
	return { result: { boards: boards.map(serializeBoard), count: boards.length } };
};

/** Read the current Board (its bound schematic + PCB). */
const boardCurrent: Handler = async () => {
	let board;
	try {
		board = await eda.dmt_Board.getCurrentBoardInfo();
	}
	catch (err) {
		throw edaError(err, 'Failed to read current Board.');
	}
	return { result: { linked: !!board, board: board ? serializeBoard(board) : null } };
};

/** Create a Board binding a schematic and/or PCB into one group. */
const boardCreate: Handler = async (payload) => {
	const schematicUuid = optionalString(payload, 'schematicUuid');
	const pcbUuid = optionalString(payload, 'pcbUuid');
	if (schematicUuid === undefined && pcbUuid === undefined) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Pass at least one of "schematicUuid" or "pcbUuid".');
	}
	let name;
	try {
		name = await eda.dmt_Board.createBoard(schematicUuid, pcbUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to create Board.');
	}
	if (name === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to create Board (check the schematic/PCB UUIDs).');
	}
	return { result: { boardName: name } };
};

/** Rename a Board by its current name. */
const boardRename: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const newName = requireString(payload, 'newName');
	let ok;
	try {
		ok = await eda.dmt_Board.modifyBoardName(name, newName);
	}
	catch (err) {
		throw edaError(err, 'Failed to rename Board.');
	}
	return { result: { ok } };
};

/** Copy a Board (its schematic + PCB) into a new Board. */
const boardCopy: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	let newName;
	try {
		newName = await eda.dmt_Board.copyBoard(name);
	}
	catch (err) {
		throw edaError(err, 'Failed to copy Board.');
	}
	if (newName === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to copy Board "${name}".`);
	}
	return { result: { boardName: newName } };
};

/** Delete a Board by name. */
const boardDelete: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	let ok;
	try {
		ok = await eda.dmt_Board.deleteBoard(name);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete Board.');
	}
	return { result: { ok } };
};

/**
 * Rebind a Board to the intended schematic + PCB — the deterministic repair for
 * a stale/orphaned binding (the #33 case: a rebuild-from-empty PCB left the Board
 * pointing at a deleted schematic UUID, so `board list` crashed and DRC reported a
 * false Netlist Error).
 *
 * A schematic can belong to only ONE Board in EasyEDA Pro, so we can't just
 * createBoard on top of the old one — the SDK would MOVE the schematic and leave a
 * stale shell (same trap `pcb new-board` documents). We therefore delete the old
 * Board (by name) FIRST, then createBoard(schematic, pcb) fresh. The old binding's
 * schematic/pcb UUIDs are captured beforehand so a failed re-create rolls back to
 * the original Board instead of leaving the project board-less.
 *
 * GUARDRAIL: if the target schematic is bound to a DIFFERENT board, refuse unless
 * force=true (rebinding would silently steal it).
 */
const boardRebind: Handler = async (payload) => {
	const schematicUuid = requireString(payload, 'schematicUuid');
	const pcbUuid = optionalString(payload, 'pcbUuid');
	const name = optionalString(payload, 'name');
	const force = optionalBoolean(payload, 'force') === true;

	let boards: Array<BoardItem>;
	try {
		boards = (await eda.dmt_Board.getAllBoardsInfo()) ?? [];
	}
	catch (err) {
		throw edaError(err, 'Failed to list Boards for rebind.');
	}

	// Locate the board to rebind: by name if given, else the current board.
	let target: BoardItem | undefined;
	if (name) {
		target = boards.find(b => b?.name === name);
	}
	else {
		try { target = (await eda.dmt_Board.getCurrentBoardInfo()) ?? undefined; }
		catch { /* none */ }
	}

	// Refuse to steal a schematic already bound to a DIFFERENT board (unless force).
	if (!force) {
		const holder = boards.find(b => b?.schematic?.uuid === schematicUuid && b?.name !== target?.name);
		if (holder) {
			throw new ActionError(
				ErrorCodes.INVALID_STATE,
				`Schematic ${schematicUuid} is already bound to board "${holder.name}". `
				+ `Rebinding here would MOVE it out of "${holder.name}" (a schematic can belong to only one board). `
				+ `Pass force=true only if you really want to move it.`,
			);
		}
	}

	// Capture the old binding for rollback, then delete the stale board (best-effort:
	// a missing target just means we create fresh).
	const oldName = target?.name;
	const oldSchematicUuid = target?.schematic?.uuid;
	const oldPcbUuid = target?.pcb?.uuid;
	if (oldName) {
		try { await eda.dmt_Board.deleteBoard(oldName); }
		catch (err) { throw edaError(err, `Failed to delete the stale Board "${oldName}" before rebinding.`); }
	}

	// Create the fresh binding.
	let newName: string | undefined;
	try { newName = await eda.dmt_Board.createBoard(schematicUuid, pcbUuid); }
	catch (err) {
		// Roll back to the original binding so we don't leave the project board-less.
		if (oldSchematicUuid || oldPcbUuid) {
			try { await eda.dmt_Board.createBoard(oldSchematicUuid, oldPcbUuid); } catch { /* best-effort */ }
		}
		throw edaError(err, 'Failed to create the rebound Board (rolled back to the previous binding).');
	}
	if (!newName) {
		if (oldSchematicUuid || oldPcbUuid) {
			try { await eda.dmt_Board.createBoard(oldSchematicUuid, oldPcbUuid); } catch { /* best-effort */ }
		}
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'createBoard returned nothing (check the schematic/PCB UUIDs); rolled back to the previous binding.');
	}

	// Restore the desired board name (createBoard mints an auto name).
	const wantName = name ?? oldName;
	if (wantName && wantName !== newName) {
		try { await eda.dmt_Board.modifyBoardName(newName, wantName); newName = wantName; }
		catch { /* keep the auto name */ }
	}

	return {
		result: {
			boardName: newName,
			schematicUuid,
			pcbUuid: pcbUuid ?? null,
			replaced: oldName
				? { name: oldName, schematicUuid: oldSchematicUuid ?? null, pcbUuid: oldPcbUuid ?? null }
				: null,
		},
	};
};

/**
 * Create a NEW board (板) that CONTAINS a fresh, empty PCB, bound to a schematic —
 * the programmatic equivalent of the UI's 新建 PCB / 原理图转 PCB. `board.create`
 * only mints the schematic↔PCB *linkage*; this makes an actual new PCB page you can
 * switch to and `pcb import-changes` into.
 *
 * The SDK needs TWO steps IN ORDER (discovered live — createPcb is a silent no-op on
 * a board name that doesn't exist yet, which is why every one-shot attempt returned
 * undefined):
 *   1. createBoard(schematicUuid) → mints a board *shell* bound to that schematic.
 *   2. createPcb(boardName)       → adds the PCB INTO that now-existing board.
 * On step-2 failure we roll back the empty shell so no PCB-less board is left behind.
 *
 * GUARDRAIL: a schematic can belong to only ONE Board in EasyEDA Pro. Calling
 * createBoard(schematicUuid) on an ALREADY-BOUND schematic silently MOVES it into
 * the new board, leaving the old board with just its PCB (bit us: `pcb new-board`
 * stole the schematic → the original board showed "PCB only, 原理图没了"). So we
 * refuse when the schematic is already bound, unless force=true is passed to move
 * it deliberately.
 */
const pcbNewBoard: Handler = async (payload) => {
	let schematicUuid = optionalString(payload, 'schematicUuid') ?? optionalString(payload, 'schematic');
	const force = optionalBoolean(payload, 'force') === true;
	if (!schematicUuid) {
		// default to the current board's schematic, else the first board in the project.
		try { schematicUuid = (await eda.dmt_Board.getCurrentBoardInfo())?.schematic?.uuid; }
		catch { /* none */ }
		if (!schematicUuid) {
			try { schematicUuid = (await eda.dmt_Board.getAllBoardsInfo())?.[0]?.schematic?.uuid; }
			catch { /* none */ }
		}
	}
	if (!schematicUuid) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'No schematic to bind — pass "schematicUuid" (no current board to infer one from).');
	}

	// Refuse to steal a schematic that is already bound to a Board (see GUARDRAIL above).
	if (!force) {
		let boundBoardName: string | undefined;
		try {
			const boards = (await eda.dmt_Board.getAllBoardsInfo()) ?? [];
			boundBoardName = boards.find((b) => b?.schematic?.uuid === schematicUuid)?.name;
		}
		catch { /* best-effort — if we can't read boards, fall through to create */ }
		if (boundBoardName) {
			throw new ActionError(
				ErrorCodes.INVALID_STATE,
				`Schematic ${schematicUuid} is already bound to board "${boundBoardName}". `
				+ `Creating a new board would MOVE it out of "${boundBoardName}" (a schematic can belong to only one board), `
				+ `leaving "${boundBoardName}" with just its PCB. `
				+ `To lay out a fresh PCB for this schematic, work inside "${boundBoardName}"; `
				+ `pass force=true only if you really want to move the schematic into a new board.`,
			);
		}
	}

	let boardName: string | undefined;
	try { boardName = await eda.dmt_Board.createBoard(schematicUuid); }
	catch (err) { throw edaError(err, 'Failed to create the board shell.'); }
	if (!boardName) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'createBoard returned nothing (check the schematicUuid).');
	}

	let pcbUuid: string | undefined;
	try { pcbUuid = await eda.dmt_Pcb.createPcb(boardName); }
	catch (err) {
		try { await eda.dmt_Board.deleteBoard(boardName); } catch { /* best-effort rollback */ }
		throw edaError(err, 'Failed to create the PCB in the new board.');
	}
	if (!pcbUuid) {
		try { await eda.dmt_Board.deleteBoard(boardName); } catch { /* best-effort rollback */ }
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'createPcb returned nothing — this EasyEDA build did not create a PCB (SDK no-op).');
	}

	// optional rename of the new board.
	const wantName = optionalString(payload, 'name');
	if (wantName) {
		try { await eda.dmt_Board.modifyBoardName(boardName, wantName); boardName = wantName; }
		catch { /* keep the auto name */ }
	}

	let pcbName: string | undefined;
	try { pcbName = (await eda.dmt_Pcb.getAllPcbsInfo() ?? []).find((p) => p.uuid === pcbUuid)?.name; }
	catch { /* best-effort */ }

	return { result: { boardName, pcbName, pcbUuid, schematicUuid } };
};

// system.notify — surface a toast INSIDE the EasyEDA window (设计流程步骤通知).
// Non-blocking; the design flow calls it as each stage passes so the user can watch
// progress live ("完成 布线,下一步 铺铜"). type ∈ info|success|warn|error|question.
const systemNotify: Handler = async (payload) => {
	const message = requireString(payload, 'message');
	const raw = (optionalString(payload, 'type') ?? 'info').toLowerCase();
	const kind = raw === 'warning' ? 'warn' : raw;
	const allowed = new Set(['info', 'success', 'warn', 'error', 'question']);
	const t = (allowed.has(kind) ? kind : 'info') as ESYS_ToastMessageType;
	const timer = optionalNumber(payload, 'duration') ?? 3;
	try {
		eda.sys_Message.showToastMessage(message, t, timer);
	}
	catch (err) {
		throw edaError(err, 'Failed to show the notification toast.');
	}
	return { result: { shown: true, message, type: t } };
};

/**
 * Sync the schematic netlist/components into the active PCB (从原理图导入变更) —
 * the primary way components arrive on the board. `importChanges` returns false
 * on a floating PCB, so ensure a Board ties the schematic and PCB together
 * first, then recompute ratlines.
 *
 * ROOT CAUSE of the long-standing "no-op" (#124, corrects #20's diagnosis):
 * importChanges COMPUTES the change list correctly, then surfaces a 确认导入信息
 * modal with an 应用修改 button — and resolves true when the DIALOG opens, not
 * when the changes apply. Headless, nobody ever clicked it, so the API looked
 * like a silent no-op ("no incremental add"). Verified live (2026-07-17,
 * ceshi): clicking 应用修改 landed all 20 components on a cleared board. The
 * handler now waits for that modal and clicks 应用修改 itself (confirm:false
 * opt-out leaves it for manual review), then reports the component count delta.
 */

// The confirm modal follows the editor UI language. Matching only the zh-Hans
// strings made every English-UI import report confirm:'no-dialog' with
// componentsAfter:0 while the dialog sat open unclicked (live: EasyEDA Pro
// 3.2.149 desktop, EN UI — clicking "Apply Changes" by hand landed 12/12 parts).
// Add further UI languages here only with a live-verified string.
export const IMPORT_CONFIRM_DIALOG_TITLES: ReadonlyArray<string> = [
	'确认导入信息',
	'Confirm Importing changes information',
];
export const IMPORT_CONFIRM_APPLY_LABELS: ReadonlyArray<string> = [
	'应用修改',
	'Apply Changes',
];

// importConfirmStepSource builds the DOM probe body for clickImportConfirm.
// Text is compared trimmed, whitespace-collapsed and case-insensitively: the
// title by containment (wrapper nodes carry extra text), the button exactly
// (so "Export Report" / "Cancel" can never be mistaken for the apply button).
export function importConfirmStepSource(
	titles: ReadonlyArray<string> = IMPORT_CONFIRM_DIALOG_TITLES,
	applyLabels: ReadonlyArray<string> = IMPORT_CONFIRM_APPLY_LABELS,
): string {
	const norm = (s: string) => s.replace(/\s+/g, ' ').trim().toLowerCase();
	return `
		const norm = s => String(s || '').replace(/\\s+/g, ' ').trim().toLowerCase();
		const titles = ${JSON.stringify(titles.map(norm))};
		const applyLabels = ${JSON.stringify(applyLabels.map(norm))};
		const modals = Array.from(document.querySelectorAll('.arco-modal, [class*=modal]'))
			.filter(e => e.offsetParent !== null && titles.some(t => norm(e.innerText).includes(t)));
		if (!modals.length) return 'none';
		const btn = modals.flatMap(m => Array.from(m.querySelectorAll('button')))
			.find(b => applyLabels.includes(norm(b.innerText)) && b.offsetParent !== null);
		if (!btn) return 'no-button';
		btn.click();
		return 'clicked';
	`;
}

// clickImportConfirm waits for the 确认导入信息 / "Confirm Importing changes
// information" modal and clicks 应用修改 / "Apply Changes".
// Returns 'applied', 'no-dialog' (import needed no confirmation), or 'no-button'.
//
// DOM access MUST go through an AsyncFunction escape: the extension sandbox
// shadows `document` to undefined in module scope (live-verified INTERNAL_ERROR
// "reading 'querySelectorAll' of undefined"), while a `new AsyncFunction`'s
// scope chain ends at the real global — the exact trick debug.exec_js uses,
// which is why exec_js probes could always see the dialog.
async function clickImportConfirm(timeoutMs: number): Promise<string> {
	const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
		new (body: string): () => Promise<unknown>;
	};
	// NOTE: '[class*=modal]' matches NESTED wrapper nodes — an inner node can
	// carry the 确认导入信息 text without the footer buttons (live-verified
	// 'no-button' miss), so the button search must span ALL matching nodes.
	const step = new AsyncFunction(importConfirmStepSource());
	const pause = (ms: number) => new Promise(r => setTimeout(r, ms));
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		const r = await step();
		if (r === 'clicked') {
			// Wait for the modal to actually close (the apply is async).
			const closeBy = Date.now() + 10_000;
			while (Date.now() < closeBy && (await step()) !== 'none') {
				await pause(250);
			}
			return 'applied';
		}
		if (r === 'no-button') return 'no-button';
		await pause(250);
	}
	return 'no-dialog';
}

const pcbImportChanges: Handler = async (payload) => {
	const schematicUuid = optionalString(payload, 'schematicUuid');
	const ensureBoard = optionalBoolean(payload, 'ensureBoard') !== false;
	const recomputeRatline = optionalBoolean(payload, 'recomputeRatline') !== false;
	const autoConfirm = optionalBoolean(payload, 'confirm') !== false;

	let board;
	try {
		board = await eda.dmt_Board.getCurrentBoardInfo();
	}
	catch { board = undefined; }

	let createdBoard = false;
	if (!board && ensureBoard) {
		let pcbUuid: string | undefined;
		try {
			pcbUuid = (await eda.dmt_Pcb.getCurrentPcbInfo())?.uuid;
		}
		catch { /* best-effort */ }
		try {
			await eda.dmt_Board.createBoard(schematicUuid, pcbUuid);
			board = await eda.dmt_Board.getCurrentBoardInfo();
			createdBoard = !!board;
		}
		catch (err) {
			throw edaError(err, 'Failed to create a Board linking the schematic and PCB.');
		}
	}

	const countComponents = async (): Promise<number> => {
		try { return ((await eda.pcb_PrimitiveComponent.getAll()) ?? []).length; }
		catch { return -1; }
	};
	const componentsBefore = await countComponents();

	// #124: importChanges' resolution semantics are UNRELIABLE around its
	// 确认导入信息 dialog — observed both "resolves true when the dialog opens"
	// and "never resolves" (which serially wedged the connector's whole action
	// queue on the live board). So: fire it WITHOUT awaiting, click 应用修改
	// concurrently, and cap the wait — the component-count delta below is the
	// ground truth either way.
	const importPromise: Promise<boolean> = eda.pcb_Document.importChanges(schematicUuid)
		.catch(() => false);
	let confirmOutcome = 'skipped';
	if (autoConfirm) {
		confirmOutcome = await clickImportConfirm(8_000);
	}
	let imported: boolean | undefined;
	let apiTimedOut = false;
	imported = await Promise.race([
		importPromise,
		new Promise<boolean | undefined>(r => setTimeout(() => r(undefined), 12_000)),
	]);
	if (imported === undefined) {
		apiTimedOut = true;
		imported = confirmOutcome === 'applied'; // the click is what actually lands parts
	}

	if (imported && recomputeRatline) {
		try {
			await eda.pcb_Document.startCalculatingRatline();
		}
		catch { /* best-effort */ }
	}
	// The apply keeps materializing components AFTER the modal closes (live:
	// counted 1 immediately, 20 a few seconds later) — poll until the count is
	// stable across two reads before reporting it as ground truth.
	let componentsAfter = await countComponents();
	if (confirmOutcome === 'applied') {
		const settleBy = Date.now() + 10_000;
		while (Date.now() < settleBy) {
			await new Promise(r => setTimeout(r, 1_000));
			const again = await countComponents();
			if (again === componentsAfter && again > componentsBefore) break;
			componentsAfter = again;
		}
	}

	return {
		result: {
			imported,
			confirm: confirmOutcome,
			apiTimedOut,
			componentsBefore,
			componentsAfter,
			createdBoard,
			// Read schematic/pcb defensively: a Board can legitimately hold only one
			// side (e.g. after a rebuild the schematic ref may be a deleted/orphaned
			// UUID), so `board.schematic.uuid` would crash — mirror serializeBoard.
			board: board
				? { name: board.name, schematicUuid: board.schematic?.uuid ?? null, pcbUuid: board.pcb?.uuid ?? null }
				: null,
			reason: imported
				? (confirmOutcome === 'no-button'
					? 'the 确认导入信息 / "Confirm Importing changes information" dialog is open but its 应用修改 / "Apply Changes" button was not found — stop and inspect the dialog/readback; update the typed import handler before retrying'
					: null)
				: 'importChanges returned false — the PCB may be floating (no linked schematic) or schematicUuid is invalid.',
		},
	};
};

// pcb.component.attrs_backfill — fill the EMPTY otherProperty values on PCB
// components from their DEVICE-LIBRARY records, resolved by LCSC C-number.
//
// Why the library, not the schematic: the platform's sch→PCB import creates
// the attribute KEYS with empty VALUES on the PCB instance (live-verified:
// Value/Voltage Rating/Tolerance/Datasheet/… all "") — blanking the
// 器件标准化 panel's PCB columns — and the SCHEMATIC instance's otherProperty
// values are ALSO empty after save/reload (live-verified: rich right after
// place, empty after reopen), so the schematic is not a usable source either.
// The device-library record (getByLcscIds via the instance's supplierId, which
// DOES survive the import — #157 keeps it a real C-number) is the only stable
// carrier of the full attribute set. Everything runs PCB-foreground; no page
// switching, no lazy-load exposure.
//
// Merge policy: only keys whose PCB value is empty/missing are filled
// (hand-edited PCB values win); overwrite=true forces the library values.
const pcbComponentAttrsBackfill: Handler = async (payload) => {
	const overwrite = optionalBoolean(payload, 'overwrite') === true;

	let comps;
	try { comps = await eda.pcb_PrimitiveComponent.getAll(); }
	catch (err) { throw edaError(err, 'Failed to list PCB components.'); }
	const parts: Array<{ comp: (NonNullable<typeof comps>)[number]; designator: string; lcsc: string }> = [];
	const noLcsc: Array<string> = [];
	for (const comp of comps ?? []) {
		let designator = '';
		let lcsc = '';
		try {
			designator = String(comp.getState_Designator() ?? '');
			lcsc = String(comp.getState_SupplierId() ?? '');
		}
		catch { continue; }
		if (!designator) continue;
		if (/^C\d+$/.test(lcsc)) parts.push({ comp, designator, lcsc });
		else noLcsc.push(designator);
	}

	// Resolve each distinct C-number to its device-library attribute set.
	const attrsByLcsc = new Map<string, Record<string, unknown>>();
	const distinct = [...new Set(parts.map(p => p.lcsc))];
	for (let i = 0; i < distinct.length; i += 20) {
		const batch = distinct.slice(i, i + 20);
		let raw: Array<Record<string, unknown>> = [];
		try { raw = (await eda.lib_Device.getByLcscIds(batch)) as unknown as Array<Record<string, unknown>>; }
		catch { /* batch is best-effort; unresolved parts are reported below */ }
		for (const r of Array.isArray(raw) ? raw : []) {
			const id = String(r.supplierId ?? (r.otherProperty as Record<string, unknown> | undefined)?.['Supplier Part'] ?? '');
			const op = r.otherProperty;
			if (/^C\d+$/.test(id) && op && typeof op === 'object') attrsByLcsc.set(id, op as Record<string, unknown>);
		}
	}

	// Projected-state keys are never merged in — see PROJECTED_STATE_KEYS.
	const projectedStateKeys = PROJECTED_STATE_KEYS;

	const updated: Array<{ designator: string; lcsc: string; filledKeys: Array<string> }> = [];
	const unresolved: Array<string> = [];
	for (const { comp, designator, lcsc } of parts) {
		const source = attrsByLcsc.get(lcsc);
		if (!source) { unresolved.push(designator); continue; }
		let current: Record<string, unknown> = {};
		try { current = (comp.getState_OtherProperty() as Record<string, unknown>) ?? {}; }
		catch { /* treat as empty */ }
		const filled: Array<string> = [];
		const merged: Record<string, unknown> = { ...current };
		for (const [key, value] of Object.entries(source)) {
			if (projectedStateKeys.has(key)) continue;
			if (value === undefined || value === null || value === '') continue;
			const existing = current[key];
			if (!overwrite && existing !== undefined && existing !== null && existing !== '') continue;
			if (existing === value) continue;
			merged[key] = value;
			filled.push(key);
		}
		// Scrub a placeholder Designator that an OLDER backfill already leaked into
		// the instance — leaving it in place would re-wipe the designator on any
		// future whole-otherProperty write.
		if (typeof merged['Designator'] === 'string' && merged['Designator'].includes('?')) {
			delete merged['Designator'];
			filled.push('Designator (stale placeholder removed)');
		}
		if (!filled.length) continue;
		try {
			// Re-assert the real designator in the same call — belt and braces so
			// this write can never be the one that resets it.
			await eda.pcb_PrimitiveComponent.modify(comp.getState_PrimitiveId(), { designator, otherProperty: merged });
			updated.push({ designator, lcsc, filledKeys: filled.sort() });
		}
		catch { /* best-effort per component; report only successful fills */ }
	}
	return {
		result: {
			updatedCount: updated.length,
			partsWithLcsc: parts.length,
			updated,
			...(unresolved.length ? { unresolvedDesignators: unresolved } : {}),
			...(noLcsc.length ? { noLcscDesignators: noLcsc } : {}),
		},
	};
};

// pcb.add_component — place a footprint on the PCB and CONNECT it, bypassing the
// broken eda.pcb_Document.importChanges (which no-ops for API-added parts even
// when they're in the netlist with a designator + footprint — see #20). Steps:
//   1. create the footprint on the PCB (pcb_PrimitiveComponent.create)
//   2. link it to its schematic twin: same uniqueId + designator (modify)
//   3. assign each pad's net from the caller-supplied `nets` map (padNumber→net)
//      via pcb_PrimitivePad.modify — this is what actually wires the part, since
//      net→pad assignment is otherwise part of the broken import flow
//   4. recompute ratlines so the new connections render
// The caller supplies `nets` (it already has the pin→net from `sch read`) because
// the netlist (getNetlistFile) is only readable while the SCHEMATIC is active,
// and this handler runs with the PCB active.
const pcbAddComponent: Handler = async (payload) => {
	const dev = payload.device as { libraryUuid?: string; uuid?: string } | undefined;
	const libraryUuid = optionalString(payload, 'libraryUuid') ?? dev?.libraryUuid;
	const uuid = optionalString(payload, 'uuid') ?? dev?.uuid;
	if (!libraryUuid || !uuid) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'device is required: pass libraryUuid + uuid (a device {libraryUuid, uuid}).');
	}
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfComponent;
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const rotation = optionalNumber(payload, 'rotation');
	const designator = optionalString(payload, 'designator');
	const uniqueId = optionalString(payload, 'uniqueId');
	const nets = (payload.nets && typeof payload.nets === 'object') ? payload.nets as Record<string, string> : {};

	// 1. Create the footprint on the PCB.
	let comp;
	try {
		comp = await eda.pcb_PrimitiveComponent.create({ libraryUuid, uuid }, layer, x, y, rotation, false);
	}
	catch (err) {
		throw edaError(err, 'Failed to place the component footprint on the PCB.');
	}
	if (!comp) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'PCB component create returned no primitive (check device uuid / layer).');
	}
	const id = comp.getState_PrimitiveId();

	// 2. Link to the schematic twin (uniqueId is the sch↔PCB key; designator pairs them).
	const prop: Record<string, unknown> = {};
	if (designator) prop.designator = designator;
	if (uniqueId) prop.uniqueId = uniqueId;
	if (Object.keys(prop).length) {
		try { await eda.pcb_PrimitiveComponent.modify(id, prop); }
		catch { /* link is best-effort — connectivity comes from the pad nets below */ }
	}

	// 3. Assign each pad's net from the supplied map.
	let assignedNets = 0;
	const unmatched: Array<string> = [];
	let pads: Array<PcbPad> = [];
	try { pads = (await eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId(id)) ?? []; }
	catch { pads = []; }
	for (const p of pads) {
		const num = String(p.getState_PadNumber?.() ?? '');
		const net = num ? nets[num] : undefined;
		if (net) {
			try { await eda.pcb_PrimitivePad.modify(p.getState_PrimitiveId(), { net }); assignedNets++; }
			catch { unmatched.push(num); }
		}
	}

	// 3b. Embedded-via net assignment (#118): a footprint can EMBED vias — QFN
	// EPAD thermal vias are the canonical case. create() leaves them net:"", so
	// the EPAD never bonds to the GND plane (heat + ground both dead) and DRC
	// reports "Clearance Error in the same footprint (SMD Pad to Via)" per via.
	// The primitive API cannot DELETE them (#120: footprint-embedded vias
	// survive delete with ok:true) but CAN modify them (@beta) — so give every
	// netless via sitting inside one of this component's just-assigned pad
	// rects that pad's net. Verified by readback (the #120 lesson: SDK booleans
	// lie); best-effort throughout — a failure never fails the place.
	let embeddedVias: { assigned: number; verified: number; failed: Array<string> } | undefined;
	try {
		type PadRect = { net: string; minX: number; minY: number; maxX: number; maxY: number };
		const rects: Array<PadRect> = [];
		for (const p of pads) {
			const num = String(p.getState_PadNumber?.() ?? '');
			const net = num ? nets[num] : undefined;
			if (!net) continue;
			const ext = padExtent(p);
			if (!ext) continue;
			const px = p.getState_X(), py = p.getState_Y();
			rects.push({ net, minX: px - ext.width / 2, minY: py - ext.height / 2, maxX: px + ext.width / 2, maxY: py + ext.height / 2 });
		}
		if (rects.length) {
			const allVias = (await eda.pcb_PrimitiveVia.getAll()) ?? [];
			const wanted = new Map<string, string>(); // viaId → net
			for (const v of allVias) {
				if (String(v.getState_Net?.() ?? '').trim() !== '') continue;
				const vx = v.getState_X(), vy = v.getState_Y();
				const hit = rects.find(r => vx >= r.minX && vx <= r.maxX && vy >= r.minY && vy <= r.maxY);
				if (hit) wanted.set(v.getState_PrimitiveId(), hit.net);
			}
			if (wanted.size) {
				const failed: Array<string> = [];
				for (const [vid, net] of wanted) {
					try { await eda.pcb_PrimitiveVia.modify(vid, { net }); }
					catch { failed.push(vid); }
				}
				let verified = 0;
				try {
					const after = (await eda.pcb_PrimitiveVia.getAll()) ?? [];
					for (const v of after) {
						const want = wanted.get(v.getState_PrimitiveId());
						if (want && String(v.getState_Net?.() ?? '') === want) verified++;
					}
				}
				catch { verified = -1; } // readback unavailable — assigned count stands, unverified
				embeddedVias = { assigned: wanted.size - failed.length, verified, failed };
			}
		}
	}
	catch { /* best-effort — embedded-via bonding must never fail the place itself */ }

	// 4. Recompute ratlines so the connections show.
	try { await eda.pcb_Document.startCalculatingRatline(); }
	catch { /* best-effort */ }

	// Rebuild-flow guardrail (#33): if the active PCB's Board binding points at a
	// schematic UUID that no longer matches any open schematic doc, adding parts
	// won't clear the resulting DRC Netlist Error until the Board is rebound. Warn
	// so the agent knows to run `board rebind`. Best-effort, never fails the place.
	const warnings: Array<string> = [];
	try {
		const board = await eda.dmt_Board.getCurrentBoardInfo();
		const boundSchUuid = board?.schematic?.uuid;
		if (boundSchUuid) {
			const schematics = (await eda.dmt_Schematic.getAllSchematicsInfo()) ?? [];
			if (!schematics.some(s => s.uuid === boundSchUuid)) {
				warnings.push(
					`Board "${board?.name}" is bound to schematic ${boundSchUuid}, which is not among the open schematics `
					+ `— DRC may report a false Netlist Error. Run \`easyeda board rebind --schematic <uuid> --pcb <uuid>\` to repair the binding.`,
				);
			}
		}
	}
	catch { /* best-effort — diagnostic only */ }

	return {
		result: {
			primitiveId: id,
			designator: comp.getState_Designator?.() ?? designator ?? null,
			uniqueId: comp.getState_UniqueId?.() ?? uniqueId ?? null,
			padCount: (pads ?? []).length,
			assignedNets,
			unmatchedPads: unmatched,
			...(embeddedVias ? { embeddedVias } : {}),
		},
		...(warnings.length ? { warnings } : {}),
	};
};

// ─── Freerouting round-trip (task #5) ───────────────────────────────────
// EasyEDA's own routing extensions (eext-freerouting/kirouting) do NOT call the
// @alpha pcb_Document.autoRouting; they round-trip a Specctra DSN to an external
// engine and import the routed SES. We mirror that with typed actions: export the
// DSN, hand it to easyeda-pcb-router (Freerouting headless), import the SES.

// ── DSN keep-out injection ───────────────────────────────────────────
// `getDsnFile` drops `pcb_PrimitiveRegion` keep-out (the DSN (structure) keeps
// only boundary + rules + layers), so an exported DSN has zero keepout and an
// external router (Freerouting) would route under the antenna. We splice the
// regions back in as Specctra `(keepout (polygon …))`.
//
// Transform EasyEDA→DSN is a PURE TRANSLATION, 1:1 mil, no flip (verified against
// pad coordinates): dsn = easyeda + offset, where offset = DSN-boundary-min −
// outline-bbox-min. (The bbox includes the outline's half-linewidth, so the offset
// can be off by ≤ that — negligible for a keep-out, which carries margin anyway.)

const DSN_RESOLUTION = 1000; // (resolution mil 1000) → keep ≤3 decimals

function dsnRound(v: number): number {
	return Math.round(v * DSN_RESOLUTION) / DSN_RESOLUTION;
}

// vertsFromPolygonSource walks a [x0,y0,'L',x1,y1,…] source array, collecting the
// number pairs and skipping command tokens ('L'/'A'/…). Arc commands degrade to
// their control points (fine for a margin-carrying keep-out).
function vertsFromPolygonSource(src: unknown): Array<[number, number]> {
	if (!Array.isArray(src)) return [];
	const nums: number[] = [];
	for (const t of src) if (typeof t === 'number') nums.push(t);
	const verts: Array<[number, number]> = [];
	for (let i = 0; i + 1 < nums.length; i += 2) verts.push([nums[i], nums[i + 1]]);
	return verts;
}

// parseDsnBoundaryMin reads the min corner of `(boundary (path <layer> <w> x y …))`.
function parseDsnBoundaryMin(dsn: string): { x: number; y: number } | null {
	const m = dsn.match(/\(\s*boundary\s*\(\s*path\s+\S+\s+[\d.eE+-]+((?:\s+[\d.eE+-]+)+)\s*\)/);
	if (!m) return null;
	const nums = m[1].trim().split(/\s+/).map(Number).filter(n => !Number.isNaN(n));
	let minX = Infinity, minY = Infinity;
	for (let i = 0; i + 1 < nums.length; i += 2) {
		minX = Math.min(minX, nums[i]);
		minY = Math.min(minY, nums[i + 1]);
	}
	return Number.isFinite(minX) && Number.isFinite(minY) ? { x: minX, y: minY } : null;
}

function dsnLayerName(layer: number): string {
	if (layer === 1) return 'TopLayer';
	if (layer === 2) return 'BottomLayer';
	return 'signal'; // MULTI / inner → all signal layers
}

// spliceIntoStructure inserts `block` just before the matching close paren of the
// top-level `(structure …)` form.
function spliceIntoStructure(dsn: string, block: string): string {
	const start = dsn.indexOf('(structure');
	if (start < 0) return dsn;
	let depth = 0;
	for (let i = start; i < dsn.length; i++) {
		if (dsn[i] === '(') depth++;
		else if (dsn[i] === ')') {
			depth--;
			if (depth === 0) return dsn.slice(0, i) + block + '\n  ' + dsn.slice(i);
		}
	}
	return dsn;
}

// injectRegionKeepouts splices every keep-out region (one carrying no-wires/
// no-pours/no-fills — the rules that matter to a router) into the DSN. Pure
// placement regions (no-components only) are skipped (autorouters don't place).
async function injectRegionKeepouts(dsn: string): Promise<{ text: string; count: number }> {
	const regions = await eda.pcb_PrimitiveRegion.getAll();
	if (!regions || !regions.length) return { text: dsn, count: 0 };

	const boundaryMin = parseDsnBoundaryMin(dsn);
	let bboxMin = { x: 0, y: 0 };
	try {
		const polys = await eda.pcb_PrimitivePolyline.getAll(undefined, BOARD_OUTLINE_LAYER);
		if (polys.length) {
			const bb = await eda.pcb_Primitive.getPrimitivesBBox(polys.map(p => p.getState_PrimitiveId()));
			if (bb) bboxMin = { x: bb.minX, y: bb.minY };
		}
	}
	catch { /* outline bbox best-effort → offset falls back to boundary min */ }
	const offX = (boundaryMin ? boundaryMin.x : 0) - bboxMin.x;
	const offY = (boundaryMin ? boundaryMin.y : 0) - bboxMin.y;

	const routingRules = new Set([5, 6, 7]); // no-wires / no-fills / no-pours
	const clauses: string[] = [];
	for (const r of regions) {
		const rules = (r.getState_RuleType() ?? []) as unknown as number[];
		if (!rules.some(v => routingRules.has(v))) continue; // placement-only → skip
		const cp = r.getState_ComplexPolygon() as unknown as { getSource?: () => unknown; polygon?: unknown };
		const src = typeof cp?.getSource === 'function' ? cp.getSource() : cp?.polygon;
		const verts = vertsFromPolygonSource(src);
		if (verts.length < 3) continue;
		const coords = verts.map(([x, y]) => `${dsnRound(x + offX)} ${dsnRound(y + offY)}`).join(' ');
		const name = r.getState_RegionName() || `region_keepout_${clauses.length + 1}`;
		clauses.push(`    (keepout "${name}" (polygon ${dsnLayerName(r.getState_Layer())} 0 ${coords}))`);
	}
	if (!clauses.length) return { text: dsn, count: 0 };
	return { text: spliceIntoStructure(dsn, '\n' + clauses.join('\n')), count: clauses.length };
}

/**
 * Export the PCB as a Specctra DSN (the autorouter input). Read-only. By default
 * splices `pcb_PrimitiveRegion` keep-out back into the DSN (`getDsnFile` drops it,
 * so the router would otherwise route under the antenna) — pass
 * `injectKeepout:false` for the raw EasyEDA export.
 */
const pcbExportDsn: Handler = async (payload) => {
	const fileName = optionalString(payload, 'fileName') ?? 'design.dsn';
	const inject = payload.injectKeepout !== false; // default true
	let file;
	try {
		file = await eda.pcb_ManufactureData.getDsnFile(fileName);
	}
	catch (err) {
		throw edaError(err, 'Failed to export DSN.');
	}
	if (!file) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			'DSN export returned no file — the PCB may be empty or have no nets (run pcb.import_changes first).',
		);
	}

	let text = await file.text();
	let keepouts = 0;
	if (inject) {
		try {
			const injected = await injectRegionKeepouts(text);
			text = injected.text;
			keepouts = injected.count;
		}
		catch { /* injection is best-effort — never break the export over it */ }
	}
	const outFile = new File([text], file.name || fileName, { type: 'text/plain' });
	const artifact = await blobToArtifact(outFile, 'pcb_dsn', file.name || fileName, 'text/plain');
	return { result: { artifactId: artifact.id, fileName: file.name || fileName, size: outFile.size, keepouts }, artifacts: [artifact] };
};

/**
 * Import a routed-result file from the autorouter. `format: 'ses'` (Specctra
 * Session, default) or `'json'` (EasyEDA autoroute JSON). The file arrives as
 * base64 (the connector can't read the daemon's disk). Mutates the PCB.
 */
const pcbImportAutoroute: Handler = async (payload) => {
	const format = (optionalString(payload, 'format') ?? 'ses').toLowerCase();
	const base64 = requireString(payload, 'fileBase64');
	const fileName = optionalString(payload, 'fileName')
		?? (format === 'json' ? 'route.json' : 'route.ses');
	let file: File;
	try {
		const bytes = Uint8Array.from(atob(base64), c => c.charCodeAt(0));
		file = new File([bytes], fileName);
	}
	catch (err) {
		throw edaError(err, 'Failed to decode the routed file (expected base64 in fileBase64).');
	}
	let imported;
	try {
		imported = format === 'json'
			? await eda.pcb_Document.importAutoRouteJsonFile(file)
			: await eda.pcb_Document.importAutoRouteSesFile(file);
	}
	catch (err) {
		throw edaError(err, 'Failed to import the autoroute result file.');
	}
	if (imported) {
		try { await eda.pcb_Document.startCalculatingRatline(); }
		catch { /* best-effort ratline refresh */ }
	}
	return {
		result: {
			imported: Boolean(imported),
			format,
			reason: imported ? null : 'importAutoRoute* returned false — wrong format, stale DSN, or net/layer mismatch.',
		},
	};
};

/**
 * Read the PCB canvas filter configuration without changing design data or view
 * state. The current public SDK exposes only this getter; there is no matching
 * setter for the UI's "component attributes" visibility category. Keep the raw
 * object intact so a future, fixture-verified typed setter can restore the whole
 * view exactly instead of guessing individual keys.
 */
const pcbViewFilterGet: Handler = async () => {
	const documentApi = eda.pcb_Document as unknown as {
		getCurrentFilterConfiguration?: () => Promise<Record<string, unknown> | undefined>;
	};
	if (typeof documentApi.getCurrentFilterConfiguration !== 'function') {
		throw new ActionError(
			ErrorCodes.EDA_API_UNAVAILABLE,
			'This EasyEDA host does not expose pcb_Document.getCurrentFilterConfiguration(). No view state was changed.',
		);
	}
	let configuration: Record<string, unknown> | undefined;
	try {
		configuration = await documentApi.getCurrentFilterConfiguration();
	}
	catch (err) {
		throw edaError(err, 'Failed to read the PCB canvas filter configuration.');
	}
	if (!configuration || typeof configuration !== 'object' || Array.isArray(configuration)) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			'PCB canvas filter getter returned no configuration. No view state was changed.',
		);
	}
	return {
		result: {
			configuration,
			readable: true,
			writable: false,
			componentAttributesVisible: null,
			componentAttributesPath: null,
			api: {
				getter: 'eda.pcb_Document.getCurrentFilterConfiguration',
				setter: null,
			},
			note: 'The public SDK has no filter setter. Do not modify pcb_PrimitiveAttribute visibility as a workaround because that changes design data.',
		},
	};
};

type PcbSnapshotFitMode = 'board' | 'all' | 'none';

function pcbSnapshotFitMode(payload: Record<string, unknown>): PcbSnapshotFitMode {
	const requested = optionalString(payload, 'fitMode');
	if (requested !== undefined) {
		if (requested === 'board' || requested === 'all' || requested === 'none') return requested;
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			'"fitMode" must be "board", "all", or "none".',
		);
	}
	// Compatibility for callers written before fitMode existed. Explicit
	// fit=true keeps its historical zoom-to-all meaning; a bare action now uses
	// the tighter board-outline fit needed by Layout review.
	const legacyFit = optionalBoolean(payload, 'fit');
	if (legacyFit !== undefined) return legacyFit ? 'all' : 'none';
	return 'board';
}

async function fitPcbSnapshotViewport(requested: PcbSnapshotFitMode): Promise<{
	requested: PcbSnapshotFitMode;
	applied: PcbSnapshotFitMode;
	fitted: boolean;
	api: string | null;
	fallbackReason?: string;
}> {
	if (requested === 'none') return { requested, applied: 'none', fitted: false, api: null };
	if (requested === 'board') {
		try {
			const ok = await eda.pcb_Document.zoomToBoardOutline();
			if (ok !== false) {
				return { requested, applied: 'board', fitted: true, api: 'eda.pcb_Document.zoomToBoardOutline' };
			}
		}
		catch { /* fall back to the older public fit API below */ }
	}
	try {
		await eda.dmt_EditorControl.zoomToAllPrimitives();
		return {
			requested,
			applied: 'all',
			fitted: true,
			api: 'eda.dmt_EditorControl.zoomToAllPrimitives',
			...(requested === 'board' ? { fallbackReason: 'zoomToBoardOutline unavailable or returned false' } : {}),
		};
	}
	catch {
		return {
			requested,
			applied: 'none',
			fitted: false,
			api: null,
			fallbackReason: `${requested} fit failed; captured the current viewport`,
		};
	}
}

/**
 * Capture the active PCB canvas as a PNG artifact. This combines the public
 * board/all fit APIs with `dmt_EditorControl.getCurrentRenderedAreaImage`.
 * It remains a viewport capture: the editor's internal Copy-as-PNG/SVG object
 * exporter is not exposed by public `eda.*` and must not be claimed here.
 */
const pcbSnapshot: Handler = async (payload) => {
	const tabId = optionalString(payload, 'tabId');
	const fitMode = pcbSnapshotFitMode(payload);
	// Optional sha256 of the PREVIOUS snapshot (caller threads it back in). When
	// present we can DETECT a stale frame ourselves (issue #31) instead of only
	// emitting advisory text: if the viewport changed but the image bytes are
	// byte-identical, the capture is stale — we force a redraw + retry once.
	const previousSha = optionalString(payload, 'previousSha256');
	let fitState = await fitPcbSnapshotViewport(fitMode);
	// Let any pending viewport change (a preceding `view region`/`view zoom`, or
	// the requested fit above) commit + repaint before we read the frame.
	await waitForCanvasSettle();

	const capture = async (): Promise<Blob> => {
		let b;
		try {
			b = await eda.dmt_EditorControl.getCurrentRenderedAreaImage(tabId);
		}
		catch (err) {
			throw edaError(err, 'Failed to capture PCB snapshot.');
		}
		if (!b) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'PCB snapshot returned no image.');
		}
		return b;
	};

	let blob = await capture();
	let sha256 = await blobSha256(blob);
	// Built-in stale detection: if the caller told us the prior frame's sha and we
	// got the exact same bytes back, the canvas almost certainly didn't repaint —
	// force a redraw (ratline recompute + repeat the requested fit) and recapture once.
	let staleRetry = false;
	if (previousSha && sha256 && sha256 === previousSha) {
		staleRetry = true;
		// Stronger redraw nudge than schematic's re-settle: a ratline recompute +
		// re-fit reliably forces EasyEDA to repaint the PCB canvas.
		try { await eda.pcb_Document.startCalculatingRatline(); }
		catch { /* best-effort redraw nudge */ }
		fitState = await fitPcbSnapshotViewport(fitMode);
		await waitForCanvasSettle();
		blob = await capture();
		sha256 = await blobSha256(blob);
	}
	const stale = Boolean(previousSha && sha256 && sha256 === previousSha);

	const artifact = await blobToArtifact(blob, 'pcb_snapshot', 'pcb-snapshot.png', 'image/png');
	return {
		result: {
			artifactId: artifact.id,
			fitted: fitState.fitted,
			fitModeRequested: fitState.requested,
			fitModeApplied: fitState.applied,
			fitApi: fitState.api,
			fitFallbackReason: fitState.fallbackReason ?? null,
			captureKind: fitState.applied === 'board'
				? 'board-fitted-viewport-png'
				: fitState.applied === 'all'
					? 'all-primitives-fitted-viewport-png'
					: 'current-viewport-png',
			objectLevelExport: false,
			sha256,
			stale,
			staleRetry,
			capturedAt: new Date().toISOString(),
			staleHint: 'EasyEDA may not auto-redraw after API edits. Thread this sha256 back as previousSha256 on the next snapshot to auto-detect a stale frame; judge state by data (pcb list/drc), screenshot for layout only.',
		},
		artifacts: [artifact],
	};
};

// ─── pcb.component.modify patch contract (issue #174) ────────────────────
// The REAL field the platform reads for lock state is `primitiveLock` — but
// our own readback (serializePcbComponent / `pcb list`) reports it as `locked`,
// so callers naturally write `{"locked":false}` … which the platform silently
// IGNORES as an unknown key and still returns a component object (= fake
// success: 22/22 "ok" unlocks that survived a reload as locked). Two defenses:
// alias-normalize the natural spellings onto the real key, and REJECT any key
// outside the documented modify() contract instead of letting it no-op.

/** patch key → the serializePcbComponent field that reads it back (null = not
 *  exposed by the serializer, so the write cannot be verified). */
export const PCB_COMPONENT_PATCH_READBACK: Record<string, string | null> = {
	layer: 'layer',
	x: 'x',
	y: 'y',
	rotation: 'rotation',
	primitiveLock: 'locked',
	addIntoBom: 'addIntoBom',
	designator: 'designator',
	name: 'name',
	uniqueId: 'uniqueId',
	manufacturerId: 'manufacturerId',
	supplierId: 'supplierId',
	manufacturer: null,
	supplier: null,
	otherProperty: null,
};

/** Natural spellings accepted for the awkward official key names. */
export const PCB_COMPONENT_PATCH_ALIASES: Record<string, string> = {
	locked: 'primitiveLock',
	lock: 'primitiveLock',
};

/**
 * Normalize a pcb.component.modify patch: map aliases onto the official keys
 * and reject unknown keys (the platform ignores them WITHOUT erroring — the
 * root cause of the #174 fake success).
 */
export function normalizePcbComponentPatch(raw: Record<string, unknown>): Record<string, unknown> {
	const out: Record<string, unknown> = {};
	const unknown: Array<string> = [];
	for (const [rawKey, value] of Object.entries(raw)) {
		const key = PCB_COMPONENT_PATCH_ALIASES[rawKey] ?? rawKey;
		if (!(key in PCB_COMPONENT_PATCH_READBACK)) {
			unknown.push(rawKey);
			continue;
		}
		if (key in out && out[key] !== value) {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Patch sets "${key}" twice with conflicting values (an alias like "locked" maps onto "primitiveLock").`,
			);
		}
		out[key] = value;
	}
	if (unknown.length > 0) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Unknown patch field(s): ${unknown.join(', ')}. The platform silently ignores unknown keys and still `
			+ `reports success (#174), so they are rejected here. Valid keys: `
			+ `${Object.keys(PCB_COMPONENT_PATCH_READBACK).join(', ')} (aliases: locked/lock → primitiveLock).`,
		);
	}
	if (Object.keys(out).length === 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Patch object is empty — nothing to modify.');
	}
	return out;
}

export interface PcbPatchVerification {
	/** patch keys the fresh readback confirms. */
	applied: Array<string>;
	/** patch keys the readback contradicts — the write did NOT stick. */
	notApplied: Array<{ field: string; expected: unknown; actual: unknown }>;
	/** patch keys the serializer cannot read back (manufacturer/supplier/otherProperty, or a non-numeric layer literal). */
	unverified: Array<string>;
}

const normDeg = (v: number): number => ((v % 360) + 360) % 360;

/**
 * Compare a normalized patch against a FRESH readback record (#174). The
 * object returned by modify() — and even getState_* on the object you just
 * wrote — can echo the input, so the caller must re-pull before verifying.
 */
export function verifyPcbComponentPatch(
	patch: Record<string, unknown>,
	readback: Record<string, unknown>,
): PcbPatchVerification {
	const v: PcbPatchVerification = { applied: [], notApplied: [], unverified: [] };
	for (const [field, expected] of Object.entries(patch)) {
		const readKey = PCB_COMPONENT_PATCH_READBACK[field];
		if (readKey === null || readKey === undefined) {
			v.unverified.push(field);
			continue;
		}
		const actual = readback[readKey];
		let ok: boolean | null;
		if (field === 'layer' && typeof expected !== 'number') {
			// The CLI historically accepts layer literals like "BOTTOM"; the readback
			// is numeric, and we have no trusted name→id table here — don't guess.
			ok = null;
		}
		else if (typeof expected === 'number' && typeof actual === 'number') {
			ok = field === 'rotation'
				? Math.abs(normDeg(expected) - normDeg(actual)) < 1e-3
				: Math.abs(expected - actual) < 1e-3;
		}
		else if (expected === null) {
			// modify() documents null as "leave blank" — an empty readback matches.
			ok = actual === null || actual === undefined || actual === '';
		}
		else {
			ok = actual === expected;
		}
		if (ok === null) v.unverified.push(field);
		else if (ok) v.applied.push(field);
		else v.notApplied.push({ field, expected, actual });
	}
	return v;
}

/**
 * Lay out a component on the active PCB: move/rotate/flip-layer/lock or set
 * designator/BOM flags. Mirrors schematic.component.modify against pcb_*.
 *
 * #174 hardening: the patch is normalized (unknown keys hard-error instead of
 * silently no-opping), and every write is verified against a FRESH readback.
 * A lock write that modify() drops is retried through the
 * setState_PrimitiveLock + done() path (the one pcb.track.lock relies on).
 * Partial application follows the #151 convention: once the canvas changed we
 * never throw — the result carries applied/notApplied; only a full no-op errors.
 */
export const pcbComponentModify: Handler = async (payload) => {
	const primitiveId = requireString(payload, 'primitiveId');
	const rawPatch = payload.patch;
	if (typeof rawPatch !== 'object' || rawPatch === null || Array.isArray(rawPatch)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required object field "patch".');
	}
	const patch = normalizePcbComponentPatch(rawPatch as Record<string, unknown>);

	let component;
	try {
		component = await eda.pcb_PrimitiveComponent.modify(
			primitiveId,
			patch as Parameters<typeof eda.pcb_PrimitiveComponent.modify>[1],
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to modify PCB component.');
	}
	if (!component) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to modify PCB component "${primitiveId}".`);
	}

	// Fresh re-pull — the returned object can echo the input (#174).
	const freshRead = async (): Promise<Record<string, unknown> | null> => {
		try {
			const c = await eda.pcb_PrimitiveComponent.get(primitiveId);
			return c ? serializePcbComponent(c) : null;
		}
		catch { return null; }
	};
	let readback = await freshRead();
	let verification = readback ? verifyPcbComponentPatch(patch, readback) : null;

	// Known fake-success path: modify() drops the lock write. Fall back to the
	// setState + done() write path (done() is what commits pending state, #134).
	let lockFallback = false;
	if (verification?.notApplied.some(f => f.field === 'primitiveLock')) {
		lockFallback = true;
		try {
			const c = await eda.pcb_PrimitiveComponent.get(primitiveId);
			if (c) {
				c.setState_PrimitiveLock(patch.primitiveLock === true);
				await c.done();
			}
		}
		catch { /* the re-verify below reports the outcome */ }
		readback = (await freshRead()) ?? readback;
		verification = readback ? verifyPcbComponentPatch(patch, readback) : verification;
	}

	if (verification && verification.applied.length === 0 && verification.notApplied.length > 0) {
		// NOTHING stuck — the canvas is unchanged, so a hard error is safe (#151).
		const detail = verification.notApplied
			.map(f => `${f.field}: wrote ${JSON.stringify(f.expected)}, read back ${JSON.stringify(f.actual)}`)
			.join('; ');
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`modify reported success but the readback shows no patched field was applied (${detail}). `
			+ `The platform accepts and drops such writes without erroring (#174).`,
		);
	}

	const result: Record<string, unknown> = {
		component: readback ?? serializePcbComponent(component),
		verified: verification !== null && verification.notApplied.length === 0,
	};
	if (verification) {
		result.applied = verification.applied;
		if (verification.notApplied.length > 0) result.notApplied = verification.notApplied;
		if (verification.unverified.length > 0) result.unverified = verification.unverified;
	}
	if (lockFallback) result.lockFallback = true;
	return { result };
};

/** Pure classifier for the lock readback (unit-testable without the eda runtime). */
export function classifyLockReadback(
	written: Array<string>,
	freshLockById: Map<string, boolean>,
	locked: boolean,
): { applied: Array<string>; notApplied: Array<string> } {
	const applied: Array<string> = [];
	const notApplied: Array<string> = [];
	for (const id of written) {
		if (freshLockById.get(id) === locked) applied.push(id);
		else notApplied.push(id);
	}
	return { applied, notApplied };
}

/**
 * Batch lock/unlock PCB components (issue #174). Dedicated write path via
 * setState_PrimitiveLock + done() — the pattern pcb.track.lock proved out —
 * followed by a fresh readback so a dropped write can never report success.
 * Partial application follows #151: some-applied returns ok with a structured
 * notApplied list; a full no-op (nothing applied, nothing already in state)
 * throws.
 */
export const pcbComponentLock: Handler = async (payload) => {
	const locked = optionalBoolean(payload, 'locked') ?? true;
	const rawIds = payload.primitiveIds;
	let ids: Array<string>;
	if (typeof rawIds === 'string') ids = [rawIds];
	else if (Array.isArray(rawIds) && rawIds.every(id => typeof id === 'string') && rawIds.length > 0) {
		ids = [...new Set(rawIds as Array<string>)];
	}
	else {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "primitiveIds" (string or non-empty string[]).',
		);
	}

	let comps: Array<PcbComponent>;
	try {
		comps = await eda.pcb_PrimitiveComponent.get(ids);
	}
	catch (err) {
		throw edaError(err, 'Failed to read the components to lock/unlock.');
	}
	const byId = new Map((comps ?? []).map(c => [c.getState_PrimitiveId(), c]));
	const missing = ids.filter(id => !byId.has(id));
	if (byId.size === 0) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			`None of the ${ids.length} primitiveId(s) exist on the active PCB — pull fresh ids via pcb.components.list.`,
		);
	}

	const alreadyInState: Array<string> = [];
	const written: Array<string> = [];
	const writeFailed: Array<string> = [];
	for (const [id, c] of byId) {
		try {
			if (c.getState_PrimitiveLock() === locked) {
				alreadyInState.push(id);
				continue;
			}
			c.setState_PrimitiveLock(locked);
			await c.done(); // pending state does not hit the canvas without done() (#134)
			written.push(id);
		}
		catch {
			writeFailed.push(id);
		}
	}

	// Fresh readback — never trust the object we just wrote through (#174).
	const freshLockById = new Map<string, boolean>();
	if (written.length > 0) {
		let fresh: Array<PcbComponent> = [];
		try { fresh = await eda.pcb_PrimitiveComponent.get(written); }
		catch { fresh = []; }
		for (const c of fresh ?? []) freshLockById.set(c.getState_PrimitiveId(), c.getState_PrimitiveLock());
	}
	const { applied, notApplied } = classifyLockReadback(written, freshLockById, locked);
	notApplied.push(...writeFailed);

	if (applied.length === 0 && alreadyInState.length === 0 && notApplied.length > 0) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`The ${locked ? 'lock' : 'unlock'} write did not stick on any of the ${notApplied.length} component(s) `
			+ `(readback still reports locked=${!locked}). Nothing changed on the canvas.`,
		);
	}

	return {
		result: {
			locked,
			requested: ids.length,
			applied,
			alreadyInState,
			notApplied,
			missing,
			verified: notApplied.length === 0,
		},
	};
};

/**
 * Delete PCB component primitives. No programmatic undo — the Skill snapshots
 * before/after and confirmation-gates this.
 */
const pcbComponentDelete: Handler = async (payload) => {
	const primitiveIds = payload.primitiveIds;
	if (
		!(typeof primitiveIds === 'string')
		&& !(Array.isArray(primitiveIds) && primitiveIds.every(id => typeof id === 'string'))
	) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "primitiveIds" (string or string[]).',
		);
	}
	const requested = typeof primitiveIds === 'string' ? [primitiveIds] : primitiveIds;
	if (requested.length === 0 || requested.some(id => !id)) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'PCB component deletion requires non-empty primitive IDs.');
	}
	let components;
	try {
		components = await eda.pcb_PrimitiveComponent.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to verify PCB component IDs before deletion.');
	}
	if (!Array.isArray(components)) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'PCB component inventory unavailable; deletion was not dispatched.');
	}
	const liveIds = new Set(components.map(component => component.getState_PrimitiveId()));
	const missing = [...new Set(requested)].filter(id => !liveIds.has(id));
	if (missing.length > 0) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			`PCB component primitive ID(s) not found: ${missing.join(', ')}. Pull fresh IDs from pcb list; no deletion was dispatched.`);
	}
	let deleted;
	try {
		deleted = await eda.pcb_PrimitiveComponent.delete(primitiveIds);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete PCB components.');
	}
	return { result: { deleted } };
};

// ─── PCB one-shot reset (pcb.page.clear) ─────────────────────────────────
// Symmetric to schematic.page.clear. `pcb.component.delete` leaves routing,
// pours, regions and free silk behind (the board looks empty in components.list
// while copper remains); this enumerates every content class so a board reset is
// actually clean. Preserves LOCKED primitives + the board outline (layer 11) by
// default — the outline is a layout prerequisite (like the sheet in the sch
// clear). Reuses the rip_up copper-layer rule so routing never touches artwork.

/** Selectable clear scopes. Each maps to one or more primitive classes below. */
const PCB_CLEAR_SCOPES = ['components', 'routing', 'copper', 'regions', 'silk'] as const;
type PcbClearScope = (typeof PCB_CLEAR_SCOPES)[number];

const PCB_BOARD_OUTLINE_LAYER = 11;
/** Copper layers: TOP=1, BOTTOM=2, INNER_1..30 = 15..44 (excludes outline 11 + artwork). */
const onPcbCopperLayer = (layer: number): boolean => layer === 1 || layer === 2 || (layer >= 15 && layer <= 44);
/** Silkscreen layers: TOP_SILK=3 / BOTTOM_SILK=4 (mirrors isSilk in pcb.silk.list). */
const isPcbSilkLayer = (layer: number): boolean => layer === PCB_TOP_SILK || layer === PCB_BOTTOM_SILK;

/** Defensive lock read — not every pcb_Primitive* class declares getState_PrimitiveLock. */
function pcbPrimLocked(p: SchPrimitiveLike): boolean {
	const f = (p as { getState_PrimitiveLock?: () => unknown }).getState_PrimitiveLock;
	try { return typeof f === 'function' ? f.call(p) === true : false; }
	catch { return false; }
}
/** Defensive layer read — vias carry no layer; returns NaN when the getter is absent. */
function pcbPrimLayer(p: SchPrimitiveLike): number {
	const f = (p as { getState_Layer?: () => unknown }).getState_Layer;
	try { return typeof f === 'function' ? Number(f.call(p)) : NaN; }
	catch { return NaN; }
}

type PcbClearKind = {
	key: string;
	// Content kinds carry a scope (gated by --only); outline kinds omit it (they
	// are gated by preserveOutline instead, never by the scope set).
	scope?: PcbClearScope;
	getAll: () => Promise<Array<SchPrimitiveLike>>;
	del: (ids: Array<string>) => Promise<boolean>;
	// Membership test: copper-only for routing tracks/arcs, silk-only (layer 3/4)
	// for silk artwork, layer-11 for outline. Lock handling is uniform in the handler.
	filter?: (p: SchPrimitiveLike) => boolean;
};

// Content classes, grouped by scope. `pcb_PrimitiveLine`/`Arc` primitives live on
// three disjoint layer bands so each getAll() is partitioned by filter with NO
// overlap: routing = copper (1/2/15-44), silk = silkscreen (3/4), outline = 11.
// Vias span layers (no layer filter). `pcb_PrimitiveString` is layer-filtered to
// silk (3/4) too — a free copper/doc-layer string is deliberate artwork, kept
// (mirrors the isSilk filter in pcb.silk.list); only silkscreen text is cleared.
const PCB_CLEAR_KINDS: Array<PcbClearKind> = [
	{ key: 'components', scope: 'components', getAll: () => eda.pcb_PrimitiveComponent.getAll(), del: ids => eda.pcb_PrimitiveComponent.delete(ids) },
	{ key: 'tracks', scope: 'routing', getAll: () => eda.pcb_PrimitiveLine.getAll(), del: ids => eda.pcb_PrimitiveLine.delete(ids), filter: p => onPcbCopperLayer(pcbPrimLayer(p)) },
	{ key: 'arcs', scope: 'routing', getAll: () => eda.pcb_PrimitiveArc.getAll(), del: ids => eda.pcb_PrimitiveArc.delete(ids), filter: p => onPcbCopperLayer(pcbPrimLayer(p)) },
	{ key: 'vias', scope: 'routing', getAll: () => eda.pcb_PrimitiveVia.getAll(), del: ids => eda.pcb_PrimitiveVia.delete(ids) },
	{ key: 'pours', scope: 'copper', getAll: () => eda.pcb_PrimitivePour.getAll(), del: ids => eda.pcb_PrimitivePour.delete(ids) },
	{ key: 'fills', scope: 'copper', getAll: () => eda.pcb_PrimitiveFill.getAll(), del: ids => eda.pcb_PrimitiveFill.delete(ids) },
	{ key: 'regions', scope: 'regions', getAll: () => eda.pcb_PrimitiveRegion.getAll(), del: ids => eda.pcb_PrimitiveRegion.delete(ids) },
	{ key: 'silkStrings', scope: 'silk', getAll: () => eda.pcb_PrimitiveString.getAll(), del: ids => eda.pcb_PrimitiveString.delete(ids), filter: p => isPcbSilkLayer(pcbPrimLayer(p)) },
	{ key: 'silkLines', scope: 'silk', getAll: () => eda.pcb_PrimitiveLine.getAll(), del: ids => eda.pcb_PrimitiveLine.delete(ids), filter: p => isPcbSilkLayer(pcbPrimLayer(p)) },
	{ key: 'silkArcs', scope: 'silk', getAll: () => eda.pcb_PrimitiveArc.getAll(), del: ids => eda.pcb_PrimitiveArc.delete(ids), filter: p => isPcbSilkLayer(pcbPrimLayer(p)) },
];

// Board-outline classes (layer 11), deleted only when preserveOutline === false.
// Outline removal bypasses the lock guard — the outline is created LOCKED, so the
// whole point of --no-preserve-outline is to remove it regardless of lock.
const PCB_OUTLINE_KINDS: Array<PcbClearKind> = [
	{ key: 'outlineLines', getAll: () => eda.pcb_PrimitiveLine.getAll(), del: ids => eda.pcb_PrimitiveLine.delete(ids), filter: p => pcbPrimLayer(p) === PCB_BOARD_OUTLINE_LAYER },
	{ key: 'outlineArcs', getAll: () => eda.pcb_PrimitiveArc.getAll(), del: ids => eda.pcb_PrimitiveArc.delete(ids), filter: p => pcbPrimLayer(p) === PCB_BOARD_OUTLINE_LAYER },
	{ key: 'outlinePolylines', getAll: () => eda.pcb_PrimitivePolyline.getAll(), del: ids => eda.pcb_PrimitivePolyline.delete(ids), filter: p => pcbPrimLayer(p) === PCB_BOARD_OUTLINE_LAYER },
];

/**
 * Parse the `only` payload into a validated, de-duplicated scope list. Omitted →
 * all scopes. Accepts a comma-separated string or a string[]. Pure (no `eda`),
 * so it is unit-tested directly. Throws on an unknown scope.
 */
export function parsePcbClearScopes(raw: unknown): Array<PcbClearScope> {
	if (raw === undefined || raw === null || raw === '') return [...PCB_CLEAR_SCOPES];
	const list = Array.isArray(raw) ? raw : String(raw).split(',');
	const wanted = list.map(s => String(s).trim().toLowerCase()).filter(Boolean);
	if (!wanted.length) return [...PCB_CLEAR_SCOPES];
	const invalid = wanted.filter(s => !(PCB_CLEAR_SCOPES as ReadonlyArray<string>).includes(s));
	if (invalid.length) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Unknown clear scope(s): ${invalid.join(', ')}. Valid: ${PCB_CLEAR_SCOPES.join(', ')}.`,
		);
	}
	// Preserve canonical order, drop dupes.
	return PCB_CLEAR_SCOPES.filter(s => wanted.includes(s));
}

/**
 * Max enumerate→delete passes per clear (issue #112). One pass is NOT enough on
 * a real board: a full clear of 153 tracks reported 153 deleted, yet a reload +
 * `--dry-run` still found 8 — the first enumeration reads a stale engine index,
 * or part of a batch is invalidated inside the same transaction. Running `pcb
 * clear` a SECOND time converged to 0, so the fix is to do that re-enumeration
 * inside the handler instead of making the caller notice.
 */
const PCB_CLEAR_MAX_ROUNDS = 5;

export const pcbPageClear: Handler = async (payload) => {
	const dryRun = optionalBoolean(payload, 'dryRun') === true;
	const includeLocked = optionalBoolean(payload, 'includeLocked') === true;
	const preserveOutline = optionalBoolean(payload, 'preserveOutline') !== false;
	const scopes = parsePcbClearScopes(payload.only);
	const active = new Set<PcbClearScope>(scopes);

	// The kinds this call is allowed to touch, resolved once (scope-gated content
	// kinds keep the lock guard; outline kinds bypass it — see PCB_OUTLINE_KINDS).
	const targets: Array<{ kind: PcbClearKind; ignoreLock: boolean }> = [];
	for (const kind of PCB_CLEAR_KINDS) {
		if (kind.scope && active.has(kind.scope)) targets.push({ kind, ignoreLock: false });
	}
	if (!preserveOutline) {
		for (const kind of PCB_OUTLINE_KINDS) targets.push({ kind, ignoreLock: true });
	}

	// Union of every id enumerated across all rounds, per kind. Rounds re-enumerate
	// (that is the point), so ids are de-duplicated here — a primitive that shows up
	// again in round 2 must not be counted twice in the report.
	const idsByKey: Record<string, Array<string>> = {};
	const seenByKey: Record<string, Set<string>> = {};
	const skippedLocked: Record<string, number> = {};
	const warnings: Array<string> = [];
	// A kind whose delete explicitly FAILED (threw / returned false) is dropped from
	// later rounds: retrying is what fixes a stale enumeration, not a rejected batch —
	// hammering it 5× would only repeat the same warning.
	const failedSet = new Set<string>();

	// Collect this round's ids for one kind, skipping locked (unless includeLocked)
	// and applying each kind's copper-layer / membership filter. An enumeration
	// failure is a WARNING (never silently swallowed — a class that fails to
	// enumerate would otherwise be under-reported as "0 to clear"). Locked
	// primitives are tallied on the FIRST round only; they enumerate every round.
	const collect = async (kind: PcbClearKind, ignoreLock: boolean, countLocked: boolean): Promise<Array<string>> => {
		let items: Array<SchPrimitiveLike>;
		try {
			items = (await kind.getAll()) ?? [];
		}
		catch (err) {
			warnings.push(warnText(`enumerate ${kind.key}`, err));
			return [];
		}
		const ids: Array<string> = [];
		for (const p of items) {
			if (kind.filter && !kind.filter(p)) continue;
			if (!ignoreLock && !includeLocked && pcbPrimLocked(p)) {
				if (countLocked) skippedLocked[kind.key] = (skippedLocked[kind.key] ?? 0) + 1;
				continue;
			}
			ids.push(p.getState_PrimitiveId());
		}
		return ids;
	};

	/** Fold a round's ids into the cumulative report (de-duped). */
	const record = (key: string, ids: Array<string>): void => {
		const seen = (seenByKey[key] ??= new Set<string>());
		for (const id of ids) {
			if (seen.has(id)) continue;
			seen.add(id);
			(idsByKey[key] ??= []).push(id);
		}
	};

	// Delete-fn index (kind key → its class delete).
	const delByKey = new Map<string, (ids: Array<string>) => Promise<boolean>>();
	for (const kind of [...PCB_CLEAR_KINDS, ...PCB_OUTLINE_KINDS]) delByKey.set(kind.key, kind.del);

	// Enumerate → delete → RE-enumerate until a round finds nothing left (or the
	// round cap trips). dry-run never loops: it reports one enumeration pass.
	const maxRounds = dryRun ? 1 : PCB_CLEAR_MAX_ROUNDS;
	let rounds = 0;
	let leftover = 0;
	for (let r = 0; r < maxRounds; r++) {
		rounds++;
		const round: Record<string, Array<string>> = {};
		let found = 0;
		for (const { kind, ignoreLock } of targets) {
			if (failedSet.has(kind.key)) continue;
			const ids = await collect(kind, ignoreLock, rounds === 1);
			if (!ids.length) continue;
			round[kind.key] = ids;
			found += ids.length;
		}
		if (dryRun) {
			for (const [key, ids] of Object.entries(round)) record(key, ids);
			break;
		}
		leftover = found;
		if (!found) break; // converged: nothing enumerates any more

		for (const [key, ids] of Object.entries(round)) {
			// Report every id this handler ATTEMPTED to delete (delete-failure is
			// reported separately via `failed`) — same contract as the single-pass
			// version this replaces.
			record(key, ids);
			const del = delByKey.get(key);
			if (!del) continue;
			try {
				const ok = await del(ids);
				if (ok === false) {
					failedSet.add(key);
					warnings.push(`delete ${key}: batch delete returned false — ${ids.length} primitive(s) may remain`);
				}
			}
			catch (err) { failedSet.add(key); warnings.push(warnText(`delete ${key}`, err)); }
		}
	}
	// The cap tripped while a round was still finding primitives: the last round's
	// deletes went out unverified, so the board may still not be clean.
	if (!dryRun && rounds >= maxRounds && leftover > 0) {
		warnings.push(`clear did not converge after ${rounds} round(s) — ${leftover} primitive(s) still enumerated on the last pass; save + \`easyeda doc reload\`, then re-run clear`);
	}

	const deleted: Record<string, number> = {};
	let total = 0;
	for (const [key, ids] of Object.entries(idsByKey)) { deleted[key] = ids.length; total += ids.length; }
	const skippedTotal = Object.values(skippedLocked).reduce((a, b) => a + b, 0);
	const failed = [...failedSet];

	return {
		result: {
			scopes,
			deleted,
			total,
			deletedIds: idsByKey,
			rounds,
			...(skippedTotal ? { skippedLocked, skippedLockedTotal: skippedTotal } : {}),
			...(failed.length ? { failed } : {}),
			preserveOutline,
			includeLocked,
			dryRun,
			...(warnings.length ? { warnings } : {}),
		},
	};
};

// ─── PCB layout adjustment (deterministic align / distribute / grid-snap) ──
// EasyEDA exposes NO component align/distribute/grid API, so these read each
// component's bbox + anchor, compute, and write absolute x/y. Fully testable.

type PcbBox = { minX: number; minY: number; maxX: number; maxY: number };
type PcbLayoutItem = { id: string; designator: string | undefined; x: number; y: number; box: PcbBox };

/** Resolve target component ids: explicit primitiveIds, else the current selection. */
async function resolvePcbTargetIds(payload: Payload): Promise<Array<string>> {
	const raw = payload.primitiveIds;
	if (typeof raw === 'string') return [raw];
	if (Array.isArray(raw) && raw.every(id => typeof id === 'string')) return raw as Array<string>;
	try {
		return (await eda.pcb_SelectControl.getAllSelectedPrimitives_PrimitiveId()) ?? [];
	}
	catch { return []; }
}

/** Read a component's anchor (x/y) and rendered bbox. Returns null for non-components. */
async function readPcbComponentLayout(id: string): Promise<PcbLayoutItem | null> {
	const component = await eda.pcb_PrimitiveComponent.get(id);
	if (!component) return null;
	const box = await eda.pcb_Primitive.getPrimitivesBBox([id]);
	if (!box) return null;
	return {
		id,
		designator: component.getState_Designator(),
		x: component.getState_X(),
		y: component.getState_Y(),
		box,
	};
}

const pcbAlign: Handler = async (payload) => {
	const mode = requireString(payload, 'mode');
	const ids = await resolvePcbTargetIds(payload);
	const items = (await Promise.all(ids.map(readPcbComponentLayout))).filter((i): i is PcbLayoutItem => i !== null);
	if (items.length < 2) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`align needs >= 2 components (got ${items.length}); select components or pass primitiveIds.`,
		);
	}

	const cx = (b: PcbBox) => (b.minX + b.maxX) / 2;
	const cy = (b: PcbBox) => (b.minY + b.maxY) / 2;
	// Target reference = the group extent; each item shifts so its edge/center matches.
	let targetFor: (it: PcbLayoutItem) => { x?: number; y?: number };
	switch (mode) {
		case 'left': { const t = Math.min(...items.map(i => i.box.minX)); targetFor = i => ({ x: i.x + (t - i.box.minX) }); break; }
		case 'right': { const t = Math.max(...items.map(i => i.box.maxX)); targetFor = i => ({ x: i.x + (t - i.box.maxX) }); break; }
		// y-up: "top" is the larger y.
		case 'top': { const t = Math.max(...items.map(i => i.box.maxY)); targetFor = i => ({ y: i.y + (t - i.box.maxY) }); break; }
		case 'bottom': { const t = Math.min(...items.map(i => i.box.minY)); targetFor = i => ({ y: i.y + (t - i.box.minY) }); break; }
		case 'centerX': { const t = items.reduce((s, i) => s + cx(i.box), 0) / items.length; targetFor = i => ({ x: i.x + (t - cx(i.box)) }); break; }
		case 'centerY': { const t = items.reduce((s, i) => s + cy(i.box), 0) / items.length; targetFor = i => ({ y: i.y + (t - cy(i.box)) }); break; }
		default:
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown align mode "${mode}"; expected left|right|top|bottom|centerX|centerY.`,
			);
	}

	const moved: Array<Record<string, unknown>> = [];
	for (const it of items) {
		const t = targetFor(it);
		const nx = t.x ?? it.x;
		const ny = t.y ?? it.y;
		try {
			if (nx !== it.x || ny !== it.y) await eda.pcb_PrimitiveComponent.modify(it.id, { x: nx, y: ny });
		}
		catch (err) {
			throw edaError(err, `Failed to align component ${it.designator ?? it.id}.`);
		}
		moved.push({ primitiveId: it.id, designator: it.designator, from: { x: it.x, y: it.y }, to: { x: nx, y: ny } });
	}
	return { result: { mode, moved, count: moved.length } };
};

const pcbDistribute: Handler = async (payload) => {
	const axis = requireString(payload, 'axis');
	if (axis !== 'x' && axis !== 'y') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `Unknown axis "${axis}"; expected x or y.`);
	}
	const ids = await resolvePcbTargetIds(payload);
	const items = (await Promise.all(ids.map(readPcbComponentLayout))).filter((i): i is PcbLayoutItem => i !== null);
	if (items.length < 3) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `distribute needs >= 3 components (got ${items.length}).`);
	}

	const center = (it: PcbLayoutItem) => axis === 'x' ? (it.box.minX + it.box.maxX) / 2 : (it.box.minY + it.box.maxY) / 2;
	const sorted = [...items].sort((a, b) => center(a) - center(b));
	const first = center(sorted[0]);
	const last = center(sorted[sorted.length - 1]);
	const step = (last - first) / (sorted.length - 1);

	const moved: Array<Record<string, unknown>> = [];
	for (let i = 0; i < sorted.length; i++) {
		const it = sorted[i];
		const delta = (first + i * step) - center(it);
		const nx = axis === 'x' ? it.x + delta : it.x;
		const ny = axis === 'y' ? it.y + delta : it.y;
		try {
			// Keep the two extremes fixed; move only the interior ones.
			if (i !== 0 && i !== sorted.length - 1 && Math.abs(delta) > 1e-6) {
				await eda.pcb_PrimitiveComponent.modify(it.id, { x: nx, y: ny });
			}
		}
		catch (err) {
			throw edaError(err, `Failed to distribute component ${it.designator ?? it.id}.`);
		}
		moved.push({ primitiveId: it.id, designator: it.designator, from: { x: it.x, y: it.y }, to: { x: nx, y: ny } });
	}
	return { result: { axis, moved, count: moved.length } };
};

const pcbGridSnap: Handler = async (payload) => {
	const grid = requireNumber(payload, 'grid');
	if (grid <= 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `grid must be > 0 (got ${grid}).`);
	}
	const ids = await resolvePcbTargetIds(payload);
	if (ids.length === 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'No target components; select components or pass primitiveIds.');
	}

	const snap = (v: number) => Math.round(v / grid) * grid;
	const snapped: Array<Record<string, unknown>> = [];
	for (const id of ids) {
		const component = await eda.pcb_PrimitiveComponent.get(id);
		if (!component) continue;
		const x = component.getState_X();
		const y = component.getState_Y();
		const nx = snap(x);
		const ny = snap(y);
		try {
			if (nx !== x || ny !== y) await eda.pcb_PrimitiveComponent.modify(id, { x: nx, y: ny });
		}
		catch (err) {
			throw edaError(err, `Failed to grid-snap component ${component.getState_Designator() ?? id}.`);
		}
		snapped.push({ primitiveId: id, designator: component.getState_Designator(), from: { x, y }, to: { x: nx, y: ny } });
	}
	return { result: { grid, snapped, count: snapped.length } };
};

/**
 * Translate components by a relative (dx, dy) — nudge a group. Operates on the
 * current selection unless primitiveIds is given.
 */
const pcbComponentsMove: Handler = async (payload) => {
	const dx = requireNumber(payload, 'dx');
	const dy = requireNumber(payload, 'dy');
	const ids = await resolvePcbTargetIds(payload);
	if (ids.length === 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'No target components; select components or pass primitiveIds.');
	}
	const moved: Array<Record<string, unknown>> = [];
	for (const id of ids) {
		const component = await eda.pcb_PrimitiveComponent.get(id);
		if (!component) continue;
		const x = component.getState_X();
		const y = component.getState_Y();
		const nx = x + dx;
		const ny = y + dy;
		try {
			await eda.pcb_PrimitiveComponent.modify(id, { x: nx, y: ny });
		}
		catch (err) {
			throw edaError(err, `Failed to move component ${component.getState_Designator() ?? id}.`);
		}
		moved.push({ primitiveId: id, designator: component.getState_Designator(), from: { x, y }, to: { x: nx, y: ny } });
	}
	return { result: { dx, dy, moved, count: moved.length } };
};

// ─── PCB auto-layout seed: cluster by shared local nets + grid-pack (P6) ──
// The mechanical first pass. The agent then applies higher-priority rules
// (mechanical/connectors → decoupling → thermal) per pcb-layout-conventions.md.

/** Global nets (GND/power/high-fanout) connect everything, so they are excluded from clustering. */
function isGlobalNetName(net: string): boolean {
	return /^(?:[adp])?gnd$|^v(?:cc|dd|ss|in|out|bus|bat|sys|ref)\b|^[+-]?\d+v\d*$|^[+-]/i.test(net)
		|| /gnd|vcc|vdd|vss/i.test(net);
}

type ArrangeItem = {
	id: string;
	designator: string | undefined;
	x: number;
	y: number;
	box: PcbBox;
	locked: boolean;
	nets: Array<string>;
};

/** Union-find clustering: components sharing a non-global, low-fanout local net join one group. */
function clusterByLocalNets(items: Array<ArrangeItem>): Array<Array<ArrangeItem>> {
	const netToIdx = new Map<string, Array<number>>();
	items.forEach((it, idx) => {
		for (const n of it.nets) {
			if (!n || isGlobalNetName(n)) continue;
			const arr = netToIdx.get(n) ?? [];
			arr.push(idx);
			netToIdx.set(n, arr);
		}
	});
	const parent = items.map((_, i) => i);
	const find = (a: number): number => {
		while (parent[a] !== a) {
			parent[a] = parent[parent[a]];
			a = parent[a];
		}
		return a;
	};
	const union = (a: number, b: number) => { parent[find(a)] = find(b); };
	for (const idxs of netToIdx.values()) {
		if (idxs.length < 2 || idxs.length > 8) continue; // skip singletons + high-fanout buses
		for (let i = 1; i < idxs.length; i++) union(idxs[0], idxs[i]);
	}
	const groups = new Map<number, Array<ArrangeItem>>();
	items.forEach((it, idx) => {
		const root = find(idx);
		const g = groups.get(root) ?? [];
		g.push(it);
		groups.set(root, g);
	});
	return [...groups.values()].sort((a, b) => b.length - a.length);
}

const pcbComponentsArrange: Handler = async (payload) => {
	const mode = optionalString(payload, 'mode') ?? 'cluster';
	const pitch = optionalNumber(payload, 'pitch') ?? 50;    // gap between cells (mil)
	const gutter = optionalNumber(payload, 'gutter') ?? 150;  // gap between cluster blocks (mil)
	const colsIn = optionalNumber(payload, 'cols');
	const ids = await resolvePcbTargetIds(payload);
	if (ids.length < 2) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `arrange needs >= 2 components (got ${ids.length}); select components or pass primitiveIds.`);
	}

	const items: Array<ArrangeItem> = [];
	for (const id of ids) {
		const component = await eda.pcb_PrimitiveComponent.get(id);
		if (!component) continue;
		const box = await eda.pcb_Primitive.getPrimitivesBBox([id]);
		if (!box) continue;
		let nets: Array<string> = [];
		try {
			const pads = await eda.pcb_PrimitiveComponent.getAllPinsByPrimitiveId(id);
			nets = [...new Set((pads ?? []).map(p => p.getState_Net()).filter((n): n is string => Boolean(n)))];
		}
		catch { /* nets optional */ }
		items.push({
			id,
			designator: component.getState_Designator(),
			x: component.getState_X(),
			y: component.getState_Y(),
			box,
			locked: component.getState_PrimitiveLock(),
			nets,
		});
	}

	const movable = items.filter(i => !i.locked);
	if (movable.length === 0) {
		return { result: { mode, groups: 0, moved: [], count: 0, note: 'all target components are locked' } };
	}

	// Anchor at the top-left of the current movable region (y-up: top = max y).
	const originX = Math.min(...movable.map(i => i.box.minX));
	const originY = Math.max(...movable.map(i => i.box.maxY));

	const groups: Array<Array<ArrangeItem>> = mode === 'grid' ? [movable] : clusterByLocalNets(movable);

	const moved: Array<Record<string, unknown>> = [];
	let blockX = originX;
	for (const group of groups) {
		const cellW = Math.max(...group.map(i => i.box.maxX - i.box.minX)) + pitch;
		const cellH = Math.max(...group.map(i => i.box.maxY - i.box.minY)) + pitch;
		const cols = colsIn ?? Math.max(1, Math.ceil(Math.sqrt(group.length)));
		// Tidy, stable order within a block: by designator, numeric-aware (C2 before C10).
		group.sort((a, b) => (a.designator ?? '').localeCompare(b.designator ?? '', undefined, { numeric: true }));
		for (let k = 0; k < group.length; k++) {
			const it = group[k];
			const col = k % cols;
			const row = Math.floor(k / cols);
			const cellCenterX = blockX + col * cellW + cellW / 2;
			const cellCenterY = originY - row * cellH - cellH / 2; // y-up: rows descend
			const bcx = (it.box.minX + it.box.maxX) / 2;
			const bcy = (it.box.minY + it.box.maxY) / 2;
			// Preserve each component's anchor↔bbox-center offset.
			const nx = cellCenterX - bcx + it.x;
			const ny = cellCenterY - bcy + it.y;
			try {
				await eda.pcb_PrimitiveComponent.modify(it.id, { x: nx, y: ny });
			}
			catch (err) {
				throw edaError(err, `Failed to arrange component ${it.designator ?? it.id}.`);
			}
			moved.push({ primitiveId: it.id, designator: it.designator, from: { x: it.x, y: it.y }, to: { x: nx, y: ny } });
		}
		const usedCols = Math.min(cols, group.length);
		blockX += usedCols * cellW + gutter;
	}

	return { result: { mode, groups: groups.length, moved, count: moved.length } };
};

// ─── PCB DRC ─────────────────────────────────────────────────────────

const pcbDrcCheck: Handler = async (payload) => {
	const strict = optionalBoolean(payload, 'strict') !== false;
	let violations: Array<unknown>;
	try {
		// Mirrors sch_Drc.check: the 3rd arg (includeVerboseError) selects the
		// overload that returns the violations array — a no-arg call returns a
		// bare boolean instead. Violations are grouped: [{name, list:[{name(net),
		// list:[{errorType, errorObjType, obj1, …}]}]}]. Requires the PCB document
		// to be the ACTIVE/foreground tab (else 'no subscription' on the canvas).
		violations = (await eda.pcb_Drc.check(strict, false, true)) as Array<unknown>;
	}
	catch (err) {
		throw edaError(err, 'Failed to run PCB DRC (ensure the PCB document is the active/foreground tab).');
	}

	// A Netlist Error ("PCB and schematic netlist does not match") is usually a
	// stale Board binding, not a real mismatch (see #33). Surface the bound
	// schematic/PCB names so the fix (`board rebind`) is obvious. Best-effort:
	// never let this diagnostic hide the actual DRC result.
	let binding: Record<string, unknown> | undefined;
	const hasNetlistError = violations.some((v) => JSON.stringify(v ?? '').toLowerCase().includes('netlist'));
	if (hasNetlistError) {
		try {
			const board = await eda.dmt_Board.getCurrentBoardInfo();
			if (board) {
				binding = {
					boardName: board.name,
					schematicUuid: board.schematic?.uuid ?? null,
					schematicName: board.schematic?.name ?? null,
					pcbUuid: board.pcb?.uuid ?? null,
					pcbName: board.pcb?.name ?? null,
					hint: 'Netlist Error is often a stale Board binding — verify the schematic UUID matches the open schematic; if not, run `easyeda board rebind --schematic <uuid> --pcb <uuid>`.',
				};
			}
		}
		catch { /* best-effort — diagnostic only */ }
	}

	return { result: { passed: violations.length === 0, violations, ...(binding ? { binding } : {}) } };
};

/**
 * Read the active PCB's DRC rule configuration (design rules: clearances, track
 * widths, via sizes, …) without running a check — inspect what pcb.drc.check
 * enforces. Returned verbatim from `eda.pcb_Drc.getCurrentRuleConfiguration`.
 */
const pcbDrcRules: Handler = async () => {
	let rules;
	try {
		rules = await eda.pcb_Drc.getCurrentRuleConfiguration();
	}
	catch (err) {
		throw edaError(err, 'Failed to read PCB DRC rule configuration (ensure the PCB document is the active/foreground tab).');
	}
	return { result: { rules: rules ?? null } };
};

/** Replace the complete opaque rule payload exported by pcb.drc.rules. */
const pcbDrcRulesSet: Handler = async payload => writePcbRules(payload);

async function writePcbRules(payload: Payload, expected?: Record<string, unknown>): Promise<ActionResult> {
	const suppliedRuleConfiguration = payload.ruleConfiguration ?? payload.rules;
	const ruleConfiguration = barePcbRuleConfiguration(suppliedRuleConfiguration);
	if (!ruleConfiguration) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'ruleConfiguration must be a complete JSON object.');
	}
	const netRules = payload.netRules;
	if (netRules !== undefined && !Array.isArray(netRules)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'netRules must be an array when provided.');
	}
	let beforeRules: Record<string, unknown> | null = null;
	let beforeNetRules: Array<Record<string, unknown>> | undefined;
	try {
		beforeRules = barePcbRuleConfiguration(await eda.pcb_Drc.getCurrentRuleConfiguration());
		if (netRules !== undefined) beforeNetRules = await eda.pcb_Drc.getNetRules();
	}
	catch (err) {
		throw edaError(err, 'Failed to read the current PCB rules before overwrite.');
	}
	if (!beforeRules || typeof beforeRules !== 'object' || Array.isArray(beforeRules)) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Current PCB rule configuration is unavailable; refusing an overwrite that cannot be rolled back.');
	}
	if (netRules !== undefined && !Array.isArray(beforeNetRules)) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Current PCB net rules are unavailable; refusing a two-stage overwrite that cannot be rolled back.');
	}
	if (payload.dryRun === true) {
		return { result: {
			dryRun: true,
			before: { ruleConfiguration: beforeRules, ...(netRules === undefined ? {} : { netRules: beforeNetRules }) },
			requested: { ruleConfiguration, ...(netRules === undefined ? {} : { netRules }) },
		} };
	}
	if (expected && (exactJSON(beforeRules) !== exactJSON(expected.ruleConfiguration)
		|| (netRules !== undefined && exactJSON(beforeNetRules) !== exactJSON(expected.netRules)))) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'PCB configuration changed during planning; read fresh state and retry. No write was attempted.');
	}

	const readActual = async (): Promise<{
		actual: { ruleConfiguration: Record<string, unknown> | null; netRules?: Array<Record<string, unknown>> } | null;
		error: string | null;
	}> => {
		try {
			const actualRules = barePcbRuleConfiguration(await eda.pcb_Drc.getCurrentRuleConfiguration());
			const actualNetRules = netRules === undefined ? undefined : await eda.pcb_Drc.getNetRules();
			return {
				actual: { ruleConfiguration: actualRules ?? null, ...(actualNetRules === undefined ? {} : { netRules: actualNetRules }) },
				error: null,
			};
		}
		catch (err) { return { actual: null, error: describeThrown(err) }; }
	};
	const rollback = async () => {
		let rollbackRulesWritten = false;
		let rollbackNetRulesWritten = netRules === undefined;
		const rollbackErrors: string[] = [];
		try { rollbackRulesWritten = await eda.pcb_Drc.overwriteCurrentRuleConfiguration(beforeRules as Record<string, unknown>); }
		catch (err) { rollbackErrors.push(`ruleConfiguration: ${describeThrown(err)}`); }
		if (netRules !== undefined) {
			try { rollbackNetRulesWritten = await eda.pcb_Drc.overwriteNetRules(beforeNetRules as Array<Record<string, unknown>>); }
			catch (err) { rollbackErrors.push(`netRules: ${describeThrown(err)}`); }
		}
		const finalReadback = await readActual();
		const rolledBack = !finalReadback.error && !!finalReadback.actual
			&& pcbRulesEqual(finalReadback.actual.ruleConfiguration, beforeRules)
			&& (netRules === undefined || exactJSON(finalReadback.actual.netRules) === exactJSON(beforeNetRules));
		return { rollbackRulesWritten, rollbackNetRulesWritten, rollbackErrors, rolledBack, finalReadback };
	};

	let rulesWritten = false;
	try {
		rulesWritten = await eda.pcb_Drc.overwriteCurrentRuleConfiguration(ruleConfiguration as Record<string, unknown>);
	}
	catch (err) {
		const rollbackResult = await rollback();
		return {
			result: {
				partial: true, verified: false, rulesWritten: null, netRulesWritten: false,
				writeError: describeThrown(err), rollbackAttempted: true,
				rollbackRulesWritten: rollbackResult.rollbackRulesWritten,
				rollbackNetRulesWritten: rollbackResult.rollbackNetRulesWritten,
				rollbackErrors: rollbackResult.rollbackErrors, rolledBack: rollbackResult.rolledBack,
				actual: rollbackResult.finalReadback.actual, readbackError: rollbackResult.finalReadback.error,
			},
			warnings: ['The rule-configuration write threw after the call began; rollback was attempted because mutation status was uncertain.'],
		};
	}
	if (!rulesWritten) {
		const readback = await readActual();
		const changed = !!readback.actual
			&& !pcbRulesEqual(readback.actual.ruleConfiguration, beforeRules);
		return {
			result: {
				partial: changed || !!readback.error, writeFailed: true, verified: false,
				rulesWritten: false, netRulesWritten: false, actual: readback.actual,
				readbackError: readback.error,
			},
			warnings: ['EasyEDA returned false while writing the rule configuration; net rules were not attempted.'],
		};
	}

	let netRulesWritten = netRules === undefined;
	let netRulesWriteError: string | null = null;
	if (netRules !== undefined) {
		try { netRulesWritten = await eda.pcb_Drc.overwriteNetRules(netRules as Array<Record<string, unknown>>); }
		catch (err) { netRulesWriteError = describeThrown(err); }
	}
	if (!netRulesWritten || netRulesWriteError) {
		const rollbackResult = await rollback();
		return {
			result: {
				partial: true, verified: false, rulesWritten: true,
				netRulesWritten: netRulesWriteError ? null : false,
				...(netRulesWriteError ? { writeError: netRulesWriteError } : { writeFailed: true }),
				rollbackAttempted: true,
				rollbackRulesWritten: rollbackResult.rollbackRulesWritten,
				rollbackNetRulesWritten: rollbackResult.rollbackNetRulesWritten,
				rollbackErrors: rollbackResult.rollbackErrors, rolledBack: rollbackResult.rolledBack,
				actual: rollbackResult.finalReadback.actual, readbackError: rollbackResult.finalReadback.error,
			},
			warnings: ['The net-rule write failed after the rule configuration was written; both rule sets were rolled back and read back.'],
		};
	}

	const readback = await readActual();
	if (readback.error || !readback.actual) {
		return {
			result: {
				partial: true, rulesWritten: true, netRulesWritten: true, verified: false,
				actual: readback.actual, readbackError: readback.error ?? 'readback returned no data',
			},
			warnings: ['PCB rules were written, but final readback failed; the applied state is unavailable.'],
		};
	}
	const ruleConfigurationVerified = pcbRulesEqual(readback.actual.ruleConfiguration, ruleConfiguration);
	const netRulesVerified = netRules === undefined || exactJSON(readback.actual.netRules) === exactJSON(netRules);
	return {
		result: {
			rulesWritten, netRulesWritten,
			ruleConfigurationVerified, netRulesVerified,
			verified: rulesWritten && netRulesWritten && ruleConfigurationVerified && netRulesVerified,
			actual: readback.actual,
		},
		...((rulesWritten && netRulesWritten && ruleConfigurationVerified && netRulesVerified) ? {} : {
			warnings: ['EasyEDA reported both writes successful, but readback differs beyond machine roundoff; inspect actual before continuing.'],
		}),
	};
}

const readPcbConfig = async () => {
	const raw = await eda.pcb_Drc.getCurrentRuleConfiguration();
	const ruleConfiguration = barePcbRuleConfiguration(raw);
	const classes = await eda.pcb_Drc.getAllNetClasses();
	const netRules = await eda.pcb_Drc.getNetRules();
	if (!ruleConfiguration || !Array.isArray(classes) || !Array.isArray(netRules)) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'PCB rules, net classes or net rules are unavailable.');
	}
	return JSON.parse(JSON.stringify({ ruleConfiguration, classes, netRules, configurationName: raw?.name ?? null })) as Record<string, any>;
};

const pcbConfigGet: Handler = async () => ({ result: await readPcbConfig() });

const pcbConfigSet: Handler = async (payload) => {
	const before = await readPcbConfig();
	const { changes, ...requested } = planPcbConfig(before, payload);
	if (payload.dryRun === true) return { result: { dryRun: true, before, requested, changes } };
	if (!changes.length) {
		const current = await readPcbConfig();
		if (exactJSON(current) !== exactJSON(before)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'PCB configuration changed during planning; no write was attempted.');
		return { result: { before, requested, changes, changed: false, verified: true, partial: false, actual: requested } };
	}
	if (payload.kind === 'bind' && exactJSON(await eda.pcb_Drc.getAllNetClasses()) !== exactJSON(before.classes)) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'PCB net classes changed during planning; no write was attempted.');
	}
	const result = await writePcbRules(requested, before);
	let verified = result.result?.verified === true;
	let classEvidence: Record<string, unknown> = {};
	if (payload.kind === 'bind') {
		try {
			const actualClasses = await eda.pcb_Drc.getAllNetClasses();
			const classesVerified = exactJSON(actualClasses) === exactJSON(before.classes);
			verified = verified && classesVerified;
			classEvidence = { actualClasses, classesVerified };
		}
		catch (err) { verified = false; classEvidence = { classesVerified: false, classesReadbackError: describeThrown(err) }; }
	}
	return { ...result, result: { ...result.result, before, requested, changes, ...classEvidence, verified, partial: !verified } };
};

const pcbNetClassList: Handler = async () => {
	try {
		const classes = (await eda.pcb_Drc.getAllNetClasses()) ?? [];
		const netRules = (await eda.pcb_Drc.getNetRules()) ?? [];
		return { result: { classes, netRules, count: classes.length } };
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB net classes and their rule assignments.');
	}
};

const pcbNetClassCreate: Handler = async (payload) => {
	const name = requireString(payload, 'name');
	const nets = [...new Set(requireStringArray(payload, 'nets'))];
	if (!nets.length) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'nets must contain at least one PCB net name.');
	const allNets = new Set((await eda.pcb_Net.getAllNetsName()) ?? []);
	const missing = nets.filter(n => !allNets.has(n));
	if (missing.length) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Net class ${name} references missing net(s): ${missing.join(', ')}`);
	}
	const existing = ((await eda.pcb_Drc.getAllNetClasses()) ?? []).find(c => c.name === name);
	if (existing) {
		const requested = [...nets].sort();
		const actual = [...(existing.nets ?? [])].sort();
		if (exactJSON(requested) === exactJSON(actual)) {
			return { result: { created: false, alreadyExists: true, verified: true, netClass: existing } };
		}
		throw new ActionError(
			ErrorCodes.PRECONDITION_REFUSED,
			`Net class ${name} already exists with different members; use a different name or edit it explicitly in EasyEDA.`,
			`existing=${actual.join(',')} requested=${requested.join(',')}`,
		);
	}
	const colorRaw = payload.color;
	const color = (colorRaw && typeof colorRaw === 'object' && !Array.isArray(colorRaw))
		? colorRaw as { r: number; g: number; b: number; alpha: number }
		: { r: 0, g: 128, b: 255, alpha: 1 };
	let ok = false;
	try { ok = await eda.pcb_Drc.createNetClass(name, nets, color); }
	catch (err) { throw edaError(err, `Failed to create PCB net class ${name}.`); }
	const readback = ((await eda.pcb_Drc.getAllNetClasses()) ?? []).find(c => c.name === name);
	const verified = !!readback && exactJSON([...(readback.nets ?? [])].sort()) === exactJSON([...nets].sort());
	return {
		result: { created: ok, verified, netClass: readback ?? null },
		...(verified ? {} : { warnings: [`Net class ${name} was not confirmed by readback; do not assume the rule assignment exists.`] }),
	};
};

// ─── PCB routing (copper tracks + vias) ──────────────────────────────
// Real routing primitives: a track is a line on a copper layer; a via is a
// plated hole. Both bind to a net by NAME (pull names from pcb.nets.list). Layer
// ids are numeric (TOP=1, BOTTOM=2; inner-copper ids are HIGHER — id 3 is silkscreen,
// not copper — read real ids from pcb.layers.list) cast to the layer type — the
// EPCB_LayerId enum may not exist as a runtime global (same reason as
// BOARD_OUTLINE_LAYER). create() is lenient and can return undefined on bad
// params without throwing, so each handler verifies a primitive came back.

const pcbLineCreate: Handler = async (payload) => {
	const startX = requireNumber(payload, 'startX');
	const startY = requireNumber(payload, 'startY');
	const endX = requireNumber(payload, 'endX');
	const endY = requireNumber(payload, 'endY');
	const net = optionalString(payload, 'net') ?? '';
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 6;
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfLine;

	let line;
	try {
		line = await eda.pcb_PrimitiveLine.create(net, layer, startX, startY, endX, endY, lineWidth);
	}
	catch (err) {
		throw edaError(err, 'Failed to create PCB track (导线).');
	}
	if (!line) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'PCB track creation returned no primitive (check the layer id and coordinates).');
	}
	return {
		result: {
			primitiveId: line.getState_PrimitiveId(),
			net,
			layer: Number(layer),
			start: { x: startX, y: startY },
			end: { x: endX, y: endY },
			lineWidth,
		},
	};
};

const pcbViaCreate: Handler = async (payload) => {
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const net = optionalString(payload, 'net') ?? '';
	const holeDiameter = optionalNumber(payload, 'holeDiameter') ?? 12;
	const diameter = optionalNumber(payload, 'diameter') ?? 24;

	let via;
	try {
		// Signature (confirmed in pro-api-types): create(net, x, y, holeDiameter, diameter, …).
		via = await eda.pcb_PrimitiveVia.create(net, x, y, holeDiameter, diameter);
	}
	catch (err) {
		throw edaError(err, 'Failed to create PCB via (过孔).');
	}
	if (!via) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'PCB via creation returned no primitive (check coordinates and diameters).');
	}
	return {
		result: {
			primitiveId: via.getState_PrimitiveId(),
			net,
			x,
			y,
			holeDiameter,
			diameter,
		},
	};
};

/**
 * Save the active PCB document to disk. The PCB counterpart to schematic.save —
 * PCB edits (track/via/move/import) are in-memory until saved, and this is what
 * the daemon's debounced autosave fires for a PCB window.
 */
const pcbSave: Handler = async () => {
	let saved;
	try {
		saved = await eda.pcb_Document.save();
	}
	catch (err) {
		throw edaError(err, 'Failed to save PCB.');
	}
	return { result: { saved } };
};

// ─── PCB copper pour (铺铜) ──────────────────────────────────────────
// pour.create needs an IPCB_Polygon (NOT raw points) — build it with
// pcb_MathPolygon.createPolygon first (this was the missing piece behind the
// earlier "无法创建覆铜边框图元" failures). After create, rebuildCopperRegion()
// computes the actual poured copper (Pour region → Poured copper).

const pcbPourCreate: Handler = async (payload) => {
	const rawPts = payload.points;
	if (!Array.isArray(rawPts) || rawPts.length < 3) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'points must be an array of >= 3 [x,y] pairs (mil).');
	}
	const pts: Array<[number, number]> = [];
	for (const p of rawPts) {
		if (!Array.isArray(p) || p.length < 2 || typeof p[0] !== 'number' || typeof p[1] !== 'number') {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each point must be [x, y] numbers.');
		}
		pts.push([p[0], p[1]]);
	}
	// A copper pour MUST bind to a net. Silently defaulting to '' created netless
	// dead copper (issue #34: a net:"" layer-1 pour that pour-fit --replace can't
	// clear because it only matches same-net pours). Reject it at the source.
	const net = (optionalString(payload, 'net') ?? '').trim();
	if (net === '') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'net is required — a copper pour must bind to a net (e.g. GND). A netless pour is dead copper.');
	}
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfCopper;
	// Enum VALUES are the strings 'solid'/'90grid'/'45grid'; pass the string (the
	// EPCB_PrimitivePourFillMethod enum is not a runtime global).
	const fillMap: Record<string, string> = { solid: 'solid', grid: '90grid', grid45: '45grid' };
	const fill = (fillMap[optionalString(payload, 'fill') ?? 'solid'] ?? 'solid') as unknown as EPCB_PrimitivePourFillMethod;
	const pourName = optionalString(payload, 'name');
	const priority = optionalNumber(payload, 'priority');
	const lineWidth = optionalNumber(payload, 'lineWidth');

	// Polygon source array: [x0,y0,'L',x1,y1,...,x0,y0] — a single 'L' command then
	// a run of vertex pairs, EXPLICITLY closed by repeating the first vertex. Matches
	// the proven balance-copper path format (patternGenerator.ts).
	const src: Array<number | string> = [pts[0][0], pts[0][1], 'L'];
	for (let i = 1; i < pts.length; i++) src.push(pts[i][0], pts[i][1]);
	src.push(pts[0][0], pts[0][1]);
	const poly = eda.pcb_MathPolygon.createPolygon(src as unknown as TPCB_PolygonSourceArray);
	if (!poly) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to build pour polygon from points (createPolygon returned undefined — points must form a valid closed polygon).');
	}

	let pour;
	try {
		pour = await eda.pcb_PrimitivePour.create(net, layer, poly, fill, undefined, pourName, priority, lineWidth);
	}
	catch (err) {
		throw edaError(err, 'Failed to create copper pour (铺铜).');
	}
	if (!pour) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Copper pour creation returned no primitive (check layer/net/points).');
	}

	let poured = false;
	try { poured = !!(await pour.rebuildCopperRegion()); }
	catch { /* rebuild best-effort — the pour region exists even if the fill compute fails */ }

	return {
		result: {
			primitiveId: pour.getState_PrimitiveId(),
			net,
			layer: Number(layer),
			fill: String(fill),
			poured,
		},
	};
};

const pcbPourList: Handler = async (payload) => {
	const net = optionalString(payload, 'net');
	let pours;
	try {
		pours = await eda.pcb_PrimitivePour.getAll(net);
	}
	catch (err) {
		throw edaError(err, 'Failed to list copper pours.');
	}
	const list = (pours ?? []).map(p => {
		let source: unknown = null;
		let geometryAvailable = false;
		try {
			source = p.getState_ComplexPolygon().getSource();
			geometryAvailable = Array.isArray(source);
		}
		catch { /* retain the boundary facts and mark geometry unknown */ }
		return {
			primitiveId: p.getState_PrimitiveId(),
			net: p.getState_Net(),
			layer: p.getState_Layer(),
			pourName: p.getState_PourName(),
			fillMethod: p.getState_PourFillMethod(),
			priority: p.getState_PourPriority(),
			lineWidth: p.getState_LineWidth(),
			locked: p.getState_PrimitiveLock(),
			geometryAvailable,
			source,
		};
	});
	return { result: { pours: list, count: list.length } };
};

// Materialized pour fills are the one PCB geometry surface whose SDK values use
// the host's 0.1mil coordinate/line-width unit. Boundaries, tracks and vias are
// already mil. Normalize this surface at the typed boundary so every downstream
// consumer compares one unit system. Polygon command fields are role-sensitive:
// ARC/CARC sweeps and R rotation are angles and must never be scaled.
const POURED_NATIVE_TO_MIL = 10;

function pouredFinite(value: unknown, label: string): number {
	if (typeof value !== 'number' || !Number.isFinite(value)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`Materialized poured polygon ${label} is not a finite number.`);
	}
	return value;
}

function normalizePouredFlatSource(raw: unknown[]): unknown[] {
	if (raw.length === 0) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Materialized poured polygon contour is empty.');
	}
	// Compact whole-shape modes have angle fields interleaved with coordinates.
	if (typeof raw[0] === 'string') {
		if (raw[0] === 'R') {
			if (raw.length !== 7) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Materialized poured R contour must contain x, y, width, height, rotation and round.');
			}
			const n = raw.slice(1).map((v, i) => pouredFinite(v, `R[${i}]`));
			return ['R', n[0] * POURED_NATIVE_TO_MIL, n[1] * POURED_NATIVE_TO_MIL,
				n[2] * POURED_NATIVE_TO_MIL, n[3] * POURED_NATIVE_TO_MIL,
				n[4], n[5] * POURED_NATIVE_TO_MIL];
		}
		if (raw[0] === 'CIRCLE') {
			if (raw.length !== 4) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Materialized poured CIRCLE contour must contain cx, cy and radius.');
			}
			return ['CIRCLE',
				pouredFinite(raw[1], 'CIRCLE.cx') * POURED_NATIVE_TO_MIL,
				pouredFinite(raw[2], 'CIRCLE.cy') * POURED_NATIVE_TO_MIL,
				pouredFinite(raw[3], 'CIRCLE.radius') * POURED_NATIVE_TO_MIL];
		}
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			`Materialized poured polygon starts with unsupported command ${raw[0]}.`);
	}
	if (raw.length < 5) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Materialized poured polygon contour is too short.');
	}
	const out: unknown[] = [
		pouredFinite(raw[0], 'start.x') * POURED_NATIVE_TO_MIL,
		pouredFinite(raw[1], 'start.y') * POURED_NATIVE_TO_MIL,
	];
	for (let i = 2; i < raw.length;) {
		const command = raw[i];
		if (typeof command !== 'string') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Materialized poured polygon command at index ${i} is invalid.`);
		}
		out.push(command);
		i++;
		switch (command) {
			case 'L':
			case 'C': {
				const first = i;
				while (i < raw.length && typeof raw[i] !== 'string') {
					out.push(pouredFinite(raw[i], `${command}[${i - first}]`) * POURED_NATIVE_TO_MIL);
					i++;
				}
				const count = i - first;
				const valid = command === 'L'
					? count >= 2 && count % 2 === 0
					: count >= 6 && count % 6 === 0;
				if (!valid) {
					throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
						`Materialized poured ${command} command has an invalid coordinate count (${count}).`);
				}
				break;
			}
			case 'ARC':
			case 'CARC': {
				if (i + 2 >= raw.length) {
					throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
						`Materialized poured ${command} command is incomplete.`);
				}
				// The sweep is degrees; only the endpoint uses the 0.1mil unit.
				out.push(
					pouredFinite(raw[i], `${command}.sweep`),
					pouredFinite(raw[i + 1], `${command}.endX`) * POURED_NATIVE_TO_MIL,
					pouredFinite(raw[i + 2], `${command}.endY`) * POURED_NATIVE_TO_MIL,
				);
				i += 3;
				break;
			}
			default:
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized poured polygon command ${command} is unsupported.`);
		}
	}
	return out;
}

function normalizePouredStrictComplexSource(raw: unknown): unknown[] {
	if (!Array.isArray(raw) || raw.length === 0) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			'Materialized poured polygon source must be a non-empty array.');
	}
	const hasNested = raw.some(Array.isArray);
	if (!hasNested) return normalizePouredFlatSource(raw);
	if (!raw.every(Array.isArray)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			'Materialized poured polygon source mixes contour arrays with flat tokens.');
	}
	return raw.map(contour => normalizePouredStrictComplexSource(contour));
}

// Read the materialized copper produced by pour-rebuild. pcb_PrimitivePour is
// only the editable boundary; pcb_PrimitivePoured carries the actual islands,
// holes and stroked thermal spokes after obstacle/keepout processing.
const pcbPouredList: Handler = async (payload) => {
	const net = optionalString(payload, 'net');
	let poured;
	let pours;
	try {
		poured = await eda.pcb_PrimitivePoured.getAll();
		// Read the complete boundary inventory even when filtering by net.  The
		// materialized inventory is global, so a filtered boundary query would
		// make every other-net object look orphaned and tempt callers to skip it.
		pours = await eda.pcb_PrimitivePour.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read materialized poured copper.');
	}
	// The official SDK uses undefined when an inventory cannot be read.  Only a
	// real [] proves known-empty; null/undefined must propagate as unavailable.
	if (!Array.isArray(poured)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			'Materialized poured copper inventory is unavailable (expected an array, including [] for known-empty).');
	}
	if (!Array.isArray(pours)) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
			'Copper pour boundary inventory is unavailable (expected an array, including [] for known-empty).');
	}
	const boundary = new Map<string, { net: string; layer: number }>();
	for (let i = 0; i < pours.length; i++) {
		const p = pours[i];
		let primitiveId: unknown;
		let boundaryNet: unknown;
		let boundaryLayer: unknown;
		try {
			primitiveId = p.getState_PrimitiveId();
			boundaryNet = p.getState_Net();
			boundaryLayer = p.getState_Layer();
		}
		catch (err) {
			throw edaError(err, `Copper pour boundary[${i}] attribution is unreadable.`);
		}
		if (typeof primitiveId !== 'string' || primitiveId.trim() === '') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Copper pour boundary[${i}] has no readable primitiveId.`);
		}
		if (typeof boundaryNet !== 'string' || boundaryNet.trim() === '') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Copper pour boundary ${primitiveId} has no readable net.`);
		}
		if (typeof boundaryLayer !== 'number' || !Number.isFinite(boundaryLayer)
			|| !Number.isInteger(boundaryLayer)
			|| !(boundaryLayer === 1 || boundaryLayer === 2 || (boundaryLayer >= 15 && boundaryLayer <= 44))) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Copper pour boundary ${primitiveId} has no readable copper layer.`);
		}
		if (boundary.has(primitiveId)) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Copper pour boundary attribution is ambiguous: duplicate primitiveId ${primitiveId}.`);
		}
		boundary.set(primitiveId, { net: boundaryNet, layer: boundaryLayer });
	}
	const list: Array<Record<string, unknown>> = [];
	for (let i = 0; i < poured.length; i++) {
		const p = poured[i];
		let primitiveId: unknown;
		let pourPrimitiveId: unknown;
		let rawFills: unknown;
		try {
			primitiveId = p.getState_PrimitiveId();
			pourPrimitiveId = p.getState_PourPrimitiveId();
			rawFills = p.getState_PourFills();
		}
		catch (err) {
			throw edaError(err, `Materialized poured object[${i}] attribution or fills are unreadable.`);
		}
		if (typeof primitiveId !== 'string' || primitiveId.trim() === '') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Materialized poured object[${i}] has no readable primitiveId.`);
		}
		if (typeof pourPrimitiveId !== 'string' || pourPrimitiveId.trim() === '') {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Materialized poured object ${primitiveId} has no readable pourPrimitiveId.`);
		}
		const meta = boundary.get(pourPrimitiveId);
		if (!meta) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Materialized poured object ${primitiveId} references missing boundary ${pourPrimitiveId}; net/layer attribution is unavailable.`);
		}
		if (!Array.isArray(rawFills)) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Materialized pour ${pourPrimitiveId} fills are unavailable (expected an array, including [] for known-empty).`);
		}
		const fills: Array<Record<string, unknown>> = [];
		for (let fillIndex = 0; fillIndex < rawFills.length; fillIndex++) {
			const f = rawFills[fillIndex];
			if (!f || typeof f !== 'object') {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill[${fillIndex}] is unreadable.`);
			}
			if (typeof f.id !== 'string' || f.id.trim() === '') {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill[${fillIndex}] has no readable id.`);
			}
			if (!f.path || typeof f.path.getSourceStrictComplex !== 'function') {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill ${f.id} has no readable polygon path.`);
			}
			let rawSource;
			try {
				rawSource = f.path.getSourceStrictComplex();
			}
			catch (err) {
				throw edaError(err, `Materialized pour ${pourPrimitiveId} fill ${f.id} has unreadable polygon geometry.`);
			}
			if (!Array.isArray(rawSource) || rawSource.length === 0) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill ${f.id} returned no polygon source.`);
			}
			if (typeof f.fill !== 'boolean') {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill ${f.id} has no readable fill flag.`);
			}
			const nativeLineWidth = pouredFinite(f.lineWidth, `fill ${f.id} lineWidth`);
			if (nativeLineWidth < 0) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`Materialized pour ${pourPrimitiveId} fill ${f.id} has a negative lineWidth.`);
			}
			const source = normalizePouredStrictComplexSource(rawSource);
			fills.push({
				id: f.id,
				lineWidth: nativeLineWidth * POURED_NATIVE_TO_MIL,
				fill: f.fill,
				source,
				sourceUnits: 'mil',
				lineWidthUnits: 'mil',
				arcSweepUnits: 'degree',
				nativeSourceUnits: '0.1mil',
				nativeLineWidthUnits: '0.1mil',
				geometryKind: f.fill ? 'filled-complex-polygon' : 'stroked-thermal-spoke-path',
			});
		}
		// Filter only after every object has complete attribution and exact fill
		// geometry. A requested net must not hide an unreadable/orphaned object.
		if (net && meta.net !== net) continue;
		list.push({
			primitiveId,
			pourPrimitiveId,
			net: meta.net,
			layer: meta.layer,
			fills,
		});
	}
	return { result: { available: true, poured: list, count: list.length } };
};

const pcbPourDelete: Handler = async (payload) => {
	const raw = payload.primitiveIds;
	let ids: Array<string>;
	if (typeof raw === 'string') ids = [raw];
	else if (Array.isArray(raw) && raw.every(id => typeof id === 'string')) ids = raw as Array<string>;
	else throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required field "primitiveIds" (string or string[]).');

	let deleted;
	try {
		deleted = await eda.pcb_PrimitivePour.delete(ids);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete copper pours.');
	}
	return { result: { deleted, primitiveIds: ids } };
};

const pcbPourRebuild: Handler = async (payload) => {
	const net = optionalString(payload, 'net');
	let pours;
	try {
		pours = await eda.pcb_PrimitivePour.getAll(net);
	}
	catch (err) {
		throw edaError(err, 'Failed to list pours for rebuild.');
	}
	let rebuilt = 0;
	for (const p of pours ?? []) {
		try { if (await p.rebuildCopperRegion()) rebuilt++; }
		catch { /* per-pour best-effort */ }
	}
	return { result: { pours: (pours ?? []).length, rebuilt } };
};

// ─── PCB 走线美化 (拐角圆弧化) ────────────────────────────────────────
// 把已布好的直角/锐角走线圆滑成圆弧，改善美观与可制造性。算法移植自开源扩展
// Easy_EDA_PCB_Beautify (m-RNA, Apache-2.0) —— 见 src/beautify/ 头部与仓库 NOTICE。
// 会 delete 原线段 + create line/arc，故内建 DRC 二分修复 + 重铺覆铜；`dryRun`
// 只计算规划、不落笔（可在真实板上安全预览）。仅处理铜层导线，绝不动丝印/板框。
const pcbBeautify: Handler = async (payload) => {
	const scope = (optionalString(payload, 'scope') ?? 'all') as BeautifyOptions['scope'];
	if (scope !== 'all' && scope !== 'selected') {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `scope must be "all" or "selected" (got "${scope}").`);
	}
	const rawNets = payload.nets;
	const nets = Array.isArray(rawNets)
		? rawNets.filter((n): n is string => typeof n === 'string' && n.length > 0)
		: undefined;
	const opts: BeautifyOptions = {
		scope,
		net: optionalString(payload, 'net'),
		nets,
		layer: optionalNumber(payload, 'layer'),
		cornerRadiusRatio: optionalNumber(payload, 'cornerRadiusRatio') ?? 3.0,
		forceArc: optionalBoolean(payload, 'forceArc') ?? false,
		mergeTransitionSegments: optionalBoolean(payload, 'mergeTransitionSegments') ?? false,
		protectDifferentialAndEqualLength: optionalBoolean(payload, 'protect') ?? true,
		enableDrc: optionalBoolean(payload, 'drc') ?? true,
		drcIgnoreCopperPour: optionalBoolean(payload, 'drcIgnoreCopperPour') ?? true,
		drcRetryCount: optionalNumber(payload, 'drcRetryCount') ?? 4,
		rebuildPour: optionalBoolean(payload, 'rebuildPour') ?? true,
		dryRun: optionalBoolean(payload, 'dryRun') ?? false,
	};
	let summary;
	try {
		summary = await runBeautify(opts);
	}
	catch (err) {
		throw edaError(err, 'Failed to beautify PCB routing (ensure the PCB document is the active/foreground tab).');
	}
	return { result: summary };
};

// ─── PCB region (禁止区域 / 规则区域 keep-out) ────────────────────────
// pcb_PrimitiveRegion is a polygon carrying one or more RULE types — keep
// components / wires / copper / etc. OUT of the area (antenna clearance,
// board-edge inset, mechanical exclusion). It is NOT net-bound filled copper —
// that's a pour (`pcb pour`). Polygon is built exactly like a pour (createPolygon,
// explicitly closed). Rule types accept friendly names OR raw enum numbers.
//
// EPCB_PrimitiveRegionRuleType: NO_COMPONENTS=2, NO_WIRES=5, NO_FILLS=6,
// NO_POURS=7, NO_INNER_ELECTRICAL_LAYERS=8, FOLLOW_REGION_RULE=9.

const REGION_RULE_BY_NAME: Record<string, number> = {
	'no-components': 2, 'keepout-components': 2, components: 2,
	'no-wires': 5, 'no-routing': 5, 'keepout-routing': 5, wires: 5, routing: 5,
	'no-fills': 6, fills: 6,
	'no-pours': 7, 'no-copper': 7, 'keepout-copper': 7, pours: 7, copper: 7,
	'no-inner': 8, 'no-inner-electrical': 8, inner: 8,
	'follow-rule': 9, constraint: 9,
};
const REGION_RULE_NAME: Record<number, string> = {
	2: 'no-components', 5: 'no-wires', 6: 'no-fills', 7: 'no-pours',
	8: 'no-inner-electrical', 9: 'follow-rule',
};
// Antenna / board-edge keep-out default: keep components, wires, AND copper out.
const DEFAULT_REGION_RULES = [2, 5, 7];

function parseRegionRuleTypes(raw: unknown): number[] {
	if (raw == null) return [...DEFAULT_REGION_RULES];
	const arr = Array.isArray(raw) ? raw : [raw];
	const out: number[] = [];
	for (const r of arr) {
		if (typeof r === 'number') { out.push(r); continue; }
		if (typeof r === 'string') {
			const normalized = r.trim().toLowerCase();
			if (/^\d+$/.test(normalized)) {
				out.push(Number(normalized));
				continue;
			}
			const v = REGION_RULE_BY_NAME[normalized];
			if (v == null) {
				throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD,
					`Unknown region ruleType "${r}". Use a name (${Object.keys(REGION_RULE_BY_NAME).join(', ')}) or an enum number.`);
			}
			out.push(v);
			continue;
		}
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'ruleType entries must be names or numbers.');
	}
	return out.length ? out : [...DEFAULT_REGION_RULES];
}

// closedPolygonFromPoints turns a payload `points` array (>= 3 [x,y] pairs, mil)
// into the explicitly-closed IPCB_Polygon that region/pour create() require.
function closedPolygonFromPoints(raw: unknown) {
	if (!Array.isArray(raw) || raw.length < 3) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'points must be an array of >= 3 [x,y] pairs (mil).');
	}
	const pts: Array<[number, number]> = [];
	for (const p of raw) {
		if (!Array.isArray(p) || p.length < 2 || typeof p[0] !== 'number' || typeof p[1] !== 'number') {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each point must be [x, y] numbers.');
		}
		pts.push([p[0], p[1]]);
	}
	const src: Array<number | string> = [pts[0][0], pts[0][1], 'L'];
	for (let i = 1; i < pts.length; i++) src.push(pts[i][0], pts[i][1]);
	src.push(pts[0][0], pts[0][1]);
	const poly = eda.pcb_MathPolygon.createPolygon(src as unknown as TPCB_PolygonSourceArray);
	if (!poly) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to build polygon from points (createPolygon returned undefined — points must form a valid closed polygon).');
	}
	return poly;
}

const pcbRegionCreate: Handler = async (payload) => {
	const poly = closedPolygonFromPoints(payload.points);
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfRegion;
	const ruleTypes = parseRegionRuleTypes(payload.ruleType ?? payload.ruleTypes);
	const regionName = optionalString(payload, 'name');
	const lineWidth = optionalNumber(payload, 'lineWidth');
	const lock = payload.locked === true;

	let region;
	try {
		region = await eda.pcb_PrimitiveRegion.create(
			layer, poly,
			ruleTypes as unknown as Array<EPCB_PrimitiveRegionRuleType>,
			regionName, lineWidth, lock,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to create PCB region (禁止区域).');
	}
	if (!region) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Region creation returned no primitive (check layer/points/ruleType).');
	}
	return {
		result: {
			primitiveId: region.getState_PrimitiveId(),
			layer: Number(layer),
			ruleType: ruleTypes,
			ruleTypeNames: ruleTypes.map(v => REGION_RULE_NAME[v] ?? String(v)),
			regionName: regionName ?? null,
		},
	};
};

const pcbRegionList: Handler = async (payload) => {
	const layer = optionalNumber(payload, 'layer');
	let regions;
	try {
		regions = await eda.pcb_PrimitiveRegion.getAll(
			layer == null ? undefined : (layer as unknown as TPCB_LayersOfRegion),
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB regions.');
	}
	const list: Array<Record<string, unknown>> = [];
	for (const r of (regions ?? [])) {
		const rules = (r.getState_RuleType() ?? []) as unknown as number[];
		let bbox;
		try {
			bbox = await eda.pcb_Primitive.getPrimitivesBBox([r.getState_PrimitiveId()]);
		}
		catch { /* bbox optional — used by the antenna keep-out check */ }
		list.push({
			primitiveId: r.getState_PrimitiveId(),
			layer: r.getState_Layer(),
			ruleType: rules,
			ruleTypeNames: rules.map(v => REGION_RULE_NAME[v] ?? String(v)),
			regionName: r.getState_RegionName() ?? null,
			geometryAvailable: (() => {
				try { return Array.isArray(r.getState_ComplexPolygon().getSource()); }
				catch { return false; }
			})(),
			source: (() => {
				try { return r.getState_ComplexPolygon().getSource(); }
				catch { return null; }
			})(),
			bbox,
			lineWidth: r.getState_LineWidth(),
			locked: r.getState_PrimitiveLock(),
		});
	}
	return { result: { regions: list, count: list.length } };
};

const pcbRegionDelete: Handler = async (payload) => {
	const raw = payload.primitiveIds;
	let ids: Array<string>;
	if (typeof raw === 'string') ids = [raw];
	else if (Array.isArray(raw) && raw.every(id => typeof id === 'string')) ids = raw as Array<string>;
	else throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required field "primitiveIds" (string or string[]).');

	let deleted;
	try {
		deleted = await eda.pcb_PrimitiveRegion.delete(ids);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete PCB regions.');
	}
	return { result: { deleted, primitiveIds: ids } };
};

// ─── PCB fill (填充区域 / net-bound solid copper, 异形大块铜) ──────────
// pcb_PrimitiveFill is a net-bound filled polygon (3V3/RF-ground patch, thermal
// copper, odd-shaped plane) — DISTINCT from a keep-out region (no net) AND from a
// pour (覆铜, which reflows around obstacles). A fill is a STATIC solid polygon on
// its net+layer. fillMode: solid(0) | mesh(1) | inner(2 = inner-electrical-layer).
// Same raw-points convention as pour/region.

const FILL_MODE_BY_NAME: Record<string, number> = { solid: 0, mesh: 1, grid: 1, inner: 2, 'inner-electrical': 2 };
const FILL_MODE_NAME: Record<number, string> = { 0: 'solid', 1: 'mesh', 2: 'inner-electrical' };

function parseFillMode(raw: unknown): number {
	if (raw == null) return 0;
	if (typeof raw === 'number') return raw;
	if (typeof raw === 'string') {
		const v = FILL_MODE_BY_NAME[raw.trim().toLowerCase()];
		if (v == null) throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `Unknown fillMode "${raw}". Use solid | mesh | inner.`);
		return v;
	}
	throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'fillMode must be a name or number.');
}

const pcbFillCreate: Handler = async (payload) => {
	const poly = closedPolygonFromPoints(payload.points);
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfFill;
	const net = optionalString(payload, 'net');
	const fillMode = parseFillMode(payload.fillMode) as unknown as EPCB_PrimitiveFillMode;
	const lineWidth = optionalNumber(payload, 'lineWidth');
	const lock = payload.locked === true;

	let fill;
	try {
		fill = await eda.pcb_PrimitiveFill.create(layer, poly, net, fillMode, lineWidth, lock);
	}
	catch (err) {
		throw edaError(err, 'Failed to create PCB fill (填充区域).');
	}
	if (!fill) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Fill creation returned no primitive (check layer/points/net).');
	}
	return {
		result: {
			primitiveId: fill.getState_PrimitiveId(),
			net: fill.getState_Net() ?? null,
			layer: Number(fill.getState_Layer()),
			fillMode: FILL_MODE_NAME[Number(fill.getState_FillMode() ?? 0)] ?? String(fill.getState_FillMode()),
		},
	};
};

const pcbFillList: Handler = async (payload) => {
	const layer = optionalNumber(payload, 'layer');
	const net = optionalString(payload, 'net');
	const includeBBox = optionalBoolean(payload, 'includeBBox') === true;
	let fills;
	try {
		fills = await eda.pcb_PrimitiveFill.getAll(
			layer == null ? undefined : (layer as unknown as TPCB_LayersOfFill),
			net,
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB fills.');
	}
	const list: Array<Record<string, unknown>> = [];
	for (const f of fills ?? []) {
		const id = f.getState_PrimitiveId();
		const item: Record<string, unknown> = {
			primitiveId: id,
			net: f.getState_Net() ?? null,
			layer: Number(f.getState_Layer()),
			fillMode: FILL_MODE_NAME[Number(f.getState_FillMode() ?? 0)] ?? String(f.getState_FillMode()),
			lineWidth: f.getState_LineWidth(),
			locked: f.getState_PrimitiveLock(),
		};
		try {
			item.source = f.getState_ComplexPolygon().getSource();
			item.geometryAvailable = Array.isArray(item.source);
		}
		catch {
			item.source = null;
			item.geometryAvailable = false;
		}
		if (includeBBox) {
			// Per-fill rendered extent — feeds `pcb check` via-bond (is this
			// junction covered by a bond fill?). Best-effort: null on failure.
			try {
				const box = await eda.pcb_Primitive.getPrimitivesBBox([id]);
				item.bbox = box ? { minX: box.minX, minY: box.minY, maxX: box.maxX, maxY: box.maxY } : null;
			}
			catch { item.bbox = null; }
		}
		list.push(item);
	}
	return { result: { fills: list, count: list.length } };
};

const pcbFillDelete: Handler = async (payload) => {
	const raw = payload.primitiveIds;
	let ids: Array<string>;
	if (typeof raw === 'string') ids = [raw];
	else if (Array.isArray(raw) && raw.every(id => typeof id === 'string')) ids = raw as Array<string>;
	else throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required field "primitiveIds" (string or string[]).');

	let deleted;
	try {
		deleted = await eda.pcb_PrimitiveFill.delete(ids);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete PCB fills.');
	}
	return { result: { deleted, primitiveIds: ids } };
};

// ─── PCB routing: list + rip-up ──────────────────────────────────────
// Reliable rip-up is hand-rolled (getAll → filter → delete) on the @public/@beta
// primitive APIs — the same pattern the official kirouting extension uses. It
// NEVER touches the board outline (layer 11) or locked primitives. clearRouting
// is the native @alpha alternative (may be undefined on this build).

const pcbLineList: Handler = async (payload) => {
	const net = optionalString(payload, 'net');
	const layer = optionalNumber(payload, 'layer') as unknown as TPCB_LayersOfLine | undefined;
	let lines;
	try {
		lines = await eda.pcb_PrimitiveLine.getAll(net, layer);
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB tracks.');
	}
	// Arcs are ALSO copper tracks (beautify rounds corners into track→arc→track).
	// Return them so headless checks (pcb.check dangling-end) can see a track
	// terminating on an arc endpoint as anchored, not floating. Best-effort: an
	// older API without pcb_PrimitiveArc must not break the line list.
	let arcs: Awaited<ReturnType<typeof eda.pcb_PrimitiveArc.getAll>> = [];
	let arcsAvailable = true;
	try { arcs = await eda.pcb_PrimitiveArc.getAll(net, layer); }
	catch {
		arcs = [];
		arcsAvailable = false;
	}
	const list = (lines ?? []).map(l => ({
		primitiveId: l.getState_PrimitiveId(),
		net: l.getState_Net(),
		layer: l.getState_Layer(),
		startX: l.getState_StartX(),
		startY: l.getState_StartY(),
		endX: l.getState_EndX(),
		endY: l.getState_EndY(),
		lineWidth: l.getState_LineWidth(),
		locked: l.getState_PrimitiveLock(),
	}));
	const arcList = (arcs ?? []).map(a => ({
		primitiveId: a.getState_PrimitiveId(),
		net: a.getState_Net(),
		layer: a.getState_Layer(),
		startX: a.getState_StartX(),
		startY: a.getState_StartY(),
		endX: a.getState_EndX(),
		endY: a.getState_EndY(),
		arcAngle: a.getState_ArcAngle(),
		lineWidth: a.getState_LineWidth(),
		locked: a.getState_PrimitiveLock(),
	}));
	return { result: { lines: list, arcs: arcList, arcsAvailable, count: list.length, arcCount: arcList.length } };
};

const pcbViaList: Handler = async (payload) => {
	const net = optionalString(payload, 'net');
	let vias;
	try {
		vias = await eda.pcb_PrimitiveVia.getAll(net);
	}
	catch (err) {
		throw edaError(err, 'Failed to list PCB vias.');
	}
	const list = (vias ?? []).map(v => ({
		primitiveId: v.getState_PrimitiveId(),
		net: v.getState_Net(),
		x: v.getState_X(),
		y: v.getState_Y(),
		holeDiameter: v.getState_HoleDiameter(),
		diameter: v.getState_Diameter(),
		locked: v.getState_PrimitiveLock(),
	}));
	return { result: { vias: list, count: list.length } };
};

// ─── pcb.track.lock (issue #127) ─────────────────────────────────────
// Lock (or unlock) copper routing primitives — tracks, arcs, vias — by net
// and/or explicit primitiveIds. The P7.0 critical-net flow routes power +
// diff pairs FIRST and locks them so a later auto-route / rip-up pass cannot
// destroy the hand-guaranteed copper (rip_up already skips locked primitives).
const pcbTrackLock: Handler = async (payload) => {
	const locked = optionalBoolean(payload, 'locked') ?? true;
	const all = optionalBoolean(payload, 'all') === true;
	const includeFills = optionalBoolean(payload, 'includeFills') ?? true;
	const rawNet = payload.net ?? payload.nets;
	let nets: Array<string> | null = null;
	if (typeof rawNet === 'string') nets = [rawNet];
	else if (Array.isArray(rawNet) && rawNet.every(n => typeof n === 'string')) nets = rawNet as Array<string>;
	const want = nets && nets.length > 0 ? new Set(nets.map(n => n.toUpperCase())) : null;
	const rawIds = payload.primitiveIds;
	const wantIds = Array.isArray(rawIds) && rawIds.every(i => typeof i === 'string') && rawIds.length > 0
		? new Set(rawIds as Array<string>) : null;
	if (!want && !wantIds && !all) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Provide "net" (string or string[]), "primitiveIds", or all=true — refusing to lock the whole board implicitly.');
	}

	let lines, arcs, vias, fills;
	try {
		lines = await eda.pcb_PrimitiveLine.getAll();
		arcs = await eda.pcb_PrimitiveArc.getAll();
		vias = await eda.pcb_PrimitiveVia.getAll();
		fills = includeFills ? await eda.pcb_PrimitiveFill.getAll().catch(() => []) : [];
	}
	catch (err) {
		throw edaError(err, 'Failed to read tracks/arcs/vias/fills for lock.');
	}

	// `all` still requires a NET (net === '' is the board outline / free artwork —
	// never locked implicitly). Pours are deliberately absent: they are meant to
	// reflow (pour-rebuild), locking them freezes stale geometry.
	const matches = (net: string, pid: string) => {
		if (wantIds !== null && wantIds.has(pid)) return true;
		if (want !== null) return want.has(net.toUpperCase());
		return all && net !== '';
	};
	const counts = { lines: 0, arcs: 0, vias: 0, fills: 0 };
	const failures: Array<string> = [];
	const apply = async (prims: Array<{ getState_PrimitiveId: () => string; getState_Net: () => string; getState_PrimitiveLock: () => boolean; setState_PrimitiveLock: (v: boolean) => unknown; done: () => Promise<unknown> }> | undefined, kind: 'lines' | 'arcs' | 'vias' | 'fills') => {
		for (const p of prims ?? []) {
			let pid = '';
			try {
				pid = p.getState_PrimitiveId();
				if (!matches(String(p.getState_Net() ?? ''), pid)) continue;
				if (p.getState_PrimitiveLock() === locked) { counts[kind]++; continue; } // already in the desired state
				p.setState_PrimitiveLock(locked);
				await p.done(); // pending state does not hit the canvas without done() (the #134 lesson)
				counts[kind]++;
			}
			catch {
				failures.push(pid || kind);
			}
		}
	};
	await apply(lines as never, 'lines');
	await apply(arcs as never, 'arcs');
	await apply(vias as never, 'vias');
	await apply(fills as never, 'fills');
	return { result: { locked, counts, total: counts.lines + counts.arcs + counts.vias + counts.fills, failures } };
};

const pcbRouteRipUp: Handler = async (payload) => {
	// Optional net filter (string or string[]); no net → rip up ALL routing.
	const rawNet = payload.net ?? payload.nets;
	let nets: Array<string> | null = null;
	if (typeof rawNet === 'string') nets = [rawNet];
	else if (Array.isArray(rawNet) && rawNet.every(n => typeof n === 'string')) nets = rawNet as Array<string>;
	const want = nets ? new Set(nets) : null;

	// COPPER layers only: TOP=1, BOTTOM=2, INNER_1..30 = 15..44. This excludes the
	// board outline (11) AND all silkscreen/assembly/mechanical/doc/custom artwork —
	// rip-up deletes COPPER routing only, never artwork (getAll() returns ALL layers).
	const onCopper = (layer: unknown) => { const n = Number(layer); return n === 1 || n === 2 || (n >= 15 && n <= 44); };

	let lines, arcs, vias;
	try {
		lines = await eda.pcb_PrimitiveLine.getAll();
		arcs = await eda.pcb_PrimitiveArc.getAll();
		vias = await eda.pcb_PrimitiveVia.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read tracks/arcs/vias for rip-up.');
	}

	// Never touch locked primitives (e.g. a locked board outline).
	const lineIds = (lines ?? [])
		.filter(l => (!want || want.has(l.getState_Net())) && onCopper(l.getState_Layer()) && !l.getState_PrimitiveLock())
		.map(l => l.getState_PrimitiveId());
	const arcIds = (arcs ?? [])
		.filter(a => (!want || want.has(a.getState_Net())) && onCopper(a.getState_Layer()) && !a.getState_PrimitiveLock())
		.map(a => a.getState_PrimitiveId());
	const viaIds = (vias ?? [])
		.filter(v => (!want || want.has(v.getState_Net())) && !v.getState_PrimitiveLock())
		.map(v => v.getState_PrimitiveId());

	// delete() returns an OVERALL boolean (a partial/failed batch is possible), so
	// report what we REQUESTED + the ok flag rather than asserting a deleted count.
	const ripDelete = async (
		kind: string,
		ids: Array<string>,
		fn: (ids: Array<string>) => Promise<boolean>,
	): Promise<{ requested: number; ok: boolean }> => {
		if (!ids.length) return { requested: 0, ok: true };
		try { return { requested: ids.length, ok: await fn(ids) }; }
		catch (err) { throw edaError(err, `Failed to delete ${kind} during rip-up.`); }
	};
	const linesRes = await ripDelete('tracks', lineIds, ids => eda.pcb_PrimitiveLine.delete(ids));
	const arcsRes = await ripDelete('arcs', arcIds, ids => eda.pcb_PrimitiveArc.delete(ids));
	const viasRes = await ripDelete('vias', viaIds, ids => eda.pcb_PrimitiveVia.delete(ids));

	return { result: { nets: nets ?? 'all', lines: linesRes, arcs: arcsRes, vias: viasRes } };
};

const pcbClearRouting: Handler = async (payload) => {
	const type = (optionalString(payload, 'type') ?? 'all') as 'all' | 'net' | 'connection';
	let cleared;
	try {
		cleared = await eda.pcb_Document.clearRouting(type);
	}
	catch (err) {
		throw edaError(err, 'clearRouting is @alpha and may be unavailable on this build — use pcb.route.rip_up for a reliable net-scoped rip-up.');
	}
	return { result: { cleared, type } };
};

// ─── PCB routing: surgical delete by primitiveId ─────────────────────
// rip_up is net-scoped (all-or-nothing per net); this deletes EXACTLY the
// tracks/arcs/vias named by id — the fix for "one bad via forces re-routing the
// whole net". Every removed primitive's full before-state is echoed in the
// result so the audit log holds enough to recreate it (recovery/replay).

const pcbRouteDelete: Handler = async (payload) => {
	const raw = payload.primitiveIds ?? payload.ids;
	let ids: Array<string>;
	if (typeof raw === 'string') ids = [raw];
	else if (Array.isArray(raw) && raw.every(id => typeof id === 'string') && raw.length > 0) ids = raw as Array<string>;
	else throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required field "primitiveIds" (string or non-empty string[]).');
	const kindGuard = optionalString(payload, 'kind'); // 'via' | 'track' — refuse ids of another kind

	let lines, arcs, vias;
	try {
		lines = await eda.pcb_PrimitiveLine.getAll();
		arcs = await eda.pcb_PrimitiveArc.getAll();
		vias = await eda.pcb_PrimitiveVia.getAll();
	}
	catch (err) {
		throw edaError(err, 'Failed to read routing primitives for delete.');
	}

	// id → {kind, locked, before} over ALL routing primitives on the board.
	type RouteEntry = { kind: 'track' | 'arc' | 'via'; locked: boolean; before: Record<string, unknown> };
	const byId = new Map<string, RouteEntry>();
	for (const l of lines ?? []) {
		byId.set(l.getState_PrimitiveId(), {
			kind: 'track', locked: l.getState_PrimitiveLock(),
			before: { net: l.getState_Net(), layer: Number(l.getState_Layer()), startX: l.getState_StartX(), startY: l.getState_StartY(), endX: l.getState_EndX(), endY: l.getState_EndY(), lineWidth: l.getState_LineWidth() },
		});
	}
	for (const a of arcs ?? []) {
		byId.set(a.getState_PrimitiveId(), {
			kind: 'arc', locked: a.getState_PrimitiveLock(),
			before: { net: a.getState_Net(), layer: Number(a.getState_Layer()) },
		});
	}
	for (const v of vias ?? []) {
		byId.set(v.getState_PrimitiveId(), {
			kind: 'via', locked: v.getState_PrimitiveLock(),
			before: { net: v.getState_Net(), x: v.getState_X(), y: v.getState_Y(), holeDiameter: v.getState_HoleDiameter(), diameter: v.getState_Diameter() },
		});
	}

	// #120 pre-check: a FOOTPRINT-EMBEDDED primitive's id is its parent
	// component's primitiveId plus a suffix (QFN EPAD thermal via ba45…f3e184
	// under component ba45…f3 — verified live). Deleting one is a lie twice
	// over: the SDK returns true, an immediate getAll even shows it gone, and
	// the next save/reload re-materializes it from the footprint definition.
	// So refuse UPFRONT — post-delete readback provably cannot catch this.
	let componentIds: Array<string> = [];
	try {
		const comps = await eda.pcb_PrimitiveComponent.getAll();
		componentIds = (comps ?? []).map(c => c.getState_PrimitiveId()).filter(Boolean);
	}
	catch { /* best-effort: without component ids the readback below still reports honestly */ }
	const embeddedParent = (id: string): string | undefined =>
		componentIds.find(cid => id !== cid && id.startsWith(cid));

	const toDelete: Record<'track' | 'arc' | 'via', Array<string>> = { track: [], arc: [], via: [] };
	const removed: Array<Record<string, unknown>> = [];
	const skippedLocked: Array<string> = [];
	const notFound: Array<string> = [];
	const wrongKind: Array<string> = [];
	const notDeletable: Array<Record<string, string>> = [];
	for (const id of ids) {
		const entry = byId.get(id);
		if (!entry) { notFound.push(id); continue; }
		if (kindGuard && entry.kind !== kindGuard) { wrongKind.push(`${id} is a ${entry.kind}`); continue; }
		if (entry.locked) { skippedLocked.push(id); continue; }
		const parent = embeddedParent(id);
		if (parent) {
			notDeletable.push({ primitiveId: id, parentComponent: parent, reason: 'footprint-embedded — the primitive API cannot delete it (delete claims success, the next reload re-materializes it); edit the footprint in EasyEDA or delete the whole component' });
			continue;
		}
		toDelete[entry.kind].push(id);
		removed.push({ primitiveId: id, kind: entry.kind, ...entry.before });
	}
	if (wrongKind.length) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `kind=${kindGuard} refused mismatched ids: ${wrongKind.join('; ')}. Drop the kind guard or fix the id list.`);
	}
	if (!removed.length) {
		if (notDeletable.length) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Nothing deletable: ${notDeletable.length} id(s) are FOOTPRINT-EMBEDDED (e.g. EPAD thermal vias — part of ${notDeletable[0].parentComponent}); the primitive API cannot delete them. To bond them to a net use \`easyeda pcb via-bond\`; to remove them edit the footprint or delete the component.`);
		}
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Nothing to delete: ${notFound.length} id(s) not found among routing primitives${skippedLocked.length ? `, ${skippedLocked.length} locked` : ''}. Pull fresh ids from pcb.line.list / pcb.via.list.`);
	}

	const results: Record<string, { requested: number; ok: boolean }> = {};
	const deleters: Record<'track' | 'arc' | 'via', (ids: Array<string>) => Promise<boolean>> = {
		track: batch => eda.pcb_PrimitiveLine.delete(batch),
		arc: batch => eda.pcb_PrimitiveArc.delete(batch),
		via: batch => eda.pcb_PrimitiveVia.delete(batch),
	};
	for (const kind of ['track', 'arc', 'via'] as const) {
		const batch = toDelete[kind];
		if (!batch.length) continue;
		try { results[`${kind}s`] = { requested: batch.length, ok: await deleters[kind](batch) }; }
		catch (err) { throw edaError(err, `Failed to delete ${kind}s.`); }
	}

	// #120: the SDK's delete() returns TRUE even when it removed nothing — a
	// FOOTPRINT-EMBEDDED via (a QFN EPAD thermal via is part of the component,
	// not a top-level primitive) survives with ok:true, and the agent walks on
	// believing the board changed. Read back every requested id and report the
	// truth: `deleted`/`count` reflect only what actually vanished; survivors
	// land in `notDeleted` with the reason. Readback is best-effort — if getAll
	// itself fails we keep the SDK verdicts rather than failing a delete that
	// may well have worked.
	const notDeleted: Array<string> = [];
	try {
		const [l2, a2, v2] = await Promise.all([
			toDelete.track.length ? eda.pcb_PrimitiveLine.getAll() : Promise.resolve([]),
			toDelete.arc.length ? eda.pcb_PrimitiveArc.getAll() : Promise.resolve([]),
			toDelete.via.length ? eda.pcb_PrimitiveVia.getAll() : Promise.resolve([]),
		]);
		const survivors = new Set<string>();
		for (const p of [...(l2 ?? []), ...(a2 ?? []), ...(v2 ?? [])]) survivors.add(p.getState_PrimitiveId());
		for (const kind of ['track', 'arc', 'via'] as const) {
			for (const id of toDelete[kind]) if (survivors.has(id)) notDeleted.push(id);
		}
	}
	catch { /* readback unavailable — SDK verdicts stand */ }

	const actuallyRemoved = removed.filter(r => !notDeleted.includes(r.primitiveId as string));
	const out: Record<string, unknown> = { deleted: results, removed: actuallyRemoved, count: actuallyRemoved.length, skippedLocked, notFound };
	if (notDeletable.length) {
		out.ok = false;
		out.notDeletable = notDeletable;
	}
	if (notDeleted.length) {
		out.ok = false;
		out.notDeleted = notDeleted;
		out.notDeletedReason = 'these primitives survived the delete (verified by readback) — most likely FOOTPRINT-EMBEDDED under a component the pre-check could not attribute; edit the footprint in EasyEDA or delete the whole component instead';
	}
	return { result: out };
};

// ─── PCB routing: via-hop (layer hop) ────────────────────────────────
// One command for "cross to the other layer and come back": stub → via → hop
// track → via → stub. Optional (off by default) net-bound bond fills over the
// vias. The fills were once thought load-bearing under pro-api-sdk#31, but that
// was our misdiagnosis — track↔via DOES register as connected (verified live
// 2026-07-07; the old "floating" symptom was stale pour connectivity, cured by
// re-pouring). So bondFill is now an opt-in extra, not a requirement. Rolls
// back everything it created on mid-sequence failure.

const pcbRouteViaHop: Handler = async (payload) => {
	const net = requireString(payload, 'net');
	const fromX = requireNumber(payload, 'fromX');
	const fromY = requireNumber(payload, 'fromY');
	const toX = requireNumber(payload, 'toX');
	const toY = requireNumber(payload, 'toY');
	const layer = (optionalNumber(payload, 'layer') ?? 1) as unknown as TPCB_LayersOfLine;
	const hopLayer = (optionalNumber(payload, 'hopLayer') ?? 2) as unknown as TPCB_LayersOfLine;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 6;
	const holeDiameter = optionalNumber(payload, 'holeDiameter') ?? 12;
	const viaDiameter = optionalNumber(payload, 'viaDiameter') ?? 24;
	const stub = optionalNumber(payload, 'stub') ?? 20;      // via setback from each endpoint (keeps vias OFF pads — via-on-pad ≠ connected)
	const bondFill = optionalBoolean(payload, 'bondFill') === true;
	const bondSize = optionalNumber(payload, 'bondSize') ?? 20; // square side, centered on each via

	if (Number(layer) === Number(hopLayer)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'layer and hopLayer must differ (a hop changes layers).');
	}
	const dx = toX - fromX, dy = toY - fromY;
	const dist = Math.hypot(dx, dy);
	if (dist <= 2 * stub + viaDiameter) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `from→to distance ${dist.toFixed(1)}mil is too short for a hop (needs > 2×stub + viaDiameter = ${2 * stub + viaDiameter}mil) — route it directly on one layer instead.`);
	}
	const ux = dx / dist, uy = dy / dist;
	const v1 = { x: fromX + ux * stub, y: fromY + uy * stub };
	const v2 = { x: toX - ux * stub, y: toY - uy * stub };

	// Track everything created so a mid-sequence failure rolls back cleanly.
	const created: { tracks: Array<string>; vias: Array<string>; fills: Array<string> } = { tracks: [], vias: [], fills: [] };
	const rollback = async () => {
		try {
			if (created.fills.length) await eda.pcb_PrimitiveFill.delete(created.fills);
			if (created.vias.length) await eda.pcb_PrimitiveVia.delete(created.vias);
			if (created.tracks.length) await eda.pcb_PrimitiveLine.delete(created.tracks);
		}
		catch { /* best-effort rollback */ }
	};

	const mkTrack = async (lyr: TPCB_LayersOfLine, x1: number, y1: number, x2: number, y2: number, what: string) => {
		const line = await eda.pcb_PrimitiveLine.create(net, lyr, x1, y1, x2, y2, lineWidth);
		if (!line) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `via-hop: ${what} track creation returned no primitive (check layer ids).`);
		created.tracks.push(line.getState_PrimitiveId());
	};
	const mkVia = async (p: { x: number; y: number }, what: string) => {
		const via = await eda.pcb_PrimitiveVia.create(net, p.x, p.y, holeDiameter, viaDiameter);
		if (!via) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `via-hop: ${what} creation returned no primitive.`);
		created.vias.push(via.getState_PrimitiveId());
	};
	const mkBond = async (p: { x: number; y: number }, lyr: TPCB_LayersOfLine) => {
		const h = bondSize / 2;
		const poly = closedPolygonFromPoints([[p.x - h, p.y - h], [p.x + h, p.y - h], [p.x + h, p.y + h], [p.x - h, p.y + h]]);
		const fill = await eda.pcb_PrimitiveFill.create(lyr as unknown as TPCB_LayersOfFill, poly, net, 0 as unknown as EPCB_PrimitiveFillMode, undefined, false);
		if (!fill) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'via-hop: bond fill creation returned no primitive.');
		created.fills.push(fill.getState_PrimitiveId());
	};

	try {
		await mkTrack(layer, fromX, fromY, v1.x, v1.y, 'entry stub');
		await mkVia(v1, 'via 1');
		await mkTrack(hopLayer, v1.x, v1.y, v2.x, v2.y, 'hop');
		await mkVia(v2, 'via 2');
		await mkTrack(layer, v2.x, v2.y, toX, toY, 'exit stub');
		if (bondFill) {
			for (const p of [v1, v2]) {
				await mkBond(p, layer);
				await mkBond(p, hopLayer);
			}
		}
	}
	catch (err) {
		await rollback();
		throw err instanceof ActionError ? err : edaError(err, 'via-hop failed mid-sequence; created primitives were rolled back.');
	}

	return {
		result: {
			net,
			layer: Number(layer),
			hopLayer: Number(hopLayer),
			vias: [{ ...v1 }, { ...v2 }],
			trackIds: created.tracks,
			viaIds: created.vias,
			fillIds: created.fills,
			bonded: bondFill,
			note: bondFill
				? 'optional bond fills placed on both layers of both vias (extra copper; NOT required for connectivity)'
				: 'no bond fills — track↔via registers as connected on its own (pro-api-sdk#31 was a misdiagnosis); verify with pcb.drc.check after a pour-rebuild',
		},
	};
};

// ─── Board outline (板框) ──────────────────────────────────────────────
// The board outline is one closed pcb_PrimitivePolyline on layer 11. EasyEDA's
// verified polygon source supports `ARC,signedSweep,endX,endY`; callers can pass
// that source directly so rounded corners stay native arcs. The layer is the
// numeric literal — EPCB_LayerId is a plain (non-const) enum that may not exist
// as a runtime global.
const BOARD_OUTLINE_LAYER = 11 as unknown as TPCB_LayersOfLine;

/** Ray-casting point-in-polygon over a closed ring of [x,y] points. */
function pointInPolygon(x: number, y: number, ring: Array<[number, number]>): boolean {
	let inside = false;
	for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
		const xi = ring[i][0], yi = ring[i][1];
		const xj = ring[j][0], yj = ring[j][1];
		if ((yi > y) !== (yj > y) && x < (xj - xi) * (y - yi) / (yj - yi) + xi) {
			inside = !inside;
		}
	}
	return inside;
}

function sourceIsClosed(source: Array<string | number>): boolean {
	if (source.length < 6 || typeof source[0] !== 'number' || typeof source[1] !== 'number') return false;
	const endX = source[source.length - 2];
	const endY = source[source.length - 1];
	return typeof endX === 'number' && Number.isFinite(endX)
		&& typeof endY === 'number' && Number.isFinite(endY)
		&& Math.abs(source[0] - endX) <= 1e-6
		&& Math.abs(source[1] - endY) <= 1e-6;
}

function outlineSourceCounts(source: Array<string | number>): { segments: number; arcs: number } {
	let segments = 0;
	let arcs = 0;
	let i = 2;
	while (i < source.length) {
		if (source[i] === 'ARC') {
			arcs++;
			i += 4;
			continue;
		}
		if (source[i] !== 'L') break;
		i++;
		while (i + 1 < source.length && typeof source[i] === 'number' && typeof source[i + 1] === 'number') {
			segments++;
			i += 2;
		}
	}
	return { segments, arcs };
}

/**
 * Recognize a closed four-line/four-quarter-arc rounded rectangle. EasyEDA may
 * rotate a saved polygon source so its first command is ARC instead of L, and
 * it quantizes persisted coordinates to 0.01 mil. Parse the whole cyclic path
 * instead of depending on the command chosen as its start.
 */
function roundedRectRadius(source: unknown): number | null {
	if (!Array.isArray(source) || source.length < 6
		|| typeof source[0] !== 'number' || !Number.isFinite(source[0])
		|| typeof source[1] !== 'number' || !Number.isFinite(source[1])) return null;
	type Segment = { kind: 'L' | 'ARC'; startX: number; startY: number; endX: number; endY: number; sweep?: number };
	const startX = source[0];
	const startY = source[1];
	let currentX = startX;
	let currentY = startY;
	const segments: Segment[] = [];
	let i = 2;
	while (i < source.length) {
		const command = source[i++];
		if (command === 'ARC') {
			const sweep = source[i++];
			const endX = source[i++];
			const endY = source[i++];
			if (typeof sweep !== 'number' || !Number.isFinite(sweep)
				|| typeof endX !== 'number' || !Number.isFinite(endX)
				|| typeof endY !== 'number' || !Number.isFinite(endY)) return null;
			segments.push({ kind: 'ARC', startX: currentX, startY: currentY, endX, endY, sweep });
			currentX = endX;
			currentY = endY;
			continue;
		}
		if (command !== 'L') return null;
		let points = 0;
		while (i + 1 < source.length && typeof source[i] === 'number' && typeof source[i + 1] === 'number') {
			const endX = source[i++] as number;
			const endY = source[i++] as number;
			if (!Number.isFinite(endX) || !Number.isFinite(endY)) return null;
			segments.push({ kind: 'L', startX: currentX, startY: currentY, endX, endY });
			currentX = endX;
			currentY = endY;
			points++;
		}
		if (points === 0) return null;
	}

	const closeTolerance = 1e-6;
	if (segments.length !== 8
		|| Math.abs(currentX - startX) > closeTolerance
		|| Math.abs(currentY - startY) > closeTolerance) return null;
	for (let index = 0; index < segments.length; index++) {
		if (segments[index].kind === segments[(index + 1) % segments.length].kind) return null;
	}

	const coordinateTolerance = 1e-6;
	const radiusTolerance = 0.011;
	const radii: number[] = [];
	let sweepSign = 0;
	for (const segment of segments) {
		const dx = segment.endX - segment.startX;
		const dy = segment.endY - segment.startY;
		if (segment.kind === 'L') {
			if (Math.hypot(dx, dy) <= coordinateTolerance
				|| (Math.abs(dx) > coordinateTolerance && Math.abs(dy) > coordinateTolerance)) return null;
			continue;
		}
		if (segment.sweep === undefined || Math.abs(Math.abs(segment.sweep) - 90) > coordinateTolerance
			|| Math.abs(dx) <= coordinateTolerance || Math.abs(dy) <= coordinateTolerance
			|| Math.abs(Math.abs(dx) - Math.abs(dy)) > radiusTolerance) return null;
		const sign = Math.sign(segment.sweep);
		if (sweepSign !== 0 && sign !== sweepSign) return null;
		sweepSign = sign;
		radii.push(Math.hypot(dx, dy) / Math.SQRT2);
	}
	if (radii.length !== 4 || radii.some(radius => !(radius > 0)
		|| Math.abs(radius - radii[0]) > radiusTolerance)) return null;
	return radii.reduce((sum, radius) => sum + radius, 0) / radii.length;
}

function centerlineBounds(points: Array<[number, number]>): { minX: number; maxX: number; minY: number; maxY: number } {
	const xs = points.map(p => p[0]);
	const ys = points.map(p => p[1]);
	return { minX: Math.min(...xs), maxX: Math.max(...xs), minY: Math.min(...ys), maxY: Math.max(...ys) };
}

/**
 * Set the board outline from either a closed polygon of points or a verified
 * EasyEDA polygon source (`L` + `ARC`). Replaces any existing outline and
 * reports whether every component falls inside.
 */
const pcbOutlineSet: Handler = async (payload) => {
	const rawPoints = payload.points;
	const rawSource = payload.source;
	if (rawPoints !== undefined && rawSource !== undefined) {
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Pass exactly one of points or source, not both.');
	}
	let points: Array<[number, number]>;
	let src: Array<string | number>;
	let outlineFormat: string;
	if (rawSource !== undefined) {
		const parsed = polygonSourceToPoints(rawSource);
		if (!Array.isArray(rawSource) || !parsed.points || !sourceIsClosed(rawSource as Array<string | number>)) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD,
				'source must be a closed EasyEDA polygon source using verified L/ARC commands.');
		}
		src = [...rawSource] as Array<string | number>;
		points = parsed.points;
		outlineFormat = parsed.format ?? 'unknown';
	}
	else {
		if (!Array.isArray(rawPoints) || rawPoints.length < 3) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'points must be an array of >= 3 [x,y] pairs (mil).');
		}
		points = [];
		for (const p of rawPoints) {
			if (!Array.isArray(p) || p.length < 2 || typeof p[0] !== 'number' || !Number.isFinite(p[0])
				|| typeof p[1] !== 'number' || !Number.isFinite(p[1])) {
				throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each point must be [x, y] finite numbers.');
			}
			points.push([p[0], p[1]]);
		}
		if (points.length > 3 && Math.abs(points[0][0] - points[points.length - 1][0]) <= 1e-6
			&& Math.abs(points[0][1] - points[points.length - 1][1]) <= 1e-6) points.pop();
		src = [points[0][0], points[0][1], 'L'];
		for (let i = 1; i < points.length; i++) src.push(points[i][0], points[i][1]);
		src.push(points[0][0], points[0][1]);
		outlineFormat = 'polyline';
	}
	const replace = optionalBoolean(payload, 'replace') !== false;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 10;
	if (!Number.isFinite(lineWidth) || lineWidth <= 0) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'lineWidth must be a finite number greater than zero.');
	}

	try {
		// THE board outline is ONE `pcb_PrimitivePolyline` (类型=板框) on layer 11 — NOT
		// a set of individual `pcb_PrimitiveLine`s. A loose line on the outline layer is
		// just a wire that happens to sit there: EasyEDA does NOT treat it as the board
		// boundary — DRC ignores it for enclosure, and the UI "清除布线 / clear routing"
		// deletes it (observed: the whole outline vanished). The polyline IS the board-
		// outline object (matches a UI-drawn 板框, verified against its IPCB_Polygon).
		// createPolygon converts the verified source into the IPCB_Polygon that
		// pcb_PrimitivePolyline.create requires.
		const poly = eda.pcb_MathPolygon.createPolygon(src as unknown as TPCB_PolygonSourceArray);
		if (!poly) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to build outline polygon (createPolygon returned undefined — points must form a valid closed polygon).');
		}

		if (replace) {
			// Remove any existing outline on layer 11: the proper polyline form AND
			// legacy individual lines/arcs (older builds drew the outline as lines).
			// Replacement must fail closed: a locked/stale old outline plus a newly
			// created one is worse than a reported failure because the returned
			// dimensions would describe only one of several board boundaries.
			const deleteAndConfirm = async (
				kind: string,
				getAll: () => Promise<Array<{ getState_PrimitiveId(): string }>>,
				remove: (ids: string[]) => Promise<boolean>,
			) => {
				const oldIds = (await getAll()).map(item => item.getState_PrimitiveId());
				if (!oldIds.length) return;
				const deleted = await remove(oldIds);
				if (!deleted) {
					throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
						`Failed to delete existing board-outline ${kind}; no replacement was created.`);
				}
				const remaining = new Set((await getAll()).map(item => item.getState_PrimitiveId()));
				const residual = oldIds.filter(id => remaining.has(id));
				if (residual.length) {
					throw new ActionError(ErrorCodes.INVALID_STATE,
						`Existing board-outline ${kind} remained after delete (${residual.join(', ')}); no replacement was created.`);
				}
			};
			await deleteAndConfirm(
				'polylines',
				() => eda.pcb_PrimitivePolyline.getAll(undefined, BOARD_OUTLINE_LAYER),
				ids => eda.pcb_PrimitivePolyline.delete(ids),
			);
			await deleteAndConfirm(
				'lines',
				() => eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER),
				ids => eda.pcb_PrimitiveLine.delete(ids),
			);
			await deleteAndConfirm(
				'arcs',
				() => eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER),
				ids => eda.pcb_PrimitiveArc.delete(ids),
			);
		}

		// Create the outline polyline LOCKED — a board outline must not move during
		// layout/routing and must survive clear-routing.
		const outline = await eda.pcb_PrimitivePolyline.create('', BOARD_OUTLINE_LAYER, poly, lineWidth, true);
		if (!outline) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Board outline creation returned no primitive (check points/layer).');
		}
		const outlineId = outline.getState_PrimitiveId();
		const counts = outlineSourceCounts(src);

		let zoomed = false;
		try { zoomed = await eda.pcb_Document.zoomToBoardOutline(); }
		catch { /* best-effort */ }

		const bbox = centerlineBounds(points);
		const radius = roundedRectRadius(src);

		// Best-effort enclosure check: any component whose bbox corner is outside.
		const outside: Array<string> = [];
		try {
			const comps = await eda.pcb_PrimitiveComponent.getAll();
			for (const c of comps) {
				const box = await eda.pcb_Primitive.getPrimitivesBBox([c.getState_PrimitiveId()]);
				if (!box) continue;
				const corners: Array<[number, number]> = [
					[box.minX, box.minY], [box.maxX, box.minY], [box.minX, box.maxY], [box.maxX, box.maxY],
				];
				if (corners.some(([x, y]) => !pointInPolygon(x, y, points))) {
					outside.push(c.getState_Designator() ?? c.getState_PrimitiveId());
				}
			}
		}
		catch { /* enclosure check is best-effort */ }

		return { result: {
			outlineId, segments: counts.segments, arcs: counts.arcs, zoomed, bbox,
			width: bbox.maxX - bbox.minX, height: bbox.maxY - bbox.minY, radius,
			lineWidth: outline.getState_LineWidth(), locked: outline.getState_PrimitiveLock(),
			outlineFormat, allInside: outside.length === 0, outside,
		} };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to set board outline.');
	}
};

/** Read the current board outline: the polyline (类型=板框) + its bounding box,
 * plus any legacy line/arc segments for backward compatibility. */
const pcbOutlineGet: Handler = async () => {
	let polylines, lines;
	try {
		polylines = await eda.pcb_PrimitivePolyline.getAll(undefined, BOARD_OUTLINE_LAYER);
		lines = await eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER);
	}
	catch (err) {
		throw edaError(err, 'Failed to read board outline.');
	}
	let arcCount = 0;
	try { arcCount = (await eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER)).length; }
	catch { /* best-effort */ }

	// The real outline is a polyline; its rendered bbox is the board extent. Fall
	// back to legacy line endpoints when no polyline exists.
	let bbox: Record<string, number> | null = null;
	if (polylines.length) {
		try { bbox = (await eda.pcb_Primitive.getPrimitivesBBox(polylines.map(p => p.getState_PrimitiveId()))) ?? null; }
		catch { /* bbox best-effort */ }
	}
	else if (lines.length) {
		let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
		for (const l of lines) {
			for (const [x, y] of [[l.getState_StartX(), l.getState_StartY()], [l.getState_EndX(), l.getState_EndY()]] as Array<[number, number]>) {
				minX = Math.min(minX, x); maxX = Math.max(maxX, x);
				minY = Math.min(minY, y); maxY = Math.max(maxY, y);
			}
		}
		bbox = { minX, maxX, minY, maxY };
	}
	// Real polygon points (#167). The bbox alone is an AABB: on a non-rectangular
	// board (Type-C sticking out, a notch, a milled cutout) "distance to the board
	// edge" computed from it is simply wrong — a part hugging the real edge of a
	// protrusion reads as far from the edge. The internal-on-edge / edge-IO layout
	// dimensions need the true boundary.
	//
	// NOTE the points are the outline's CENTER LINE, while the bbox is the RENDERED
	// extent and therefore includes the outline's line width (measured on ceshi:
	// 5 mil larger on every side for a 10-mil outline). The mill follows the center
	// line, so the points are the truthful board edge.
	let points: Array<[number, number]> | null = null;
	let outlineFormat: string | null = null;
	let centerlineBBox: { minX: number; maxX: number; minY: number; maxY: number } | null = null;
	let width: number | null = null;
	let height: number | null = null;
	let radius: number | null = null;
	let lineWidth: number | null = null;
	let locked: boolean | null = null;
	let sourceSegments: number | null = null;
	let sourceArcs: number | null = null;
	if (polylines.length > 0) {
		try {
			const sources = polylines.map(p => p.getState_Polygon()?.getSource());
			const selected = selectBoardOutlineSource(sources);
			points = selected.points;
			outlineFormat = selected.format;
			if (points && selected.index >= 0) {
				centerlineBBox = centerlineBounds(points);
				width = centerlineBBox.maxX - centerlineBBox.minX;
				height = centerlineBBox.maxY - centerlineBBox.minY;
				radius = roundedRectRadius(sources[selected.index]);
				lineWidth = polylines[selected.index].getState_LineWidth();
				locked = polylines[selected.index].getState_PrimitiveLock();
				const sourceCounts = outlineSourceCounts(sources[selected.index] as Array<string | number>);
				sourceSegments = sourceCounts.segments;
				sourceArcs = sourceCounts.arcs;
			}
		}
		catch { /* best-effort: callers fall back to the bbox */ }
	}

	// `outline` = the canonical polyline-based board outline; `segments`/`arcs` keep
	// reporting legacy line/arc counts so old boards still read sensibly.
	return { result: {
		outline: polylines.length, segments: lines.length, arcs: arcCount, legacyArcs: arcCount,
		sourceSegments, sourceArcs, nativeArcs: sourceArcs, bbox,
		points, outlineFormat, centerlineBBox, width, height, radius, lineWidth, locked,
	} };
};

/**
 * Flatten a TPCB_PolygonSourceArray into plain [x, y] points.
 *
 * The source array is an SVG-path-like flat mix of command tokens and numbers:
 * `[x0, y0, 'L', x1, y1, x2, y2, …]` — a start point followed by commands, where
 * `L` takes an arbitrary run of coordinate pairs (verified on a live ceshi board).
 * `ARC` is `["ARC", signedSweepDegrees, endX, endY]`: the start is the current
 * point, and the sign selects the sweep direction. This layout and the two-center
 * disambiguation were verified against 129 live board arcs (#215). Other curved
 * commands (`CARC`/`C`/`R`/`CIRCLE`) remain fail-closed until real samples prove
 * their layouts. A wrong boundary is worse than an admitted AABB approximation.
 *
 * `pcb outline-round` emits four verified quarter-circle ARC commands, so the
 * sampled points here are only for measurement/containment; the stored board
 * edge remains a native curved path.
 */
export function polygonSourceToPoints(src: unknown): { points: Array<[number, number]> | null; format: string | null } {
	if (!Array.isArray(src) || src.length < 6) return { points: null, format: 'empty' };
	const pts: Array<[number, number]> = [];
	let i = 0;
	if (typeof src[0] !== 'number' || !Number.isFinite(src[0]) || typeof src[1] !== 'number' || !Number.isFinite(src[1])) {
		return { points: null, format: `unexpected-start:${String(src[0])}` };
	}
	let currentX = src[0] as number;
	let currentY = src[1] as number;
	let hasArc = false;
	pts.push([currentX, currentY]);
	i = 2;
	while (i < src.length) {
		const tok = src[i];
		if (typeof tok !== 'string') return { points: null, format: `unexpected-token:${String(tok)}` };
		if (tok === 'L') {
			i++;
			let consumed = 0;
			while (i + 1 < src.length && typeof src[i] === 'number' && Number.isFinite(src[i]) && typeof src[i + 1] === 'number' && Number.isFinite(src[i + 1])) {
				currentX = src[i] as number;
				currentY = src[i + 1] as number;
				pts.push([currentX, currentY]);
				i += 2;
				consumed++;
			}
			if (consumed === 0) return { points: null, format: 'malformed-L' };
			continue;
		}
		if (tok !== 'ARC') return { points: null, format: `unsupported-command:${tok}` };
		if (i + 3 >= src.length || typeof src[i + 1] !== 'number' || !Number.isFinite(src[i + 1])
			|| typeof src[i + 2] !== 'number' || !Number.isFinite(src[i + 2])
			|| typeof src[i + 3] !== 'number' || !Number.isFinite(src[i + 3])) {
			return { points: null, format: 'malformed-ARC' };
		}

		const sweepDegrees = src[i + 1] as number;
		const endX = src[i + 2] as number;
		const endY = src[i + 3] as number;
		const absSweep = Math.abs(sweepDegrees);
		const chordX = endX - currentX;
		const chordY = endY - currentY;
		const chord = Math.hypot(chordX, chordY);
		const sineHalf = Math.sin(absSweep * Math.PI / 360);
		if (absSweep <= 0 || absSweep >= 360 || chord <= 1e-12 || sineHalf <= 1e-12) {
			return { points: null, format: 'malformed-ARC' };
		}
		const radius = chord / (2 * sineHalf);
		const midX = (currentX + endX) / 2;
		const midY = (currentY + endY) / 2;
		const centerOffset = Math.sqrt(Math.max(0, radius * radius - chord * chord / 4));
		const normalX = -chordY / chord;
		const normalY = chordX / chord;
		let best: { mismatch: number; centerX: number; centerY: number } | null = null;
		for (const sign of [1, -1]) {
			const centerX = midX + sign * centerOffset * normalX;
			const centerY = midY + sign * centerOffset * normalY;
			const startAngle = Math.atan2(currentY - centerY, currentX - centerX) * 180 / Math.PI;
			const endAngle = Math.atan2(endY - centerY, endX - centerX) * 180 / Math.PI;
			const directed = ((sweepDegrees >= 0 ? endAngle - startAngle : startAngle - endAngle) % 360 + 360) % 360;
			const mismatch = Math.abs(directed - absSweep);
			if (!best || mismatch < best.mismatch) best = { mismatch, centerX, centerY };
		}
		if (!best || best.mismatch >= 0.5) return { points: null, format: 'arc-sweep-mismatch' };

		const startRadians = Math.atan2(currentY - best.centerY, currentX - best.centerX);
		const steps = Math.max(2, Math.ceil(absSweep / 2)); // <=2° per chord, <0.01mm sagitta at the reported radii
		for (let step = 1; step < steps; step++) {
			const angle = startRadians + sweepDegrees * Math.PI / 180 * step / steps;
			pts.push([best.centerX + radius * Math.cos(angle), best.centerY + radius * Math.sin(angle)]);
		}
		pts.push([endX, endY]);
		currentX = endX;
		currentY = endY;
		hasArc = true;
		i += 4;
	}
	// A closed ring repeats its first point last; drop the duplicate so consumers
	// can treat the list as a plain ring without special-casing it.
	if (pts.length > 2) {
		const [fx, fy] = pts[0];
		const [lx, ly] = pts[pts.length - 1];
		if (Math.abs(fx - lx) < 1e-6 && Math.abs(fy - ly) < 1e-6) pts.pop();
	}
	if (pts.length < 3) return { points: null, format: 'degenerate' };
	return { points: pts, format: hasArc ? 'arc-polyline' : 'polyline' };
}

function polygonArea(points: Array<[number, number]>): number {
	let twice = 0;
	for (let i = 0, j = points.length - 1; i < points.length; j = i++) {
		twice += points[j][0] * points[i][1] - points[i][0] * points[j][1];
	}
	return Math.abs(twice) / 2;
}

function pointOnRing(x: number, y: number, ring: Array<[number, number]>): boolean {
	for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
		const ax = ring[j][0], ay = ring[j][1], bx = ring[i][0], by = ring[i][1];
		const dx = bx - ax, dy = by - ay;
		const length2 = dx * dx + dy * dy;
		const t = length2 <= 1e-18 ? 0 : Math.max(0, Math.min(1, ((x - ax) * dx + (y - ay) * dy) / length2));
		if (Math.hypot(x - (ax + t * dx), y - (ay + t * dy)) <= 1e-6) return true;
	}
	return false;
}

function segmentCrossesRing(a: [number, number], b: [number, number], ring: Array<[number, number]>): boolean {
	const cross = (p: [number, number], q: [number, number], r: [number, number]) =>
		(q[0] - p[0]) * (r[1] - p[1]) - (q[1] - p[1]) * (r[0] - p[0]);
	const onSegment = (p: [number, number], q: [number, number], r: [number, number]) =>
		Math.abs(cross(p, q, r)) <= 1e-9
		&& r[0] >= Math.min(p[0], q[0]) - 1e-9 && r[0] <= Math.max(p[0], q[0]) + 1e-9
		&& r[1] >= Math.min(p[1], q[1]) - 1e-9 && r[1] <= Math.max(p[1], q[1]) + 1e-9;
	for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
		const c = ring[j], d = ring[i];
		const abC = cross(a, b, c), abD = cross(a, b, d);
		const cdA = cross(c, d, a), cdB = cross(c, d, b);
		if (((abC > 1e-9 && abD < -1e-9) || (abC < -1e-9 && abD > 1e-9))
			&& ((cdA > 1e-9 && cdB < -1e-9) || (cdA < -1e-9 && cdB > 1e-9))) return true;
		if (onSegment(a, b, c) || onSegment(a, b, d) || onSegment(c, d, a) || onSegment(c, d, b)) return true;
	}
	return false;
}

function ringStrictlyInside(candidate: Array<[number, number]>, outer: Array<[number, number]>): boolean {
	if (!candidate.every(([x, y]) => pointInPolygon(x, y, outer) && !pointOnRing(x, y, outer))) return false;
	for (let i = 0, j = candidate.length - 1; i < candidate.length; j = i++) {
		if (segmentCrossesRing(candidate[j], candidate[i], outer)) return false;
	}
	return true;
}

type SelectedBoardOutlineSource = {
	points: Array<[number, number]> | null;
	format: string | null;
	index: number;
};

/** Select a proven outer ring and preserve its source index for primitive metadata. */
function selectBoardOutlineSource(sources: unknown[]): SelectedBoardOutlineSource {
	if (!Array.isArray(sources) || sources.length === 0) return { points: null, format: 'empty', index: -1 };
	const parsed = sources.map(polygonSourceToPoints);
	const failed = parsed.find(result => !result.points);
	if (failed) {
		return { points: null, format: sources.length === 1 ? failed.format : `ambiguous:${sources.length}-polylines:${failed.format}`, index: -1 };
	}
	if (parsed.length === 1) return { ...parsed[0], index: 0 };

	const ranked = parsed.map((result, index) => ({ ...result, index, area: polygonArea(result.points!) }))
		.sort((a, b) => b.area - a.area);
	if (ranked[0].area <= 1e-9 || Math.abs(ranked[0].area - ranked[1].area) <= 1e-9) {
		return { points: null, format: `ambiguous:${sources.length}-polylines`, index: -1 };
	}
	const outer = ranked[0].points!;
	// Vertex-only containment is insufficient for a concave outline: two inner
	// vertices can be legal while their connecting segment cuts across a notch.
	// Require every vertex strictly inside and every candidate edge disjoint from
	// the outer boundary before claiming a proven containing ring.
	const containsEveryOtherRing = ranked.slice(1).every(candidate => ringStrictlyInside(candidate.points!, outer));
	if (!containsEveryOtherRing) return { points: null, format: `ambiguous:${sources.length}-polylines`, index: -1 };
	return { points: outer, format: `${ranked[0].format};outer-of:${sources.length}`, index: ranked[0].index };
}

/** Select a proven outer ring from one or more outline-layer polygon sources. */
export function selectBoardOutlineSources(sources: unknown[]): { points: Array<[number, number]> | null; format: string | null } {
	const { points, format } = selectBoardOutlineSource(sources);
	return { points, format };
}

/** Remove the current board outline (all primitives on the BOARD_OUTLINE layer). */
const pcbOutlineClear: Handler = async () => {
	let removed = 0;
	try {
		const polylines = await eda.pcb_PrimitivePolyline.getAll(undefined, BOARD_OUTLINE_LAYER);
		if (polylines.length) {
			await eda.pcb_PrimitivePolyline.delete(polylines.map(p => p.getState_PrimitiveId()));
			removed += polylines.length;
		}
		const lines = await eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER);
		if (lines.length) {
			await eda.pcb_PrimitiveLine.delete(lines.map(l => l.getState_PrimitiveId()));
			removed += lines.length;
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to clear board outline.');
	}
	try {
		const arcs = await eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER);
		if (arcs.length) {
			await eda.pcb_PrimitiveArc.delete(arcs.map(a => a.getState_PrimitiveId()));
			removed += arcs.length;
		}
	}
	catch { /* best-effort */ }
	return { result: { removed } };
};

// ─── PCB canvas origin (display coordinates only) ─────────────────────

const pcbOriginGet: Handler = async () => {
	try {
		const origin = await eda.pcb_Document.getCanvasOrigin();
		if (!origin || !Number.isFinite(origin.offsetX) || !Number.isFinite(origin.offsetY)) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'PCB canvas origin returned invalid offsets.');
		}
		return { result: {
			offsetX: origin.offsetX,
			offsetY: origin.offsetY,
			affectsGeometry: false,
			note: 'Display/canvas origin only; PCB data coordinates and primitive geometry are unchanged.',
		} };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to read the PCB canvas origin.');
	}
};

const pcbOriginSet: Handler = async (payload) => {
	const offsetX = requireNumber(payload, 'offsetX');
	const offsetY = requireNumber(payload, 'offsetY');
	if (!Number.isFinite(offsetX) || !Number.isFinite(offsetY)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'offsetX and offsetY must be finite numbers in mil.');
	}
	try {
		const before = await eda.pcb_Document.getCanvasOrigin();
		const applied = await eda.pcb_Document.setCanvasOrigin(offsetX, offsetY);
		if (!applied) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'setCanvasOrigin returned false; the display origin was not confirmed.');
		}
		const after = await eda.pcb_Document.getCanvasOrigin();
		const verified = !!after
			&& Number.isFinite(after.offsetX) && Number.isFinite(after.offsetY)
			&& Math.abs(after.offsetX - offsetX) <= 1e-6
			&& Math.abs(after.offsetY - offsetY) <= 1e-6;
		if (!verified) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED,
				`Canvas origin readback mismatch: requested (${offsetX}, ${offsetY}), got (${String(after?.offsetX)}, ${String(after?.offsetY)}).`);
		}
		return { result: {
			offsetX: after.offsetX,
			offsetY: after.offsetY,
			previous: before ?? null,
			changed: !before || before.offsetX !== after.offsetX || before.offsetY !== after.offsetY,
			verified: true,
			affectsGeometry: false,
			note: 'Display/canvas origin changed; PCB data coordinates and primitive geometry were not moved.',
		} };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to set the PCB canvas origin.');
	}
};

// ─── View (editor canvas) ────────────────────────────────────────────
// All map to eda.dmt_EditorControl.*, which acts on the last-focused canvas
// (no tabId) and works on both schematic and PCB documents. These are the
// toolbar/keyboard view shortcuts (适应全部 `K`, 适应选中, zoom-to, region).

/** Zoom to fit all primitives — 适应全部 (the `K` shortcut). */
const viewFit: Handler = async () => {
	try {
		const region = await eda.dmt_EditorControl.zoomToAllPrimitives();
		if (region === false) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Canvas does not support fit-all (or no focused canvas).');
		}
		return { result: { region } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to fit all primitives.');
	}
};

/** Zoom to fit the currently selected primitives — 适应选中. */
const viewFitSelection: Handler = async () => {
	try {
		const region = await eda.dmt_EditorControl.zoomToSelectedPrimitives();
		if (region === false) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Canvas does not support fit-selection (or no focused canvas).');
		}
		return { result: { region } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to fit selection.');
	}
};

/** Pan/zoom to a center coordinate and/or scale ratio (percent). */
const viewZoom: Handler = async (payload) => {
	const x = optionalNumber(payload, 'x');
	const y = optionalNumber(payload, 'y');
	const scale = optionalNumber(payload, 'scale');
	try {
		const region = await eda.dmt_EditorControl.zoomTo(x, y, scale);
		if (region === false) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Canvas does not support this zoom (or no focused canvas).');
		}
		return { result: { region } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to zoom.');
	}
};

/** Zoom to a rectangular region (two X bounds, two Y bounds). */
const viewRegion: Handler = async (payload) => {
	const left = requireNumber(payload, 'left');
	const right = requireNumber(payload, 'right');
	const top = requireNumber(payload, 'top');
	const bottom = requireNumber(payload, 'bottom');
	// Order the bounds (and reject a zero-area box) before handing them to
	// zoomToRegion — a reversed/degenerate rectangle otherwise renders as a tiny
	// sliver in a mostly-blank frame (issue #20).
	const region = normalizeRegion(left, right, top, bottom);
	try {
		const ok = await eda.dmt_EditorControl.zoomToRegion(region.left, region.right, region.top, region.bottom);
		if (!ok) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Canvas does not support region zoom (or no focused canvas).');
		}
		return { result: { ok, region } };
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to zoom to region.');
	}
};

// ─── Debug escape hatch ──────────────────────────────────────────────

/**
 * Run arbitrary `eda.*` JavaScript. This is the deliberate escape hatch for
 * operations that have no typed action yet; the Skill confirmation-gates it.
 * Repeated debug snippets should be promoted to typed actions over time.
 */
const debugExecJs: Handler = async (payload) => {
	const code = requireString(payload, 'code');
	let fn: (eda: unknown) => Promise<unknown>;
	let value: unknown;
	try {
		const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
			new (arg: string, body: string): (eda: unknown) => Promise<unknown>;
		};
		fn = new AsyncFunction('eda', code);
	}
	catch (err) {
		// Construction has no access to eda and never executes the user's code.
		// Do not echo the script: it can contain credentials or private design data.
		throw new ActionError(ErrorCodes.PRECONDITION_REFUSED,
			'exec_js script could not be compiled; no code was executed. Fix JavaScript syntax and shell/JSON escaping before retrying.',
			describeThrown(err));
	}
	try {
		value = await fn(eda);
	}
	catch (err) {
		throw edaError(err, 'exec_js failed.');
	}
	// Preserve binary results across the JSON/WebSocket transport.  Returning a
	// Blob directly causes JSON.stringify to emit `{}` (and using `.text()` can
	// corrupt ZIP/Gerber bytes), so expose an explicit base64 envelope.
	if (value instanceof Blob) {
		return {
			result: {
				value: {
					__type: 'blob',
					mimeType: value.type || 'application/octet-stream',
					size: value.size,
					base64: await blobToBase64(value),
				},
			},
		};
	}
	if (value instanceof ArrayBuffer) {
		const bytes = new Uint8Array(value);
		return { result: { value: { __type: 'bytes', size: bytes.byteLength, base64: uint8ToBase64(bytes) } } };
	}
	if (ArrayBuffer.isView(value)) {
		const bytes = new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
		return { result: { value: { __type: 'bytes', size: bytes.byteLength, base64: uint8ToBase64(bytes) } } };
	}
	return { result: { value: value ?? null } };
};

// ─── Registry & dispatch ─────────────────────────────────────────────

const HANDLERS: Record<string, Handler> = {
	'system.page_reload': systemPageReload,
	'project.current': projectCurrent,
	'project.export_source': projectExportSource,
	'project.find': projectFind,
	'project.create': projectCreate,
	'project.open': projectOpen,
	'project.export': projectExport,
	'document.current': documentCurrent,
	'document.open': documentOpen,
	'document.close': documentClose,
	'view.fit': viewFit,
	'view.fit_selection': viewFitSelection,
	'view.zoom': viewZoom,
	'view.region': viewRegion,
	'schematic.pages.list': schematicPagesList,
	'schematic.page.open': schematicPageOpen,
	'schematic.page.create': schematicPageCreate,
	'schematic.page.rename': schematicPageRename,
	'schematic.page.delete': schematicPageDelete,
	'schematic.page.clear': schematicPageClear,
	'schematic.primitives.delete': schematicPrimitivesDelete,
	'schematic.rename': schematicRename,
	'schematic.titleblock.get': schematicTitleBlockGet,
	'schematic.titleblock.modify': schematicTitleBlockModify,
	'schematic.components.list': schematicComponentsList,
	'schematic.attribute.inspect': schematicAttributeInspect,
	'schematic.designators.list': schematicDesignatorsList,
	'schematic.component.place': schematicComponentPlace,
	'schematic.component.modify': schematicComponentModify,
	'schematic.component.delete': schematicComponentDelete,
	'schematic.wire.create': schematicWireCreate,
	'schematic.group.move': schematicGroupMove,
	'schematic.netflag.create': schematicNetflagCreate,
	'schematic.pin.set_no_connect': schematicPinSetNoConnect,
	'schematic.pin.disconnect': schematicPinDisconnect,
	'schematic.select': schematicSelect,
	'schematic.drc.check': schematicDrcCheck,
	'schematic.check': schematicCheck,
	'schematic.bridgeCheck': schematicBridgeCheck,
	'schematic.read': schematicRead,
	'schematic.save': schematicSave,
	'schematic.export.netlist': schematicExportNetlist,
	'schematic.export.image': schematicExportImage,
	'schematic.export.bom': schematicExportBom,
	'schematic.power.connect_pin': schematicPowerConnectPin,
	'schematic.library.search': schematicLibrarySearch,
	'schematic.library.get_by_lcsc': schematicLibraryGetByLcscIds,
	'library.list': libraryList,
	'library.footprint.create': libraryFootprintCreate,
	'library.footprint.get': libraryFootprintGet,
	'library.footprint.copy': libraryFootprintCopy,
	'library.footprint.delete': libraryFootprintDelete,
	'library.footprint.build': libraryFootprintBuild,
	'library.footprint.region_create': libraryFootprintRegionCreate,
	'library.symbol.create': librarySymbolCreate,
	'library.symbol.build': librarySymbolBuild,
	'library.symbol.get': librarySymbolGet,
	'library.symbol.export_source': librarySymbolExportSource,
	'library.symbol.delete': librarySymbolDelete,
	'library.model3d.create': libraryModel3DCreate,
	'library.model3d.get': libraryModel3DGet,
	'library.model3d.search': libraryModel3DSearch,
	'library.model3d.copy': libraryModel3DCopy,
	'library.model3d.delete': libraryModel3DDelete,
	'library.device.create': libraryDeviceCreate,
	'library.device.get': libraryDeviceGet,
	'library.device.set_model3d': libraryDeviceSetModel3D,
	'library.device.delete': libraryDeviceDelete,
	'schematic.rebind.footprint': schematicRebindFootprint,
	'schematic.rebind.symbol': schematicRebindSymbol,
	'schematic.component.replace': schematicComponentReplace,
	'schematic.component.resolve_lcsc': schematicComponentResolveLcsc,
	'schematic.text.list': schematicTextList,
	'pcb.documents.list': pcbDocumentsList,
	'pcb.components.list': pcbComponentsList,
	'pcb.layers.list': pcbLayersList,
	'pcb.layers.set_current': pcbLayerSetCurrent,
	'pcb.layers.visibility': pcbLayerVisibility,
	'pcb.view.side': pcbViewSide,
	'pcb.stackup.set': pcbStackupSet,
	'pcb.silk.align': pcbSilkAlign,
	'pcb.silk.list': pcbSilkList,
	'pcb.silk.add': pcbSilkAdd,
	'pcb.silk.import_svg': pcbSilkImportSvg,
	'pcb.silk.set': pcbSilkSet,
	'pcb.silk.netnames': pcbSilkNetnames,
	'pcb.silk.label_pads': pcbSilkLabelPads,
	'pcb.nets.list': pcbNetsList,
	'pcb.net_class.list': pcbNetClassList,
	'pcb.net_class.create': pcbNetClassCreate,
	'pcb.report': pcbReport,
	'pcb.constraint.list': pcbConstraintList,
	'pcb.differential_pair.create': pcbDiffPairCreate,
	'pcb.differential_pair.delete': pcbDiffPairDelete,
	'pcb.differential_pair.rename': pcbDiffPairRename,
	'pcb.equal_length_group.create': pcbEqGroupCreate,
	'pcb.equal_length_group.add_nets': pcbEqGroupAddNets,
	'pcb.equal_length_group.delete': pcbEqGroupDelete,
	'pcb.board.info': pcbBoardInfo,
	'board.list': boardList,
	'board.current': boardCurrent,
	'board.create': boardCreate,
	'board.new_pcb': pcbNewBoard,
	'system.notify': systemNotify,
	'board.rename': boardRename,
	'board.copy': boardCopy,
	'board.delete': boardDelete,
	'board.rebind': boardRebind,
	'pcb.import_changes': pcbImportChanges,
	'pcb.add_component': pcbAddComponent,
	'pcb.component.attrs_backfill': pcbComponentAttrsBackfill,
	'pcb.component.modify': pcbComponentModify,
	'pcb.component.lock': pcbComponentLock,
	'pcb.component.delete': pcbComponentDelete,
	'pcb.page.clear': pcbPageClear,
	'pcb.align': pcbAlign,
	'pcb.distribute': pcbDistribute,
	'pcb.grid_snap': pcbGridSnap,
	'pcb.components.move': pcbComponentsMove,
	'pcb.components.arrange': pcbComponentsArrange,
	'pcb.drc.check': pcbDrcCheck,
	'pcb.drc.rules': pcbDrcRules,
	'pcb.drc.rules.set': pcbDrcRulesSet,
	'pcb.config.get': pcbConfigGet,
	'pcb.config.set': pcbConfigSet,
	'pcb.net.color.set': pcbNetColorSet,
	'pcb.line.create': pcbLineCreate,
	'pcb.via.create': pcbViaCreate,
	'pcb.line.list': pcbLineList,
	'pcb.via.list': pcbViaList,
	'pcb.route.rip_up': pcbRouteRipUp,
	'pcb.track.lock': pcbTrackLock,
	'pcb.route.delete': pcbRouteDelete,
	'pcb.route.via_hop': pcbRouteViaHop,
	'pcb.clear_routing': pcbClearRouting,
	'pcb.pour.create': pcbPourCreate,
	'pcb.pour.list': pcbPourList,
	'pcb.poured.list': pcbPouredList,
	'pcb.pour.delete': pcbPourDelete,
	'pcb.pour.rebuild': pcbPourRebuild,
	'pcb.beautify': pcbBeautify,
	'pcb.region.create': pcbRegionCreate,
	'pcb.region.list': pcbRegionList,
	'pcb.region.delete': pcbRegionDelete,
	'pcb.fill.create': pcbFillCreate,
	'pcb.fill.list': pcbFillList,
	'pcb.fill.delete': pcbFillDelete,
	'pcb.save': pcbSave,
	'pcb.export.dsn': pcbExportDsn,
	'pcb.import_autoroute': pcbImportAutoroute,
	'pcb.view.filter.get': pcbViewFilterGet,
	'pcb.snapshot': pcbSnapshot,
	'pcb.outline.set': pcbOutlineSet,
	'pcb.outline.get': pcbOutlineGet,
	'pcb.outline.clear': pcbOutlineClear,
	'pcb.origin.get': pcbOriginGet,
	'pcb.origin.set': pcbOriginSet,
	'debug.exec_js': debugExecJs,
};

/**
 * Run the handler for an action, attaching best-effort context to the result.
 *
 * @param action - the action name
 * @param payload - the request payload (may be undefined)
 * @returns the action result with context attached
 */
export async function runAction(
	action: string,
	payload: Record<string, unknown> | undefined,
): Promise<ActionResult> {
	const handler = HANDLERS[action];
	if (!handler) {
		throw new ActionError(ErrorCodes.UNKNOWN_ACTION, `Unknown action "${action}".`);
	}
	if (typeof eda === 'undefined') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'The eda object is not available in this context.');
	}

	const result = await handler(asPayload(payload));
	if (!result.context) {
		result.context = await readResponseContext();
	}
	return result;
}
