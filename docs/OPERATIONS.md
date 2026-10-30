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
