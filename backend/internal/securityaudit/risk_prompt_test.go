package securityaudit

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestExtractLatestUserPrompt(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"answer"},{"role":"user","content":[{"type":"text","text":"latest one"},{"type":"text","text":"latest two"}]}]}`)
	prompt, err := ExtractLatestUserPrompt("openai_chat_completions", body)
	require.NoError(t, err)
	require.Equal(t, "latest one\n\nlatest two", prompt)
}

func TestPrepareRiskPromptRedactsAndBounds(t *testing.T) {
	prompt := "Bearer token-value password=secret-value sk-abcdefghijklmnopqrstuvwxyz\n" +
		"-----BEGIN RSA PRIVATE KEY-----\nprivate\n-----END RSA PRIVATE KEY-----\n" + strings.Repeat("界", 20000)
	redacted, hash, truncated := prepareRiskPrompt(prompt)
	require.NotContains(t, redacted, "token-value")
	require.NotContains(t, redacted, "secret-value")
	require.NotContains(t, redacted, "abcdefghijklmnopqrstuvwxyz")
	require.NotContains(t, redacted, "private\n")
	require.Contains(t, redacted, "[REDACTED_PRIVATE_KEY]")
	require.Len(t, hash, 64)
	require.True(t, truncated)
	require.LessOrEqual(t, len(redacted), maxRiskPromptBytes)
	require.True(t, utf8.ValidString(redacted))
}

func TestFullPromptFromScanTextKeepsOnlyLatestTurn(t *testing.T) {
	require.Equal(t, "latest", FullPromptFromScanText("latest"+promptAuditPrioritySeparator+"older"))
}
