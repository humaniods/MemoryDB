# Research and implementation decisions

> Historical design research from v0.1. Current implementation status is in
> FEATURES.md, README.md and API.md; roadmap entries below are not current status.

Prepared 23 October 2026 for Sanyam Sharma.

## What we are building

An original Go memory database and agent-memory platform with a familiar Python
client and admin-style UI. The attached prior comparison is the initial brief;
current official READMEs and license files were checked again. They are moving
branches, not an immutable source audit. No precise parity or production-quality
claim follows from marketing descriptions or layer counts.

Both references are memory systems, not new general-purpose SQL storage engines.
Our project deliberately owns storage, retrieval and memory lifecycle code.
This is a larger undertaking than placing an API in front of PostgreSQL.

## What each reference contributes

TencentDB Agent Memory describes four managed assets: chat, skills, Wiki and
CodeGraph. It combines team sharing, ownership/ACLs, agent bindings and layered
chat retrieval. Its README describes BM25/vector rank fusion and asynchronous
knowledge processing. Our design takes the separation of assets, authorization
and retrieval as inspiration. [R1]

Hy-Memory describes chat, working and coding scenes. Chat has a hierarchical
model, including reserved layers; working records facts and reusable experience;
coding adds revision evolution. Its SDK exposes different modes and configurable
storage backends. Our design takes scene separation, evidence, temporal updates
and revision provenance as inspiration. [R2]

These are feature references, not evidence that either engine's benchmark scores
transfer to ours. Eight layer names do not automatically provide better memory
than four. Preserve useful semantics rather than duplicating naming schemes.

## Go versus Rust: decision for this project

| Decision factor | Go | Rust |
|---|---|---|
| Transition from Python/Django | Smaller conceptual jump; chosen first | Ownership/lifetimes add learning work |
| Local service delivery | Standard HTTP/JSON/testing tools in one toolchain | Strong options, with different ecosystem choices |
| Fine control over memory layout | Possible, GC affects tuning | Explicit ownership suits tightly controlled allocation |
| Current bottleneck | Correctness, retrieval quality, disk design | Language alone does not solve these |
| Recommendation | Build v0.1 through storage/index validation | Reconsider only after profiling identifies a specific need |

This is an engineering judgment for your comfort and scope, not a measured
performance comparison. Do not split the first engine across Go and Rust.
No Rust implementation was created in this iteration.

## Proposed architecture

1. **Native engine:** WAL, typed record envelope, immutable revisions, latest-view
   projection, lexical/vector/graph indexes, snapshots and recovery.
2. **Memory services:** evidence-linked extraction, reconciliation, summaries,
   experience evaluation, skill approval, Wiki generation, code graph updates.
3. **Governance:** authenticated identities, tenants, owners, ACLs, asset bindings,
   deletion/retention, audit events, quotas.
4. **Interfaces:** HTTP API, Python manager-style SDK, native Go client later,
   admin UI, then MCP/proxy adapters.

Django stays optional at the application edge. Calling `Memory.objects.create`
from a Django service does not require the engine itself to run in Python.
The SDK uses familiar manager names [R3], but deliberately makes no claim of
supporting Django migrations, QuerySet chaining or SQL joins.

## Unified model (target, not all implemented)

Every memory should have a namespace, kind, schema version, immutable revision,
evidence references, event time and ingestion time. Separate identity from
content. Store extracted claims as derived data with links to raw evidence.

Kinds: raw conversation, atomic fact, summary, profile, prospective intention,
working fact, reusable experience, approved skill, Wiki page, code symbol,
code edge, code revision. Evidence records should preserve source session,
message/tool-call ID, document span or repository commit/path/line range.

Use names instead of forcing reference-specific L0-L7 values onto the model.
The prototype supports a free-form `layer` plus typed `kind`; it does not validate
an automatic hierarchy. Future schemas must validate each kind independently.

For temporal facts, use subject + predicate + scope + validity interval. A user
changing their preferred language should supersede the old preference while
preserving the evidence. Different contexts may coexist; never blindly delete
facts because their text looks contradictory. Uncertain extraction goes to a
review queue. Record confidence, source and extraction model/prompt version.

## Storage design progression

**Implemented:** one process owns a checksummed framed append-only journal;
fsync before acknowledgement, replay into RAM, immutable history, revision checks.

**Next:** schema-versioned WAL; idempotency keys; crash fault injection; streaming
recovery; snapshot manifests with checksums; snapshot rename + directory fsync;
compaction preserving required revisions; explicit physical erasure semantics.

**Then:** immutable sorted segments plus mutable index, checksummed segment
blocks, sparse offsets, bloom filters, background merge policy and index replay.
Implement and measure these before claiming a full LSM engine. Persist lexical
postings and vector indexes as rebuildable derived structures.

**Later, only if required:** replication protocol, leader epoch, durable commit
index, follower recovery, consensus, online backup and sharding. None exists in
v0.1. Running two writers over one file is rejected, not a supported cluster.

## Retrieval design

