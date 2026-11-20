/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';

type State = Record<string, unknown>;
type Ref = { uuid: string; libraryUuid: string };
type Event = { kind: string; documentUuid: string; args: unknown[] };
const OLD_DEVICE = 'a'.repeat(32), OLD_LIBRARY = 'b'.repeat(32), OLD_ASSET = 'c'.repeat(32);
const NEW_DEVICE = 'd'.repeat(32), NEW_LIBRARY = 'e'.repeat(32), NEW_ASSET = 'f'.repeat(32), WRONG_ASSET = '0'.repeat(32);
const PROJECT = '1234567890abcdef'.repeat(2), PAGE = '1234567890abcdef', OTHER_PAGE = 'fedcba0987654321';
const OLD_ID = '1'.repeat(16), OLD_DEVICE_INSTANCE = '2'.repeat(16), OLD_FP_INSTANCE = '3'.repeat(16);
const CANDIDATE_ID = '4'.repeat(16), CANDIDATE_DEVICE_INSTANCE = '5'.repeat(16), CANDIDATE_FP_INSTANCE = '6'.repeat(16);
const RESTORED_ID = '7'.repeat(16), RESTORED_DEVICE_INSTANCE = '8'.repeat(16), RESTORED_FP_INSTANCE = '9'.repeat(16);
const OLD_NAME = 'Synthetic old device', NEW_NAME = 'Synthetic replacement device';
const OLD_MODEL = 'SYNTHETIC-ORIGINAL-A', NEW_MODEL = 'SYNTHETIC-REPLACEMENT-B';
const OLD_SUPPLIER = 'C123456', NEW_SUPPLIER = 'C654321';
const clone = <T>(value: T): T => structuredClone(value);
const mutations = new Set(['component.create', 'component.modify', 'component.delete']);

function primitive(state: State): any {
	const fields: Record<string, string> = {
		PrimitiveId: 'primitiveId', ComponentType: 'componentType', Designator: 'designator', Name: 'name',
		X: 'x', Y: 'y', Rotation: 'rotation', Mirror: 'mirror', Net: 'net', SubPartName: 'subPartName',
		AddIntoBom: 'addIntoBom', AddIntoPcb: 'addIntoPcb', UniqueId: 'uniqueId',
		Manufacturer: 'manufacturer', ManufacturerId: 'manufacturerId', Supplier: 'supplier', SupplierId: 'supplierId',
		Component: 'device', Symbol: 'symbol', Footprint: 'footprint', OtherProperty: 'otherProperty',
	};
	return Object.fromEntries(Object.entries(fields).map(([method, key]) => [`getState_${method}`, () => state[key]]));
}

interface Options {
	wrongCandidateSource?: boolean;
	drift?: 'create' | 'restore';
	unprovedCandidateAbsence?: 'still-present' | 'read-error';
	/** Live platform: a created device carries its own otherProperty and modify merges into it. */
	deviceFields?: boolean;
	/** Live platform drops one restored entry. */
	dropKept?: string;
}

