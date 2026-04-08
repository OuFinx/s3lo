#!/bin/sh
set -e

REPO="OuFinx/s3lo"
BINARY="s3lo"
INSTALL_DIR="/usr/local/bin"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    darwin) OS="darwin" ;;
    linux) OS="linux" ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version via redirect (no jq dependency)
VERSION=$(curl -sSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" | grep -oE '[^/]+$')
if [ -z "$VERSION" ]; then
    echo "Failed to get latest version"
    exit 1
fi

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."

# Download
URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${OS}_${ARCH}.tar.gz"
TMP_DIR=$(mktemp -d)
curl -sSL "$URL" -o "${TMP_DIR}/${BINARY}.tar.gz"

# Extract
tar xzf "${TMP_DIR}/${BINARY}.tar.gz" -C "$TMP_DIR"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
    echo "Need sudo to install to ${INSTALL_DIR}"
    sudo mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
fi

# Remove quarantine on macOS
if [ "$OS" = "darwin" ]; then
    sudo xattr -d com.apple.quarantine "${INSTALL_DIR}/${BINARY}" 2>/dev/null || true
fi

chmod +x "${INSTALL_DIR}/${BINARY}"

# Cleanup
rm -rf "$TMP_DIR"

echo "Installed ${BINARY} ${VERSION} to ${INSTALL_DIR}/${BINARY}"
${INSTALL_DIR}/${BINARY} version
