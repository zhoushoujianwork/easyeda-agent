/**
 * Typed-action dispatch. Each action maps to exactly one (occasionally a small
 * cluster of) `eda.*` call(s), serializes the result to plain JSON, and returns
 * an `ActionResult`. Errors from `eda.*` are wrapped as `ActionError` with the
 * original message preserved in `detail`.
 */

import { documentTypeLabel, readResponseContext } from './eda-context';
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
	filterExactLcsc,
	isLcscQuery,
	newArtifactId,
	type NamedLibItem,
	normalizeRegion,
	normalizeWirePoints,
	optionalBoolean,
	optionalNumber,
	optionalString,
	pickNamedCandidate,
	requireNumber,
	requireString,
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
	const detail = err instanceof Error ? err.message : String(err);
	return new ActionError(ErrorCodes.EDA_CALL_FAILED, message, detail);
}

// ─── Serialization helpers ───────────────────────────────────────────

/**
 * Serialize a schematic component primitive to plain JSON using its public
 * getState_* accessors.
 *
 * NOTE: the `uniqueId`, `component`, `symbol`, and `footprint` fields are
 * placed-INSTANCE identifiers (sub-primitive ids of this specific placement).
 * They are NOT the device-library uuid that `schematicComponentPlace`
 * ({ libraryUuid, uuid }) expects — replaying one of them into `sch place`
 * makes `eda.sch_PrimitiveComponent.create` hang. To re-place the same part,
 * fetch a fresh device uuid via `schematicLibrarySearch` (lib search). The
 * connector exposes no replayable deviceUuid because `eda.sch_*` does not
 * surface the source device-library identity of a placed instance.
 *
 * @param component - the component primitive object
 * @returns a plain JSON record
 */