function harness(options: Options = {}) {
	let currentPage = PAGE, candidateDeleteReturned = false;
	const events: Event[] = [];
	const part = (replacement: boolean) => ({
		name: replacement ? NEW_NAME : OLD_NAME, manufacturer: replacement ? 'Synthetic new vendor' : 'Synthetic old vendor',
		manufacturerId: replacement ? NEW_MODEL : OLD_MODEL, supplier: 'LCSC', supplierId: replacement ? NEW_SUPPLIER : OLD_SUPPLIER,
	});
	const original: State = {
		primitiveId: OLD_ID, componentType: 'part', designator: 'U3', ...part(false),
		x: 800, y: 500, rotation: 90, mirror: true, net: '', subPartName: undefined,
		addIntoBom: true, addIntoPcb: true, uniqueId: 'synthetic-stable-replace-link', otherProperty: options.deviceFields ? { Voltage: '3.3V', Datasheet: 'old.pdf' } : { Voltage: '3.3V' },
		device: { uuid: OLD_DEVICE_INSTANCE, libraryUuid: OLD_LIBRARY, name: OLD_NAME },
		symbol: { uuid: 'abcdef0123456789', libraryUuid: OLD_LIBRARY },
		footprint: { uuid: OLD_FP_INSTANCE, libraryUuid: OLD_LIBRARY, name: 'Synthetic old package' },
		nativeSource: { uuid: OLD_ASSET, libraryUuid: OLD_LIBRARY },
	};
	const states = new Map<string, State>([[OLD_ID, original]]);
	const log = (kind: string, ...args: unknown[]) => events.push({ kind, documentUuid: currentPage, args: clone(args) });
	const drift = (when: Options['drift']) => {
		if (options.drift === when && currentPage === PAGE) { currentPage = OTHER_PAGE; log('context.drift', when); }
	};
	const detail = (uuid: string, libraryUuid: string): State => {
		const replacement = uuid === NEW_DEVICE;
		assert.ok(replacement || uuid === OLD_DEVICE);
		assert.equal(libraryUuid, replacement ? NEW_LIBRARY : OLD_LIBRARY);
		return { uuid, libraryUuid, name: part(replacement).name, property: part(replacement), association: { footprint: {
			uuid: replacement ? NEW_ASSET : OLD_ASSET, libraryUuid,
		} } };
	};
	const row = (replacement: boolean) => ({ uuid: replacement ? NEW_DEVICE : OLD_DEVICE, libraryUuid: replacement ? NEW_LIBRARY : OLD_LIBRARY, name: part(replacement).name });
	const eda: any = {
		sys_Log: { add: () => undefined },
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: PROJECT }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: currentPage, tabId: `${currentPage}@${PROJECT}`, documentType: 1 }) },
		sys_FileManager: { getDocumentFootprintSources: async () => {
			const entries = [...states.values()].map(state => {
				const footprint = state.footprint as Ref, source = state.nativeSource as Ref;
				return { footprintUuid: footprint.uuid, documentSource: [
					JSON.stringify({ type: 'DOCHEAD' }) + '||' + JSON.stringify({ docType: 'FOOTPRINT', uuid: footprint.uuid }) + '|',
					JSON.stringify({ type: 'META' }) + '||' + JSON.stringify({ title: 'Synthetic native package', source: `${source.uuid}|${source.libraryUuid}` }) + '|',
				].join('\n') };
			});
			log('native.sources', entries); return entries;
		} },
		lib_Device: {
			getByLcscIds: async (ids: string[], _unused: unknown, all: boolean) => {
				log('device.byLcsc', ids, all); assert.equal(all, true);
				assert.ok(ids[0] === OLD_SUPPLIER || ids[0] === NEW_SUPPLIER);
				return [row(ids[0] === NEW_SUPPLIER)];
			},
			search: async (query: string, library: string) => {
				log('device.search', query, library); const replacement = library === NEW_LIBRARY;
				assert.equal(library, replacement ? NEW_LIBRARY : OLD_LIBRARY);
				assert.equal(query, replacement ? NEW_MODEL : OLD_MODEL); return [row(replacement)];
			},
			get: async (uuid: string, library: string) => { log('device.get', uuid, library); return detail(uuid, library); },
		},
		sch_PrimitiveComponent: {
			get: async (id: string) => {
				log('component.get', id);
				if (id === CANDIDATE_ID && candidateDeleteReturned && options.unprovedCandidateAbsence === 'read-error') throw new Error('synthetic candidate presence unavailable');
				const state = states.get(id); return state ? primitive(state) : undefined;
			},
			getAll: async () => [...states.values()].map(primitive),
			getAllPinsByPrimitiveId: async () => [],
			create: async (device: Ref, x: number, y: number, subPartName: unknown, rotation: number, mirror: boolean, addIntoBom: boolean, addIntoPcb: boolean) => {
				const replacement = device.uuid === NEW_DEVICE, id = replacement ? CANDIDATE_ID : RESTORED_ID;
				log('component.create', device, id);
				const source = { uuid: replacement ? NEW_ASSET : OLD_ASSET, libraryUuid: device.libraryUuid };
				const state: State = {
					...clone(original), ...part(replacement), primitiveId: id, designator: 'U?', uniqueId: `synthetic-created-${id}`,
					x, y, subPartName, rotation, mirror, addIntoBom, addIntoPcb,
					otherProperty: options.deviceFields ? { 'Manufacturer Part': part(replacement).manufacturerId, Datasheet: replacement ? 'new.pdf' : 'old.pdf' } : {},
					device: { uuid: replacement ? CANDIDATE_DEVICE_INSTANCE : RESTORED_DEVICE_INSTANCE, libraryUuid: device.libraryUuid, name: part(replacement).name },
					footprint: { uuid: replacement ? CANDIDATE_FP_INSTANCE : RESTORED_FP_INSTANCE, libraryUuid: device.libraryUuid, name: 'Synthetic created package' },
					nativeSource: replacement && options.wrongCandidateSource ? { ...source, uuid: WRONG_ASSET } : source,
				};
				states.set(id, state); if (replacement) drift('create'); return primitive(state);
			},
			modify: async (id: string, properties: State) => {
				log('component.modify', id, properties); const state = states.get(id);
				if (!state) return undefined;
				const next = clone(properties);
				if (options.deviceFields && next.otherProperty) next.otherProperty = { ...(state.otherProperty as State), ...(next.otherProperty as State) };
				if (options.dropKept && next.otherProperty) delete (next.otherProperty as State)[options.dropKept];
				Object.assign(state, next); if (id === CANDIDATE_ID) drift('restore'); return primitive(state);
			},
			delete: async (id: string) => {
				log('component.delete', id);
				if (id === CANDIDATE_ID) candidateDeleteReturned = true;
				if (!(id === CANDIDATE_ID && options.unprovedCandidateAbsence === 'still-present')) states.delete(id);
				return true;
			},
		},
	};
	return { eda, events, states, original };
}

