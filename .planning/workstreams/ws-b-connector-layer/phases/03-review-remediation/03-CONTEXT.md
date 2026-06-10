# Phase 3 Context — Review Remediation

Source: full-codebase code + security review, 2026-06-10 (all 19 Go packages).
Verdict was REQUEST CHANGES. This phase fixes every finding. Severity ordering
below is the recommended fix order; the WAL findings (F1–F3) share one code path
and must be fixed in a single pass.

## Findings Register

### 🔴 Critical

**F1 — WAL consumed-marker corrupts stream for payloads ≥ 16 MB**
`internal/buffer/wal.go:104-124`. `markConsumed` overwrites the first byte of the
4-byte big-endian length prefix with 0xFF. On read, length is reconstructed
assuming that byte was 0x00. Payload ≥ 16,777,216 bytes → wrong length → file
offset desyncs → all subsequent records misread/skipped.
FIX (locked decision): change record format to `[1-byte status][4-byte length][payload]`.
Status 0x00 = live, 0xFF = consumed. markConsumed writes only the status byte.
No back-compat needed — no WAL files exist in production yet. Update wal.go
package doc, appendRecord, streamRecords, markConsumed, and all offsets in tests.

**F2 — Data race on `b.seg` in Buffer.Write**
`internal/buffer/buffer.go:118-120`. Write releases segMu then reads b.seg for
appendRecord; rotation block can concurrently Close that file and replace the
pointer. FIX: capture `seg := b.seg` under segMu (or hold segMu across append).
Add a concurrent-writers test and require `go test -race` passes.

**F3 — Segment rotation strands unconsumed records + unbounded disk growth**
`internal/buffer/buffer.go:126-142` + `173-180`. drainOnce streams only the
current segPath; rotated-away segments are never drained (silent data loss) and
never deleted (disk fills forever). countDropped is never incremented anywhere.
FIX: drain enumerates ALL `wal-*.seg` files in cfg.Dir sorted oldest-first
(filename embeds unix timestamp; use sortable naming — add a monotonic counter
suffix `wal-<unix>-<seq>.seg` since two rotations in one second collide).
Delete a segment file when every record in it is consumed. Enforce MaxSizeMB as
TOTAL across segments: when exceeded, delete the OLDEST segment and add its
record count to countDropped.

**F4 — Elastic NDJSON injection via Index config value**
`internal/connector/elastic/connector.go:217`. Action line built by string
concatenation of cfg.Index; a value containing `"` injects bulk-API parameters.
Config is hot-reloadable so the write path is reachable. FIX: build via
`json.Marshal(map[string]any{"index": map[string]string{"_index": c.cfg.Index}})`
once in Send (or precompute in New + recompute if cfg changes). Add a test with
a hostile index name asserting the payload stays well-formed.

**F5 — agent.stop panics on nil drain func**
`internal/agent/agent.go:151` passes nil drain to Flush; drainOnce calls it
unguarded → nil deref on first buffered record during graceful shutdown.
FIX: Buffer.Flush (and Start) return error / no-op with error log on nil drain;
agent.stop stops passing nil (skip the Flush until the real drain func is wired,
with a TODO comment referencing the agent.start wiring task).

### 🟠 High

**F6 — Failed delivery acks with nil error are invisible**
`splunk/connector.go:267-274`, `elastic/connector.go:281-307` return
DeliveryAck{Status:"failed"} with nil error; Dispatcher.process
(`connector/connector.go:265-285`) only checks err != nil → failures never
logged/retried. CONTRACT (locked): any non-delivered outcome returns a non-nil
error AND a failed ack; the ack carries detail. Align splunk + elastic (kafka
already conforms). Dispatcher increments its delivered/failed counters based on
Send results (counters exist but are never incremented — wire accepted in
Enqueue, delivered/failed in process).

**F7 — Batch truncation silently drops overflow**
`splunk/connector.go:168-172`, `elastic/connector.go:210-214`. Signals beyond
MaxBatchEvents/MaxBatchDocs are discarded while reporting "delivered".
FIX: chunk into ⌈n/limit⌉ sequential requests; first failed chunk aborts and
returns error (remaining signals count as failed); ack aggregates.

