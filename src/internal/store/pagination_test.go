package store

import (
	"testing"
)

func TestCursorRoundTripIntID(t *testing.T) {
	c := Cursor{AfterIntID: 42}
	got, err := DecodeCursor(c.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if got.AfterIntID != 42 {
		t.Fatalf("AfterIntID = %d, want 42", got.AfterIntID)
	}
}

func TestCursorRoundTripCreatedAt(t *testing.T) {
	c := Cursor{AfterCreated: "2026-05-24T10:00:00Z", AfterStringID: "snd_abc"}
	got, err := DecodeCursor(c.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if got.AfterCreated != c.AfterCreated || got.AfterStringID != c.AfterStringID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, c)
	}
}

func TestDecodeCursorEmpty(t *testing.T) {
	got, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("DecodeCursor empty: %v", err)
	}
	if got.AfterIntID != 0 || got.AfterCreated != "" || got.AfterStringID != "" {
		t.Fatalf("expected zero cursor, got %+v", got)
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	if _, err := DecodeCursor("!!!not-base64!!!"); err == nil {
		t.Fatal("expected error for invalid cursor")
	}
}

func TestClampLimit(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, DefaultPageLimit},
		{-5, DefaultPageLimit},
		{10, 10},
		{MaxPageLimit, MaxPageLimit},
		{MaxPageLimit + 1, MaxPageLimit},
		{10000, MaxPageLimit},
	}
	for _, tt := range tests {
		if got := ClampLimit(tt.in); got != tt.want {
			t.Errorf("ClampLimit(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
