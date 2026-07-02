/**
 * Wire protocol shapes shared between the easyeda-agent Go daemon and this
 * connector. Field names MUST match `docs/protocol.md`,
 * `docs/connector-contract.md` and `internal/protocol/envelope.go` exactly.
 */

// Replaced at build time by esbuild `define` with extension.json's version
// (see config/esbuild.common.ts). Falls back for non-esbuild contexts (tsc).
declare const __CONNECTOR_VERSION__: string;
export const CONNECTOR_VERSION =
	typeof __CONNECTOR_VERSION__ === 'undefined' ? '0.0.0-dev' : __CONNECTOR_VERSION__;
export const PROTOCOL_VERSION = 'v1';
export const SERVICE_ID = 'easyeda-agent';
export const CAPABILITIES = ['schematic.v1', 'pcb.v1'];

// ─── Daemon → connector frames ───────────────────────────────────────

export interface HandshakeFrame {
	type: 'handshake';
	service: string;
	version?: string;
}

export interface RequestFrame {
	type: 'request';
	id: string;
	version?: string;
	action: string;
	payload?: Record<string, unknown>;
	windowId?: string;
}

export interface PingFrame {
	type: 'ping';
	id?: string;
}

export interface PongFrame {
	type: 'pong';
	id?: string;
}

export type InboundFrame =
	| HandshakeFrame
	| RequestFrame
	| PingFrame
	| PongFrame
	| { type: string; [key: string]: unknown };

// ─── Connector → daemon frames ───────────────────────────────────────

export interface RegisterFrame {
	type: 'register';
	windowId: string;
	connectorVersion: string;
	easyedaVersion: string;
	capabilities: Array<string>;
}

export interface ContextFrame {
	type: 'context';
	windowId: string;
	projectUuid?: string;
	projectName?: string;
	documentUuid?: string;
	documentType?: string;
	tabId?: string;
	unit?: string;
}

export interface ResponseContext {
	projectUuid?: string;
	projectName?: string;
	documentUuid?: string;
	documentType?: string;
	tabId?: string;
	unit?: string;
}

export interface ResponseArtifact {
	id: string;
	kind: string;
	mimeType?: string;
	fileName?: string;
	inlineBase64?: string;
}

export interface ResponseError {
	code: string;
	message: string;
	detail?: string;
}

export interface ResponseFrame {
	type: 'response';
	id: string;
	version: string;
	ok: boolean;
	result?: Record<string, unknown>;
	context?: ResponseContext;
	artifacts?: Array<ResponseArtifact>;
	warnings?: Array<string>;
	error?: ResponseError;
}

// ─── Stable error codes ──────────────────────────────────────────────

export const ErrorCodes = {
	UNKNOWN_ACTION: 'UNKNOWN_ACTION',
	MISSING_PAYLOAD_FIELD: 'MISSING_PAYLOAD_FIELD',
	EDA_API_UNAVAILABLE: 'EDA_API_UNAVAILABLE',
	EDA_CALL_FAILED: 'EDA_CALL_FAILED',
	INVALID_STATE: 'INVALID_STATE',
	INTERNAL_ERROR: 'INTERNAL_ERROR',
} as const;

/**
 * Result returned by an action handler. The dispatcher wraps this into a full
 * `ResponseFrame`.
 */
export interface ActionResult {
	result?: Record<string, unknown>;
	artifacts?: Array<ResponseArtifact>;
	warnings?: Array<string>;
	context?: ResponseContext;
}

/**
 * Thrown by handlers to produce a structured error response while preserving
 * the original eda error in `detail`.
 */
export class ActionError extends Error {
	public readonly code: string;
	public readonly detail?: string;

	constructor(code: string, message: string, detail?: string) {
		super(message);
		this.name = 'ActionError';
		this.code = code;
		this.detail = detail;
	}
}
