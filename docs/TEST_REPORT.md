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
| `pip install superb-memory` **by name** from PyPI | PASS — clean Python 3.10 venv with no checkout on the path: `pip show` reports 0.2.1, `mdb` and `memorydb-mcp` land on PATH, `mdb serve` downloaded and sha256-verified `memorydb-0.2.1-linux-amd64.tar.gz`, and that venv's client created, searched and deleted a record through the served database |
| the release workflow's `pypi` job | PASS — run 41183014621 uploaded `superb_memory-0.2.1` wheel + sdist through Trusted Publishing (OIDC from the `pypi` environment, no index credential in the repository); `https://pypi.org/pypi/superb-memory/json` then reported version `0.2.1`, and the two gated-out uploads skipped rather than failing |
| `pip install memorydb-django` **by name** from PyPI | PASS — clean venv, no checkout on the path: it resolved `memorydb-django 0.2.1` + `superb-memory 0.2.1` + `Django 5.2.18` from PyPI, `mdb serve` fetched and sha256-verified the v0.2.1 binary, then `cache.set/get/incr/delete` mapped to exactly one engine record per key (`[':1:delta', ':1:gamma']`, delete removed the row, a 2s TTL row expired on its own) and a session saved by `SessionMiddleware` came back from its cookie in a second process |
| `pip install "git+https://…#subdirectory=sdk/langchain"` | PASS — clean venv installed `memorydb-langchain 0.2.1` against `superb-memory 0.2.1` from PyPI plus `langchain-core 1.7.3`, and both modules import from `site-packages` |
| `pip install memorydb-langchain` **by name** from PyPI | PASS — clean venv, no checkout on the path: it resolved `memorydb-langchain 0.2.1` + `superb-memory 0.2.1` + `langchain-core 1.7.3`, `mdb serve` fetched and sha256-verified the v0.2.1 binary, then `MemoryDBChatMessageHistory` stored a `SystemMessage`/`HumanMessage`/`AIMessage` conversation that a **second client instance** read back in order, and `clear()` removed the `kind=chat` record (one record per session, `version 1`, 2 messages for the audit-demo session) |
| the third `pypi` upload step | PASS — run 41194303445 with all three names in `PYPI_PROJECTS`: the two already-published uploads skipped, `memorydb_langchain-0.2.1` wheel + sdist uploaded, and `https://pypi.org/pypi/memorydb-langchain/json` then reported `0.2.1` |
| the `pypi` job's per-distribution gating | PASS — run 41188687918 with `PYPI_PROJECTS=superb-memory,memorydb-django` re-ran the first upload as a skip (`skip-existing`) rather than a failure, uploaded the Django wheel + sdist, and left the third step skipped |

Not executed here: `npm publish`, which needs a stored `NPM_TOKEN` secret because npm has no
trusted-publishing equivalent, and that secret has not been created. Everything else above ran
against the published v0.2.1 release, the GitHub Actions runs for it, PyPI and ghcr.io.

## Connector mirroring and the sample seed (3 November 2026)

The 28 October table above predates `sources.go`. This is what ran for the connector and demo
work, on a fresh server built from this tree (Go 1.18.1, loopback HTTP, throwaway journal):

