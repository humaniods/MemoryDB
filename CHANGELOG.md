# Changelog

Behaviour changes go here because the GitHub Release notes are the fixed block that
`.github/workflows/release.yml` writes for every tag, so they cannot say what changed.
Evidence for each line lives in [docs/TEST_REPORT.md](docs/TEST_REPORT.md).

## 0.2.8 — 6 November 2026

- Faster: a write no longer costs the size of the database. Every batch used to copy the whole
  record map, re-validate every document of every collection and rebuild every equality index
  before it could commit, so a single create took 27 ms at 20,000 records and grew linearly. A
  batch is now applied in place under the write lock and undone if validation or the journal
  write fails; it validates only the documents it wrote, against a cached schema and an index of
  unique-key holders, and updates only those rows' index entries. A batch that writes a
  collection still re-checks and rebuilds everything, as before. Over HTTP with real fsync on the
  same machine, a create at 20,000 records went from 26.9 to 1.7 ms `p50` and now stays flat as
  the corpus grows.
- Faster: search tokenizes each record once, when it is written, instead of on every query, counts
  document frequency only for the query's own terms, and ranks on positions so only the returned
  page is deep-copied - it used to JSON-round-trip every match before applying `limit`. Over HTTP
  at 20,000 records, text search went from 726 to 36 ms `p50` and hybrid search from 1,143 to
  84 ms. Results are identical: 300 random queries returned the same hits, order and scores from
  the old and new code. Search is still an exact scan, so it still grows linearly with the corpus.
- Fixed: `-compact` failed with `transaction/snapshot exceeds 32 MiB` once the database outgrew
  one journal frame - 40,000 memories with 64-dimension vectors (a 65 MB journal) were enough,
  so a growing database could no longer be compacted. A snapshot is now a run of frames of at most
  8 MiB at the head of the journal. A binary from before this change refuses such a journal with
  `snapshot must be first frame` instead of reading part of it, so do not downgrade after
  compacting.
- New: online backups. Start the server with `-backup-dir DIR`; `POST /api/v1/admin/backup/` or
  `mdb backup` writes `memorydb-<time>-seq<N>.bak` and its `.sha256` while the server keeps
  serving. Before, a backup meant stopping the server.
- New: `GET /metrics` (admin) in the Prometheus text format - journal sequence and bytes, live
  records by kind, revisions, receipts, the poisoned flag, and per-route request counts and
  latency histograms. `mdb metrics` prints it.
- New: `-rate-limit N` and `-rate-burst B` give each principal a token bucket and answer `429`
  with `Retry-After` when it is empty. Off by default; `/metrics` scrapes are never limited.
- New: physical purge. `POST /api/v1/admin/purge/ {"ids": [...]}` or `mdb purge ID…` erases every
  revision of records that are already deleted, and their copies inside idempotency receipts, by
  rewriting the journal. Retrying the original request with its `Idempotency-Key` now answers
  `404` instead of bringing the data back. Backups taken earlier are not touched.
- New: `bench_test.go` (`go test -run '^$' -bench .`) measures write and search cost at 2,000,
  10,000 and 20,000 records, and `indexes_test.go` checks that the incremental index state always
  equals a full rebuild, plus unique swaps within one batch, expired and deleted unique holders,
  and rollback after a rejected batch or a failed journal write.

## 0.2.7 — 6 November 2026

