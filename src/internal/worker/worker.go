package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ranaroussi/minigun/internal/config"
	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/models"
	"github.com/ranaroussi/minigun/internal/store"
	"github.com/ranaroussi/minigun/internal/token"
)

type Manager struct {
	cfg     *config.Config
	store   *store.Store
	mailgun *mailgun.Client
	log     *slog.Logger

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func NewManager(cfg *config.Config, st *store.Store, mg *mailgun.Client, log *slog.Logger) *Manager {
	return &Manager{
		cfg:     cfg,
		store:   st,
		mailgun: mg,
		log:     log,
		running: map[string]context.CancelFunc{},
	}
}

func (m *Manager) Start(ctx context.Context, sendID string) error {
	m.mu.Lock()
	if _, ok := m.running[sendID]; ok {
		m.mu.Unlock()
		return errors.New("send already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.running[sendID] = cancel
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.running, sendID)
			m.mu.Unlock()
		}()
		if err := m.run(runCtx, sendID); err != nil {
			m.log.Error("send worker failed", "send_id", sendID, "err", err)
		}
	}()
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	for _, c := range m.running {
		c()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) RecoverPending(ctx context.Context) error {
	sends, err := m.store.ListRunningSends(ctx)
	if err != nil {
		return err
	}
	for _, snd := range sends {
		hasInFlight, err := m.store.HasInFlightBatch(ctx, snd.ID)
		if err != nil {
			m.log.Error("check in-flight", "send_id", snd.ID, "err", err)
			continue
		}
		if hasInFlight {
			m.log.Warn("send has in-flight batch from previous run; leaving in current status, requires manual resume",
				"send_id", snd.ID, "status", snd.Status)
			continue
		}
		if err := m.Start(ctx, snd.ID); err != nil {
			m.log.Error("restart send", "send_id", snd.ID, "err", err)
		}
	}
	return nil
}

func (m *Manager) run(ctx context.Context, sendID string) error {
	snd, err := m.store.GetSend(ctx, sendID)
	if err != nil {
		return fmt.Errorf("load send: %w", err)
	}
	if err := m.store.UpdateSendStatus(ctx, sendID, models.SendStatusRunning, nil); err != nil {
		return fmt.Errorf("mark running: %w", err)
	}

	switch snd.Type {
	case models.SendTypeSingle:
		return m.runSingle(ctx, snd)
	case models.SendTypeBulk:
		return m.runBulk(ctx, snd)
	default:
		return fmt.Errorf("unknown send type: %s", snd.Type)
	}
}

func (m *Manager) runSingle(ctx context.Context, snd *models.Send) error {
	if snd.RecipientEmail == nil {
		return m.failSend(ctx, snd.ID, "single send missing recipient_email")
	}
	if snd.SendingDomain == "" {
		return m.failSend(ctx, snd.ID, "single send missing sending_domain")
	}
	html := derefStr(snd.BodyHTML)
	text := derefStr(snd.BodyText)
	var listUnsub, listUnsubPost string
	if snd.LastSubscriptionID > 0 {
		tok := token.Sign(m.cfg.HMACSecret, snd.ID, snd.LastSubscriptionID)
		unsubURL := fmt.Sprintf("%s/u/%s", m.cfg.PublicURL, tok)
		for _, ph := range []string{"%recipient.unsubscribe%", "%recipient.unsub_url%"} {
			html = strings.ReplaceAll(html, ph, unsubURL)
			text = strings.ReplaceAll(text, ph, unsubURL)
		}
		listUnsub = fmt.Sprintf("<%s>", unsubURL)
		listUnsubPost = "List-Unsubscribe=One-Click"
	}
	msg := &mailgun.Message{
		Domain:                snd.SendingDomain,
		From:                  snd.FromHeader,
		To:                    []string{*snd.RecipientEmail},
		Subject:               snd.Subject,
		HTML:                  html,
		Text:                  text,
		Tag:                   snd.ID,
		TrackingOpens:         true,
		TrackingClicks:        true,
		TrackingUnsubscribeOn: false,
		TestMode:              snd.TestMode,
		ListUnsubscribe:       listUnsub,
		ListUnsubscribePost:   listUnsubPost,
		// v:minigun_send_id is a redundant safety net for the events archive.
		// The o:tag above is the primary anchor (it's how the events-pull
		// cron filters Mailgun's events API by send_id). The user variable
		// makes the send_id available inside every event's user_variables
		// blob without re-parsing the tag — useful for richer queries later.
		CustomVars: map[string]string{
			"minigun_send_id": snd.ID,
		},
	}
	if snd.ReplyTo != nil {
		msg.ReplyTo = *snd.ReplyTo
	}
	if _, err := m.mailgun.SendMessageWithRetry(ctx, msg, 5); err != nil {
		return m.failSend(ctx, snd.ID, err.Error())
	}
	return m.store.UpdateSendStatus(ctx, snd.ID, models.SendStatusCompleted, nil)
}

func (m *Manager) runBulk(ctx context.Context, snd *models.Send) error {
	if snd.ListID == nil || snd.MaxSubscriptionID == nil {
		return m.failSend(ctx, snd.ID, "bulk send missing list_id or max_subscription_id")
	}
	if snd.SendingDomain == "" {
		return m.failSend(ctx, snd.ID, "bulk send missing sending_domain")
	}
	listID := *snd.ListID
	maxID := *snd.MaxSubscriptionID
	throttle := time.Duration(snd.ThrottleMS) * time.Millisecond

	bodyHTML := derefStr(snd.BodyHTML)
	bodyText := derefStr(snd.BodyText)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		current, err := m.store.GetSend(ctx, snd.ID)
		if err != nil {
			return err
		}
		recipients, err := m.store.NextRecipientBatch(ctx, listID, current.LastSubscriptionID, maxID, current.BatchSize)
		if err != nil {
			return m.failSend(ctx, snd.ID, fmt.Sprintf("next batch: %v", err))
		}
		if len(recipients) == 0 {
			return m.store.UpdateSendStatus(ctx, snd.ID, models.SendStatusCompleted, nil)
		}

		batchIndex, err := m.store.NextBatchIndex(ctx, snd.ID)
		if err != nil {
			return m.failSend(ctx, snd.ID, fmt.Sprintf("next batch index: %v", err))
		}
		startID := recipients[0].SubscriptionID
		endID := recipients[len(recipients)-1].SubscriptionID

		batch, err := m.store.CreateBatch(ctx, snd.ID, batchIndex, startID, endID, len(recipients))
		if err != nil {
			return m.failSend(ctx, snd.ID, fmt.Sprintf("create batch: %v", err))
		}

		recipVars := map[string]map[string]any{}
		subIDs := make([]string, 0, len(recipients))
		emails := make([]string, 0, len(recipients))
		for _, r := range recipients {
			emails = append(emails, r.Email)
			subIDs = append(subIDs, strconv.FormatInt(r.SubscriptionID, 10))
			recipVars[r.Email] = m.buildRecipientVars(snd, r)
		}

		listUnsub := fmt.Sprintf("<%s>", "%recipient.unsub_url%")
		msg := &mailgun.Message{
			Domain:                snd.SendingDomain,
			From:                  snd.FromHeader,
			To:                    emails,
			Subject:               snd.Subject,
			HTML:                  bodyHTML,
			Text:                  bodyText,
			Tag:                   snd.ID,
			TrackingOpens:         true,
			TrackingClicks:        true,
			TrackingUnsubscribeOn: false,
			ListUnsubscribe:       listUnsub,
			ListUnsubscribePost:   "List-Unsubscribe=One-Click",
			RecipientVariables:    recipVars,
			CustomVars: map[string]string{
				"minigun_send_id":          snd.ID,
				"minigun_subscription_ids": strings.Join(subIDs, ","),
				"minigun_batch_id":         batch.ID,
			},
			TestMode: snd.TestMode,
		}
		if snd.ReplyTo != nil {
			msg.ReplyTo = *snd.ReplyTo
		}

		resp, err := m.mailgun.SendMessageWithRetry(ctx, msg, 5)
		if err != nil {
			errStr := err.Error()
			_ = m.store.MarkBatchStatus(ctx, batch.ID, models.BatchStatusFailed, &errStr)
			return m.failSend(ctx, snd.ID, errStr)
		}
		respJSON, _ := json.Marshal(resp)
		respStr := string(respJSON)
		if err := m.store.MarkBatchStatus(ctx, batch.ID, models.BatchStatusSucceeded, &respStr); err != nil {
			return m.failSend(ctx, snd.ID, fmt.Sprintf("mark batch succeeded: %v", err))
		}
		if err := m.store.AdvanceSendCursor(ctx, snd.ID, endID); err != nil {
			return m.failSend(ctx, snd.ID, fmt.Sprintf("advance cursor: %v", err))
		}

		if throttle > 0 {
			select {
			case <-time.After(throttle):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (m *Manager) buildRecipientVars(snd *models.Send, r models.Recipient) map[string]any {
	out := map[string]any{}
	if r.Params != "" {
		var params map[string]any
		if err := json.Unmarshal([]byte(r.Params), &params); err == nil {
			for k, v := range params {
				out[k] = v
			}
		}
	}
	tok := token.Sign(m.cfg.HMACSecret, snd.ID, r.SubscriptionID)
	unsubURL := fmt.Sprintf("%s/u/%s", m.cfg.PublicURL, tok)
	out["unsub_url"] = unsubURL
	out["unsubscribe"] = unsubURL
	out["manage_url"] = fmt.Sprintf("%s/manage/%s", m.cfg.PublicURL, tok)
	return out
}

func (m *Manager) failSend(ctx context.Context, sendID, errStr string) error {
	if err := m.store.UpdateSendStatus(ctx, sendID, models.SendStatusFailed, &errStr); err != nil {
		m.log.Error("mark send failed", "send_id", sendID, "err", err)
	}
	return errors.New(errStr)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
