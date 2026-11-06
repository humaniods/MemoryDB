# Operations

## Install and upgrade

Three ways to get the server onto a machine, all of which produce the same static binary:

```bash
bash install.sh                                # release archive for this OS/CPU, sha256-checked
bash install.sh --source /path/to/MemoryDB    # build from the tree with Go 1.24+
docker build -t memorydb /path/to/MemoryDB     # container image, UI files included
```

`install.sh` writes to `~/.local/bin/memorydb` (`--prefix`, or `MEMORYDB_INSTALL_DIR`),
refuses an archive whose SHA256 does not match the release's `sha256sums.txt`, and prints a
start command with a token it generated for you. `build-dist.sh` is what a release runs to
produce those archives; pointing `--dist-base` at its output directory (`file:///…` or a plain
path) installs from disk, which is how the installer is tested without a published release.

Upgrading replaces the binary, never the data. Stop the server, then:

```bash
./memorydb -data memory.wal -verify                        # journal readable before you start
./memorydb -data memory.wal -backup memory-before.bak       # keeps its .sha256 sidecar
install.sh --version 0.3.0
```

The WAL format is versioned inside the file (`format: 2` frames), so an older journal starts
under a newer binary; a newer journal does not go back. Keep the backup next to its
`.sha256` sidecar and check it before relying on it.

Windows has no native build. The single-writer guarantee comes from `syscall.Flock`, which Go
implements only on Unix (`lock_unix.go`, `//go:build linux || darwin || freebsd`), and a
`GOOS=windows` compile fails on `undefined: lockPath`. Run it in WSL, or as a container.

## Authentication and deployment

`MEMORYDB_TOKEN` creates a local admin in workspace `default`, user `local-user`.
For multiple identities, supply `-auth principals.json` containing:

```json
[{"token_sha256":"REPLACE_WITH_LOWERCASE_SHA256_OF_A_RANDOM_TOKEN","user":"app-one","workspace":"project-one","role":"admin"}]
```

Generate a long random bearer token locally and hash its UTF-8 bytes with SHA256.
Store the plaintext token in the application's secret settings, not source control.
User/workspace names match `[A-Za-z][A-Za-z0-9_-]{0,63}`. Roles: `admin`, `writer`,
`reader`. Restart after configuration changes. Unset `MEMORYDB_TOKEN` when only
configured principals should exist. Configuration hashes do not encrypt the WAL.

Reader tokens can query but cannot write. Writers own their writes; admins can
also edit team-visible records and manage collection definitions. Being an admin
does not grant access to another owner's private record. Reader allowlists grant
read access within the same workspace. Collection schemas are workspace-visible.
`agents` and `source` are retrieval metadata, not separate security boundaries.

The default listener is loopback. Non-loopback requires `-tls-cert` and `-tls-key`,
or explicit `-allow-insecure-network` behind a trusted HTTPS reverse proxy. Restrict
ports to trusted clients, provision persistent storage, and manage token rotation.
No rate limiter or per-tenant resource quota is implemented.

## Backup, restore and compaction

CLI maintenance acquires the same exclusive database lock. Stop the server first:

```bash
./memorydb -data memory.wal -verify
./memorydb -data memory.wal -backup memory-backup.bak
./memorydb -data restored.wal -restore memory-backup.bak
./memorydb -data memory.wal -compact 3
```

Keep the backup and its `.sha256` sidecar together. Backup/restore never overwrite
an existing destination. Restore verifies the checksum and WAL replay. Test the
restored copy before replacing a working instance. The engine's Backup method
supports a consistent in-process copy, but no online HTTP backup endpoint exists.

`-compact 0` preserves all revisions; positive N retains the last N per record.
Revision numbers stay unchanged. Tombstones and original idempotency receipts
remain. Compaction atomically replaces the journal, fsyncs the directory, and
retains its sidecar lock. It is not a physical-erasure operation. Never delete
an active `.lock` file, modify a live WAL or run writers on shared network filesystems.

## Optional extraction

```bash
export MEMORYDB_LLM_URL='https://YOUR_PROVIDER/v1/chat/completions'
export MEMORYDB_LLM_MODEL='YOUR_MODEL'
export MEMORYDB_LLM_KEY='YOUR_SECRET'
```

The URL is the complete chat-completions endpoint, configured by the operator.
The adapter uses JSON-object mode and validates proposed facts and literal user
message evidence. It does not prove that a quote entails the claimed fact. Provider
requests time out after 25 seconds, redirects are disabled, and response reads
are bounded. Only requests with `extract:true` send conversation data to that
configured provider. No paid/provider call is needed for supplied facts or search.
The Python client timeout defaults to 35 seconds.

