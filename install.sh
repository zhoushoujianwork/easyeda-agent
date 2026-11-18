#!/usr/bin/env bash
# easyeda-agent installer
# Usage: curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash
set -euo pipefail

REPO="zhoushoujianwork/easyeda-agent"
SKILL_NAME="easyeda-agent"
# EASYEDA_INSTALL_SKILLS: ""|auto (detect), "none" (skip), or CSV of codex,claude,agents
INSTALL_SKILLS="${EASYEDA_INSTALL_SKILLS:-}"
# EASYEDA_SKILL_PRESERVE=1 keeps existing files instead of clean-replacing
SKILL_PRESERVE="${EASYEDA_SKILL_PRESERVE:-0}"
# EASYEDA_INSTALL_DIR selects an absolute binary destination.
# CODEX_HOME / CLAUDE_CONFIG_DIR select client config roots.
# EASYEDA_VERSION=v0.18.2 pins the release and skips the GitHub API lookup entirely
VERSION="${EASYEDA_VERSION:-}"

# ── helpers ──────────────────────────────────────────────────────────────────
info()  { printf '\033[34m[easyeda-agent]\033[0m %s\n' "$*"; }
ok()    { printf '\033[32m✔\033[0m %s\n' "$*"; }
warn()  { printf '\033[33m⚠\033[0m %s\n' "$*"; }
fatal() { printf '\033[31m✘\033[0m %s\n' "$*" >&2; exit 1; }

# ── resolve latest release ───────────────────────────────────────────────────
# api.github.com allows only 60 requests/hour per IP unauthenticated, so a shared
# office / NAT / CI address can hand back 403 instead of the release JSON. Send a
# token when we can find one (GITHUB_TOKEN / GH_TOKEN / the gh CLI), and let
# EASYEDA_VERSION bypass the API completely.
github_token() {
  _tok="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
  if [ -z "$_tok" ] && command -v gh >/dev/null 2>&1; then
    _tok=$(gh auth token 2>/dev/null || true)
  fi
  printf '%s' "$_tok"
}

rate_limit_fatal() {
  printf '\033[31m✘\033[0m %s\n' \
    "GitHub API rate limit (HTTP ${1}) — could not resolve the latest release." >&2
  printf '    Unauthenticated api.github.com allows 60 requests/hour per IP;\n' >&2
  printf '    a shared office / NAT / CI address burns through that fast.\n' >&2
  if [ -n "$2" ]; then
    printf '    A token was sent but still rejected — it may be expired or invalid.\n' >&2
  fi
  printf '    Fix it either way:\n' >&2
  printf '      1) authenticate (5000 requests/hour):\n' >&2
  printf '           export GITHUB_TOKEN=<token>    # GH_TOKEN works too\n' >&2
  printf '           gh auth login                 # gh CLI is picked up automatically\n' >&2
  printf '      2) skip the API by pinning a release tag:\n' >&2
  printf '           EASYEDA_VERSION=<tag> bash ./install.sh\n' >&2
  printf '           tags: https://github.com/%s/releases\n' "$REPO" >&2
  exit 1
}

resolve_latest_web() {
  _meta=$(curl -sSL --connect-timeout 15 --max-time 60 -o /dev/null \
    -w '%{url_effective}\n%{http_code}' "https://github.com/${REPO}/releases/latest") || return 1
  _code=$(printf '%s\n' "$_meta" | tail -n 1)
  _url=$(printf '%s\n' "$_meta" | sed '$d')
  [ "$_code" = 200 ] || return 1
  _tag=${_url##*/}
  case "$_tag" in
    v[0-9]*.[0-9]*.[0-9]*) VERSION="$_tag"; return 0 ;;
    *) return 1 ;;
  esac
}

if [ -n "$VERSION" ]; then
  # Tags are v-prefixed; accept "0.18.2" as well as "v0.18.2".
  case "$VERSION" in
    [0-9]*) VERSION="v${VERSION}" ;;
  esac
  info "Pinned release: ${VERSION} (EASYEDA_VERSION)"
