# Dev Environment & Debug Playbook

How to stand up a working loop against **EasyEDA Pro** and iterate on the
connector fast. Covers both the **desktop client** and the **web editor**
(`pro.lceda.cn/editor`), plus a **hot-reload** trick that skips the
uninstall→re-import dance entirely.

Companion docs: [connector-contract.md](connector-contract.md) (the wire
protocol & runtime constraints) and [protocol.md](protocol.md) (message
envelopes). This file is the operational "how do I run it" guide.

## The AI-agent automation testing loop (what this whole setup is for)

The pieces below combine into one repeatable loop an AI agent (or you) runs to
develop + regression-test the connector against a LIVE editor, hands-free:

1. **Bootstrap the live environment** — with a browser-control tool
   (chrome-devtools MCP) the agent opens the web editor, opens a project by
   `#id=<projectUuid>`, and waits for the connector to attach — no manual clicks.
   The agent-facing SOP (exact steps, retries, pitfalls) is
   [`skills/easyeda-agent/references/environment-setup.md`](../skills/easyeda-agent/references/environment-setup.md);
   §1–4 below are the manual equivalent.
2. **Iterate the connector fast** — edit `extension/src`, `make eext`, then
   **hot-reload** into the running editor via IndexedDB (§5) — no uninstall /
   re-import / restart. Go/daemon changes hot-reload via `air` (`make dev`).
3. **Verify against real geometry, not tests alone** — drive the typed actions
   against the live board; judge by data (`sch/pcb list`, `drc`, `check`,
   `layout-lint`), never by a screenshot alone (stale/blank-frame traps, Core
   Rule 7 in the skill). "Builds + unit tests pass" ≠ "works live" — several bugs
   this project shipped only surfaced on a real board.
4. **Probe → file → fix → merge → re-verify** — run a real design task as the
   probe (the fixed regression case is [`esp32MiniRequire.md`](../esp32MiniRequire.md);
   the copy-a-golden-board harness is `training/copy-check.py`), file each gap as
   a GitHub issue labeled `ready-for-agent`, let ClawFlow implement + open a PR,
   then **you merge and live-verify** (the operator can't do runtime acceptance —
   see the advisory-loop section at the bottom). The rolling gap/roadmap ledger is
   [`optimization-loop.md`](optimization-loop.md).

## TL;DR loop

```
make dev                       # daemon with air hot-reload (rebuilds on .go change)
# open EasyEDA (desktop or web), open a project, import the .eext once,
# enable "Allow external interaction"
easyeda daemon health          # windows[] should list your connected editor
easyeda project info --project <name>   # first typed action round-trip
```

## Prerequisites

- Go toolchain + `make` (see repo root `Makefile`).
- `air` for daemon hot-reload: `go install github.com/air-verse/air@latest`.
- Node ≥ 20 for building the connector `.eext` (`make eext`); run
  `npm install` in `extension/` once (brings `ws`, used by the hot-reload server).
- EasyEDA Pro: desktop client **or** the web editor `https://pro.lceda.cn/editor`.
- For the hands-free agent loop (optional): a **chrome-devtools MCP** server (drives
  the web editor + runs the hot-reload inject) and the **`gh` CLI** (the
  probe → issue → PR → merge cycle). Without them the manual §1–5 flow still works.

## 1. Start the daemon

```bash
make dev                 # air live-reload; leave running in a terminal
# or one-shot:
./bin/easyeda daemon start &
./bin/easyeda daemon health   # status=found; windows[] empty until a connector attaches
```

The daemon listens on `127.0.0.1:60832-60841` (`0xEDA0`-`0xEDA9`) and speaks the handshake in
[connector-contract.md](connector-contract.md).

## 2. Open a project (this step is manual)

The connector activates via `onStartupFinished`, which only fires once a
project is open in the editor. **The CLI cannot open a not-yet-open project**
— that first open is always done in the editor UI:

- Desktop: File → Open, or pick a recent project.
- Web: double-click a project in the list, or navigate to
  `https://pro.lceda.cn/editor#id=<projectUuid>`.

Once open, document switching *is* scriptable:

```bash
easyeda doc ls --project <name>            # list pages/PCBs, ★ = active
easyeda doc switch <page|pcb|uuid> --project <name>
```

## 3. Import the connector + grant permission (first time per install)

1. **高级(A) → 扩展管理器(E)… → 导入** and pick
   `dist/easyeda-agent-connector.eext`.
2. A security prompt appears: external-interaction permission is **disabled
   by default**. Confirm it.