- Fixed: the TypeScript declarations in the npm package did not compile. `sdk/node/index.d.ts`
  declared `export interface Record`, which shadows TypeScript's own global `Record<KeyType,
  ValueType>` for the rest of that file, so its three `Record<string, …>` annotations were read as
  the local interface given two type arguments it does not take. Installing `superb-memory@0.2.6`
  and running `tsc` on a file that only says `import { MemoryDB } from 'superb-memory'` fails with
  three `TS2315: Type 'Record' is not generic` errors - `--strict` on or off, the errors are in the
  shipped file, not in the caller's code. The record interface is now `MemoryDBRecord`, which frees
  the global name, and the map annotations are back to plain `Record<string, unknown>`. Nothing else
  in the published surface keeps the old name, and it could never have been used: no reader of
  `index.d.ts` has ever had a compiling build that imported `Record` from this package. No API, no
  wire format and no runtime behaviour changes; `index.mjs` is untouched, and `node test.mjs` passes
  against a live server.
- Fixed: `sdk/node` shipped a `npm test` script pointing at `test.mjs`, but `files` omitted that
  file, so the command failed with a module-not-found error inside an installed copy. `test.mjs` is
  in the tarball now.
- Documented: `sdk/django/README.md` showed `CACHES[...]["LOCATION"]` next to `SESSION_ENGINE`
  without saying the session backend never reads `CACHES`. It takes its connection from the
  `MEMORYDB_URL` Django setting, or the `MEMORYDB_URL` environment variable, and if neither is set
  it raises `ValueError: pass a url with token=, a memorydb:// connection string, or set
  MEMORYDB_URL` on the first session read rather than at startup. The settings snippet now carries
  that line and the gotcha is spelled out.
- New: the README and `docs/OPERATIONS.md` carry measured latency and throughput numbers for the
  search path instead of only the caveat that it is an exact scan. Measured on the 0.2.6/0.2.7
  engine, single node, loopback: text search p50 grows roughly linearly with the corpus
  (~47 µs per stored record), and a lone writer's latency grows with it too, because every write
  re-derives the BM25 statistics over the whole dataset under one mutex.
- No server behaviour change: no status code, response shape or query semantic moves in this
  release. Everything above is the npm package's type declarations, one packaging field, the
  Django README and the performance documentation.

## 0.2.6 — 5 November 2026

- **npm:** `superb-memory` is now a real npm package. `npm install superb-memory` then
  `import { MemoryDB } from 'superb-memory'` works - verified against the registry before this
  release was cut. The first version (0.2.5) was published by hand, because npm lets you register
  a trusted publisher only on a package that already exists; from 0.2.6 the release workflow
  publishes it itself over OIDC with provenance, and the repository stores no npm credential.
- Fixed: `sdk/langchain/README.md` told readers to call `history.record_id`, an attribute the
  class never had, so the copied example died on `AttributeError`. It is a real property now,
  `None` before the first message and after `clear()`.
- Fixed: `chat_history.py`'s own docstring pointed at `db.messages.get(pk).history()`. There is no
  `db.messages` manager - the client exposes `objects`, `collections` and `sources` - and `get()`
  returns a record, which has no `.history()`. It points at `db.objects.history(pk=…)` now, which
  is what the README's own example had always used correctly.
- Changed: `docs/CONNECTING.md` and `README.md` stop telling Node readers to copy a file out of a
  clone and use the package name instead.
- No server change at all: no HTTP status, no response shape and no query semantic moves in this
  release. Everything above is client Python, client docs, `sdk/node` packaging and the workflow.

## 0.2.5 — 4 November 2026

- **Behaviour change:** `POST search/` answers `400 query vector has N dimensions but the
  stored vectors have M` when the query vector's length matches nothing stored in the
  caller's scope. It used to answer `200 {"results": []}`, so a wrong-size embedding failed
  silently - the same class of defect as the 0.2.4 `avg` truncation. A scope holding no
  vectors, or only vectors of another `embedding_model` than the one the query names, still
  answers normally: there is no length there to contradict.
- Fixed: `docs/CONNECTING.md` told Node readers to `import { MemoryDB } from 'memorydb'`.
  The client is `sdk/node`, its package name is `superb-memory`, and npm has never published
  it, so that import could not resolve from any registry. The snippet now imports
  `./sdk/node/index.mjs` and says why.
- Fixed: `sdk/node/index.d.ts` declared `memories.list()` as returning bare records.
  `GET memories/` returns `{record, score}` hits with or without `q` - the typing now matches
  what the server sends. (`memories.history()` genuinely does return records; that stayed.)
- Documented: every operator except `exists` requires the field to be present, so `ne` means
  "has this field, with a different value", not "different or absent" - SQL NULL semantics.
  `{"op":"exists","value":false}` finds the rows a comparison skips.
- Documented: a Search section in `docs/API.md` for the hit shape, the dimension rule and
  embedding-model separation.
- Not a change: `{"fn":"count"}` still returns `400 unsupported aggregate count`; counts come
  from `groups[].count`, which is documented where aggregation is explained.

## 0.2.4 — 4 November 2026

- Fixed: `avg` cut every repeating decimal to the places its denominator's 2s and 5s needed,
  so `avg(1,1,2)` answered `1` and `41597/770` answered `54.0` while `docs/API.md` promised 17
  significant digits. Finite decimals print exactly as before. Found by an outside tester who
  cloned the repo and tested `v0.2.3`.

## 0.2.3 — 4 November 2026

- Fixed: 0.2.2's wheels were tagged one commit before their own documentation, so the README
  readers saw described behaviour the released wheel did not have. At this tag
  `examples/call.json` and `examples/customers.json` exist, `mdb ingest --file <wrong path>`
  prints `mdb: cannot read …` instead of a Python traceback, and `mdb ingest` keeps the
  `context` field its payload documents instead of dropping it.

## 0.2.2 — 3 November 2026

- Added: connector mirroring - `POST sources/NAME/sync/`, `GET sources/`, `mdb sync`,
  `mdb sources`, `mdb demo`, `db.sources.sync` in Python, the `memorydb_sync`,
  `memorydb_list_sources` and `memorydb_demo` MCP tools, and a provenance line in the console.
  Push-based, hash-per-row: you run the extractor, only changed rows are written, and an
  unchanged re-sync moves nothing.
- Added: README rewritten as a showcase with screenshots of the running console and Studio.

## 0.2.1 — 30 October 2026

- Renamed: the Python distribution is now `superb-memory` (was `sanyam-memorydb-client`;
  `memorydb` and `mdb` were already taken on PyPI). Import names, the `mdb` CLI,
  `memorydb-mcp` and the Go module path are unchanged.
- Published: `superb-memory`, `memorydb-django` and `memorydb-langchain` on PyPI, uploaded by
  CI through Trusted Publishing, so no index credential lives in the repository.
- Published: a public multi-arch container image at `ghcr.io/humaniods/memorydb`.

## 0.2.0 — 30 October 2026

- First public release: one static `CGO_ENABLED=0` binary for linux/darwin on amd64/arm64,
  the wheels and sdists for all three Python distributions, `sha256sums.txt`, and
  `install.sh`. The engine, the browser console, memory Studio, the `mdb` CLI, the MCP
  server, the Go and Node clients, the LangChain wrapper and the Django cache/session
  backends all shipped here.