function serializeComponent(component: SchComponent): Record<string, unknown> {
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
	return {
		primitiveId: pin.getState_PrimitiveId(),
		pinNumber: pin.getState_PinNumber(),
		pinName: pin.getState_PinName(),
		x: pin.getState_X(),
		y: pin.getState_Y(),
		rotation: pin.getState_Rotation(),
		noConnected: pin.getState_NoConnected(),
	};
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

/**
 * Serialize a single PCB component pad to plain JSON. Pads carry the
 * net-by-name connectivity model that replaces schematic net flags.
 *
 * @param pad - the PCB component pad primitive object
 * @returns a plain JSON record
 */
function serializePcbPad(pad: PcbPad): Record<string, unknown> {
	return {
		primitiveId: pad.getState_PrimitiveId(),
		padNumber: pad.getState_PadNumber(),
		net: pad.getState_Net(),
		layer: pad.getState_Layer(),
		x: pad.getState_X(),
		y: pad.getState_Y(),
		rotation: pad.getState_Rotation(),
		padType: pad.getState_PadType(),
	};
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
	return { result: { tabId } };
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

/**
 * Modify the focused page's 明细表 (title block): toggle visibility and/or patch
 * fields. `titleBlockData` carries only the items to change; unknown keys are
 * ignored by EasyEDA, untouched items keep their current value.
 */
const schematicTitleBlockModify: Handler = async (payload) => {
	const showTitleBlock = optionalBoolean(payload, 'showTitleBlock');
	const titleBlockData = payload.titleBlockData;
	if (titleBlockData !== undefined && (typeof titleBlockData !== 'object' || titleBlockData === null)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Field "titleBlockData" must be an object.');
	}
	if (showTitleBlock === undefined && titleBlockData === undefined) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Pass at least one of "showTitleBlock" or "titleBlockData".');
	}
	let ok;
	try {
		ok = await eda.dmt_Schematic.modifySchematicPageTitleBlock(
			showTitleBlock,
			titleBlockData as Parameters<typeof eda.dmt_Schematic.modifySchematicPageTitleBlock>[1],
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to modify schematic page title block.');
	}
	return { result: { ok } };
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
	return {
		result: {
			ok,
			verified: false,
			warning: '重命名已提交，但页面列表元数据尚未同步为新名（EasyEDA 平台异步缓存，issue #55）；'
				+ '请稍后重试或触发任意其他写操作后再用 doc ls 确认。',
		},
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

const schematicComponentsList: Handler = async (payload) => {
	const allPages = optionalBoolean(payload, 'allPages') === true;
	const includePins = optionalBoolean(payload, 'includePins') === true;
	// includeBBox attaches each component's rendered extent {minX,minY,maxX,maxY}
	// (via eda.sch_Primitive.getPrimitivesBBox) so the agent / `sch layout-lint`
	// can reason about size, spacing, and overlap — mirrors pcb.components.list.
	const includeBBox = optionalBoolean(payload, 'includeBBox') === true;
	let components;
	try {
		components = await eda.sch_PrimitiveComponent.getAll(undefined, allPages);
	}
	catch (err) {
		throw edaError(err, 'Failed to list schematic components.');
	}

	const serialized: Array<Record<string, unknown>> = [];
	for (const component of components) {
		const record = serializeComponent(component);
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
				record.pins = (pins ?? []).map(serializePin);
			}
			catch { /* pins are optional */ }
		}
		serialized.push(record);
	}

	return { result: { components: serialized, count: serialized.length } };
};

const schematicComponentPlace: Handler = async (payload) => {
	const libraryUuid = requireString(payload, 'libraryUuid');
	const uuid = requireString(payload, 'uuid');
	const x = requireNumber(payload, 'x');
	const y = requireNumber(payload, 'y');
	const subPartName = optionalString(payload, 'subPartName');
	const rotation = optionalNumber(payload, 'rotation');
	const mirror = optionalBoolean(payload, 'mirror');
	const addIntoBom = optionalBoolean(payload, 'addIntoBom');
	const addIntoPcb = optionalBoolean(payload, 'addIntoPcb');

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
	return {
		result: {
			primitiveId: component.getState_PrimitiveId(),
			component: serializeComponent(component),
		},
	};
};

const schematicComponentModify: Handler = async (payload) => {
	const primitiveId = requireString(payload, 'primitiveId');
	const patch = payload.patch;
	if (typeof patch !== 'object' || patch === null) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required object field "patch".');
	}

	let component;
	try {
		component = await eda.sch_PrimitiveComponent.modify(
			primitiveId,
			patch as Parameters<typeof eda.sch_PrimitiveComponent.modify>[1],
		);
	}
	catch (err) {
		throw edaError(err, 'Failed to modify component.');
	}
	if (!component) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to modify component "${primitiveId}".`);
	}
	return { result: { component: serializeComponent(component) } };
};

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
	let deleted;
	try {
		deleted = await eda.sch_PrimitiveComponent.delete(primitiveIds);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete components.');
	}
	return { result: { deleted } };
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
	return `${label}: ${err instanceof Error ? err.message : String(err)}`;
}

/** Delete a group of ids via its owning class (components fall through to the component class). */
async function deleteSchGroup(key: string, ids: Array<string>): Promise<void> {
	const kind = SCH_PAGE_PRIMITIVE_KINDS.find(k => k.key === key);
	if (kind) await kind.del(ids);
	else await eda.sch_PrimitiveComponent.delete(ids);
}

/**
 * Clear the ACTIVE schematic page — delete every page-level primitive, not just
 * components. `schematic.component.delete` leaves wires/buses/graphics behind
 * (forcing a fall back to raw `debug.exec_js`); this enumerates every
 * `sch_Primitive*` class so a page reset is actually clean. `preserveSheet`
 * (default true) keeps the sheet/title block; `dryRun` counts without deleting.
 * No undo.
 */
const schematicPageClear: Handler = async (payload) => {
	const preserveSheet = optionalBoolean(payload, 'preserveSheet') !== false;
	const dryRun = optionalBoolean(payload, 'dryRun') === true;

	const idsByKey: Record<string, Array<string>> = {};
	const warnings: Array<string> = [];

	// 1) Components — net flags/ports/labels are components too, so this single
	//    class covers them all. Honor preserveSheet by skipping the sheet.
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

	// 2) Wires, buses, and graphics — each its own class.
	for (const kind of SCH_PAGE_PRIMITIVE_KINDS) {
		try {
			for (const p of await kind.getAll()) (idsByKey[kind.key] ??= []).push(p.getState_PrimitiveId());
		}
		catch (err) {
			warnings.push(warnText(`enumerate ${kind.key}`, err));
		}
	}

	// 3) Delete (unless dryRun).
	if (!dryRun) {
		for (const [key, ids] of Object.entries(idsByKey)) {
			if (!ids.length) continue;
			try {
				await deleteSchGroup(key, ids);
			}
			catch (err) {
				warnings.push(warnText(`delete ${key}`, err));
			}
		}
	}

	const deleted: Record<string, number> = {};
	let total = 0;
	for (const [key, ids] of Object.entries(idsByKey)) { deleted[key] = ids.length; total += ids.length; }

	return {
		result: {
			deleted,
			total,
			deletedIds: idsByKey,
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

	const deleted: Record<string, number> = {};
	let total = 0;
	for (const [key, ids] of Object.entries(idsByKey)) { deleted[key] = ids.length; total += ids.length; }

	return {
		result: {
			deleted,
			total,
			deletedIds: idsByKey,
			...(notFound.length ? { notFound } : {}),
			...(warnings.length ? { warnings } : {}),
		},
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

	const movedComponents: Array<Record<string, unknown>> = [];
	const movedWires: Array<Record<string, unknown>> = [];
	const notFound: Array<string> = [];
	const seen = new Set<string>();

	for (const comp of allComponents) {
		const id = comp.getState_PrimitiveId();
		if (!wantIds.has(id)) continue;
		seen.add(id);
		const from = { x: comp.getState_X(), y: comp.getState_Y() };
		const to = { x: from.x + dx, y: from.y + dy };
		let moved;
		try { moved = await eda.sch_PrimitiveComponent.modify(id, { x: to.x, y: to.y }); }
		catch (err) { throw edaError(err, `group-move: failed to translate component ${id}.`); }
		if (!moved) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: modify returned no primitive for component ${id}.`);
		movedComponents.push({ primitiveId: id, designator: moved.getState_Designator?.() ?? null, from, to });
	}

	for (const wire of allWires) {
		const id = wire.getState_PrimitiveId();
		if (!wantIds.has(id)) continue;
		seen.add(id);
		const line = normalizeWirePoints(wire.getState_Line());
		const shifted = line.map((v, i) => (i % 2 === 0 ? v + dx : v + dy));
		const net = wire.getState_Net();
		const color = wire.getState_Color();
		const lineWidth = wire.getState_LineWidth();
		const lineType = wire.getState_LineType();
		try { await eda.sch_PrimitiveWire.delete([id]); }
		catch (err) { throw edaError(err, `group-move: failed to remove old wire ${id} before recreating it shifted.`); }
		let created;
		try { created = await eda.sch_PrimitiveWire.create(shifted, net, color, lineWidth, lineType); }
		catch (err) { throw edaError(err, `group-move: failed to recreate wire ${id} at the shifted position (original was deleted — rerun with the same spec to retry).`); }
		if (!created) throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: recreating wire ${id} returned no primitive (original was deleted).`);
		movedWires.push({ oldPrimitiveId: id, newPrimitiveId: created.getState_PrimitiveId(), net });
	}

	for (const id of wantIds) {
		if (!seen.has(id)) notFound.push(id);
	}

	if (!movedComponents.length && !movedWires.length) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `group-move: none of the ${wantIds.size} id(s) resolved to a component or wire. Pull fresh ids first.`);
	}

	return {
		result: {
			dx, dy,
			movedComponents,
			movedWires,
			count: movedComponents.length + movedWires.length,
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
			component: serializeComponent(component),
		},
	};
};

// ─── No-connect flag (非连接标识) ───────────────────────────────────────
//
// A no-connect mark is NOT a standalone primitive — it is a PIN STATE.
// `pin.setState_NoConnected(true)` both renders the X marker on the pin and
// tells DRC the pin is intentionally unconnected (so it stops reporting the
// "un-connected pin" error). `setState_NoConnected` is the only @public mutator
// on a component pin besides pinNumber. Pins are reachable ONLY via
// getAllPinsByPrimitiveId(component primitiveId), so we resolve the component by
// designator first, then the pin(s) by pin number — how an engineer names them
// ("U1 pin 23 is NC"). Pass noConnected=false to clear the mark.
const schematicPinSetNoConnect: Handler = async (payload) => {
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

	let pins;
	try {
		pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(cid);
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
			byNumber.get(n)!.setState_NoConnected(value);
		}
		catch (err) {
			throw edaError(err, `Failed to set no-connect on ${designator} pin ${n}.`);
		}
	}

	// Re-pull fresh to confirm the STORED state — an immediate getState off the
	// just-mutated handle can echo the input (same trap as createNetFlag rotation).
	let confirmed: Array<{ pin: string; noConnected: boolean | null }>;
	try {
		const fresh = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(cid);
		const freshByNumber = new Map((fresh ?? []).map(p => [p.getState_PinNumber(), p]));
		confirmed = wantPins.map(n => ({
			pin: n,
			noConnected: freshByNumber.get(n)?.getState_NoConnected() ?? null,
		}));
	}
	catch {
		// Could not re-pull — fall back to the optimistic value, and let the
		// no-op guard below skip (we can't prove it failed without a read).
		confirmed = wantPins.map(n => ({ pin: n, noConnected: value }));
	}

	// VERIFY-OR-FAIL. On EasyEDA Pro 3.2.x, pin.setState_NoConnected is a NO-OP:
	// the pin primitive has no `noConnected` field (sch_PrimitivePin.get exposes
	// none), the @public setter silently does nothing, and a re-pull / DRC re-run /
	// canvas snapshot all confirm no NC mark is ever placed. The setter type is
	// marked @public so this compiles and "succeeds" — which is exactly why it
	// silently lied before. Detect the no-op and fail loudly instead, naming it as
	// a platform limitation (not a connector bug) so the caller doesn't trust a
	// phantom result. If a future EDA build makes the setter real, this guard
	// passes automatically.
	const notApplied = confirmed.filter(c => c.noConnected !== value);
	if (notApplied.length === wantPins.length) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`EasyEDA did not apply no-connect to ${designator} pin(s) ${wantPins.join(', ')}: `
			+ `pin.setState_NoConnected is a no-op on this EDA build (verified by re-pull). The pin `
			+ `primitive has no noConnected field, so DRC still treats these pins as floating. This is `
			+ `an EasyEDA platform limitation, not a connector defect — there is no public API to place `
			+ `a 非连接标识 on this version.`,
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

const schematicSnapshot: Handler = async (payload) => {
	const tabId = optionalString(payload, 'tabId');
	// Auto fit-to-all before capturing (适应全部) is ON by default so the whole
	// sheet lands in frame without the caller having to issue a separate view.fit
	// — pass fit=false to keep the current viewport. Best-effort: a failure must
	// not block the capture. Bonus: changing the viewport also nudges EasyEDA to
	// redraw, which mitigates the stale-frame problem documented below.
	const fit = optionalBoolean(payload, 'fit') !== false;
	// Optional sha256 of the PREVIOUS snapshot (caller threads it back in). When
	// present we can DETECT a stale frame ourselves (issue #20) instead of only
	// emitting advisory text: if the canvas state changed but the image bytes are
	// byte-identical, the capture is stale — we retry once after another settle.
	const previousSha = optionalString(payload, 'previousSha256');
	let fitted = false;
	if (fit) {
		try {
			await eda.dmt_EditorControl.zoomToAllPrimitives();
			fitted = true;
		}
		catch {
			/* best-effort — fall through and capture at the current viewport */
		}
	}

	// Let any pending viewport change (a preceding `view region`/`view zoom`, or
	// the zoomToAllPrimitives above) commit + repaint before we read the frame.
	// Without this the --no-fit path captures the PRE-region viewport (issue #20).
	await waitForCanvasSettle();

	const capture = async (): Promise<Blob> => {
		let b;
		try {
			b = await eda.dmt_EditorControl.getCurrentRenderedAreaImage(tabId);
		}
		catch (err) {
			throw edaError(err, 'Failed to capture snapshot.');
		}
		if (!b) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Snapshot returned no image.');
		}
		return b;
	};

	let blob = await capture();
	let sha256 = await blobSha256(blob);
	// Built-in stale detection: if the caller told us the prior frame's sha and we
	// got the exact same bytes back, the canvas almost certainly didn't repaint —
	// give it one more settle + recapture before reporting.
	let staleRetry = false;
	if (previousSha && sha256 && sha256 === previousSha) {
		staleRetry = true;
		await waitForCanvasSettle();
		blob = await capture();
		sha256 = await blobSha256(blob);
	}
	const stale = Boolean(previousSha && sha256 && sha256 === previousSha);

	const artifact = await blobToArtifact(blob, 'schematic_snapshot', 'snapshot.png', 'image/png');
	// Anti-stale metadata (issue #2/#20): EasyEDA does NOT auto-redraw after eda.*
	// edits, so getCurrentRenderedAreaImage can return a byte-identical STALE
	// frame. We now (a) settle the canvas before capturing, (b) expose the frame
	// `sha256` so the caller can thread it back as `previousSha256` to let us
	// detect+retry a stale frame, and (c) still surface `primitiveCount` for the
	// data-over-picture judgement.
	const primitiveCount = await countLivePagePrimitives();
	return {
		result: {
			artifactId: artifact.id,
			primitiveCount,
			fitted,
			sha256,
			stale,
			staleRetry,
			capturedAt: new Date().toISOString(),
			staleHint: 'EasyEDA may not auto-redraw after API edits. Thread this sha256 back as previousSha256 on the next snapshot to auto-detect a stale frame; judge state by data, screenshot for layout only.',
		},
		artifacts: [artifact],
	};
};

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
	// `includeVerboseError` selects the SDK overload: the literal `true` overload
	// returns the violations array (what we normalize); the literal `false` one
	// returns a bare boolean. Default true so we always get detail — and ACTUALLY
	// read the payload field (it used to be hardcoded `true`, so the CLI flag was
	// silently ignored). The overloads demand a literal arg, hence two branches.
	// issue #7
	const includeVerbose = optionalBoolean(payload, 'includeVerboseError') !== false;
	if (!includeVerbose) {
		// Non-verbose overload: a bare boolean with no per-item detail. Returned
		// verbatim as `passed` (raw debug callers only — the CLI always asks for
		// the verbose/array form).
		let ok: boolean;
		try {
			ok = await eda.sch_Drc.check(strict, false, false);
		}
		catch (err) {
			throw edaError(err, 'Failed to run DRC.');
		}
		return {
			result: {
				passed: ok,
				fatal: 0,
				summary: { fatal: 0, error: 0, warn: 0, info: 0, unknown: 0, total: 0 },
				violations: [],
				raw: ok,
			},
		};
	}
	let violations: unknown;
	try {
		violations = await eda.sch_Drc.check(strict, false, true);
	}
	catch (err) {
		throw edaError(err, 'Failed to run DRC.');
	}
	// Normalize: expand each violation to {level, rule, message, ids, x, y} +
	// a severity summary so callers can locate issues and gate on fatal count,
	// instead of seeing only the SDK's aggregate `{count, type}` groups. issue #7
	return { result: normalizeDrc(violations) };
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
	type: string; // 'floating-pin' | 'geom-net-mismatch' | 'wire-crossing' | 'wire-over-pin' | 'net-marker-mismatch' | 'multi-net-wire' | 'zero-length-wire' | 'dangling-wire'
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
	count?: number;
	message?: string;
	at?: { x: number; y: number }; // location of a crossing / through-pin
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

// True if (px,py) lies on the segment (x1,y1)-(x2,y2) — endpoints included.
function pointOnSegment(px: number, py: number, x1: number, y1: number, x2: number, y2: number): boolean {
	if (px < Math.min(x1, x2) - CHECK_EPS || px > Math.max(x1, x2) + CHECK_EPS) return false;
	if (py < Math.min(y1, y2) - CHECK_EPS || py > Math.max(y1, y2) + CHECK_EPS) return false;
	const cross = (px - x1) * (y2 - y1) - (py - y1) * (x2 - x1);
	return Math.abs(cross) <= CHECK_EPS * Math.max(1, Math.hypot(x2 - x1, y2 - y1));
}

interface CheckWireSegment {
	seg: Seg;
	wirePrimitiveId: string;
	net: string;
}

interface NetlistPinInfo {
	net?: string;
}

interface NetlistComponentInfo {
	props?: Record<string, unknown>;
	pinInfoMap?: Record<string, NetlistPinInfo>;
}

// Flatten every wire's polyline (getState_Line → flat [x0,y0,x1,y1,…]) into segments.
function collectWireSegments(wires: Array<{ getState_Line: () => Array<number>; getState_Net?: () => string; getState_PrimitiveId?: () => string }>): Array<CheckWireSegment> {
	const segs: Array<CheckWireSegment> = [];
	for (const w of wires) {
		let line: Array<number> | undefined;
		try { line = w.getState_Line(); }
		catch { continue; }
		if (!Array.isArray(line)) continue;
		let wirePrimitiveId = '';
		let net = '';
		try { wirePrimitiveId = String(w.getState_PrimitiveId?.() ?? ''); }
		catch { /* optional */ }
		try { net = String(w.getState_Net?.() ?? ''); }
		catch { /* optional */ }
		for (let i = 0; i + 3 < line.length; i += 2) {
			segs.push({ seg: [line[i], line[i + 1], line[i + 2], line[i + 3]], wirePrimitiveId, net });
		}
	}
	return segs;
}

// Result of reading the JSON-authoritative netlist. `available` distinguishes
// "netlist fetched+parsed" (trust its pin→net facts, even the ABSENCE of a net)
// from "couldn't fetch/parse" (netlist muted → geometry alone decides). Without
// this flag an uncompiled/missing netlist would look like "every pin has no net"
// and manufacture geom-net-mismatch false reports.
interface NetlistPinNets {
	byDesignator: Map<string, Map<string, string>>;
	available: boolean;
}

async function collectNetlistPinNets(): Promise<NetlistPinNets> {
	const byDesignator = new Map<string, Map<string, string>>();
	const muted = (): NetlistPinNets => ({ byDesignator, available: false });
	let file: File | undefined;
	try { file = await eda.sch_ManufactureData.getNetlistFile(); }
	catch { return muted(); }
	if (!file) return muted();
	let parsed: unknown;
	try { parsed = JSON.parse(await file.text()); }
	catch { return muted(); }
	const components = (parsed as { components?: Record<string, NetlistComponentInfo> })?.components;
	if (!components || typeof components !== 'object') return muted();
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

type Seg = [number, number, number, number];

// Signed orientation of point C relative to directed segment A→B; 0 = collinear
// (within eps), ±1 = the two sides. Used for the proper-intersection test.
function orient(ax: number, ay: number, bx: number, by: number, cx: number, cy: number): number {
	const v = (by - ay) * (cx - bx) - (bx - ax) * (cy - by);
	return v > CHECK_EPS ? 1 : v < -CHECK_EPS ? -1 : 0;
}

// True only when two segments cross in BOTH interiors (a real routing tangle).
// Shared endpoints, T-junctions, and collinear overlaps give a 0 orientation and
// are excluded — those are legitimate (wires meet at pins/junctions).
function segmentsProperlyCross(s1: Seg, s2: Seg): boolean {
	const [a, b, c, d] = s1;
	const [e, f, g, h] = s2;
	const o1 = orient(a, b, c, d, e, f);
	const o2 = orient(a, b, c, d, g, h);
	const o3 = orient(e, f, g, h, a, b);
	const o4 = orient(e, f, g, h, c, d);
	return o1 !== 0 && o2 !== 0 && o3 !== 0 && o4 !== 0 && o1 !== o2 && o3 !== o4;
}

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
	const wireSegs = collectWireSegments((wires ?? []) as Array<{ getState_Line: () => Array<number>; getState_Net?: () => string; getState_PrimitiveId?: () => string }>);
	const segs = wireSegs.map(w => w.seg);

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
		catch { continue; }
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
		catch { /* marker without coords — skip */ }
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
			const touchesEndpoint = Math.hypot(m.x - ws.seg[0], m.y - ws.seg[1]) <= COINCIDE_TOL
				|| Math.hypot(m.x - ws.seg[2], m.y - ws.seg[3]) <= COINCIDE_TOL;
			if (!touchesEndpoint) continue;
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
		if (unique.length > 1 || nets.length !== unique.length) {
			multiNetWires++;
			findings.push({
				type: 'multi-net-wire',
				level: 'warn',
				wirePrimitiveId: wireId,
				nets,
				count: nets.length,
				message: `导线有多个网络名: ${nets.join('、')}`,
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

	for (const c of components ?? []) {
		// Net flags/ports/labels are components too but have no real pins to float
		// — getAllPinsByPrimitiveId returns empty for them, so they're skipped.
		const primitiveId = c.getState_PrimitiveId();
		let pins;
		try { pins = await eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId(primitiveId); }
		catch { continue; }
		if (!pins || pins.length === 0) continue;
		const designator = c.getState_Designator?.() ?? '';

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

	// Rule 2: wire-crossing — two wire segments cross in their interiors (a routing
	// tangle layout-lint can't see; it only checks component bbox overlap). Shared
	// endpoints / junctions are excluded. Cap reported findings, count them all.
	const CROSS_CAP = 50;
	let crossingTotal = 0;
	for (let i = 0; i < segs.length; i++) {
		for (let j = i + 1; j < segs.length; j++) {
			if (!segmentsProperlyCross(segs[i], segs[j])) continue;
			crossingTotal++;
			if (findings.filter(f => f.type === 'wire-crossing').length < CROSS_CAP) {
				const at = segIntersection(segs[i], segs[j]) ?? undefined;
				findings.push({
					type: 'wire-crossing',
					level: 'warn',
					count: 1,
					at,
					message: '两条导线交叉(走线打结;改走通道/换 L 形拐点避开)',
				});
			}
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
	for (const w of wires ?? []) {
		let line: Array<number> | Array<Array<number>> | undefined;
		try { line = w.getState_Line(); }
		catch { continue; }
		if (!Array.isArray(line) || line.length === 0) continue;
		// getState_Line is flat [x1,y1,x2,y2,…] OR nested [[x1,y1],[x2,y2],…].
		const verts: Array<[number, number]> = [];
		if (Array.isArray(line[0])) {
			for (const p of line as Array<Array<number>>) verts.push([p[0], p[1]]);
		}
		else {
			const flat = line as Array<number>;
			for (let i = 0; i + 1 < flat.length; i += 2) verts.push([flat[i], flat[i + 1]]);
		}
		if (verts.length === 0) continue;
		let wirePid = '';
		let wnet = '';
		try { wirePid = String(w.getState_PrimitiveId?.() ?? ''); }
		catch { /* optional */ }
		try { wnet = String(w.getState_Net?.() ?? ''); }
		catch { /* optional */ }

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

		// Classify each of the wire's TWO extreme endpoints separately: does it touch
		// a pin, and does it touch a marker (netflag/netport/netlabel) or a DIFFERENT
		// wire? The old rule collapsed all vertices with `verts.some(...)`, so a stub
		// with one end on a pin always looked "connected" and orphan stubs (flag
		// deleted, wire残留) slipped through. Per-end classification lets us tell a
		// genuine terminus from a pin-anchored stub whose far end floats (issue #51).
		const endpoints: Array<[number, number]> = [verts[0], verts[verts.length - 1]];
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
		total: findings.length,
	};
	return { result: { passed: findings.length === 0, summary, findings } };
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
	const { byDesignator: pinNets } = await collectNetlistPinNets();

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
			componentType: c.getState_ComponentType?.() ?? '',
			name: c.getState_Name?.() ?? '',
			uniqueId: c.getState_UniqueId?.() ?? '', // sch↔PCB link key (for pcb.add_component)
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
			check = { error: err instanceof Error ? err.message : String(err) };
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

	let raw: Array<unknown>;
	try {
		raw = await eda.lib_Device.search(query);
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
		raw = await eda.lib_Device.getByLcscIds(lcscIds);
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

// ─── Rebind: swap a placed component's footprint / symbol ─────────────

/** A device-library identity ({ libraryUuid, uuid }) as reported by getState_Component(). */
interface DeviceRef { libraryUuid: string; uuid: string }

/**
 * Resolve a device's real library UUID. Imported devices (Altium/KiCad → EasyEDA)
 * often carry an EMPTY `libraryUuid` on the placed instance, which makes
 * `lib_Device.modify` / `sch_PrimitiveComponent.create` hang. When that happens we
 * reverse-look-up the true library UUID by searching the PROJECT library by the
 * device's name and matching on the device uuid (falling back to a lone hit).
 *
 * @param ref - the { libraryUuid, uuid } from getState_Component()
 * @param name - the device name (getState_Name()) used for the reverse search
 * @returns the ref with libraryUuid filled in, or a structured error if unresolvable
 */
async function resolveDeviceLibrary(ref: DeviceRef, name: string | undefined): Promise<DeviceRef> {
	if (ref.libraryUuid) return ref;
	if (!name) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			'This component has an empty device libraryUuid and no name to reverse-look-up from. '
			+ 'Cannot rebind — re-import the device from a library first.',
		);
	}
	let raw: Array<Record<string, unknown>>;
	try {
		// 'project' scope: search the current project's library (imported devices live here).
		raw = (await eda.lib_Device.search(name, 'project')) as unknown as Array<Record<string, unknown>>;
	}
	catch (err) {
		throw edaError(err, `Failed to reverse-look-up device library for "${name}".`);
	}
	if (!Array.isArray(raw) || raw.length === 0) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			`Device "${name}" has an empty libraryUuid and was not found in the project library. `
			+ 'Cannot resolve its real library UUID for rebind.',
		);
	}
	const byUuid = raw.find(r => r.uuid === ref.uuid);
	const chosen = byUuid ?? (raw.length === 1 ? raw[0] : undefined);
	if (!chosen || typeof chosen.libraryUuid !== 'string' || !chosen.libraryUuid) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			`Could not resolve a unique library UUID for device "${name}" (${raw.length} candidates). `
			+ 'Rebind aborted to avoid modifying the wrong device.',
		);
	}
	return { libraryUuid: chosen.libraryUuid, uuid: typeof chosen.uuid === 'string' ? chosen.uuid : ref.uuid };
}

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

/**
 * Fetch the placed component primitive by id, or throw a NOT-FOUND-style error.
 */
async function getComponentOrThrow(primitiveId: string): Promise<SchComponent> {
	let component: SchComponent | undefined;
	try {
		component = await eda.sch_PrimitiveComponent.get(primitiveId);
	}
	catch (err) {
		throw edaError(err, `Failed to read component "${primitiveId}".`);
	}
	if (!component) {
		throw new ActionError(
			ErrorCodes.INVALID_STATE,
			`No schematic component found with primitiveId "${primitiveId}".`,
		);
	}
	return component;
}

/**
 * The "five-step binding method" for swapping a placed component's footprint OR
 * symbol, exposed as a typed action so the operation no longer needs `debug.exec_js`.
 *
 * WHY delete-then-create (not a plain modify): `sch_PrimitiveComponent.modify` cannot
 * change the symbol/footprint reference of an already-placed instance (see
 * marketplace-coverage.md:64). The reference lives on the DEVICE-library record, so we:
 *   1. resolve the device's real library UUID (imported devices carry an empty one),
 *   2. `lib_Device.modify` the device association to the new footprint/symbol,
 *   3. `delete` the stale placed instance,
 *   4. `create` a fresh instance (which now inherits the new footprint/symbol),
 *   5. `modify` the new instance to restore designator / uniqueId / manufacturer /
 *      supplier / otherProperty (position, rotation, mirror & BOM flags are replayed
 *      into `create` directly).
 *
 * Original state is captured up front; any failure after step 2 rolls back the device
 * association and re-creates the original instance so the schematic is never left
 * half-rebound.
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

		const component = await getComponentOrThrow(primitiveId);
		const snapshot = serializeComponent(component);
		const deviceRaw = component.getState_Component() as DeviceRef;
		const oldSymbol = component.getState_Symbol() as DeviceRef | undefined;
		const oldFootprint = component.getState_Footprint() as DeviceRef | undefined;
		const device = await resolveDeviceLibrary(
			{ libraryUuid: deviceRaw?.libraryUuid ?? '', uuid: deviceRaw?.uuid ?? '' },
			typeof snapshot.name === 'string' ? snapshot.name : undefined,
		);

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

		const recreate = async (): Promise<SchComponent | undefined> => {
			const c = await eda.sch_PrimitiveComponent.create(
				{ libraryUuid: device.libraryUuid, uuid: device.uuid },
				x, y, subPartName, rotation, mirror, addIntoBom, addIntoPcb,
			);
			if (c && Object.keys(restoreProps).length) {
				try { await eda.sch_PrimitiveComponent.modify(c.getState_PrimitiveId(), restoreProps); }
				catch { /* best-effort restore */ }
			}
			return c ?? undefined;
		};

		let deleted = false;
		const rollback = async () => {
			if (rollbackAssoc) {
				try { await eda.lib_Device.modify(device.uuid, device.libraryUuid, undefined, undefined, rollbackAssoc); }
				catch { /* best-effort */ }
			}
			if (deleted) {
				try { await recreate(); } catch { /* best-effort */ }
			}
		};

		// Step 2: point the device at the new footprint/symbol.
		try {
			const ok = await eda.lib_Device.modify(device.uuid, device.libraryUuid, undefined, undefined, newAssoc);
			if (ok === false) {
				throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `lib_Device.modify returned false for ${kind} rebind.`);
			}
		}
		catch (err) {
			if (err instanceof ActionError) throw err;
			throw edaError(err, `Failed to bind the new ${kind} onto device "${device.uuid}".`);
		}

		// Step 3: delete the stale placed instance.
		try {
			await eda.sch_PrimitiveComponent.delete(primitiveId);
			deleted = true;
		}
		catch (err) {
			await rollback();
			throw edaError(err, `Failed to delete the old instance "${primitiveId}" (rolled back the ${kind} binding).`);
		}

		// Step 4 + 5: re-place and restore original state.
		let created: SchComponent | undefined;
		try {
			created = await recreate();
		}
		catch (err) {
			await rollback();
			throw edaError(err, `Failed to re-place the component after ${kind} rebind (rolled back).`);
		}
		if (!created) {
			await rollback();
			throw new ActionError(
				ErrorCodes.EDA_CALL_FAILED,
				`Re-placing the component after ${kind} rebind returned no primitive (rolled back).`,
			);
		}

		return {
			result: {
				primitiveId: created.getState_PrimitiveId(),
				previousPrimitiveId: primitiveId,
				rebound: kind,
				device: { uuid: device.uuid, libraryUuid: device.libraryUuid },
				[kind]: { uuid: target.uuid, libraryUuid: target.libraryUuid, name: target.name },
				component: serializeComponent(created),
			},
			warnings: [
				`Re-placing minted a new primitiveId; wires on the old instance's pins may need re-drawing — run \`sch drc\` / \`sch check\` to confirm connectivity.`,
			],
		};
	};
}

