/**
 * Small runtime helpers that do not depend on the EasyEDA `eda` object.
 */

import { ActionError, ErrorCodes } from './protocol';

const BASE64_CHARS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

/**
 * Encode a Uint8Array to a standard (padded) base64 string.
 *
 * Implemented manually so we do NOT depend on Node `Buffer` (unavailable in the
 * EasyEDA browser runtime) nor on `btoa` (which only accepts Latin-1 strings).
 *
 * @param bytes - raw bytes
 * @returns base64-encoded string
 */
export function uint8ToBase64(bytes: Uint8Array): string {
	let output = '';
	const len = bytes.length;
	let i = 0;

	for (; i + 2 < len; i += 3) {
		const triple = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
		output += BASE64_CHARS[(triple >> 18) & 0x3F];
		output += BASE64_CHARS[(triple >> 12) & 0x3F];
		output += BASE64_CHARS[(triple >> 6) & 0x3F];
		output += BASE64_CHARS[triple & 0x3F];
	}

	const remaining = len - i;
	if (remaining === 1) {
		const chunk = bytes[i];
		output += BASE64_CHARS[(chunk >> 2) & 0x3F];
		output += BASE64_CHARS[(chunk << 4) & 0x3F];
		output += '==';
	}
	else if (remaining === 2) {
		const chunk = (bytes[i] << 8) | bytes[i + 1];
		output += BASE64_CHARS[(chunk >> 10) & 0x3F];
		output += BASE64_CHARS[(chunk >> 4) & 0x3F];
		output += BASE64_CHARS[(chunk << 2) & 0x3F];
		output += '=';
	}

	return output;
}

/**
 * Read a Blob/File into a base64 string via its ArrayBuffer.
 *
 * @param blob - source Blob or File
 * @returns base64-encoded contents
 */
export async function blobToBase64(blob: Blob): Promise<string> {
	const buffer = await blob.arrayBuffer();
	return uint8ToBase64(new Uint8Array(buffer));
}

/**
 * Generate an artifact id.
 *
 * @returns `art_<uuid>` identifier
 */
export function newArtifactId(): string {
	return `art_${crypto.randomUUID()}`;
}

type PayloadRecord = Record<string, unknown>;

/**
 * Coerce a possibly-missing payload to a record.
 *
 * @param payload - raw payload from the request frame
 * @returns a record (empty if payload was missing)
 */
export function asPayload(payload?: Record<string, unknown>): PayloadRecord {
	return payload ?? {};
}

/**
 * Require a string field from the payload, throwing a structured error if absent.
 *
 * @param payload - request payload
 * @param field - field name
 * @returns the string value
 */
export function requireString(payload: PayloadRecord, field: string): string {
	const value = payload[field];
	if (typeof value !== 'string' || value.length === 0) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Missing required string field "${field}".`,
		);
	}
	return value;
}

/**
 * Require a number field from the payload, throwing a structured error if absent.
 *
 * @param payload - request payload
 * @param field - field name
 * @returns the numeric value
 */
export function requireNumber(payload: PayloadRecord, field: string): number {
	const value = payload[field];
	if (typeof value !== 'number' || Number.isNaN(value)) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Missing required number field "${field}".`,
		);
	}
	return value;
}

/**
 * Read an optional string field.
 *
 * @param payload - request payload
 * @param field - field name
 * @returns the string value or undefined
 */
export function optionalString(payload: PayloadRecord, field: string): string | undefined {
	const value = payload[field];
	return typeof value === 'string' ? value : undefined;
}

/**
 * Read an optional number field.
 *
 * @param payload - request payload
 * @param field - field name
 * @returns the numeric value or undefined
 */
export function optionalNumber(payload: PayloadRecord, field: string): number | undefined {
	const value = payload[field];
	return typeof value === 'number' && !Number.isNaN(value) ? value : undefined;
}

/**
 * Read an optional boolean field.
 *
 * @param payload - request payload
 * @param field - field name
 * @returns the boolean value or undefined
 */
export function optionalBoolean(payload: PayloadRecord, field: string): boolean | undefined {
	const value = payload[field];
	return typeof value === 'boolean' ? value : undefined;
}

