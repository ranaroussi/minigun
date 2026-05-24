package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 500
)

type Cursor struct {
	AfterIntID    int64  `json:"i,omitempty"`
	AfterStringID string `json:"s,omitempty"`
	AfterCreated  string `json:"t,omitempty"`
}

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	return c, nil
}

func ClampLimit(n int) int {
	if n <= 0 {
		return DefaultPageLimit
	}
	if n > MaxPageLimit {
		return MaxPageLimit
	}
	return n
}