const schematicRebindFootprint: Handler = makeRebindHandler('footprint');
const schematicRebindSymbol: Handler = makeRebindHandler('symbol');

// ─── Composite: pin → wire → netflag/netport in one call ────────────

type Direction = 'up' | 'down' | 'left' | 'right';

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
// 2026-06-29 VERTICAL FIX (y-DOWN build): cycle reversed to up→right→down→left and
// power/ground anchors swapped, so a flag's up/down body now renders correctly
// (left/right numbers are unchanged). Verified via getPrimitivesBBox on real settled
// flags — a --direction down ground had been rendering its bars TOWARD the pin. Keep
// byte-identical to orientation.json (rotationCycle + bodyAnchorAtRot0) and orient.py.
const ROTATION_CYCLE: Direction[] = ['up', 'right', 'down', 'left'];
const BODY_ANCHOR_AT_ROT0: Record<'power' | 'ground' | 'port', Direction> = {
	power: 'down',
	ground: 'up',
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
		await eda.sch_PrimitiveComponent.delete([pid]);
		rotationNegates = stored === 270;
	}
	catch {
		rotationNegates = false;
	}
	return rotationNegates;
}

// Value to PASS so the flag's STORED rotation equals `desired` (what the linter reads).
async function appliedRotation(desired: number): Promise<number> {
	return (await detectRotationNegation()) ? (((360 - desired) % 360) + 360) % 360 : desired;
}

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
	const applied = await appliedRotation(rotation);

	// `--direction` is the VISUAL outward direction (what the caller sees on the
	// canvas), NOT a raw coordinate sign. On EasyEDA Pro (verified 3.2.121, issue
	// #19) the schematic document coords are y-DOWN: a LARGER stored y renders
	// LOWER on screen. So 'up' (visually higher) must DECREASE y and 'down'
	// (visually lower) must INCREASE y. The earlier y-UP assumption pushed top-pin
	// stubs/netports DOWN into the IC body and vice-versa even when DRC was clean.
	// (The flag-rotation table below is independent: it is calibrated against real
	// rendered bbox via calibrate.js and already keyed to visual directions — e.g.
	// rotationFor('port','up')===90, the exact rotation a reporter had to pass by
	// hand alongside `--direction down`; fixing the endpoint sign makes the wire and
	// the flag orientation agree without touching that calibrated table.)
	let endX = pinX;
	let endY = pinY;
	switch (direction) {
		case 'up': endY = pinY - offset; break;
		case 'down': endY = pinY + offset; break;
		case 'left': endX = pinX - offset; break;
		case 'right': endX = pinX + offset; break;
		default:
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown direction "${direction}"; expected up/down/left/right.`,
			);
	}

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
	endX = Math.round(endX / SCH_GRID) * SCH_GRID;
	endY = Math.round(endY / SCH_GRID) * SCH_GRID;

	if (endX === pinX && endY === pinY) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`offset must be non-zero (got ${offset}); pin and netflag would overlap.`,
		);
	}

	// An OFF-grid pin cannot get a valid stub at all: the snapped endpoint jogs
	// the perpendicular axis, turning the stub diagonal (EasyEDA refuses to
	// create it) — and un-snapping would leave the flag floating instead. Fail
	// with the actionable cause (probe round #3: autolayout's fractional zone
	// centers put every pin off-grid → 53/64 cryptic stub failures).
	const offGridX = pinX !== Math.round(pinX / SCH_GRID) * SCH_GRID;
	const offGridY = pinY !== Math.round(pinY / SCH_GRID) * SCH_GRID;
	if ((direction === 'left' || direction === 'right') ? offGridY : offGridX) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			`Pin (${pinX}, ${pinY}) sits OFF the ${SCH_GRID}-unit schematic grid on the stub's cross axis — the snapped endpoint would make the stub diagonal (EasyEDA refuses it) or leave the flag floating. Re-place the part so its anchor lands on the ${SCH_GRID}-grid (sch autolayout does this automatically), then reconnect.`,
		);
	}

	// Stub wire pin → endpoint.
	let wire;
	try {
		wire = await eda.sch_PrimitiveWire.create([pinX, pinY, endX, endY]);
	}
	catch (err) {
		throw edaError(err, 'Failed to create pin-stub wire.');
	}
	if (!wire) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Wire creation returned no primitive.');
	}

	// Netflag/netport at the far end (NOT at the pin — that would be the bug we are preventing).
	let flag;
	try {
		if (kind in NET_FLAG_KINDS) {
			flag = await eda.sch_PrimitiveComponent.createNetFlag(NET_FLAG_KINDS[kind], net, endX, endY, applied);
		}
		else if (kind in NET_PORT_KINDS) {
			flag = await eda.sch_PrimitiveComponent.createNetPort(NET_PORT_KINDS[kind], net, endX, endY, applied);
		}
		else {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Unknown kind "${kind}"; expected one of: ${[...Object.keys(NET_FLAG_KINDS), 'ground', ...Object.keys(NET_PORT_KINDS)].join(', ')}.`,
			);
		}
	}
	catch (err) {
		if (err instanceof ActionError) throw err;
		throw edaError(err, 'Failed to create netflag/netport at wire end.');
	}
	if (!flag) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to create ${kind}.`);
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
const schematicPinDisconnect: Handler = async (payload) => {
	const designator = optionalString(payload, 'designator');
	const pinNumber = optionalString(payload, 'pin');
	const flagPrimitiveId = optionalString(payload, 'flagPrimitiveId');
	const wirePrimitiveId = optionalString(payload, 'wirePrimitiveId');
	if (!flagPrimitiveId && !wirePrimitiveId && !(designator && pinNumber)) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Provide either "designator"+"pin", or a "flagPrimitiveId"/"wirePrimitiveId" to disconnect.',
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

	// Resolve the target pin coordinate. Prefer explicit designator+pin; else derive
	// it from the located stub's pin-side endpoint further below.
	let pinX: number | undefined;
	let pinY: number | undefined;
	if (designator && pinNumber) {
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

	// A generous tolerance shared with the check rules (grid-snap slop).
	const TOL = CHECK_EPS * 8;
	// Endpoints of a wire as [x,y] pairs (first + last vertex).
	const endpointsOf = (w: { getState_Line: () => Array<number> | Array<Array<number>> }): Array<[number, number]> => {
		let line;
		try { line = w.getState_Line(); }
		catch { return []; }
		if (!Array.isArray(line) || line.length === 0) return [];
		const verts: Array<[number, number]> = [];
		if (Array.isArray(line[0])) {
			for (const p of line as Array<Array<number>>) verts.push([p[0], p[1]]);
		}
		else {
			const flat = line as Array<number>;
			for (let i = 0; i + 1 < flat.length; i += 2) verts.push([flat[i], flat[i + 1]]);
		}
		if (verts.length === 0) return [];
		return [verts[0], verts[verts.length - 1]];
	};

	// Locate the stub wire.
	let stubWire: { pid: string; ends: Array<[number, number]> } | undefined;
	if (wirePrimitiveId) {
		for (const w of wires ?? []) {
			if (String(w.getState_PrimitiveId?.() ?? '') === wirePrimitiveId) {
				stubWire = { pid: wirePrimitiveId, ends: endpointsOf(w) };
			}
		}
	}
	// Derive pin coordinate from a flag if that's all we were given.
	if (!stubWire && flagPrimitiveId && (pinX === undefined || pinY === undefined)) {
		for (const c of components ?? []) {
			if (String(c.getState_PrimitiveId?.() ?? '') === flagPrimitiveId) {
				// The wire endpoint that coincides with THIS flag is the free end;
				// the opposite endpoint is the pin. Find the wire touching the flag.
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
						pinX = pinEnd[0];
						pinY = pinEnd[1];
						stubWire = { pid: String(w.getState_PrimitiveId?.() ?? ''), ends };
					}
				}
			}
		}
	}
	// With a pin coordinate but no wire yet, find the wire with an endpoint on the pin.
	if (!stubWire && pinX !== undefined && pinY !== undefined) {
		for (const w of wires ?? []) {
			const ends = endpointsOf(w);
			if (ends.length !== 2) continue;
			const onPin = ends.some(e => Math.hypot(e[0] - pinX!, e[1] - pinY!) <= TOL);
			if (onPin) {
				stubWire = { pid: String(w.getState_PrimitiveId?.() ?? ''), ends };
				break;
			}
		}
	}

	if (!stubWire) {
		throw new ActionError(
			ErrorCodes.EDA_CALL_FAILED,
			'No stub wire found on the target pin — nothing to disconnect (already clean?).',
		);
	}

	// Find the flag/port/label sitting on the stub's non-pin endpoint(s).
	const NET_MARKER_TYPES = new Set(['netflag', 'netport', 'netlabel', 'short_symbol']);
	const flagIds: Array<string> = [];
	for (const c of components ?? []) {
		let type: string;
		try { type = String(c.getState_ComponentType?.() ?? ''); }
		catch { continue; }
		if (!NET_MARKER_TYPES.has(type)) continue;
		let cx: number;
		let cy: number;
		try { cx = c.getState_X(); cy = c.getState_Y(); }
		catch { continue; }
		const onEnd = stubWire.ends.some(e => Math.hypot(e[0] - cx, e[1] - cy) <= TOL);
		if (onEnd) flagIds.push(String(c.getState_PrimitiveId?.() ?? ''));
	}

	// Delete wire + any flags together via the same routed delete used elsewhere.
	const deleted: { wires: Array<string>; components: Array<string> } = { wires: [], components: [] };
	try {
		if (stubWire.pid) {
			await deleteSchGroup('wires', [stubWire.pid]);
			deleted.wires.push(stubWire.pid);
		}
		const validFlags = flagIds.filter(Boolean);
		if (validFlags.length) {
			await deleteSchGroup('components', validFlags);
			deleted.components.push(...validFlags);
		}
	}
	catch (err) {
		throw edaError(err, 'Failed to delete stub wire / flag.');
	}

	return {
		result: {
			disconnected: true,
			pin: designator && pinNumber ? `${designator}:${pinNumber}` : undefined,
			at: pinX !== undefined && pinY !== undefined ? { x: pinX, y: pinY } : undefined,
			deletedWires: deleted.wires,
			deletedFlags: deleted.components,
		},
	};
};

