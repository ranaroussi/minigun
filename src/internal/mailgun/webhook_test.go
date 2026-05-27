package mailgun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func signFor(t *testing.T, key, ts, token string) string {
	t.Helper()
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(ts + token))
	return hex.EncodeToString(m.Sum(nil))
}

func TestVerifyWebhookSignature_Valid(t *testing.T) {
	key := "shh-its-a-secret"
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	ts := strconv.FormatInt(now.Unix(), 10)
	tok := "abcdef0123456789"
	sig := signFor(t, key, ts, tok)
	if err := VerifyWebhookSignature(key, WebhookSignature{ts, tok, sig}, now); err != nil {
		t.Fatalf("expected valid signature, got: %v", err)
	}
}

func TestVerifyWebhookSignature_WrongKey(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	ts := strconv.FormatInt(now.Unix(), 10)
	tok := "abcdef"
	sig := signFor(t, "actual-key", ts, tok)
	if err := VerifyWebhookSignature("wrong-key", WebhookSignature{ts, tok, sig}, now); err == nil {
		t.Fatal("expected signature mismatch")
	}
}

func TestVerifyWebhookSignature_StaleTimestamp(t *testing.T) {
	key := "k"
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	staleTS := strconv.FormatInt(now.Add(-20*time.Minute).Unix(), 10)
	tok := "t"
	sig := signFor(t, key, staleTS, tok)
	err := VerifyWebhookSignature(key, WebhookSignature{staleTS, tok, sig}, now)
	if err == nil {
		t.Fatal("expected stale timestamp to be rejected")
	}
}

func TestVerifyWebhookSignature_FutureTimestamp(t *testing.T) {
	key := "k"
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	futureTS := strconv.FormatInt(now.Add(30*time.Minute).Unix(), 10)
	tok := "t"
	sig := signFor(t, key, futureTS, tok)
	err := VerifyWebhookSignature(key, WebhookSignature{futureTS, tok, sig}, now)
	if err == nil {
		t.Fatal("expected future timestamp to be rejected (clock skew bounds)")
	}
}

func TestVerifyWebhookSignature_MissingFields(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	cases := []WebhookSignature{
		{"", "t", "s"},
		{"123", "", "s"},
		{"123", "t", ""},
	}
	for _, c := range cases {
		if err := VerifyWebhookSignature("k", c, now); err == nil {
			t.Errorf("expected error for case %+v", c)
		}
	}
}

func TestVerifyWebhookSignature_EmptySigningKey(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := VerifyWebhookSignature("", WebhookSignature{"123", "t", "s"}, now); err == nil {
		t.Fatal("empty signing key must always reject")
	}
}