| Check | Result |
|---|---|
| `go vet ./...` + `gofmt -e` | PASS, no findings |
| `go test -race -coverprofile=... ./...` | PASS: **36** engine/API tests (5 new in `sources_test.go`) + 1 Go client test; **73.4%** engine statement coverage (was 68.8%), 41.2% for `sdk/goclient` |
| `TestConnectorSyncWritesOnlyChangedRows` | PASS — an identical re-sync leaves `engine.Sequence()` untouched, so a no-op costs no journal frame |
| `TestConnectorComparesAgainstTheLastPayloadSynced` | PASS — a console edit to a mirrored row survives a sync of an unchanged source (reported `unchanged`, not reverted), and is replaced by the source's new value once that row moves upstream, with the edited value kept as a revision in `memories/ID/history/` |
| `TestConnectorRefusesToRewriteForeignRows` | PASS — a key another connector wrote is not taken over |
| `TestConnectorInputLimitsAndPermissions` | PASS — reader token 403, invalid connector name, duplicate key inside one payload, missing collection, empty sync |
| `TestDemoSeedIsRepeatableAndSearchable` | PASS — second `POST demo/` reports `seeded:false`; the seeded tables, facts and `region` index stay queryable |
| `mdb demo` on a fresh database | PASS — `{"seeded":true,"rows":{"customers":6,"invoices":8,"subscriptions":6,"support_tickets":6},"documents":6,"memories":4}`, one source listed as `acme-sample` with 26 rows; re-running printed `{"seeded":false,"reason":"the sample customers collection already exists"}` |
| `mdb sync` twice with the same file | PASS — first run `{"unchanged":6,"created":0,"updated":0}`, second run identical, health `sequence` **7 before and 7 after** |
| `mdb sync --delete-missing` with one row changed and one dropped | PASS — `{"updated":1,"removed":1,"unchanged":4,"written":2,"sequence":8}`: two changes, one transaction, then a re-run that reports `unchanged:5` and holds at sequence 8 |
| Provenance on the written row | PASS — `metadata{connector, connector_key, source_hash, synced_at}`, `source: "sync:acme-sample"`, `layer: "mirror"`, and `memories/<id>/history/` shows `[(1, 12500), (2, 16500)]` for the edited row |
| Indexes and search still work on mirrored rows | PASS — `mdb query customers --where region="Rajasthan"` plans `equality index: region`; `mdb search Thar --kind document` returns the mirrored row on its rendered `content` |
| Cross-connector overwrite over HTTP and MCP | PASS — both refuse with `key c-104 already exists in customers (written by connector acme-sample)`, no write, no sequence move |
| Payload strictness | PASS — a bare row object is rejected at the HTTP boundary (`json: unknown field "id"`); `{"data": …}` is required there, while the Python client and `mdb sync` normalize bare dicts for you |
| MCP server | PASS — `tools/list` returns **10** tools including `memorydb_sync`, `memorydb_list_sources`, `memorydb_demo`; both new read tools answered over stdio |
| `python3 examples/connectors_demo.py` | PASS — create 3 (`written:3`, sequence 11) → identical resync (`unchanged:3`, sequence 11) → changed plus `delete_missing` (`updated:1, removed:1, written:2`, sequence 12), then provenance, revisions, `sources.all()` and a `kind=document` search |
| `node tests/ui.cjs` in headless Chromium | PASS, now also covering the connector path: sync 2 rows through `sources/airline/sync/`, an identical re-sync that writes nothing and does not move the sequence, both mirrored rows carrying `mirrored from airline @ <synced_at>` in the console, and a hand-written document in another collection showing **no** such label |
| `python3 tests/smoke.py` | PASS — no regression in auth, CRUD, schema, indexes, pagination, rollback, ingestion, jobs, restart or backup/restore; it now also runs `sdk/cli.py` as a subprocess against the live server, so the CLI's `--where` shorthand and its refusal are covered there |
| Browser screenshot of the provenance line | PASS — inspected at 1440px against a demo-seeded workspace: the mirrored row reads `Version 1 · local-user · mirrored from acme-sample @ 2026-11-03 05:33:05`, while a hand-written row in the same result keeps the plain `Version 1 · local-user` line |
| `mdb query --where` accepts what the server's own hints print | PASS after the fix — the demo reply suggests `mdb query customers --where region="Rajasthan"`, which previously died on `json.loads` with a raw traceback; the shorthand (quoted or bare, comma- or `and`-separated, numeric values coerced) now resolves to the same equality terms, still plans `equality index: region`, the JSON form is unchanged, and a term that is neither exits `1` with `mdb: --where wants JSON like […] or field=value terms`. Covered in `tests/smoke.py` |
| `mdb memory history --id` | PASS after the fix — the client already unwraps `results`, so the CLI's second unwrap raised `TypeError: list indices must be integers or slices, not str`; it now prints revisions 1..n, oldest first |
| Every command in the README's *Everyday commands* block | RAN — `health`, `demo`, `collections ls`, `collections create`, `query` (both `--where` forms, `--fields`), `search`, `memory add/ls/history`, `doc add/get/patch/rm`, `ingest --file`, `tx --file --key`, `sync` twice, `sources`, against a live server built from this tree, each with the payload shape now named in `mdb <cmd> --help`. **Corrected below:** that pass fed `ingest`/`sync` files it had written itself, so the paths the README actually names were still untested |
| README images | PASS — `node tests/screenshots.cjs` builds the server from this tree, seeds `mdb demo`, drives the real console and Studio in headless Chromium and writes the four PNGs; each was opened and read back before being linked, and `docs/QUICKSTART.md` carries the other two |
| The two files README and QUICKSTART tell you to pass to `ingest`/`sync` | FAIL then FIXED — neither `call.json` nor `customers.json` existed anywhere in the repository, so copy-pasting those lines printed a raw `FileNotFoundError` traceback. `examples/call.json` (three-message support call, three evidence-linked facts, `context: account`) and `examples/customers.json` (three Rajasthan customers) are now shipped and both blocks reference them by that path |
| `mdb ingest --file <missing>` and `mdb tx --file <missing>` | PASS after the fix — `mdb: cannot read examples/absent.json: No such file or directory`, exit `1`, no traceback |
| `mdb ingest --file` honours `context` | PASS — the CLI dropped the field the API documents, so an example written against `docs/API.md` silently ingested into the default context; `examples/call.json` now lands its facts under `account` |
| QUICKSTART read top to bottom on a fresh journal | PASS after the fix — §5 created a `customers` collection, which §3's `mdb demo` had already seeded, so a reader following the page failed on the schema call; §5 now uses `contacts` and §6's mirror still targets the demo's `customers` (verified: a second connector may own different keys in one collection) |
| Numbers quoted as sync output | FIXED — README and QUICKSTART showed `"sequence":11` from one scratch journal. A reader's sequence depends on everything they ran before, so the docs now state the counts only (`created 3, written 3`, then `unchanged 3` with **no `written` key at all**) and say the sequence does not move, which is what a fresh-journal run of §3 → §5 → §6 printed (`sequence` 14 on both syncs) |

