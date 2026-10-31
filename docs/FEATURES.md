# Capability map — 0.2

This records actual implemented behavior. A kind name alone is not an automatic
cognitive capability. Neither reference system is imported or fully reproduced.

| Area | Implemented | Remaining boundary |
|---|---|---|
| General data | JSON collections, schemas, CRUD, unique indexes with `casefold` columns, equality indexes, typed comparisons, cursor pagination, projection, count, `where`/`or`/`not` predicates, `icontains`/`iequals` matching, `sum`/`avg`/`min`/`max` with `group_by`, atomic `inc` counters | No SQL, joins or Django ORM backend (the Django integration covers caches and sessions, not models); `contains` on an array is membership, not substring - keep a joined text field for full-text search; aggregates cover the filtered set, not the page |
| Transactions | Atomic 1–256 write batches, revision checks, durable receipts | No interactive multi-request transactions; not universal exactly-once HTTP retries |
| Persistence | Framed WAL, fsync, checksums, partial-tail recovery, lock, compaction, checksummed backup/restore | RAM resident; snapshot max 32 MiB; no segment engine, replication or PITR |
| Conversations | Raw messages, evidence-linked structured facts, exact quote validation | No semantic-entailment proof or evaluation corpus |
| Extraction | Optional native HTTP chat-completions adapter, bounded response, evidence checks | No embedding generation; provider behavior only mock-tested |
| Reconciliation | Subject/predicate/context key supersedes prior owned fact | No general semantic contradiction reasoning |
| Summary/profile | Extractive user-message summary; manually editable profile records | No autonomous hierarchical profile/schema induction |
| Intentions | Records and expiry | No reminder scheduler or prospective reasoning |
| Working memory | Tool traces stored as unreviewed experiences | No learned evaluator or automatic reusable-skill synthesis |
| Skills | Draft-on-edit, explicit approval, source and agent metadata | No skill execution sandbox; agent field is a retrieval filter, not authenticated agent ACL |
| Wiki | Source-preserving paragraph ingestion and links | No PDF ingestion, autonomous synthesis or citation-quality evaluation |
| CodeGraph | Go AST functions, package-direct calls, reverse impact traversal | Methods, dynamic/imported calls unresolved; not whole-program type analysis |
| Coding history | Repository/branch/commit metadata, parent/base validation, Unicode patch reconstruction and hashes | No automatic Git import, merge engine or complete commit DAG synchronization |
| Search | Unicode BM25, exact cosine, embedding-model separation, rank fusion | No ANN, learned reranker or persisted full-text index |
| Permissions | Token-bound workspace/user, roles, team/private/reader-list visibility, owner-only history | No identity provider, dynamic team membership, per-agent authentication or separate access audit log |
| Jobs | WAL-backed ingest jobs, leases, five attempts, restart recovery, console | Failed jobs need explicit retry; no distributed queue or fair scheduling guarantee |
| UI | Collection/query/editor console, transactions, ingestion/jobs/graph, memory Studio | Graph output is JSON; schema editing and operational maintenance use API/CLI |
| Integrations | Python manager/query builder, Go HTTP SDK, Node/TS client, `mdb` CLI (including `mdb serve`, which downloads and sha256-checks the server), stdio MCP server, LangChain chat history, Django cache + session backends (`memorydb_django`), connection strings (`memorydb://TOKEN@host:port`), language-neutral HTTP | Packages install by name from PyPI (`pip install superb-memory`) or from the wheels attached to the GitHub Release (`v0.2.1`), from a git URL or from the tree; the server image is public at `ghcr.io/humaniods/memorydb:0.2.1`. `memorydb-django`, `memorydb-langchain` and the npm name are still installed from the release wheels until their index projects are created — the release workflow uploads through PyPI Trusted Publishing, so no index credential lives in the repository. No Django *database* backend (models/migrations/admin still need SQL), no desktop proxy or hosted connectors yet |
| Forgetting | Logical tombstones, expiry, bounded history compaction | Receipts/backups retain data; no physical erasure |

Production completion requires defined workload/capacity targets, sustained fault
and load testing, independent security review, a labeled memory-quality dataset,
and implementation of whichever remaining capabilities that workload requires.
