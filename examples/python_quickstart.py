"""Python quickstart — pip install sanyam-memorydb-client, then MEMORYDB_URL=memorydb://TOKEN@host:8080 python3 examples/python_quickstart.py"""
import os
import sys
import uuid

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "sdk"))
from memorydb import MemoryDB

db = MemoryDB()  # reads MEMORYDB_URL; or MemoryDB.from_url("memorydb://TOKEN@127.0.0.1:8080")

print("health:", db.request("GET", "health/")["status"])

if not any(c["collection"] == "py_items" for c in db.collections.all()):
    db.collections.create("py_items", fields={"name": {"type": "string", "required": True},
                                              "price": {"type": "number"}}, unique=["name"])

items = db.collection("py_items")
name = f"mouse-{uuid.uuid4().hex[:8]}"   # name is unique, so re-runs must not reuse it
items.objects.create(data={"name": name, "price": 499})
cheap = items.objects.filter(price__lt=1000).order_by("-price").all()
print("cheapest-first rows:", len(cheap))

fact = db.objects.create(kind="fact", content="user prefers wired mice", source="python-demo")
hits = db.objects.search("mice", limit=5)
print("search hit kinds:", [h["record"]["kind"] for h in hits])

db.objects.delete(pk=fact["id"], version=fact["version"])
print("python quickstart OK")
