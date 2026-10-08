/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';
import { ActionError, ErrorCodes } from './protocol';

// Offline contract regressions only. These mocks do not certify rendering,
// manufacturing output or save/reload behavior on an EasyEDA host.
const valid = {
	dataBase64: Buffer.from('reference-source').toString('base64'),
	fileName: 'module.png', x: 10, y: -20, width: 200, height: 120,
};

test('schematic image create requires two positive finite dimensions before mutation', async t => {
	const globals = globalThis as any;
	const oldEda = globals.eda;
	let calls = 0;
	globals.eda = { sch_PrimitiveObject: { create: async () => { calls++; } } };
	t.after(() => { if (oldEda === undefined) delete globals.eda; else globals.eda = oldEda; });
	for (const field of ['width', 'height']) {
		for (const value of [undefined, null, 0, -1, NaN, Infinity, -Infinity, '200']) {
			await assert.rejects(() => runAction('schematic.image.create', { ...valid, [field]: value }), (err: unknown) => {
				assert.ok(err instanceof ActionError);
				assert.ok([ErrorCodes.MISSING_PAYLOAD_FIELD, ErrorCodes.PRECONDITION_REFUSED].includes(err.code as any));
				return true;
			});
		}
	}
	assert.equal(calls, 0);
});

test('schematic image create refuses invalid coordinates, rotation and mirror before mutation', async t => {
	const globals = globalThis as any;
	const oldEda = globals.eda;
	let calls = 0;
	globals.eda = { sch_PrimitiveObject: { create: async () => { calls++; } } };
	t.after(() => { if (oldEda === undefined) delete globals.eda; else globals.eda = oldEda; });
	for (const field of ['x', 'y', 'rotation']) {
		for (const value of [null, NaN, Infinity, -Infinity, '90']) {
			await assert.rejects(() => runAction('schematic.image.create', { ...valid, [field]: value }));
		}
	}
	for (const mirror of ['true', 1, null]) {
		await assert.rejects(() => runAction('schematic.image.create', { ...valid, mirror }));
	}
	assert.equal(calls, 0);
});

test('schematic image create reports unsupported when the host has no create API', async t => {
	const globals = globalThis as any;
	const oldEda = globals.eda;
	t.after(() => { if (oldEda === undefined) delete globals.eda; else globals.eda = oldEda; });
	for (const host of [{}, { sch_PrimitiveObject: {} }, { sch_PrimitiveObject: { create: true } }]) {
		globals.eda = host;
		await assert.rejects(() => runAction('schematic.image.create', valid), (err: unknown) => {
			assert.ok(err instanceof ActionError);
			assert.equal(err.code, ErrorCodes.PRECONDITION_REFUSED);
			assert.match(err.message, /^unsupported: schematic embedded reference-object create API unavailable/);
			return true;
		});
	}
});
