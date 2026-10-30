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
| PATCH | `documents/COLLECTION/ID/` | `{version,set:{field:value},unset:[field],inc:{field:step}}`; top-level keys |
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

Operators: `eq,ne,gt,gte,lt,lte,in,contains,icontains,prefix,iequals,exists`. Where
predicates are ANDed. Nested fields use dot paths. Missing and null differ. No
numeric string coercion.
`contains`/`prefix`/`eq` fold no case; `icontains` and `iequals` fold ASCII case, which
is what SQLite's `LIKE` and `COLLATE NOCASE` do, so ported search and login lookups
keep their behaviour. On arrays `contains`/`icontains` test membership, not substring;
to search inside a list store a joined text field and use `icontains` on it.

Boolean structure, all optional:

```json
{"where":[{"field":"category","op":"eq","value":"Web App"}],
 "or":[[{"field":"title","op":"icontains","value":"univorya"}],[{"field":"tags","op":"icontains","value":"rust"}]],
 "not":[{"field":"is_demo","op":"eq","value":true}]}
```

`or` groups are each ANDed and at least one must match, in addition to `where`.
`not` rejects any record matching one of its predicates. An `or` group holds 1..32
predicates and a query holds at most 32 groups and 32 `not` predicates. Equality
indexes still accelerate `where` when `or`/`not` are present, because those clauses
only narrow the indexed candidate set.

Sorting takes one field as a name, or several as a list that breaks ties left to right:

```json
{"order_by":[{"field":"likes","descending":true},{"field":"views","descending":true},
            {"field":"created_at","descending":true}]}
```

Up to 4 terms, each field named once. A missing value sorts after a present one in both
directions. Record id always breaks the final tie, so equal rows never shuffle between
pages. `"order_by":"age"` with `"descending":true` is the single-field shorthand and stays
valid; combining `descending` with a list is rejected as ambiguous.

`offset` skips rows without a cursor, which is what numbered pages need:

```json
{"order_by":[{"field":"likes","descending":true}],"limit":24,"offset":48}
```

`limit` is the page size, `offset` the rows to skip (0..1,000,000). `total` still counts
the whole filtered set, so a page reports its own rows and the real page count. Offsets
are not snapshots: a write between page fetches can shift or repeat a row. `offset` and
`cursor` cannot be combined - use a cursor when results must not move.
Integers preserve JSON precision; arithmetic comparisons accept bounded decimal
representations (128 characters, exponent -308..308). Sorting has a total order
across heterogeneous values. Missing sort fields follow present values. Projection
returns requested dot-path keys in a flat data object. Fetch the full document
before replacing it; saving a projection can otherwise drop unselected fields.

`limit` max 500; default 50. Pass `next_cursor` as `cursor` with exactly the same
query to continue. Mutations or changed active results return 409. `total` counts
all matching readable documents, so a count is `total` with `limit:1`.

## Aggregation

```json
{"limit":1,"where":[{"field":"category","op":"eq","value":"Web App"}],
 "group_by":["category"],
 "aggregate":[{"fn":"sum","field":"views"},{"fn":"max","field":"views","as":"top"}]}
```

`fn` is `sum`, `avg`, `min` or `max`; at most 16 per query, each on one dot path.
`as` names the output key and must match the collection-name pattern; the default is
`<fn>_<field>` with dots and dashes folded to underscores. Duplicate output names are
rejected. `group_by` takes up to 8 dot paths and is optional.

Results come back in `groups`, beside `results` rather than instead of them. Each row
is `{group,count,values}`; with no `group_by` there is one row and `group` is null.
Aggregation covers every filtered record, never just the page, and ignores `limit`,
`order_by` and `projection`. `count` is the number of matching records in the group,
including records missing the aggregated field.

`sum`/`avg` skip non-numeric values. A group holding no number for that field reports
null, the way SQL `SUM` returns NULL rather than zero. Exact integer and finite decimal
results are emitted as numbers of full precision, so values past 2^53 survive a sum and
an average like 9007199254741053/4 comes back as `2251799813685263.25`. A repeating
decimal falls back to 17 significant digits. `min`/`max` use the same total order as
`order_by` and return the stored value unchanged.

## Counters

`inc` adds to numeric fields inside the engine lock and re-reads the live record, so a
page-view or like counter never loses a concurrent write and needs no `version`. Omitting
`version` on a plain `set` still conflicts. Steps and the stored value must be numbers
without string coercion; adding onto text is rejected, and a field the record lacks starts
at zero. Negative steps decrement. `set`, `unset` and `inc` may be combined in one PATCH
and land as a single revision. Results keep exact integer precision.

```python
projects.increment(pk=pid, views=1)
projects.update(pk=pid, inc={"likes": -1}, stage="Beta")
```

Python clients use `Sum`/`Avg`/`Min`/`Max`:

```python
projects.aggregate(total=Sum("views"), best=Max("views")).groups()
projects.group_by("category").aggregate(Sum("views")).groups()
```

Schema: `{fields:{name:{type:"string",required:true}},unique:["name"],indexes:["name"],strict:false}`.
Types: any,string,number,integer,boolean,object,array. Strict checks top-level
undeclared keys; it is not recursive JSON Schema. Unique/index paths need field
declarations. Missing/null values do not participate in uniqueness. A string field may
set `casefold:true`, which makes its unique index ignore ASCII case the way SQLite's
`UNIQUE COLLATE NOCASE` does, so `Priya@x.com` and `priya@x.com` are one identity;
`casefold` on any other type is rejected. It affects uniqueness only - `eq` stays
case-exact and `iequals` is the folding matcher. Adding `casefold` re-canonicalises the
field, so a collection already holding two casings of one value cannot adopt it until
that data is resolved. Collection changes can be submitted as versioned admin
transactions and validate existing data.
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
