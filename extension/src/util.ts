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
