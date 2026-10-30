# MemoryDB 0.2

An original Go document database with AI-memory services, a browser console, and
Django-style Python and native Go clients. No existing database engine or either
reference memory system is embedded. Go runtime dependencies: standard library only.

**Status: experimental, single-node database.** Working storage and application
APIs, not a PostgreSQL/MongoDB replacement or full TencentDB/Hy-Memory parity.
The precise supported scope and remaining capabilities are in [FEATURES](docs/FEATURES.md).

## Install and start

Requires Linux or macOS, Go 1.24+ to build from source and Python 3.10+ for the Python
client. Tested on Linux with Go 1.27.3. **Windows has no native build**: the journal lock
is advisory `flock()`, which Go only exposes on Unix, and a `GOOS=windows` compile fails
on it (`undefined: lockPath`). On Windows use WSL or the Docker image.

**Fastest path — pip only, no clone and no Go toolchain:**

```bash
pip install https://github.com/humaniods/MemoryDB/releases/download/v0.2.1/superb_memory-0.2.1-py3-none-any.whl
mdb serve      # fetches the release binary, checks its sha256, runs it
```

Once the `superb-memory` name is live on PyPI the same two lines are
`pip install superb-memory` and `mdb serve`.

`mdb serve` keeps everything under `~/.memorydb` (binary cache, journal, token) and prints
the connection string before starting:

```
  connect: memorydb://TOKEN@127.0.0.1:8080
  console: http://127.0.0.1:8080   studio: http://127.0.0.1:8080/studio/
```

Point every client at it and go:

```bash
export MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080
mdb health
python3 -c "from memorydb import MemoryDB; print(MemoryDB().objects.search('fridays'))"
```

Use `--bin <path>`, `--from <archive>` or `--source <tree>` to run a binary you already
have, `--addr`/`--data`/`--worker` to place it, and flags after `--` reach the server
(`-- -tls-cert cert.pem -tls-key key.pem`). The archive is refused if its sha256 does not
match the release's `sha256sums.txt`.

Server-only install, without the Python package:

```bash
bash install.sh                    # newest release archive for this OS/CPU, sha256-checked
bash install.sh --source .         # no release yet? build from this tree instead
bash install.sh --from memorydb-0.2.1-linux-amd64.tar.gz
```

It installs to `~/.local/bin/memorydb` (override with `--prefix` or `MEMORYDB_INSTALL_DIR`)
and prints the exact start command with a freshly generated token. Release archives come
from the GitHub release for the tag; `--version`, `--dist-base` (including a `file://`
directory) pick a different one. `build-dist.sh` produces those archives plus
`sha256sums.txt` locally, which is what the release workflow runs.

An archive is installed only if it matches its checksum. With `--from`, a `sha256sums.txt`
next to the file is used, and an archive that file does not name is refused rather than
installed unchecked (`MEMORYDB_UNCHECKED=1` overrides if you know why).

The server is one static binary with no runtime dependencies, so any of these is a
complete install:

```bash
go install github.com/humaniods/MemoryDB@latest      # the module path is the repo
go build -trimpath -ldflags "-s -w" -o memorydb .    # or, from a clone
python3 manage.py runserver -worker                  # or, the familiar entry point
docker pull ghcr.io/humaniods/memorydb:latest        # or, the published image (amd64 + arm64)
docker build -t memorydb .                           # or, build that image from this tree
```

```bash
export MEMORYDB_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
./memorydb -data memory.wal -addr 127.0.0.1:8080 -worker
memorydb -version                                      # or ~/.local/bin/memorydb, from install.sh
```

Open http://127.0.0.1:8080, click **Connect database**, and use your token.
Collections, document CRUD, queries, transactions, ingestion and jobs are in the
console. `/studio/` provides memory browsing, editing, search and revision history.
Tokens stay in page memory. Restart with the same `-data` path to retain data.

Three distributions, one name on each index:

| Package | What it gives you |
|---|---|
| `superb-memory` | `from memorydb import MemoryDB`, the `mdb` CLI and the `memorydb-mcp` stdio server |
| `memorydb-django` | `CACHES` + `SESSION_ENGINE` backends |
| `memorydb-langchain` | `MemoryDBChatMessageHistory` |
| `sdk/node` (npm: `superb-memory`) | dependency-free Node 18+ client with types |