## v0.2.2 — what the published artifacts were checked against (3 November 2026)

`main` at `46ebe51` is tagged `v0.2.2`. Everything below was run against what the release
workflow published, not against a local build, except where it says otherwise.

| Check | Result |
|---|---|
| GitHub Release `v0.2.2` | PASS — 11 assets (4 server archives, 3 wheels, 3 sdists, `sha256sums.txt`); every asset re-downloaded and verified with `sha256sum -c` against the sidecar |
| `ghcr.io/humaniods/memorydb:0.2.2` and `:latest` | PASS — both pulled anonymously (`DOCKER_CONFIG=$(mktemp -d)`, so the local `config.json` credential for another account is not used) and `docker run --rm --entrypoint memorydb … -version` printed `0.2.2` for both tags |
| `bash install.sh` | PASS — fetched the linux-amd64 archive from the release, checked its checksum, installed into a throwaway `MEMORYDB_INSTALL_DIR=/tmp/…`; the installed binary reported `0.2.2` |
| `go install github.com/humaniods/MemoryDB@latest` | PASS — the proxy resolved the module to **v0.2.2** (this machine's Go is 1.18 so the compile itself needs the 1.24+ the README states; CI builds the shipped binaries with 1.24) |
| `pip install "git+https://github.com/humaniods/MemoryDB.git#subdirectory=sdk"` | PASS — clean venv, `superb-memory 0.2.2`, `mdb health` answered the live server |
| The three release wheels in a clean venv | PASS — `superb_memory-0.2.2`, `memorydb_django-0.2.2`, `memorydb_langchain-0.2.2` install side by side, `import memorydb, memorydb_django, memorydb_langchain` succeeds, and `mdb serve` fetched the release binary, printed the connect string, then ran `health`, `demo`, `collections ls`, `query --where region=Rajasthan`, `search`, `memory add`, `memory history`, `ingest`, `sync` ×2 and `sources` against it |
| `memorydb-mcp` from the release wheel | PASS — stdio `initialize` reported `{'name': 'memorydb', 'version': '0.2.2'}`, `tools/list` returned the 10 tools, and `memorydb_list_sources` answered with the live connectors |
| `examples/` scripts against a live server | PASS — `python_quickstart.py`, `connectors_demo.py`, `django_demo.py` (cache **and** session backends), `langchain_demo.py`, `node_quickstart.mjs` |
| `python3 manage.py check`, `gofmt -l`, `go test -race ./...` | PASS — no findings (Go tree unchanged since the tag) |
| `python3 tests/smoke.py` | PASS — HTTP + Python SDK + `mdb` CLI subprocess, restart and backup/restore |
| **PyPI upload** | **FAILED — 0.2.2 is NOT on PyPI.** `pypa/gh-action-pypi-publish@release/v1` in the `pypi` job got `403 Invalid API Token: OIDC scoped token is not valid for project 'superb-memory', project-scoped token is not valid for project: 'superb-memory'` on the very first upload. Identical from the tag push (run 41599351948) and from a `workflow_dispatch` on `main` (run 41599948632), so it is not a ref problem. `superb-memory`, `memorydb-django` and `memorydb-langchain` therefore still resolve to **0.2.1** from pip, which does not contain the connector work |
| What was ruled out | The `pypi` job still declares `environment: pypi` + `id-token: write`; `gh api …/actions/variables` shows `PUBLISH_PYPI=true` and `PYPI_PROJECTS=superb-memory,memorydb-django,memorydb-langchain`; `…/environments/pypi` has `protection_rules: []` and no branch policy; the repo holds **no** secrets at all; the wheels built and were named `superb_memory-0.2.2-*`; the same three upload steps succeeded from the same workflow shape on 31 October. Warehouse's message means the minted OIDC claims matched no registered publisher, so the remaining suspect is the publisher rows on pypi.org — which only the project owner can read |
| Retry path | The publisher rows on pypi.org have to be repaired by the project owner first. Then `gh run rerun 41599351948 --failed`: a rerun replays the workflow as it existed at the tag and reuses that run's stored artifacts, so PyPI would receive byte-identical files to the release assets — it has now been run a third time (10:40 UTC) and failed with the same 403, which rules out a transient. A `workflow_dispatch` on `main` builds whatever main's tree carries at 0.2.2, so it is only equivalent while `main` is still the tag's commit; it is not any more |
| Rough edge found, not fixed | `mdb serve --token <11 chars>` prints a `memorydb://…` connect URL and *then* the server exits on `set MEMORYDB_TOKEN (16+ characters)`; the token length should be validated before anything is printed |

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
render stored script-like text without executing it, connector sync through the HTTP API
(two rows created, an identical re-sync that writes nothing and leaves the transaction
sequence where it was) and the `mirrored from <connector>` line on every mirrored row but
on no hand-written one, evidence ingestion, queue
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

For the connector work specifically: no third-party service was ever contacted. Sources were
local JSON files and the built-in sample, so "mirrors Postgres/Stripe" describes the shape of
the API, not a tested integration - and a source that changes between two syncs, a key field
that is not unique, or a table larger than the 500-row request limit is the caller's paging
problem, not something verified here.
