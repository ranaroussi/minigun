# Changelog

All notable changes to the MiniGun Worker are documented here. Versions are
tagged `worker/vX.Y.Z` and follow [Semantic Versioning](https://semver.org/).

## [0.2.6] - 2026-08-13

### Added
- `GET /sends` accepts an optional `list` query parameter (slug or id) that
  restricts the feed to one list, newest first. An unknown list returns 404,
  while a known list with no sends returns an empty `items` array - so callers
  can tell "no such list" apart from "list exists but was never sent to".
  Powers the CLI's `send stats <list>`, which resolves a list's most recent
  send when you don't have the send id handy.

## [0.2.5] - 2026-07-29

### Changed
- The engagement events-pull cron now considers only `type = 'bulk'` sends.
  Single (transactional/welcome) sends are no longer polled against Mailgun's
  events API, so they stop spinning up a per-send 30-day pull schedule and no
  longer fold opens/clicks into the message-engagement rollups. Mailgun-side
  open/click tracking on those emails is unchanged; we simply don't ingest it.
  Existing single sends drop out of the candidate set automatically (no
  backfill or schema change).

## [0.2.4] - 2026-07-28

### Fixed
- `POST /send/bulk` no longer runs the first batch inline. Building recipient
  variables, signing tokens, and calling Mailgun inside the creation request
  made it the heaviest single invocation in the worker, so a borderline
  creation could exceed the isolate resource limit and return Cloudflare
  Error 1102 to the caller (even though the send itself then self-healed via
  the watchdog). The handler now returns 202 immediately after `createSend`
  and kicks the first batch on the self-call chain (`scheduleNextStep`), with
  the every-minute cron watchdog as the safety net if that kick drops. The
  created send is reported as `queued`.

### Changed
- `buildBody` renders the markdown once and derives the plain-text part from
  the already-rendered HTML instead of parsing the source a second time,
  halving the markdown-parse cost of the creation request.

## [0.2.3] - 2026-07-17

### Changed
- Raised the bulk `batch_size` cap 100 -> 200 (`MAX_BATCH_SIZE`) for better
  throughput. The self-heal floor stays at 100, so if an isolate ever trips
  the CPU limit at 200 the watchdog still walks it back down to a safe size.
- The inter-batch throttle is now governed worker-side: `createSend` clamps
  `throttle_ms` to `MAX_THROTTLE_MS` (500ms). Callers may request a shorter
  delay, but larger values are capped, so the operative pace is controlled
  here rather than by a caller's hard-coded default (the CLI still emits its
  own default on every send). Default when unspecified is also 500ms.
- CLI `--batch-size` default 100 -> 200 and `--throttle-ms` default 1000 -> 500
  (`cli` and `src` send commands); MCP tool doc updated.

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
