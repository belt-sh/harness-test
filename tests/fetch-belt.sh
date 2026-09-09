#!/bin/sh
# Fetch the released belt CLI (linux-amd64) into tests/belt for --hooks belt runs.
# Reads https://dist.inference.sh/cli/manifest.json, verifies the sha256, and
# unpacks the single-file tarball. BELT_VERSION=vX.Y.Z pins a version.
set -eu
cd "$(dirname "$0")"
MANIFEST=https://dist.inference.sh/cli/manifest.json
if [ -n "${BELT_VERSION:-}" ]; then
  ver=$BELT_VERSION
  url="https://dist.inference.sh/cli/$ver/inferencesh-cli-$ver-linux-amd64.tar.gz"
  sha=""
else
  json=$(curl -fsSL "$MANIFEST")
  ver=$(printf '%s' "$json" | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' | head -1)
  url=$(printf '%s' "$json" | tr -d '\n' | sed -n 's/.*"linux-amd64": *{[^}]*"url": *"\([^"]*\)".*/\1/p')
  sha=$(printf '%s' "$json" | tr -d '\n' | sed -n 's/.*"linux-amd64": *{[^}]*"sha256": *"\([^"]*\)".*/\1/p')
fi
[ -n "$url" ] || { echo "fetch-belt: no linux-amd64 build in manifest" >&2; exit 1; }
tmp=$(mktemp -d)
curl -fsSL "$url" -o "$tmp/belt.tgz"
if [ -n "$sha" ]; then
  echo "$sha  $tmp/belt.tgz" | sha256sum -c - >/dev/null
fi
tar xzf "$tmp/belt.tgz" -C "$tmp"
bin=$(find "$tmp" -maxdepth 1 -type f ! -name belt.tgz | head -1)
[ -n "$bin" ] || { echo "fetch-belt: tarball has no binary" >&2; exit 1; }
mv "$bin" belt
chmod +x belt
rm -rf "$tmp"
echo "fetch-belt: $ver -> tests/belt"
