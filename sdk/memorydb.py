"""Django-style, dependency-free HTTP client for the original Go engine.
This is not a Django ORM backend: custom QuerySet support, but no SQL, migrations or joins.
"""
import json
import os
from urllib.error import HTTPError
from urllib.parse import quote, unquote, urlencode, urlsplit
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

def parse_connection_string(connection_string):
    """memorydb://TOKEN@host:port (plain HTTP) or memorydbs://TOKEN@host:port (TLS).

    The port defaults to 8080. This mirrors the mongodb:// and redis:// convention so
    deployments can hand one env var (MEMORYDB_URL) to every client.
    """
    parts = urlsplit(connection_string)
    if parts.scheme not in ("memorydb", "memorydbs"):
        raise ValueError("connection string must start with memorydb:// or memorydbs://")
    if not parts.username:
        raise ValueError("connection string must carry the token: memorydb://TOKEN@host:port")
    host = parts.hostname or "127.0.0.1"
    port = parts.port or 8080
    scheme = "https" if parts.scheme == "memorydbs" else "http"
    return scheme + "://" + host + ":" + str(port), unquote(parts.username)


class MemoryDB:
    def __init__(self, url="http://127.0.0.1:8080", *, token=None, timeout=35):
        if token is None:
            if url.startswith(("memorydb://", "memorydbs://")):
                connection_string = url
            else:
                connection_string = os.environ.get("MEMORYDB_URL", "")
            if not connection_string:
                raise ValueError("pass a url with token=, a memorydb:// connection string, or set MEMORYDB_URL")
            url, token = parse_connection_string(connection_string)
        self.url = url.rstrip("/") + "/api/v1/"
        self.token = token
        self.timeout = timeout
        self.objects = MemoryManager(self)
        self.collections = CollectionManager(self)

    @classmethod
    def from_url(cls, connection_string, **fields):
        """MemoryDB.from_url("memorydb://TOKEN@127.0.0.1:8080")"""
        url, token = parse_connection_string(connection_string)
        return cls(url, token=token, **fields)

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

SUPPORTED_LOOKUPS = {"eq", "ne", "gt", "gte", "lt", "lte", "in", "contains", "icontains",
                    "prefix", "exists", "iequals", "iexact"}


def _predicates(lookups):
    """Turn Django-style kwargs into engine predicates; iexact is the Django spelling of iequals."""
    predicates = []
    for key, value in lookups.items():
        parts = key.split("__")
        op = parts.pop() if parts[-1] in SUPPORTED_LOOKUPS else "eq"
        if op == "iexact":
            op = "iequals"
        predicates.append({"field": ".".join(parts), "op": op, "value": value})
    return predicates


def _aggregate(fn, field, as_=None):
    expr = {"fn": fn, "field": field}
    if as_:
        expr["as"] = as_
    return expr


def Sum(field, as_=None):
    return _aggregate("sum", field, as_)


def Avg(field, as_=None):
    return _aggregate("avg", field, as_)


def Min(field, as_=None):
    return _aggregate("min", field, as_)


def Max(field, as_=None):
    return _aggregate("max", field, as_)


