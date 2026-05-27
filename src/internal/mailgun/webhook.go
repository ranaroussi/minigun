package mailgun

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// FreshnessWindow caps how far in the past (or future) a webhook
// timestamp may sit before we reject the payload as a possible replay.
// Mailgun retries deliveries within an ~8h window, so 15 minutes is
// well within that ceiling while still bounding the replay surface.
const FreshnessWindow = 15 * time.Minute

// WebhookSignature is the trio Mailgun sends inside every webhook
// payload (under the top-level "signature" key).
type WebhookSignature struct {
	Timestamp string `json:"timestamp"`
	Token     string `json:"token"`
	Signature string `json:"signature"`
}

// VerifyWebhookSignature returns nil iff the signature is valid against
// the configured signing key AND the timestamp is within FreshnessWindow.
// Match Mailgun's reference scheme:
//
//	signature == hex(HMAC_SHA256(signingKey, timestamp + token))
//
// `now` is injected so tests can pin the clock.
func VerifyWebhookSignature(signingKey string, sig WebhookSignature, now time.Time) error {
	if signingKey == "" {
		return errors.New("no signing key configured")
	}
	if sig.Timestamp == "" || sig.Token == "" || sig.Signature == "" {
		return errors.New("missing timestamp / token / signature")
	}

	tsInt, err := strconv.ParseInt(sig.Timestamp, 10, 64)
	if err != nil {
		return errors.New("timestamp not numeric")
	}
	delta := now.Unix() - tsInt
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > FreshnessWindow {
		return errors.New("timestamp stale or in the future")
	}

	expectedRaw := hmac.New(sha256.New, []byte(signingKey))
	expectedRaw.Write([]byte(sig.Timestamp + sig.Token))
	expected := hex.EncodeToString(expectedRaw.Sum(nil))

	got := strings.ToLower(sig.Signature)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(got)) != 1 {
		return errors.New("signature mismatch")
	}
	return nil
}