3. Enable the permission: in the connector's detail page, open the
   **配置 (Config)** tab and tick **允许外部交互 (Allow external interaction)**.
   Without it, every `eda.sys_WebSocket` call throws (see connector-contract.md).

After enabling, the connector's watchdog reconnects within a few seconds —
no manual Reconnect needed.

## 4. Confirm the round-trip

```bash
easyeda daemon health          # windows[] now lists connectorVersion / easyedaVersion / context
easyeda project info --project <name>          # structured data flows back
easyeda notify --project <name> --message "hi" --type success   # visible toast in the editor
```

Prefer `--project <name>` over `--window <id>`: it routes by project and
survives windowId churn across reconnects.

## 5. Hot-reload the connector (skip uninstall → re-import)

Re-importing a `.eext` is slow and error-prone: EasyEDA dedups by UUID, so you
must uninstall the old one first (a version bump alone silently fails), and an
already-open window keeps running the OLD code until reloaded. There's a much
faster path once you know where EasyEDA keeps installed extensions.

### Where extensions live: IndexedDB

EasyEDA Pro stores every installed extension in the browser's **IndexedDB**,
not on disk. This is true for both the web editor and the Electron desktop
client (the desktop app is a Chromium webview).

- **Database**: `User_<teamUuid>_v6` (the `teamUuid` is the one returned by
  `easyeda project info`).
- **Object stores**:
  - `extensionsIndex` — one record per extension, keyed by `uuid`. Fields:
    `config` (the parsed `extension.json`), `isEnable`,
    `isAllowExternalInteractions` (yes — the permission is just this boolean),
    `fileIndex`, `fileSize`.
  - `extensionsObjectStorage` — one record per file, key = `<uuid>|<path>`,
    with `source` being a `File` object. The connector's only executable file
    is `<uuid>|dist/index.js`.
  - `extensionsUserConfig`, `standaloneScript`.

### The trick

Overwrite the `<uuid>|dist/index.js` record with a freshly-built bundle, bump
`config.version` in `extensionsIndex`, then reload the page. EasyEDA re-reads
the extension from IndexedDB on load, so the new code runs — **no uninstall,
no re-import, no file dialogs**.

### Transport constraint

The editor is HTTPS, so `fetch('http://127.0.0.1/...')` is blocked as mixed
content. But `ws://127.0.0.1` is allowed (that's exactly how the connector
reaches the daemon). So move the new bundle over a **local WebSocket**, not
HTTP.

### Steps

Two committed scripts do this end to end — no external tooling needed:
`extension/scripts/hot-reload-server.mjs` (the WS file server) and
`extension/scripts/hot-reload-inject.js` (the browser-side IndexedDB writer).

```bash
# 0. One-time: install the ws devDependency the server uses.
(cd extension && npm install)

# 1. Rebuild the connector after editing its source (bumps the patch version).
make eext                                   # recompiles extension/dist/index.js

# 2. Serve the fresh bundle over a local WebSocket (one-shot; exits after serving).
node extension/scripts/hot-reload-server.mjs &     # ws://127.0.0.1:8790

# 3. In the EDITOR PAGE, run hot-reload-inject.js — paste into the browser console,
#    or (agent-driven) pass its body to a chrome-devtools MCP evaluate_script call.
#    Fill in TEAM (teamUuid, from `easyeda project info`), UUID + VERSION (from
#    extension/extension.json). It pulls the bundle over ws://, overwrites the
#    connector's <uuid>|dist/index.js record + bumps config.version, then reloads.

# 4. Verify the new code is live.
easyeda daemon health                       # connectorVersion shows the new version
```

The inject script writes the IndexedDB records described above; the server reads
`extension/dist/index.js` + the version from `extension.json`. Both take flags
(`--port`, `--bundle`; `--keep` to stay resident) — see the file headers.

`connectorVersion` in `daemon health` is compiled into `index.js`
(`CONNECTOR_VERSION`), so a changed value is proof the new bundle is running.

### Minimal WS protocol (if you build your own transport)

Server is request/response over a JSON WebSocket:

- Client → `{"action":"getUpdatedFileTree","fileList":[]}`
  Server → `{"action":"getUpdatedFileTree_Response","fileList":[{path,id,hash,status},...]}`
  (empty `fileList` makes the server mark everything as `add`.)
- Client → `{"action":"updateFile","file":{"path":"index.js","id":..,"hash":..}}`
  Server → `{"action":"updateFile_Response","success":true,"file":{path,hash,size,mtime,content}}`
  where `content` is base64.

