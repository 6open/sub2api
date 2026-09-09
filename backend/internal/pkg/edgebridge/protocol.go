package edgebridge

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
)

const MaxBody = 8 << 20
const MaxConcurrent = 20
const ExecutionTimeoutSeconds = 1200
const AdminID int64 = 1
const NodeHeader = "X-Lklb-Node-Token"
const Prefix = "/internal/lklb-edge"

var ErrDelegated = errors.New("edge execution delegated")

type Plan struct {
	ID        string      `json:"id"`
	Permit    string      `json:"permit"`
	URL       string      `json:"url"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body"`
	Model     string      `json:"model"`
	ExpiresAt int64       `json:"expires_at"`
}
type Receipt struct {
	ID               string      `json:"id"`
	Status           int         `json:"status"`
	Outcome          string      `json:"outcome"`
	Model            string      `json:"model"`
	InputTokens      int         `json:"input_tokens"`
	ImageInputTokens int         `json:"image_input_tokens,omitempty"`
	OutputTokens     int         `json:"output_tokens"`
	CachedTokens     int         `json:"cached_tokens"`
	CacheWriteTokens int         `json:"cache_write_tokens"`
	DurationMS       int         `json:"duration_ms"`
	FirstTokenMS     int         `json:"first_token_ms"`
	FirstOutputMS    *int        `json:"first_output_ms,omitempty"`
	Headers          http.Header `json:"headers"`
	ErrorCode        string      `json:"error_code,omitempty"`
	ResponseID       string      `json:"response_id,omitempty"`
}

func (r Receipt) Validate() error {
	if r.FirstOutputMS != nil && (*r.FirstOutputMS < 0 || *r.FirstOutputMS > (ExecutionTimeoutSeconds+120)*1000) {
		return errors.New("invalid first output time")
	}
	if r.ImageInputTokens < 0 || r.ImageInputTokens > r.InputTokens {
		return errors.New("invalid image token usage")
	}
	if len(r.ResponseID) > 160 || strings.ContainsAny(r.ResponseID, "\r\n\x00") {
		return errors.New("invalid response ID")
	}
	if len(r.ErrorCode) > 80 || strings.ContainsAny(r.ErrorCode, "\r\n\x00") {
		return errors.New("invalid error code")
	}
	if len(r.Model) > 128 || strings.ContainsAny(r.Model, "\r\n\x00") || r.CachedTokens > r.InputTokens || r.CacheWriteTokens > r.InputTokens {
		return errors.New("invalid receipt")
	}
	for k, values := range r.Headers {
		if !strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Codex-") && k != "Retry-After" && k != "Date" && k != "X-Request-Id" {
			return errors.New("invalid receipt header")
		}
		for _, v := range values {
			if len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
				return errors.New("invalid receipt header")
			}
		}
	}
	if r.ID == "" || r.Status < 0 || r.Status > 599 || r.DurationMS < 0 || r.DurationMS > (ExecutionTimeoutSeconds+120)*1000 || r.FirstTokenMS < 0 || r.InputTokens < 0 || r.InputTokens > 16<<20 || r.OutputTokens < 0 || r.OutputTokens > 1<<20 || r.CachedTokens < 0 || r.CacheWriteTokens < 0 || r.CachedTokens+r.CacheWriteTokens > r.InputTokens {
		return errors.New("invalid receipt")
	}
	switch r.Outcome {
	case "completed", "failed", "unknown":
	default:
		return errors.New("invalid outcome")
	}
	return nil
}

type contextKey struct{}
type Session struct {
	ID                string
	UserID            int64
	KeyID             int64
	UserMax           int
	Issued            atomic.Bool
	Used              atomic.Bool
	Snapshot          any
	Capture           func(*http.Request, int64) error
	BeforeAccountSlot func(int64) error
}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func FromContext(ctx context.Context) *Session { s, _ := ctx.Value(contextKey{}).(*Session); return s }
func SlotID(ctx context.Context, fallback string) string {
	if s := FromContext(ctx); s != nil {
		return "edge:" + s.ID
	}
	return fallback
}
func MayRelease(ctx context.Context) bool { s := FromContext(ctx); return s == nil || !s.Issued.Load() }
func Capture(req *http.Request, id int64) (bool, error) {
	s := FromContext(req.Context())
	if s == nil {
		return false, nil
	}
	if !s.Used.Swap(true) {
		_ = s.Capture(req, id)
	}
	return true, ErrDelegated
}
