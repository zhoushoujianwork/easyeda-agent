// Hot-reload WS file server for the connector dev loop (see docs/dev-environment.md §5).
//
// WHY: the EasyEDA Pro editor page is HTTPS, so `fetch('http://127.0.0.1/...')` is
// blocked as mixed content — but `ws://127.0.0.1` is allowed (the connector itself
// reaches the daemon that way). So we hand the freshly-built connector bundle to the
// editor page over a local WebSocket, which the page then writes straight into
// IndexedDB (the hot-reload-inject.js companion). No uninstall / re-import / restart.
//
// PROTOCOL (matches dev-environment.md §5): a JSON WebSocket —
//   client → {"action":"getFile"}
//   server → {"action":"getFile_Response","content":<base64>,"size":<bytes>,"version":"x.y.z"}
//
// USAGE:  node extension/scripts/hot-reload-server.mjs [--port 8790] [--bundle <path>] [--keep]
//   Defaults: port 8790, bundle = <this repo>/extension/dist/index.js.
//   Exits ~5s after serving the first request (one-shot), unless --keep is passed.
//   Requires `ws` (an extension devDependency): run `npm install` in extension/ first.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { WebSocketServer } from 'ws';

const here = dirname(fileURLToPath(import.meta.url));

function arg(name, fallback) {
	const i = process.argv.indexOf(`--${name}`);
	return i >= 0 && process.argv[i + 1] ? process.argv[i + 1] : fallback;
}
const port = Number(arg('port', '8790'));
const bundle = resolve(arg('bundle', resolve(here, '..', 'dist', 'index.js')));
const keep = process.argv.includes('--keep');

// Best-effort version tag from extension.json (purely informational in the reply).
let version = '';
try {
	version = JSON.parse(readFileSync(resolve(here, '..', 'extension.json'), 'utf8')).version ?? '';
}
catch { /* optional */ }

const wss = new WebSocketServer({ host: '127.0.0.1', port });
console.log(`hot-reload WS server on ws://127.0.0.1:${port}  (bundle: ${bundle}${version ? `, v${version}` : ''})`);

const bail = keep ? null : setTimeout(() => {
	console.log('no request within 120s — exiting');
	process.exit(0);
}, 120_000);

wss.on('connection', (ws) => {
	ws.on('message', (raw) => {
		let msg = {};
		try { msg = JSON.parse(String(raw)); }
		catch { /* ignore non-JSON */ }
		if (msg.action !== 'getFile') return;
		let buf;
		try { buf = readFileSync(bundle); }
		catch (err) {
			ws.send(JSON.stringify({ action: 'getFile_Response', error: String(err) }));
			return;
		}
		ws.send(JSON.stringify({
			action: 'getFile_Response',
			content: buf.toString('base64'),
			size: buf.length,
			version,
		}));
		console.log(`served ${buf.length} bytes`);
		if (!keep) {
			if (bail) clearTimeout(bail);
			setTimeout(() => process.exit(0), 5_000);
		}
	});
});