```bash
V=https://github.com/humaniods/MemoryDB/releases/download/v0.2.1
pip install $V/superb_memory-0.2.1-py3-none-any.whl    # client + mdb + memorydb-mcp
pip install $V/memorydb_django-0.2.1-py3-none-any.whl           # CACHES + SESSION_ENGINE
pip install $V/memorydb_langchain-0.2.1-py3-none-any.whl        # chat message history

pip install superb-memory memorydb-django memorydb-langchain   # once the PyPI names are live
```

The wheels come from the release itself, so they need no clone and no git; every one of
them is named in that release's `sha256sums.txt`. `pip install
"git+https://github.com/humaniods/MemoryDB.git#subdirectory=sdk"` is the equivalent while
you review a newer commit than the last tag, and that form installs the client first so the
Django/LangChain packages find their dependency already satisfied.

Verified end-to-end after cutting v0.2.0, from a virtualenv with no repository checkout on
the path: the three release wheels installed with plain `pip install <url>`; `mdb serve`
downloaded `memorydb-0.2.0-linux-amd64.tar.gz` from the release, checked it against
`sha256sums.txt`, generated a token and started the server; the Python client, the Django
cache and session backends through `SessionMiddleware`, `memorydb-langchain` history and the
MCP `tools/list` handshake all worked against that server; `install.sh` fetched and verified
the same archive standalone; `bash build-dist.sh` produced all four platform archives
(`darwin/arm64` needs the Go 1.24+ the workflow installs); and
`docker pull ghcr.io/humaniods/memorydb:0.2.0` succeeded anonymously, both architectures,
serving health and writes from the running image. Refusals were checked too: a tampered
archive (`sha256 mismatch`) and one absent from `sha256sums.txt` are both refused.

Published with v0.2.0: the server archives, the three wheels, `sha256sums.txt` and the
`ghcr.io/humaniods/memorydb` image. Pending on the package indexes: `pip install
superb-memory` by name, and `npm install superb-memory`. Both need a
one-time account setup — PyPI projects with Trusted Publishing for this repository plus the
`PUBLISH_PYPI=true` repository variable, and an `NPM_TOKEN` secret plus `PUBLISH_NPM=true` —
after which the release workflow's `pypi` and `npm` jobs do the upload on every tag.

## Clients and integrations

Every client speaks one connection string, like `mongodb://` and `redis://` do:

```
memorydb://TOKEN@127.0.0.1:8080          # plain HTTP (loopback/dev)
memorydbs://TOKEN@host:443               # TLS
```

| Interface | Package / entry point | Notes |
|---|---|---|
| Python | `pip install ./sdk` → `MemoryDB()` | reads `MEMORYDB_URL`; also `MemoryDB.from_url(...)` |
| CLI | same wheel → `mdb health / doc / query / search / memory / ingest / tx` | scripts, cron, CI |
| MCP server | same wheel → `memorydb-mcp` (stdio) | Claude Desktop, Qoder, IDE agents; see `examples/mcp_config.json` |
| Node / TypeScript | `sdk/node` (`import MemoryDB from './sdk/node/index.mjs'`) | dependency-free, Node 18+, ships `index.d.ts` |
| Go | `sdk/goclient` → `goclient.Connect("memorydb://…")` | stdlib only |
| LangChain | `pip install -e sdk/langchain` → `MemoryDBChatMessageHistory` | needs `langchain-core`; each append is an immutable revision |
| Django | `pip install ./sdk/django` → `memorydb_django.MemoryDBCache`, `memorydb_django.sessions` | two lines in `settings.py` replace the cache and session stores; TTLs use the engine's own record expiry |

See `docs/QUICKSTART.md` for the 60-second path, `docs/CONNECTING.md` for step-by-step
recipes to connect a website, frontend product, AI agent (MCP), LangChain or Django app, and
`examples/` for runnable demos
(`curl_demo.sh`, `python_quickstart.py`, `node_quickstart.mjs`, `langchain_demo.py`,
`django_demo.py`).

## Use from Django or another Python application

Cache and sessions are a `settings.py` drop-in, the way `sqlite3` is for Django itself:

```python
# pip install ./sdk ./sdk/django
CACHES = {"default": {
    "BACKEND": "memorydb_django.MemoryDBCache",
    "LOCATION": "memorydb://TOKEN@127.0.0.1:8080",
}}
SESSION_ENGINE = "memorydb_django.sessions"   # MEMORYDB_URL env var works too
```

