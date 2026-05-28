package worker

import (
	"context"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

// RunAutoPruneScheduler ticks once per `interval` and runs the configured
// prune thresholds against every list. No-ops when
// cfg.ListHygieneAutoPruneEnabled is false. Conservative by design — see
// the config struct for default thresholds.
//
// Intentional design choices:
//   - Single shared threshold across all lists. Per-list customization
//     belongs in the manual surface (POST /lists/{id}/prune); the cron is
//     for "I've audited my defaults and trust them for everything."
//   - Bounded per-call (Limit defaults to 1000). A list with massive
//     accumulated dormancy will need multiple ticks to drain, which is
//     intentional: gives operators time to spot anomalies in audit logs.
//   - Skips lists with zero engagement data — the criterion query already
//     handles this via LEFT JOIN + NULL semantics; no extra guard needed.
func (m *Manager) RunAutoPruneScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if !m.cfg.ListHygieneAutoPruneEnabled {
		return
	}
	m.runAutoPruneOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.runAutoPruneOnce(ctx)
		}
	}
}

func (m *Manager) runAutoPruneOnce(ctx context.Context) {
	if !m.cfg.ListHygieneAutoPruneEnabled {
		return
	}
	const dayMs = int64(24 * 60 * 60 * 1000)
	criteria := store.PruneCriteria{
		MinMessagesSinceEngagement: m.cfg.ListHygieneAutoPruneByCount,
		DormantForMs:               m.cfg.ListHygieneAutoPruneByRecencyDays * dayMs,
		NoDeliveryForMs:            m.cfg.ListHygieneAutoPruneNoDeliveryDays * dayMs,
	}
	if !criteria.HasAny() {
		m.log.Info("auto-prune: no thresholds set, skipping")
		return
	}
	lists, err := m.store.ListLists(ctx)
	if err != nil {
		m.log.Warn("auto-prune: list lists", "err", err)
		return
	}
	for _, l := range lists {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res, err := m.store.PruneList(ctx, store.ListPruneCandidatesParams{
			ListID:   l.ID,
			Criteria: criteria,
			Limit:    1000,
		}, false, 0)
		if err != nil {
			m.log.Warn("auto-prune: list", "list_id", l.ID, "err", err)
			continue
		}
		if res.Unsubscribed > 0 {
			m.log.Info("auto-prune: unsubscribed",
				"list_id", l.ID,
				"list_slug", l.Slug,
				"unsubscribed", res.Unsubscribed,
				"candidates", res.Candidates,
			)
		}
	}
}
