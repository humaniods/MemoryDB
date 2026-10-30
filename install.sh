#!/usr/bin/env bash
# Put the MemoryDB server on this machine: a release binary when one exists for this
# platform, an archive you already downloaded, or a build from the source tree.
#
#   ./install.sh                              # newest release binary for this OS/arch
#   ./install.sh --from memorydb-0.2.0-linux-amd64.tar.gz
#   ./install.sh --source .                   # go build, no network
#   MEMORYDB_DIST_BASE=file:///tmp/dist ./install.sh
#
# Windows has no native build: the database lock is advisory (flock), which Go only
# exposes on Unix. On Windows use WSL or the Docker image.
set -euo pipefail

version="${MEMORYDB_VERSION:-0.2.0}"
repo="${MEMORYDB_REPO:-humaniods/MemoryDB}"
prefix="${MEMORYDB_INSTALL_DIR:-$HOME/.local/bin}"
dist_base="${MEMORYDB_DIST_BASE:-https://github.com/$repo/releases/download/v$version}"
archive=""
source_dir=""

while [ $# -gt 0 ]; do
  case "$1" in
    --version) version="$2"; shift 2 ;;
    --prefix) prefix="$2"; shift 2 ;;
    --from) archive="$2"; shift 2 ;;
    --source) source_dir="$2"; shift 2 ;;
    --dist-base) dist_base="$2"; shift 2 ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW*|MSYS*|CYGWIN*|Windows*)
    echo "MemoryDB has no native Windows build (advisory file locking is Unix-only)." >&2
    echo "Run it in WSL: bash install.sh   —  or build the Docker image from this tree:" >&2
    echo "  docker build -t memorydb . && docker run -p 8080:8080 -e MEMORYDB_TOKEN=... -v memorydb-data:/data memorydb" >&2
    exit 1 ;;
  *) echo "unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1;
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

install_from() { # archive path, already on disk
  local expected="$1" file="$2"
  if [ -n "$expected" ] && [ "$(checksum "$file")" != "$expected" ]; then
    echo "sha256 mismatch for $(basename "$file"); refusing to install it." >&2
    exit 1
  fi
  tar -xzf "$file" -C "$tmp"
  [ -x "$tmp/memorydb" ] || { echo "the archive holds no memorydb binary" >&2; exit 1; }
}

fetch() { # source (https URL, file:// URL or plain path) -> destination
  case "$1" in
    https://*) curl -fL --proto '=https' --tlsv1.2 -sS -o "$2" "$1" ;;
    file://*) cp "${1#file://}" "$2" 2>/dev/null ;;
    *) cp "$1" "$2" 2>/dev/null ;;
  esac
}

if [ -n "$source_dir" ]; then
  command -v go >/dev/null 2>&1 || { echo "--source needs Go 1.24 or newer on PATH." >&2; exit 1; }
  (cd "$source_dir" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$tmp/memorydb" .)
elif [ -n "$archive" ]; then
  # A file the operator fetched themselves still gets checked when the checksums list
  # sits next to it, which is how a release page works. A sidecar that exists but does
  # not name this file is a mismatch of a different kind, so it stops here too.
  sidecar="$(dirname "$archive")/sha256sums.txt"
  wanted=""
  if [ -f "$sidecar" ]; then
    wanted="$(grep -F " $(basename "$archive")" "$sidecar" | cut -d' ' -f1 || true)"
    if [ -z "$wanted" ] && [ -z "${MEMORYDB_UNCHECKED:-}" ]; then
      echo "$(basename "$archive") is not listed in $sidecar; refusing to install it." >&2
      echo "Set MEMORYDB_UNCHECKED=1 to install it anyway." >&2
      exit 1
    fi
  fi
  install_from "$wanted" "$archive"
else
  name="memorydb-$version-$os-$arch.tar.gz"
  echo "fetching $dist_base/$name"
  if [ "${dist_base#https://}" != "$dist_base" ] && ! command -v curl >/dev/null 2>&1; then
    echo "curl is needed to download a release binary; use --source or --from." >&2
    exit 1
  fi
  # https only over the network (no plain-http downgrade); a file:// URL or a plain
  # directory is a copy, which is how --dist-base points at a release page already saved.
  fetch "$dist_base/$name" "$tmp/$name" || {
    echo "no release binary for $os/$arch at $dist_base." >&2
    echo "Build it from the source tree instead: ./install.sh --source /path/to/MemoryDB" >&2
    exit 1
  }
  wanted=""
  if fetch "$dist_base/sha256sums.txt" "$tmp/sha256sums.txt"; then
    wanted="$(grep -F " $name" "$tmp/sha256sums.txt" | cut -d' ' -f1 || true)"
    [ -n "$wanted" ] || echo "warning: $name is not listed in sha256sums.txt; installed unchecked" >&2
  fi
  install_from "$wanted" "$tmp/$name"
fi

mkdir -p "$prefix"
install -m 0755 "$tmp/memorydb" "$prefix/memorydb"
# Ask the installed binary what it is; a cross-installed archive for a CPU this machine
# cannot run still installs, and the honest report is "could not execute".
echo "installed $("$prefix/memorydb" -version 2>/dev/null || echo 'unknown (could not execute it here)') to $prefix/memorydb"
token="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))' 2>/dev/null || true)"
[ -n "$token" ] || token="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
cat <<EOF

Start it:

  export MEMORYDB_TOKEN='$token'
  $prefix/memorydb -addr 127.0.0.1:8080 -data memory.wal -worker

Console at http://127.0.0.1:8080 — the token above is yours; keep it out of source control.
If $prefix is not already on PATH: export PATH="$prefix:\$PATH"

The Python client is a separate install: pip install ./sdk  (or the wheel from the release).
EOF
