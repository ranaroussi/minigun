package worker

import (
	"context"
	"database/sql"
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
// Idempotent in practice — each pull begins strictly after the previous
// pull's highest event timestamp, so re-running with no time elapsed
// fetches an empty window.
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
	// Incremental window: begin strictly AFTER the highest event
	// timestamp the previous pull processed (default: the send's
	// created_at on the first pull). Because Mailgun returns events in
	// time order and we advance the watermark to the last event we saw,
	// the next pull never re-fetches an already-counted event — so the
	// per-call counter increments in the Apply* methods stay correct
	// without any dedup ledger. The +1ms makes the lower bound exclusive
	// at storage (ms) granularity.
	beginMs := snd.CreatedAtMs
	if snd.EventsLastPulledThroughMs.Valid {
		beginMs = snd.EventsLastPulledThroughMs.Int64 + 1
	}
	endMs := nowMs

	listID, err := m.store.GetSendListID(ctx, snd.SendID)
	if err != nil {
		return 0, 0, false, err
	}
	listNS := sql.NullString{String: listID, Valid: listID != ""}

	// lastEventTs tracks the highest event_timestamp_ms processed in this
	// batch — the next pull resumes strictly after it. When maxPagesPerSend
	// caps the loop with pages remaining we still advance to lastEventTs
	// (forward progress); the next beat continues from there.
	var lastEventTs int64
	cappedWithMorePages := false
	// contactCache memoizes email→contact_id within this send so a
	// recipient with many events triggers one lookup, not one per event.
	contactCache := map[string]sql.NullString{}

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
			ev, ok := store.NormalizeEvent(raw)
			if !ok {
				continue
			}
			if ev.EventTimestampMs > lastEventTs {
				lastEventTs = ev.EventTimestampMs
			}
			contactID, cached := contactCache[ev.Recipient]
			if !cached {
				contactID, err = m.store.LookupContactIDByEmail(ctx, ev.Recipient)
				if err != nil {
					return inserted, pages, false, err
				}
				contactCache[ev.Recipient] = contactID
			}
			if !contactID.Valid {
				// Event for an address we never stored — nothing to roll up.
				continue
			}
			inserted++
			// Fold into both engagement tiers. cme (per-send, per-contact
			// detail) covers more event types than the per-list rollup.
			if isMessageEvent(ev.Event) {
				if cmeErr := m.store.ApplyEventToMessageEngagement(ctx, snd.SendID, contactID.String, listNS, ev.Event, ev.EventTimestampMs/1000, ev.Severity, ev.Reason); cmeErr != nil {
					return inserted, pages, false, cmeErr
				}
			}
			if listID != "" && isEngagementEvent(ev.Event) {
				if engErr := m.store.ApplyEventToEngagement(ctx, contactID.String, listID, ev.Event, ev.EventTimestampMs); engErr != nil {
					return inserted, pages, false, engErr
				}
			}
		}
		if page.Paging.Next == "" || page.Paging.Next == pageURL {
			break
		}
		pageURL = page.Paging.Next
		if pages >= maxPagesPerSend {
			// Exiting with an outstanding next page — don't freeze even
			// if we're past the window; the next beat continues paging.
			cappedWithMorePages = true
		}
	}

	// Watermark advance: to lastEventTs when we processed anything,
	// otherwise hold at beginMs-1 (the through-point already covered) so
	// an empty window doesn't skip the lower bound forward past unseen
	// events.
	newThroughMs := beginMs - 1
	if lastEventTs > 0 {
		newThroughMs = lastEventTs
	}

	frozen = !cappedWithMorePages && nowMs >= snd.CreatedAtMs+ArchiveMaxAgeMS

	if progressErr := m.store.RecordEventPullProgress(ctx, snd.SendID, store.EventPullProgress{
		LastPulledAtMs:      nowMs,
		LastPulledThroughMs: newThroughMs,
		Inserted:            inserted,
		Freeze:              frozen,
	}); progressErr != nil {
		return inserted, pages, frozen, progressErr
	}
	return inserted, pages, frozen, nil
}

// isEngagementEvent reports whether the event type drives an update to
// the per-(contact, list) contact_engagement summary.
func isEngagementEvent(eventType string) bool {
	switch eventType {
	case "delivered", "opened", "clicked":
		return true
	default:
		return false
	}
}

// isMessageEvent reports whether the event type updates the per-(send,
// contact) message engagement row. Broader than isEngagementEvent (the
// per-list rollup gate): cme also records acceptance, failure, complaint,
// and unsubscribe so a single message's full lifecycle is queryable.
func isMessageEvent(eventType string) bool {
	switch eventType {
	case "accepted", "delivered", "opened", "clicked", "failed", "rejected", "complained", "unsubscribed":
		return true
	default:
		return false
	}
}


