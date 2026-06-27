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
	newArtifactId,
	optionalBoolean,
	optionalNumber,
	optionalString,
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

/** Rename a schematic page. */
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
	return { result: { ok } };
};

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
	const points = payload.points;
	if (!Array.isArray(points)) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "points" (number[] or number[][]).',
		);
	}
	const net = optionalString(payload, 'net');
	const color = optionalString(payload, 'color') ?? null;
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? null;
	const lineType = (payload.lineType as ESCH_PrimitiveLineType | undefined) ?? null;

	let wire;
	try {
		wire = await eda.sch_PrimitiveWire.create(
			points as Array<number> | Array<Array<number>>,
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

const schematicSnapshot: Handler = async (payload) => {
	const tabId = optionalString(payload, 'tabId');
	let blob;
	try {
		blob = await eda.dmt_EditorControl.getCurrentRenderedAreaImage(tabId);
	}
	catch (err) {
		throw edaError(err, 'Failed to capture snapshot.');
	}
	if (!blob) {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Snapshot returned no image.');
	}
	const artifact = await blobToArtifact(blob, 'schematic_snapshot', 'snapshot.png', 'image/png');
	return { result: { artifactId: artifact.id }, artifacts: [artifact] };
};

// ─── DRC ──────────────────────────────────────────────────────────────

const schematicDrcCheck: Handler = async (payload) => {
	const strict = optionalBoolean(payload, 'strict') !== false;
	let violations: Array<unknown>;
	try {
		// `includeVerboseError: true` selects the verbose overload that returns
		// an array; the violation shape is untyped (`any`) by the SDK.
		violations = await eda.sch_Drc.check(strict, false, true);
	}
	catch (err) {
		throw edaError(err, 'Failed to run DRC.');
	}
	return { result: { passed: violations.length === 0, violations } };
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

	// EasyEDA is y-UP: +y renders upward. 'up' must increase y, 'down' decrease.
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

	if (endX === pinX && endY === pinY) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`offset must be non-zero (got ${offset}); pin and netflag would overlap.`,
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
const pcbLayersList: Handler = async () => {
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

	return { result: { layers, currentLayer, copperLayerCount, count: layers.length } };
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
			board: board
				? {
					name: board.name,
					schematicUuid: board.schematic.uuid,
					schematicName: board.schematic.name,
					pcbUuid: board.pcb.uuid,
					pcbName: board.pcb.name,
					parentProjectUuid: board.parentProjectUuid,
				}
				: null,
			pcb: pcb ? { uuid: pcb.uuid, name: pcb.name } : null,
		},
	};
};

// ─── Board (板子/组合 — schematic↔PCB binding) ─────────────────────────
// A Board groups one schematic + one PCB and is identified by NAME (not uuid).

type BoardItem = NonNullable<Awaited<ReturnType<typeof eda.dmt_Board.getCurrentBoardInfo>>>;

/** Serialize a Board to the {name, schematic, pcb, parentProjectUuid} shape. */
function serializeBoard(board: BoardItem): Record<string, unknown> {
	return {
		name: board.name,
		schematicUuid: board.schematic.uuid,
		schematicName: board.schematic.name,
		pcbUuid: board.pcb.uuid,
		pcbName: board.pcb.name,
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
			board: board
				? { name: board.name, schematicUuid: board.schematic.uuid, pcbUuid: board.pcb.uuid }
				: null,
			reason: imported
				? null
				: 'importChanges returned false — the PCB may be floating (no linked schematic) or schematicUuid is invalid.',
		},
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
	return { result: { passed: violations.length === 0, violations } };
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
	const lineWidth = optionalNumber(payload, 'lineWidth') ?? 6;

	try {
		if (replace) {
			const oldLines = await eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER);
			if (oldLines.length) {
				await eda.pcb_PrimitiveLine.delete(oldLines.map(l => l.getState_PrimitiveId()));
			}
			try {
				const oldArcs = await eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER);
				if (oldArcs.length) await eda.pcb_PrimitiveArc.delete(oldArcs.map(a => a.getState_PrimitiveId()));
			}
			catch { /* arcs best-effort */ }
		}

		let segments = 0;
		for (let i = 0; i < points.length; i++) {
			const a = points[i];
			const b = points[(i + 1) % points.length];
			const ln = await eda.pcb_PrimitiveLine.create('', BOARD_OUTLINE_LAYER, a[0], a[1], b[0], b[1], lineWidth);
			if (ln) segments++;
		}

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

		return { result: { segments, zoomed, bbox, allInside: outside.length === 0, outside } };
	}
	catch (err) {
		throw edaError(err, 'Failed to set board outline.');
	}
};

/** Read the current board outline: segment/arc counts + bounding box. */
const pcbOutlineGet: Handler = async () => {
	let lines;
	try {
		lines = await eda.pcb_PrimitiveLine.getAll(undefined, BOARD_OUTLINE_LAYER);
	}
	catch (err) {
		throw edaError(err, 'Failed to read board outline.');
	}
	let arcCount = 0;
	try { arcCount = (await eda.pcb_PrimitiveArc.getAll(undefined, BOARD_OUTLINE_LAYER)).length; }
	catch { /* best-effort */ }

	let bbox: Record<string, number> | null = null;
	if (lines.length) {
		let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
		for (const l of lines) {
			const pts: Array<[number, number]> = [[l.getState_StartX(), l.getState_StartY()], [l.getState_EndX(), l.getState_EndY()]];
			for (const [x, y] of pts) {
				minX = Math.min(minX, x); maxX = Math.max(maxX, x);
				minY = Math.min(minY, y); maxY = Math.max(maxY, y);
			}
		}
		bbox = { minX, maxX, minY, maxY };
	}
	return { result: { segments: lines.length, arcs: arcCount, bbox } };
};

/** Remove the current board outline (all primitives on the BOARD_OUTLINE layer). */
const pcbOutlineClear: Handler = async () => {
	let removed = 0;
	try {
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
	try {
		const ok = await eda.dmt_EditorControl.zoomToRegion(left, right, top, bottom);
		if (!ok) {
			throw new ActionError(ErrorCodes.EDA_CALL_FAILED, 'Canvas does not support region zoom (or no focused canvas).');
		}
		return { result: { ok } };
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
	'schematic.netflag.create': schematicNetflagCreate,
	'schematic.select': schematicSelect,
	'schematic.snapshot': schematicSnapshot,
	'schematic.drc.check': schematicDrcCheck,
	'schematic.save': schematicSave,
	'schematic.export.netlist': schematicExportNetlist,
	'schematic.export.bom': schematicExportBom,
	'schematic.power.connect_pin': schematicPowerConnectPin,
	'schematic.library.search': schematicLibrarySearch,
	'pcb.documents.list': pcbDocumentsList,
	'pcb.components.list': pcbComponentsList,
	'pcb.layers.list': pcbLayersList,
	'pcb.nets.list': pcbNetsList,
	'pcb.board.info': pcbBoardInfo,
	'board.list': boardList,
	'board.current': boardCurrent,
	'board.create': boardCreate,
	'board.rename': boardRename,
	'board.copy': boardCopy,
	'board.delete': boardDelete,
	'pcb.import_changes': pcbImportChanges,
	'pcb.component.modify': pcbComponentModify,
	'pcb.component.delete': pcbComponentDelete,
	'pcb.align': pcbAlign,
	'pcb.distribute': pcbDistribute,
	'pcb.grid_snap': pcbGridSnap,
	'pcb.components.move': pcbComponentsMove,
	'pcb.components.arrange': pcbComponentsArrange,
	'pcb.drc.check': pcbDrcCheck,
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