/**
 * Normalize wire `points` into the flat `number[]` form that
 * `eda.sch_PrimitiveWire.create` actually accepts.
 *
 * Callers may pass either:
 *  - flat:   `[x1, y1, x2, y2, ...]`
 *  - nested: `[[x1, y1], [x2, y2], ...]`
 *
 * EDA only accepts the flat form — nested arrays make `create` fail with
 * `EDA_CALL_FAILED / "create failed!"` (see issue #5). We flatten here so every
 * caller (CLI / `call` / sch.py / debug.exec_js) is normalized at a single source
 * of truth. The result is validated to be an even-length (`≥4`) array of finite
 * numbers — i.e. at least two coordinate pairs.
 *
 * @param points - raw `points` value from the payload
 * @returns flat `number[]` (`[x1, y1, x2, y2, ...]`)
 * @throws ActionError when `points` is missing/empty or not a valid coordinate list
 */
export function normalizeWirePoints(points: unknown): number[] {
	if (!Array.isArray(points) || points.length === 0) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			'Missing required field "points" (number[] or number[][]).',
		);
	}

	let flat: unknown[];
	if (Array.isArray(points[0])) {
		// Nested [[x,y],...] → flatten one level into [x,y,...].
		flat = [];
		for (const pair of points) {
			if (!Array.isArray(pair) || pair.length !== 2) {
				throw new ActionError(
					ErrorCodes.MISSING_PAYLOAD_FIELD,
					'Invalid "points": each nested entry must be a [x, y] pair.',
				);
			}
			flat.push(pair[0], pair[1]);
		}
	}
	else {
		flat = points;
	}

	if (flat.length < 4 || flat.length % 2 !== 0) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Invalid "points": expected an even number of coordinates (≥4 = at least two [x, y] points), got ${flat.length}.`,
		);
	}
	for (const n of flat) {
		if (typeof n !== 'number' || !Number.isFinite(n)) {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				'Invalid "points": all coordinates must be finite numbers.',
			);
		}
	}

	return flat as number[];
}

/** A canvas rectangle with X/Y bounds already normalized to min/max order. */
export interface NormalizedRegion {
	left: number;
	right: number;
	top: number;
	bottom: number;
}

/**
 * Normalize a `view region` rectangle so `zoomToRegion` always receives a
 * sane, non-degenerate box.
 *
 * `eda.dmt_EditorControl.zoomToRegion(left, right, top, bottom)` expects two X
 * bounds and two Y bounds, but it does NOT defensively order them — passing a
 * reversed pair (e.g. `right < left`, or `top`/`bottom` in the wrong order for
 * the y-DOWN schematic coords, issue #19/#20) yields a zero/negative-area box
 * that the canvas resolves to a tiny sliver in a mostly-blank frame. We sort
 * each axis here so the rectangle is the same regardless of which corner the
 * caller passed first, and reject a fully degenerate (zero-area) request up
 * front instead of letting it render as blank.
 *
 * @param left - first X bound
 * @param right - second X bound
 * @param top - first Y bound
 * @param bottom - second Y bound
 * @returns the rectangle with `left<=right` and `top<=bottom`
 * @throws ActionError when any bound is non-finite or the box has zero area
 */
export function normalizeRegion(
	left: number,
	right: number,
	top: number,
	bottom: number,
): NormalizedRegion {
	for (const [name, value] of [['left', left], ['right', right], ['top', top], ['bottom', bottom]] as const) {
		if (typeof value !== 'number' || !Number.isFinite(value)) {
			throw new ActionError(
				ErrorCodes.MISSING_PAYLOAD_FIELD,
				`Invalid region: "${name}" must be a finite number.`,
			);
		}
	}
	const minX = Math.min(left, right);
	const maxX = Math.max(left, right);
	const minY = Math.min(top, bottom);
	const maxY = Math.max(top, bottom);
	if (minX === maxX || minY === maxY) {
		throw new ActionError(
			ErrorCodes.MISSING_PAYLOAD_FIELD,
			`Invalid region: zero-area box (x span ${maxX - minX}, y span ${maxY - minY}). `
			+ 'left/right and top/bottom must each be two distinct bounds.',
		);
	}
	return { left: minX, right: maxX, top: minY, bottom: maxY };
}
