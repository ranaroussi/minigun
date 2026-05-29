package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/ranaroussi/minigun/internal/mailgun"
)

// DueEventPullRow is the per-send candidate row returned by ListDueEventPulls.
// Carries enough to construct the Mailgun events URL + run nextDueAt without
// re-fetching the send.
type DueEventPullRow struct {
	SendID                       string
	FromDomain                   string
	CreatedAtMs                  int64
	EventsPullsCount             int64
	EventsLastPulledAtMs         sql.NullInt64
	EventsLastPulledThroughMs    sql.NullInt64
}

// MessageEngagement mirrors a row of contact_message_engagement — the
// per-(send, contact) message detail tier (Phase 6). Timestamps are
// epoch SECONDS (see migration 00010 for the unit-split rationale).
type MessageEngagement struct {
	SendID          string
	ContactID       string
	ListID          sql.NullString
	SentAt          sql.NullInt64
	DeliveredAt     sql.NullInt64
	FirstOpenAt     sql.NullInt64
	LastOpenAt      sql.NullInt64
	TotalOpens      int64
	FirstClickAt    sql.NullInt64
	LastClickAt     sql.NullInt64
	TotalClicks     int64
	Failed          int64
	FailedAt        sql.NullInt64
	FailureSeverity sql.NullString
	FailureReason   sql.NullString
	ComplainedAt    sql.NullInt64
	UnsubscribedAt  sql.NullInt64
	RepliedAt       sql.NullInt64
	UpdatedAt       int64
}

// MessageClick mirrors a row of contact_message_clicks — the per-URL
// click rollup for a (send, contact). Timestamps are epoch SECONDS, like
// MessageEngagement. URL is canonical (see canonicalizeClickURL).
type MessageClick struct {
	SendID       string
	ContactID    string
	ListID       sql.NullString
	URL          string
	FirstClickAt sql.NullInt64
	LastClickAt  sql.NullInt64
	TotalClicks  int64
	UpdatedAt    int64
}

// ContactEngagement mirrors the row in contact_engagement. Used by both
// the engagement-summary maintenance code (Phase 2) and the auto-prune
// query (Phase 4).
type ContactEngagement struct {
	ContactID                    string
	ListID                       string
	LastDeliveredAtMs            sql.NullInt64
	LastOpenAtMs                 sql.NullInt64
	LastClickAtMs                sql.NullInt64
	LastEngagementAtMs           sql.NullInt64
	TotalDelivered               int64
	TotalOpens                   int64
	TotalClicks                  int64
	MessagesSinceLastEngagement  int64
	UpdatedAt                    time.Time
}

// ---------------------------------------------------------------------------
// Cron-helper queries
// ---------------------------------------------------------------------------

