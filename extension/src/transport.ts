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
 *   │  Go daemon    │  60832-60841  │  (EasyEDA Pro)    │
 *   └──────────────┘                └──────────────────┘
 *
 * Port range is 0xEDA0-0xEDA9 (60832-60841) — "EDA" spelled in hex, and
 * deliberately far from 49620-49629, which the OFFICIAL eext-run-api-gateway
 * scans (we originally copied that convention; two ecosystems fighting over
 * one port bind was the result — see docs/ecosystem-survey.md).
 */

import { buildContextFrame, readEasyEdaVersion } from './eda-context';
import { runAction } from './actions';
import {
	ActionError,
	CAPABILITIES,
	CONNECTOR_VERSION,
	ErrorCodes,
	type ContextFrame,
	type InboundFrame,
	PROTOCOL_VERSION,
	type RegisterFrame,
	type RequestFrame,
	type ResponseFrame,
	SERVICE_ID,
} from './protocol';

// ─── Configuration ───────────────────────────────────────────────────

const WS_ID = 'easyeda-agent';
const PORT_START = 0xeda0; // 60832 — "EDA0" in hex; own range, no official-gateway conflict
const PORT_END = 0xeda9; // 60841
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
// A full 10-port scan (each up to CONNECTION_TIMEOUT_MS + REGISTER_DELAY_MS) settles
// in well under 20s. If `isConnecting` stays true longer than this many watchdog
// ticks, the flow is wedged (a session invalidated mid-scan, or a renderer that was
// suspended while backgrounded, can leak isConnecting=true) — and a wedged
// isConnecting freezes EVERY reconnect (watchdogTick, scanAndConnect, AND the wake
// listeners all early-return on it), which is exactly the "only reopening the window
// fixes it" bug. The watchdog force-resets past this bound.
const STUCK_CONNECTING_TICKS = 8; // ~24s @ 3s/tick
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
let watchdogStarted = false;
let watchdogWorker: Worker | null = null;
// Set by an explicit stop() so the always-on watchdog does NOT immediately
// reconnect behind the user's back; cleared by start()/reconnect().
let suspended = false;
let heartbeatPending = false;
let heartbeatSeq = 0;
// Watchdog tick counter + the tick at which the current connect flow started, so a
// wedged isConnecting can be detected and force-reset (see STUCK_CONNECTING_TICKS).
let watchdogTicks = 0;
let connectingSinceTick = 0;
let missedPongs = 0;
let retryCount = 0;
let windowId: string | null = null;
// Signature of the last context frame pushed to the daemon, so the heartbeat can
// re-send context ONLY when the active project/document actually changed (e.g.
// the user switched tabs in the UI). Reset on each new connection so a reconnect
// always re-pushes. Empty string = nothing sent yet.
let lastContextSig = '';
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
	suspended = false;
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
	suspended = true; // keep the watchdog from auto-reconnecting after an explicit stop
	cancelConnectionFlow();
	if (showToast) {
		eda.sys_Message.showToastMessage(eda.sys_I18n.text('Connection stopped'));
	}
}

/**
 * Start the connection flow if auto-connect is enabled.
 */
