#!/usr/bin/env bash
set -euo pipefail

# Builds understory and installs it to ~/.local/bin (or
# $UNDERSTORY_INSTALL_DIR), then code-signs it with a stable local
# certificate + identifier instead of leaving it plain ad-hoc-signed.
#
# The signing is load-bearing again: dashkit v0.12.0 replaced the
# window registry with window-title matching, so understory scripts
# System Events on every poll (list VS Code's window titles) and on
# every Enter (raise the matched window), both of which need the macOS
# Automation permission. An ad-hoc-signed binary's signature is keyed
# to its content hash, so macOS drops any TCC grant on every rebuild,
# while a real certificate + fixed --identifier anchors the grant
# across rebuilds. This script assumes that certificate already exists
# in your login keychain (create one once, e.g. via openssl + `security
# import`/`add-trusted-cert`, named whatever UNDERSTORY_CODESIGN_CERT
# below expects).

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
	echo "warning: $DEST is only ad-hoc signed; macOS will likely need the Automation" >&2
	echo "warning: permission re-granted after every future rebuild. See scripts/install.sh's" >&2
	echo "warning: own comment for why, and how to set up a stable local signing identity." >&2
	echo "Installed (unsigned) $DEST." >&2
fi
