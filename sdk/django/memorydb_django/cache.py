"""Cache backend: Django's cache API on top of MemoryDB records.

settings.py:
    CACHES = {"default": {
        "BACKEND": "memorydb_django.MemoryDBCache",
        "LOCATION": "memorydb://TOKEN@127.0.0.1:8080",   # or set MEMORYDB_URL
        "OPTIONS": {"collection": "django_cache"},
    }}
"""
import base64
import pickle
from datetime import datetime, timedelta, timezone

from django.core.cache.backends.base import BaseCache, DEFAULT_TIMEOUT
from memorydb import MemoryDB, MemoryDBError

MAX_PAGE = 500


class MemoryDBCache(BaseCache):
    def __init__(self, server, params):
        super().__init__(params)
        options = params.get("OPTIONS", {})
        location = server if isinstance(server, str) else options.get("url", "")
        if location.startswith("memorydb"):
            self.db = MemoryDB.from_url(location)
        elif location:
            self.db = MemoryDB(location, token=options.get("token"))
        else:
            self.db = MemoryDB()  # reads MEMORYDB_URL
        self.collection = options.get("collection", "django_cache")
        self._ensure_collection()

    # -- engine plumbing ----------------------------------------------------
    def _ensure_collection(self):
        try:
            self.db.collections.create(self.collection,
                                       fields={"cache_key": {"type": "string", "required": True},
                                               "payload": {"type": "string"}},
                                       unique=["cache_key"], indexes=["cache_key"])
        except MemoryDBError as exc:
            if exc.status != 409:  # already exists
                raise

    def _find(self, key):
        page = self.db.request("POST", "query/" + self.collection + "/",
                               {"where": [{"field": "cache_key", "op": "eq", "value": key}], "limit": 1})
        return page["results"][0] if page["results"] else None

    def _write(self, key, value, timeout):
        payload = base64.b64encode(pickle.dumps(value)).decode()
        expires = None
        deadline = self.get_backend_timeout(timeout)
        if deadline:
            expires = datetime.fromtimestamp(deadline, timezone.utc).isoformat()
        for attempt in range(3):
            record = self._find(key)
            try:
                if record is None:
                    self.db.request("POST", "documents/" + self.collection + "/",
                                    {"data": {"cache_key": key, "payload": payload}, "expires": expires})
                else:
                    updated = dict(record)
                    updated["data"] = {"cache_key": key, "payload": payload}
                    updated["expires"] = expires
                    self.db.request("PUT", "documents/" + self.collection + "/" + record["id"] + "/", updated)
                return
            except MemoryDBError as exc:
                if exc.status != 409 or attempt == 2:
                    raise  # concurrent writer bumped the version; re-read and retry

    def _read(self, key):
        record = self._find(key)
        if record is None:
            return None
        return pickle.loads(base64.b64decode(record["data"]["payload"]))

    # -- BaseCache contract ---------------------------------------------------
    def get(self, key, default=None, version=None):
        envelope = self._read(self.make_key(key, version=version))
        if envelope is None:
            return default
        value, flags = envelope
        return value if flags == "c" else default

    def set(self, key, value, timeout=DEFAULT_TIMEOUT, version=None):
        self._write(self.make_key(key, version=version), (value, "c"), timeout)

    def add(self, key, value, timeout=DEFAULT_TIMEOUT, version=None):
        full = self.make_key(key, version=version)
        if self._read(full) is None:
            self.set(key, value, timeout=timeout, version=version)
            return True
        return False

    def delete(self, key, version=None):
        record = self._find(self.make_key(key, version=version))
        if record is None:
            return
        self.db.request("DELETE", "documents/" + self.collection + "/" + record["id"] + "/",
                        headers={"If-Match": str(record["version"])})

    def touch(self, key, timeout=DEFAULT_TIMEOUT, version=None):
        full = self.make_key(key, version=version)
        envelope = self._read(full)
        if envelope is None:
            return False
        self._write(full, envelope, timeout)
        return True

    def clear(self):
        while True:
            page = self.db.request("POST", "query/" + self.collection + "/", {"limit": MAX_PAGE})
            if not page["results"]:
                return
            for record in page["results"]:
                self.db.request("DELETE", "documents/" + self.collection + "/" + record["id"] + "/",
                                headers={"If-Match": str(record["version"])})
