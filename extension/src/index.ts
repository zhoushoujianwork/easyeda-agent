/**
 * EasyEDA Agent Connector — extension entry point.
 *
 * Bridges the easyeda-agent Go daemon to the official `eda.*` API over a local
 * WebSocket. On startup it scans ports 60832-60841 (0xEDA0-0xEDA9), validates the daemon
 * handshake (service "easyeda-agent"), registers a windowId, sends context, and
 * keeps a heartbeat. Incoming `request` frames are dispatched to typed actions
 * (see ./actions) and answered with `response` frames.
 *
 * Exported functions are wired to menu items in `extension.json`.
 */

import * as extensionConfig from '../extension.json';
import {
	getConnectionStatus,
	reconnect as transportReconnect,
	start as transportStart,
	stop as transportStop,
} from './transport';

const STORAGE_KEY_AUTO_CONNECT = 'autoConnectEnabled';

// ─── Lifecycle ────────────────────────────────────────────────────────

/**
 * Extension activation entry (supports onStartupFinished auto-start).
 *
 * @param status - activation reason (e.g. 'onStartupFinished')
 * @param arg - optional activation argument
 */
// eslint-disable-next-line unused-imports/no-unused-vars
export function activate(status?: 'onStartupFinished', arg?: string): void {
	transportStart();
}

/**
 * Extension deactivation: tear down the connection without showing a toast.
 */
export function deactivate(): void {
	transportStop(false);
}

// ─── Menu actions ─────────────────────────────────────────────────────

/**
 * Manually reconnect (menu item).
 */
export function reconnect(): void {
	transportReconnect();
}

/**
 * Stop the connection and cancel retries (menu item).
 */
export function stopConnection(): void {
	transportStop();
}

/**
 * Toggle the auto-connect-on-startup preference (menu item).
 */
export async function toggleAutoConnect(): Promise<void> {
	const current = eda.sys_Storage.getExtensionUserConfig(STORAGE_KEY_AUTO_CONNECT);
	const currentlyEnabled = current !== false;
	await eda.sys_Storage.setExtensionUserConfig(STORAGE_KEY_AUTO_CONNECT, !currentlyEnabled);
	const msgKey = currentlyEnabled ? 'Auto-Connect disabled' : 'Auto-Connect enabled';
	eda.sys_Message.showToastMessage(eda.sys_I18n.text(msgKey));
}

/**
 * Show the About dialog with the current connection status (menu item).
 */
export function about(): void {
	const status = getConnectionStatus();
	let statusLine: string;
	if (status.connected) {
		const portInfo = `Connected (port ${status.port})`;
		const windowInfo = status.windowId ? `\nWindow ID: ${status.windowId}` : '\nWindow ID: (not registered)';
		statusLine = `${portInfo}${windowInfo}`;
	}
	else if (status.connecting) {
		statusLine = 'Connecting...';
	}
	else {
		statusLine = 'Disconnected';
	}

	eda.sys_Dialog.showInformationMessage(
		`EasyEDA Agent Connector v${extensionConfig.version}\n${statusLine}`,
		'About',
	);
}
