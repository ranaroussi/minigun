# Changelog

All notable changes to the MiniGun Worker are documented here. Versions are
tagged `worker/vX.Y.Z` and follow [Semantic Versioning](https://semver.org/).

## [0.2.2] - 2026-07-15

### Fixed
- Bulk sends can no longer wedge on an oversized `batch_size`, the recurring
  root cause of stalled sends. Two layers:
  - `createSend` now clamps `batch_size` to `MAX_BATCH_SIZE` (100) regardless
    of what the caller (CLI, MCP, API) requests. Building a batch's
    per-recipient Mailgun variables is CPU-bound and linear in size; above
    ~100 an invocation trips the Workers CPU limit and dies before recording
    an outcome. The prior 0.2.1 default of 250 was silently overridden because
    the CLI hard-coded `--batch-size 500`; a server-side clamp closes that.
  - `sweepStuckSends` now self-heals: a running send stalled past the stale
    window with no `in_flight` batch (its step chain died mid-build) has its
    `batch_size` halved toward `SAFE_BATCH_FLOOR` (100) before the watchdog
    re-kicks it, so any oversized/legacy send walks down (500 -> 250 -> 125 ->
    100) until step() completes, without a manual fix.

### Changed
- Default bulk `batch_size` is now 100 (was 250), matching the hard cap.
- CLI `--batch-size` default lowered 500 -> 100 (`cli` and `src` send
  commands); MCP tool doc updated to note the server-side cap.

## [0.2.1] - 2026-07-09

### Fixed
- Watchdog now self-heals sends wedged by an orphaned `in_flight` batch.
  `sweepStuckSends` calls the new `reclaimStuckBatches`, which flips any
  batch left `in_flight` past the 2-minute stale window to `failed` before
  listing stuck sends. A Worker invocation cannot run for minutes, so such a
  batch is provably orphaned (its invocation died before recording an
  outcome); clearing it lets the send resume from its cursor and re-send that
  range. The 0.2.0 fix only narrowed the orphan window — it did not let the
  watchdog recover an orphan that still occurred, so a wedged send required
  manual intervention. Trade-off: a worker that died after Mailgun accepted
  but before the batch row was written (sub-second window) yields a duplicate
  for that batch, which is preferable to a permanently stalled send.

### Changed
- Default bulk `batch_size` lowered from 500 to 250. At 500 recipients the
  per-batch recipient-variable build repeatedly exhausted the CPU budget and
  orphaned the batch; 250 halves that cost while the reclaim watchdog covers
  any residual orphan.

## [0.2.0] - 2026-07-01

### Added
- Public, unauthenticated geo endpoint served on `geo.misc.sh`: `GET /geo`
  (country, city, region, timezone) and `GET /full` (adds the requesting IP).
  Mounted ahead of the bearer-auth middleware so it needs no token.

### Fixed
- Stuck bulk sends caused by CPU-orphaned batches. `step()` now creates the
  `in_flight` batch row only immediately before the Mailgun call, after the
  CPU-heavy recipient-variable build. Previously an invocation killed
  mid-build (`exceededCpu`) left an orphaned `in_flight` batch, which wedged
  the send permanently: `step()` short-circuits to `busy` while one exists and
  `sweepStuckSends` excludes any send that has one, so no watchdog could
  recover it.

### Changed
- The engagement events-pull now pauses while any send is in flight
  (`hasActiveSend` guard in `pullDueSendEvents`). The per-tick pull could
  otherwise exhaust the scheduled handler's CPU budget folding a large send's
  delivery burst, starving the send watchdog. Deferring the pull a tick drops
  nothing — the burst/daily schedule stays due until it runs.