class QuerySet:
    """Lazy immutable query builder. Pagination rejects concurrent DB changes."""
    def __init__(self, manager, query=None):
        self.manager = manager
        self.query = dict(query or {"where": [], "limit": 100})

    def filter(self, **lookups):
        predicates = list(self.query.get("where", []))
        predicates.extend(_predicates(lookups))
        return QuerySet(self.manager, {**self.query, "where": predicates})

    def exclude(self, **lookups):
        predicates = list(self.query.get("not", []))
        predicates.extend(_predicates(lookups))
        return QuerySet(self.manager, {**self.query, "not": predicates})

    def or_q(self, *groups):
        """Require the filters above plus at least one of these lookup groups.

        Each group is ANDed on its own, so or_q({"email": x}, {"username": x}) means
        "either field matches" - something a flat where list cannot say.
        """
        or_groups = list(self.query.get("or", []))
        for group in groups:
            predicates = _predicates(group)
            if not predicates:
                raise ValueError("each or_q group needs at least one lookup")
            or_groups.append(predicates)
        return QuerySet(self.manager, {**self.query, "or": or_groups})

    def aggregate(self, *exprs, **named):
        """Fold the whole filtered set, not the current page.

        projects.aggregate(total=Sum("views"), best=Max("views")) reads like Django's
        aggregate(); the keyword becomes the output alias.
        """
        entries = list(self.query.get("aggregate", []))
        entries.extend(exprs)
        for alias, expr in named.items():
            entries.append({**expr, "as": alias})
        return QuerySet(self.manager, {**self.query, "aggregate": entries})

    def group_by(self, *fields):
        grouped = list(self.query.get("group_by", []))
        grouped.extend(fields)
        return QuerySet(self.manager, {**self.query, "group_by": grouped})

    def groups(self):
        """Return one {group, count, values} row per aggregate group."""
        return self.page(limit=1).get("groups", [])

    def order_by(self, *fields):
        """Sort by one or more fields, prefixing a name with "-" for descending.

        A single field keeps the compact wire form; several fields travel as a list so
        ties fall through, the way "trending" needs likes, then views, then newest.
        """
        terms = [{"field": f.lstrip("-"), "descending": f.startswith("-")} for f in fields]
        if not terms:
            raise ValueError("order_by needs at least one field")
        if len(terms) == 1:
            return QuerySet(self.manager, {**self.query, "order_by": terms[0]["field"],
                                           "descending": terms[0]["descending"]})
        return QuerySet(self.manager, {**self.query, "order_by": terms, "descending": False})

    def offset(self, n):
        if n < 0:
            raise ValueError("offset cannot be negative")
        return QuerySet(self.manager, {**self.query, "offset": n})

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

    def exclude(self, **lookups):
        return QuerySet(self).exclude(**lookups)

    def or_q(self, *groups):
        return QuerySet(self).or_q(*groups)

    def aggregate(self, *exprs, **named):
        return QuerySet(self).aggregate(*exprs, **named)

    def group_by(self, *fields):
        return QuerySet(self).group_by(*fields)

    def order_by(self, *fields):
        return QuerySet(self).order_by(*fields)

    def offset(self, n):
        return QuerySet(self).offset(n)

    def all(self):
        return QuerySet(self)

    def update(self, *, pk, version=None, unset=None, inc=None, **fields):
        """Patch a record. Pass version for a compare-and-set, or inc to add onto
        numeric fields server-side; inc re-reads the record under the engine lock, so
        counters like page views cannot lose a concurrent write and need no version."""
        body = {"version": version, "set": fields, "unset": unset or []}
        if inc:
            body["inc"] = inc
        return self.client.request("PATCH", self.path + quote(pk, safe="") + "/", body)

    def increment(self, *, pk, **steps):
        """Atomically add to numeric fields, e.g. increment(pk=pid, views=1)."""
        return self.update(pk=pk, inc=steps)

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

    def schema(self, name):
        """The collection record, which carries both its schema and the revision to change it."""
        for record in self.all():
            if record["collection"] == name:
                return record
        raise MemoryDBError(404, f"collection {name} does not exist")

    def alter(self, name, *, fields=None, unique=None, indexes=None, strict=None):
        """Widen a collection in place, which is what ALTER TABLE ADD COLUMN amounts to here.

        Only new declarations are added and an existing field may not be restated with a
        different type. Rows written before an added field gain that field at its zero value,
        so a reader never meets a document missing a column the schema promises. A required
        or unique field that the stored rows cannot satisfy is refused by the engine.
        """
        record = self.schema(name)
        schema = record["data"]
        for path, field in (fields or {}).items():
            current = schema.setdefault("fields", {}).get(path)
            if current is not None and current != field:
                raise MemoryDBError(409, f"{name}.{path} already has a different schema")
            schema["fields"][path] = field
        for key, added in (("unique", unique), ("indexes", indexes)):
            existing = schema.setdefault(key, [])
            for path in added or []:
                if path not in existing:
                    existing.append(path)
        if strict is not None:
            schema["strict"] = strict
        return self.client.request("PUT", "memories/" + quote(record["id"], safe="") + "/",
                                   {**record, "data": schema})
