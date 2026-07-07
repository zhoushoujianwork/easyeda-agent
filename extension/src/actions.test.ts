/// <reference types="@jlceda/pro-api-types" />
/**
 * Unit tests for schematic component serialization (issue #52).
 *
 * Run with: `npm test` (node:test via ts-node, no EasyEDA runtime needed).
 * These exercise pure helpers that do not touch the `eda` global. The
 * triple-slash reference above loads the ambient `eda` declaration so ts-node
 * (which follows imports, not tsconfig's include glob) can compile actions.ts.
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { normalizeDeviceRef, serializeComponent } from './actions';

/** A minimal mock of eda.sch_PrimitiveComponent exposing only the getters
 *  serializeComponent reads. Casts through unknown since the real type is huge. */
function mockComponent(overrides: Record<string, unknown> = {}): any {
	const base: Record<string, unknown> = {
		PrimitiveId: 'e123',
		ComponentType: 'component',
		Designator: 'USB1',
		Name: 'TYPE-C 16PIN 2MD(073)',
		X: 100,
		Y: 200,
		Rotation: 0,
		Mirror: false,
		Net: '',
		SubPartName: '',
		AddIntoBom: true,
		AddIntoPcb: true,
		UniqueId: 'uq-1',
		Manufacturer: 'XKB',
		ManufacturerId: 'U262-16-C-N',
		Supplier: 'LCSC',
		SupplierId: 'C2765186',
		Component: { libraryUuid: 'LIB-A', uuid: 'DEV-A' },
		Symbol: { libraryUuid: 'LIB-S', uuid: 'SYM-INSTANCE' },
		Footprint: { libraryUuid: 'LIB-F', uuid: 'FP-INSTANCE' },
		OtherProperty: {},
		...overrides,
	};
	const obj: Record<string, unknown> = {};
	for (const [k, v] of Object.entries(base)) {
		obj[`getState_${k}`] = () => v;
	}
	return obj;
}

test('serializeComponent: exposes structured device identity (issue #52)', () => {
	const out = serializeComponent(mockComponent());
	assert.deepEqual(out.device, {
		libraryUuid: 'LIB-A',
		uuid: 'DEV-A',
		name: 'TYPE-C 16PIN 2MD(073)',
	});
});

test('serializeComponent: device.uuid is the device (not footprint) uuid', () => {
	const out = serializeComponent(mockComponent());
	const device = out.device as Record<string, unknown>;
	const footprint = out.footprint as Record<string, unknown>;
	assert.equal(device.uuid, 'DEV-A');
	assert.notEqual(device.uuid, footprint.uuid);
});

test('serializeComponent: keeps raw component field for backward compat', () => {
	const out = serializeComponent(mockComponent());
	assert.deepEqual(out.component, { libraryUuid: 'LIB-A', uuid: 'DEV-A' });
});

test('normalizeDeviceRef: empty libraryUuid (imported device) reported faithfully', () => {
	const ref = normalizeDeviceRef({ libraryUuid: '', uuid: 'DEV-X' }, 'Some Part');
	assert.deepEqual(ref, { libraryUuid: '', uuid: 'DEV-X', name: 'Some Part' });
});

test('normalizeDeviceRef: missing/undefined raw yields empty strings, never throws', () => {
	assert.deepEqual(normalizeDeviceRef(undefined, undefined), { libraryUuid: '', uuid: '', name: '' });
	assert.deepEqual(normalizeDeviceRef(null, 42), { libraryUuid: '', uuid: '', name: '' });
});

test('normalizeDeviceRef: non-string uuid/libraryUuid coerced to empty', () => {
	const ref = normalizeDeviceRef({ libraryUuid: 123, uuid: null }, 'X');
	assert.deepEqual(ref, { libraryUuid: '', uuid: '', name: 'X' });
});
