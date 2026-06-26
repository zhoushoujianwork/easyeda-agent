#!/usr/bin/env bash
# easyeda-agent installer
# Usage: curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh
set -euo pipefail

REPO="zhoushoujianwork/easyeda-agent"
SKILLS_DIR="${HOME}/.claude/skills"

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

# ── install skills ────────────────────────────────────────────────────────────
info "Installing skills to ${SKILLS_DIR}..."
mkdir -p "$SKILLS_DIR"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "${BASE_URL}/skills.tar.gz" | tar -xzf - -C "$TMP"
# Copy each skill dir; preserve existing files with -n to avoid clobbering user edits
cp -r "$TMP"/. "$SKILLS_DIR/"
ok "Skills installed → ${SKILLS_DIR}"

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
printf '  4. In Claude Code, use the skills:\n'
printf '       /easyeda-schematic   (schematic design)\n'
printf '       /easyeda-pcb         (PCB layout)\n\n'
printf 'Full docs: https://github.com/%s\n' "$REPO"