// ─── Generic document open ───────────────────────────────────────────

/**
 * Open any document (schematic page or PCB) by UUID. A generalization of
 * schematic.page.open that works for all document types the editor supports.
 */
const documentOpen: Handler = async (payload) => {
	const uuid = requireString(payload, 'uuid');
	let tabId;
	try {
		tabId = await eda.dmt_EditorControl.openDocument(uuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to open document.');
	}
	if (tabId === undefined) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, `Failed to open document "${uuid}".`);
	}
	return { result: { tabId } };
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

const pcbStackupSet: Handler = async (payload) => {
	const count = optionalNumber(payload, 'count');
	let setCount: boolean | null = null;
	if (count != null) {
		const allowed = [2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 32];
		if (!allowed.includes(count)) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `count must be an even number 2..32, got ${count}`);
		}
		try {
			setCount = await eda.pcb_Layer.setTheNumberOfCopperLayers(count as 2 | 4 | 6 | 8 | 10 | 12 | 14 | 16);
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
			let ok: boolean;
			try {
				ok = await eda.pcb_Layer.modifyLayer(id as unknown as TPCB_LayersInTheSelectable, prop as { type?: TPCB_LayerTypesOfInnerLayer; name?: string });
			}
			catch (err) {
				throw edaError(err, `Failed to modify layer ${id} (only inner layers accept a type change).`);
			}
			modified.push({ layer: id, ok, ...prop });
		}
	}

	const allLayers = await eda.pcb_Layer.getAllLayers();
	const copperLayerCount = await eda.pcb_Layer.getTheNumberOfCopperLayers();
	return { result: { copperLayerCount, setCount, modified, layers: allLayers } };
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
				kind: 'attribute',
				text: a.getState_Value?.() ?? '',
				key: a.getState_Key?.() ?? '',
				layer,
				mirror: !!a.getState_Mirror?.(),
				reverse: !!a.getState_Reverse?.(),
				rotation: Number(a.getState_Rotation?.() ?? 0),
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
				kind: 'string',
				text: s.getState_Text?.() ?? '',
				layer,
				mirror: !!s.getState_Mirror?.(),
				reverse: !!s.getState_Reverse?.(),
				rotation: Number(s.getState_Rotation?.() ?? 0),
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
	let s;
	try {
		s = await eda.pcb_PrimitiveString.create(
			layer, x, y, text, '', fontSize, lineWidth,
			0 as unknown as EPCB_PrimitiveStringAlignMode, rotation, false, 0, false, false,
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
	return { result: { primitiveId: id, layer: Number(layer), x, y, fontSize, lineWidth, rotation, bbox } };
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
	for (const k of ['x', 'y', 'rotation', 'fontSize', 'lineWidth', 'text'] as const) {
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
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'nothing to do — provide x/y/rotation/fontSize/lineWidth/text, and/or --align (+ --ref).');
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
			if (isAttr) {
				if ('text' in props) { props.value = props.text; delete props.text; }
				await eda.pcb_PrimitiveAttribute.modify(id, props as never);
			}
			else {
				await eda.pcb_PrimitiveString.modify(id, props as never);
			}
			results.push({ primitiveId: id, ok: true, x: props.x, y: props.y });
		}
		catch (err) {
			results.push({ primitiveId: id, ok: false, error: String(err) });
		}
	}
	return { result: { align: align || undefined, count: results.length, results } };
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
		result.netsError = err instanceof Error ? err.message : String(err);
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
	catch (err) { result.netClassesError = err instanceof Error ? err.message : String(err); }

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
	catch (err) { result.differentialPairsError = err instanceof Error ? err.message : String(err); }

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
	catch (err) { result.equalLengthNetGroupsError = err instanceof Error ? err.message : String(err); }

	return { result };
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
 */
