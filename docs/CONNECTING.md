# Connecting an application or product to MemoryDB

Step-by-step recipes for the ways a developer consumes a database:
server-side SDK, browser frontend, AI agent (MCP), framework memory (LangChain),
a Django drop-in that replaces the cache and session stores, and a connector that mirrors
a table you already own.

Every client speaks one connection string:

```
memorydb://TOKEN@127.0.0.1:8080     plain HTTP (loopback / private network)
memorydbs://TOKEN@host:443           TLS
```

---

## A. Website or product with a backend (recommended)

### 1. Run MemoryDB near your app

Nothing on this machine but pip — `mdb serve` downloads the release binary, checks it against
the release's `sha256sums.txt`, and runs it:

```bash
pip install superb-memory
mdb serve            # data in ~/.memorydb/data, token in ~/.memorydb/token (0600)
```

Same server/VPS — keep the database on loopback only:

```bash
docker run -d --name memorydb -p 127.0.0.1:8080:8080 \
  -e MEMORYDB_TOKEN="$(openssl rand -base64 32)" \
  -v memorydb-data:/data ghcr.io/humaniods/memorydb:latest
```

Separate machine — the server refuses to start on a public address without TLS:

```bash
memorydb -addr 0.0.0.0:8080 -data memory.wal \
  -tls-cert fullchain.pem -tls-key privkey.pem -worker
```

### 2. Give each product its own principal (never share the admin token)

Write `principals.json`; each entry is one application identity:

```json
[
  {"token_sha256": "<sha256 hex of the app token>", "user": "mywebsite", "workspace": "prod", "role": "writer"},
  {"token_sha256": "<sha256 hex of the analytics token>", "user": "dashboard", "workspace": "prod", "role": "reader"}
]
```

```bash
python3 -c "import hashlib; print(hashlib.sha256(b'my-app-token-here-1234').hexdigest())"
memorydb -auth principals.json -addr 127.0.0.1:8080 -data memory.wal
```

What this buys you per application:

| Role | Can do |
|---|---|
| `reader` | GET + search/query/graph POSTs only |
| `writer` | full CRUD inside its own workspace; owns its records |
| `admin` | also create collections/schemas and edit team-visible records |

Different `workspace` values share one process and journal but cannot read each other's data.

### 3. Point the app at the database with one env var

```bash
MEMORYDB_URL=memorydb://my-app-token-here-1234@127.0.0.1:8080
```

### 4. Call it from application code

Node (dependency-free client, Node 18+):

```js
// The client is one dependency-free file; import it from a clone or copy it into your app.
// It is not on npm yet, so there is no package name to install against.
import { MemoryDB } from './sdk/node/index.mjs';
const db = new MemoryDB(process.env.MEMORYDB_URL);

await db.memories.create({ kind: 'fact', content: 'user prefers dark mode', source: 'web-app' });
const hits = await db.search({ text: 'dark mode', limit: 5 });
for (const { record, score } of hits.results) console.log(record.id, score);
```

Python:

```python
from memorydb import MemoryDB
db = MemoryDB()  # reads MEMORYDB_URL

db.collections.create("users", fields={"email": {"type": "string", "required": True}})
db.collection("users").objects.create(data={"email": "u@x.com"}, idempotency_key="signup-8812")
recent = db.collection("users").objects.filter(email__prefix="u@").all()
```

Go:

```go
client, err := goclient.Connect(os.Getenv("MEMORYDB_URL"))
_, err = client.Create(ctx, "users", map[string]any{"email": "u@x.com"}, "signup-8812")
```

### 5. Optional: watch it in the UI

Open `http://127.0.0.1:8080/` (database console) and `/studio/` (memory explorer)
and connect with the admin token — records your app wrote appear immediately.

---

## B. Frontend-only product (React/Vue/mobile web, no backend)

Direct browser → MemoryDB calls do **not** work today, by design:

