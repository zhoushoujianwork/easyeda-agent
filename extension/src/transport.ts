/**
 * WebSocket transport between this connector and the easyeda-agent Go daemon.
 *
 * Adapted from the proven `eext-run-api-gateway` transport: port-scan +
 * handshake validation + register + heartbeat + auto-reconnect, all over
 * `eda.sys_WebSocket`. The raw-JS execute path is replaced with typed-action
 * dispatch (see ./actions).
 *
 *   ┌──────────────┐   WebSocket    ┌──────────────────┐
 *   │ easyeda-agent │ ◄───────────► │  this connector   │
 *   │  Go daemon    │  49620-49629  │  (EasyEDA Pro)    │
 *   └──────────────┘                └──────────────────┘
 */

import { buildContextFrame, readEasyEdaVersion } from './eda-context';
import { runAction } from './actions';
import {
	ActionError,
	CAPABILITIES,
	CONNECTOR_VERSION,
	ErrorCodes,
	type InboundFrame,
	PROTOCOL_VERSION,
	type RegisterFrame,
	type RequestFrame,
	type ResponseFrame,
	SERVICE_ID,
} from './protocol';

// ─── Configuration ───────────────────────────────────────────────────

const WS_ID = 'easyeda-agent';
const PORT_START = 49620;
const PORT_END = 49629;
const RETRY_DELAY_MS = 3000;
const MAX_RETRIES = 5;
const HEARTBEAT_INTERVAL_MS = 15000;
const HEARTBEAT_TIMEOUT_MS = 5000;
const CONNECTION_TIMEOUT_MS = 1500;
const STORAGE_KEY_AUTO_CONNECT = 'autoConnectEnabled';

// ─── State ────────────────────────────────────────────────────────────

let currentPort: number | null = null;
let handshakeVerified = false;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
let heartbeatPending = false;
let heartbeatSeq = 0;
let retryCount = 0;
let windowId: string | null = null;
let isConnecting = false;
let connectionSessionId = 0;

// ─── Status ───────────────────────────────────────────────────────────

export interface ConnectionStatus {
	connected: boolean;
	connecting: boolean;
	port: number | null;
	windowId: string | null;
}

/**
 * Read the current connection status (for the About dialog).
 *
 * @returns the connection status snapshot
 */
export function getConnectionStatus(): ConnectionStatus {
	return {
		connected: handshakeVerified,
		connecting: isConnecting,
		port: currentPort,
		windowId,
	};
}

// ─── Session helpers ──────────────────────────────────────────────────

function nextConnectionSessionId(): number {
	connectionSessionId += 1;
	return connectionSessionId;
}

function isConnectionSessionActive(sessionId: number): boolean {
	return sessionId === connectionSessionId;
}

function closeWebSocket(): void {
	try {
		eda.sys_WebSocket.close(WS_ID);
	}
	catch { /* ignore */ }
}

function cancelConnectionFlow(resetRetryCount = true): void {
	nextConnectionSessionId();
	isConnecting = false;
	clearRetryTimer();
	stopHeartbeat();
	handshakeVerified = false;
	currentPort = null;
	windowId = null;
	if (resetRetryCount) {
		retryCount = 0;
	}
	closeWebSocket();
}

// ─── Public control ───────────────────────────────────────────────────

/**
 * Force a reconnect: cancel any active flow and rescan the port range.
 */
export function reconnect(): void {
	eda.sys_Message.showToastMessage(eda.sys_I18n.text('Reconnecting...'));
	cancelConnectionFlow();
	void scanAndConnect();
}

/**
 * Stop the connection and cancel retries.
 *
 * @param showToast - whether to show a toast confirming the stop
 */
export function stop(showToast = true): void {
	cancelConnectionFlow();
	if (showToast) {
		eda.sys_Message.showToastMessage(eda.sys_I18n.text('Connection stopped'));
	}
}

/**
 * Start the connection flow if auto-connect is enabled.
 */
export function start(): void {
	const storedValue = eda.sys_Storage.getExtensionUserConfig(STORAGE_KEY_AUTO_CONNECT);
	const autoConnectEnabled = storedValue !== false;
	if (autoConnectEnabled) {
		void scanAndConnect();
	}
}

