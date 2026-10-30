# Verification report — MemoryDB 0.2

Originally executed on Linux with Go 1.27.3 and a C compiler, and **re-run on 28 October
2026** on the Linux box that carries the Univorya integration, whose `go` is 1.18.1 — the
module asks for 1.24 and older toolchains ignore the directive, so everything below passed
on 1.18.1 except the cross-compiles noted. These are local results; GitHub Actions has not
yet been run for this repository.

| Check | Result |
|---|---|
| `go test -race -coverprofile=... ./...` | PASS: 31 engine/API tests + 1 Go client test |
| Engine/API statement coverage | 68.8% |
| Go client statement coverage | 60.9% |
| `go vet ./...` | PASS |
| `python3 tests/smoke.py` | PASS: actual server + HTTP + Python SDK |
| `node tests/ui.cjs` | PASS: actual server + headless Chromium |
| Desktop console visual inspection | PASS: rendered screenshot inspected |
| Mobile width check (390px) | PASS: tested jobs view has no horizontal overflow |

## Local install story (28 October 2026)

The module is now `github.com/humaniods/MemoryDB` and the server answers `-version`, so the
database can be obtained without cloning this tree. What was actually executed here:

| Check | Result |
|---|---|
| `bash build-dist.sh linux/amd64 linux/arm64 darwin/amd64` | PASS — three `CGO_ENABLED=0` archives in `dist/` plus `sha256sums.txt` |
| `bash build-dist.sh` (all four default targets) | `darwin/arm64` fails inside Go 1.18.1's own assembler (`sys_darwin_arm64.s: unexpected token`), not in this code; the release workflow pins Go 1.24 |
| `GOOS=windows go build` | Fails with `undefined: lockPath` — `lock_unix.go` is `//go:build linux \|\| darwin \|\| freebsd`, so Windows stays unsupported and `install.sh` says so instead of offering a broken artifact |
| `bash install.sh` against `--dist-base file://…/dist` | PASS — fetched the archive for this OS/CPU, matched it against `sha256sums.txt`, installed the binary, and `memorydb -version` answered `0.2.1` |
| `bash install.sh --from <archive>` | PASS when `sha256sums.txt` beside it names and matches the file |
| `bash install.sh --from` with a wrong checksum | exits 1: `sha256 mismatch … refusing to install it` |
| `bash install.sh --from renamed.tar.gz` beside a sidecar that does not name it | exits 1: `… is not listed in …; refusing`, with `MEMORYDB_UNCHECKED=1` as the documented override |
| `bash install.sh --from` an archive built for another CPU | installs and reports `unknown (could not execute it here)` instead of claiming a version it never ran |
| `bash install.sh --source .` | PASS — builds with `go build -trimpath -ldflags -X main.version=…`, no network |
| missing asset (`--dist-base file:///tmp/nope`) | exits 1 and points at `--source` rather than installing nothing |
| `uname` shim reporting `MINGW64_NT-10.0` | exits 1 with the WSL / `docker build` instructions before touching the filesystem |
| `bash install.sh --help` | prints the four usage lines and exits 0 |
| installed binary serving | `MemoryDB 0.2.1 listening on 127.0.0.1:18095`; `/api/v1/health/` returns `version: 0.2.1` with a bearer token and 401 without one |
| `pip install ./sdk` in a fresh Python 3.10 venv | PASS — `superb_memory-0.2.1-py3-none-any.whl` built and installed; that venv's `memorydb` module (loaded from `site-packages`) created a collection and read back a record through the installed server |
| the three release wheels by URL in a checkout-free venv | PASS — `pip install <release wheel URL>` for `superb-memory`, `memorydb-django`, `memorydb-langchain`, then `mdb serve` downloaded `memorydb-0.2.1-linux-amd64.tar.gz`, verified it against `sha256sums.txt` and served the Python client, the Django cache and sessions through `SessionMiddleware`, LangChain history and the MCP `tools/list` handshake |
| every v0.2.1 asset against `sha256sums.txt` | PASS — 10/10 `OK` after re-download; the four archives contain `ELF x86-64`/`ELF aarch64` statically linked and `Mach-O x86_64`/`arm64` PIE binaries respectively |
| anonymous `docker pull ghcr.io/humaniods/memorydb:0.2.1` | PASS with an empty `DOCKER_CONFIG` (public package), both platforms in the index; the running container answered health and accepted writes |

Not executed here: `install.sh`'s default GitHub-release fetch, `go install
github.com/humaniods/MemoryDB@latest`, `pip install git+https://…#subdirectory=sdk` and
`.github/workflows/release.yml` — all four need this work committed and tagged on GitHub,
which has not been done.

## Verified failure and consistency cases

Atomic batch rollback, duplicate unique values under concurrent writes, exact
JSON integer comparisons, missing versus null, schema enforcement, stale writes,
durable idempotency/restart, partial frame recovery, checksum corruption, injected
short append, injected fsync failure with unknown outcome, exclusive process
ownership, compaction with preserved versions and lock, backup/restore checksums,
workspace isolation, reader role restrictions, owner-only private history,
expiry-invalidated pagination, heterogeneous sort ordering, empty JSON objects,
malformed job handling, lease/restart behavior, fact correction/evidence validation,
mock extraction provider, skill approval spoofing, Go local-variable shadowing,
scoped graph and Unicode revision reconstruction.

## End-to-end flows

Python smoke: authentication, memory CRUD/vector search/history/version conflict,
collection schema/index, document create/update/delete, query chaining/projection/
pagination, transaction rollback, idempotent creates, ingestion, job processing,
server restart, CLI verify/backup/compaction/restore.

Browser smoke: connect, create collection, create/edit document, indexed query,
render stored script-like text without executing it, evidence ingestion, queue
and run job, mobile overflow check, memory Studio creation. No browser page errors
were observed. Tests are development tooling; browser packages are not runtime
server dependencies. The test names describe only what their assertions cover.

## Not verified / not promised

No production deployment, real provider request, LLM memory-quality evaluation,
independent penetration test, power-loss hardware testing, million-record load
benchmark, exhaustive filesystem failure injection, replication test, or Windows
support. TLS configuration exists but the integration harness uses loopback HTTP.
The CI definition is included but its remote execution is unverified. Coverage
is not proof of correctness or security; all edge cases and zero bugs are not
claimed. See FEATURES.md for unimplemented capabilities.
