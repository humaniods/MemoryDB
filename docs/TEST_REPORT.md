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

The commit tagged `v0.2.2` is `46ebe51`. Everything below was run against what the release
workflow published, not against a local build, except where it says otherwise. (This section was
written while `main` was still that commit; `main` has since moved on, see the 4 November section.)

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
| **PyPI upload** | **FAILED on 3 November, RESOLVED on 4 November — see the next section.** `pypa/gh-action-pypi-publish@release/v1` in the `pypi` job got `403 Invalid API Token: OIDC scoped token is not valid for project 'superb-memory', project-scoped token is not valid for project: 'superb-memory'` on the very first upload. Identical from the tag push (run 41599351948), from a `workflow_dispatch` on `main` (run 41599948632) and from a targeted rerun of the failed job, so it was not a ref problem and not a transient |
| What was ruled out | The `pypi` job still declares `environment: pypi` + `id-token: write`; `gh api …/actions/variables` shows `PUBLISH_PYPI=true` and `PYPI_PROJECTS=superb-memory,memorydb-django,memorydb-langchain`; `…/environments/pypi` has `protection_rules: []` and no branch policy; the repo holds **no** secrets at all; the wheels built and were named `superb_memory-0.2.2-*`; the same three upload steps succeeded from the same workflow shape on 31 October |
| First diagnosis, and why it was wrong | "Warehouse's message means no registered publisher matched these claims" — that is the `invalid-publisher` error, which says *"valid token, but no corresponding publisher"*. Our 403 said something else, so the claim-matching theory (and the stale `repository_owner_id` idea that came with it) was wrong. Read the error text against the code that emits it before theorising |
| Rough edge found, not fixed | `mdb serve --token <11 chars>` prints a `memorydb://…` connect URL and *then* the server exits on `set MEMORYDB_TOKEN (16+ characters)`; the token length should be validated before anything is printed |

## 4 November 2026 — PyPI unblocked, and what 0.2.2's wheels still miss