**F8 — dryrun index misalignment when mapper errors occur**
`internal/dryrun/dryrun.go:142-168`. mapper.MapBatch compacts both events and
errors slices, but dryrun pairs them with signals by index → wrong SignalID
attribution and inflated OCSFValid. FIX: replace MapBatch call with a per-signal
`mapper.Map` loop building a parallel `events []*Event` (nil at failed index).
validateOCSFBatch already skips nil events. Add a test with an invalid layer
mid-batch asserting correct SignalID attribution and counts.

**F9 — Config Watcher misses atomic-rename updates + stderr logging**
`internal/connector/config.go:93`. Only Write|Create handled; rename-based
saves (vim, atomic writers) drop the inode watch silently. FIX: watch the
parent directory of the config file, filter events to the target filename,
handle Rename/Remove by re-adding. Replace fmt.Fprintf(os.Stderr) with a
*zap.Logger field (accept logger in NewWatcher; default zap.NewNop()).

### 🟡 Medium / Low

**F10 — CircuitBreaker.Call TOCTOU race**
`internal/resilience/circuit_breaker.go:58-105`. State read under lock then
released; lastFailureTime read unlocked at line 65; two goroutines can both
transition Open→HalfOpen. FIX: hold cb.mu for the entire decision (check state,
timeout transition, increment) — release only around the operation() call itself.

**F11 — GetSecret decrypts entire store on every call**
`internal/secrets/env_fallback.go:36`. FIX: cache the decrypted map in Store
behind sync.RWMutex; invalidate on SaveSecrets.

**F12 — Kafka RequiredAcks=0 unreachable**
`kafka/connector.go:81`. `if cfg.RequiredAcks == 0 { cfg.RequiredAcks = 1 }`
conflates "unset" with NoAck. FIX: change Config field to `RequiredAcks *int`
(nil = default 1) OR sentinel constant; keep YAML compat; document.

**F13 — activity_id=99 without ActivityName**
`internal/ocsf/mapper.go:518`. OCSF validators may reject 99/"Other" without a
name. FIX: in Map(), when activityID==99 set ev.ActivityName = "Other".

**F14 — Drain backoff sleeps inside streamRecords callback**
`internal/buffer/buffer.go:198`. Sleep up to BackoffMax happens while the
segment file handle is open mid-iteration. FIX (part of the F1-F3 rewrite):
on drain error, return from the stream immediately; compute backoff in
drainLoop/Flush and wait there before the next attempt.

**F15 — secrets temp file perms window**
`internal/secrets/store.go:156`. os.Create → 0666&umask before later Chmod.
FIX: `os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)`.

**F16 — Elastic raw APIKey retained on struct after encoding**
`elastic/connector.go:103-111`. FIX: zero cfg.APIKey on the stored struct copy
after computing apiKeyHeader (`cfg.APIKey = ""` before assigning c.cfg).
Connect()'s empty-check must then use apiKeyHeader != "" instead.

**F17 — mapper LoggedTime non-determinism** (accepted/deferred)
`mapper.go:304` time.Now() in Map. DEFER — note in SUMMARY as accepted; do not
fix in this phase (clock injection is an API change touching all connectors).

## Locked Decisions

1. WAL format: `[status:1][len:4 BE][payload]`, no migration/back-compat code.
2. Segment naming: `wal-<unix>-<seq>.seg` with per-process atomic seq counter.
3. Delivery contract: non-delivered ⇒ non-nil error + failed ack, everywhere.
4. Chunking: sequential, abort on first failed chunk.
5. F17 deferred — document, don't fix.
6. All fixes verified by `go test -race ./...` + `go vet ./...` (SC-12).

## Scope Fences

- Do NOT implement agent.start wiring (registration, dispatcher creation) — that
  is a future phase; only the nil-drain guard (F5) touches agent.go.
- Do NOT implement syslog/argusxdr connector bodies (known scaffolds).
- Do NOT add new dependencies; stdlib + existing deps only.
- Do NOT change the proto contract or pkg/signal public API.

## Test Environment Notes

- Windows: WAL tests must Close() file handles before delete/rename (Windows
  file locking) — pattern already established in buffer_test.go.
- Worktree-isolated subagents do not work on this machine — plans execute
  sequentially on the main tree at C:\Users\Drupad\argus-sdk.
