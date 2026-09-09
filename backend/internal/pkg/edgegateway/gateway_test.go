package edgegateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgepermit"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type mainFixture struct {
	pub             ed25519.PublicKey
	key             ed25519.PrivateKey
	status          atomic.Int32
	calls           atomic.Int32
	mu              sync.Mutex
	bills           map[string]edgebridge.Receipt
	id              string
	userID          atomic.Int64
	stateRegistered atomic.Bool
}

func fixture(t *testing.T) (*mainFixture, *httptest.Server) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &mainFixture{pub: pub, key: key, bills: map[string]edgebridge.Receipt{}, id: strings.Repeat("a", 32)}
	f.status.Store(200)
	f.userID.Store(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(edgebridge.NodeHeader) != strings.Repeat("s", 32) {
			w.WriteHeader(404)
			return
		}
		switch r.URL.Path {
		case edgebridge.Prefix + "/prepare":
			if r.Header.Get("Authorization") != "Bearer test-admin" {
				w.WriteHeader(401)
				return
			}
			body, _ := io.ReadAll(r.Body)
			headers := http.Header{"Authorization": {"Bearer upstream-canary"}, "Content-Type": {"application/json"}}
			now := time.Now()
			claims := edgepermit.Claims{Version: 1, RequestID: f.id, NodeID: "bwg", UserID: f.userID.Load(), APIKeyID: 108, AccountID: 6, Model: "gpt-6-astra", BodySHA256: edgepermit.BodyHash(body), RequestSHA256: edgepermit.RequestHash("https://chatgpt.com/backend-api/codex/responses", headers, body), IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
			token, err := edgepermit.Sign(f.key, claims)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(edgebridge.Plan{ID: f.id, Permit: token, URL: "https://chatgpt.com/backend-api/codex/responses", Headers: headers, Body: body, Model: claims.Model, ExpiresAt: claims.ExpiresAt})
		case edgebridge.Prefix + "/heartbeat":
			var in struct {
				TurnStateReceived bool `json:"turn_state_received"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.TurnStateReceived {
				if r.Header.Get("Session-Id") != "test-session" {
					w.WriteHeader(403)
					return
				}
				f.stateRegistered.Store(true)
			}
			w.WriteHeader(200)
		case edgebridge.Prefix + "/settle":
			var receipt edgebridge.Receipt
			if json.NewDecoder(r.Body).Decode(&receipt) != nil || receipt.Validate() != nil {
				w.WriteHeader(400)
				return
			}
			f.mu.Lock()
			f.bills[receipt.ID] = receipt
			f.mu.Unlock()
			f.calls.Add(1)
			w.WriteHeader(int(f.status.Load()))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return f, server
}
func mockUpstream(g *Gateway, count *atomic.Int32) {
	g.upstream.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		data := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":3}},\"output\":[]}}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
}
func request() *http.Request {
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":true}`))
	r.Header.Set("Authorization", "Bearer test-admin")
	return r
}
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestStreamTimeoutAndTerminalHandling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		idle     bool
		terminal bool
	}{
		{"idle", true, false}, {"total_deadline", false, false}, {"terminal_without_eof", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, main := fixture(t)
			g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer g.store.Close()
			if g.executionTimeout <= 5*time.Minute || g.upstream.Timeout != 0 {
				t.Fatal("legacy total timeout retained")
			}
			g.executionTimeout = 400 * time.Millisecond
			g.idleTimeout = 100 * time.Millisecond
			g.upstream.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				pr, pw := io.Pipe()
				go func() { <-r.Context().Done(); _ = pw.CloseWithError(r.Context().Err()) }()
				go func() {
					if tc.terminal {
						_, _ = io.WriteString(pw, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
						return
					}
					if tc.idle {
						return
					}
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-r.Context().Done():
							return
						case <-ticker.C:
							if _, e := io.WriteString(pw, ": ping\n\n"); e != nil {
								return
							}
						}
					}
				}()
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Codex-Turn-State": {"opaque-test-state"}}, Body: pr}, nil
			})
			w := httptest.NewRecorder()
			start := time.Now()
			req := request()
			req.Header.Set("Session-Id", "test-session")
			g.ServeHTTP(w, req)
			if !f.stateRegistered.Load() {
				t.Fatal("state was not registered with original session")
			}
			if w.Header().Get("X-Codex-Turn-State") != "opaque-test-state" {
				t.Fatal("missing turn state")
			}
			var saved record
			if err := g.store.Load(f.id, &saved); err != nil {
				t.Fatal(err)
			}
			if tc.terminal {
				if saved.Receipt.Outcome != "completed" || time.Since(start) >= g.executionTimeout {
					t.Fatal("waited for EOF after terminal")
				}
				return
			}
			want := "execution_deadline"
			if tc.idle {
				want = "upstream_idle_timeout"
			}
			if saved.Diagnostic == nil || saved.Diagnostic.Reason != want || saved.Receipt.Outcome != "unknown" {
				t.Fatalf("unexpected outcome: %+v", saved)
			}
			if !strings.Contains(w.Body.String(), want) {
				t.Fatal("client missing explicit error")
			}
		})
	}
}

