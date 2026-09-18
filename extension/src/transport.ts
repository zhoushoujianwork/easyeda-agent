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
 *   │  Go daemon    │ 127.0.0.1     │  (EasyEDA Pro)    │
 *   │               │      :60832   │                   │
 *   └──────────────┘                └──────────────────┘
 *
 * **One pinned port, not a sweep.** The daemon binds a SINGLE fixed port, 60832
 * (0xEDA0 — "EDA" spelled in hex), and never spills to the next one: a second
 * daemon on 60832 is replaced, a foreign holder is refused (see
 * `internal/app/cmd_daemon.go`). So 60833-60841 can never hold a daemon, and
 * probing them is pure idling — every dead port burns a full
 * CONNECTION_TIMEOUT_MS because `eda.sys_WebSocket.register()` never reports a
 * refused connection. Real logs showed ~7s spent walking dead ports on every
 * reconnect, which under `make dev` (air rebuilds the daemon on each .go edit)
 * is paid over and over. This transport therefore tries 60832 ONLY, with
 * exponential backoff (see BACKOFF_*).
 *
 * The rest of 0xEDA0-0xEDA9 (60832-60841) stays RESERVED for us — deliberately
 * far from 49620-49629, which the OFFICIAL eext-run-api-gateway scans (we
 * originally copied that convention; two ecosystems fighting over one port bind
 * was the result — see docs/ecosystem-survey.md). A non-standard deployment can
 * still reach it through the `daemonPorts` escape hatch below.
 */

import { ActionQueue, isBypassAction } from './action-queue';
import { sweepDeadlines } from './deadlines';
import { buildContextFrame, readEasyEdaVersion } from './eda-context';
import { runAction } from './actions';
import { createWebSocketId } from './transport-identity';
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
import { describeThrown } from './util';

// ─── Configuration ───────────────────────────────────────────────────

