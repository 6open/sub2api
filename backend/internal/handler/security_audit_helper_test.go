package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestValidInternalSecurityAuditBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "0123456789abcdef0123456789abcdef"
	t.Setenv("PROMPT_AUDIT_INTERNAL_BYPASS_SECRET", secret)
	t.Setenv("PROMPT_AUDIT_INTERNAL_API_KEY_ID", "9001")
	serviceKey := &service.APIKey{ID: 9001}

	newContext := func(value string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if value != "" {
			c.Request.Header.Set(internalSecurityAuditBypassHeader, value)
		}
		return c
	}

	require.True(t, validInternalSecurityAuditBypass(newContext(secret), serviceKey))
	require.False(t, validInternalSecurityAuditBypass(newContext(secret), &service.APIKey{ID: 9002}))
	require.False(t, validInternalSecurityAuditBypass(newContext("wrong"), serviceKey))
	require.False(t, validInternalSecurityAuditBypass(newContext(""), serviceKey))
	t.Setenv("PROMPT_AUDIT_INTERNAL_BYPASS_SECRET", "short")
	require.False(t, validInternalSecurityAuditBypass(newContext("short"), serviceKey))
}

func TestValidInternalSecurityAuditBypassFromFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PROMPT_AUDIT_INTERNAL_BYPASS_SECRET", "")
	t.Setenv("PROMPT_AUDIT_INTERNAL_API_KEY_ID", "")
	secret := "abcdef0123456789abcdef0123456789"
	path := filepath.Join(t.TempDir(), "bypass.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"secret":"`+secret+`","api_key_id":77}`), 0o600))
	t.Setenv("PROMPT_AUDIT_INTERNAL_BYPASS_FILE", path)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set(internalSecurityAuditBypassHeader, secret)
	require.True(t, validInternalSecurityAuditBypass(c, &service.APIKey{ID: 77}))
	require.False(t, validInternalSecurityAuditBypass(c, &service.APIKey{ID: 78}))
}

func TestCachesSecurityAuditCompletionSkipsWebSocketStages(t *testing.T) {
	require.True(t, cachesSecurityAuditCompletion("http"))
	require.True(t, cachesSecurityAuditCompletion(""))
	require.False(t, cachesSecurityAuditCompletion("first_turn"))
	require.False(t, cachesSecurityAuditCompletion("subsequent_turn"))
}

func TestRunSecurityAuditDoesNotSkipSubsequentWebSocketTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := &turnCountingEngine{mode: securityaudit.ModeAsync}
	coordinator := securityaudit.NewCoordinator(nil, engine)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	subject := middleware2.AuthSubject{UserID: 7, Concurrency: 1}
	first := runSecurityAudit(c, nil, coordinator, nil, nil, subject, "openai_responses", "gpt-test",
		[]byte(`{"type":"response.create","response":{"input":"benign"}}`), "first_turn")
	require.NotNil(t, first)
	require.True(t, first.AllowNextStage)
	require.Equal(t, int64(1), engine.enqueues.Load())
	_, cached := c.Get(securityAuditCompletedContextKey)
	require.False(t, cached, "WebSocket stages must not set the HTTP completion cache")

	// Even if an HTTP path previously cached completion on this Context, WS turns
	// must still audit every response.create payload.
	c.Set(securityAuditCompletedContextKey, true)

	second := runSecurityAudit(c, nil, coordinator, nil, nil, subject, "openai_responses", "gpt-test",
		[]byte(`{"type":"response.create","response":{"input":"malicious follow-up"}}`), "subsequent_turn")
	require.NotNil(t, second)
	require.Equal(t, int64(2), engine.enqueues.Load(), "subsequent WebSocket turns must be audited again")
}

type turnCountingEngine struct {
	mode     securityaudit.Mode
	enqueues atomic.Int64
}

func (e *turnCountingEngine) EffectiveMode() securityaudit.Mode { return e.mode }
func (e *turnCountingEngine) Enqueue(context.Context, securityaudit.Request) error {
	e.enqueues.Add(1)
	return nil
}
func (e *turnCountingEngine) Evaluate(context.Context, securityaudit.Request) (*securityaudit.PromptDecision, error) {
	return &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}, nil
}