const pcbImportChanges: Handler = async (payload) => {
	const schematicUuid = optionalString(payload, 'schematicUuid');
	const ensureBoard = optionalBoolean(payload, 'ensureBoard') !== false;
	const recomputeRatline = optionalBoolean(payload, 'recomputeRatline') !== false;

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

	let imported;
	try {
		imported = await eda.pcb_Document.importChanges(schematicUuid);
	}
	catch (err) {
		throw edaError(err, 'Failed to import changes from the schematic.');
	}

	if (imported && recomputeRatline) {
		try {
			await eda.pcb_Document.startCalculatingRatline();
		}
		catch { /* best-effort */ }
	}

	return {
		result: {
			imported,
			createdBoard,
			// Read schematic/pcb defensively: a Board can legitimately hold only one
			// side (e.g. after a rebuild the schematic ref may be a deleted/orphaned
			// UUID), so `board.schematic.uuid` would crash — mirror serializeBoard.
			board: board
				? { name: board.name, schematicUuid: board.schematic?.uuid ?? null, pcbUuid: board.pcb?.uuid ?? null }
				: null,
			reason: imported
				? null
				: 'importChanges returned false — the PCB may be floating (no linked schematic) or schematicUuid is invalid.',
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
 * Capture the active PCB canvas as a PNG artifact. Reuses the canvas-agnostic
 * `dmt_EditorControl.getCurrentRenderedAreaImage`, so it mirrors schematic.snapshot
 * for the PCB. Same stale-frame caveat — judge layout/DRC by data, screenshot for
 * a human eyeball only.
 */
const pcbSnapshot: Handler = async (payload) => {
	const tabId = optionalString(payload, 'tabId');
	const fit = optionalBoolean(payload, 'fit') !== false;
	// Optional sha256 of the PREVIOUS snapshot (caller threads it back in). When
	// present we can DETECT a stale frame ourselves (issue #31) instead of only
	// emitting advisory text: if the viewport changed but the image bytes are
	// byte-identical, the capture is stale — we force a redraw + retry once.
	const previousSha = optionalString(payload, 'previousSha256');
	let fitted = false;
	if (fit) {
		try { await eda.dmt_EditorControl.zoomToAllPrimitives(); fitted = true; }
		catch { /* best-effort */ }
	}
	// Let any pending viewport change (a preceding `view region`/`view zoom`, or
	// the zoomToAllPrimitives above) commit + repaint before we read the frame.
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
	// force a redraw (ratline recompute + zoom-to-all nudge) and recapture once.
	let staleRetry = false;
	if (previousSha && sha256 && sha256 === previousSha) {
		staleRetry = true;
		// Stronger redraw nudge than schematic's re-settle: a ratline recompute +
		// re-fit reliably forces EasyEDA to repaint the PCB canvas.
		try { await eda.pcb_Document.startCalculatingRatline(); }
		catch { /* best-effort redraw nudge */ }
		try { await eda.dmt_EditorControl.zoomToAllPrimitives(); }
		catch { /* best-effort redraw nudge */ }
		await waitForCanvasSettle();
		blob = await capture();
		sha256 = await blobSha256(blob);
	}
	const stale = Boolean(previousSha && sha256 && sha256 === previousSha);

	const artifact = await blobToArtifact(blob, 'pcb_snapshot', 'pcb-snapshot.png', 'image/png');
	return {
		result: {
			artifactId: artifact.id,
			fitted,
			sha256,
			stale,
			staleRetry,
			capturedAt: new Date().toISOString(),
			staleHint: 'EasyEDA may not auto-redraw after API edits. Thread this sha256 back as previousSha256 on the next snapshot to auto-detect a stale frame; judge state by data (pcb list/drc), screenshot for layout only.',
		},
		artifacts: [artifact],
	};
};

/**
 * Lay out a component on the active PCB: move/rotate/flip-layer/lock or set
 * designator/BOM flags. Mirrors schematic.component.modify against pcb_*.
 */
const pcbComponentModify: Handler = async (payload) => {
	const primitiveId = requireString(payload, 'primitiveId');
	const patch = payload.patch;
	if (typeof patch !== 'object' || patch === null) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'Missing required object field "patch".');
	}

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
	return { result: { component: serializePcbComponent(component) } };
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
	let deleted;
	try {
		deleted = await eda.pcb_PrimitiveComponent.delete(primitiveIds);
	}
	catch (err) {
		throw edaError(err, 'Failed to delete PCB components.');
	}
	return { result: { deleted } };
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
	const list = (pours ?? []).map(p => ({
		primitiveId: p.getState_PrimitiveId(),
		net: p.getState_Net(),
		layer: p.getState_Layer(),
		pourName: p.getState_PourName(),
		fillMethod: p.getState_PourFillMethod(),
		priority: p.getState_PourPriority(),
		lineWidth: p.getState_LineWidth(),
		locked: p.getState_PrimitiveLock(),
	}));
	return { result: { pours: list, count: list.length } };
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
			const v = REGION_RULE_BY_NAME[r.trim().toLowerCase()];
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
	return { result: { lines: list, count: list.length } };
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

	const toDelete: Record<'track' | 'arc' | 'via', Array<string>> = { track: [], arc: [], via: [] };
	const removed: Array<Record<string, unknown>> = [];
	const skippedLocked: Array<string> = [];
	const notFound: Array<string> = [];
	const wrongKind: Array<string> = [];
	for (const id of ids) {
		const entry = byId.get(id);
		if (!entry) { notFound.push(id); continue; }
		if (kindGuard && entry.kind !== kindGuard) { wrongKind.push(`${id} is a ${entry.kind}`); continue; }
		if (entry.locked) { skippedLocked.push(id); continue; }
		toDelete[entry.kind].push(id);
		removed.push({ primitiveId: id, kind: entry.kind, ...entry.before });
	}
	if (wrongKind.length) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `kind=${kindGuard} refused mismatched ids: ${wrongKind.join('; ')}. Drop the kind guard or fix the id list.`);
	}
	if (!removed.length) {
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
	return { result: { deleted: results, removed, count: removed.length, skippedLocked, notFound } };
};

