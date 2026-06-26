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
// After MAX_RETRIES fast attempts we DON'T give up — we fall back to this slow
// background poll so a daemon started/restarted later auto-reconnects with no
// manual Reconnect. The daemon is almost always launched AFTER the editor (and
// `make build` compiles first), so a terminal give-up would strand every fresh
// `bin/easyeda daemon`.
const SLOW_RETRY_DELAY_MS = 10000;
const MAX_RETRIES = 5;
// EasyEDA's eda.sys_WebSocket closes idle connections after ~5s of silence.
// Ping more often than that to keep the socket alive between actions, which
// otherwise causes a register -> 5s silence -> close -> reconnect storm.
const HEARTBEAT_INTERVAL_MS = 3000;
// Liveness is consecutive-miss based, NOT a single round-trip deadline. The
// daemon never idle-closes, and EasyEDA's webview can lag pong delivery under
// load (canvas redraw, GC). Tearing the socket down on ONE missed pong tore down
// perfectly healthy connections every ~5s — the reconnect storm. Only give up
// after this many pings go unanswered in a row (~9s of true silence).
const MAX_MISSED_PONGS = 3;
const CONNECTION_TIMEOUT_MS = 1500;
// Delay between close() and register() of the same WS id. close() is async and
// exposes no completion callback; if we re-register before EasyEDA releases the
// id, register() silently ignores the new url/callback (documented in
// pro-api-types index.d.ts:21025), leaving the previous callback bound. Observed
// id-release is well under this; 200ms is a safety margin. The deferred register
// is cancelled in settle() so a completed/aborted attempt never re-registers.
const REGISTER_DELAY_MS = 200;
const STORAGE_KEY_AUTO_CONNECT = 'autoConnectEnabled';

// ─── State ────────────────────────────────────────────────────────────

let currentPort: number | null = null;
let handshakeVerified = false;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
let heartbeatPending = false;
let heartbeatSeq = 0;
let missedPongs = 0;
let retryCount = 0;
let windowId: string | null = null;
let isConnecting = false;
let connectionSessionId = 0;
// Whether we've already shown the "Connected" toast for the current connected
// era. Stays true across silent auto-reconnects (heartbeat blips) so they don't
// spam the toast; reset only on a real outage (daemon-not-found retry branch) or
// an explicit user reconnect/stop, so the NEXT genuine connect announces once.
let connectionAnnounced = false;

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
	connectionAnnounced = false;
	cancelConnectionFlow();
	void scanAndConnect();
}

/**
 * Stop the connection and cancel retries.
 *
 * @param showToast - whether to show a toast confirming the stop
 */
