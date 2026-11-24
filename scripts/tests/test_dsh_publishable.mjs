import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

// The root package is both the DSH bundle and the npm distribution of it
// (`dsh plugin add easyeda-agent-dsh`, or a tarball attached to a Release).
// These assertions are the publishing contract; the packaging test proves the
// packed tarball still installs and speaks MCP.

const root = fileURLToPath(new URL('../../', import.meta.url));
const manifest = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8'));

test('the package can be published to npm', () => {
  assert.equal(manifest.name, 'easyeda-agent-dsh');
  assert.match(manifest.version, /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/);
  // `private: true` makes `npm publish` refuse the package and hides it from
  // registry search, so it must stay off now that the bundle has a public name.
  assert.notEqual(manifest.private, true, 'npm publish refuses private packages');
  assert.equal(manifest.license, 'MIT');
  assert.ok(manifest.description.length > 0);
  assert.match(manifest.main, /^\.\/index\.js$/);
});

test('registry discovery fields name the project and its intended consumers', () => {
  for (const keyword of ['dsh', 'dsh-plugin', 'deepseek-harness']) {
    assert.ok(manifest.keywords.includes(keyword), `missing keyword: ${keyword}`);
  }
  assert.equal(manifest.repository.type, 'git');
  assert.equal(manifest.repository.url,
    'git+https://github.com/zhoushoujianwork/easyeda-agent.git');
  assert.equal(manifest.homepage,
    'https://github.com/zhoushoujianwork/easyeda-agent#readme');
  assert.equal(manifest.bugs.url,
    'https://github.com/zhoushoujianwork/easyeda-agent/issues');
});

test('publishing stays a single self-contained bundle', () => {
  assert.equal(manifest.dsh.bundle.patch, './cordis.patch.yml');
  assert.doesNotThrow(() => readFileSync(path.join(root, manifest.dsh.bundle.patch)));
  // A registry install may not resolve workspace/file/link/git dependency
  // specs, and the MCP SDK is the only runtime dependency the bundle needs.
  for (const [name, spec] of Object.entries(manifest.dependencies)) {
    assert.doesNotMatch(spec, /^(?:workspace|file|link|git|github|https?):/,
      `${name} uses a non-registry dependency spec: ${spec}`);
  }
  // The whitelist carries the bundle and nothing from the Go/connector tree;
  // the packaging test additionally checks the packed file list.
  for (const required of ['index.js', 'cordis.patch.yml', '.agents/skills/easyeda-agent/']) {
    assert.ok(manifest.files.includes(required), `files is missing ${required}`);
  }
  assert.match(manifest.engines.node, /^>=\d+\.\d+\.\d+$/);
});
