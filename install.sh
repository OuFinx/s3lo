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
VERSION=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" | grep -oE '[^/]+$')
if [ -z "$VERSION" ]; then
    echo "Failed to get latest version"
    exit 1
fi

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# Download. -f matters: without it curl writes a 404 HTML body to the archive
# and the failure surfaces later as a confusing tar error.
ARCHIVE="${BINARY}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"
curl -fsSL "${BASE}/${ARCHIVE}" -o "${TMP_DIR}/${ARCHIVE}"

# Verify the archive against the checksums published with the release. A
# curl-pipe-sh installer that skips a checksum it already publishes is trusting
# the network for the contents of a binary about to be run as root.
if curl -fsSL "${BASE}/checksums.txt" -o "${TMP_DIR}/checksums.txt"; then
    if command -v sha256sum >/dev/null 2>&1; then
        SHA_CMD="sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        SHA_CMD="shasum -a 256"
    else
        echo "Cannot verify download: neither sha256sum nor shasum found" >&2
        exit 1
    fi
    EXPECTED=$(grep " ${ARCHIVE}\$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')
    if [ -z "$EXPECTED" ]; then
        echo "Cannot verify download: ${ARCHIVE} is not listed in checksums.txt" >&2
        exit 1
    fi
    ACTUAL=$(${SHA_CMD} "${TMP_DIR}/${ARCHIVE}" | awk '{print $1}')
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        echo "Checksum mismatch for ${ARCHIVE}" >&2
        echo "  expected: ${EXPECTED}" >&2
        echo "  actual:   ${ACTUAL}" >&2
        exit 1
    fi
    echo "Checksum verified."
else
    echo "Cannot verify download: checksums.txt not found for ${VERSION}" >&2
    exit 1
fi

# Extract
tar xzf "${TMP_DIR}/${ARCHIVE}" -C "$TMP_DIR"

# Make it executable while it is still ours to chmod.
chmod +x "${TMP_DIR}/${BINARY}"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    if [ "$OS" = "darwin" ]; then
        xattr -d com.apple.quarantine "${INSTALL_DIR}/${BINARY}" 2>/dev/null || true
    fi
else
    echo "Need sudo to install to ${INSTALL_DIR}"
    sudo mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    if [ "$OS" = "darwin" ]; then
        sudo xattr -d com.apple.quarantine "${INSTALL_DIR}/${BINARY}" 2>/dev/null || true
    fi
fi

echo "Installed ${BINARY} ${VERSION} to ${INSTALL_DIR}/${BINARY}"
${INSTALL_DIR}/${BINARY} version