// EasyEDA 3.2.175 can activate the same extension more than once in one app
// window. eda.sys_WebSocket is shared across those activations, so a fixed id
// makes each activation close/re-register the other's socket forever. Give each
// activation its own host-managed socket; the daemon coalesces registrations
// that report the same project/document/tab when routing an action.
const WS_ID_BASE = createWebSocketId();
// 叠加在 PR #154 的 activation-scoped id 之上:即便每个激活已有独立 id,真机 soak
// 实测(2026-08-04,停 daemon 45s/60s 各一轮)仍会卡死 —— 第二轮 210s 没能自愈,
// 60832 上持续报 "closed before the connection is established"。activation-scoped
// 解决的是「多激活互踢」,解决不了「本激活自己的 id 被 EasyEDA 判为 active 后
// register() 被静默忽略」。连续整轮扫描失败后换一个全新 id 才是逃生口。
// 阈值从 2 提到 4:一"轮失败"过去是整段 10 端口扫描(~18s),现在是一次 1.7s 的
// 尝试。沿用 2 会把换 id 的节奏从 ~40s 一次压到 ~4s 一次,在 EasyEDA 共享的 socket
// 表里堆一串死 id —— 那正是这段逻辑当初要躲的race。4 次失败 + 退避 ≈ 十几秒起,
// 之后每 4 次 ≈ 40s 一换,和改动前的墙钟压力同量级。
const WS_ID_ROTATE_AFTER_FAILED_SCANS = 4;
let wsId: string = WS_ID_BASE;
let wsIdGeneration = 0;
// The one port the daemon binds. Not a range start — the daemon never spills
// (see the file header).
export const DAEMON_PORT = 0xeda0; // 60832 — "EDA0" in hex
// End of the range we reserve for ourselves. Nothing scans it by default; it is
// here as the documented bound for the `daemonPorts` override and as the marker
// that 60833-60841 stay ours (no official-gateway conflict).
export const RESERVED_PORT_END = 0xeda9; // 60841
// ─ Escape hatch ─
// Pinning is right for the shipped topology (one daemon, one port), NOT a law of
// physics. A non-standard deployment (several daemons side by side, a port
// already taken by something else, `easyeda daemon start --ports …`) can point
// the connector elsewhere by setting this extension user-config key:
//
//   eda.sys_Storage.setExtensionUserConfig('daemonPorts', '60832-60841')
//   eda.sys_Storage.setExtensionUserConfig('daemonPorts', '60840,60832')
//   eda.sys_Storage.setExtensionUserConfig('daemonPorts', 60900)
//
// (run it from `debug.exec_js`, or from the editor's script console). It is read
// at the START of every attempt, so it takes effect on the next retry — no
// re-import, no EasyEDA restart. Unset/garbage → the pinned default.
const STORAGE_KEY_DAEMON_PORTS = 'daemonPorts';
// Hard cap on an override list: a typo like "1-65535" must not turn reconnect
// into an unbounded sweep that never comes back. 12 × ~1.7s stays inside the
// STUCK_CONNECTING_TICKS watchdog bound (~24s), so even a maxed-out override
// can't be mistaken for a wedged connect flow.
const MAX_OVERRIDE_PORTS = 12;
// ─ Backoff ─
// Retry cadence when nobody answers on the pinned port. The first attempt is
// immediate; then 0.5s → 1s → 2s → 4s → 8s (capped), each ±25% jitter.
//
// Why these numbers: one attempt now costs REGISTER_DELAY_MS + CONNECTION_TIMEOUT_MS
// ≈ 1.7s (one port, not ten), so the common case this change targets — the daemon
// restarting under `make dev` — reconnects in ~2-3s: the daemon is back within a
// second or two and the 0.5s/1s steps land right on top of it. The cap exists for
// the OTHER case (daemon deliberately stopped for minutes): at 8s the socket
// churn against EasyEDA's shared id table is ~3x gentler than the old flat 3s,
// while a daemon started later still auto-connects within ~8s — better than the
// old 10s slow poll it replaces. Jitter keeps several activations/windows from
// re-registering in lockstep.
const BACKOFF_BASE_MS = 500;
const BACKOFF_MAX_MS = 8000;
const BACKOFF_JITTER = 0.25;
// EasyEDA's eda.sys_WebSocket closes idle connections after ~5s of silence.
// Ping more often than that to keep the socket alive between actions, which
// otherwise causes a register -> 5s silence -> close -> reconnect storm.
const HEARTBEAT_INTERVAL_MS = 3000;
// Liveness is consecutive-miss based, NOT a single round-trip deadline. The
// daemon never idle-closes, and EasyEDA's webview can lag pong delivery under
// load (canvas redraw, GC). Only give up after this many pings go unanswered in
// a row (~9s of true silence).
const MAX_MISSED_PONGS = 3;
// Per-attempt budget. A dead port burns this in full — register() never reports
// a refused connection, so only the timeout ends the attempt. With the port
// pinned this is paid ONCE per attempt (~1.7s with REGISTER_DELAY_MS) instead of
// ten times per sweep; the pacing between attempts is BACKOFF_*, not this value.
//
// It is tempting to shrink this (back when 10 dead ports ≈ 18s), and 600ms was tried:
// **it made recovery strictly worse** (soak 2026-08-04: 45s ✅ / 60s ❌ / 75s ❌
// vs 45s ✅ / 60s ✅ / 75s ❌ at 1500ms; one run reached scan session=58 without
// ever reconnecting). A faster sweep doubles the rate of close()/register()
// cycles against EasyEDA's shared socket table, and REGISTER_DELAY_MS's 200ms
// release window stops being enough — the real bottleneck is that id state
// machine, not latency, so speeding the loop up feeds the very race it loses.
// The sweep cost is instead removed by pinning: a reconnect probes ONE port.
const CONNECTION_TIMEOUT_MS = 1500;
// A pinned attempt settles in ~1.7s; even a MAX_OVERRIDE_PORTS-wide override
// sweep (each port up to CONNECTION_TIMEOUT_MS + REGISTER_DELAY_MS) stays under
// this bound. If `isConnecting` stays true longer than this many watchdog
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
// The last port that completed a handshake. Survives disconnects on purpose —
// it is the hint that makes a reconnect a single attempt instead of a sweep.
let lastGoodPort: number | null = null;
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
// Earliest wall-clock time the next attempt may start (0 = right now). This is
// what makes the backoff REAL: the watchdog ticks every HEARTBEAT_INTERVAL_MS and
// calls scanAndConnect() whenever we are not connected, so a retry timer alone
// could never pace attempts slower than 3s (the old SLOW_RETRY_DELAY_MS was
// silently defeated that way). Every non-forced entry into scanAndConnect checks
// this gate.
let nextAttemptAt = 0;
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