v0.1 uses Unicode-aware tokenization, BM25 text scores, exact cosine over supplied
vectors, and reciprocal rank fusion. Identity scope is applied before scoring.
Lexical and semantic rank lists contribute `1 / (60 + rank)` with rank starting
at one. Scores are retrieval ranks, not confidence probabilities.

Future retrieval: permission-first indexed candidates, source/time/kind filters,
embedding model/version enforcement, HNSW approximate search, optional learned
reranking, then token/latency budgets. Ground graph expansion in typed evidence.
Do not claim semantic quality from random or hashed vectors.

Use separate workers for extraction and consolidation. Fast path: persist raw
input; retrieve approved memories. Slow path: generate candidates, validate JSON,
attach evidence, reconcile and publish accepted revisions. An LLM can propose
content; the deterministic engine enforces storage/permission rules.

## CodeGraph and coding history must remain separate

A `links` array is only a graph primitive. Real CodeGraph requires parser-backed
symbol extraction, import resolution, typed calls, source spans, commit IDs and
incremental invalidation. Start with Go's standard AST packages and a separately
implemented Python parser adapter. Dynamic calls must be marked unresolved.

A full-content version history is not an exact Git diff chain. Coding memory
needs repository identity, branch, commit/parent, patch, base/result hashes,
decision and tool/test evidence. Branches form a DAG; never model all branches as
one linear sequence. Add reconstruction tests before exposing rollback.

## Reusing code without importing whole systems

Your requested approach is supported: select small useful components, port them
to Go and own their tests/interfaces. This iteration uses **original code** based
on public concepts; no upstream implementation was transplanted. A code-level
function audit is still needed before selecting exact upstream snippets.

The checked root licenses label TencentDB Agent Memory MIT [R4] and Hy-Memory
Apache-2.0 [R5]. For future copying/translation, inspect the exact source file,
its headers and any vendored dependencies as well. Keep upstream copyright and
license notices, applicable NOTICE text, and mark modifications. Translating
Python to Go does not automatically remove upstream obligations.

Maintain a provenance entry for every port: repository, exact commit, file,
function, source license, retained notice, reason for selection, local tests and
changes. No selected source is pinned yet; a README review is not a code audit.
Do not copy application branding, secrets or bundled data as implementation.

## Evaluation before production claims

Build a labeled, consented dataset: Hinglish preference recall, corrected facts,
expired intentions, successful/failed work traces, skill boundaries, Wiki evidence,
code impact and branch history. Include adversarial cross-tenant cases.

Measure Recall@k, MRR/nDCG, evidence precision, correction accuracy, stale-memory
rate, cross-tenant leakage, per-stage p50/p95/p99 latency, write acknowledgement
latency, restart time, peak RAM, disk growth and retrieval cost. Report machine,
record count/dimensions, model versions and methodology alongside every number.
Do not set an invented ATS-style percentage or speed claim for memory quality.

Suggested progression: 1k correctness corpus -> 10k/100k scaling experiments ->
index decision. Voice agents need measured retrieval budgets before integration.
No throughput/quality benchmark was run in v0.1.

## Delivery gates

| Milestone | Deliverable | Acceptance gate |
|---|---|---|
| 0 - now | Journal, CRUD/search, revisions, SDK, Studio | Recovery/auth/API/race/UI smoke checks |
| 1 - storage | Snapshots, compaction, schema upgrades, idempotency | Fault injection at every write boundary; restore tests |
| 2 - memory | Evidence-led extraction, reconciliation, summaries, intentions | Labeled correction/provenance/expiry evaluation |
| 3 - knowledge | Wiki jobs, experience and skill lifecycle, code graph and Git DAG | Citation accuracy; parser and reconstruction tests |
| 4 - governance | Team/role/agent ACLs, bindings, audit and retention UI | Permission checks across every retrieval/export path |
| 5 - integration | Django package, Go SDK, MCP/proxy, async workers | End-to-end Unpod-style voice-agent evaluation |
| 6 - scale | Persistent indexes, ANN, backup, operational metrics | Published workload-specific benchmarks; recovery drills |

Do not estimate the whole scope as a weekend build. Decide capacity and
multi-user/hosting needs before the storage/index milestone; they materially
change the implementation. Default so far: local Linux, modest dataset, one
trusted principal, no paid model dependency.

## Official sources checked

- [R1] https://github.com/TencentCloud/TencentDB-Agent-Memory (feat/server_team branch shown during review)
- [R2] https://github.com/Tencent-Hunyuan/Hy-Memory (main branch shown during review)
- [R3] https://docs.djangoproject.com/en/5.2/topics/db/queries/
- [R4] https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/LICENSE
- [R5] https://github.com/Tencent-Hunyuan/Hy-Memory/blob/main/LICENSE
- Go language reference used for implementation style: https://go.dev/doc/effective_go

The uploaded six-page chat comparison supplied initial requirements, not source
code or authoritative implementation details. This report does not rely on its
unverified exact release-date claim. Official root directory pages were not
sufficient to inspect internal function implementations during this review.
