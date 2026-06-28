/**
 * Unit tests for pure helpers in `util.ts`.
 *
 * Run with: `npm test` (node:test via ts-node, no EasyEDA runtime needed).
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { ActionError } from './protocol';
import { normalizeRegion, normalizeWirePoints } from './util';

test('normalizeWirePoints: flat input is returned unchanged', () => {
	assert.deepEqual(normalizeWirePoints([195, 350, 215, 350]), [195, 350, 215, 350]);
});

test('normalizeWirePoints: nested pairs are flattened', () => {
	assert.deepEqual(normalizeWirePoints([[195, 350], [215, 350]]), [195, 350, 215, 350]);
});

test('normalizeWirePoints: nested and flat yield identical create args', () => {
	const nested = normalizeWirePoints([[100, 200], [100, 300], [150, 300]]);
	const flat = normalizeWirePoints([100, 200, 100, 300, 150, 300]);
	assert.deepEqual(nested, flat);
	assert.deepEqual(flat, [100, 200, 100, 300, 150, 300]);
});

test('normalizeWirePoints: missing / empty points throws', () => {
	assert.throws(() => normalizeWirePoints(undefined), ActionError);
	assert.throws(() => normalizeWirePoints([]), ActionError);
	assert.throws(() => normalizeWirePoints('nope'), ActionError);
});

test('normalizeWirePoints: odd-length or too-short flat input throws', () => {
	assert.throws(() => normalizeWirePoints([1, 2]), ActionError); // only one point
	assert.throws(() => normalizeWirePoints([1, 2, 3]), ActionError); // odd length
});

test('normalizeWirePoints: malformed nested entry throws', () => {
	assert.throws(() => normalizeWirePoints([[1, 2], [3]]), ActionError); // not a pair
	assert.throws(() => normalizeWirePoints([[1, 2, 3], [4, 5, 6]]), ActionError); // triple
});

test('normalizeWirePoints: non-finite coordinates throw', () => {
	assert.throws(() => normalizeWirePoints([1, 2, NaN, 4]), ActionError);
	assert.throws(() => normalizeWirePoints([[1, 2], [Infinity, 4]]), ActionError);
});

test('normalizeRegion: already-ordered box is returned unchanged', () => {
	assert.deepEqual(normalizeRegion(400, 730, 300, 520), { left: 400, right: 730, top: 300, bottom: 520 });
});

test('normalizeRegion: reversed X / Y bounds are sorted to min/max', () => {
	assert.deepEqual(normalizeRegion(730, 400, 520, 300), { left: 400, right: 730, top: 300, bottom: 520 });
	assert.deepEqual(normalizeRegion(400, 730, 520, 300), { left: 400, right: 730, top: 300, bottom: 520 });
});

test('normalizeRegion: negative coordinates are supported', () => {
	assert.deepEqual(normalizeRegion(-100, -500, 50, -50), { left: -500, right: -100, top: -50, bottom: 50 });
});

test('normalizeRegion: zero-area box (collapsed axis) throws', () => {
	assert.throws(() => normalizeRegion(400, 400, 300, 520), ActionError); // x span 0
	assert.throws(() => normalizeRegion(400, 730, 300, 300), ActionError); // y span 0
});

test('normalizeRegion: non-finite bound throws', () => {
	assert.throws(() => normalizeRegion(NaN, 730, 300, 520), ActionError);
	assert.throws(() => normalizeRegion(400, Infinity, 300, 520), ActionError);
});