| Check | Result |
|---|---|
| Root cause, read out of Warehouse | Both halves of the 403 come from `warehouse/macaroons/caveats/__init__.py`: `OIDCPublisher.verify` → *"OIDC scoped token is not valid for project 'X'"* fires when `context not in request.oidc_publisher.projects`, and `ProjectID.verify` → *"project-scoped token is not valid for project: 'X'"*. Both firing means the token **was** minted - a publisher matched every claim - but `mint_token` scopes the macaroon to `publisher.projects` of the **one** row `GitHubActionPublisher.lookup_by_claims` returned, and that lookup filters on repo/owner/owner-id/workflow + environment only and has no project input at all (the project name is only known later, from the wheel's metadata). The console treats `(repository_name, repository_owner, workflow_filename, environment)` as the identity of a publisher and attaches the existing row rather than duplicating it - but **pending** publishers `reify` a fresh row each time, so the three one-project-per-round uploads on 31 October left **three duplicate rows** on one claims tuple. From then on every release could authorize only one of the three projects, which is exactly what the 403 said |
| Console state before the fix | `superb-memory` and `memorydb-django` both showed `Repository: humaniods/MemoryDB`, `Workflow: release.yml`, `Environment name: (Any)` - three identical rows, none shared |
| Fix applied (by the project owner, in the PyPI console) | All three publishers removed, then re-added once per project with `humaniods` / `MemoryDB` / `release.yml` / environment `pypi`, so PyPI's dedupe attached **one shared row** to all three projects. No repository change was needed: one job with `environment: pypi` now resolves one row whose `projects` list carries all three names |
| PyPI upload, retry | PASS — `gh run rerun 41599351948 --failed` (attempt 4, 05:36 UTC) finished `completed/success` with the `pypi` job green; `container`, `server`, `python-clients` and `publish` were untouched, and the release still reports `published_at 2026-11-03T07:54:56Z` with its 11 assets |
| PyPI indexes | PASS — `superb-memory`, `memorydb-django` and `memorydb-langchain` all report **0.2.2** with wheel + sdist each |
| Byte-identity with the release | PASS — all six PyPI files (`3 wheels + 3 sdists`) have the same sha256 as the GitHub Release assets, and the release's 10 checksum entries still match `sha256sums.txt` exactly. The rerun reused the tag run's stored artifacts, as intended - nothing was rebuilt |
| Clean venv, installed **from PyPI** | PASS — `pip install superb-memory memorydb-django memorydb-langchain` pulled 0.2.2 with Django 5.2.19 and langchain-core 1.7.4; `mdb serve` bootstrapped the release binary and printed `MemoryDB 0.2.2 listening`, then `health` returned `version 0.2.2` |
| Every documented command, in doc order, on a fresh journal | PASS — `demo` (6 documents / 4 memories / 26 mirror rows), `collections ls`, `query customers --where region=Rajasthan --fields id,name,mrr` (equality index used, provenance fields present), `search "renewal" --limit 5`, `memory add --kind fact`, `memory history --id …` (single revision, oldest first), `ingest --file examples/call.json` (chat + 3 evidence-linked facts), `sync stripe …` → `created 3, written 3` then `unchanged 3` with no `written` key, `sync … --delete-missing` → `removed 0` because the demo rows belong to the `acme-sample` connector and `stripe` may only tombstone its own keys, and `sources` listing both connectors with their row counts |
| Django cache + sessions from the PyPI wheel | PASS — `MemoryDBCache` `set/get/add/get_or_set/touch/delete_many/clear` (add returns False on collision), and `SESSION_ENGINE = "memorydb_django.sessions"` round-tripped a saved session through a fresh `SessionStore` and `cycle_key` worked. Note the harness needs `SECRET_KEY` for session signing - that is Django's requirement, not the backend's |
| LangChain from the PyPI wheel | PASS — the README snippet verbatim: two appends read back as `[('human', …), ('ai', 'Jaipur.')]`, `objects.history` shows revisions `[1, 2]` (immutable trail), `clear()` empties it |
| MCP from the PyPI wheel | PASS — `initialize` → `{'name': 'memorydb', 'version': '0.2.2'}`, `tools/list` → the 10 tools, `memorydb_health` answered the live server |
| ghcr re-check | PASS — `ghcr.io/humaniods/memorydb:0.2.2` and `:latest` both report `0.2.2` on an anonymous pull |
| **Gap found: 0.2.2's wheels predate the doc fixes** | `v0.2.2` is `46ebe51`, but the example files and the CLI fixes landed in `84fa3a4`, which is **only on `main`**. So the published 0.2.2 wheel still (a) has no `examples/call.json` / `examples/customers.json` in the repository at that tag, (b) `mdb ingest --file <wrong path>` raises a raw `FileNotFoundError` traceback instead of `mdb: cannot read …`, and (c) `mdb ingest` **drops the `context` field** the payload documents, so facts land with an empty context. The happy-path commands above pass because they read the files from a `main` checkout. The published README on GitHub is `main`, so readers are being pointed at behaviour the released wheel does not have - this needs a 0.2.3 patch release rather than a doc rollback |

## v0.2.3 (4 November 2026) — the release whose wheels match their own documentation

`efb027e` is tagged `v0.2.3`. It moves ten version strings together - `main.go`, the
`install.sh` default, `sdk/cli.py`'s `SERVER_VERSION`, `sdk/memorydb_mcp.py`'s `SERVER_INFO`,
the three `pyproject.toml` files, `sdk/node/package.json`, and the `v0.2.2` references in
`docs/QUICKSTART.md` and `docs/FEATURES.md` - so that the artifacts on PyPI carry `84fa3a4`.

| Check | Result |
|---|---|
| Release workflow | PASS — run 41782917182, every job green: `server`, `python-clients`, `publish`, `container`, **`pypi`** (`npm` skipped). This is the first **fresh tag** to publish to PyPI successfully, not a rerun of an old one, so the shared-publisher fix is proven for future releases |
| PyPI indexes | PASS — `superb-memory`, `memorydb-django` and `memorydb-langchain` all serve **0.2.3** (wheel + sdist). Note for the next reader: `/pypi/<name>/0.2.3/json` answers 200 immediately after upload while `/pypi/<name>/json` keeps reporting the previous version for several minutes because that document is CDN-cached - check the version-specific URL first, then `pip install <name>==0.2.3` in a clean venv, which is the authoritative test |
| Byte-identity with the release | PASS — all six 0.2.3 PyPI files carry the same sha256 as the GitHub Release assets; `v0.2.3` has 11 assets and `sha256sums.txt`'s 10 entries all match |
| `bash install.sh` at the tag | PASS — fetched and checksum-verified the linux-amd64 archive into a throwaway `MEMORYDB_INSTALL_DIR`; the installed binary reported `0.2.3` |
| `go install …@latest` | PASS — the module proxy resolved **v0.2.3** (the compile still needs the Go 1.24+ the README states; this box has 1.18) |
| ghcr | PASS — `:0.2.3` reports `0.2.3`, and `:latest` reports `0.2.3` **after an explicit `docker pull`**. A plain `docker run` on a `latest` this machine had already pulled printed `0.2.2`: the daemon serves the cached manifest, so re-pull before trusting a `latest` check |
| The three gaps 0.2.2 shipped with, re-tested from PyPI | PASS — `git ls-tree v0.2.3` carries `examples/call.json` and `examples/customers.json`; `mdb ingest --file does-not-exist.json` now prints `mdb: cannot read does-not-exist.json: No such file or directory` and exits 1 with no traceback; and `mdb ingest --file examples/call.json` writes all three facts with `metadata.context = 'account'`, which 0.2.2 silently dropped |
| QUICKSTART in document order, fresh journal | PASS — `demo` (6 documents, 4 memories, 26 mirrored rows), then §6 exactly as printed: first sync `created 3, updated 0, unchanged 0, written 3`, second sync `created 0, updated 0, unchanged 3` and **no `written` key**, `--delete-missing` `removed 0` because the demo rows belong to `acme-sample` and `stripe` may only tombstone its own keys, `sources` → `acme-sample` 26 rows / `stripe` 3 rows, `query customers --where region=Rajasthan --fields id,name` → `equality index: region` |
| Django cache + sessions, 0.2.3 wheels | PASS — `MemoryDBCache` round-trip, `add` returns False on a collision, `memorydb_django.sessions` saved and reloaded a session through a fresh `SessionStore` |
| LangChain, 0.2.3 wheel | PASS — the README snippet verbatim, human + ai read back in order |
| MCP, 0.2.3 wheel | PASS — `initialize` → `{'name': 'memorydb', 'version': '0.2.3'}`, `tools/list` → the 10 tools |
| Local gates before tagging | PASS — `gofmt -l` clean, `go vet ./...` clean, `go test -race ./...` ok, `go build` + `-version` printed `0.2.3`, `python3 -m build` produced `superb_memory-0.2.3-py3-none-any.whl` with `Version: 0.2.3` in its metadata, `python3 manage.py check` clean, `python3 tests/smoke.py` PASS |
| Behaviour worth knowing, not a bug | `mdb sync` against a collection that does not exist returns `mdb error [400]: collection customers does not exist; create it first` and exits 1. QUICKSTART §6 only works because §3's `mdb demo` created `customers`, so the section is correct **in document order** and a reader who jumps straight to §6 gets a clean, actionable error rather than a silent create |

## v0.2.4 (4 November 2026) — an outside tester found a real `avg` bug

A reviewer who cloned the project and tested `v0.2.3` independently reported that `avg` cuts
repeating decimals: `avg(1,1,2)` answered `1` instead of `1.3333333333333333`, and `41597/770`
answered `54.0`. The report was right, so `404c363` carries the fix and `b5bb43f` is tagged
`v0.2.4`. Reproduced against a binary built from `main` **before** editing anything, and the root
cause was exactly as filed: `rationalJSON` used `rem.Sign() == 1` to decide that a denominator was
made of 2s and 5s alone, but `Sign` only says *positive* - the leftovers after dividing out the 2s
and 5s (3, 7, 11) are positive too, so every repeating quotient took the exact-print branch and was
rounded by `FloatString(places)`, where `places` counts only the 2s and 5s and is usually 0 or 1.
The test has to be `rem == 1`. **`docs/API.md:122` already promised 17 significant digits, so the
documentation was correct and the code was not** - the opposite of the 0.2.2 case, where the wheels
matched their docs and the docs had run ahead of the code.

| Check | Result |
|---|---|
| Live repro before the fix | `avg(1,1,2)` → `"values":{"avg_v":1}` from a `main` build on `POST query/t/` |
| Blast radius | `avg` is the only caller that divides (`documents.go:783`). `sum` reports the accumulator as-is, and `engine.go:423` (`inc`) adds finite decimals whose sum is always finite, so no other output moves; no README or doc quotes an `avg` result, so nothing needed re-wording |
| Fix verification, independently sourced | `aggregate_test.go` pins both halves: five finite cases keep printing in full (`0.5`, `0.125`, `0.35`, `2251799813685263.25`, `2`) and seven repeating cases are checked against **17 significant digits computed separately** with Python's `decimal` at `prec=17`. Expected values were re-derived here before trusting the tester's table, and matched |
| The test actually guards something | The one-line condition was flipped back and the suite re-run: it fails on exactly the seven cases the tester listed (`0`, `-0`, `1`, `0`, `0.2`, `54.0`, `49.65`) plus the end-to-end `avg(1,1,2)`. Then flipped forward again and everything passed |
| Local gates | `gofmt -l` clean, `go vet ./...` clean, `go test -race -count=1 ./...` ok with coverage **73.6%** (was 73.4%), `python3 tests/smoke.py` PASS, `./memorydb -version` prints `0.2.4` |
| Release workflow | PASS — run 41804878483, all jobs green (`server`, `python-clients`, `publish`, `container`, `pypi`; `npm` skipped). Second consecutive **fresh tag** to publish to PyPI unaided, so the single shared publisher row is holding |
| PyPI indexes | PASS — all three names serve **0.2.4** (wheel + sdist). The CDN trap repeated exactly: `/pypi/<name>/0.2.4/json` answered 200 at once while `/pypi/<name>/json` still reported `0.2.3` in `info.version` |
| Byte-identity + sidecar | PASS — 11 release assets, `sha256sum -c sha256sums.txt` → 10/10 `OK`, and all six PyPI files carry the same sha256 as the release assets |
| `bash install.sh` at the tag | PASS — downloaded and checksum-verified the linux-amd64 archive; the installed `~/.local/bin/memorydb` reports `0.2.4`. Note the install dir is `$HOME/.local/bin`, not `.` - `./memorydb` after install.sh is a stale instinct |
| `go install github.com/humaniods/MemoryDB@v0.2.4` | PASS, with two things worth knowing: the binary it drops in `$GOPATH/bin` is named **`MemoryDB`** (module path, capitalised), not the `memorydb` the very next README block runs; and it compiled here on **Go 1.18.1** even though the README states 1.24+ is needed |
| ghcr | PASS — `:0.2.4` and `:latest` both report `0.2.4` on an anonymous re-pull. `docker run --rm <image> -version` fails ("Run 'docker run --help'") because the image defines only `CMD`, so the argument replaces the command - use `--entrypoint memorydb` |
| End-to-end from PyPI, the point of the release | PASS — clean venv, `pip install superb-memory==0.2.4`, `mdb serve` fetched `memorydb-0.2.4-linux-amd64.tar.gz` and sha256-checked it, `mdb health` reported `version 0.2.4`, and `avg(1,1,2)` returned raw JSON `"avg_v":1.3333333333333333`. The **released** binary is fixed, not just `main` |
| Other released surfaces | PASS — `memorydb-django==0.2.4` and `memorydb-langchain==0.2.4` import, MCP `initialize` → `{'name': 'memorydb', 'version': '0.2.4'}` with the 10 tools |
| Verification trap found on the way | Printing the response through Python's `json.load` turned `0.28571428571428571` into `0.2857142857142857` and `0.16666666666666667` into `0.16666666666666666`. That is float64's shortest repr in the *display*, not a server bug - compare raw bytes for anything digit-count related. Integers are safe: `json.loads` keeps `9007199254741053` an `int` |
| Tester's other findings, checked here | `{"fn":"count"}` → `400 unsupported aggregate count` (`aggFns`, `documents.go:646`); `docs/CONNECTING.md:81` really does show `import { MemoryDB } from 'memorydb'` while `sdk/node/package.json` names the package `superb-memory`, which npm has never published; a 3-dim query vector against 4-dim stored vectors really does return `{"results":[]}` with HTTP 200 |
| One tester claim that does not hold | `GET memories/?q=` and the same call without `q` were reported as two different response shapes. Measured on a live server: **both** return `{"results":[{"record":…,"score":…}]}` - without a query `score` is just `0`. The defect is in the *typing*, not the API: `sdk/node/index.d.ts:40` declares `list(): Promise<{ results: Record[] }>`, so TypeScript readers are told to expect bare records (`history` at `:42` genuinely does return records, so the file is not wrong everywhere) |

## The tester's rough edges, worked through (4 November 2026, unreleased)

Four findings survived the check against the code and were fixed on `main`; they are **not in
any published artifact yet**, so `pip install superb-memory==0.2.4` still has the old behaviour.

| Fix | What changed and how it was verified |
|---|---|
| Wrong-size embedding was silent | A `search/` vector whose length matches nothing stored returned `200 {"results":[]}` - reproduced first against a `main` build: a 4-dim query scored, 3-dim and 5-dim both answered 200 with an empty page. Cosine is undefined across lengths, so `Engine.CheckVectorDims` (`engine.go`) now rejects it with `400 query vector has 3 dimensions but the stored vectors have 4`, and the handler calls it before searching. The comparison list is scoped the same way the search itself is (`inScope` is now shared instead of duplicated, so documents/collections/jobs, expiry, visibility and kind/source/agent filters all agree), and the model filter is honoured: naming a model with no stored vectors is not a dimension problem, and an empty scope stays silent because there is no length to contradict |
| Tests at both levels | `TestSearchRejectsQueryVectorOfUnknownDimension` (engine, includes the "other user must not be rejected by vectors they cannot read" case) and `TestHTTPSearchRejectsWrongVectorDimension` (API boundary, the level the tester actually hit). The guard was then stubbed to `return nil` and the suite re-run to prove both tests really fail without it - the HTTP one reproduced the reported `200 {"results":[]}` exactly - before the condition was restored |
| Node import in the docs | `docs/CONNECTING.md` showed `import { MemoryDB } from 'memorydb'`, which cannot resolve: the package is named `superb-memory` and npm has never published it. The block now imports `./sdk/node/index.mjs` and says so plainly, matching what `README.md:180` already admitted. Ran the rewritten block verbatim against a live server: it creates a memory, searches, and the `for (const {record, score} of hits.results)` line added to the snippet works |
| Node typing for `memories.list` | `index.d.ts:40` now declares `{results: {record, score}[]}`, the shape `GET memories/` actually returns (measured with and without `q`, and through the Node client itself: `list shape record,score`) |
| Missing-field semantics undocumented | Verified operator by operator on a live server rather than reasoning from the switch: every operator except `exists` needs the field present (`ne`/`eq`/`gt`/`gte`/`lt`/`lte`/`in` all skipped the row with no `w`, and `contains`/`prefix`/`icontains`/`iequals` cannot match a nil value). `docs/API.md` now states that `ne` means "has this field, with a different value", not "not equal or absent" - SQL NULL semantics - and points at `{"op":"exists","value":false}` for the rows a comparison skips. Also re-confirmed the missing/null split: a declared `integer` field refuses null outright (`400 invalid type for field w`), while an undeclared field set to null counts as present, so `ne` matches it and `exists:false` does not |

`{"fn":"count"}` was filed as a gap and is not one: `aggFns` already answers
`400 unsupported aggregate count`, and `groups[].count` is documented two lines below where
the aggregate syntax is explained. `Search` gained no new behaviour for text queries - the
guard fires only when a vector is sent.

| Gate | Result |
|---|---|
| Local gates | PASS — `gofmt -l` clean, `go vet ./...` clean, `go test -race -count=1 -cover ./...` ok with coverage **74.1%** (was 73.6%), `python3 tests/smoke.py` PASS |
| Live behaviour sweep | 26 checks on a fresh journal: empty scope accepts a vector query, correct length still scores with the unchanged `{record, score}` shape, unknown model and text-only searches untouched, `kind:document` gets its own length expectations, and the seven operator cases above. 24 passed first time; the 2 failures were my harness using a `"null": true` schema option that `Field` (`documents.go:20`) does not have - re-run correctly, the server behaved as documented |
| Release state | Decided with the maintainer on 4 November 2026: this ships as **0.2.5**, a patch, with the 200→400 change called out in the new `CHANGELOG.md` rather than buried. The release notes on GitHub are static text produced by `release.yml`, which is why the changelog file exists - it is the only place a reader can see what a version changed. Verification of the published artifacts follows in the next section |

## v0.2.5 (4 November 2026) — what the published artifacts were checked against

`7a6ca74` is tagged `v0.2.5` (tag object `d2a1130`). Release run 41846090783: `server`,
`python-clients`, `publish`, `container` and `pypi` all green, `npm` skipped as designed - the
third consecutive fresh tag to publish to PyPI unaided. Everything below was measured on
**downloaded artifacts**, never on this working tree.

| Check | Result |
|---|---|
| Release assets | PASS — 11 assets, and `sha256sum -c sha256sums.txt` over every file the sidecar names → 10/10 `OK`. Wheels are byte-identical across the two channels that serve them: `superb_memory` `0f9bf551…`, `memorydb_django` `9dfe3a04…`, `memorydb_langchain` `be8112cb…` match PyPI's own recorded sha256, so the release and the index are the same bytes |
| PyPI | PASS — all three names serve **0.2.5** (wheel + sdist). Read through `/pypi/<name>/0.2.5/json`, not the project-level document, which the CDN still serves stale |
| `bash install.sh` at the tag | PASS — `https://raw.githubusercontent.com/…/v0.2.5/install.sh` is byte-identical to `git show v0.2.5:install.sh` (`951ff3d5…`), and running that verified copy into `MEMORYDB_INSTALL_DIR` fetched `memorydb-0.2.5-linux-amd64.tar.gz`, checked it, and installed a binary reporting `0.2.5`. The pipe form is refused in this environment, so the bytes were compared instead of piped - the same file either way |
| `go install …@v0.2.5` | PASS — compiles and prints `0.2.5`; the binary is still named **`MemoryDB`**, not the `memorydb` the README runs next |
| ghcr | PASS — `:0.2.5` and `:latest` resolve to the same image id (`8250b5d6…`) on an anonymous re-pull, the container's own `health/` says `0.2.5`, and **the new guard works inside the image**: a 5-dim row then a 2-dim query gives `400 query vector has 2 dimensions but the stored vectors have 5` |
| The behaviour change, on the released binary | PASS — empty scope still `200 {"results":[]}`; one 4-dim fact then a 3-dim query → `400 query vector has 3 dimensions but the stored vectors have 4`, exactly the string in `docs/API.md`; a 4-dim query → `200` with `["record","score"]` and score `0.01639344262295082`. So the 400 is in the artifact people install, not just on `main` |
| `avg` still fixed | PASS — on the same released binary: `avg(1,2,3)` → `2` as an integer, `avg(1,2)` → `1.5`, and a 3-row group summing to 2 → raw `"avg_amount":0.66666666666666667`. No regression from the 0.2.4 fix |
| The two documented semantics, on the released binary | PASS — `ne region=Rajasthan` matches only the one row that *has* `region` (total 1) while `exists:false` finds the three that lack it (total 3), and `{"fn":"count"}` → `400 unsupported aggregate count` |
| MCP surface, the agent-facing one | PASS — `initialize` → `serverInfo {'name': 'memorydb', 'version': '0.2.5'}` from the PyPI wheel, `memorydb_search` with a wrong-size vector returns `isError: true` with the same 400 text instead of an empty list, and health/text/list are unaffected |
| Every README *Everyday commands* line, in doc order | PASS 10/10 — `health`, `demo`, `collections ls`, `query --where region=Rajasthan --fields …`, `search "renewal" --limit 5`, `memory add`, `memory history --id <real id>`, `ingest --file examples/call.json`, `sync stripe … --file examples/customers.json`, `sources`, driven by the **0.2.5 wheel** against a server the same wheel started via `mdb serve` (which fetched `memorydb-0.2.5-linux-amd64.tar.gz`, checksummed it and reported `MemoryDB 0.2.5 listening`). Console `/` and `/studio/` both serve HTML from the released binary |
| Other released surfaces | PASS — `memorydb-django==0.2.5` cache `set`/`get`/`add`/`incr`/`delete` against the released server, `memorydb-langchain==0.2.5` history round-trip, and the Node block from `docs/CONNECTING.md` run verbatim: it prints `record id + score`, and `Object.keys(list().results[0])` is `record,score`, which is what `index.d.ts` now declares |

| Own harness errors, recorded because they looked like defects | Four, all mine, none of them the release's: (1) `MEMORYDB_URL=http://127.0.0.1:18099/api/v1` made **every** MCP tool return `route or method not found` - `memorydb.py:94` appends `/api/v1/` itself, so the value must be origin-only or a `memorydb://` string; (2) `POST memories/` needs a `kind` from `valid()` (`engine.go:242`) and `search/` accepts no `user` field, so my first payloads got 400s for the right reasons; (3) a 300 s `timeout` killed `mdb serve` mid-run, which produced six instant `Connection refused` in the sweep; (4) I called `MemoryDBChatMessageHistory(user=…)` - the constructor takes `session_id`. Also `curl -sSfL` on 11 assets hit a HTTP/2 stream error, so the assets were fetched individually |
| Observed, not a defect | `GET /studio` (no trailing slash) is `401 {"error":"valid bearer token required"}` because `main.go:111` serves exactly `/` and `/studio/`. Every URL the project prints or documents - README:80, QUICKSTART:39, `mdb serve`'s own banner at `cli.py:251` - uses the trailing slash, so nothing documented breaks; `install.sh:129` mentions only the console. A human typing the bare path sees JSON |

## Found while verifying 0.2.5, and not yet released

`pip show`-level verification of the LangChain integration turned up two references to APIs that
have never existed, in the wheel people actually install:

| Claim | Reality on 0.2.5 |
|---|---|
| `sdk/langchain/README.md:29` - `db.objects.history(pk=history.record_id)` | `MemoryDBChatMessageHistory` has no `record_id`; the copied example dies on `AttributeError`. The id is reachable only through the private `_find()`, which is exactly what `examples/langchain_demo.py` was doing |
| `sdk/langchain/memorydb_langchain/chat_history.py:4` - `db.messages.get(pk).history()` | there is no `db.messages` manager at all (`memorydb.py:97` gives `objects`, `collections`, `sources`), and `get()` returns a record dict that has no `.history()` |

Fixed locally, not committed: `record_id` is now a real property (`chat_history.py`, returns
`None` before the first message and after `clear()`), the docstring points at the manager that
exists, and the example uses the public property instead of reaching into `_find()`. Verified
against the **released 0.2.5 server** with the repo's SDK: `record: 46a00b5b…`, revision trail
`[1, 2]` oldest-first, README block runs verbatim, and after `clear()` → `None` and `[]`. Client
Python only, so the Go server, the binaries, the container and the other two wheels are unaffected
and none of it changes anything a working 0.2.5 install already does. Gates: `gofmt`/`go vet`/
`go test -race` unchanged at 74.1%, `python3 tests/smoke.py` PASS, `python -m build sdk/langchain`
produces `memorydb_langchain-0.2.5` fine - which is the trap: that wheel would publish over
nothing and still be version 0.2.5, so this needs its own number.

**Release state of 0.2.5: shipped and verified on all four channels.** The LangChain fix above is
waiting on the maintainer's choice of version.

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