/**
 * Parse a `daemonPorts` override into a bounded, de-duplicated port list.
 *
 * Accepts what a human would plausibly type into the config: a number, a
 * `"60832"` / `"60832-60841"` / `"60840,60832"` string, or an array of those.
 * Anything unparseable is dropped rather than throwing — this runs on the
 * reconnect path, where a bad config must degrade to the pinned default, never
 * break the transport.
 *
 * @param raw - the raw user-config value
 * @returns the ports to try, in the order given (empty = no usable override)
 */
export function parsePorts(raw: unknown): number[] {
	const ports: number[] = [];
	const push = (port: number): void => {
		if (Number.isInteger(port) && port >= 1 && port <= 65535
			&& !ports.includes(port) && ports.length < MAX_OVERRIDE_PORTS) {
			ports.push(port);
		}
	};

	const items: unknown[] = Array.isArray(raw)
		? raw
		: typeof raw === 'string' ? raw.split(',') : [raw];

	for (const item of items) {
		if (typeof item === 'number') {
			push(item);
			continue;
		}
		if (typeof item !== 'string') {
			continue;
		}
		const token = item.trim();
		const range = /^(\d+)\s*-\s*(\d+)$/.exec(token);
		if (range) {
			const lo = Number(range[1]);
			const hi = Number(range[2]);
			for (let port = lo; port <= hi && ports.length < MAX_OVERRIDE_PORTS; port++) {
				push(port);
			}
			continue;
		}
		if (/^\d+$/.test(token)) {
			push(Number(token));
		}
	}
	return ports;
}

/**
 * The ports this attempt will try, most-likely first.
 *
 * Normally exactly `[DAEMON_PORT]` — the daemon binds one fixed port and never
 * spills, so there is nothing else to look at. With a `daemonPorts` override the
 * list can be longer, and then the last port that completed a handshake goes
 * first: a restarted daemon re-binds the same port in practice, so the hint
 * turns the common reconnect back into a single attempt.
 *
 * @param override - the raw `daemonPorts` user-config value (undefined = pinned)
 * @param lastGood - the last port that completed a handshake, if any
 * @returns the ordered ports to try
 */
export function resolvePorts(override: unknown, lastGood: number | null): number[] {
	const configured = parsePorts(override);
	const ports = configured.length > 0 ? configured : [DAEMON_PORT];
	if (lastGood !== null && ports.length > 1 && ports.includes(lastGood)) {
		return [lastGood, ...ports.filter((port) => port !== lastGood)];
	}
	return ports;
}

/**
 * Read the configured ports for this attempt (pinned default on any failure).
 *
 * @returns the ordered ports to try
 */
function attemptPorts(): number[] {
	let override: unknown;
	try {
		override = eda.sys_Storage.getExtensionUserConfig(STORAGE_KEY_DAEMON_PORTS);
	}
	catch {
		return [DAEMON_PORT];
	}
	return resolvePorts(override, lastGoodPort);
}

/**
 * Delay before the Nth consecutive failed attempt is retried.
 *
 * Exponential from BACKOFF_BASE_MS, capped at BACKOFF_MAX_MS, ±BACKOFF_JITTER.
 *
 * @param failures - consecutive failed attempts so far (1 = the first failure)
 * @param rand - random source in [0,1) (injectable for tests)
 * @returns the delay in milliseconds
 */
export function backoffDelayMs(failures: number, rand: () => number = Math.random): number {
	const n = Math.max(1, Math.floor(failures));
	const base = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** (n - 1));
	return Math.round(base * (1 + (rand() * 2 - 1) * BACKOFF_JITTER));
}

function rotateWsId(): void {
	closeWebSocket();
	wsIdGeneration += 1;
	wsId = `${WS_ID_BASE}-r${wsIdGeneration}`;
	diag(`rotated websocket id → ${wsId} (previous id never accepted a registration)`);
}

function closeWebSocket(): void {
	try {
		eda.sys_WebSocket.close(wsId);
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
		nextAttemptAt = 0; // a fresh outage starts at the fast end of the backoff
	}
	closeWebSocket();
}

// ─── Public control ───────────────────────────────────────────────────

/**
 * Force a reconnect: cancel any active flow and retry the daemon port now.
 */
export function reconnect(): void {
	eda.sys_Message.showToastMessage(eda.sys_I18n.text('Reconnecting...'));
	connectionAnnounced = false;
	suspended = false;
	cancelConnectionFlow();
	void scanAndConnect(true); // explicit user action — never sit out the backoff
}

/**
 * Stop the connection and cancel retries.
 *
 * @param showToast - whether to show a toast confirming the stop
 */
