package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ranaroussi/minigun/internal/ids"
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

// MailgunEvent is the canonical row shape exposed for query endpoints
// (Phase 3) — kept narrower than the full table so the wire shape stays
// stable as we add columns.
type MailgunEvent struct {
	ID                string
	Domain            string
	MailgunEventID    string
	Event             string
	Severity          *string
	Recipient         string
	RecipientDomain   string
	EventTimestampMs  int64
	EventTimestampISO string
	MessageID         *string
	MgSendID          string
	ContactID         *string
	URL               *string
	Reason            *string
	Tags              *string
	ClientInfo        *string
	Geolocation       *string
	UserVariables     *string
	RawPayload        string
	CreatedAt         time.Time
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
// cron — i.e., non-frozen, non-test sends within the archive window. The
// burst-vs-daily schedule logic is computed in the worker layer (see
// worker/events_pull.go nextDueAt); the SQL just narrows candidates so the
// downstream filter has a small set to look at.
//
// nowMs is passed by the caller so tests can pin time deterministically.
func (s *Store) ListDueEventPulls(ctx context.Context, nowMs int64, maxAgeMs int64, limit int) ([]DueEventPullRow, error) {
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
		  AND CAST(strftime('%s', created_at) AS INTEGER) * 1000 > ?
		ORDER BY COALESCE(events_last_pulled_at_ms, 0) ASC
		LIMIT ?`,
		nowMs-maxAgeMs, limit,
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

// NormalizedEvent is the cleaned, per-event payload ready to insert into
// mailgun_events. Decoupled from mailgun.RawEvent so persistence doesn't
// see the wire-format variability.
type NormalizedEvent struct {
	Domain            string
	MailgunEventID    string
	Event             string
	Severity          sql.NullString
	Recipient         string
	RecipientDomain   string
	EventTimestampMs  int64
	EventTimestampISO string
	MessageID         sql.NullString
	MgSendID          string
	URL               sql.NullString
	Reason            sql.NullString
	Tags              sql.NullString
	ClientInfo        sql.NullString
	Geolocation       sql.NullString
	UserVariables     sql.NullString
	RawPayload        string
}

// NormalizeEvent converts a raw Mailgun event into the persisted shape.
// Returns (nil, false) for events lacking the bare minimum identifiers
// (id, event, timestamp, recipient) so the pull loop can defensively skip
// malformed events without aborting the batch.
func NormalizeEvent(raw mailgun.RawEvent, domain, sendID string) (*NormalizedEvent, bool) {
	if raw.ID == "" || raw.Event == "" || raw.Recipient == "" {
		return nil, false
	}
	if raw.Timestamp == 0 {
		return nil, false
	}
	recipient := strings.ToLower(raw.Recipient)
	atIdx := strings.LastIndex(recipient, "@")
	recipientDomain := ""
	if atIdx >= 0 {
		recipientDomain = recipient[atIdx+1:]
	}
	tsMs := int64(raw.Timestamp * 1000)
	ev := &NormalizedEvent{
		Domain:            domain,
		MailgunEventID:    raw.ID,
		Event:             raw.Event,
		Severity:          optString(raw.Severity),
		Recipient:         recipient,
		RecipientDomain:   recipientDomain,
		EventTimestampMs:  tsMs,
		EventTimestampISO: time.UnixMilli(tsMs).UTC().Format(time.RFC3339Nano),
		MgSendID:          sendID,
		URL:               optString(raw.URL),
		Reason:            optString(raw.Reason),
	}
	// Extract message-id from raw.Message.headers["message-id"] if present.
	if len(raw.Message) > 0 {
		var msg struct {
			Headers map[string]any `json:"headers"`
		}
		if err := json.Unmarshal(raw.Message, &msg); err == nil {
			if mid, ok := msg.Headers["message-id"].(string); ok && mid != "" {
				ev.MessageID = sql.NullString{String: mid, Valid: true}
			}
		}
	}
	if len(raw.Tags) > 0 {
		if j, err := json.Marshal(raw.Tags); err == nil {
			ev.Tags = sql.NullString{String: string(j), Valid: true}
		}
	}
	if len(raw.ClientInfo) > 0 {
		ev.ClientInfo = sql.NullString{String: string(raw.ClientInfo), Valid: true}
	}
	if len(raw.Geolocation) > 0 {
		ev.Geolocation = sql.NullString{String: string(raw.Geolocation), Valid: true}
	}
	if len(raw.UserVars) > 0 {
		ev.UserVariables = sql.NullString{String: string(raw.UserVars), Valid: true}
	}
	if len(raw.Raw) > 0 {
		ev.RawPayload = string(raw.Raw)
	} else {
		// Defensive: if FetchEvents didn't populate Raw (shouldn't happen),
		// re-marshal so the column is never empty.
		if j, err := json.Marshal(raw); err == nil {
			ev.RawPayload = string(j)
		}
	}
	return ev, true
}

// InsertEventResult mirrors the TS side: was the row new, and what
// contact_id did the email resolve to.
type InsertEventResult struct {
	Inserted  bool
	ContactID sql.NullString
}

// InsertEventIfNew inserts one normalized event with INSERT OR IGNORE.
// Returns Inserted=true if a row was actually written (and the resolved
// ContactID if any), Inserted=false if the UNIQUE constraint on
// mailgun_event_id rejected a duplicate.
//
// This is the entire idempotency contract for the events archive: the
// 6h overlap window we re-fetch on every pull will re-present events
// we've already archived, and this function makes that a no-op.
func (s *Store) InsertEventIfNew(ctx context.Context, ev *NormalizedEvent) (*InsertEventResult, error) {
	id := ids.NewMailgunEvent()
	now := nowISO()
	res, err := s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO mailgun_events
		  (id, domain, mailgun_event_id, event, severity, recipient, recipient_domain,
		   event_timestamp_ms, event_timestamp_iso, message_id, mg_send_id, contact_id,
		   url, reason, tags, client_info, geolocation, user_variables, raw_payload, created_at)
		VALUES (
		  ?, ?, ?, ?, ?, ?, ?,
		  ?, ?, ?, ?,
		  (SELECT id FROM contacts WHERE email = ? LIMIT 1),
		  ?, ?, ?, ?, ?, ?, ?, ?
		)`,
		id,
		ev.Domain,
		ev.MailgunEventID,
		ev.Event,
		ev.Severity,
		ev.Recipient,
		ev.RecipientDomain,
		ev.EventTimestampMs,
		ev.EventTimestampISO,
		ev.MessageID,
		ev.MgSendID,
		ev.Recipient,
		ev.URL,
		ev.Reason,
		ev.Tags,
		ev.ClientInfo,
		ev.Geolocation,
		ev.UserVariables,
		ev.RawPayload,
		now,
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return &InsertEventResult{Inserted: false}, nil
	}
	// Look up the contact_id stamped onto the row we just inserted.
	var contactID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT contact_id FROM mailgun_events WHERE id = ?`, id).Scan(&contactID); err != nil {
		// We just inserted — this shouldn't fail, but if it does, treat
		// it as "inserted but contact unresolved" rather than as an error.
		return &InsertEventResult{Inserted: true}, nil
	}
	return &InsertEventResult{Inserted: true, ContactID: contactID}, nil
}

// ---------------------------------------------------------------------------
// Engagement summary maintenance
// ---------------------------------------------------------------------------

// ApplyEventToEngagement applies one event to the per-(contact, list)
// engagement summary. Only called when InsertEventIfNew actually inserted
// (the entire idempotency contract relies on this being downstream of a
// successful UNIQUE-constrained insert).
//
// Semantics — see the proposal doc:
//   delivered → bump total_delivered + messages_since_last_engagement
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
			  messages_since_last_engagement = messages_since_last_engagement + 1,
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

// ---------------------------------------------------------------------------
// Read endpoints (Phase 3)
// ---------------------------------------------------------------------------

// ListSendEventsParams narrows what ListSendEvents returns.
// AfterTsMs + AfterID together form the keyset cursor — the next page
// starts strictly after (AfterTsMs, AfterID). Limit is clamped by the
// caller (see api/events.go).
type ListSendEventsParams struct {
	SendID    string
	EventType string // "" = all; otherwise filters mailgun_events.event
	SinceMs   int64  // 0 = no lower bound
	AfterTsMs int64  // 0 = first page
	AfterID   string // "" = first page
	Limit     int
}

// ListSendEvents returns one page of events for a send, ordered ASC by
// (event_timestamp_ms, id). The ASC order keeps the analytical-replay
// use case natural: "show me how this send unfolded." The keyset cursor
// is stable across inserts because mailgun_event_id (and hence id) is
// monotonic per (send, ms) — newly arrived late events for older windows
// would fall before the current cursor and the client wouldn't see them
// on subsequent pages. That's acceptable for the archive's purpose
// (forensic replay); for "find late-arriving opens" use SinceMs instead.
func (s *Store) ListSendEvents(ctx context.Context, p ListSendEventsParams) ([]MailgunEvent, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Build the WHERE clause dynamically — keeping the parameterized
	// form close to the SQL so future maintainers can read both at once.
	clauses := []string{"mg_send_id = ?"}
	args := []any{p.SendID}
	if p.EventType != "" {
		clauses = append(clauses, "event = ?")
		args = append(args, p.EventType)
	}
	if p.SinceMs > 0 {
		clauses = append(clauses, "event_timestamp_ms >= ?")
		args = append(args, p.SinceMs)
	}
	if p.AfterTsMs > 0 || p.AfterID != "" {
		// Keyset pagination on (event_timestamp_ms, id). The OR form is
		// the canonical cursor predicate for compound-key ordering; SQLite
		// can evaluate it efficiently when the underlying index covers
		// (mg_send_id, event_timestamp_ms).
		clauses = append(clauses, "(event_timestamp_ms > ? OR (event_timestamp_ms = ? AND id > ?))")
		args = append(args, p.AfterTsMs, p.AfterTsMs, p.AfterID)
	}
	args = append(args, limit)
	query := `
		SELECT id, domain, mailgun_event_id, event, severity, recipient, recipient_domain,
		       event_timestamp_ms, event_timestamp_iso, message_id, mg_send_id, contact_id,
		       url, reason, tags, client_info, geolocation, user_variables, raw_payload, created_at
		FROM mailgun_events
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY event_timestamp_ms ASC, id ASC
		LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailgunEvent
	for rows.Next() {
		var ev MailgunEvent
		var severity, messageID, contactID, url, reason, tags, clientInfo, geo, userVars sql.NullString
		var createdAt string
		if err := rows.Scan(
			&ev.ID, &ev.Domain, &ev.MailgunEventID, &ev.Event, &severity,
			&ev.Recipient, &ev.RecipientDomain,
			&ev.EventTimestampMs, &ev.EventTimestampISO,
			&messageID, &ev.MgSendID, &contactID,
			&url, &reason, &tags, &clientInfo, &geo, &userVars,
			&ev.RawPayload, &createdAt,
		); err != nil {
			return nil, err
		}
		ev.Severity = stringPtr(severity)
		ev.MessageID = stringPtr(messageID)
		ev.ContactID = stringPtr(contactID)
		ev.URL = stringPtr(url)
		ev.Reason = stringPtr(reason)
		ev.Tags = stringPtr(tags)
		ev.ClientInfo = stringPtr(clientInfo)
		ev.Geolocation = stringPtr(geo)
		ev.UserVariables = stringPtr(userVars)
		if ev.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
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