function install(t: TestContext, eda: any) {
	const globals = globalThis as any, keys = ['eda', 'EDMT_EditorDocumentType'] as const;
	const previous = Object.fromEntries(keys.map(key => [key, globals[key]]));
	t.after(() => { for (const key of keys) { if (previous[key] === undefined) delete globals[key]; else globals[key] = previous[key]; } });
	globals.eda = eda; globals.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3 };
}

async function replace(t: TestContext, f: ReturnType<typeof harness>): Promise<any> {
	install(t, f.eda);
	return runAction('schematic.component.replace', { primitiveId: OLD_ID, deviceUuid: NEW_DEVICE, deviceLibraryUuid: NEW_LIBRARY, keepProperties: true });
}

function facts(error: any): any {
	const text = String(error.detail), start = text.indexOf('{');
	assert.ok(start >= 0, 'replace failure must preserve structured reconciliation facts');
	return JSON.parse(text.slice(start));
}

test('native16 replace freshly proves requested device and native source while preserving instance identity', async t => {
	const f = harness(), result = await replace(t, f);
	assert.equal(result.mutationContextAvailable, true);
	assert.deepEqual(result.result.device, { uuid: NEW_DEVICE, libraryUuid: NEW_LIBRARY });
	assert.equal(result.result.previousDevice.uuid, OLD_DEVICE);
	assert.equal(result.result.component.footprint.uuid, CANDIDATE_FP_INSTANCE);
	assert.equal(result.result.component.device.uuid, CANDIDATE_DEVICE_INSTANCE);
	assert.equal(result.result.component.uniqueId, f.original.uniqueId);
	assert.equal(result.result.component.designator, f.original.designator);
	assert.equal(result.result.component.manufacturerId, NEW_MODEL);
	assert.equal(result.result.component.supplierId, NEW_SUPPLIER);
	assert.deepEqual(result.result.component.otherProperty, f.original.otherProperty);
	assert.equal(f.states.has(OLD_ID), false); assert.equal(f.states.has(CANDIDATE_ID), true);
	const createIndex = f.events.findIndex(event => event.kind === 'component.create');
	const fresh = f.events.slice(createIndex + 1);
	assert.ok(fresh.some(event => event.kind === 'component.get' && event.args[0] === CANDIDATE_ID));
	assert.ok(fresh.some(event => event.kind === 'native.sources' && JSON.stringify(event.args).includes(NEW_ASSET)));
	assert.ok(fresh.some(event => event.kind === 'device.search' && event.args[0] === NEW_MODEL && event.args[1] === NEW_LIBRARY));
	assert.ok(fresh.some(event => event.kind === 'device.get' && event.args[0] === NEW_DEVICE));
});