export function stop(showToast = true): void {
	bootstrapGeneration += 1;
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
	// Skip the kick when a connection is already established or in flight: the
	// host re-dispatches activation events (and re-evaluates this bundle) on more
	// than one occasion, and re-running scanAndConnect() on a verified session
	// would tear down a working socket for nothing.
	if (!handshakeVerified && !isConnecting && autoConnectEnabled()) {
		void scanAndConnect();
	}
}

// ─── Module-load bootstrap ────────────────────────────────────────────
// Do not depend solely on the host calling activate().
//
// EasyEDA Pro (pro-api 0.3.4, api.js) activates user extensions from exactly one
// place, and that place is behind a login gate with no retry:
//
//   function ec() { return globalVariableUserInfo?.uuid === undefined ? false : !!Cl(...) }
//   async function Ig(e, t) {
//     if (!t && !ec()) return;                                  // <-- gives up here
//     async function n(r) {
//       Us(r, OnStartupFinished) && (await Ta(r, {onStartupFinished: true}), s = true);
//       Us(r, OnLogged)          && (await Ta(r, {onLogged: true}),          s = true);
//       s || await Ta(r);
//     }
//     if (e) await n(e);
//     else if (t) for (const r of loadedBuiltinExtensions) await i(r);   // system.startupFinished
//     else        for (const r of loadedExtensions)        await n(r);
//   }
//   bus.pull("system.startupFinished", () => Ig(void 0, TRUE));          // builtins only
//   bus.rpcService("user.setUserInfo", async t => { ...; changed && (...) && gw() });
//   async function gw() { ...; isExtensionsInitialized || await hw(); ... }
//   async function hw() { ...; isExtensionsInitialized = true; await Ig(void 0, false); }
//
// hw() marks the extension system initialized BEFORE calling Ig(), so a cold
// start where the extension init wins the race against the user info leaves
// every user extension unactivated, and the later user.setUserInfo -> gw() retry
// skips hw()/Ig() forever. Field symptom: the connector works right after an
// import or an extension reload, then silently never comes back on any later
// cold start — the shape recorded in issue #185 ("never self-heals").
//
// The host evaluates this bundle in order to build its sandbox, so module scope
// is the earliest execution point we own. Bootstrapping here means a skipped
// activate() can no longer strand the connector. When the host DOES activate us
// normally, this has already run and activate() becomes a no-op (start() is
// guarded above, startWatchdog() is guarded internally).
let bootstrappedFromModuleLoad = false;
let bootstrapGeneration = 0;

/**
 * Start the transport from the extension module body.
 *
 * Called at module scope by `index.ts`. Whichever of this and the host-driven
 * `activate()` runs first wins; the other is a no-op for this evaluation.
 */
export function bootstrapFromModuleLoad(): void {
	if (bootstrappedFromModuleLoad) {
		return;
	}
	bootstrappedFromModuleLoad = true;
	const generation = bootstrapGeneration;
	// Also the load-time probe: seeing this line in the editor's 日志 panel proves
	// the host evaluated the bundle even though it never called activate().
	diag('module evaluated — self-bootstrapping the transport (host activate() is not required)');
	const begin = (): void => {
		if (generation !== bootstrapGeneration || handshakeVerified) {
			return;
		}
		try {
			start();
		}
		catch { /* sandbox globals not ready mid-evaluation — retried right below */ }
	};
	begin();
	// The sandbox globals are injected around the module body; anything missing
	// during it is present one macrotask later. Both calls are idempotent.
	try {
		setTimeout(begin, 0);
	}
	catch { /* no timers in this sandbox — the synchronous call already ran */ }
}

// ─── Connect (pinned port) ────────────────────────────────────────────

/**
 * Try the daemon port(s), register a WebSocket, and keep the one whose daemon
 * sends a valid `handshake` (service === "easyeda-agent").
 *
 * @param force - bypass the backoff gate (explicit reconnect, window wake, or a
 *   retry timer firing at its own deadline)
 */
