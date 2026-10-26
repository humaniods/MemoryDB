"""Django-style, dependency-free HTTP client for the original Go engine.
This is not a Django ORM backend: custom QuerySet support, but no SQL, migrations or joins.
"""
import json
from urllib.error import HTTPError
from urllib.parse import urlencode, quote
from urllib.request import Request, urlopen

class MemoryDBError(Exception):
    def __init__(self, status, message):
        self.status = status
        super().__init__(message)

class MemoryManager:
    def __init__(self, client):
        self.client = client

    def create(self, *, kind="fact", content, **fields):
        return self.client.request("POST", "memories/", {"kind": kind, "content": content, **fields})

    def filter(self, *, kind="", source="", q="", limit=50):
        return self.client.request("GET", "memories/?" + urlencode(dict(kind=kind, source=source, q=q, limit=limit)))["results"]

    def get(self, *, pk):
        return self.client.request("GET", "memories/" + quote(pk, safe="") + "/")

    def update(self, *, pk, version, **fields):
        record = self.get(pk=pk)
        if record["version"] != version:
            raise MemoryDBError(409, "version conflict; reload the record")
        record.update(fields)
        record["version"] = version
        return self.client.request("PUT", "memories/" + quote(pk, safe="") + "/", record)

    def delete(self, *, pk, version):
        return self.client.request("DELETE", "memories/" + quote(pk, safe="") + "/", headers={"If-Match": str(version)})

    def history(self, *, pk):
        return self.client.request("GET", "memories/" + quote(pk, safe="") + "/history/")["results"]

    def search(self, text="", *, vector=None, kind="", source="", embedding_model="", agent="", limit=10):
        return self.client.request("POST", "search/", dict(text=text, vector=vector or [], kind=kind, source=source, embedding_model=embedding_model, agent=agent, limit=limit))["results"]

class MemoryDB:
    def __init__(self, url="http://127.0.0.1:8080", *, token, timeout=35):
        self.url = url.rstrip("/") + "/api/v1/"
        self.token = token
        self.timeout = timeout
        self.objects = MemoryManager(self)
        self.collections = CollectionManager(self)

    def collection(self, name):
        return Collection(self, name)

    def transaction(self, changes, *, idempotency_key=None):
        headers = {"Idempotency-Key": idempotency_key} if idempotency_key else None
        return self.request("POST", "transactions/", {"changes": changes}, headers=headers)["results"]

    def ingest(self, messages, *, facts=None, extract=False, source="python", context="", tools=None):
        return self.request("POST", "ingest/", {"messages": messages, "facts": facts or [], "extract": extract,
                            "source": source, "context": context, "tools": tools or []})["results"]

    def queue_ingest(self, **payload):
        return self.request("POST", "jobs/", payload)

    def request(self, method, path, body=None, headers=None):
        req = Request(self.url + path, data=json.dumps(body).encode() if body is not None else None,
                      headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json", **(headers or {})}, method=method)
        try:
            with urlopen(req, timeout=self.timeout) as response:
                return json.load(response)
        except HTTPError as exc:
            try:
                message = json.load(exc).get("error", str(exc))
            except (ValueError, AttributeError):
                message = str(exc)
            raise MemoryDBError(exc.code, message) from exc

class QuerySet:
    """Lazy immutable query builder. Pagination rejects concurrent DB changes."""
    def __init__(self, manager, query=None):
        self.manager = manager
        self.query = dict(query or {"where": [], "limit": 100})

    def filter(self, **lookups):
        predicates = list(self.query.get("where", []))
        supported = {"eq", "ne", "gt", "gte", "lt", "lte", "in", "contains", "prefix", "exists"}
        for key, value in lookups.items():
            parts = key.split("__")
            op = parts.pop() if parts[-1] in supported else "eq"
            predicates.append({"field": ".".join(parts), "op": op, "value": value})
        return QuerySet(self.manager, {**self.query, "where": predicates})

    def order_by(self, field):
        return QuerySet(self.manager, {**self.query, "order_by": field.lstrip("-"), "descending": field.startswith("-")})

    def only(self, *fields):
        return QuerySet(self.manager, {**self.query, "projection": list(fields)})

    def page(self, cursor=None, limit=None):
        query = dict(self.query)
        if cursor:
            query["cursor"] = cursor
        if limit is not None:
            query["limit"] = limit
        return self.manager.client.request("POST", "query/" + self.manager.name + "/", query)

    def count(self):
        return self.page(limit=1)["total"]

    def first(self):
        results = self.page(limit=1)["results"]
        return results[0] if results else None

    def __iter__(self):
        cursor = None
        while True:
            page = self.page(cursor=cursor)
            yield from page["results"]
            cursor = page.get("next_cursor")
            if not cursor:
                break

    def all(self):
        return list(self)

class DocumentManager:
    def __init__(self, client, name):
        import re
        if not re.fullmatch(r"[A-Za-z][A-Za-z0-9_-]{0,63}", name):
            raise ValueError("Invalid collection name")
        self.client, self.name = client, name
        self.path = "documents/" + name + "/"

    def create(self, *, data=None, idempotency_key=None, **fields):
        if data is not None and fields:
            raise ValueError("Pass data or keyword fields, not both")
        return self.client.request("POST", self.path, {"data": fields if data is None else data},
                                   headers={"Idempotency-Key": idempotency_key} if idempotency_key else None)

    def get(self, *, pk):
        return self.client.request("GET", self.path + quote(pk, safe="") + "/")

    def filter(self, **lookups):
        return QuerySet(self).filter(**lookups)

    def all(self):
        return QuerySet(self)

    def update(self, *, pk, version, unset=None, **fields):
        return self.client.request("PATCH", self.path + quote(pk, safe="") + "/",
                                   {"version": version, "set": fields, "unset": unset or []})

    def delete(self, *, pk, version):
        return self.client.request("DELETE", self.path + quote(pk, safe="") + "/", headers={"If-Match": str(version)})

    def bulk_create(self, documents, *, idempotency_key=None):
        return self.client.transaction([{"record": {"kind": "document", "collection": self.name, "data": d, "visibility": "team"}, "expected": 0} for d in documents], idempotency_key=idempotency_key)

class Collection:
    def __init__(self, client, name):
        self.objects = DocumentManager(client, name)

class CollectionManager:
    def __init__(self, client):
        self.client = client

    def create(self, name, *, fields=None, unique=None, indexes=None, strict=False):
        return self.client.request("POST", "collections/", {"name": name, "schema": {"fields": fields or {}, "unique": unique or [], "indexes": indexes or [], "strict": strict}})

    def all(self):
        return self.client.request("GET", "collections/")["results"]
