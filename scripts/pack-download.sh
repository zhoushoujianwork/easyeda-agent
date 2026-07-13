#!/usr/bin/env bash
# 把一次 release 的全部资产打成单个自包含总包，放进 downloads/ 供国内用户
# (无法访问 GitHub Release) 一次下载全套。人工分发用。
#
#   scripts/pack-download.sh v0.11.1
#
# 前提:dist/ 里已经是该版本的产物 (由 `make release` 或手动交叉编译生成)。
# 本脚本不构建、不改版本号、不打 tag —— 只搬运 + 压缩,幂等可重跑。
set -euo pipefail

VERSION="${1:?usage: pack-download.sh vX.Y.Z}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
OUT="$ROOT/downloads"
NAME="easyeda-agent-$VERSION"

ASSETS=(
  easyeda_darwin_amd64 easyeda_darwin_arm64
  easyeda_linux_amd64  easyeda_linux_arm64
  easyeda_windows_amd64.exe
  easyeda-agent-connector.eext skills.tar.gz install.sh
)

for f in "${ASSETS[@]}"; do
  [ -f "$DIST/$f" ] || { echo "缺少产物: dist/$f — 先跑 make release 或交叉编译" >&2; exit 1; }
done

mkdir -p "$OUT"
STAGE="$(mktemp -d)/$NAME"
mkdir -p "$STAGE"
for f in "${ASSETS[@]}"; do command cp "$DIST/$f" "$STAGE/"; done

( cd "$(dirname "$STAGE")" && zip -rq "$NAME.zip" "$NAME" )
command cp -f "$(dirname "$STAGE")/$NAME.zip" "$OUT/"
rm -rf "$(dirname "$STAGE")"

echo "✅ $OUT/$NAME.zip"
unzip -l "$OUT/$NAME.zip"
