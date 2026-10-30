"""Django integration for MemoryDB: cache and session backends that drop into settings.py.

    CACHES = {"default": {"BACKEND": "memorydb_django.MemoryDBCache",
                          "LOCATION": "memorydb://TOKEN@127.0.0.1:8080"}}
    SESSION_ENGINE = "django.contrib.sessions.backends.memorydb"  # via this package's session module

Values expire through the engine's own record expiry, not a swept key table:
an expired record stops being readable server-side.
"""
from memorydb_django.cache import MemoryDBCache
from memorydb_django.sessions import SessionStore

__all__ = ["MemoryDBCache", "SessionStore"]