`-worker` runs ingestion jobs every two seconds. Running jobs have two-minute
leases; expired leases can be reclaimed after restart. Failed jobs require explicit
retry, with at most five attempts. Results use a job-derived idempotency key.
Provider calls may repeat after a crash or retry, even when stored results deduplicate.

## Failure handling

- HTTP 409: reload current revision or restart pagination; resolve unique conflicts.
- HTTP 503: writes are poisoned after an I/O failure. Investigate storage, restart,
  inspect outcome/history and retry only under a known idempotency contract.
- Startup checksum failure: preserve the damaged WAL; restore a verified backup.
  Do not erase corrupt frames or silently truncate complete checksum failures.
- RAM/disk growth: monitor both, take backups, compact within the documented limit.
  There are no built-in capacity alarms or automated retention guarantees.

## Measured throughput and latency

The engine has no metrics endpoint, so these are the numbers to plan against. They were
measured on 5 November 2026 against the published 0.2.6 binary (0.2.7 changes nothing in the
server) on one x86-64 machine: AMD Ryzen 7 7735U, 16 logical CPUs, 30 GB RAM, AVX2, journal on the local
filesystem, HTTP over loopback, one client, nothing else competing. Workload: memories with
64-dimensional vectors and ~80-character content, plus a typed `benchdocs` collection of
integer/string documents. Latency is `p50/p95` of 12-20 sequential requests with `limit=10`.

Search cost is a scan of the authorized data, so it grows with the corpus:

| memories + documents | text `p50/p95` | vector `p50/p95` | hybrid `p50/p95` | indexed filter on 20k documents |
| --- | --- | --- | --- | --- |
| 2,000 | 102 / 132 ms | 99 / 118 ms | 107 / 119 ms | 2.3 ms |
| 5,000 | 332 / 369 ms | 321 / 452 ms | 255 / 318 ms | 5.1 ms |
| 10,000 | 494 / 560 ms | 521 / 753 ms | 500 / 532 ms | 24.4 ms |
| 20,000 | 1013 / 1045 ms | 1024 / 1149 ms | 1007 / 1092 ms | 72.3 ms |

That is about 50 µs per stored record per search (the 5,000-row point measured high, at
66 µs). Extrapolating the same constant, a 100,000-record corpus would answer a text query in
roughly 5 seconds - extrapolation, not measurement.

Throughput, at the 20,000-record corpus:

| what | result |
| --- | --- |
| one writer | 44.4 ms `p50` per create → about 23 writes/s; the same writes cost 1.7 ms `p50` while the corpus was under 2,000 |
| the whole 20,000-record seed, one writer | 380.6 s → 53 writes/s averaged over the growth from 0 to 20,000 |
| 4 concurrent writers | 20 writes/s aggregate, 192 ms `p50` |
| 16 concurrent writers | 21 writes/s aggregate, 803 ms `p50`, 873 ms worst |
| 8 concurrent readers | 2.7 searches/s aggregate, 2,968 ms `p50` |
| one reader while a writer hammers, same query | 2,265 ms `p50` against 1,115 ms idle |

Concurrency buys almost nothing, because every write takes the single engine mutex to append to
the journal, fsync, and re-derive the indexes and BM25 statistics for the whole dataset, and
reads queue behind it. A writer in flight roughly doubles a reader's latency. Query shape
matters too: at the same corpus, `renewal pricing` (two very common terms) took 1,115 ms while
`invoice sla migration` took 523 ms, so high-frequency terms are the expensive case.
Document filters are the exception - an indexed `tag` equality returning 207 of 20,000 documents
took 66-72 ms, because those records are indexed rather than scanned.

Footprint and recovery for that corpus: a 43,467,476-byte journal covering 40,374 transactions,
86.5 MB resident immediately after replay (about 2 KB per stored record, indexes included), and
2.15 s from process start to the first successful `GET /api/v1/health/`. Paged reads that walk
all 20,000 memories are transient and pushed resident memory to ~181 MB.

So: comfortable for one-node agent memory and small document sets up to a few thousand records
with sub-second reads, and roughly 20-25 writes/s per node whatever the size. Past ~10,000
records a search is a second-scale operation and past ~50,000 the scan is the reason to add an
ANN index - see [the gap list in README](../README.md#storage-and-consistency-contract). None of
this is a guarantee: there is no capacity alarm, no rate limiter, and no replication here.
