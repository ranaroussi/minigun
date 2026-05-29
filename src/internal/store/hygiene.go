package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// List hygiene — prune candidate query + executor
// ---------------------------------------------------------------------------

// PruneCriteria narrows what counts as a prune-eligible contact. The three
// signals are independent — a contact matches when ANY enabled criterion
// holds. Zero values disable the corresponding criterion (so an empty
// PruneCriteria matches nothing, which is the safe default).
//
//   MinMessagesSinceEngagement
//     "we've delivered N or more messages without an open/click."
//     Reads contact_engagement.messages_since_last_engagement.
//
//   DormantForMs
//     "the contact's last open/click is older than now - DormantForMs"
//     (a duration in ms, since SQLite stores last_engagement_at_ms as an
//     integer). Contacts with NULL last_engagement_at_ms are excluded by
//     this criterion (they've never engaged — covered by the next one).
//
//   NoDeliveryForMs
//     "the contact has been on the list but never had a delivered event
//     in the last NoDeliveryForMs window." Useful for identifying
//     never-engaged cohorts where the contact may not even be receiving
//     mail (Mailgun is rejecting at the gateway).
type PruneCriteria struct {
	MinMessagesSinceEngagement int64
	DormantForMs               int64
	NoDeliveryForMs            int64
}

// HasAny reports whether at least one criterion is enabled. Callers use this
// to fail-closed when no thresholds were set — pruning an entire list with
// no criteria would be catastrophic.
func (p PruneCriteria) HasAny() bool {
	return p.MinMessagesSinceEngagement > 0 || p.DormantForMs > 0 || p.NoDeliveryForMs > 0
}

// PruneCandidate is one matching row from the candidates query. The
// criterion fields record WHICH thresholds the candidate breached, so the
// executor can pick the most specific reason value to audit with.
type PruneCandidate struct {
	SubscriptionID                int64
	ContactID                     string
	Email                         string
	MessagesSinceLastEngagement   int64
	LastEngagementAtMs            sql.NullInt64
	LastDeliveredAtMs             sql.NullInt64
	TotalDelivered                int64
	MatchedByCount                bool
	MatchedByRecency              bool
	MatchedByNoDelivery           bool
}

// Reason returns the audit string for the most specific matched criterion.
// Order is intentional: count > recency > no-delivery. Count is the most
// actionable signal (we know exactly how many wasted deliveries happened);
// recency is the next clearest; no-delivery is the catch-all for cohorts
// that never engaged in any way.
func (p PruneCandidate) Reason() string {
	switch {
	case p.MatchedByCount:
		return "auto-prune-by-count"
	case p.MatchedByRecency:
		return "auto-prune-by-recency"
	case p.MatchedByNoDelivery:
		return "auto-prune-by-no-delivery"
	default:
		return "auto-prune"
	}
}

// ListPruneCandidatesParams is the input to ListPruneCandidates. NowMs is
// passed by the caller so tests can pin time; the executor uses time.Now
// internally if the field is zero.
type ListPruneCandidatesParams struct {
	ListID   string
	Criteria PruneCriteria
	NowMs    int64
	Limit    int
}

