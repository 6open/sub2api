package edgepermit

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestPermitBinding(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	body := []byte(`{"model":"test-model","input":"hello"}`)
	c := Claims{Version: Version, RequestID: "request-1", NodeID: "bwg", UserID: 1,
		APIKeyID: 2, AccountID: 3, Model: "test-model", BodySHA256: BodyHash(body),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(MaxLifetime).Unix()}
	token, err := Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(pub, token, "bwg", c.Model, body, now)
	if err != nil || got != c {
		t.Fatalf("round trip: claims=%+v error=%v", got, err)
	}

	for _, tc := range []struct {
		name               string
		key                ed25519.PublicKey
		token, node, model string
		body               []byte
		at                 time.Time
	}{
		{"wrong-signer", other, token, "bwg", c.Model, body, now},
		{"missing-key", nil, token, "bwg", c.Model, body, now},
		{"wrong-node", pub, token, "another-node", c.Model, body, now},
		{"wrong-model", pub, token, "bwg", "other-model", body, now},
		{"changed-body", pub, token, "bwg", c.Model, append(append([]byte{}, body...), ' '), now},
		{"expired", pub, token, "bwg", c.Model, body, now.Add(MaxLifetime)},
		{"future", pub, token, "bwg", c.Model, body, now.Add(-time.Second)},
		{"tampered", pub, "A" + token[1:], "bwg", c.Model, body, now},
		{"oversized", pub, strings.Repeat("a", MaxTokenBytes+1), "bwg", c.Model, body, now},
		{"malformed", pub, "a.b.c", "bwg", c.Model, body, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Verify(tc.key, tc.token, tc.node, tc.model, tc.body, tc.at); err != ErrInvalid {
				t.Fatalf("wanted rejection, got %v", err)
			}
		})
	}
	// Signature verification deliberately does not claim a lease. This must not
	// be mistaken for replay protection by the future HTTP handler.
	if _, err := Verify(pub, token, "bwg", c.Model, body, now); err != nil {
		t.Fatal(err)
	}
}

func TestRejectInvalidClaims(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base := Claims{Version: Version, RequestID: "r", NodeID: "bwg", UserID: 1, APIKeyID: 2,
		AccountID: 3, Model: "m", BodySHA256: BodyHash(nil), IssuedAt: 100, ExpiresAt: 150}
	for _, mutate := range []func(*Claims){
		func(c *Claims) { c.Version = 2 },
		func(c *Claims) { c.ExpiresAt = c.IssuedAt },
		func(c *Claims) { c.ExpiresAt = c.IssuedAt + 61 },
		func(c *Claims) { c.NodeID = "" },
		func(c *Claims) { c.AccountID = 0 },
		func(c *Claims) { c.BodySHA256 = "not-a-hash" },
	} {
		c := base
		mutate(&c)
		if _, err := Sign(priv, c); err != ErrInvalid {
			t.Fatalf("accepted invalid claims: %+v", c)
		}
	}
	if _, err := Sign(nil, base); err != ErrInvalid {
		t.Fatal("accepted missing signing key")
	}
}

func FuzzVerify(f *testing.F) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	f.Add("a.b")
	f.Add("")
	f.Fuzz(func(t *testing.T, token string) {
		_, _ = Verify(pub, token, "bwg", "model", nil, time.Unix(1800000000, 0))
	})
}