1. The server sends no CORS headers, so the browser blocks cross-origin `fetch`.
2. A token inside browser JavaScript is public — anyone can extract it and write to your database.

Supported patterns, in order of preference:

- **Thin proxy (recommended).** One serverless function or route that holds the token
  server-side and forwards a whitelisted subset:

  ```js
  // e.g. an Express/Vercel route: browser -> /api/db/search -> MemoryDB
  app.post('/api/db/search', async (req, res) => {
    const db = new MemoryDB(process.env.MEMORYDB_URL);   // token stays server-side
    res.json(await db.search({ text: String(req.body.text ?? ''), limit: 10 }));
  });
  ```

- **CORS + scoped read-only principal.** If you must call the API from the browser,
  add CORS support to the server (open feature request) and issue a `reader`-role
  token limited to one workspace. Accept that the token is visible to every visitor;
  treat it as a public API key, never as a secret.

Never expose an `admin` token to a frontend under any pattern.

---

## C. AI agent or IDE product (MCP)

Any MCP-capable client (Claude Desktop, Qoder, IDE assistants) connects with one config
block — no code at all:

```json
{
  "mcpServers": {
    "memorydb": {
      "command": "memorydb-mcp",
      "env": { "MEMORYDB_URL": "memorydb://TOKEN@127.0.0.1:8080" }
    }
  }
}
```

`pip install superb-memory` puts `memorydb-mcp` on PATH; from a clone use
`"command": "python3", "args": ["<path>/MemoryDB/sdk/memorydb_mcp.py"]`.

Tools the agent gets: `memorydb_health`, `memorydb_search`, `memorydb_list_memories`,
`memorydb_add_memory`, `memorydb_ingest` (evidence-linked facts),
`memorydb_create_collection`, `memorydb_query_documents`, `memorydb_sync`,
`memorydb_list_sources`, `memorydb_demo`.

---

## D. LangChain / agent-framework product

```python
from memorydb import MemoryDB
from memorydb_langchain import MemoryDBChatMessageHistory  # pip install memorydb-langchain

history = MemoryDBChatMessageHistory(session_id="user-42", client=MemoryDB())
history.add_message(HumanMessage("What is the capital of Rajasthan?"))
```

Each session is one record; every append is a new immutable revision in the WAL, so
`db.objects.history(pk=...)` doubles as your audit log. Use one principal per deployed
product so sessions stay in separate workspaces.

---

## E. Django project (cache + sessions, SQLite-style drop-in)

If your site already runs on Django, this is the shortest path in: two `settings.py` keys, no
models, no migrations, no new code in your views.

```bash
pip install memorydb-django              # from a clone: pip install ./sdk ./sdk/django
```

```python
# settings.py
CACHES = {"default": {
    "BACKEND": "memorydb_django.MemoryDBCache",
    "LOCATION": os.environ["MEMORYDB_URL"],   # memorydb://TOKEN@127.0.0.1:8080
    "OPTIONS": {"collection": "django_cache"}, # optional; default shown
}}

SESSION_ENGINE = "memorydb_django.sessions"
# optional: MEMORYDB_URL / MEMORYDB_SESSIONS_COLLECTION settings, default collection
# is django_sessions
```

Both backends create their collection on first use (typed `*_key` field, unique + indexed), so
there is no setup step, and every `cache.set` / `session.save` writes a single MemoryDB record
whose `expires` carries the deadline. The engine stops serving expired records at read time, so
`timeout=` and `SESSION_COOKIE_AGE` need no cleanup job, and a cache key and a session row are
queryable like any other document (`mdb query django_cache`).

What that buys you over the stock database-backed stores:

- the same store your application data and agent memory already live in;
- optimistic concurrency instead of row locks - concurrent writers retry, they never deadlock;
- immutable revisions, so `/studio/` can show how a session or value changed over time.

`examples/django_demo.py` runs both backends against a live server through Django's
`SessionMiddleware` and asserts expiry, `add()`, `touch()`, `clear()` and `CreateError`.