// ListPruneCandidates returns currently-subscribed contacts on `listID`
// that match at least one of the enabled criteria.
//
// The query joins subscriptions → contacts → LEFT JOIN contact_engagement.
// The LEFT JOIN is critical: contacts with no engagement row at all
// (never been delivered to) should still match the no-delivery criterion.
// The criterion predicates handle the NULL cases explicitly.
//
// The hygiene tool's safety contract is "never prune more than `limit`
// in one call." The default is 1000 — small enough to keep a dry-run
// response cheap to render, large enough to be useful for normal cleanup.
func (s *Store) ListPruneCandidates(ctx context.Context, p ListPruneCandidatesParams) ([]PruneCandidate, error) {
	if !p.Criteria.HasAny() {
		return nil, errors.New("ListPruneCandidates: at least one criterion is required")
	}
	if p.ListID == "" {
		return nil, errors.New("ListPruneCandidates: list_id is required")
	}
	nowMs := p.NowMs
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}

	// Build the criterion predicates dynamically. Each enabled criterion
	// adds one OR'd clause and also adds a SELECT expression we read back
	// into the MatchedBy* fields so the caller knows WHY this row matched.
	preds := []string{}
	args := []any{p.ListID}

	// We always SELECT three flag columns (matched_by_*). When a criterion
	// is disabled the flag is "0" literal; when enabled it's the
	// criterion predicate. Keeping the SELECT shape stable means scanning
	// rows is simple.
	flagCount := "0 AS matched_by_count"
	flagRecency := "0 AS matched_by_recency"
	flagNoDelivery := "0 AS matched_by_no_delivery"

	// Each flag expression is wrapped in COALESCE so the LEFT JOIN's NULL
	// rows produce a clean 0/1 — without this, `NULL >= 10` would leak
	// NULL into the SELECT and scan would fail on the integer column.
	if p.Criteria.MinMessagesSinceEngagement > 0 {
		preds = append(preds, "(COALESCE(ce.messages_since_last_engagement, 0) >= ?)")
		args = append(args, p.Criteria.MinMessagesSinceEngagement)
		flagCount = "(COALESCE(ce.messages_since_last_engagement, 0) >= ?) AS matched_by_count"
	}
	if p.Criteria.DormantForMs > 0 {
		cutoff := nowMs - p.Criteria.DormantForMs
		preds = append(preds, "(ce.last_engagement_at_ms IS NOT NULL AND ce.last_engagement_at_ms < ?)")
		args = append(args, cutoff)
		flagRecency = "(CASE WHEN ce.last_engagement_at_ms IS NOT NULL AND ce.last_engagement_at_ms < ? THEN 1 ELSE 0 END) AS matched_by_recency"
	}
	if p.Criteria.NoDeliveryForMs > 0 {
		cutoff := nowMs - p.Criteria.NoDeliveryForMs
		// "subscribed before the cutoff AND (no engagement row OR last_delivered_at_ms < cutoff)"
		// We anchor on subscribed_at because a contact added yesterday hasn't
		// had time to receive anything — pruning them as "never delivered"
		// would be a false positive.
		preds = append(preds, "(CAST(strftime('%s', subs.subscribed_at) AS INTEGER) * 1000 < ? AND (ce.contact_id IS NULL OR ce.last_delivered_at_ms IS NULL OR ce.last_delivered_at_ms < ?))")
		args = append(args, cutoff, cutoff)
		flagNoDelivery = "(CASE WHEN CAST(strftime('%s', subs.subscribed_at) AS INTEGER) * 1000 < ? AND (ce.contact_id IS NULL OR ce.last_delivered_at_ms IS NULL OR ce.last_delivered_at_ms < ?) THEN 1 ELSE 0 END) AS matched_by_no_delivery"
	}

	// Re-bind the flag SELECT args (they share the same threshold values
	// as the WHERE preds — the SELECT happens FIRST in the SQL evaluation
	// order so the args list is: list_id, [select flag args...], [where args...]).
	// We rebuild here to keep the binding order obvious and correct.
	selectArgs := []any{}
	if p.Criteria.MinMessagesSinceEngagement > 0 {
		selectArgs = append(selectArgs, p.Criteria.MinMessagesSinceEngagement)
	}
	if p.Criteria.DormantForMs > 0 {
		selectArgs = append(selectArgs, nowMs-p.Criteria.DormantForMs)
	}
	if p.Criteria.NoDeliveryForMs > 0 {
		selectArgs = append(selectArgs, nowMs-p.Criteria.NoDeliveryForMs, nowMs-p.Criteria.NoDeliveryForMs)
	}
	finalArgs := append([]any{}, selectArgs...)
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, limit)

	query := `
		SELECT
		  subs.id, subs.contact_id, c.email,
		  COALESCE(ce.messages_since_last_engagement, 0),
		  ce.last_engagement_at_ms,
		  ce.last_delivered_at_ms,
		  COALESCE(ce.total_delivered, 0),
		  ` + flagCount + `,
		  ` + flagRecency + `,
		  ` + flagNoDelivery + `
		FROM subscriptions subs
		JOIN contacts c ON c.id = subs.contact_id
		LEFT JOIN contact_engagement ce ON ce.contact_id = subs.contact_id AND ce.list_id = subs.list_id
		WHERE subs.list_id = ?
		  AND subs.subscribed = 1
		  AND (` + strings.Join(preds, " OR ") + `)
		ORDER BY ce.messages_since_last_engagement DESC, subs.id ASC
		LIMIT ?`

	rows, err := s.DB.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PruneCandidate
	for rows.Next() {
		var c PruneCandidate
		var matchedCount, matchedRecency, matchedNoDelivery int
		if err := rows.Scan(
			&c.SubscriptionID, &c.ContactID, &c.Email,
			&c.MessagesSinceLastEngagement,
			&c.LastEngagementAtMs, &c.LastDeliveredAtMs,
			&c.TotalDelivered,
			&matchedCount, &matchedRecency, &matchedNoDelivery,
		); err != nil {
			return nil, err
		}
		c.MatchedByCount = matchedCount == 1
		c.MatchedByRecency = matchedRecency == 1
		c.MatchedByNoDelivery = matchedNoDelivery == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// PruneListResult summarizes a prune run for the HTTP / CLI response.
type PruneListResult struct {
	ListID       string           `json:"list_id"`
	DryRun       bool             `json:"dry_run"`
	Candidates   int              `json:"candidates"`
	Unsubscribed int              `json:"unsubscribed"`
	Sample       []PruneCandidate `json:"sample"`
	ReasonCounts map[string]int   `json:"reason_counts"`
}

// PruneList runs the prune workflow against `listID`. When DryRun is true,
// returns the candidates without modifying any rows. Otherwise iterates
// candidates, unsubscribes each subscription, and writes an
// unsubscribe_events row with the most specific matched reason.
//
// Idempotent: running again immediately afterward yields zero candidates
// because the WHERE clause requires subscribed=1.
//
// Bounded: each call processes at most `limit` candidates. Callers
// wanting larger batches must loop and call again.
func (s *Store) PruneList(ctx context.Context, p ListPruneCandidatesParams, dryRun bool, sampleSize int) (*PruneListResult, error) {
	candidates, err := s.ListPruneCandidates(ctx, p)
	if err != nil {
		return nil, err
	}
	if sampleSize <= 0 {
		sampleSize = 25
	}
	if sampleSize > len(candidates) {
		sampleSize = len(candidates)
	}
	reasonCounts := map[string]int{}
	for _, c := range candidates {
		reasonCounts[c.Reason()]++
	}
	result := &PruneListResult{
		ListID:       p.ListID,
		DryRun:       dryRun,
		Candidates:   len(candidates),
		Unsubscribed: 0,
		Sample:       candidates[:sampleSize],
		ReasonCounts: reasonCounts,
	}
	if dryRun {
		return result, nil
	}
	// Apply: atomically unsubscribe + audit each candidate in one tx.
	// Phase 5 fix for H4 — the Phase 4 implementation used two separate
	// calls (UnsubscribeSubscription, then RecordUnsubscribeEventWithReason),
	// which left a window where the unsubscribe could commit and the
	// audit insert could fail, leaving an unsubscribe without a reason
	// audit row.
	for _, c := range candidates {
		if _, _, err := s.UnsubscribeAndAudit(ctx, p.ListID, c.ContactID, c.Email, c.Reason()); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return result, err
		}
		result.Unsubscribed++
	}
	return result, nil
}