// ─── Port scan & connect ──────────────────────────────────────────────

/**
 * Scan the port range, register a WebSocket for each, and keep the one whose
 * daemon sends a valid `handshake` (service === "easyeda-agent").
 */
async function scanAndConnect(): Promise<void> {
	if (isConnecting) {
		return;
	}

	const sessionId = nextConnectionSessionId();
	isConnecting = true;
	clearRetryTimer();

	try {
		if (retryCount >= MAX_RETRIES) {
			eda.sys_Message.showToastMessage(eda.sys_I18n.text('Max retries reached'), ESYS_ToastMessageType.ERROR);
			return;
		}

		for (let port = PORT_START; port <= PORT_END; port++) {
			if (!isConnectionSessionActive(sessionId)) {
				return;
			}

			const found = await tryConnectToPort(port, sessionId);
			if (!isConnectionSessionActive(sessionId)) {
				return;
			}

			if (found) {
				currentPort = port;
				retryCount = 0;
				startHeartbeat(sessionId);
				return;
			}
		}

		retryCount++;
		eda.sys_Message.showToastMessage(
			`${eda.sys_I18n.text('Daemon not found, retrying...')} (${retryCount}/${MAX_RETRIES})`,
		);
		scheduleRetry(sessionId);
	}
	finally {
		if (isConnectionSessionActive(sessionId)) {
			isConnecting = false;
		}
	}
}

/**
 * Try to connect to a single port and wait for a valid handshake.
 *
 * @param port - the TCP port to try
 * @param sessionId - the active connection session id
 * @returns true if handshake succeeded and the connection is kept
 */
function tryConnectToPort(port: number, sessionId: number): Promise<boolean> {
	return new Promise((resolve) => {
		let settled = false;
		let timer: ReturnType<typeof setTimeout>;

		const settle = (success: boolean) => {
			if (settled) {
				return;
			}
			settled = true;
			clearTimeout(timer);
			if (!success && isConnectionSessionActive(sessionId)) {
				closeWebSocket();
			}
			resolve(success);
		};

		if (!isConnectionSessionActive(sessionId)) {
			resolve(false);
			return;
		}

		// Close any stale connection first: register() ignores new params for an
		// already-active connection with the same id.
		closeWebSocket();

		timer = setTimeout(() => settle(false), CONNECTION_TIMEOUT_MS);
		handshakeVerified = false;

		try {
			eda.sys_WebSocket.register(
				WS_ID,
				`ws://127.0.0.1:${port}/eda`,
				async (event: MessageEvent) => {
					if (!isConnectionSessionActive(sessionId)) {
						settle(false);
						return;
					}

					let msg: InboundFrame;
					try {
						msg = JSON.parse(event.data) as InboundFrame;
					}
					catch (err) {
						console.error('[easyeda-agent] Failed to parse frame:', err);
						return;
					}

					// Handshake phase.
					if (msg.type === 'handshake') {
						if ((msg as { service?: string }).service === SERVICE_ID) {
							handshakeVerified = true;
							windowId = crypto.randomUUID();
							sendRegister();
							void sendContext();
							eda.sys_Message.showToastMessage(
								`${eda.sys_I18n.text('Connected to easyeda-agent')} (port ${port})`,
							);
							settle(true);
						}
						else {
							console.warn(`[easyeda-agent] Unexpected handshake service "${(msg as { service?: string }).service}"`);
							settle(false);
						}
						return;
					}

					if (!handshakeVerified) {
						return;
					}

					await handleMessage(msg);
				},
				() => {},
			);
		}
		catch (err) {
			// register() throws when external-interaction permission is disabled.
			console.error('[easyeda-agent] Failed to register WebSocket:', err);
			settle(false);
		}
	});
}

// ─── Register / context ───────────────────────────────────────────────