else
  info "Fetching latest release..."
  API_TOKEN=$(github_token)
  API_URL="https://api.github.com/repos/${REPO}/releases/latest"
  # No -f here: we want the body *and* the status code so the failure can explain itself.
  if [ -n "$API_TOKEN" ]; then
    API_RESP=$(curl -sSL -w '\n%{http_code}' \
      -H 'Accept: application/vnd.github+json' \
      -H "Authorization: Bearer ${API_TOKEN}" "$API_URL") || API_RESP=""
  else
    API_RESP=$(curl -sSL -w '\n%{http_code}' \
      -H 'Accept: application/vnd.github+json' "$API_URL") || API_RESP=""
  fi
  API_CODE=$(printf '%s\n' "$API_RESP" | tail -n 1)
  API_BODY=$(printf '%s\n' "$API_RESP" | sed '$d')

  case "$API_CODE" in
    200) ;;
    401) fatal "GitHub API rejected the token (HTTP 401). Unset GITHUB_TOKEN/GH_TOKEN or run 'gh auth login', or pass EASYEDA_VERSION=<tag>." ;;
    403|429)
      if resolve_latest_web; then
        warn "GitHub API returned HTTP ${API_CODE}; resolved latest from the public release redirect"
      else
        rate_limit_fatal "$API_CODE" "$API_TOKEN"
      fi
      ;;
    404) fatal "No 'latest' release for ${REPO} (HTTP 404). Pick a tag from https://github.com/${REPO}/releases and pass EASYEDA_VERSION=<tag>." ;;
    '' | 000) fatal "Could not reach api.github.com (network or proxy issue). Retry, or pass EASYEDA_VERSION=<tag> to skip the API." ;;
    *) fatal "GitHub API returned HTTP ${API_CODE} while resolving the latest release. Pass EASYEDA_VERSION=<tag> to skip the API." ;;
  esac

  if [ -z "${VERSION:-}" ]; then
    VERSION=$(printf '%s\n' "$API_BODY" \
      | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    [ -n "$VERSION" ] || fatal "Could not parse a tag_name out of the GitHub API response. Pass EASYEDA_VERSION=<tag> to skip the API."
  fi
  info "Latest: ${VERSION}"
fi

BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

# ── detect OS + arch ─────────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)       ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) fatal "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
  darwin|linux) ;;
  *) fatal "Unsupported OS: $OS (native Windows: run install.ps1 in PowerShell — irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex)" ;;
esac

BINARY_NAME="easyeda_${OS}_${ARCH}"

# ── choose install dir (no sudo required) ────────────────────────────────────
if [ -n "${EASYEDA_INSTALL_DIR:-}" ]; then
  INSTALL_DIR="$EASYEDA_INSTALL_DIR"
  case "$INSTALL_DIR" in /*) ;; *) fatal "EASYEDA_INSTALL_DIR must be an absolute path" ;; esac
elif [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
fi
mkdir -p "$INSTALL_DIR"
TMP=$(mktemp -d)
BIN_TMP=""
trap 'rm -rf "$TMP"; [ -z "$BIN_TMP" ] || rm -f "$BIN_TMP"' EXIT

# Download and validate all selected assets BEFORE changing an installed file.
# Only HTTP 404 means an old release without checksums; network/server failures
# must not silently disable integrity checking.
SHA_CMD=""
if command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  SHA_CMD="shasum -a 256"
fi
SUM_CODE=$(curl -sSL --connect-timeout 15 --max-time 120 -w '%{http_code}' \
  "${BASE_URL}/checksums.txt" -o "$TMP/checksums.txt") || fatal "Could not download checksums.txt"
case "$SUM_CODE" in
  200) [ -n "$SHA_CMD" ] || fatal "Install sha256sum or shasum to verify this release" ;;
  404) warn "Old release has no checksums.txt; binary version and Skill metadata will be checked" ;;
  *) fatal "checksums.txt returned HTTP $SUM_CODE; nothing installed" ;;
esac
verify_asset() {
  _name="$1"; _path="$2"
  [ "$SUM_CODE" = 200 ] || return 0
  _want=$(awk -v n="$_name" '{ f=$2; sub(/^\*/,"",f); if (f==n) print $1 }' "$TMP/checksums.txt")
  [ "${#_want}" = 64 ] || fatal "Missing/duplicate/invalid checksum for $_name"
  case "$_want" in *[!0-9a-fA-F]*) fatal "Invalid checksum for $_name" ;; esac
  _got=$($SHA_CMD "$_path" | awk '{print $1}')
  [ "$_want" = "$_got" ] || fatal "checksum mismatch for $_name; nothing installed"
  ok "sha256 verified: $_name"
}

download_release_asset() {
  _name="$1"; _dest="$2"; _primary="${BASE_URL}/${_name}"
  _attempt=1
  while [ "$_attempt" -le 3 ]; do
    if curl -fsSL --connect-timeout 15 --max-time 300 "$_primary" -o "$_dest"; then
      return 0
    fi
    warn "GitHub download attempt ${_attempt}/3 failed for ${_name}"
    rm -f "$_dest"
    _attempt=$((_attempt + 1))
  done

  # A third-party transport is trusted only for availability. The expected
  # digest must already have come directly from GitHub.
  [ "$SUM_CODE" = 200 ] || fatal "GitHub download failed and no trusted checksum is available; mirror fallback refused"
  _proxy="${EASYEDA_GITHUB_PROXY-https://gh-proxy.com/}"
  case "$_proxy" in
    ''|off|OFF|Off) fatal "download failed: ${_primary} (mirror fallback disabled)" ;;
  esac
  case "$_proxy" in
    *'{url}'*) _mirror=${_proxy//\{url\}/$_primary} ;;
    *) _mirror="${_proxy%/}/${_primary}" ;;
  esac
  warn "GitHub failed; trying checksum-verified mirror for ${_name}"
  curl -fsSL --connect-timeout 15 --max-time 300 "$_mirror" -o "$_dest" \
    || fatal "GitHub and mirror downloads both failed for ${_name}"
}

