# MemoryDB 0.2

An original Go document database with AI-memory services, a browser console, and
Django-style Python and native Go clients. No existing database engine or either
reference memory system is embedded. Go runtime dependencies: standard library only.

**Status: experimental, single-node database.** Working storage and application
APIs, not a PostgreSQL/MongoDB replacement or full TencentDB/Hy-Memory parity.
The precise supported scope and remaining capabilities are in [FEATURES](docs/FEATURES.md).

## Start

Requires Linux, Go 1.24+ and Python 3.10+ for the Python client. Tested on Linux
with Go 1.27.3. Windows native builds are unsupported; use Linux/WSL.

```bash
export MEMORYDB_TOKEN="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
python3 manage.py runserver -worker
```

Open http://127.0.0.1:8080, click **Connect database**, and use your token.
Collections, document CRUD, queries, transactions, ingestion and jobs are in the
console. `/studio/` provides memory browsing, editing, search and revision history.
Tokens stay in page memory. Restart with the same `-data` path to retain data.

```bash
go build -o memorydb .
./memorydb -data memory.wal -addr 127.0.0.1:8080 -worker
pip install ./sdk
```

## Use from Django or another Python application

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

The Python client has a custom lazy query builder. It is not a Django database
backend: Django models, migrations, SQL joins and Django Admin integration are
not implemented. Use it from your Django service/view. Any language can use the
HTTP API; Go helpers are in `sdk/goclient`.

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