// ─── PCB routing: via-hop (layer hop with bonded vias) ───────────────
// One command for "cross to the other layer and come back": stub → via → hop
// track → via → stub, PLUS a small net-bound bond fill on BOTH layers of BOTH
// vias. The fills are load-bearing, not decoration: on this platform a track
// touching a via does NOT register as connected on 4-layer / ex-PLANE boards
// (pro-api-sdk#31) — a net-bound fill overlapping via+track is the only
// reliable bridge. Rolls back everything it created on mid-sequence failure.

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
	const bondFill = optionalBoolean(payload, 'bondFill') !== false;
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
				? 'bond fills placed on both layers of both vias (track↔via alone does not register as connected on 4-layer / ex-PLANE boards — pro-api-sdk#31)'
				: 'bondFill=false: on 4-layer / ex-PLANE boards this hop may NOT register as connected (pro-api-sdk#31) — verify with pcb.drc.check',
		},
	};
};

// ─── Board outline (板框) ──────────────────────────────────────────────
// The board outline is a closed loop of lines on the BOARD_OUTLINE layer (11).
// Native arcs do not commit on the current build, so curves are line-segment
// approximated by the caller. The layer is the numeric literal — EPCB_LayerId is
// a plain (non-const) enum that may not exist as a runtime global.
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