function sendRegister(): void {
	if (!windowId) {
		return;
	}
	const frame: RegisterFrame = {
		type: 'register',
		windowId,
		connectorVersion: CONNECTOR_VERSION,
		easyedaVersion: readEasyEdaVersion(),
		capabilities: CAPABILITIES,
	};
	sendFrame(frame);
}

async function sendContext(): Promise<void> {
	if (!windowId) {
		return;
	}
	try {
		const frame = await buildContextFrame(windowId);
		sendFrame(frame);
	}
	catch (err) {
		console.warn('[easyeda-agent] Failed to build context frame:', err);
	}
}

function sendFrame(frame: unknown): void {
	try {
		eda.sys_WebSocket.send(WS_ID, JSON.stringify(frame));
	}
	catch (err) {
		console.error('[easyeda-agent] Failed to send frame:', err);
	}
}

// ─── Heartbeat ────────────────────────────────────────────────────────

function startHeartbeat(sessionId: number): void {
	stopHeartbeat();
	heartbeatTimer = setInterval(() => {
		if (!isConnectionSessionActive(sessionId)) {
			stopHeartbeat();
			return;
		}
		if (!handshakeVerified) {
			return;
		}
		try {
			heartbeatPending = true;
			heartbeatSeq += 1;
			sendFrame({ type: 'ping', id: `hb-${heartbeatSeq}` });
			setTimeout(() => {
				if (!isConnectionSessionActive(sessionId)) {
					return;
				}
				if (heartbeatPending) {
					console.warn('[easyeda-agent] Heartbeat timeout, reconnecting...');
					cancelConnectionFlow();
					void scanAndConnect();
				}
			}, HEARTBEAT_TIMEOUT_MS);
		}
		catch {
			cancelConnectionFlow();
			void scanAndConnect();
		}
	}, HEARTBEAT_INTERVAL_MS);
}

function stopHeartbeat(): void {
	if (heartbeatTimer) {
		clearInterval(heartbeatTimer);
		heartbeatTimer = null;
	}
	heartbeatPending = false;
}

// ─── Retry ────────────────────────────────────────────────────────────

function scheduleRetry(sessionId: number): void {
	clearRetryTimer();
	retryTimer = setTimeout(() => {
		if (!isConnectionSessionActive(sessionId) || isConnecting) {
			return;
		}
		void scanAndConnect();
	}, RETRY_DELAY_MS);
}

function clearRetryTimer(): void {
	if (retryTimer) {
		clearTimeout(retryTimer);
		retryTimer = null;
	}
}

// ─── Inbound message handling ─────────────────────────────────────────

async function handleMessage(msg: InboundFrame): Promise<void> {
	switch (msg.type) {
		case 'ping':
			// Reply to a daemon-initiated ping.
			sendFrame({ type: 'pong', id: (msg as { id?: string }).id });
			return;
		case 'pong':
			heartbeatPending = false;
			return;
		case 'request':
			await handleRequest(msg as RequestFrame);
			return;
		default:
			// Unknown frame types are ignored.
			return;
	}
}

async function handleRequest(request: RequestFrame): Promise<void> {
	const base = {
		type: 'response' as const,
		id: request.id,
		version: request.version ?? PROTOCOL_VERSION,
	};

	let response: ResponseFrame;
	try {
		const result = await runAction(request.action, request.payload);
		response = {
			...base,
			ok: true,
		};
		if (result.result !== undefined) {
			response.result = result.result;
		}
		if (result.context !== undefined) {
			response.context = result.context;
		}
		if (result.artifacts !== undefined && result.artifacts.length > 0) {
			response.artifacts = result.artifacts;
		}
		if (result.warnings !== undefined && result.warnings.length > 0) {
			response.warnings = result.warnings;
		}
	}
	catch (err) {
		response = {
			...base,
			ok: false,
			error: toResponseError(err),
		};
	}

	sendFrame(response);
}

function toResponseError(err: unknown): ResponseFrame['error'] {
	if (err instanceof ActionError) {
		return {
			code: err.code,
			message: err.message,
			detail: err.detail,
		};
	}
	const message = err instanceof Error ? err.message : String(err);
	return {
		code: ErrorCodes.INTERNAL_ERROR,
		message,
	};
}
