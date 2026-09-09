#!/usr/bin/env bash
set -euo pipefail

# Builds understory and installs it to ~/.local/bin (or
# $UNDERSTORY_INSTALL_DIR), then code-signs it with a stable local
# certificate + identifier instead of leaving it plain ad-hoc-signed.
#
# The signing is a leftover from the AppleScript window-detection era,
# kept harmlessly: understory has needed no macOS permission since
# dashkit v0.8.0's window registry replaced the title cascade (VS Code
# windows are matched by exact folder path against
# ~/.local/state/vscode-windows/, a plain directory read). The stable
# identity stays so this script stays symmetrical with canopy's (which
# still scripts Ghostty) and so a future permission-needing feature
# wouldn't need the grant dance again: an ad-hoc-signed binary's
# signature is keyed to its content hash, so macOS drops any TCC grant
# on every rebuild, while a real certificate + fixed --identifier
# anchors the grant across rebuilds. This script assumes that
# certificate already exists in your login keychain (create one once,
# e.g. via openssl + `security import`/`add-trusted-cert`, named
# whatever UNDERSTORY_CODESIGN_CERT below expects).

CERT_NAME="${UNDERSTORY_CODESIGN_CERT:-luiul-local-devtools}"
IDENTIFIER="com.luiul.understory"
DEST="${UNDERSTORY_INSTALL_DIR:-$HOME/.local/bin}/understory"

cd "$(dirname "$0")/.."

tmp=$(mktemp -t understory-build)
go build -o "$tmp" ./cmd/understory
install -m 0755 "$tmp" "$DEST"
rm -f "$tmp"

if security find-identity -v -p codesigning 2>/dev/null | grep -q "\"$CERT_NAME\""; then
	codesign --force --sign "$CERT_NAME" --identifier "$IDENTIFIER" "$DEST"
	echo "Installed and signed $DEST as $IDENTIFIER."
else
	echo "warning: no codesigning identity named \"$CERT_NAME\" found in the login keychain." >&2
	echo "warning: $DEST is only ad-hoc signed. Harmless today (understory needs no macOS" >&2
	echo "warning: permission), but any future permission-needing feature would need re-granting" >&2
	echo "warning: after every rebuild. See scripts/install.sh's own comment for the setup." >&2
	echo "Installed (unsigned) $DEST." >&2
fi
