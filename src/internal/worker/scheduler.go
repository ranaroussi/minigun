package worker

import (
	"context"
	"time"
)

// RunScheduledSendDispatcher wakes every interval and dispatches any
// future-dated sends whose send_at has arrived, handing each to the normal
// run path (which flips it from 'scheduled' to 'running'). Blocks until ctx
// is cancelled. Scheduling granularity is therefore bounded by interval —
// fine for email, where minute-level precision is plenty.
func (m *Manager) RunScheduledSendDispatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	m.dispatchDueSendsOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.dispatchDueSendsOnce(ctx)
		}
	}
}

func (m *Manager) dispatchDueSendsOnce(ctx context.Context) {
	ids, err := m.store.ListDueScheduledSends(ctx, 100)
	if err != nil {
		m.log.Warn("list due scheduled sends", "err", err)
		return
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Start guards against double-dispatch via its running map, so a
		// send already picked up on a prior tick is a no-op error we ignore.
		if err := m.Start(ctx, id); err != nil {
			m.log.Debug("dispatch scheduled send skipped", "send_id", id, "err", err)
		}
	}
}
