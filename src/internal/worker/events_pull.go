package worker

import (
	"context"
	"errors"
	"time"

	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/store"
)

// ---------------------------------------------------------------------------
// Schedule
// ---------------------------------------------------------------------------

// BurstOffsets — relative to send.created_at, pull at these offsets.
var burstOffsetsMS = []int64{
	0,
	1 * 60 * 60 * 1000,         // +1h
	6 * 60 * 60 * 1000,         // +6h
	24 * 60 * 60 * 1000,        // +24h
}

const (
	// After the burst is exhausted, pull every DailyIntervalMS until the
	// archive window closes.
	DailyIntervalMS = 24 * 60 * 60 * 1000

	// ArchiveMaxAgeMS — total archive window, anchored to send.created_at.
	// 30 days matches Mailgun's paid-tier retention; beyond it there's
	// nothing pullable that we haven't already seen.
	ArchiveMaxAgeMS int64 = 30 * 24 * 60 * 60 * 1000

	// OverlapMS — every pull re-fetches the last 6h of events. The
	// UNIQUE(mailgun_event_id) constraint dedupes; this gives us margin
	// against Mailgun's out-of-order event arrival.
	OverlapMS int64 = 6 * 60 * 60 * 1000

	// maxPagesPerSend — hard cap on pages fetched per send per beat.
	// Prevents one cron tick from saturating after a long Mailgun outage.
	maxPagesPerSend = 50
)

// NextEventPullDueAt returns the timestamp of the next scheduled pull for
// the given send. Returns (0, true) if the send is past its archive window
// (caller should freeze).
//
// Burst phase (pulls 0..3): use the burstOffsetsMS lookup.
// Daily phase (pulls 4+):   last_pulled_at + 24h, capped by the window.
//
// Anchored to created_at (not completed_at) so the cron can dispatch
// without knowing lifecycle state.
func NextEventPullDueAt(row store.DueEventPullRow) (dueAtMs int64, frozen bool) {
	if row.EventsPullsCount < int64(len(burstOffsetsMS)) {
		return row.CreatedAtMs + burstOffsetsMS[row.EventsPullsCount], false
	}
	if !row.EventsLastPulledAtMs.Valid {
		// Defensive: pulls_count >= 4 implies a prior pull. If somehow not,
		// treat as "due now."
		return time.Now().UnixMilli(), false
	}
	next := row.EventsLastPulledAtMs.Int64 + DailyIntervalMS
	if next > row.CreatedAtMs+ArchiveMaxAgeMS {
		return 0, true
	}
	return next, false
}

// ---------------------------------------------------------------------------
// Scheduler
// ---------------------------------------------------------------------------

// RunEventsArchiveScheduler wakes every interval, finds sends with events
// due to be pulled, and ingests their events from Mailgun. Blocks until
// ctx is cancelled. No-ops when cfg.EventsArchiveEnabled is false — the
// caller (cmd/serve.go) still spawns the goroutine so flipping the env
// var doesn't require a restart of the rest of the manager.
func (m *Manager) RunEventsArchiveScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	m.pullEventsOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.pullEventsOnce(ctx)
		}
	}
}

// pullEventsOnce is the cron tick. Short-circuits when the feature flag
// is off so an operator can ship dormant in Phase 1, leave the scheduler
// running, and activate by setting EVENTS_ARCHIVE_ENABLED=true.
func (m *Manager) pullEventsOnce(ctx context.Context) {
	if !m.cfg.EventsArchiveEnabled {
		return
	}
	const batchSize = 20
	nowMs := time.Now().UnixMilli()
	candidates, err := m.store.ListDueEventPulls(ctx, nowMs, ArchiveMaxAgeMS, batchSize*2)
	if err != nil {
		m.log.Warn("events-pull: list candidates", "err", err)
		return
	}
	picked := make([]store.DueEventPullRow, 0, batchSize)
	for _, c := range candidates {
		if len(picked) >= batchSize {
			break
		}
		dueAt, frozen := NextEventPullDueAt(c)
		if frozen {
			// Past the window but still wasn't frozen in the DB — run one
			// last pull to flush remaining events, after which the freeze
			// logic in RecordEventPullProgress will mark archive_complete.
			picked = append(picked, c)
			continue
		}
		if dueAt <= nowMs {
			picked = append(picked, c)
		}
	}
	for _, snd := range picked {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := m.pullEventsForOneSend(ctx, snd); err != nil {
			m.log.Warn("events-pull: send", "send_id", snd.SendID, "err", err)
			if recordErr := m.store.RecordEventPullError(ctx, snd.SendID, err.Error()); recordErr != nil {
				m.log.Warn("events-pull: record error", "send_id", snd.SendID, "err", recordErr)
			}
		}
	}
}

