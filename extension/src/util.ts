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
 * A library item that can be matched by name — the common shape of
 * `lib_Footprint.search` / `lib_Symbol.search` results ({ uuid, libraryUuid,
 * name }). Extra fields are ignored.
 */
export interface NamedLibItem {
	uuid: string;
	libraryUuid: string;
	name: string;
}

/** The outcome of matching a name against a list of library candidates. */
export type NamedMatch<T extends NamedLibItem> =
	| { kind: 'match'; item: T }
	| { kind: 'none' }
	| { kind: 'ambiguous'; matches: Array<T> };

/**
 * Pick a single library candidate by name, deterministically.
 *
 * Matching precedence (the rebind actions rely on an EXACT swap, so we never
 * silently take a fuzzy/partial hit):
 *   1. exact case-sensitive name → if exactly one, that's the match; if several
 *      share the identical name, it's ambiguous (caller must disambiguate by uuid).
 *   2. no case-sensitive hit → exact case-insensitive name (same one-vs-many rule).
 *   3. otherwise → no match (the search returned only partial/substring hits).
 *
 * Returning a discriminated result (rather than throwing) keeps this pure and
 * unit-testable without the `eda` runtime; the handler maps it to an ActionError.
 *
 * @param name - the footprint/symbol name to bind to
 * @param candidates - the search results to choose from
 * @returns a discriminated match result
 */
