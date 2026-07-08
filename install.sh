#!/usr/bin/env bash
# easyeda-agent installer
# Usage: curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh
set -euo pipefail

REPO="zhoushoujianwork/easyeda-agent"
SKILL_NAME="easyeda-agent"
# EASYEDA_INSTALL_SKILLS: ""|auto (detect), "none" (skip), or CSV of codex,claude
INSTALL_SKILLS="${EASYEDA_INSTALL_SKILLS:-}"
# EASYEDA_SKILL_PRESERVE=1 keeps existing files instead of clean-replacing
SKILL_PRESERVE="${EASYEDA_SKILL_PRESERVE:-0}"

# ── helpers ──────────────────────────────────────────────────────────────────
info()  { printf '\033[34m[easyeda-agent]\033[0m %s\n' "$*"; }
ok()    { printf '\033[32m✔\033[0m %s\n' "$*"; }
warn()  { printf '\033[33m⚠\033[0m %s\n' "$*"; }
fatal() { printf '\033[31m✘\033[0m %s\n' "$*" >&2; exit 1; }

# ── resolve latest release ───────────────────────────────────────────────────
info "Fetching latest release..."
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
[ -n "$VERSION" ] || fatal "Could not determine latest release version"
info "Latest: ${VERSION}"

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
  *) fatal "Unsupported OS: $OS (Windows: download easyeda_windows_amd64.exe manually)" ;;
esac

BINARY_NAME="easyeda_${OS}_${ARCH}"

# ── choose install dir (no sudo required) ────────────────────────────────────
if [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

# ── install CLI binary ────────────────────────────────────────────────────────
info "Downloading ${BINARY_NAME}..."
curl -fsSL "${BASE_URL}/${BINARY_NAME}" -o "${INSTALL_DIR}/easyeda"
chmod +x "${INSTALL_DIR}/easyeda"
ok "CLI installed → ${INSTALL_DIR}/easyeda"

# ── install skills (Codex + Claude Code) ──────────────────────────────────────
# Resolve which clients to install for.
# codex → ~/.codex/skills/easyeda-agent, claude → ~/.claude/skills/easyeda-agent
detect_targets() {
  # Explicit "none" → skip entirely.
  case "$INSTALL_SKILLS" in
    none|NONE|None) return 0 ;;
  esac

  if [ -n "$INSTALL_SKILLS" ] && [ "$INSTALL_SKILLS" != "auto" ]; then
    # Explicit CSV list (e.g. "codex,claude").
    printf '%s\n' "$INSTALL_SKILLS" | tr ',' '\n' | while IFS= read -r t; do
      t=$(printf '%s' "$t" | tr -d '[:space:]')
      [ -n "$t" ] && printf '%s\n' "$t"
    done
    return 0
  fi

  # auto-detect
  found=0
  if [ -d "${HOME}/.codex" ] || command -v codex >/dev/null 2>&1; then
    printf 'codex\n'; found=1
  fi
  if [ -d "${HOME}/.claude" ] || command -v claude >/dev/null 2>&1; then
    printf 'claude\n'; found=1
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
    codex)  printf '%s/.codex/skills\n' "$HOME" ;;
    claude) printf '%s/.claude/skills\n' "$HOME" ;;
    *)      return 1 ;;
  esac
}

# install_skill_to <client> <src_skill_dir>
# Cleanly replaces <base>/easyeda-agent from the release, backing up local edits.
install_skill_to() {
  _client="$1"; _src="$2"
  _base=$(client_base_dir "$_client") || { warn "Unknown skill target: ${_client} (skipped)"; return 0; }
  mkdir -p "$_base"
  _dest="${_base}/${SKILL_NAME}"

  # Records the installed version so the daemon's startup skill-sync knows this
  # dir is already current and skips a needless re-download (see `easyeda skill`).
  _write_marker() { printf '%s\n' "${VERSION#v}" > "${_dest}/.version"; }

  if [ ! -d "$_dest" ]; then
    cp -r "$_src" "$_dest"
    _write_marker
    ok "${_client} skill installed → ${_dest}"
    return 0
  fi

  if [ "$SKILL_PRESERVE" = "1" ]; then
    cp -rn "$_src"/. "$_dest"/ 2>/dev/null || cp -r "$_src"/. "$_dest"/
    _write_marker
    ok "${_client} skill updated (preserve mode, existing files kept) → ${_dest}"
    return 0
  fi

  # Detect local modifications vs the release; back up before replacing.
  if diff -r "$_src" "$_dest" >/dev/null 2>&1; then
    _write_marker
    ok "${_client} skill already up to date → ${_dest}"
    return 0
  fi
  _bak="${_dest}.bak.$(date +%Y%m%d%H%M%S)"
  mv "$_dest" "$_bak"
  cp -r "$_src" "$_dest"
  _write_marker
  ok "${_client} skill updated → ${_dest} (old backed up → ${_bak})"
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

TARGETS=$(detect_targets)
if [ -z "$TARGETS" ]; then
  info "Skill install skipped (EASYEDA_INSTALL_SKILLS=none)"
else
  info "Downloading skills.tar.gz..."
  curl -fsSL "${BASE_URL}/skills.tar.gz" | tar -xzf - -C "$TMP"
  SRC_SKILL="${TMP}/${SKILL_NAME}"
  [ -d "$SRC_SKILL" ] || fatal "skills.tar.gz did not contain ${SKILL_NAME}/"
  printf '%s\n' "$TARGETS" | while IFS= read -r client; do
    [ -n "$client" ] && install_skill_to "$client" "$SRC_SKILL"
  done
fi

# ── PATH check ────────────────────────────────────────────────────────────────
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
  warn "${INSTALL_DIR} is not in PATH"
  printf '    Add to ~/.zshrc or ~/.bashrc:\n'
  printf '    export PATH="$PATH:%s"\n\n' "$INSTALL_DIR"
fi

# ── next steps ────────────────────────────────────────────────────────────────
printf '\n'
ok "easyeda-agent ${VERSION} installed"
printf '\n'
printf 'Next steps:\n'
printf '  1. Start the daemon:\n'
printf '       easyeda daemon start\n\n'
printf '  2. Install the EasyEDA connector extension:\n'
printf '       Download: %s/easyeda-agent-connector.eext\n' "$BASE_URL"
printf '       In EasyEDA Pro: 扩展管理 → 导入扩展 → select the .eext file\n\n'
printf '  3. In EasyEDA Pro: 设置 → 允许外部交互 (Allow external interaction)\n\n'
printf '  4. Use the skill in your AI client:\n'
printf '       /easyeda-agent       (schematic + PCB workflow)\n'
printf '       Installed for detected clients: Codex (~/.codex/skills) and/or Claude Code (~/.claude/skills)\n\n'
printf 'Full docs: https://github.com/%s\n' "$REPO"
