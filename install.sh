#!/bin/bash
set -euo pipefail

REPO="logicminds/filemaid"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="filemaid"

OS=$(uname -s)
ARCH=$(uname -m)

if [[ "$OS" != "Darwin" ]]; then
    echo "Error: filemaid only supports macOS at this time." >&2
    exit 1
fi

if [[ "$ARCH" != "arm64" ]]; then
    echo "Error: filemaid only supports Apple Silicon (arm64) at this time." >&2
    exit 1
fi

ASSET="filemaid-darwin-arm64"
CHECKSUM_FILE="${ASSET}.sha256"

LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [[ -z "$LATEST" ]]; then
    echo "Error: could not determine latest release." >&2
    exit 1
fi

echo "Installing filemaid ${LATEST} for darwin-arm64..."

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

cd "$TMP_DIR"

curl -fsSL -o "$ASSET" "https://github.com/${REPO}/releases/download/${LATEST}/${ASSET}"
curl -fsSL -o "$CHECKSUM_FILE" "https://github.com/${REPO}/releases/download/${LATEST}/${CHECKSUM_FILE}"

if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c "$CHECKSUM_FILE"
else
    echo "Warning: shasum not found; skipping checksum verification." >&2
fi

mkdir -p "$INSTALL_DIR"
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd)"
mv "$ASSET" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

echo "filemaid ${LATEST} installed to ${INSTALL_DIR}/${BINARY_NAME}"
echo "Make sure ${INSTALL_DIR} is on your PATH."
