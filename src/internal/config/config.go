package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	MailgunAPIKey  string
	MailgunDomain  string
	MailgunRegion  string
	MailgunAPIBase string

	PublicURL  string
	HMACSecret string

	DBPath     string
	ListenAddr string

	TurnstileSiteKey   string
	TurnstileSecretKey string

	APIToken string
}

func FromEnv() (*Config, error) {
	c := &Config{
		MailgunAPIKey:      os.Getenv("MAILGUN_API_KEY"),
		MailgunDomain:      os.Getenv("MAILGUN_DOMAIN"),
		MailgunRegion:      envOr("MAILGUN_REGION", "us"),
		MailgunAPIBase:     os.Getenv("MAILGUN_API_BASE"),
		PublicURL:          strings.TrimRight(os.Getenv("MINIGUN_PUBLIC_URL"), "/"),
		HMACSecret:         os.Getenv("MINIGUN_HMAC_SECRET"),
		DBPath:             envOr("MINIGUN_DB_PATH", "/data/minigun.db"),
		ListenAddr:         envOr("MINIGUN_LISTEN_ADDR", ":8080"),
		TurnstileSiteKey:   os.Getenv("MINIGUN_TURNSTILE_SITE_KEY"),
		TurnstileSecretKey: os.Getenv("MINIGUN_TURNSTILE_SECRET_KEY"),
		APIToken:           os.Getenv("MINIGUN_API_TOKEN"),
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
