# memorydb-django

Django cache and session backends for [MemoryDB](https://github.com/humaniods/MemoryDB).
Two lines in `settings.py`, the way `sqlite3` is a drop-in for Django itself — no models, no
migrations, no new code in your views.

```bash
pip install memorydb-django
```

```python
# settings.py
CACHES = {"default": {
    "BACKEND": "memorydb_django.MemoryDBCache",
    "LOCATION": "memorydb://TOKEN@127.0.0.1:8080",   # or leave empty and set MEMORYDB_URL
}}

SESSION_ENGINE = "memorydb_django.sessions"
```

Then use Django's own API:

```python
from django.core.cache import cache
cache.set("hero", {"name": "Karna"}, timeout=30)   # one record; the TTL is the record's expiry
cache.get("hero")

request.session["user_id"] = 42                    # SESSION_COOKIE_AGE enforced server-side
request.session.save()
```

Details worth knowing:

- Each cache key and each session is one MemoryDB document in the `django_cache` /
  `django_sessions` collection (typed, unique and indexed; created on first use).
- `timeout=` and `SESSION_COOKIE_AGE` are stored as the record's own `expires` field, so the
  server stops serving them at the deadline. No cleanup cron, and `clear_expired()` is a no-op.
- Concurrent writers use optimistic versions and retry, so there are no row locks or deadlocks.
- Set `MEMORYDB_SESSIONS_COLLECTION` / `OPTIONS["collection"]` to move either store elsewhere, so
  two Django projects on one server stay apart.
- Session values are stored through Django's own `SESSION_SERIALIZER` and signed with your
  `SECRET_KEY`, exactly as the database-backed store signs them, so sessions your site already
  issued keep working when you switch engines.

This is a cache/session backend, **not** a `django.db` backend — models, migrations, the admin
and ORM queries still need Postgres or SQLite.
