#!/usr/bin/env bash
# Build one distributable archive per platform into dist/, with a sha256sums.txt next to
# them — the same layout install.sh fetches from a GitHub release, so a local run of this
# script produces something the installer can be tested against end to end.
#
#   ./build-dist.sh                 # all four supported targets
#   ./build-dist.sh linux/amd64     # just one
#
# Go 1.24 or newer is required for the darwin/arm64 cross-build; older toolchains fail in
# their own assembler, not in this code. Windows is not a target: the database lock is
# advisory flock(), which Go only exposes on Unix.
set -euo pipefail

root="$(cd "$(dirname "$0")" && pwd)"
version="${MEMORYDB_VERSION:-$(grep -o 'version = "[^"]*"' "$root/main.go" | cut -d'"' -f2)}"
out="${MEMORYDB_DIST_DIR:-$root/dist}"
targets=("${@:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}")

mkdir -p "$out"
rm -f "$out"/*.tar.gz "$out"/sha256sums.txt
for target in ${targets[@]}; do
  os="${target%/*}"; arch="${target#*/}"
  stage="$out/.stage-$os-$arch"
  rm -rf "$stage"; mkdir -p "$stage"
  echo "building $os/$arch"
  (cd "$root" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
     go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/memorydb" .)
  tar -czf "$out/memorydb-$version-$os-$arch.tar.gz" -C "$stage" memorydb
  rm -rf "$stage"
done
(cd "$out" && sha256sum memorydb-*.tar.gz > sha256sums.txt)
ls -1 "$out"
echo "dist ready at $out (version $version)"