/**
 * Set the board outline from a closed polygon of points (mil, y-up). Replaces
 * any existing outline, draws one line per edge (closing the loop), and reports
 * whether every component falls inside.
 */
const pcbOutlineSet: Handler = async (payload) => {
	const raw = payload.points;
	if (!Array.isArray(raw) || raw.length < 3) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'points must be an array of >= 3 [x,y] pairs (mil).');
	}
	const points: Array<[number, number]> = [];
	for (const p of raw) {
		if (!Array.isArray(p) || p.length < 2 || typeof p[0] !== 'number' || typeof p[1] !== 'number') {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'each point must be [x, y] numbers.');
		}
		points.push([p[0], p[1]]);
	}
	const replace = optionalBoolean(payload, 'replace') !== false;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 10;

	try {
		// THE board outline is ONE `pcb_PrimitivePolyline` (类型=板框) on layer 11 — NOT
		// a set of individual `pcb_PrimitiveLine`s. A loose line on the outline layer is
		// just a wire that happens to sit there: EasyEDA does NOT treat it as the board
		// boundary — DRC ignores it for enclosure, and the UI "清除布线 / clear routing"
		// deletes it (observed: the whole outline vanished). The polyline IS the board-
		// outline object (matches a UI-drawn 板框, verified against its IPCB_Polygon).
		// Build the closed-polygon source [x0,y0,'L',x1,y1,…,x0,y0] (same path format as
		// pcbPourCreate), then createPolygon → the IPCB_Polygon that create() requires.
		const src: Array<number | string> = [points[0][0], points[0][1], 'L'];
		for (let i = 1; i < points.length; i++) src.push(points[i][0], points[i][1]);
		src.push(points[0][0], points[0][1]);
		const poly = eda.pcb_MathPolygon.createPolygon(src as unknown as TPCB_PolygonSourceArray);
		if (!poly) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Failed to build outline polygon (createPolygon returned undefined — points must form a valid closed polygon).');
		}

		if (replace) {
			// Remove any existing outline on layer 11: the proper polyline form AND
			// legacy individual lines/arcs (older builds drew the outline as lines).
			try {
				const oldPl = await eda.pcb_PrimitivePolyline.getAll(undefined, BOARD_OUTLINE_LAYER);
				if (oldPl.length) await eda.pcb_PrimitivePolyline.delete(oldPl.map(p => p.getState_PrimitiveId()));
			}
			catch { /* best-effort */ }
			try {
				const oldLines = await eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER);
				if (oldLines.length) await eda.pcb_PrimitiveLine.delete(oldLines.map(l => l.getState_PrimitiveId()));
			}
			catch { /* best-effort */ }
			try {
				const oldArcs = await eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER);
				if (oldArcs.length) await eda.pcb_PrimitiveArc.delete(oldArcs.map(a => a.getState_PrimitiveId()));
			}
			catch { /* arcs best-effort */ }
		}

		// Create the outline polyline LOCKED — a board outline must not move during
		// layout/routing and must survive clear-routing.
		const outline = await eda.pcb_PrimitivePolyline.create('', BOARD_OUTLINE_LAYER, poly, lineWidth, true);
		if (!outline) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Board outline creation returned no primitive (check points/layer).');
		}
		const outlineId = outline.getState_PrimitiveId();
		const segments = points.length;

		let zoomed = false;
		try { zoomed = await eda.pcb_Document.zoomToBoardOutline(); }
		catch { /* best-effort */ }

		const xs = points.map(p => p[0]);
		const ys = points.map(p => p[1]);
		const bbox = { minX: Math.min(...xs), maxX: Math.max(...xs), minY: Math.min(...ys), maxY: Math.max(...ys) };

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

		return { result: { outlineId, segments, zoomed, bbox, allInside: outside.length === 0, outside } };
	}
	catch (err) {
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
	// `outline` = the canonical polyline-based board outline; `segments`/`arcs` keep
	// reporting legacy line/arc counts so old boards still read sensibly.
	return { result: { outline: polylines.length, segments: lines.length, arcs: arcCount, bbox } };
};

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
	let value: unknown;
	try {
		const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
			new (arg: string, body: string): (eda: unknown) => Promise<unknown>;
		};
		const fn = new AsyncFunction('eda', code);
		value = await fn(eda);
	}
	catch (err) {
		throw edaError(err, 'exec_js failed.');
	}
	// A non-JSON-serializable return (e.g. a Blob) will not survive the wire;
	// debug snippets that need binary should base64-encode it themselves.
	return { result: { value: value ?? null } };
};

