package token

import "testing"

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := "super-secret-key"
	tok := Sign(secret, "s_test123", 42)
	out, err := Verify(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.SendID != "s_test123" || out.SubscriptionID != 42 {
		t.Fatalf("got %+v", out)
	}
}

func TestVerifyTampered(t *testing.T) {
	secret := "super-secret-key"
	tok := Sign(secret, "s_test123", 42)
	if _, err := Verify(secret, tok+"x"); err == nil {
		t.Fatal("expected error for tampered token")
	}
	if _, err := Verify("wrong-secret", tok); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestVerifyMalformed(t *testing.T) {
	if _, err := Verify("k", "not.a.token.at.all"); err == nil {
		t.Fatal("expected error for malformed token")
	}
	if _, err := Verify("k", "only-one-part"); err == nil {
		t.Fatal("expected error for too few parts")
	}
}