test('native16 wrong replacement source is cleaned up before a freshly proven original rollback', async t => {
	const f = harness({ wrongCandidateSource: true });
	await assert.rejects(() => replace(t, f), (error: any) => {
		const report = facts(error);
		assert.equal(report.rollback.attempted, true); assert.equal(report.rollback.verified, true);
		assert.equal(report.rollback.restoredOriginal.primitiveId, RESTORED_ID);
		assert.equal(report.rollback.restoredOriginal.verified, true); return true;
	});
	const cleanupIndex = f.events.findIndex(event => event.kind === 'component.delete' && event.args[0] === CANDIDATE_ID);
	const restoreIndex = f.events.findIndex(event => event.kind === 'component.create' && (event.args[0] as Ref).uuid === OLD_DEVICE);
	assert.ok(cleanupIndex >= 0 && restoreIndex > cleanupIndex);
	assert.ok(f.events.slice(cleanupIndex + 1, restoreIndex).some(event => event.kind === 'component.get' && event.args[0] === CANDIDATE_ID));
	assert.equal(f.states.has(CANDIDATE_ID), false); assert.equal(f.states.has(RESTORED_ID), true);
	for (const key of ['designator', 'uniqueId', 'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'otherProperty']) assert.deepEqual(f.states.get(RESTORED_ID)?.[key], f.original[key]);
	const fresh = f.events.slice(restoreIndex + 1);
	assert.ok(fresh.some(event => event.kind === 'native.sources' && JSON.stringify(event.args).includes(RESTORED_FP_INSTANCE) && JSON.stringify(event.args).includes(OLD_ASSET)));
	assert.ok(fresh.some(event => event.kind === 'device.search' && event.args[0] === OLD_MODEL && event.args[1] === OLD_LIBRARY));
	assert.ok(fresh.some(event => event.kind === 'device.get' && event.args[0] === OLD_DEVICE));
});

test('native16 replace drift after create or identity restore stops mutation and records unknown presences', async t => {
	for (const drift of ['create', 'restore'] as const) await t.test(drift, async sub => {
		const f = harness({ drift });
		await assert.rejects(() => replace(sub, f), (error: any) => {
			assert.match(`${error.message} ${error.detail}`, /context|project\/document/i);
			const report = facts(error);
			assert.equal(report.oldPrimitive.present, null);
			assert.equal(report.replacement.primitiveId, CANDIDATE_ID);
			assert.equal(report.replacement.present, null);
			assert.equal(report.rollback.attempted, false); assert.equal(report.rollback.verified, false);
			return true;
		});
		const driftIndex = f.events.findIndex(event => event.kind === 'context.drift'); assert.ok(driftIndex >= 0);
		assert.equal(f.events.slice(driftIndex + 1).some(event => mutations.has(event.kind)), false);
		assert.equal(f.events.some(event => mutations.has(event.kind) && event.documentUuid === OTHER_PAGE), false);
		assert.equal(f.events.filter(event => event.kind === 'component.create').length, 1);
	});
});

test('native16 candidate cleanup without proven absence refuses to recreate the original', async t => {
	for (const unprovedCandidateAbsence of ['still-present', 'read-error'] as const) await t.test(unprovedCandidateAbsence, async sub => {
		const f = harness({ wrongCandidateSource: true, unprovedCandidateAbsence });
		await assert.rejects(() => replace(sub, f), (error: any) => {
			const report = facts(error);
			assert.equal(report.replacement.primitiveId, CANDIDATE_ID);
			assert.equal(report.replacement.present, unprovedCandidateAbsence === 'read-error' ? null : true);
			assert.equal(report.rollback.attempted, false); assert.equal(report.rollback.verified, false);
			assert.match(report.rollback.errors.join('; '), /removal.*proven|rollback.*not attempted/i);
			return true;
		});
		assert.equal(f.events.filter(event => event.kind === 'component.create').length, 1);
		assert.equal(f.states.has(RESTORED_ID), false);
		assert.ok(f.events.some(event => event.kind === 'component.delete' && event.args[0] === CANDIDATE_ID));
	});
});

test('keepProperties restores old custom entries on top of the new device\'s own fields', async t => {
	const f = harness({ deviceFields: true }), result = await replace(t, f);
	const props = result.result.component.otherProperty;
	assert.equal(props.Voltage, '3.3V');
	assert.equal(props['Manufacturer Part'], NEW_MODEL);
	assert.equal(props.Datasheet, 'new.pdf', 'fields the new device fills are not overwritten by the old part');
	assert.equal(f.states.has(CANDIDATE_ID), true);
});

test('keepProperties still fails closed when a kept entry is not restored', async t => {
	const f = harness({ deviceFields: true, dropKept: 'Voltage' });
	await assert.rejects(() => replace(t, f), (error: any) => /replacement-restore/.test(error.message) && /otherProperty\.Voltage was not restored/.test(String(error.detail)));
	assert.equal(f.states.has(CANDIDATE_ID), false);
});