// ─── Registry & dispatch ─────────────────────────────────────────────

const HANDLERS: Record<string, Handler> = {
	'project.current': projectCurrent,
	'document.current': documentCurrent,
	'document.open': documentOpen,
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
	'schematic.component.place': schematicComponentPlace,
	'schematic.component.modify': schematicComponentModify,
	'schematic.component.delete': schematicComponentDelete,
	'schematic.wire.create': schematicWireCreate,
	'schematic.group.move': schematicGroupMove,
	'schematic.netflag.create': schematicNetflagCreate,
	'schematic.pin.set_no_connect': schematicPinSetNoConnect,
	'schematic.select': schematicSelect,
	'schematic.snapshot': schematicSnapshot,
	'schematic.drc.check': schematicDrcCheck,
	'schematic.check': schematicCheck,
	'schematic.read': schematicRead,
	'schematic.save': schematicSave,
	'schematic.export.netlist': schematicExportNetlist,
	'schematic.export.bom': schematicExportBom,
	'schematic.power.connect_pin': schematicPowerConnectPin,
	'schematic.pin.disconnect': schematicPinDisconnect,
	'schematic.library.search': schematicLibrarySearch,
	'schematic.library.get_by_lcsc': schematicLibraryGetByLcscIds,
	'schematic.rebind.footprint': schematicRebindFootprint,
	'schematic.rebind.symbol': schematicRebindSymbol,
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
	'pcb.silk.set': pcbSilkSet,
	'pcb.nets.list': pcbNetsList,
	'pcb.report': pcbReport,
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
	'pcb.component.modify': pcbComponentModify,
	'pcb.component.delete': pcbComponentDelete,
	'pcb.align': pcbAlign,
	'pcb.distribute': pcbDistribute,
	'pcb.grid_snap': pcbGridSnap,
	'pcb.components.move': pcbComponentsMove,
	'pcb.components.arrange': pcbComponentsArrange,
	'pcb.drc.check': pcbDrcCheck,
	'pcb.drc.rules': pcbDrcRules,
	'pcb.line.create': pcbLineCreate,
	'pcb.via.create': pcbViaCreate,
	'pcb.line.list': pcbLineList,
	'pcb.via.list': pcbViaList,
	'pcb.route.rip_up': pcbRouteRipUp,
	'pcb.route.delete': pcbRouteDelete,
	'pcb.route.via_hop': pcbRouteViaHop,
	'pcb.clear_routing': pcbClearRouting,
	'pcb.pour.create': pcbPourCreate,
	'pcb.pour.list': pcbPourList,
	'pcb.pour.delete': pcbPourDelete,
	'pcb.pour.rebuild': pcbPourRebuild,
	'pcb.region.create': pcbRegionCreate,
	'pcb.region.list': pcbRegionList,
	'pcb.region.delete': pcbRegionDelete,
	'pcb.fill.create': pcbFillCreate,
	'pcb.fill.list': pcbFillList,
	'pcb.fill.delete': pcbFillDelete,
	'pcb.save': pcbSave,
	'pcb.export.dsn': pcbExportDsn,
	'pcb.import_autoroute': pcbImportAutoroute,
	'pcb.snapshot': pcbSnapshot,
	'pcb.outline.set': pcbOutlineSet,
	'pcb.outline.get': pcbOutlineGet,
	'pcb.outline.clear': pcbOutlineClear,
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
