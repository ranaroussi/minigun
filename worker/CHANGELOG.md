# Changelog

All notable changes to the MiniGun Worker are documented here. Versions are
tagged `worker/vX.Y.Z` and follow [Semantic Versioning](https://semver.org/).

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
