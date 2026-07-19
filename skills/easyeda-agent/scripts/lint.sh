#!/bin/bash
# Data-only schematic linter with a diff-aware baseline.
#
#   tools/schematic-lint/lint.sh [projectName] [--all] [--save] [--init-git] \
#                                [host] [portStart] [portEnd]
#
# Pulls the full layout from a connected EasyEDA window in ONE call, then:
#   default     if a baseline exists → DIFF (only NEW/FIXED; fold pre-existing
#               "没动过" problems); otherwise a full lint.
#   --all       force a full global report (ignore the baseline).
#   --save      run a full lint, then record the current layout as the new
#               baseline (commits it too if the store is a git repo).
#   --init-git  make the baseline store a git repo so every --save is versioned,
#               then exit.
#
# Baseline store: ${EASYEDA_LINT_DIR:-~/.easyeda-agent/lint}/<project>/ — sits
# next to the daemon's audit log (~/.easyeda-agent/audit). The schematic lives
# in EasyEDA's webview, not on disk, so we version the layout SNAPSHOT instead.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"   # scripts → easyeda-agent → skills → repo root
BIN="$ROOT/bin/easyeda"

# ---- arg parse: flags anywhere, positionals in order ----
ALL=0; SAVE=0; INITGIT=0
POS=()
for a in "$@"; do
  case "$a" in
    --all) ALL=1 ;;
    --save) SAVE=1 ;;
    --init-git) INITGIT=1 ;;
    --*) echo "unknown flag: $a" >&2; exit 2 ;;
    *) POS+=("$a") ;;
  esac
done
PROJ="${POS[0]:-ceshi}"
HOST="${POS[1]:-127.0.0.1}"
PS="${POS[2]:-60832}"; PE="${POS[3]:-60841}"

STORE="${EASYEDA_LINT_DIR:-$HOME/.easyeda-agent/lint}/$PROJ"
SNAP="$STORE/snapshot.json"

[ -x "$BIN" ] || { echo "build first: make build" >&2; exit 1; }

# --init-git sets up the store and exits (no live window needed).
if [ "$INITGIT" = 1 ]; then
  mkdir -p "$STORE"
  ( cd "$STORE" && (git rev-parse --git-dir >/dev/null 2>&1 || git init -q) )
  echo "git baseline store ready: $STORE"
  exit 0
fi

# 1. find the daemon port (first that reports service easyeda-agent)
PORT=""
for p in $(seq "$PS" "$PE"); do
  if curl -fsS --max-time 1 "http://$HOST:$p/health" 2>/dev/null | grep -q 'easyeda-agent'; then
    PORT="$p"; break
  fi
done
[ -n "$PORT" ] || { echo "no easyeda-agent daemon on $HOST:$PS-$PE (run: $BIN daemon)" >&2; exit 1; }

# 2. resolve the live windowId for the project
WIN="$("$BIN" health 2>/dev/null | python3 -c "
import json,sys
wins=json.load(sys.stdin).get('found',{}).get('raw',{}).get('windows',[])
for w in wins:
    if w.get('context',{}).get('projectName')=='$PROJ': print(w['windowId']); break
")"
[ -n "$WIN" ] || { echo "no connected window for project '$PROJ'" >&2; exit 1; }

# 3. pull the full layout via debug.exec_js into a temp file
CUR="$(mktemp)"
trap 'rm -f "$CUR"' EXIT
PROBE="$(cat "$DIR/probe.js")"
python3 - "$HOST" "$PORT" "$WIN" "$PROBE" "$CUR" <<'PY'
import json, sys, urllib.request
host, port, win, probe, outpath = sys.argv[1:6]
body = json.dumps({"action": "debug.exec_js", "windowId": win, "payload": {"code": probe}}).encode()
req = urllib.request.Request(f"http://{host}:{port}/action", data=body, headers={"Content-Type": "application/json"})
resp = json.load(urllib.request.urlopen(req, timeout=60))
if not resp.get("ok"):
    print("probe failed:", resp.get("error"), file=sys.stderr); sys.exit(1)
with open(outpath, "w") as f:
    json.dump(resp["result"]["value"], f)
PY

save_baseline() {
  mkdir -p "$STORE/history"
  cp "$CUR" "$SNAP"
  TS="$(date -u +%Y%m%dT%H%M%SZ)"
  cp "$CUR" "$STORE/history/$TS.json"
  if ( cd "$STORE" && git rev-parse --git-dir >/dev/null 2>&1 ); then
    ( cd "$STORE" && git add -A && git commit -q -m "snapshot $PROJ $TS" >/dev/null 2>&1 || true )
    echo "✅ 基线已保存并提交: $SNAP"
  else
    echo "✅ 基线已保存: $SNAP  (history/$TS.json)"
  fi
}

# 4. dispatch
if [ "$SAVE" = 1 ]; then
  python3 "$DIR/lint.py" "$CUR"
  echo ""
  save_baseline
elif [ "$ALL" = 1 ]; then
  python3 "$DIR/lint.py" "$CUR"
elif [ -f "$SNAP" ]; then
  python3 "$DIR/diff.py" "$SNAP" "$CUR" || true   # diff exits 1 on new problems
else
  python3 "$DIR/lint.py" "$CUR"
  echo ""
  echo "（无基线：跑 'lint.sh $PROJ --save' 建立基线，下次即可只看变更；--all 可强制全局）"
fi