// PullEventsForOneSend (exported for use by HTTP/MCP/CLI on-demand triggers)
// pulls Mailgun events for one send and updates the per-send watermark.
// Idempotent — calling it twice with no time elapsed is safe (the UNIQUE
// constraint on mailgun_event_id deduplicates).
func (m *Manager) PullEventsForOneSend(ctx context.Context, sendID string) (inserted int64, pages int, frozen bool, err error) {
	nowMs := time.Now().UnixMilli()
	candidates, err := m.store.ListDueEventPulls(ctx, nowMs, ArchiveMaxAgeMS, 1)
	if err != nil {
		return 0, 0, false, err
	}
	for _, c := range candidates {
		if c.SendID == sendID {
			return m.pullEventsForOneSendInternal(ctx, c)
		}
	}
	// The send isn't in the due-set (might be frozen, test_mode, or out
	// of window). Synthesize a minimal row so the caller still gets a
	// best-effort attempt — useful for diagnostic tools.
	listID, _ := m.store.GetSendListID(ctx, sendID)
	_ = listID
	return 0, 0, false, errors.New("send not eligible for archive (frozen / test_mode / out of window / missing domain)")
}

func (m *Manager) pullEventsForOneSend(ctx context.Context, snd store.DueEventPullRow) error {
	_, _, _, err := m.pullEventsForOneSendInternal(ctx, snd)
	return err
}

func (m *Manager) pullEventsForOneSendInternal(ctx context.Context, snd store.DueEventPullRow) (inserted int64, pages int, frozen bool, err error) {
	nowMs := time.Now().UnixMilli()
	beginMs := snd.CreatedAtMs
	if snd.EventsLastPulledThroughMs.Valid {
		beginMs = snd.EventsLastPulledThroughMs.Int64
	}
	beginMs -= OverlapMS
	endMs := nowMs

	listID, err := m.store.GetSendListID(ctx, snd.SendID)
	if err != nil {
		return 0, 0, false, err
	}

	var page *mailgun.EventsPage
	var pageURL string
	for pages < maxPagesPerSend {
		select {
		case <-ctx.Done():
			return inserted, pages, false, ctx.Err()
		default:
		}
		if pageURL == "" {
			page, err = m.mailgun.FetchEvents(ctx, mailgun.FetchEventsParams{
				Domain:  snd.FromDomain,
				Tag:     snd.SendID,
				BeginMs: beginMs,
				EndMs:   endMs,
				Limit:   300,
			})
		} else {
			page, err = m.mailgun.FetchEventsPage(ctx, pageURL)
		}
		if err != nil {
			return inserted, pages, false, err
		}
		pages++
		if len(page.Items) == 0 {
			break
		}
		for _, raw := range page.Items {
			ev, ok := store.NormalizeEvent(raw, snd.FromDomain, snd.SendID)
			if !ok {
				continue
			}
			res, insertErr := m.store.InsertEventIfNew(ctx, ev)
			if insertErr != nil {
				m.log.Warn("events-pull: insert", "send_id", snd.SendID, "mailgun_event_id", ev.MailgunEventID, "err", insertErr)
				continue
			}
			if !res.Inserted {
				continue
			}
			inserted++
			if listID != "" && res.ContactID.Valid {
				if engErr := m.store.ApplyEventToEngagement(ctx, res.ContactID.String, listID, ev.Event, ev.EventTimestampMs); engErr != nil {
					m.log.Warn("events-pull: engagement upsert", "send_id", snd.SendID, "contact_id", res.ContactID.String, "err", engErr)
				}
			}
		}
		if page.Paging.Next == "" || page.Paging.Next == pageURL {
			break
		}
		pageURL = page.Paging.Next
	}

	frozen = nowMs >= snd.CreatedAtMs+ArchiveMaxAgeMS

	if progressErr := m.store.RecordEventPullProgress(ctx, snd.SendID, store.EventPullProgress{
		LastPulledAtMs:      nowMs,
		LastPulledThroughMs: endMs - OverlapMS,
		Inserted:            inserted,
		Freeze:              frozen,
	}); progressErr != nil {
		return inserted, pages, frozen, progressErr
	}
	return inserted, pages, frozen, nil
}
