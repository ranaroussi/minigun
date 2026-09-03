package worker

import (
	"context"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

var statsPollOffsets = []time.Duration{
	0,
	1 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
	48 * time.Hour,
	120 * time.Hour,
}

// NextStatsFetch returns the next time we should poll Mailgun for a send's
// stats, or (zero, true) if all six offsets have already elapsed. Exported
// so the API's force-refresh path can advance the same schedule the cron
// uses when it persists a forced fetch.
func NextStatsFetch(completedAt time.Time, now time.Time) (time.Time, bool) {
	elapsed := now.Sub(completedAt)
	for _, off := range statsPollOffsets {
		if off > elapsed {
			return completedAt.Add(off), false
		}
	}
	return time.Time{}, true
}

// RunStatsScheduler wakes every interval, finds sends with stats due, and
// updates send_stats with fresh Mailgun counts. Blocks until ctx is cancelled.
func (m *Manager) RunStatsScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	m.refreshStatsOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshStatsOnce(ctx)
		}
	}
}

func (m *Manager) refreshStatsOnce(ctx context.Context) {
	due, err := m.store.ListDueSendStats(ctx, 50)
	if err != nil {
		m.log.Warn("list due send_stats", "err", err)
		return
	}
	for _, r := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := m.refreshOneSendStats(ctx, r.SendID, r.CompletedAt); err != nil {
			m.log.Warn("refresh send stats", "send_id", r.SendID, "err", err)
		}
	}
}

func (m *Manager) refreshOneSendStats(ctx context.Context, sendID string, completedAt time.Time) error {
	snd, err := m.store.GetSend(ctx, sendID)
	if err != nil {
		return err
	}
	now := time.Now()
	nextFetch, isFinal := NextStatsFetch(completedAt, now)

	totals, mgErr := m.mailgun.PerSendMetrics(ctx, snd.ID, snd.CreatedAt)
	if mgErr != nil {
		return m.store.RecordStatsFetchError(ctx, sendID, &nextFetch, isFinal, mgErr.Error())
	}
	return m.store.ApplyMailgunStats(ctx, sendID, store.SendStatsUpdate{
		Sent:       totals.Sent,
		Delivered:  totals.Delivered,
		Opened:     totals.Opened,
		Clicked:    totals.Clicked,
		Failed:     totals.Failed,
		Complained: totals.Complained,
		NextFetch:  &nextFetch,
		IsFinal:    isFinal,
	})
}
