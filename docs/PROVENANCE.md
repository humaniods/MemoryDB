# Implementation provenance

The delivered Go engine, HTTP handlers, Studio and Python client were written
for this project. No upstream source functions were copied or translated in
this iteration. No dependency on TencentDB Agent Memory, Hy-Memory, PostgreSQL,
SQLite, Chroma, Qdrant, Kuzu is present. Optional extraction calls an operator-configured model service;
no model SDK is imported.

The reference READMEs and license files informed the research and capability
roadmap. A source-code audit and selection of exact small components is an open
step. No claim is made that this is a Go port of either project.

BM25, cosine similarity and reciprocal rank fusion are implemented as general
retrieval algorithms. The framed JSON/SHA256 journal is an original simple design.
Python manager naming is inspired by common Django usage, not copied Django code.

Future borrowed component entries must contain:

| Field | Required value |
|---|---|
| Upstream repository | Full canonical URL |
| Revision | Exact commit SHA |
| Original location | File and function |
| License | File-level and transitive dependency check |
| Attribution | Required retained copyright, LICENSE, NOTICE |
| Local changes | Explanation of translated/modified behavior |
| Verification | Focused tests including original edge cases |

Upstream notices must not be removed merely because code is translated into Go.
Check license files and bundled third-party components at the exact selected
commit before borrowing code. The project's own distribution license remains
for its owner to choose.
