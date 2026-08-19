package securityaudit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	RiskPromptSourceGuard       = "prompt_guard"
	RiskPromptSourceCyberPolicy = "upstream_cyber_policy"
	maxRiskPromptBytes          = 16 * 1024
)

var (
	privateKeyPattern     = regexp.MustCompile(`(?is)-----BEGIN(?: [A-Z0-9]+)? PRIVATE KEY-----.*?-----END(?: [A-Z0-9]+)? PRIVATE KEY-----`)
	passwordJSONPattern   = regexp.MustCompile(`(?i)(["']?(?:password|passwd|pwd|cookie|session(?:_?token)?|access_?token|refresh_?token|api_?key|secret)["']?\s*[:=]\s*["']?)([^\s,"';}]+)`)
	longCredentialPattern = regexp.MustCompile(`\b(?:sk|rk|pk)-[A-Za-z0-9_-]{12,}\b`)
)

type RiskPromptInput struct {
	Source    string
	RequestID string
	UserID    int64
	APIKeyID  int64
	GroupID   *int64
	Model     string
	Prompt    string
}

type RiskPromptRecorder interface {
	RecordRiskPrompt(context.Context, RiskPromptInput) error
}

func ExtractLatestUserPrompt(protocol string, body []byte) (string, error) {
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return "", errors.New("risk prompt request JSON is invalid")
	}
	prompt := latestUserPromptFromSegments(extractProtocolSegments(protocol, document))
	if prompt == "" {
		return "", ErrNoPromptText
	}
	return prompt, nil
}

func prepareRiskPrompt(prompt string) (redacted, hash string, truncated bool) {
	prompt = strings.ReplaceAll(strings.TrimSpace(prompt), "\x00", "")
	digest := sha256.Sum256([]byte(prompt))
	hash = hex.EncodeToString(digest[:])
	redacted = privateKeyPattern.ReplaceAllString(prompt, "[REDACTED_PRIVATE_KEY]")
	redacted = bearerPattern.ReplaceAllString(redacted, "Bearer [REDACTED]")
	redacted = longCredentialPattern.ReplaceAllString(redacted, "[REDACTED_API_KEY]")
	redacted = apiKeyPattern.ReplaceAllString(redacted, "$1 [REDACTED]")
	redacted = passwordJSONPattern.ReplaceAllString(redacted, "$1[REDACTED]")
	if len(redacted) > maxRiskPromptBytes {
		redacted = redacted[:maxRiskPromptBytes]
		for !utf8.ValidString(redacted) {
			redacted = redacted[:len(redacted)-1]
		}
		truncated = true
	}
	return redacted, hash, truncated
}
