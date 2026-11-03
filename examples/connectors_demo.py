#!/usr/bin/env python3
"""Mirror a table twice and prove the second sync writes nothing new.

Run against any server:  python3 examples/connectors_demo.py
Needs MEMORYDB_URL=memorydb://TOKEN@host:port (an admin token creates the collection).
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdk"))
from memorydb import MemoryDB, MemoryDBError  # noqa: E402

COLLECTION = "connector_demo"
ROWS = [
    {"id": "c-1", "name": "Rani Textiles", "region": "Tamil Nadu", "mrr": 48000},
    {"id": "c-2", "name": "Deccan Logistics", "region": "Telangana", "mrr": 91000},
    {"id": "c-3", "name": "Kaaveri Dairy", "region": "Karnataka", "mrr": 4200},
]

db = MemoryDB()
schema = {"fields": {"id": {"type": "string", "required": True}, "name": {"type": "string"},
                     "region": {"type": "string"}, "mrr": {"type": "number"}},
          "unique": ["id"], "indexes": ["region"]}
try:
    db.collections.create(COLLECTION, **schema)
except MemoryDBError as exc:
    if exc.status != 409:
        raise
    print("collection already present; dropping its rows first is left to you")

before = db.request("GET", "health/")["sequence"]
print("first sync ", db.sources.sync("acme-crm", collection=COLLECTION, items=ROWS))
after = db.request("GET", "health/")["sequence"]

# Same rows again: nothing moved, so nothing is written and the journal does not advance.
print("resync     ", db.sources.sync("acme-crm", collection=COLLECTION, items=ROWS))
if db.request("GET", "health/")["sequence"] != after:
    raise SystemExit("an unchanged resync still wrote the journal")

# One changed value, one row dropped by the source.
changed = [dict(ROWS[0], mrr=52000), ROWS[1]]
print("changed    ", db.sources.sync("acme-crm", collection=COLLECTION, items=changed, delete_missing=True))

customer = db.collection(COLLECTION).objects.filter(id="c-1").all()[0]
print("row keeps its provenance:", {k: customer["metadata"][k] for k in ("connector", "connector_key", "source_hash")})
history = db.request("GET", "memories/%s/history/" % customer["id"])["results"]
print("revisions of c-1:", [(h["version"], h["data"]["mrr"]) for h in history])
print("connectors:", db.sources.all())
print("search     :", [hit["record"]["content"] for hit in db.objects.search("Rani", kind="document")])
print("sequence moved by", db.request("GET", "health/")["sequence"] - before, "transactions")