info "Downloading ${BINARY_NAME}..."
download_release_asset "$BINARY_NAME" "$TMP/binary"
verify_asset "$BINARY_NAME" "$TMP/binary"
chmod 0755 "$TMP/binary"
ACTUAL_VERSION=$("$TMP/binary" --version) || fatal "Downloaded binary cannot run on this host; nothing installed"
[ "$ACTUAL_VERSION" = "easyeda-agent $VERSION" ] \
  || fatal "Downloaded binary version differs: $ACTUAL_VERSION; expected $VERSION"

# ── install skills (Codex + Claude Code + shared Agent root) ──────────────────
# Repository source: .agents/skills/easyeda-agent. The release packager keeps
# the archive root as easyeda-agent/, independent of the checkout layout.
# Resolve which clients to install for.
# codex → ~/.codex/skills/easyeda-agent, claude → ~/.claude/skills/easyeda-agent,
# agents → ~/.agents/skills/easyeda-agent (Codex Desktop shared skill root)
detect_targets() {
  # Explicit "none" → skip entirely.
  case "$INSTALL_SKILLS" in
    none|NONE|None) return 0 ;;
  esac

  if [ -n "$INSTALL_SKILLS" ] && [ "$INSTALL_SKILLS" != "auto" ]; then
    # Explicit CSV list (e.g. "codex,claude,agents").
    printf '%s\n' "$INSTALL_SKILLS" | tr ',' '\n' | while IFS= read -r t; do
      t=$(printf '%s' "$t" | tr -d '[:space:]')
      [ -n "$t" ] && printf '%s\n' "$t"
    done
    return 0
  fi

  # auto-detect
  found=0
  if [ -d "${CODEX_HOME:-${HOME}/.codex}" ] || command -v codex >/dev/null 2>&1; then
    printf 'codex\n'; found=1
  fi
  if [ -d "${CLAUDE_CONFIG_DIR:-${HOME}/.claude}" ] || command -v claude >/dev/null 2>&1; then
    printf 'claude\n'; found=1
  fi
  if [ -d "${HOME}/.agents" ]; then
    printf 'agents\n'; found=1
  fi
  # Neither detected → create both by default so the skill is ready when a
  # client shows up. EASYEDA_INSTALL_SKILLS=none opts out.
  if [ "$found" = 0 ]; then
    warn "No Codex/Claude Code client detected; creating both skill dirs by default." >&2
    printf 'codex\n'
    printf 'claude\n'
  fi
}

# Map a client name to its skills base dir.
client_base_dir() {
  case "$1" in
    codex)  printf '%s/skills\n' "${CODEX_HOME:-${HOME}/.codex}" ;;
    claude) printf '%s/skills\n' "${CLAUDE_CONFIG_DIR:-${HOME}/.claude}" ;;
    agents) printf '%s/skills\n' "${HOME}/.agents" ;;
    *)      return 1 ;;
  esac
}

