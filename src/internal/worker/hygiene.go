package worker

import (
	"context"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

// Phase 5: persistent throttle key/window matching the Worker side
// (worker/src/send/auto_prune.ts). The in-process time.Ticker is robust
// to a single restart but not to crash-loops or rolling deploys, so
// piggybacking on the worker_state table protects against re-firing
// auto-prune within 24h across process boundaries.
const (
	autoPruneStateKey         = "auto_prune_last_run_ms"
	autoPruneMinIntervalMS    = int64(24 * 60 * 60 * 1000)
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
	// Phase 5 throttle: even with the 24h ticker, a crash-loop or rolling
	// restart could re-fire on every boot. worker_state persists the last
	// run across process boundaries.
	nowMs := time.Now().UnixMilli()
	if last, ok, err := m.store.GetStateInt64(ctx, autoPruneStateKey); err != nil {
		m.log.Warn("auto-prune: throttle read", "err", err)
	} else if ok && nowMs-last < autoPruneMinIntervalMS {
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
	// Persist the run timestamp BEFORE iterating lists, matching the
	// Worker side. A crash midway means we skip the next cycle (safer
	// than re-running and re-auditing).
	if err := m.store.SetStateInt64(ctx, autoPruneStateKey, nowMs); err != nil {
		m.log.Warn("auto-prune: throttle write", "err", err)
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
