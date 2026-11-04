# Changelog

Behaviour changes go here because the GitHub Release notes are the fixed block that
`.github/workflows/release.yml` writes for every tag, so they cannot say what changed.
Evidence for each line lives in [docs/TEST_REPORT.md](docs/TEST_REPORT.md).

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
