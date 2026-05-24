package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrInvalidToken = errors.New("invalid unsubscribe token")

type Unsubscribe struct {
	SendID         string
	SubscriptionID int64
}

func Sign(secret, sendID string, subscriptionID int64) string {
	mac := computeMAC([]byte(secret), sendID, subscriptionID)
	return fmt.Sprintf("%s.%d.%s", sendID, subscriptionID, encodeB64(mac))
}

func Verify(secret, token string) (*Unsubscribe, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	sendID := parts[0]
	subID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, ErrInvalidToken
	}
	provided, err := decodeB64(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	expected := computeMAC([]byte(secret), sendID, subID)
	if !hmac.Equal(expected, provided) {
		return nil, ErrInvalidToken
	}
	return &Unsubscribe{SendID: sendID, SubscriptionID: subID}, nil
}

func computeMAC(secret []byte, sendID string, subID int64) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(sendID))
	h.Write([]byte(":"))
	h.Write([]byte(strconv.FormatInt(subID, 10)))
	sum := h.Sum(nil)
	return sum[:16]
}

func encodeB64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeB64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
