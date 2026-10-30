"""Django integration check: cache + sessions on MemoryDB, settings.py style.

Run: MEMORYDB_URL=memorydb://TOKEN@127.0.0.1:8080 python examples/django_demo.py
"""
import os
import sys
import time

_here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(_here, "..", "sdk"))
sys.path.insert(0, os.path.join(_here, "..", "sdk", "django"))

import django
from django.conf import settings

settings.configure(
    INSTALLED_APPS=["django.contrib.contenttypes", "django.contrib.auth", "django.contrib.sessions"],
    DATABASES={"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": ":memory:"}},
    CACHES={"default": {
        "BACKEND": "memorydb_django.MemoryDBCache",
        "LOCATION": os.environ.get("MEMORYDB_URL", ""),
        "OPTIONS": {"collection": "django_cache"},
    }},
    SESSION_ENGINE="memorydb_django.sessions",
    SESSION_COOKIE_AGE=3,
    SECRET_KEY="test-key-for-integration-only",
)
django.setup()

from django.core.cache import cache

cache.clear()
cache.set("user:1:name", "Aarav", timeout=60)
cache.set("user:1:prefs", {"theme": "dark", "lang": "hi"}, timeout=60)
assert cache.get("user:1:name") == "Aarav", "string roundtrip"
assert cache.get("user:1:prefs") == {"theme": "dark", "lang": "hi"}, "dict roundtrip"
assert cache.get("missing-key") is None
assert cache.add("user:1:name", "dup", timeout=60) is False, "add must not overwrite"
cache.set("user:1:prefs", {"theme": "light"}, timeout=60)   # overwrite existing record
assert cache.get("user:1:prefs") == {"theme": "light"}, "update roundtrip"
assert cache.touch("user:1:prefs", timeout=60) is True
cache.delete("user:1:name")
assert cache.get("user:1:name") is None
cache.set("short", "x", timeout=1)
time.sleep(2)
assert cache.get("short") is None, "native record expiry enforced server-side"
print("DJANGO CACHE BACKEND: all checks passed")

# --- session backend: exactly the django.contrib.sessions store API ---
from memorydb_django import SessionStore

store = SessionStore()
store.create()                      # generates key, writes record
key = store.session_key
store["user_id"] = 42
store["fav"] = "dal-baati"
store.save()

fresh = SessionStore(session_key=key)
assert fresh.load() == {"user_id": 42, "fav": "dal-baati"}, "session roundtrip"
assert SessionStore().exists(key) is True

store["user_id"] = 43
store.modified = True
store.save()
assert SessionStore(session_key=key)["user_id"] == 43, "session update"

store.delete()
assert SessionStore().exists(key) is False, "session deleted"

# --- through Django's own middleware, the way a real request hits it ---
from django.contrib.sessions.middleware import SessionMiddleware
from django.test import RequestFactory


def request_with_session(session_key=None):
    request = RequestFactory().get("/")
    if session_key:
        request.COOKIES["sessionid"] = session_key
    SessionMiddleware(lambda r: None).process_request(request)
    return request


live = request_with_session()
live.session["cart"] = ["paratha", "chai"]
live.session.save()
assert request_with_session(live.session.session_key).session["cart"] == ["paratha", "chai"], "middleware roundtrip"
request_with_session(live.session.session_key).session.delete()

flash = SessionStore()
flash.create()
flash["notice"] = "order placed"
flash.save()
time.sleep(4)
assert SessionStore().exists(flash.session_key) is False, "SESSION_COOKIE_AGE enforced by native expiry"
print("DJANGO SESSION BACKEND: all checks passed")
