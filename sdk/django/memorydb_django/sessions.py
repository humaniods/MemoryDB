"""Session backend: django.contrib.sessions storage on MemoryDB records.

settings.py:
    SESSION_ENGINE = "memorydb_django.sessions"

Sessions expire through the engine's own record expiry (SESSION_COOKIE_AGE);
expired sessions stop being readable server-side with no sweep cron.
"""
from datetime import datetime, timedelta, timezone

from django.conf import settings
from django.contrib.sessions.backends.base import CreateError, SessionBase
from memorydb import MemoryDB, MemoryDBError


def _client():
    location = getattr(settings, "MEMORYDB_URL", "")
    if location.startswith("memorydb"):
        return MemoryDB.from_url(location)
    if location:
        return MemoryDB(location, token=getattr(settings, "MEMORYDB_TOKEN", None))
    return MemoryDB()  # reads the MEMORYDB_URL env var


def _collection():
    return getattr(settings, "MEMORYDB_SESSIONS_COLLECTION", "django_sessions")


class SessionStore(SessionBase):
    def __init__(self, session_key=None):
        super().__init__(session_key)
        self.db = _client()
        self.collection = _collection()
        try:
            self.db.collections.create(self.collection,
                                       fields={"session_key": {"type": "string", "required": True},
                                               "payload": {"type": "string"}},
                                       unique=["session_key"], indexes=["session_key"])
        except MemoryDBError as exc:
            if exc.status != 409:
                raise

    def _find(self, session_key):
        page = self.db.request("POST", "query/" + self.collection + "/",
                               {"where": [{"field": "session_key", "op": "eq", "value": session_key}], "limit": 1})
        return page["results"][0] if page["results"] else None

    def load(self):
        record = self._find(self.session_key) if self.session_key else None
        if record is None:
            self._session_key = None
            return {}
        return self.decode(record["data"]["payload"])

    def exists(self, session_key):
        return self._find(session_key) is not None

    def create(self):
        while True:
            self._session_key = self._get_new_session_key()
            try:
                self.save(must_create=True)
            except CreateError:
                continue  # key collision; try another
            self.modified = True
            return

    def save(self, must_create=False):
        if self.session_key is None:
            return self.create()
        payload = self.encode(self._get_session(no_load=must_create))
        expires = (datetime.now(timezone.utc) + timedelta(seconds=self.get_expiry_age())).isoformat()
        for attempt in range(3):
            record = self._find(self.session_key)
            try:
                if record is None:
                    self.db.request("POST", "documents/" + self.collection + "/",
                                    {"data": {"session_key": self.session_key, "payload": payload}, "expires": expires})
                    return
                if must_create:
                    raise CreateError
                updated = dict(record)
                updated["data"] = {"session_key": self.session_key, "payload": payload}
                updated["expires"] = expires
                self.db.request("PUT", "documents/" + self.collection + "/" + record["id"] + "/", updated)
                return
            except MemoryDBError as exc:
                if exc.status != 409 or attempt == 2:
                    raise

    def delete(self, session_key=None):
        if session_key is None:
            if self.session_key is None:
                return
            session_key = self.session_key
        record = self._find(session_key)
        if record is not None:
            self.db.request("DELETE", "documents/" + self.collection + "/" + record["id"] + "/",
                            headers={"If-Match": str(record["version"])})

    @classmethod
    def clear_expired(cls):
        pass  # expiry is enforced by the engine at read time; nothing to sweep