export function pickNamedCandidate<T extends NamedLibItem>(
	name: string,
	candidates: Array<T>,
): NamedMatch<T> {
	const exact = candidates.filter(c => c.name === name);
	if (exact.length === 1) return { kind: 'match', item: exact[0] };
	if (exact.length > 1) return { kind: 'ambiguous', matches: exact };

	const lower = name.toLowerCase();
	const ci = candidates.filter(c => c.name.toLowerCase() === lower);
	if (ci.length === 1) return { kind: 'match', item: ci[0] };
	if (ci.length > 1) return { kind: 'ambiguous', matches: ci };

	return { kind: 'none' };
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

/**
 * Cross-check a pin's GEOMETRIC connectivity against the JSON-authoritative
 * netlist (getNetlistFile → pin→net). Pure (no `eda` runtime) so it can be unit
 * tested; the schematic.check handler feeds it live geometry + netlist facts.
 *
 * @param hasNetlistNet    - the authoritative netlist assigns this pin a non-empty net
 * @param geomConnected    - geometry says the pin is touched by a wire / net marker
 * @param netlistAvailable - the netlist JSON was actually fetched+parsed. When it is
 *                           NOT available (getNetlistFile failed / empty), the netlist
 *                           source is muted and we fall back to PURE geometry — a
 *                           geom-connected pin is 'connected', never a mismatch, so a
 *                           missing/uncompiled netlist can't manufacture false reports.
 * @returns
 *   - 'connected'         netlist has a net (authoritative — drops #15-class geometric
 *                         false positives), OR netlist unavailable + geometry connects
 *   - 'floating'          neither source connects it → real floating pin
 *   - 'geom-net-mismatch' netlist available + geometry says wired but netlist has NO
 *                         net → suspected missed report: a wire touches the pin yet it
 *                         is on no net (cosmetic touch, or a not-yet-compiled netlist)
 */
// EasyEDA auto-assigns an internal single-pin net name to a wire that touches a
// pin but has no netflag/netport/netlabel claiming it. The stored form looks like
// `$3N308` / `$12N1` (a `$`, digits, an `N`, more digits). This name is NOT the
// same as "unconnected", which is why `sch check` used to miss orphan stubs left
// behind after their flag was deleted. Matching this shape lets the dangling-wire
// rule flag "one end on a pin, other end free, net is auto-named" as a WARN.
const AUTO_NET_RE = /^\$\d+N\d+$/;

export function isAutoGeneratedNet(net: string | undefined | null): boolean {
	return typeof net === 'string' && AUTO_NET_RE.test(net.trim());
}

/**
 * Classify a wire's connectivity from a per-END touch summary. Given, for each of
 * the wire's two extreme endpoints, whether it touches a pin and whether it touches
 * a netflag/netport/netlabel/other-wire ("marker"), plus the wire's net name,
 * decide how the dangling-wire rule should treat it.
 *
 *  - 'connected'    — at least one end touches a marker/other wire (a real terminus),
 *                     OR both ends touch pins (a legitimate pin-to-pin wire).
 *  - 'orphan-stub'  — exactly one end touches a pin, the other touches nothing, and
 *                     the net name is auto-generated → flag deleted, stub残留.
 *  - 'dangling'     — no end touches anything (fully floating), the classic case.
 */
export type WireConnectivity = 'connected' | 'orphan-stub' | 'dangling';

export function classifyWireConnectivity(
	ends: Array<{ touchesPin: boolean; touchesMarker: boolean }>,
	net: string | undefined | null,
): WireConnectivity {
	const anyMarker = ends.some(e => e.touchesMarker);
	if (anyMarker) return 'connected';
	const pinEnds = ends.filter(e => e.touchesPin).length;
	// A wire whose both extreme ends land on pins is a normal pin-to-pin connection.
	if (pinEnds >= 2) return 'connected';
	if (pinEnds === 1) {
		// One end on a pin, the other floating. LIVE-VERIFIED (2026-07-07): a
		// connect_pin stub's wire.getState_Net() is EMPTY STRING on this build
		// regardless of whether its flag still exists — the $3N308-style
		// auto-generated name only appears via the COMPILED netlist file
		// (schematicCheck's netlist cross-check), never on the raw wire primitive.
		// So an intact stub never even reaches this branch (its far end touches the
		// flag → anyMarker=true → 'connected' above); reaching here with a blank/
		// auto-pattern net means the flag is gone. A genuinely blank net is
		// therefore ALSO an orphan signal, not just the (never-observed-in-practice)
		// $-pattern — keep isAutoGeneratedNet too in case a future build does stamp
		// it. A real, non-blank net name means a human hand-drew this half-finished
		// wire deliberately (schematic.wire.create --net X) — leave that alone.
		return (net === '' || isAutoGeneratedNet(net)) ? 'orphan-stub' : 'connected';
	}
	return 'dangling';
}

export type PinConnectivity = 'connected' | 'floating' | 'geom-net-mismatch';

export function classifyPinConnectivity(
	hasNetlistNet: boolean,
	geomConnected: boolean,
	netlistAvailable: boolean,
): PinConnectivity {
	if (hasNetlistNet) return 'connected';
	// Netlist muted (couldn't fetch) → trust geometry alone, no mismatch signal.
	if (!netlistAvailable) return geomConnected ? 'connected' : 'floating';
	if (geomConnected) return 'geom-net-mismatch';
	return 'floating';
}

// ─── LCSC C-number exact matching ─────────────────────────────────────

/**
 * Does this string look like a bare LCSC C-number (e.g. "C5665")? Used to
 * detect when a `lib search --query` is really an exact-part lookup rather
 * than free text. Case-insensitive; leading/trailing space tolerated.
 */
export function isLcscQuery(query: string): boolean {
	return /^\s*C\d+\s*$/i.test(query);
}

/**
 * Read a device-library record's LCSC C-number. Current builds emit it as the
 * top-level `supplierId`; newer/migrated records carry it in
 * `otherProperty['Supplier Part']` (canonical EasyEDA property name). Read
 * top-level first, then fall back — mirrors the projection in
 * `schematicLibraryGetByLcscIds`. Returns '' when neither is present.
 */
export function readLcscField(record: Record<string, unknown>): string {
	const op = (record.otherProperty as Record<string, unknown> | undefined) ?? {};
	const raw = record.supplierId ?? op['Supplier Part'];
	return raw == null ? '' : String(raw);
}

/**
 * Filter raw `lib_Device.search` results down to those whose LCSC field is a
 * strict, case-insensitive match for the requested C-number. This is the
 * convergence step the free-text ranker lacks: `search("C5665")` returns an
 * op-amp `CLC5665IMX` (name contains "5665") ranked first, but only the record
 * whose `supplierId`/`Supplier Part` equals `C5665` is the real part.
 */
export function filterExactLcsc(
	records: Array<Record<string, unknown>>,
	lcsc: string,
): Array<Record<string, unknown>> {
	const want = lcsc.trim().toUpperCase();
	return records.filter(r => readLcscField(r).trim().toUpperCase() === want);
}