export function stop(showToast = true): void {
	connectionAnnounced = false;
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
		// Daemon is genuinely gone — let the next successful connect announce again.
		connectionAnnounced = false;
		if (retryCount <= MAX_RETRIES) {
			eda.sys_Message.showToastMessage(
				`${eda.sys_I18n.text('Daemon not found, retrying...')} (${retryCount}/${MAX_RETRIES})`,
			);
			scheduleRetry(sessionId, RETRY_DELAY_MS);
		}
		else {
			// Don't strand the user: keep scanning forever on a quiet slow poll so a
			// daemon started later auto-connects. Announce the switch once, then go
			// silent (no toast spam every 10s).
			if (retryCount === MAX_RETRIES + 1) {
				eda.sys_Message.showToastMessage(
					eda.sys_I18n.text('Daemon not found — will keep retrying in the background; just start the daemon.'),
				);
			}
			scheduleRetry(sessionId, SLOW_RETRY_DELAY_MS);
		}
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
		let registerTimer: ReturnType<typeof setTimeout>;

		const settle = (success: boolean) => {
			if (settled) {
				return;
			}
			settled = true;
			clearTimeout(timer);
			clearTimeout(registerTimer);
			if (!success && isConnectionSessionActive(sessionId)) {
				closeWebSocket();
			}
			resolve(success);
		};

		if (!isConnectionSessionActive(sessionId)) {
			resolve(false);
			return;
		}

		// Close any stale connection first. CRITICAL: register() silently ignores
		// the new url/callback if a connection with the same id is still "active"
		// (per eda.sys_WebSocket docs). close() is async, so registering in the
		// same tick leaves the PREVIOUS session's callback bound — it then swallows
		// the daemon's pong, the heartbeat times out, and we reconnect forever.
		// Wait a beat after close() so EasyEDA fully releases the id first.
		closeWebSocket();

		const doRegister = (): void => {
			if (!isConnectionSessionActive(sessionId)) {
				settle(false);
				return;
			}
			timer = setTimeout(() => settle(false), CONNECTION_TIMEOUT_MS);
			handshakeVerified = false;
			diag(`register port=${port} session=${sessionId}`);

			try {
				eda.sys_WebSocket.register(
					WS_ID,
					`ws://127.0.0.1:${port}/eda`,
					async (event: MessageEvent) => {
						let msg: InboundFrame;
						try {
							msg = JSON.parse(event.data) as InboundFrame;
						}
						catch (err) {
							console.error('[easyeda-agent] Failed to parse frame:', err);
							return;
						}

						// A callback left bound from a previous session (id-reuse race).
						// Ignore it entirely — it must NOT touch the shared heartbeat
						// state, or a stale pong would mask the CURRENT session's
						// liveness. The current session's own loop tracks its misses.
						if (!isConnectionSessionActive(sessionId)) {
							diag(`onMessage STALE session=${sessionId} type=${msg?.type}`);
							return;
						}

						// Handshake phase.
						if (msg.type === 'handshake') {
							if ((msg as { service?: string }).service === SERVICE_ID) {
								handshakeVerified = true;
								windowId = crypto.randomUUID();
								sendRegister();
								void sendContext();
								if (!connectionAnnounced) {
									connectionAnnounced = true;
									eda.sys_Message.showToastMessage(
										`${eda.sys_I18n.text('Connected to easyeda-agent')} (port ${port})`,
									);
								}
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
		};

		registerTimer = setTimeout(doRegister, REGISTER_DELAY_MS);
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

/**
 * Emit a low-volume diagnostic line to the daemon (surfaces in the daemon log as
 * "connector LOG: ..."). Reserved for connection-lifecycle events — reconnect
 * reasons and register attempts — to aid recovery/troubleshooting from the daemon
 * side. Deliberately NOT called per ping/pong, to keep the daemon log readable.
 * Best-effort; never throws.
 */
function diag(msg: string): void {
	try {
		eda.sys_WebSocket.send(WS_ID, JSON.stringify({ type: 'log', msg }));
	}
	catch { /* socket not ready — ignore */ }
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

		const reconnect = (reason: string): void => {
			diag(`${reason}, session=${sessionId} -> reconnect`);
			cancelConnectionFlow();
			void scanAndConnect();
		};

		// Liveness check BEFORE sending the next ping: if the previous ping is
		// still unanswered, count it as a miss. Only reconnect once we've missed
		// MAX_MISSED_PONGS in a row — a single lagged pong is not a dead socket.
		if (heartbeatPending) {
			missedPongs += 1;
			if (missedPongs >= MAX_MISSED_PONGS) {
				reconnect(`liveness lost: ${missedPongs} pings unanswered`);
				return;
			}
		}
		else {
			missedPongs = 0;
		}

		heartbeatPending = true;
		heartbeatSeq += 1;
		try {
			// Send directly (not via sendFrame) so a throw — which means the
			// underlying socket is gone — becomes an immediate reconnect signal.
			eda.sys_WebSocket.send(WS_ID, JSON.stringify({ type: 'ping', id: `hb-${heartbeatSeq}` }));
		}
		catch {
			reconnect('heartbeat send failed (socket gone)');
		}
	}, HEARTBEAT_INTERVAL_MS);
}

function stopHeartbeat(): void {
	if (heartbeatTimer) {
		clearInterval(heartbeatTimer);
		heartbeatTimer = null;
	}
	heartbeatPending = false;
	missedPongs = 0;
}

// ─── Retry ────────────────────────────────────────────────────────────

function scheduleRetry(sessionId: number, delayMs: number): void {
	clearRetryTimer();
	retryTimer = setTimeout(() => {
		if (!isConnectionSessionActive(sessionId) || isConnecting) {
			return;
		}
		void scanAndConnect();
	}, delayMs);
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
			missedPongs = 0;
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
