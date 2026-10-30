# MemoryDB Python client

`sanyam-memorydb-client` gives you the Python client, the `mdb` command-line tool and the
`memorydb-mcp` stdio server for [MemoryDB](https://github.com/humaniods/MemoryDB) — a single-binary
Go document database that also stores AI-agent memory (facts with evidence, summaries, tool
traces, hybrid BM25 + vector search).

## One line to a running database

```bash
pip install sanyam-memorydb-client
mdb serve                    # fetches the server binary, starts it on 127.0.0.1:8080
```

`mdb serve` prints the connection string; every client reads it from the environment:

```bash
export MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080
```

## Documents

```python
from memorydb import MemoryDB

db = MemoryDB()                       # reads MEMORYDB_URL
db.collections.create("customers",
                      fields={"email": {"type": "string", "required": True},
                              "age": {"type": "integer"}},
                      unique=["email"], indexes=["age"])

customers = db.collection("customers")
row = customers.objects.create(email="demo@example.com", age=24, idempotency_key="customer-demo-1")
customers.objects.filter(age__gte=18).order_by("-age").only("email").all()
customers.objects.increment(pk=row["id"], views=1)     # atomic counter, no version needed
```

Django-style lookups: `eq ne gt gte lt lte in contains icontains prefix iequals exists`,
plus `or_q`, `Sum/Avg/Min/Max` with `group_by`, cursor pagination and optimistic `version` checks.

## Agent memory

```python
db.ingest([{"role": "user", "content": "I prefer terse answers in Hindi"}],
          facts=[{"subject": "user", "predicate": "answer_style", "value": "terse Hindi",
                  "quote": "terse answers in Hindi", "message_index": 0, "confidence": 0.95}])
db.objects.search("terse")
```

Facts need an exact quote from a user message, so a stored memory always carries its evidence.

## CLI and MCP

```bash
mdb health; mdb search "fridays"; mdb memory add --content "user ships on fridays"
memorydb-mcp          # stdio MCP server for Claude Desktop, Qoder, IDE agents
```

Companion packages: `memorydb-django` (cache + session backends) and `memorydb-langchain`
(chat message history). Docs: `docs/QUICKSTART.md` and `docs/CONNECTING.md` in the repository.
