# HTTP API 0.2

Base `/api/v1/`; all endpoints require `Authorization: Bearer TOKEN`.
JSON response errors use `{"error":"message"}`. Statuses: 400 invalid request,
401 authentication, 403 role, 404 unreadable/missing, 409 conflict, 503 storage fault.
Some record ownership failures currently return 400 rather than 403.

| Method | Path | Input / result |
|---|---|---|
| GET | `health/` | Version, authenticated identity, sequence |
| GET / POST | `collections/` | List / `{name,schema}`; create needs admin |
| GET / POST | `documents/COLLECTION/` | First page / create `{data,visibility?,readers?}` |
| GET / PUT | `documents/COLLECTION/ID/` | Read / replace full record with current version |
| PATCH | `documents/COLLECTION/ID/` | `{version,set:{field:value},unset:[field]}`; top-level keys |
| DELETE | `documents/COLLECTION/ID/` | `If-Match: VERSION`; logical tombstone |
| POST | `query/COLLECTION/` | Query below; `{results,total,next_cursor?,sequence,plan}` |
| POST | `transactions/` | `{changes:[{record,expected}]}`; atomic batch |
| GET / POST | `memories/` | List/search via kind/source/q/limit, or create record |
| GET / PUT / DELETE | `memories/ID/` | Record CRUD; PUT full record/version; DELETE If-Match |
| GET | `memories/ID/history/` | Owner-only revisions, including tombstones |
| POST | `search/` | `{text,vector,embedding_model,agent,kind,source,limit}` |
| POST | `ingest/` | Conversation + facts or extract flag; records result |
| POST | `jobs/` | Queue ingestion body; returns durable job |
| POST | `jobs/ID/run/` | Process/retry owned job |
| POST | `wiki/` | `{text,source,title}`; source-preserving paragraphs |
| POST | `graph/` | `{id,reverse,depth,limit}` scoped BFS |
| POST | `code/index/` | `{repository,branch,commit,files:{path:GoSource}}` |
| POST | `code/revisions/` | `{repository,branch,commit,parent?,path,before,after,reason}` |
| POST | `skills/ID/approve/` | `{version}`; admin with write permission required |

`Idempotency-Key` supports durable matching-request deduplication for creates,
transactions and ingestion. Keys are scoped to workspace/user/path and must fit
256 bytes including that prefix. Reusing a key for different contents conflicts.
Use a fresh key for each logical operation. See README for mutation retry limitations.

## Query and schema

```json
{"where":[{"field":"age","op":"gte","value":18}],"order_by":"age","descending":true,"projection":["email"],"limit":50}
```

Operators: `eq,ne,gt,gte,lt,lte,in,contains,prefix,exists`. Predicates are ANDed.
Nested fields use dot paths. Missing and null differ. No numeric string coercion.
Integers preserve JSON precision; arithmetic comparisons accept bounded decimal
representations (128 characters, exponent -308..308). Sorting has a total order
across heterogeneous values. Missing sort fields follow present values. Projection
returns requested dot-path keys in a flat data object. Fetch the full document
before replacing it; saving a projection can otherwise drop unselected fields.

`limit` max 500; default 50. Pass `next_cursor` as `cursor` with exactly the same
query to continue. Mutations or changed active results return 409. `total` counts
all matching readable documents. No OR/NOT query AST or aggregation pipeline.

Schema: `{fields:{name:{type:"string",required:true}},unique:["name"],indexes:["name"],strict:false}`.
Types: any,string,number,integer,boolean,object,array. Strict checks top-level
undeclared keys; it is not recursive JSON Schema. Unique/index paths need field
declarations. Missing/null values do not participate in uniqueness. Collection
changes can be submitted as versioned admin transactions and validate existing data.
A collection with live documents cannot be deleted. Unknown collections query as
empty; inserts require an existing definition.

## Evidence-linked ingestion

```json
{"messages":[{"role":"user","content":"I prefer Hindi"}],"facts":[{"subject":"user","predicate":"language","value":"Hindi","quote":"I prefer Hindi","message_index":0,"confidence":1}],"source":"example","context":"general"}
```

`extract:true` replaces supplied facts with provider extraction. Both together are
rejected. Roles: user/assistant/system/tool. Facts require a literal quote from a
user message and confidence 0.5..1. A fact key is subject+predicate+context within
the owner/workspace. New values supersede older matching facts without erasing
history. A summary is an extractive excerpt, not generated profile reasoning.
Optional `tools:[{name,input,output,success}]` stores unreviewed experiences.

Memory records support kinds chat,fact,summary,profile,intention,experience,skill,
wiki,code,revision; internal collection/document/job kinds also exist. Optional
fields include source,layer,parent,links,metadata (string values),vector,
embedding_model,expires (RFC3339),visibility,readers,agents. Links must resolve to
active records readable by the record owner at write time. Deletion can leave
historical dangling links; traversal filters unreadable/inactive targets.