The IndexedDB key uses the connector's install path (`dist/index.js`), while
the server's `dir` root maps to that dir's contents — so a server file `index.js`
lands at IndexedDB key `<uuid>|dist/index.js`. Mind that offset.

## Troubleshooting

- **`daemon health` shows `windows: []`** — the connector isn't attached.
  Check: project actually open? permission "允许外部交互" enabled? For a
  freshly-imported connector in an already-open window, reload the page to
  trigger `onStartupFinished`.
- **Permission toggle is hard to find** — it's not on the extension card;
  it's in the connector's **detail page → 配置 (Config)** tab.
- **Uninstall confirm dialog is flaky to automate** — ticking the "I
  understand" checkbox re-renders the confirm button. Prefer the hot-reload
  path (section 5), which never uninstalls.
- **`fetch` to the daemon/localhost fails in the editor** — expected;
  HTTPS→HTTP mixed content is blocked. Use `ws://127.0.0.1` (the connector and
  the hot-reload transport both rely on this being allowed).
- **Automating the web editor via chrome-devtools**: it uses a dedicated
  Chrome profile. If it errors with "browser already running", a stale Chrome
  is holding that profile — kill the process using that `--user-data-dir` and
  let the tooling relaunch. The extension install persists in the profile, so
  you don't need to re-import after the relaunch.
- **Network blocks a git host** (e.g. a corporate gateway blocking a mirror) —
  clone from a machine with open egress (a cloud VM) and copy the tree back.
- **Connector port-scans forever (`WebSocket ... closed before ... established`
  on every port 60832–60841), `daemon health` shows a daemon but `windows: 0`** —
  usually TWO daemons fighting over the port: `air` restarted the daemon after a
  `.go` edit but a previous instance lingered, so `/health` answers on the port
  while the other instance's `/eda` WebSocket never completes the handshake.
  Diagnose + fix:
  ```bash
  pgrep -fl "easyeda daemon"          # >1 line = orphans fighting
  lsof -iTCP:60832 -sTCP:LISTEN -n    # which PID owns the port
  pkill -f "easyeda daemon"; sleep 2  # kill all, then start ONE clean:
  nohup ./bin/easyeda daemon start > /tmp/easyeda-daemon.log 2>&1 &
  ```
  Then reload the editor page so the connector re-handshakes. (If you run the
  daemon under `make dev`/air, prefer letting air own it — but after a messy
  restart, a single clean `daemon start` is the reliable reset.)

## Notes

- The connector persists in the browser/profile's IndexedDB, so it survives
  restarts of the editor. Wiping site data removes it.
- The hot-reload approach touches EasyEDA-internal IndexedDB structures that
  are **not an official, stable API** — treat it as a dev-only convenience and
  re-verify the store names/keys if EasyEDA bumps its schema version
  (`_v6` today).

## Advisory loop: what the implement operator can and cannot do

ClawFlow's implement operator runs `claude -p` in a **headless worktree** — it
has the repo source but **no live EasyEDA editor, no connected connector, no
running daemon with an attached window**. That draws a hard line:

| Task type | Operator? | Examples |
|---|---|---|
| **Code / doc change** | ✅ yes | new `pcb check` rule, `sch rebind-*`, netlist cross-check |
| **Runtime acceptance** | ❌ no | end-to-end board bring-up, any `pcb drc passed:true` gate |

**Why runtime acceptance can't go through the operator**: its pass criteria
(`pcb drc passed`, `pcb check` 0 ERROR, BOM saved) are **runtime products** that
only exist by driving a live editor through the S0–S6 / P0–P10 flow. No source
edit produces them, and the headless worktree shows `windows: []` — every flow
command returns `NO_CONNECTOR`. A correct operator run on such an issue **fails
and says so** (it must not fabricate a "DRC passed" PR — that violates the
"never feed a pre-cooked answer" rule).

**The advisory loop, done right:**
- Code/doc issues → label `ready-for-agent` → operator evaluates, implements,
  opens a PR, auto-merges → **you re-verify** with the editor loop (build + tests
  for code correctness, then run the new command against a live board).
- Runtime-acceptance issues (e.g. the ESP32 end-to-end regressions) → **do NOT**
  label `ready-for-agent`. Mark them as interactive/manual and run them from a
  session that has chrome-devtools MCP + a live daemon + a connected editor.

Verified 2026-07-06: issues #44 (rebind actions) and #45 (netlist cross-check)
went code → operator → PR → auto-merge → re-verified in an editor session; issue
#42 (ESP32 end-to-end) was correctly `agent-failed` by the operator with a
precise "no attached editor" explanation.
