// Package edgepermit authenticates the control plane's request-bound execution
// permits. Verification does not consume a permit: the control plane must
// atomically claim its RequestID before any upstream execution is allowed.
package edgepermit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	Version       = 1
	MaxLifetime   = 60 * time.Second
	MaxTokenBytes = 8192
	domain        = "lklb-edge-execution-v1\x00"
)

var ErrInvalid = errors.New("invalid edge execution permit")

// Claims never contain upstream credentials or client API keys. BodySHA256
// refers to the exact outbound bytes after the main gateway's model mapping
// and policy transformations, not the original client request.
type Claims struct {
	Version       int    `json:"version"`
	RequestID     string `json:"request_id"`
	NodeID        string `json:"node_id"`
	UserID        int64  `json:"user_id"`
	APIKeyID      int64  `json:"api_key_id"`
	AccountID     int64  `json:"account_id"`
	Model         string `json:"model"`
	BodySHA256    string `json:"body_sha256"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
	IssuedAt      int64  `json:"issued_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

func BodyHash(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// RequestHash binds the outbound destination and credentials as well as the
// exact body. Only canonical, validated headers may be used here.
func RequestHash(target string, headers http.Header, body []byte) string {
	data, _ := json.Marshal(struct {
		Method   string      `json:"method"`
		Target   string      `json:"target"`
		Headers  http.Header `json:"headers"`
		BodyHash string      `json:"body_hash"`
	}{http.MethodPost, target, headers, BodyHash(body)})
	return BodyHash(data)
}

func VerifyExecution(key ed25519.PublicKey, token, node, model, target string, headers http.Header, body []byte, now time.Time) (Claims, error) {
	c, err := Verify(key, token, node, model, body, now)
	if err != nil || c.RequestSHA256 != RequestHash(target, headers, body) {
		return Claims{}, ErrInvalid
	}
	return c, nil
}

func (c Claims) valid() bool {
	if c.Version != Version || c.UserID <= 0 || c.APIKeyID <= 0 || c.AccountID <= 0 ||
		c.IssuedAt <= 0 || c.ExpiresAt <= c.IssuedAt ||
		c.ExpiresAt-c.IssuedAt > int64(MaxLifetime/time.Second) {
		return false
	}
	for _, field := range []string{c.RequestID, c.NodeID, c.Model} {
		if field == "" || len(field) > 128 || strings.TrimSpace(field) != field || strings.ContainsAny(field, "\r\n\x00") {
			return false
		}
	}
	h, err := hex.DecodeString(c.BodySHA256)
	return err == nil && len(h) == sha256.Size && hex.EncodeToString(h) == c.BodySHA256
}

// Sign is called only after the main gateway has authorized the request and
// reserved its budget and user/account concurrency slots.
func Sign(key ed25519.PrivateKey, claims Claims) (string, error) {
	if len(key) != ed25519.PrivateKeySize || !claims.valid() {
		return "", ErrInvalid
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalid
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	sig := ed25519.Sign(key, []byte(domain+payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks the signature, node, validity window, model and exact body.
// The edge has only a public key and therefore cannot grant itself permits.
// It must additionally claim this request at the control plane; a replayed
// signed token remains cryptographically valid until expiry.
func Verify(key ed25519.PublicKey, token, node, model string, body []byte, now time.Time) (Claims, error) {
	var empty Claims
	if len(key) != ed25519.PublicKeySize || len(token) > MaxTokenBytes {
		return empty, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return empty, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || !ed25519.Verify(key, []byte(domain+parts[0]), sig) {
		return empty, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return empty, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil || !c.valid() ||
		c.NodeID != node || c.Model != model || c.BodySHA256 != BodyHash(body) ||
		now.Unix() < c.IssuedAt || now.Unix() >= c.ExpiresAt {
		return empty, ErrInvalid
	}
	return c, nil
}