func TestStreamAndRestartSettlementDoNotRepeatUpstream(t *testing.T) {
	f, main := fixture(t)
	f.status.Store(503)
	dir := filepath.Join(t.TempDir(), "state")
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, dir)
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	mockUpstream(g, &executions)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, request())
	if w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatal("direct response not delivered")
	}
	if f.calls.Load() != 0 {
		t.Fatal("client response waited for settlement")
	}
	if g.pending.Load() != 1 {
		t.Fatal("receipt not persisted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.RunRecovery(ctx); close(done) }()
	waitFor(t, func() bool { return f.calls.Load() > 0 && g.blocked.Load() })
	cancel()
	<-done
	g.store.Close()
	f.status.Store(200)
	restarted, err := New(main.URL, strings.Repeat("s", 32), f.pub, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.store.Close()
	mockUpstream(restarted, &executions)
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { restarted.RunRecovery(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return restarted.pending.Load() == 0 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bills) != 1 || f.bills[f.id].InputTokens != 10 || f.bills[f.id].CachedTokens != 3 || executions.Load() != 1 {
		t.Fatal("recovery changed usage or repeated execution")
	}
	w = httptest.NewRecorder()
	restarted.ServeHTTP(w, request())
	if w.Code != 409 || executions.Load() != 1 {
		t.Fatal("replayed execution allowed")
	}
}
func TestUnauthorizedAndOversizeNeverReachUpstream(t *testing.T) {
	f, main := fixture(t)
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	var n atomic.Int32
	mockUpstream(g, &n)
	r := request()
	r.Header.Del("Authorization")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(strings.Repeat("x", edgebridge.MaxBody+1)))
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	if n.Load() != 0 {
		t.Fatal("unauthorized execution")
	}
}
func TestUnknownExecutionNotAutomaticallyRetried(t *testing.T) {
	f, main := fixture(t)
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	var n int
	g.upstream.Transport = roundTrip(func(*http.Request) (*http.Response, error) { n++; return nil, fmt.Errorf("network ambiguity") })
	w := httptest.NewRecorder()
	g.ServeHTTP(w, request())
	if w.Code != 502 || n != 1 {
		t.Fatal("unexpected retry")
	}
	var saved record
	if err := g.store.Load(f.id, &saved); err != nil || saved.Receipt.Outcome != "unknown" {
		t.Fatal("unknown execution not retained")
	}
}

func TestNativeSSEWithoutContentType(t *testing.T) {
	f, main := fixture(t)
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	var calls atomic.Int32
	mockUpstream(g, &calls)
	base := g.upstream.Transport
	g.upstream.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(r)
		if resp != nil {
			resp.Header.Del("Content-Type")
		}
		return resp, err
	})
	w := httptest.NewRecorder()
	g.ServeHTTP(w, request())
	if w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatal("headerless native SSE rejected")
	}
	var saved record
	if g.store.Load(f.id, &saved) != nil || saved.Receipt.Outcome != "completed" {
		t.Fatal("usage lost")
	}
}

func TestImageHistoryForwardedAndImageUsagePersisted(t *testing.T) {
	f, main := fixture(t)
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	body := `{"model":"gpt-6-astra","stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,test"}]},{"role":"user","content":"hello"}]}`
	g.upstream.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		if string(data) != body {
			t.Error("image payload changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\",\"usage\":{\"input_tokens\":100,\"output_tokens\":2,\"input_tokens_details\":{\"image_tokens\":80}}}}\n\n"))}, nil
	})
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-admin")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("image request rejected: %d", w.Code)
	}
	var saved record
	if g.store.Load(f.id, &saved) != nil || saved.Receipt.ImageInputTokens != 80 || saved.Receipt.InputTokens != 100 {
		t.Fatal("image usage lost")
	}
}

func TestPilot157AllowedButOtherSignedUsersDenied(t *testing.T) {
	for _, id := range []int64{157, 158} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			f, main := fixture(t)
			f.userID.Store(id)
			g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer g.store.Close()
			var calls atomic.Int32
			mockUpstream(g, &calls)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, request())
			if id == 157 {
				if w.Code != 200 || calls.Load() != 1 {
					t.Fatal("approved user rejected")
				}
			} else if w.Code != 403 || calls.Load() != 0 {
				t.Fatal("unapproved user executed")
			}
		})
	}
}

func TestTwentySlotsAndOverflow(t *testing.T) {
	f, main := fixture(t)
	g, err := New(main.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	if cap(g.slots) != 20 {
		t.Fatalf("concurrency=%d", cap(g.slots))
	}
	for i := 0; i < 20; i++ {
		g.slots <- struct{}{}
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, request())
	if w.Code != 503 {
		t.Fatal("twenty-first request admitted")
	}
	for i := 0; i < 20; i++ {
		<-g.slots
	}
	var calls atomic.Int32
	mockUpstream(g, &calls)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, request())
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal("released slot unusable")
	}
}