export function start(): void {
	suspended = false;
	startWatchdog(); // always-on background-immune reconnect driver
	if (autoConnectEnabled()) {
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
		// Toast ONCE per outage (on the first failed scan), then retry SILENTLY.
		// Previously every fast retry toasted "(n/MAX)" every RETRY_DELAY_MS — at a
		// 3s cadence the toasts stacked and obscured the UI ("one starts before the
		// last ends"). The retry cadence is unchanged (fast recovery); only the
		// notification is deduped to a single background-retry notice per outage. The
		// eventual reconnect announces once via connectionAnnounced.
		if (retryCount === 1) {
			eda.sys_Message.showToastMessage(
				eda.sys_I18n.text('Daemon not found — retrying in the background; just start the daemon.'),
			);
		}
		// Fast retries first (quick recovery from a daemon restart), then fall back to
		// a quiet slow poll so a daemon started much later still auto-connects.
		scheduleRetry(sessionId, retryCount <= MAX_RETRIES ? RETRY_DELAY_MS : SLOW_RETRY_DELAY_MS);
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
								lastContextSig = '';
								sendRegister();
								void sendContext(true);
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

// contextSig fingerprints the project/document fields that matter for routing,
// so the heartbeat can skip re-sending an unchanged context.
function contextSig(frame: ContextFrame): string {
	return [frame.projectUuid, frame.documentUuid, frame.documentType, frame.tabId].join('|');
}

// sendContext pushes the current project/document context to the daemon. With
// force=true (on connect) it always sends; otherwise (on heartbeat) it sends
// only when the context changed since the last push — so `easyeda daemon health`
// reflects a UI tab-switch within one heartbeat (~3s) without flooding the
// socket every tick.
async function sendContext(force = false): Promise<void> {
	if (!windowId) {
		return;
	}
	try {
		const frame = await buildContextFrame(windowId);
		const sig = contextSig(frame);
		if (!force && sig === lastContextSig) {
			return;
		}
		lastContextSig = sig;
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

// startHeartbeat just (re)arms the liveness state on a fresh connection — the
// actual pinging is driven by the watchdog ticker (see startWatchdog), which is
// worker-backed so it keeps firing even when EasyEDA's window is backgrounded and
// the main thread's setInterval is frozen (the old nudge-to-reconnect bug).
function startHeartbeat(_sessionId: number): void {
	heartbeatPending = false;
	missedPongs = 0;
}

function stopHeartbeat(): void {
	heartbeatPending = false;
	missedPongs = 0;
}

// heartbeatTick runs one liveness round on the live connection: count a miss if
// the previous ping is still unanswered, reconnect after MAX_MISSED_PONGS, else
// send the next ping (+ piggyback a context refresh). Called by the watchdog.
function heartbeatTick(): void {
	if (!handshakeVerified) {
		return;
	}
	const reconnectNow = (reason: string): void => {
		diag(`${reason} -> reconnect`);
		cancelConnectionFlow();
		void scanAndConnect();
	};

	// Liveness check BEFORE sending the next ping: a still-pending ping is a miss.
	// Only reconnect after MAX_MISSED_PONGS in a row — one lagged pong isn't death.
	if (heartbeatPending) {
		missedPongs += 1;
		if (missedPongs >= MAX_MISSED_PONGS) {
			reconnectNow(`liveness lost: ${missedPongs} pings unanswered`);
			return;
		}
	}
	else {
		missedPongs = 0;
	}

	heartbeatPending = true;
	heartbeatSeq += 1;
	try {
		// Send directly (not via sendFrame) so a throw — the socket is gone —
		// becomes an immediate reconnect signal.
		eda.sys_WebSocket.send(WS_ID, JSON.stringify({ type: 'ping', id: `hb-${heartbeatSeq}` }));
	}
	catch {
		reconnectNow('heartbeat send failed (socket gone)');
		return;
	}
	void sendContext();
}

// ─── Watchdog ─────────────────────────────────────────────────────────
// One always-on ticker drives BOTH the heartbeat (when connected) and reconnect
// retries (when not). It runs in a Web Worker because a worker's timer keeps
// firing while the EasyEDA window is backgrounded, whereas the main thread's
// setInterval is throttled/frozen — that freeze was why a daemon restart used to
// need a manual window "nudge" to reconnect. If the webview blocks workers
// (CSP/blob), we fall back to a main-thread interval plus focus/online listeners.

function autoConnectEnabled(): boolean {
	return eda.sys_Storage.getExtensionUserConfig(STORAGE_KEY_AUTO_CONNECT) !== false;
}

function watchdogTick(): void {
	watchdogTicks += 1;
	if (isConnecting) {
		// Self-heal: a connect flow that hasn't settled in a bounded number of ticks
		// is wedged (leaked isConnecting=true). Force a clean reset so the fall-through
		// scan below starts fresh — otherwise every reconnect path stays frozen and
		// only reopening the window recovers.
		if (watchdogTicks - connectingSinceTick >= STUCK_CONNECTING_TICKS) {
			diag(`watchdog: connect flow stuck ${(watchdogTicks - connectingSinceTick) * HEARTBEAT_INTERVAL_MS / 1000}s -> force reset`);
			cancelConnectionFlow(false); // isConnecting=false, new session, keep retryCount
			connectingSinceTick = watchdogTicks; // give the fresh scan a full window
			// fall through to start a new scan this tick
		}
		else {
			return; // a connect attempt is legitimately in flight
		}
	}
	else {
		connectingSinceTick = watchdogTicks; // advance the baseline while idle/connected
	}
	if (handshakeVerified) {
		heartbeatTick();
	}
	else if (!suspended && autoConnectEnabled()) {
		void scanAndConnect();
	}
}

function startWatchdog(): void {
	if (watchdogStarted) {
		return;
	}
	watchdogStarted = true;
	const tick = (): void => {
		try {
			watchdogTick();
		}
		catch { /* never let a tick throw kill the loop */ }
	};
	try {
		// Inline blob worker: it only owns a timer and posts a tick — all eda.* work
		// stays on the main thread (eda.* is main-thread only).
		const code = `setInterval(function(){postMessage(0);}, ${HEARTBEAT_INTERVAL_MS});`;
		const url = URL.createObjectURL(new Blob([code], { type: 'application/javascript' }));
		watchdogWorker = new Worker(url);
		watchdogWorker.onmessage = tick;
		diag('watchdog: worker ticker started');
	}
	catch {
		diag('watchdog: worker unavailable — main-thread interval (throttled when backgrounded)');
		setInterval(tick, HEARTBEAT_INTERVAL_MS);
	}
	// Belt-and-suspenders: recover immediately on window focus / network up — the
	// main path for the setInterval-fallback case, and faster recovery generally.
	// When we come back to the foreground and are NOT verified, force a clean
	// reconnect even if isConnecting looks true: a renderer suspended while
	// backgrounded can leave a half-finished connect flow wedged, and the old
	// `!isConnecting` guard made foregrounding a no-op — so only reopening the whole
	// window recovered. cancelConnectionFlow() clears the wedge first.
	const wake = (): void => {
		if (handshakeVerified || suspended || !autoConnectEnabled()) {
			return;
		}
		cancelConnectionFlow(false);
		void scanAndConnect();
	};
	try {
		globalThis.addEventListener?.('focus', wake);
		globalThis.addEventListener?.('online', wake);
		globalThis.addEventListener?.('visibilitychange', wake);
	}
	catch { /* no addEventListener in this host — ignore */ }
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