`cache.set("hero", payload, timeout=30)` writes one MemoryDB record carrying that TTL as the
record's own `expires`, so the server stops serving it at the deadline - no sweep cron, and
`SESSION_COOKIE_AGE` is enforced the same way. `cache.add/get_or_set/touch/delete_many/clear`
and Django's `SessionStore` contract (`create/save/load/exists/delete/flush/cycle_key`) all
behave as documented, including `CreateError` on a key collision and optimistic retries when a
concurrent writer bumps the version. `examples/django_demo.py` runs the checks through Django's
own `SessionMiddleware`.

The Python client has a custom lazy query builder for your own collections. It is not a Django
database backend: Django models, migrations, SQL joins and Django Admin integration are
not implemented. Use it from your Django service/view. Any language can use the
HTTP API; Go helpers are in `sdk/goclient`.

```python
import os
from memorydb import MemoryDB

db = MemoryDB(token=os.environ['MEMORYDB_TOKEN'])
# Run collection creation once during application setup.
db.collections.create('customers', fields={
    'email': {'type': 'string', 'required': True},
    'age': {'type': 'integer'},
}, unique=['email'], indexes=['age'])
Customer = db.collection('customers')
row = Customer.objects.create(email='demo@example.com', age=24,
                              idempotency_key='customer-demo-1')
rows = Customer.objects.filter(age__gte=18).order_by('-age').only('email').all()
row = Customer.objects.update(pk=row['id'], version=row['version'], age=25)
Customer.objects.delete(pk=row['id'], version=row['version'])
```

## Supported operations

- JSON collections, optional typed schemas, required fields, unique constraints,
  equality indexes, filtering, deterministic sorting, projection, count and cursors.
- Atomic write batches (up to 256 changes), optimistic version checks, durable
  idempotency receipts, immutable revisions, logical deletion and expiry.
- Checksummed WAL, partial-tail recovery, process lock, backup/restore and compaction.
- Workspace-scoped tokens, reader/writer/admin roles, private/team/reader-list visibility.
- BM25, model-separated exact vector cosine and reciprocal rank fusion.
- Evidence-linked chat ingestion, optional LLM fact extraction, contextual fact
  replacement, extractive summaries, tool-trace experiences and durable jobs.
- Wiki paragraph ingestion, skill approval, scoped graph traversal, Go AST call
  graph and verified Unicode revision patches.

See [API](docs/API.md), [operations](docs/OPERATIONS.md),
[tests](docs/TEST_REPORT.md) and [research](docs/RESEARCH.md).

## Verify

```bash
python3 manage.py test       # Go race tests; requires a C compiler
python3 manage.py check      # go vet
python3 tests/smoke.py       # real server + Python client + recovery CLI
npm install --no-save playwright@1.62.1
npx playwright install chromium
node tests/ui.cjs            # real browser; development dependency only
```

## Storage and consistency contract

One process owns a WAL through a sidecar lock. A transaction is one length-framed
JSON event plus SHA256 checksum. The writer validates the complete batch, appends,
fsyncs, then publishes changes to readers. Readers observe a consistent committed
state while holding a read lock; the API does not expose long-lived read transactions.
A short final frame is truncated on restart. A complete corrupt frame fails startup.
An append/fsync error disables further writes until restart: the last write's
commit outcome may be unknown. Use stable idempotency keys for retryable creates
and exact transaction requests; do not blindly retry mutations.

Idempotency receipts are persistent and unbounded. HTTP PUT/PATCH/DELETE retries
can fail at version/readability checks before replaying a receipt; inspect the
record/history after uncertainty. They are not a universal exactly-once protocol.

All records/history and indexes reside in RAM. Indexes rebuild on every write;
this is not an LSM/B-tree engine. Text/vector search scans the authorized data.
Request bodies are capped at 4 MiB; transactions and compaction snapshots at
32 MiB. Compaction fails safely above its snapshot limit. No large-scale capacity,
latency SLA, distributed replication, ANN, SQL, joins, aggregation pipeline,
encryption at rest or point-in-time recovery is claimed.

Deletion/expiry is logical. History, receipts, snapshots and backups can retain
old content. This is **not physical erasure**. Collection schemas cannot expire.
Pagination rejects intervening writes or changes in matching active records;
restart the query after HTTP 409. No stable multi-request snapshot is promised.

The project has not undergone an independent security audit, production load
qualification or live-provider memory-quality evaluation. Passing tests cannot
guarantee all edge cases or zero bugs. Keep important data backed up and qualify
your workload before depending on this as its sole database.
