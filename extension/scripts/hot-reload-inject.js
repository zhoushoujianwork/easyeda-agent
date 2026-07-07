// Browser-side half of the connector hot-reload (see docs/dev-environment.md §5).
//
// Run this INSIDE the EasyEDA Pro editor page — paste into the browser devtools
// console, or (agent-driven) pass as the body of a chrome-devtools MCP
// `evaluate_script` call. It pulls the freshly-built bundle from the local WS
// server (hot-reload-server.mjs), overwrites the connector's executable record in
// IndexedDB, bumps its stored version, and reloads the page. EasyEDA re-reads the
// extension from IndexedDB on load, so the new code runs — no uninstall, no
// re-import, no EasyEDA restart.
//
// FILL IN THESE THREE before running (all discoverable, see below):
//   TEAM    — teamUuid, from `easyeda project info` (or the User_<teamUuid>_v6 DB name)
//   UUID    — the connector's extension uuid, from extension/extension.json ("uuid")
//   VERSION — the new version string, from extension/extension.json ("version")
// PORT defaults to 8790 (match hot-reload-server.mjs --port).
//
// NOTE: IndexedDB store names/keys are EasyEDA-internal, not a stable public API
// (today the DB is `User_<teamUuid>_v6`); re-verify if EasyEDA bumps its schema.

async function hotReloadConnector({ TEAM, UUID, VERSION, PORT = 8790 }) {
	// 1. Fetch the new bundle over ws:// (http:// is blocked as mixed content).
	const b64 = await new Promise((resolve, reject) => {
		const ws = new WebSocket(`ws://127.0.0.1:${PORT}`);
		ws.onopen = () => ws.send(JSON.stringify({ action: 'getFile' }));
		ws.onmessage = (ev) => {
			const m = JSON.parse(ev.data);
			if (m.action === 'getFile_Response') {
				if (m.error) reject(new Error(m.error));
				else resolve(m.content);
				ws.close();
			}
		};
		ws.onerror = () => reject(new Error('WS error — is hot-reload-server.mjs running on port ' + PORT + '?'));
		setTimeout(() => reject(new Error('WS timeout')), 15000);
	});
	const bin = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));

	// 2. Open the extension IndexedDB.
	const db = await new Promise((res, rej) => {
		const q = indexedDB.open(`User_${TEAM}_v6`);
		q.onsuccess = () => res(q.result);
		q.onerror = () => rej(q.error);
	});
	const get = (store, key) => new Promise((res, rej) => {
		const r = db.transaction(store, 'readonly').objectStore(store).get(key);
		r.onsuccess = () => res(r.result);
		r.onerror = () => rej(r.error);
	});
	const put = (store, val, key) => new Promise((res, rej) => {
		const os = db.transaction(store, 'readwrite').objectStore(store);
		const r = os.keyPath == null && key !== undefined ? os.put(val, key) : os.put(val);
		r.onsuccess = () => res(true);
		r.onerror = () => rej(r.error);
	});

	// 3. Overwrite the connector's only executable file: <uuid>|dist/index.js.
	const fileKey = `${UUID}|dist/index.js`;
	const rec = await get('extensionsObjectStorage', fileKey);
	if (!rec) throw new Error(`file record not found: ${fileKey} (wrong UUID, or connector not installed?)`);
	rec.source = new File([bin], 'index.js', { type: 'text/javascript' });
	await put('extensionsObjectStorage', rec, fileKey);

	// 4. Bump the stored version so EasyEDA treats it as changed.
	const idx = await get('extensionsIndex', UUID);
	if (!idx) throw new Error(`index record not found for uuid ${UUID}`);
	const oldVersion = idx.config && idx.config.version;
	if (idx.config) idx.config.version = VERSION;
	if (typeof idx.fileSize === 'number') idx.fileSize = bin.length;
	await put('extensionsIndex', idx, UUID);

	// 5. Reload so EasyEDA re-reads the extension from IndexedDB.
	location.reload();
	return { ok: true, bytes: bin.length, oldVersion, newVersion: VERSION };
}

// Example call (replace the placeholders):
// hotReloadConnector({ TEAM: '<teamUuid>', UUID: '<extensionUuid>', VERSION: '<x.y.z>' });