// ListDueEventPulls returns sends that are candidates for the events-pull
// cron — i.e., non-frozen, non-test sends with a sending_domain. The
// burst-vs-daily schedule logic and the past-the-window freeze decision
// are computed in the worker layer (see worker/events_pull.go nextDueAt);
// the SQL just narrows candidates so the downstream filter has a small
// set to look at.
//
// Intentionally does NOT filter by age — sends past ArchiveMaxAgeMS still
// need one final pull so the worker layer can set events_archive_complete=1
// and freeze them. Filtering on age in SQL (Phase 2 bug) left aged-out
// sends in events_archive_complete=0 forever and silently dropped any
// tail events from the final window.
//
// nowMs is accepted for signature compatibility with callers that still
// pass it. maxAgeMs is unused.
func (s *Store) ListDueEventPulls(ctx context.Context, nowMs int64, maxAgeMs int64, limit int) ([]DueEventPullRow, error) {
	_ = nowMs
	_ = maxAgeMs
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT
		  id,
		  sending_domain,
		  CAST(strftime('%s', created_at) AS INTEGER) * 1000,
		  events_pulls_count,
		  events_last_pulled_at_ms,
		  events_last_pulled_through_ms
		FROM sends
		WHERE events_archive_complete = 0
		  AND test_mode = 0
		  AND status IN ('completed', 'failed', 'cancelled', 'running')
		  AND sending_domain != ''
		ORDER BY COALESCE(events_last_pulled_at_ms, 0) ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueEventPullRow
	for rows.Next() {
		var r DueEventPullRow
		if err := rows.Scan(
			&r.SendID,
			&r.FromDomain,
			&r.CreatedAtMs,
			&r.EventsPullsCount,
			&r.EventsLastPulledAtMs,
			&r.EventsLastPulledThroughMs,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordEventPullProgress updates the per-send watermark + counters on a
// successful pull, freezing the send if it's reached the end of the
// archive window. The freeze decision is made by the caller (the worker
// layer knows the archive-max-age constant).
func (s *Store) RecordEventPullProgress(ctx context.Context, sendID string, args EventPullProgress) error {
	freeze := 0
	if args.Freeze {
		freeze = 1
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE sends SET
		  events_last_pulled_at_ms      = ?,
		  events_last_pulled_through_ms = ?,
		  events_pulls_count            = events_pulls_count + 1,
		  events_archive_count          = events_archive_count + ?,
		  events_archive_complete       = ?,
		  events_last_pull_error        = NULL,
		  updated_at                    = ?
		WHERE id = ?`,
		args.LastPulledAtMs,
		args.LastPulledThroughMs,
		args.Inserted,
		freeze,
		nowISO(),
		sendID,
	)
	return err
}

// EventPullProgress is the arguments bag for RecordEventPullProgress.
type EventPullProgress struct {
	LastPulledAtMs      int64
	LastPulledThroughMs int64
	Inserted            int64
	Freeze              bool
}

// RecordEventPullError records a failure from a Mailgun pull. We don't
// bump events_pulls_count and don't advance the watermark — the next beat
// will retry the same window.
func (s *Store) RecordEventPullError(ctx context.Context, sendID, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE sends SET
		  events_last_pull_error = ?,
		  updated_at             = ?
		WHERE id = ?`,
		errMsg, nowISO(), sendID,
	)
	return err
}

// GetSendListID looks up the list_id for one send. Singles without a list
// return ("", nil). Used by the engagement-summary maintenance to decide
// whether the event should update contact_engagement (list-tied events
// only).
func (s *Store) GetSendListID(ctx context.Context, sendID string) (string, error) {
	var listID sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT list_id FROM sends WHERE id = ?`, sendID).Scan(&listID)
	if err != nil {
		return "", err
	}
	if !listID.Valid {
		return "", nil
	}
	return listID.String, nil
}

// ---------------------------------------------------------------------------
// Event ingestion
// ---------------------------------------------------------------------------

// NormalizedEvent is the cleaned, per-event payload the pull loop folds
// directly into the two engagement rollups. There is no raw event ledger,
// so this is never persisted — it's a transient carrier between
// NormalizeEvent and the Apply* methods, carrying only the fields the
// rollups consume.
type NormalizedEvent struct {
	Event            string
	Severity         sql.NullString
	Recipient        string
	EventTimestampMs int64
	Reason           sql.NullString
	// URL is the clicked link (only present on "clicked" events). The
	// pull loop folds it into contact_message_clicks; canonicalization
	// happens at apply time, not here.
	URL string
}

// NormalizeEvent converts a raw Mailgun event into the rollup-ready shape.
// Returns (nil, false) for events lacking the bare minimum identifiers
// (id, event, timestamp, recipient) so the pull loop can defensively skip
// malformed events without aborting the batch.
func NormalizeEvent(raw mailgun.RawEvent) (*NormalizedEvent, bool) {
	if raw.ID == "" || raw.Event == "" || raw.Recipient == "" {
		return nil, false
	}
	if raw.Timestamp == 0 {
		return nil, false
	}
	return &NormalizedEvent{
		Event:            raw.Event,
		Severity:         optString(raw.Severity),
		Recipient:        strings.ToLower(raw.Recipient),
		EventTimestampMs: int64(raw.Timestamp * 1000),
		Reason:           optString(raw.Reason),
		URL:              raw.URL,
	}, true
}

// LookupContactIDByEmail resolves a recipient email to its contact_id.
// Returns an invalid sql.NullString (not an error) when no contact
// matches — Mailgun can report events for addresses we never stored
// (e.g. forwarded mail), and those simply don't move any rollup.
func (s *Store) LookupContactIDByEmail(ctx context.Context, email string) (sql.NullString, error) {
	var id sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM contacts WHERE email = ? LIMIT 1`,
		strings.ToLower(email),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	}
	if err != nil {
		return sql.NullString{}, err
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// Engagement summary maintenance
// ---------------------------------------------------------------------------

// ApplyEventToEngagement applies one event to the per-(contact, list)
// engagement summary. Counters increment per call, so the pull loop must
// only call this once per event — which the incremental watermark
// guarantees: each pull begins strictly after the highest event
// timestamp the previous pull processed, so no event is seen twice.
//
// Semantics — see the proposal doc:
//   delivered → bump total_delivered + messages_since_last_engagement
//               (guarded against out-of-order arrival: msgs_since_eng
//               only increments when the delivered event is NEWER than
//               the contact's last_engagement_at_ms — otherwise a late
//               delivered for an already-opened message would falsely
//               inflate dormancy and bias prune-by-count toward false
//               positives)
//   opened    → bump total_opens, RESET messages_since_last_engagement
//   clicked   → bump total_clicks, RESET messages_since_last_engagement
//   other     → no-op (the raw row is archived but doesn't move the summary)
//
// last_*_at_ms columns use MAX(prior, new) so out-of-order arrival from
// Mailgun's eventually-consistent API doesn't move them backwards.
func (s *Store) ApplyEventToEngagement(ctx context.Context, contactID, listID, eventType string, eventTsMs int64) error {
	now := nowISO()
	switch eventType {
	case "delivered":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_engagement
			  (contact_id, list_id, last_delivered_at_ms,
			   total_delivered, messages_since_last_engagement, updated_at)
			VALUES (?, ?, ?, 1, 1, ?)
			ON CONFLICT(contact_id, list_id) DO UPDATE SET
			  last_delivered_at_ms           = MAX(COALESCE(last_delivered_at_ms, 0), excluded.last_delivered_at_ms),
			  total_delivered                = total_delivered + 1,
			  messages_since_last_engagement = CASE
			    WHEN excluded.last_delivered_at_ms > COALESCE(last_engagement_at_ms, 0)
			    THEN messages_since_last_engagement + 1
			    ELSE messages_since_last_engagement
			  END,
			  updated_at                     = excluded.updated_at`,
			contactID, listID, eventTsMs, now,
		)
		return err
	case "opened":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_engagement
			  (contact_id, list_id, last_open_at_ms, last_engagement_at_ms,
			   total_opens, messages_since_last_engagement, updated_at)
			VALUES (?, ?, ?, ?, 1, 0, ?)
			ON CONFLICT(contact_id, list_id) DO UPDATE SET
			  last_open_at_ms                = MAX(COALESCE(last_open_at_ms, 0), excluded.last_open_at_ms),
			  last_engagement_at_ms          = MAX(COALESCE(last_engagement_at_ms, 0), excluded.last_open_at_ms),
			  total_opens                    = total_opens + 1,
			  messages_since_last_engagement = 0,
			  updated_at                     = excluded.updated_at`,
			contactID, listID, eventTsMs, eventTsMs, now,
		)
		return err
	case "clicked":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_engagement
			  (contact_id, list_id, last_click_at_ms, last_engagement_at_ms,
			   total_clicks, messages_since_last_engagement, updated_at)
			VALUES (?, ?, ?, ?, 1, 0, ?)
			ON CONFLICT(contact_id, list_id) DO UPDATE SET
			  last_click_at_ms               = MAX(COALESCE(last_click_at_ms, 0), excluded.last_click_at_ms),
			  last_engagement_at_ms          = MAX(COALESCE(last_engagement_at_ms, 0), excluded.last_click_at_ms),
			  total_clicks                   = total_clicks + 1,
			  messages_since_last_engagement = 0,
			  updated_at                     = excluded.updated_at`,
			contactID, listID, eventTsMs, eventTsMs, now,
		)
		return err
	default:
		return nil
	}
}

// ApplyEventToMessageEngagement applies one event to the per-(send,
// contact) detail row in contact_message_engagement. eventTsSec is epoch
// SECONDS (the caller converts from the ledger's ms). listID may be NULL
// for list-less singles. severity/reason are only consulted for failures.
//
// Counters increment per call; the incremental watermark (each pull
// begins strictly after the previous pull's highest event timestamp)
// ensures each event is applied exactly once. Timestamp fields use
// MIN/MAX so out-of-order arrival within a single pull converges.
func (s *Store) ApplyEventToMessageEngagement(ctx context.Context, sendID, contactID string, listID sql.NullString, eventType string, eventTsSec int64, severity, reason sql.NullString) error {
	now := time.Now().Unix()
	switch eventType {
	case "accepted":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, sent_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  sent_at    = MIN(COALESCE(sent_at, excluded.sent_at), excluded.sent_at),
			  updated_at = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, now)
		return err
	case "delivered":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, delivered_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  delivered_at = MIN(COALESCE(delivered_at, excluded.delivered_at), excluded.delivered_at),
			  updated_at   = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, now)
		return err
	case "opened":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, first_open_at, last_open_at, total_opens, updated_at)
			VALUES (?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  first_open_at = MIN(COALESCE(first_open_at, excluded.first_open_at), excluded.first_open_at),
			  last_open_at  = MAX(COALESCE(last_open_at, 0), excluded.last_open_at),
			  total_opens   = total_opens + 1,
			  updated_at    = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, eventTsSec, now)
		return err
	case "clicked":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, first_click_at, last_click_at, total_clicks, updated_at)
			VALUES (?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  first_click_at = MIN(COALESCE(first_click_at, excluded.first_click_at), excluded.first_click_at),
			  last_click_at  = MAX(COALESCE(last_click_at, 0), excluded.last_click_at),
			  total_clicks   = total_clicks + 1,
			  updated_at     = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, eventTsSec, now)
		return err
	case "failed", "rejected":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, failed, failed_at, failure_severity, failure_reason, updated_at)
			VALUES (?, ?, ?, 1, ?, ?, ?, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  failed           = 1,
			  failed_at        = MAX(COALESCE(failed_at, 0), excluded.failed_at),
			  failure_severity = excluded.failure_severity,
			  failure_reason   = excluded.failure_reason,
			  updated_at       = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, severity, reason, now)
		return err
	case "complained":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, complained_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  complained_at = MAX(COALESCE(complained_at, 0), excluded.complained_at),
			  updated_at    = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, now)
		return err
	case "unsubscribed":
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO contact_message_engagement (send_id, contact_id, list_id, unsubscribed_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(send_id, contact_id) DO UPDATE SET
			  unsubscribed_at = MAX(COALESCE(unsubscribed_at, 0), excluded.unsubscribed_at),
			  updated_at      = excluded.updated_at`,
			sendID, contactID, listID, eventTsSec, now)
		return err
	default:
		return nil
	}
}

// canonicalizeClickURL normalizes a clicked link so the per-URL rollup
// keys on the destination rather than on per-recipient link noise:
//   - trim surrounding whitespace
//   - lowercase scheme + host (path/case preserved — paths can be
//     case-sensitive)
//   - drop the query string and fragment
// On a parse failure (or a scheme-less/host-less string) it returns the
// trimmed input unchanged so a malformed link still aggregates
// deterministically instead of being silently dropped.
func canonicalizeClickURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Host = strings.ToLower(u.Host) // url.Parse already lowercases the scheme
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "" {
		// Match the Worker's URL.toString(), which renders a bare host
		// with a trailing slash — keeps the canonical form identical
		// across runtimes.
		u.Path = "/"
	}
	return u.String()
}

// ApplyClickToURL folds one "clicked" event into the per-URL rollup
// (contact_message_clicks). url is canonicalized here; an empty/blank
// url is a no-op (cme.total_clicks still counts it via
// ApplyEventToMessageEngagement). eventTsSec is epoch SECONDS.
//
// Like the other Apply* methods the counter increments per call; the
// incremental watermark guarantees each event is seen once, with the same
// accepted crash/retry drift noted in worker/events_pull.go.
func (s *Store) ApplyClickToURL(ctx context.Context, sendID, contactID string, listID sql.NullString, rawURL string, eventTsSec int64) error {
	clickURL := canonicalizeClickURL(rawURL)
	if clickURL == "" {
		return nil
	}
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO contact_message_clicks
		  (send_id, contact_id, list_id, url, first_click_at, last_click_at, total_clicks, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(send_id, contact_id, url) DO UPDATE SET
		  first_click_at = MIN(COALESCE(first_click_at, excluded.first_click_at), excluded.first_click_at),
		  last_click_at  = MAX(COALESCE(last_click_at, 0), excluded.last_click_at),
		  total_clicks   = total_clicks + 1,
		  list_id        = COALESCE(list_id, excluded.list_id),
		  updated_at     = excluded.updated_at`,
		sendID, contactID, listID, clickURL, eventTsSec, eventTsSec, now)
	return err
}

// ---------------------------------------------------------------------------
// Read endpoints
// ---------------------------------------------------------------------------

// ListSendRecipientsParams narrows ListSendRecipients. AfterContactID is
// the keyset cursor (contact_id is the stable per-send ordering key).
type ListSendRecipientsParams struct {
	SendID         string
	AfterContactID string
	Limit          int
}

// ListSendRecipients returns one page of per-recipient message
// engagement rows for a send, ordered by contact_id ASC. Keyset
// paginated on contact_id (unique within a send via the composite PK).
func (s *Store) ListSendRecipients(ctx context.Context, p ListSendRecipientsParams) ([]MessageEngagement, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses := []string{"send_id = ?"}
	args := []any{p.SendID}
	if p.AfterContactID != "" {
		clauses = append(clauses, "contact_id > ?")
		args = append(args, p.AfterContactID)
	}
	args = append(args, limit)
	query := `
		SELECT send_id, contact_id, list_id, sent_at, delivered_at,
		       first_open_at, last_open_at, total_opens,
		       first_click_at, last_click_at, total_clicks,
		       failed, failed_at, failure_severity, failure_reason,
		       complained_at, unsubscribed_at, replied_at, updated_at
		FROM contact_message_engagement
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY contact_id ASC
		LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageEngagement
	for rows.Next() {
		var m MessageEngagement
		if err := rows.Scan(
			&m.SendID, &m.ContactID, &m.ListID, &m.SentAt, &m.DeliveredAt,
			&m.FirstOpenAt, &m.LastOpenAt, &m.TotalOpens,
			&m.FirstClickAt, &m.LastClickAt, &m.TotalClicks,
			&m.Failed, &m.FailedAt, &m.FailureSeverity, &m.FailureReason,
			&m.ComplainedAt, &m.UnsubscribedAt, &m.RepliedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListSendClicksParams narrows ListSendClicks. The keyset cursor is the
// composite (AfterContactID, AfterURL) — contact_id is the primary order
// key, url breaks ties (both come from the previous page's last row).
type ListSendClicksParams struct {
	SendID         string
	AfterContactID string
	AfterURL       string
	Limit          int
}

// ListSendClicks returns one page of per-URL click rows for a send,
// ordered by (contact_id, url) ASC. Keyset-paginated on that composite
// (unique within a send via the PK).
func (s *Store) ListSendClicks(ctx context.Context, p ListSendClicksParams) ([]MessageClick, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses := []string{"send_id = ?"}
	args := []any{p.SendID}
	if p.AfterContactID != "" {
		// Composite keyset: advance past (AfterContactID, AfterURL).
		clauses = append(clauses, "(contact_id > ? OR (contact_id = ? AND url > ?))")
		args = append(args, p.AfterContactID, p.AfterContactID, p.AfterURL)
	}
	args = append(args, limit)
	query := `
		SELECT send_id, contact_id, list_id, url,
		       first_click_at, last_click_at, total_clicks, updated_at
		FROM contact_message_clicks
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY contact_id ASC, url ASC
		LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageClick
	for rows.Next() {
		var m MessageClick
		if err := rows.Scan(
			&m.SendID, &m.ContactID, &m.ListID, &m.URL,
			&m.FirstClickAt, &m.LastClickAt, &m.TotalClicks, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListContactEngagement returns engagement rows for one contact.
// When listID is non-empty, narrows to one (contact, list); otherwise
// returns one row per list the contact has engaged with.
//
// The contactID can be either a contact_id (c_*) or an email — the
// caller is expected to have resolved it via ResolveContact upstream.
func (s *Store) ListContactEngagement(ctx context.Context, contactID, listID string) ([]ContactEngagement, error) {
	clauses := []string{"contact_id = ?"}
	args := []any{contactID}
	if listID != "" {
		clauses = append(clauses, "list_id = ?")
		args = append(args, listID)
	}
	query := `
		SELECT contact_id, list_id,
		       last_delivered_at_ms, last_open_at_ms, last_click_at_ms, last_engagement_at_ms,
		       total_delivered, total_opens, total_clicks,
		       messages_since_last_engagement, updated_at
		FROM contact_engagement
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY (last_engagement_at_ms IS NULL) ASC, last_engagement_at_ms DESC,
		         (last_delivered_at_ms IS NULL) ASC, last_delivered_at_ms DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactEngagement
	for rows.Next() {
		var ce ContactEngagement
		var updatedAt string
		if err := rows.Scan(
			&ce.ContactID, &ce.ListID,
			&ce.LastDeliveredAtMs, &ce.LastOpenAtMs, &ce.LastClickAtMs, &ce.LastEngagementAtMs,
			&ce.TotalDelivered, &ce.TotalOpens, &ce.TotalClicks,
			&ce.MessagesSinceLastEngagement, &updatedAt,
		); err != nil {
			return nil, err
		}
		if ce.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return nil, err
		}
		out = append(out, ce)
	}
	return out, rows.Err()
}

// ResolveContactID accepts either a contact_id (c_*) or an email and
// returns the canonical contact_id. Returns ErrNotFound when no contact
// matches.
func (s *Store) ResolveContactID(ctx context.Context, idOrEmail string) (string, error) {
	key := strings.TrimSpace(idOrEmail)
	if key == "" {
		return "", ErrNotFound
	}
	// contact_id prefix is "c_" (see ids/ids.go). Cheap heuristic so we
	// don't run two queries against contacts.
	if strings.HasPrefix(key, "c_") {
		var id string
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM contacts WHERE id = ?`, key).Scan(&id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", ErrNotFound
			}
			return "", err
		}
		return id, nil
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM contacts WHERE email = ?`, strings.ToLower(key)).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return id, nil
}

// optString turns "" into sql.NullString{Valid:false} and "foo" into
// sql.NullString{String:"foo", Valid:true}. Convenience for the
// NormalizeEvent function above.
func optString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
