package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	MailgunAPIKey  string
	MailgunRegion  string
	MailgunAPIBase string

	PublicURL  string
	HMACSecret string

	DBPath     string
	ListenAddr string

	TurnstileSiteKey   string
	TurnstileSecretKey string

	APIToken string

	// Shared HMAC secret used to verify Mailgun webhook payloads. Set
	// via the dashboard at Sending → Webhooks → "HTTP webhook signing
	// key". When empty, the /webhooks/mailgun endpoint refuses all
	// requests (fail-closed) so we never trust an unsigned payload.
	MailgunWebhookSigningKey string

	// Feature flag for the Mailgun events archive (Phase 2+ of the
	// rollout). When false, the events-pull cron and contact_engagement
	// maintenance remain dormant — Phase 1 only ships the schema and
	// send-path tagging, so the data starts accumulating on Mailgun's
	// side ahead of any local archive activity. Flip to true once the
	// consumer code lands.
	EventsArchiveEnabled bool

	// Feature flag for the optional auto-prune cron (Phase 4). When
	// true, a daily scheduler runs the prune executor against every list
	// using ListHygieneAutoPrune* thresholds below. Default off — the
	// manual surface (POST /lists/{id}/prune) and the per-criterion
	// thresholds the operator chooses are the recommended path; auto-
	// prune is only safe when the events archive is well-populated AND
	// the operator has audited what the candidates query returns.
	ListHygieneAutoPruneEnabled bool

	// Auto-prune thresholds. All default to conservative values that
	// only purge the worst dormancy cases. Zero disables the criterion.
	ListHygieneAutoPruneByCount       int64 // messages_since_last_engagement >= N
	ListHygieneAutoPruneByRecencyDays int64 // last_engagement_at_ms older than D days
	ListHygieneAutoPruneNoDeliveryDays int64 // no delivered events in last D days
}

func FromEnv() (*Config, error) {
	c := &Config{
		MailgunAPIKey:      os.Getenv("MAILGUN_API_KEY"),
		MailgunRegion:      envOr("MAILGUN_REGION", "us"),
		MailgunAPIBase:     os.Getenv("MAILGUN_API_BASE"),
		PublicURL:          strings.TrimRight(os.Getenv("MINIGUN_PUBLIC_URL"), "/"),
		HMACSecret:         os.Getenv("MINIGUN_HMAC_SECRET"),
		DBPath:             envOr("MINIGUN_DB_PATH", "/data/minigun.db"),
		ListenAddr:         envOr("MINIGUN_LISTEN_ADDR", ":8080"),
		TurnstileSiteKey:   os.Getenv("MINIGUN_TURNSTILE_SITE_KEY"),
		TurnstileSecretKey: os.Getenv("MINIGUN_TURNSTILE_SECRET_KEY"),
		APIToken:           os.Getenv("MINIGUN_API_TOKEN"),
		MailgunWebhookSigningKey: os.Getenv("MAILGUN_WEBHOOK_SIGNING_KEY"),
		EventsArchiveEnabled:     strings.EqualFold(os.Getenv("EVENTS_ARCHIVE_ENABLED"), "true"),
		ListHygieneAutoPruneEnabled: strings.EqualFold(os.Getenv("LIST_HYGIENE_AUTO_PRUNE_ENABLED"), "true"),
		// Conservative defaults: 20 wasted deliveries OR 180 days no engagement.
		// Operators who want different thresholds set the corresponding env var.
		ListHygieneAutoPruneByCount:        envInt64("LIST_HYGIENE_AUTO_PRUNE_BY_COUNT", 20),
		ListHygieneAutoPruneByRecencyDays:  envInt64("LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS", 180),
		ListHygieneAutoPruneNoDeliveryDays: envInt64("LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS", 0),
	}

	if c.MailgunAPIBase == "" {
		switch strings.ToLower(c.MailgunRegion) {
		case "eu":
			c.MailgunAPIBase = "https://api.eu.mailgun.net"
		case "us", "":
			c.MailgunAPIBase = "https://api.mailgun.net"
		default:
			return nil, fmt.Errorf("invalid MAILGUN_REGION: %q (expected us or eu)", c.MailgunRegion)
		}
	}
	c.MailgunAPIBase = strings.TrimRight(c.MailgunAPIBase, "/")

	if c.TurnstileSiteKey != "" && c.TurnstileSecretKey == "" {
		return nil, errors.New("MINIGUN_TURNSTILE_SECRET_KEY is required when MINIGUN_TURNSTILE_SITE_KEY is set")
	}

	return c, nil
}

func (c *Config) RequireForServe() error {
	var missing []string
	if c.MailgunAPIKey == "" {
		missing = append(missing, "MAILGUN_API_KEY")
	}
	if c.PublicURL == "" {
		missing = append(missing, "MINIGUN_PUBLIC_URL")
	}
	if c.HMACSecret == "" {
		missing = append(missing, "MINIGUN_HMAC_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt64(k string, def int64) int64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}
