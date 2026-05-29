import { describe, expect, it } from 'vitest';
import { ARCHIVE_MAX_AGE_MS, nextDueAt } from '../src/send/events_pull';

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;

describe('nextDueAt', () => {
  const created = 1_700_000_000_000;

  it('returns the burst offsets for pulls 0..3', () => {
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 0, events_last_pulled_at_ms: null }),
    ).toBe(created);
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 1, events_last_pulled_at_ms: created }),
    ).toBe(created + 1 * HOUR);
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 2, events_last_pulled_at_ms: created + 1 * HOUR }),
    ).toBe(created + 6 * HOUR);
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 3, events_last_pulled_at_ms: created + 6 * HOUR }),
    ).toBe(created + 24 * HOUR);
  });

  it('uses last_pulled + 24h once burst is exhausted', () => {
    const last = created + 25 * HOUR;
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 4, events_last_pulled_at_ms: last }),
    ).toBe(last + DAY);
  });

  it('continues stacking 24h beats during the daily phase', () => {
    const last = created + 10 * DAY;
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 10, events_last_pulled_at_ms: last }),
    ).toBe(last + DAY);
  });

  it('returns null once the next beat would fall past the archive window', () => {
    const last = created + ARCHIVE_MAX_AGE_MS - 1 * HOUR;
    expect(
      nextDueAt({ created_at_ms: created, events_pulls_count: 20, events_last_pulled_at_ms: last }),
    ).toBeNull();
  });

  it('falls back to now when daily phase is reached with a null watermark', () => {
    const before = Date.now();
    const v = nextDueAt({
      created_at_ms: created,
      events_pulls_count: 4,
      events_last_pulled_at_ms: null,
    })!;
    expect(v).toBeGreaterThanOrEqual(before);
    expect(v).toBeLessThanOrEqual(Date.now() + 10);
  });
});