# Stage on the destination filesystem and swap only a complete directory. On a
# failed switch, restore the previous directory. Preserve mode reports mixed
# content and keeps the old marker instead of claiming a complete upgrade.
install_skill_to() {
  _client="$1"; _src="$2"
  _base=$(client_base_dir "$_client") || fatal "Unknown skill target: $_client"
  case "$_base" in /*) ;; *) fatal "Client config directory must be absolute: $_base" ;; esac
  mkdir -p "$_base"
  _dest="${_base}/${SKILL_NAME}"
  if [ -L "$_dest" ]; then
    _dest=$(cd "$_dest" && pwd -P) || fatal "Skill symlink target is unavailable"
    _base=$(dirname "$_dest")
  fi
  _stage=$(mktemp -d "${_base}/.easyeda-stage.XXXXXX")
  if ! cp -R "$_src"/. "$_stage"/; then
    rm -rf "$_stage"; fatal "Could not stage $_client Skill; existing files kept"
  fi
  _preserved=0
  if [ "$SKILL_PRESERVE" = 1 ] && [ -d "$_dest" ]; then
    if ! cp -R "$_dest"/. "$_stage"/; then
      rm -rf "$_stage"; fatal "Could not preserve $_client Skill; existing files kept"
    fi
    _preserved=1
  else
    printf '%s\n' "${VERSION#v}" > "$_stage/.version"
  fi
  _backup="$_stage.previous"
  if [ -e "$_dest" ]; then
    if ! mv "$_dest" "$_backup"; then
      rm -rf "$_stage"; fatal "Could not back up $_client Skill; existing files kept"
    fi
  fi
  if ! mv "$_stage" "$_dest"; then
    if [ -e "$_backup" ]; then mv "$_backup" "$_dest" || fatal "Restore $_backup to $_dest"; fi
    rm -rf "$_stage"; fatal "Could not install $_client Skill"
  fi
  rm -rf "$_backup"
  if [ "$_preserved" = 1 ]; then
    warn "$_client Skill preserved (mixed local/release content; previous version marker kept) → $_dest"
  else
    ok "$_client Skill installed → $_dest"
  fi
}

TARGETS=$(detect_targets)
# Reject bad client names/paths before changing the CLI or any Skill directory.
for client in $TARGETS; do
  CLIENT_BASE=$(client_base_dir "$client") || fatal "Unknown skill target: $client"
  case "$CLIENT_BASE" in /*) ;; *) fatal "Client config directory must be absolute: $CLIENT_BASE" ;; esac
  if [ -L "$CLIENT_BASE/$SKILL_NAME" ]; then
    [ -d "$CLIENT_BASE/$SKILL_NAME" ] || fatal "Skill symlink target is unavailable: $CLIENT_BASE/$SKILL_NAME"
  fi
done
if [ -z "$TARGETS" ]; then
  info "Skill install skipped (EASYEDA_INSTALL_SKILLS=none)"
else
  info "Downloading skills.tar.gz..."
  download_release_asset "skills.tar.gz" "$TMP/skills.tar.gz"
  verify_asset "skills.tar.gz" "$TMP/skills.tar.gz"
  tar -xzf "$TMP/skills.tar.gz" -C "$TMP" || fatal "Invalid Skill archive; nothing installed"
  SRC_SKILL="$TMP/$SKILL_NAME"
  [ -s "$SRC_SKILL/SKILL.md" ] || fatal "Skill archive has no SKILL.md; nothing installed"
  SKILL_VERSION=$(sed -n 's/^  version: *"\([^"]*\)" *$/\1/p' "$SRC_SKILL/SKILL.md")
  [ "$SKILL_VERSION" = "${VERSION#v}" ] || fatal "Skill metadata version differs from $VERSION; nothing installed"
fi
BIN_TMP=$(mktemp "${INSTALL_DIR}/.easyeda-download.XXXXXX")
cp "$TMP/binary" "$BIN_TMP"
chmod 0755 "$BIN_TMP"
mv "$BIN_TMP" "${INSTALL_DIR}/easyeda"
BIN_TMP=""
ok "CLI installed → ${INSTALL_DIR}/easyeda"
for client in $TARGETS; do
  install_skill_to "$client" "$SRC_SKILL"
done

# ── PATH check ────────────────────────────────────────────────────────────────
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  warn "${INSTALL_DIR} is not in PATH"
  printf '    Add to ~/.zshrc or ~/.bashrc:\n'
  printf '    export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
fi

# ── next steps ────────────────────────────────────────────────────────────────
printf '\n'
ok "easyeda-agent ${VERSION} installed"
printf '\n'
printf 'Next steps:\n'
printf '  1. Start the daemon:\n'
printf '       easyeda daemon start\n\n'
printf '  2. Install the EasyEDA connector extension (either channel):\n'
printf '     a) Sideload this release (same major.minor compatibility line):\n'
printf '          Download: %s/easyeda-agent-connector.eext\n' "$BASE_URL"
printf '          In EasyEDA Pro: 扩展管理 → 导入扩展 → select the .eext file\n'
printf '     b) 立创官方插件市场 (one-click, auto-updates in place; may lag the CLI):\n'
printf '          https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector\n'
printf '          (renamed from easyeda-agent-connector; same uuid — existing installs\n'
printf '           keep auto-updating in place, no action needed)\n\n'
printf '  3. In EasyEDA Pro: 设置 → 允许外部交互 (Allow external interaction)\n\n'
printf '  4. Use the skill in your AI client:\n'
printf '       /easyeda-agent       (schematic + PCB workflow)\n'
printf '       Installed for detected clients: Codex (~/.codex/skills), Codex Desktop shared (~/.agents/skills), and/or Claude Code (~/.claude/skills)\n\n'
printf 'Upgrading later? No need to re-run this script:\n'
printf '       easyeda update           # CLI binary + skill dirs → latest\n'
printf '       easyeda update --check   # report only (cli / skill / connector)\n'
printf '     (connector patch drift is compatible; re-import only when `update` reports a major/minor mismatch)\n\n'
printf 'Full docs: https://github.com/%s\n' "$REPO"
