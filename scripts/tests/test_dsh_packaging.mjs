import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const repository = fileURLToPath(new URL('../../', import.meta.url));

async function npm(args, cwd) {
  assert.ok(process.env.npm_execpath, 'run this regression through npm run test:dsh');
  const result = await execFileAsync(process.execPath, [process.env.npm_execpath, ...args], {
    cwd, env: process.env, encoding: 'utf8', timeout: 60_000, maxBuffer: 16 * 1024 * 1024,
  });
  return result.stdout;
}

test('packed DSH bundle installs outside the checkout and starts its actual MCP', { timeout: 120_000 }, async (t) => {
  const temporary = realpathSync(mkdtempSync(path.join(tmpdir(), 'easyeda-dsh-packaging-')));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const packed = JSON.parse(await npm(['pack', '--ignore-scripts', '--json', '--pack-destination', temporary], repository));
  assert.equal(packed.length, 1);
  const files = new Set(packed[0].files.map((file) => file.path));
  for (const required of ['package.json', 'index.js', 'cordis.patch.yml', 'mcp/package.json',
    'mcp/src/server.mjs', 'mcp/src/core.mjs', '.agents/skills/easyeda-agent/SKILL.md']) {
    assert.ok(files.has(required), `packed bundle is missing ${required}`);
  }
  for (const file of files) {
    assert.doesNotMatch(file, /^(?:artifacts|bin|dist|extension|internal|cmd|docs|scripts|node_modules)\//,
      `unrelated or private file entered the bundle: ${file}`);
    assert.doesNotMatch(file, /^\.agents\/(?:memory|worktrees|skills\/easyeda-repo-[^/]+)\//);
    assert.doesNotMatch(file, /^mcp\/(?:node_modules|test)\//);
  }

  const profile = path.join(temporary, '用户 DSH #50%', 'web');
  mkdirSync(profile, { recursive: true });
  writeFileSync(path.join(profile, 'package.json'), JSON.stringify({ name: 'dsh-packaging-fixture', private: true }));
  // Install the tarball, never a file: directory dependency or a copied SDK.
  // A fresh npm ci cache contains locked tarballs, not the registry metadata
  // needed to resolve a consumer's dependencies. Allow the normal registry
  // lookup rather than silently depending on a developer's warm metadata cache.
  await npm(['install', '--prefer-offline', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false',
    path.join(temporary, packed[0].filename)], profile);
  const profileRequire = createRequire(path.join(profile, 'package.json'));
  const bundleManifest = profileRequire.resolve('easyeda-agent-dsh/package.json');
  const bundleRequire = createRequire(bundleManifest);
  const manifest = JSON.parse(readFileSync(bundleManifest, 'utf8'));
  const nestedManifest = JSON.parse(readFileSync(path.join(path.dirname(bundleManifest), 'mcp/package.json'), 'utf8'));
  assert.equal(manifest.dependencies['@modelcontextprotocol/sdk'], nestedManifest.dependencies['@modelcontextprotocol/sdk']);
  const entry = bundleRequire('easyeda-agent-dsh');
  assert.equal(entry.name, 'easyeda-agent-dsh');
  assert.equal(typeof entry.apply, 'function');
  const untouchedContext = new Proxy({}, {
    get() { assert.fail('bundle compatibility entry must not read its context'); },
    set() { assert.fail('bundle compatibility entry must not mutate its context'); },
    has() { assert.fail('bundle compatibility entry must not inspect its context'); },
  });
  assert.equal(entry.apply(untouchedContext), undefined);
  const sdk = realpathSync(bundleRequire.resolve('@modelcontextprotocol/sdk/server/index.js'));
  assert.ok(sdk.startsWith(realpathSync(profile) + path.sep), 'SDK must resolve from the installed profile');

  const patchPath = path.join(path.dirname(bundleManifest), manifest.dsh.bundle.patch);
  const patch = readFileSync(patchPath, 'utf8');
  const expressions = [...patch.matchAll(/^\s+- !!js ("[^\n]+")\s*$/gm)].map((match) => JSON.parse(match[1]));
  assert.equal(expressions.length, 2, 'expected shipped MCP and Skill path expressions');
  const evaluate = new Function('ctx', 'expression', 'with (ctx) { return eval(expression); }');
  const context = { baseUrl: pathToFileURL(profile + path.sep).href };
  const server = evaluate(context, expressions[0]);
  const skill = evaluate(context, expressions[1]);
  assert.equal(server, path.join(path.dirname(bundleManifest), 'mcp/src/server.mjs'));
  assert.equal(skill, path.join(path.dirname(bundleManifest), '.agents/skills/easyeda-agent'));
  assert.match(readFileSync(path.join(skill, 'SKILL.md'), 'utf8'), /^name: easyeda-agent$/m);

  // The server executes EASYEDA_BIN with ['actions']; node actions supplies a
  // small offline catalog while preserving its real subprocess and MCP paths.
  const catalog = ['artifact', 'board', 'document', 'pcb', 'project', 'schematic', 'system'].map((domain) => ({
    name: domain === 'pcb' ? 'pcb.board.info' : `${domain}.info`, domain,
    mutates: false, description: 'Offline packaging fixture', inputs: [],
  }));
  writeFileSync(path.join(profile, 'actions'), `console.log(${JSON.stringify(JSON.stringify(catalog))});\n`);
  // Resolve both client and server SDKs from the installed profile, never the
  // checkout's node_modules. The SDK handles the real stdio MCP handshake.
  const { Client } = bundleRequire('@modelcontextprotocol/sdk/client/index.js');
  const { StdioClientTransport } = bundleRequire('@modelcontextprotocol/sdk/client/stdio.js');
  const env = { ...process.env, EASYEDA_BIN: process.execPath };
  delete env.NODE_PATH;
  const transport = new StdioClientTransport({ command: process.execPath, args: [server], cwd: profile, env });
  const client = new Client({ name: 'dsh-packaging-test', version: '1.0.0' });
  try {
    await client.connect(transport);
    assert.equal(client.getServerVersion().name, 'easyeda-agent-mcp');
    const listed = await client.listTools();
    assert.equal(listed.tools.length, 12);
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_pcb'));
    assert.ok(!listed.tools.some((tool) => tool.name === 'easyeda_debug'));
    const discovery = await client.callTool({
      name: 'easyeda_actions', arguments: { domain: 'pcb', search: 'board.info', mutates: false },
    });
    assert.equal(discovery.isError, false);
    assert.equal(discovery.structuredContent.count, 1);
    assert.equal(discovery.structuredContent.actions[0].name, 'pcb.board.info');
  } finally {
    await client.close();
  }
});