async function scanAndConnect(force = false): Promise<void> {
	if (isConnecting) {
		return;
	}
	// Backoff gate. The watchdog calls us every HEARTBEAT_INTERVAL_MS while
	// disconnected, so without this check the retry cadence would be pinned at 3s
	// no matter what scheduleRetry asked for.
	if (!force && nextAttemptAt > 0 && Date.now() < nextAttemptAt) {
		return;
	}

	const sessionId = nextConnectionSessionId();
	isConnecting = true;
	clearRetryTimer();
	const ports = attemptPorts();
	diag(`connect attempt session=${sessionId} ports=${ports.join(',')} retryCount=${retryCount} wsId=${wsId}`);

	try {
		for (const port of ports) {
			if (!isConnectionSessionActive(sessionId)) {
				return;
			}

			const found = await tryConnectToPort(port, sessionId);
			if (!isConnectionSessionActive(sessionId)) {
				return;
			}

			if (found) {
				currentPort = port;
				// Remember it: only matters under a multi-port `daemonPorts`
				// override, where it keeps the reconnect a single attempt
				// (see resolvePorts).
				lastGoodPort = port;
				retryCount = 0;
				nextAttemptAt = 0;
				startHeartbeat();
				return;
			}
		}

		retryCount++;
		// 无人应答 = daemon 不在,或我们的 id 已被焊死、register 全被忽略。
		// 从连接器视角这两者不可分辨,所以连续失败够 N 次就换 id:daemon 真不在时换了
		// 也无副作用,id 被焊死时这是唯一的出路。
		if (retryCount % WS_ID_ROTATE_AFTER_FAILED_SCANS === 0) {
			rotateWsId();
		}
		// Daemon is genuinely gone — let the next successful connect announce again.
		connectionAnnounced = false;
		// Toast ONCE per outage (on the first failed attempt), then retry SILENTLY.
		// Previously every fast retry toasted "(n/MAX)" on each retry — at a 3s
		// cadence the toasts stacked and obscured the UI ("one starts before the
		// last ends"). Retries stay fast; only the notification is deduped to a
		// single background-retry notice per outage. The eventual reconnect
		// announces once via connectionAnnounced. This matters MORE now that the
		// first retries are sub-second.
		if (retryCount === 1) {
			eda.sys_Message.showToastMessage(
				eda.sys_I18n.text('Daemon not found — retrying in the background; just start the daemon.'),
			);
		}
		// Exponential backoff: sub-second at first (a daemon restarting under `make
		// dev` is back within a second or two), settling at BACKOFF_MAX_MS so a
		// genuinely absent daemon is polled quietly instead of hammered. We never
		// give up — the daemon is usually started AFTER the editor, so a terminal
		// give-up would strand every fresh `bin/easyeda daemon`.
		const delay = backoffDelayMs(retryCount);
		nextAttemptAt = Date.now() + delay;
		scheduleRetry(sessionId, delay);
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
					wsId,
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
		eda.sys_WebSocket.send(wsId, JSON.stringify(frame));
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
/**
 * Emit one diagnostic line.
 *
 * Routing matters here, and it is not obvious (2026-08-04, probed on a live
 * EasyEDA Pro 3.2.175):
 *
 * - **`console.*` is DEAD CODE inside an extension.** The EasyEDA sandbox hands
 *   the extension its own `console` whose every method is literally `()=>{}`
 *   (verified via `_EXTAPI_SCRIPT_SPACES_[uuid].console.log.toString()`), so
 *   nothing a connector logs that way reaches DevTools or anywhere else. The
 *   sandbox also blanks `window`/`document`/`localStorage`/`indexedDB`/
 *   `postMessage` to `undefined` — `eda.*` is the ONLY channel out.
 * - **The WebSocket alone was the old behaviour, and it fails exactly when it
 *   matters.** Diagnostics for a connection problem cannot travel over the
 *   connection that is down. That is the structural reason the reconnect bug
 *   stayed a black box for so long.
 *
 * So the primary sink is `eda.sys_Log`: it works while disconnected, the user
 * can read it in the editor's 日志 panel, and `eda.sys_Log.sort()` reads it
 * back programmatically — which is what makes an offline soak diagnosable at
 * all. The WebSocket send is kept as the online path (daemon-side log frames).
 */
function diag(msg: string): void {
	try {
		eda.sys_Log.add(`[easyeda-agent] ${msg}`);
	}
	catch { /* log panel unavailable — never let diagnostics break transport */ }
	try {
		eda.sys_WebSocket.send(wsId, JSON.stringify({ type: 'log', msg }));
	}
	catch { /* socket not ready — ignore */ }
}

// ─── Heartbeat ────────────────────────────────────────────────────────

function startHeartbeat(): void {
	heartbeatPending = false;
	missedPongs = 0;
}

function stopHeartbeat(): void {
	heartbeatPending = false;
	missedPongs = 0;
}

// heartbeatTick runs one liveness round on this activation's socket: count a
// miss if the previous ping is still unanswered, reconnect after
// MAX_MISSED_PONGS, else send the next ping (+ context refresh).
function heartbeatTick(): void {
	if (!handshakeVerified) {
		return;
	}
	const reconnectNow = (reason: string): void => {
		diag(`${reason} -> reconnect`);
		cancelConnectionFlow();
		void scanAndConnect();
	};

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
		eda.sys_WebSocket.send(wsId, JSON.stringify({ type: 'ping', id: `hb-${heartbeatSeq}` }));
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
	// 保底触发所有到期的守卫(队列放弃闸 + 每次平台调用的 withTimeout)。
	// **必须在最前面、且不受任何分支影响**:队首卡死与连接状态无关,而这条
	// worker tick 是本进程里唯一被真机证明不受后台节流影响的时基
	// (2026-08-24:退化 6 分钟里心跳一拍不落,而 setTimeout 一次没响)。
	// 见 deadlines.ts 的头注释。
	try {
		sweepDeadlines();
	}
	catch { /* 清扫失败绝不能拖垮心跳/重连 */ }
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
		// force: the user just came back to this window — retry now rather than
		// sitting out the remainder of a backoff step.
		void scanAndConnect(true);
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
		// force: this timer IS the backoff deadline. Without it, timer/clock skew
		// of a millisecond would bounce off the nextAttemptAt gate and hand the
		// retry to the next watchdog tick instead.
		void scanAndConnect(true);
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

/**
 * 本激活的**唯一咽喉**:所有动作都从这条 FIFO 链上过(旁路名单除外)。
 * 每个窗口/激活一条队列 —— 一次只跑一个 handler。见 action-queue.ts 的文件头。
 */
const actionQueue = new ActionQueue();

async function handleRequest(request: RequestFrame): Promise<void> {
	const base = {
		type: 'response' as const,
		id: request.id,
		version: request.version ?? PROTOCOL_VERSION,
	};

	// 入队是**同步**发生的(submit 在第一个 await 之前就把任务挂上了链),
	// 所以入队顺序 === 消息到达顺序。这一点是整个 happens-before 的地基:
	// 换成先 await 再入队,FIFO 立刻退化回原来的并发。
	const outcome = await actionQueue.submit({
		id: request.id,
		timeoutMs: request.timeoutMs,
		bypass: isBypassAction(request.action),
		run: () => runAction(request.action, request.payload),
	});

	let response: ResponseFrame;
	switch (outcome.status) {
		case 'ok': {
			const result = outcome.value;
			response = { ...base, ok: true };
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
			break;
		}
		case 'error':
			response = { ...base, ok: false, error: toResponseError(outcome.error) };
			break;
		case 'abandoned':
			// daemon 多半已经在 (timeoutMs - 2s) 就超时了,所以这条回执往往落不到
			// 任何等待者手上 —— 那不要紧:它的价值在于**下一条**响应上递增了的
			// seqAbandoned。措辞必须停在可证边界内:我们只知道「不再等它了」,
			// 不知道它到底做没做成。
			response = {
				...base,
				ok: false,
				error: {
					code: ErrorCodes.ACTION_ABANDONED,
					message: `action "${request.action}" was abandoned after ${outcome.waitedMs}ms so the queue could keep flowing`,
					detail: 'the handler is still running; its effect may land later — treat any conclusion about this write as unproven (seqAbandoned was incremented)',
				},
			};
			break;
		case 'overflow':
			response = {
				...base,
				ok: false,
				error: {
					code: ErrorCodes.QUEUE_OVERFLOW,
					message: `connector action queue is full (${outcome.depth} waiting) — this action was NOT executed`,
					detail: 'the editor is not draining actions; wait for the backlog to settle (each head is abandoned after its own timeoutMs) or restart EasyEDA',
				},
			};
			break;
	}

	// 顺序证据挂在**每一条**响应上,包括失败与旁路的。
	response.seq = outcome.stamp.seq;
	response.seqAbandoned = outcome.stamp.seqAbandoned;
	if (outcome.stamp.unordered) {
		response.unordered = true;
	}
	if (outcome.stamp.abandonedIds !== undefined && outcome.stamp.abandonedIds.length > 0) {
		response.abandonedIds = outcome.stamp.abandonedIds;
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
	const message = describeThrown(err);
	return {
		code: ErrorCodes.INTERNAL_ERROR,
		message,
	};
}