Not covered, by design: this is not `django.db.backends`. Models, migrations, the admin,
queries via the ORM and `manage.py` still need Postgres/SQLite - MemoryDB speaks JSON documents,
not SQL.

---

## F. Mirroring a table you already own (connector)

If Postgres, a CSV on S3, or a REST list is the source of truth and you want it searchable and
graph-linked here, push it under a connector name. MemoryDB remembers one SHA-256 per row, so a
nightly re-sync writes only the rows whose data actually moved - an identical re-run writes
nothing and does not even advance the journal.

```bash
mdb sync nightly-postgres --collection customers --key-field id --file customers.json
mdb sources                       # which connectors own rows here, and when they last synced
```

The same call from Python, with `delete_missing` tombstoning rows the source dropped:

```python
from memorydb import MemoryDB
db = MemoryDB()                                    # MEMORYDB_URL
report = db.sources.sync("nightly-postgres", collection="customers", key_field="id",
                         items=[{"data": row} for row in rows], delete_missing=True)
# {'created': 2, 'updated': 1, 'unchanged': 597, 'removed': 0, 'written': 3, 'sequence': 41}
```

Raw HTTP, for a cron job or a CI step that has no SDK:

```bash
curl -s -X POST -H "Authorization: Bearer $MEMORYDB_TOKEN" -H "Content-Type: application/json" \
  -d '{"collection":"customers","key_field":"id","items":[{"data":{"id":"c-1","mrr":48000}}]}' \
  "$MEMORYDB_URL/api/v1/sources/nightly-postgres/sync/"
```

What each mirrored row carries, and what the console prints on it:

- `metadata.connector`, `metadata.connector_key`, `metadata.source_hash`, `metadata.synced_at`;
- `source: "sync:<connector>"` and `layer: "mirror"`, so `mdb search` and the graph can tell a
  mirrored row from a memory your agent wrote;
- `content` rendered from the row (`id: c-1 | mrr: 48000 | …`), which is what BM25 indexes - pass
  an explicit `content` per item to control that text.

The guardrails are the point of the design. A key that a different connector - or a direct write -
already owns is refused rather than taken over. Inside a connector the comparison is against the
payload this connector last sent, not against the row's current values, so a hand edit survives a
sync of an unchanged source and is replaced by the source's new value as soon as that row changes
upstream - and neither edit is ever deleted, every step is a revision under
`memories/<id>/history/`. A key repeated inside one payload is refused, and a reader token cannot
sync at all. One request carries at most 500 rows with keys up to 128 characters
inside a 4 MiB body, and `delete_missing` scans up to 20000 existing rows, so page large sources
yourself (read the source, sync a slice, repeat with the same connector and no `delete_missing`
until the final slice).

This is polling-and-hash mirroring, not change-data-capture: there is no hosted service that
watches Stripe or Postgres for you, and nothing arrives between your runs. Run the extractor
yourself - a scheduled job, GitHub Action or `cron` that reads the source and calls `mdb sync`.

`mdb demo` is the same code path with built-in data: four mirrored tables, notes, and
evidence-linked facts, seeded once and left alone on the next run, so a fresh install has
something to click in the console and search before your own data arrives.

---

## Production checklist

- [ ] TLS (or `-allow-insecure-network` behind a trusted reverse proxy only) for any non-loopback bind
- [ ] One principal per application; least-privilege roles; rotate by editing `principals.json`
- [ ] `Idempotency-Key` header on every retried write — replays return the original receipt instead of duplicating
- [ ] Backups: `memorydb -backup file` (checksummed snapshot), restore with `-restore`; test both before launch
- [ ] Compaction: `memorydb -compact N` periodically — deletes are logical, the journal grows until compacted
- [ ] Know the limits: 4 MiB requests, 32 MiB transactions, in-memory indexes rebuilt per write — keep datasets small
- [ ] Not supported yet: clustering/replication, SQL, joins, disk encryption, point-in-time recovery; the engine is unaudited — do not store regulated data
