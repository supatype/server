#!/usr/bin/env bash
# Prints the minisign release public key, the base64 line of $MINISIGN_PUBLIC_KEY with
# any "untrusted comment" line dropped, for embedding in a release build with
#   -X github.com/supatype/server/internal/pluginwidgets.embeddedReleaseKey=<key>
#
# Fails when there is no key, so a release never ships a server that cannot check a
# plugin widget's signature: such a server would refuse every widget in production.
set -euo pipefail
key="$(printf '%s\n' "${MINISIGN_PUBLIC_KEY:-}" | grep -v '^untrusted comment' | tr -d '\r\n[:space:]')"
if [ -z "$key" ]; then
  echo "release-public-key: MINISIGN_PUBLIC_KEY is empty; set the release public key secret" >&2
  exit 1
fi
printf '%s' "$key"
