/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';

const OLD = 'a'.repeat(32), LIB = 'b'.repeat(32), NEW = 'c'.repeat(32);
const exact = { uuid: OLD, libraryUuid: LIB, name: 'Original', manufacturerId: 'EXACT' };
const flush = async () => { for (let i = 0; i < 100; i++) await Promise.resolve(); };

function host(t: TestContext, lib: any, fields: Record<string, unknown> = {}) {
	const globals = globalThis as any, previous = globals.eda;
	const writes: string[] = [], reads: string[] = [];
	const state: Record<string, unknown> = {
		PrimitiveId: 'old', ComponentType: 'part', Name: 'Original', ManufacturerId: 'EXACT', SupplierId: 'C123',
		Component: { uuid: OLD, libraryUuid: LIB }, Footprint: {}, ...fields,
	};
	const component = new Proxy({}, { get: (_target, key) => String(key).startsWith('getState_') ? () => state[String(key).replace('getState_', '')] : undefined });
	globals.eda = {
		lib_Device: Object.fromEntries(['getByLcscIds', 'search'].map(method => [method, (...args: any[]) => {
			reads.push(`${method}:${args[0]}`); return lib[method](...args);
		}])),
		sch_PrimitiveComponent: {
			get: async () => component, getAllPinsByPrimitiveId: async () => [],
			delete: async () => { writes.push('delete'); throw new Error('unexpected delete'); },
			create: async () => { writes.push('create'); throw new Error('unexpected create'); },
		},
	};
	t.after(() => { globals.eda = previous; });
	return { writes, reads, replace: (target: Record<string, unknown> = { deviceUuid: OLD, deviceLibraryUuid: LIB }) =>
		runAction('schematic.component.replace', { primitiveId: 'old', ...target }) };
}

for (const stage of ['lcsc', 'mpn', 'project-name'] as const) {
	test(`identity ${stage} never settles: shared budget rejects and late exact result cannot write`, async t => {
		t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
		let land!: (value: any[]) => void;
		const pendingRead = new Promise<any[]>(resolve => { land = resolve; });
		const f = host(t, { getByLcscIds: () => pendingRead, search: () => pendingRead },
			stage === 'lcsc' ? {} : stage === 'mpn' ? { SupplierId: '' } : { SupplierId: '', ManufacturerId: '' });
		try {
			const rejected = assert.rejects(f.replace({ deviceUuid: NEW, deviceLibraryUuid: LIB }), (err: any) => {
				assert.match(err.message, new RegExp(`identity\\.${stage}.*timed out`));
				assert.match(err.message, /BEFORE any canvas change/); return true;
			});
			await flush(); t.mock.timers.tick(7000); await rejected;
			assert.equal(f.reads.length, 1, 'no further library calls after shared budget exhaustion');
			land([exact]); await flush(); assert.deepEqual(f.writes, []);
		} finally { t.mock.timers.reset(); }
	});
}

test('identity rejects retain stage and underlying reason; true empty results remain no-match', async t => {
	for (const fail of [true, false]) {
		const read = async () => { if (fail) throw new Error('synthetic library unavailable'); return []; };
		const f = host(t, { getByLcscIds: read, search: read });
		await assert.rejects(f.replace(), (err: any) => {
			assert.match(err.message, fail ? /identity\.lcsc.*synthetic library unavailable.*identity\.mpn.*identity\.project-name/ : /no exact LCSC\/MPN\/project-name library match/);
			if (fail) assert.doesNotMatch(err.message, /no exact LCSC/);
			return true;
		});
		assert.deepEqual(f.writes, []);
	}
});

test('fast LCSC failure can fall back to exact MPN; fuzzy first hit is ignored', async t => {
	const f = host(t, {
		getByLcscIds: async () => { throw new Error('temporary LCSC failure'); },
		search: async () => [{ ...exact, uuid: NEW, manufacturerId: 'EXACT-OTHER' }, exact],
	});
	await assert.rejects(f.replace(), /Target device is the SAME/);
	assert.deepEqual(f.writes, []);
});

for (const selector of ['lcsc', 'query'] as const) {
	for (const mode of ['pending', 'reject', 'empty', 'exact'] as const) {
		test(`replace target ${selector} ${mode} has a terminal pre-write result`, async t => {
			t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
			let land!: (value: any[]) => void;
			const targetRead = () => mode === 'pending' ? new Promise<any[]>(resolve => { land = resolve; })
				: mode === 'reject' ? Promise.reject(new Error('synthetic target unavailable')) : Promise.resolve(mode === 'empty' ? [] : [exact]);
			const f = host(t, {
				getByLcscIds: (ids: string[]) => ids[0] === 'C123' ? Promise.resolve([exact]) : targetRead(),
				search: targetRead,
			});
			try {
				const rejected = assert.rejects(f.replace(selector === 'lcsc' ? { lcsc: 'C999' } : { query: 'Original' }), (err: any) => {
					const text = `${err.message} ${err.detail ?? ''}`;
					assert.match(text, mode === 'pending' ? /timed out after 7000ms/ : mode === 'reject' ? /synthetic target unavailable/ : mode === 'empty' ? /no canvas write attempted/ : /Target device is the SAME/);
					if (mode === 'pending' || mode === 'reject') assert.match(text, new RegExp(`replace\\.target\\.${selector}.*no canvas write attempted`));
					return true;
				});
				await flush(); if (mode === 'pending') t.mock.timers.tick(7000); await rejected;
				if (land) { land([exact]); await flush(); }
				assert.deepEqual(f.writes, []);
			} finally { t.mock.timers.reset(); }
		});
	}
}

test('identity fallbacks share remaining budget rather than restarting seven seconds', async t => {
	t.mock.timers.enable({ apis: ['setTimeout', 'Date'] });
	const f = host(t, {
		getByLcscIds: () => new Promise((_resolve, reject) => setTimeout(() => reject(new Error('first lookup unavailable')), 4000)),
		search: () => new Promise(() => {}),
	});
	try {
		let settled = false;
		const rejected = assert.rejects(f.replace(), (err: any) => {
			assert.match(err.message, /identity\.lcsc.*first lookup unavailable.*identity\.mpn.*timed out/); return true;
		}).then(() => { settled = true; });
		await flush(); t.mock.timers.tick(4000); await flush();
		assert.equal(f.reads.length, 2);
		t.mock.timers.tick(2999); await flush(); assert.equal(settled, false);
		t.mock.timers.tick(1); await rejected;
		assert.equal(f.reads.length, 2); assert.deepEqual(f.writes, []);
	} finally { t.mock.timers.reset(); }
});
