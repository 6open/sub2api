package edgegateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgepermit"
)

type record struct {
	Receipt    edgebridge.Receipt
	Done       bool
	Diagnostic *streamDiagnostic `json:",omitempty"`
}
type streamDiagnostic struct {
	Reason        string
	LastEvent     string
	Bytes         int64
	LastDataAgoMS int64
}

type observedReader struct {
	io.Reader
	last  atomic.Int64
	bytes atomic.Int64
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.last.Store(time.Now().UnixNano())
		r.bytes.Add(int64(n))
	}
	return n, err
}

type Gateway struct {
	mainURL, token    string
	public            ed25519.PublicKey
	store             *edgebridge.Store
	control, upstream *http.Client
	slots             chan struct{}
	active            sync.Map
	pending           atomic.Int64
	blocked           atomic.Bool
	wake              chan struct{}
	reserved          atomic.Int64
	executionTimeout  time.Duration
	idleTimeout       time.Duration
	mainProxy         *httputil.ReverseProxy
}

func New(mainURL, token string, public ed25519.PublicKey, dir string) (*Gateway, error) {
	u, err := url.Parse(mainURL)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Path != "" || len(token) < 32 || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("invalid private control configuration")
	}
	store, err := edgebridge.OpenStore(dir)
	if err != nil {
		return nil, err
	}
	controlTransport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxConnsPerHost: 24, MaxIdleConnsPerHost: 24, MaxIdleConns: 24, ResponseHeaderTimeout: 35 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	transport := &http.Transport{Proxy: nil, DialContext: publicDial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 120 * time.Second, MaxConnsPerHost: edgebridge.MaxConcurrent, MaxIdleConnsPerHost: edgebridge.MaxConcurrent, MaxIdleConns: edgebridge.MaxConcurrent, DisableCompression: true, ForceAttemptHTTP2: true}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	g := &Gateway{mainURL: mainURL, token: token, public: public, store: store, slots: make(chan struct{}, edgebridge.MaxConcurrent), wake: make(chan struct{}, 1), control: &http.Client{Transport: controlTransport, Timeout: 40 * time.Second, CheckRedirect: noRedirect}, upstream: &http.Client{Transport: transport, CheckRedirect: noRedirect}, executionTimeout: time.Duration(edgebridge.ExecutionTimeoutSeconds) * time.Second, idleTimeout: 180 * time.Second}
	g.mainProxy = newMainProxy(&url.URL{Scheme: "https", Host: "lklb.top"})
	ids, err := store.IDs()
	if err != nil {
		store.Close()
		return nil, err
	}
	for _, id := range ids {
		var r record
		if err := store.Load(id, &r); err != nil {
			store.Close()
			return nil, err
		}
		if !r.Done {
			g.pending.Add(1)
		}
	}
	g.blocked.Store(g.pending.Load() > 0)
	return g, nil
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" || (host != "chatgpt.com" && host != "cli-chat-proxy.grok.com" && host != "api.x.ai") {
		return nil, errors.New("upstream denied")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	dial := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range addrs {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
			continue
		}
		conn, err := dial.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("no public upstream reachable")
}
func (g *Gateway) controlRequest(ctx context.Context, path string, data []byte, original *http.Request) (*http.Response, error) {
	method := "POST"
	if data == nil {
		method = "GET"
	}
	r, err := http.NewRequestWithContext(ctx, method, g.mainURL+edgebridge.Prefix+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(edgebridge.NodeHeader, g.token)
	if original != nil {
		for _, h := range []string{"Authorization", "X-Api-Key", "User-Agent", "Session_id", "Session-Id", "Conversation_id", "Conversation-Id", "X-Session-Id", "Originator", "Version", "Openai-Beta", "X-Codex-Turn-State", "X-Codex-Turn-Metadata"} {
			if v := original.Header.Get(h); v != "" {
				r.Header.Set(h, v)
			}
		}
		ip, _, _ := net.SplitHostPort(original.RemoteAddr)
		if a := net.ParseIP(ip); a != nil && a.IsLoopback() {
			if real := original.Header.Get("X-Real-IP"); net.ParseIP(real) != nil {
				ip = real
			}
		}
		if net.ParseIP(ip) != nil {
			r.Header.Set("X-Lklb-Client-IP", ip)
		}
	}
	return g.control.Do(r)
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "edge_error", "message": message}})
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		if g.blocked.Load() {
			w.WriteHeader(503)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"node": "bwg", "pending_settlements": g.pending.Load()})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") || path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(r.URL.Path, "\\") || (r.Method != "GET" && r.Method != "POST" && r.Method != "DELETE") {
		fail(w, 404, "Not found")
		return
	}
	if g.blocked.Load() {
		fail(w, 503, "Pending settlement; retry later")
		return
	}
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		fail(w, 503, "Node busy")
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/models" {
		g.models(w, r)
		return
	}
	size := r.ContentLength
	if size < 0 {
		size = edgebridge.MaxBody
	}
	if size > edgebridge.MaxBody {
		fail(w, 413, "Request too large")
		return
	}
	// Budget the request and decoded plan together, while allowing twenty small
	// requests. Large requests still consume proportionally more of the pool.
	budget := int64(8<<20) + 6*size
	if !memoryHeadroom() {
		fail(w, 503, "Memory pressure")
		return
	}
	for {
		old := g.reserved.Load()
		if old+budget > 192<<20 {
			fail(w, 503, "Request memory budget busy")
			return
		}
		if g.reserved.CompareAndSwap(old, old+budget) {
			break
		}
	}
	defer g.reserved.Add(-budget)
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, edgebridge.MaxBody))
	if err != nil {
		fail(w, 413, "Request too large")
		return
	}
	if !directResponses(r, data) {
		g.relayMain(w, r, data)
		return
	}
	var request struct {
		Stream bool `json:"stream"`
	}
	if json.Unmarshal(data, &request) != nil {
		fail(w, 400, "Invalid JSON")
		return
	}
	response, err := g.controlRequest(r.Context(), "/prepare", data, r)
	if err != nil {
		fail(w, 503, "Main authorization unavailable")
		return
	}
	planLimit := min(int64(12<<20), (budget-2*size)/3)
	planBody, err := io.ReadAll(io.LimitReader(response.Body, planLimit+1))
	response.Body.Close()
	if err != nil || int64(len(planBody)) > planLimit {
		fail(w, 503, "Authorization response unavailable")
		return
	}
	if response.StatusCode != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(planBody)
		return
	}
	w.Header().Set("X-Lklb-Execution-Route", "us")
	var plan edgebridge.Plan
	if json.Unmarshal(planBody, &plan) != nil || !allowedExecutionURL(plan.URL) || len(plan.Body) > edgebridge.MaxBody {
		fail(w, 503, "Invalid execution plan")
		return
	}
	claims, err := edgepermit.VerifyExecution(g.public, plan.Permit, "bwg", plan.Model, plan.URL, plan.Headers, plan.Body, time.Now())
	if err != nil || !edgebridge.PilotUserAllowed(claims.UserID) || claims.RequestID != plan.ID {
		fail(w, 403, "Invalid execution permit")
		return
	}
	if _, loaded := g.active.LoadOrStore(plan.ID, true); loaded {
		fail(w, 409, "Execution already active")
		return
	}
	defer g.active.Delete(plan.ID)
	entry := record{Receipt: edgebridge.Receipt{ID: plan.ID, Outcome: "unknown"}}
	if err := g.store.Save(plan.ID, &entry, true); err != nil {
		fail(w, 409, "Execution already claimed or state unavailable")
		return
	}
	defer func() {
		if err := g.store.Save(plan.ID, &entry, false); err != nil {
			g.blocked.Store(true)
			g.pending.Add(1)
			return
		}
		g.pending.Add(1)
		g.active.Delete(plan.ID)
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}()
	// Client disconnect does not erase billable usage: drain the bounded upstream
	// request and settle it. The heartbeat stops execution if the main is lost.
	ctx, cancel := context.WithTimeout(context.Background(), g.executionTimeout)
	defer cancel()
	if !g.beat(ctx, plan.ID) {
		entry.Receipt.Outcome = "failed"
		entry.Receipt.Status = 503
		fail(w, 503, "Execution lease unavailable")
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		last := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if g.beat(ctx, plan.ID) {
					last = time.Now()
				} else if time.Since(last) > 25*time.Second {
					cancel()
					return
				}
			}
		}
	}()
	upstream, err := http.NewRequestWithContext(ctx, "POST", plan.URL, bytes.NewReader(plan.Body))
	if err != nil {
		fail(w, 503, "Invalid upstream request")
		return
	}
	upstream.Header = plan.Headers.Clone()
	started := time.Now()
	resp, err := g.upstream.Do(upstream)
	if err != nil {
		fail(w, 502, "Upstream outcome unknown")
		entry.Receipt.DurationMS = int(time.Since(started).Milliseconds())
		entry.Diagnostic = &streamDiagnostic{Reason: "upstream_transport_error"}
		slog.Warn("edge_upstream_transport_error", "request_id", plan.ID, "duration_ms", entry.Receipt.DurationMS)
		return
	}
	defer resp.Body.Close()
	entry.Receipt.Status = resp.StatusCode
	entry.Receipt.Headers = receiptHeaders(resp.Header)
	if state := resp.Header.Get("X-Codex-Turn-State"); state != "" {
		if !g.beatWithState(ctx, plan.ID, r) {
			entry.Receipt.DurationMS = int(time.Since(started).Milliseconds())
			entry.Diagnostic = &streamDiagnostic{Reason: "turn_state_registration_failed"}
			fail(w, 503, "Session state registration unavailable")
			return
		}
		w.Header().Set("X-Codex-Turn-State", state)
	}
	if id := resp.Header.Get("X-Request-Id"); id != "" {
		w.Header().Set("X-Request-Id", id)
	}
	w.Header().Set("X-Lklb-Edge-Request-ID", plan.ID)
	w.Header().Set("Cache-Control", "no-store")
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		entry.Receipt.ErrorCode = errorCode(body)
		entry.Receipt.Outcome = "failed"
		entry.Receipt.DurationMS = int(time.Since(started).Milliseconds())
		slog.Warn("edge_upstream_rejected", "request_id", plan.ID, "status", resp.StatusCode, "duration_ms", entry.Receipt.DurationMS)
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}
	// Native Codex can omit Content-Type on a successful SSE response. Parse
	// the bounded event stream as the main gateway does instead of rejecting it.
	if request.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	}
	reader := &observedReader{Reader: io.LimitReader(resp.Body, 64<<20)}
	reader.last.Store(time.Now().UnixNano())
	var idleExpired atomic.Bool
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		tick := time.NewTicker(min(time.Second, g.idleTimeout/2))
		defer tick.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				if time.Since(time.Unix(0, reader.last.Load())) >= g.idleTimeout {
					idleExpired.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var event strings.Builder
	var final json.RawMessage
	lastEvent := ""
	terminal := false
	tooLarge := false
	persistFailed := false
	gone := false
	controller := http.NewResponseController(w)
	parseEvent := func() {
		if event.Len() == 0 {
			return
		}
		var e struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Delta    string          `json:"delta"`
		}
		if json.Unmarshal([]byte(event.String()), &e) == nil {
			if entry.Receipt.FirstOutputMS == nil && effectiveOutputEvent(e.Type, []byte(event.String())) {
				ms := int(time.Since(started).Milliseconds())
				entry.Receipt.FirstOutputMS = &ms
			}
			switch e.Type {
			case "response.created", "response.in_progress", "response.output_text.delta", "response.reasoning_summary_text.delta", "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.completed", "response.failed", "response.incomplete", "error":
				lastEvent = e.Type
			default:
				lastEvent = "other"
			}
			if e.Type == "response.output_text.delta" && entry.Receipt.FirstTokenMS == 0 {
				entry.Receipt.FirstTokenMS = int(time.Since(started).Milliseconds())
			}
			if e.Type == "error" {
				terminal = true
				entry.Receipt.Outcome = "failed"
				entry.Receipt.ErrorCode = errorCode([]byte(event.String()))
			}
			if e.Type == "response.completed" || e.Type == "response.failed" || e.Type == "response.incomplete" {
				terminal = true
				entry.Receipt.ErrorCode = errorCode(e.Response)
				if e.Type != "response.completed" {
					entry.Receipt.Outcome = "failed"
				}
				var result struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Usage *struct {
						Input   int `json:"input_tokens"`
						Output  int `json:"output_tokens"`
						Details struct {
							Image  int `json:"image_tokens"`
							Cached int `json:"cached_tokens"`
							Write  int `json:"cache_write_tokens"`
						} `json:"input_tokens_details"`
					} `json:"usage"`
				}
				if json.Unmarshal(e.Response, &result) == nil && result.Usage != nil {
					entry.Receipt.Model = result.Model
					entry.Receipt.ResponseID = result.ID
					entry.Receipt.InputTokens = result.Usage.Input
					entry.Receipt.ImageInputTokens = result.Usage.Details.Image
					entry.Receipt.OutputTokens = result.Usage.Output
					entry.Receipt.CachedTokens = result.Usage.Details.Cached
					entry.Receipt.CacheWriteTokens = result.Usage.Details.Write
					entry.Receipt.Outcome = "failed"
					if e.Type == "response.completed" {
						entry.Receipt.Outcome = "completed"
					}
					final = append(final[:0], e.Response...)
					entry.Receipt.DurationMS = int(time.Since(started).Milliseconds())
					if g.store.Save(plan.ID, &entry, false) != nil {
						persistFailed = true
						cancel()
					}
				}
			}
		}
		event.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			parseEvent()
			if persistFailed {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			if event.Len()+len(line) > 4<<20 {
				tooLarge = true
				cancel()
				break
			}
			event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			event.WriteByte('\n')
		}
		if request.Stream && !gone {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				gone = true
			} else if len(line) == 0 {
				if controller.Flush() != nil {
					gone = true
				}
			}
		}
		if terminal {
			break
		}
	}
	parseEvent()
	entry.Receipt.DurationMS = int(time.Since(started).Milliseconds())
	if len(final) == 0 {
		reason := "missing_terminal_usage"
		switch {
		case persistFailed:
			reason = "receipt_persist_failed"
		case idleExpired.Load():
			reason = "upstream_idle_timeout"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			reason = "execution_deadline"
		case ctx.Err() != nil:
			reason = "execution_cancelled"
		case tooLarge:
			reason = "event_too_large"
		case reader.bytes.Load() >= 64<<20:
			reason = "stream_size_limit"
		case scanner.Err() != nil:
			reason = "stream_read_error"
		}
		entry.Diagnostic = &streamDiagnostic{Reason: reason, LastEvent: lastEvent, Bytes: reader.bytes.Load(), LastDataAgoMS: time.Since(time.Unix(0, reader.last.Load())).Milliseconds()}
		slog.Warn("edge_stream_incomplete", "request_id", plan.ID, "reason", reason, "last_event", lastEvent, "bytes", entry.Diagnostic.Bytes, "last_data_ago_ms", entry.Diagnostic.LastDataAgoMS, "duration_ms", entry.Receipt.DurationMS)
		if request.Stream && !gone {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, _ = fmt.Fprintf(w, "\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"edge_error\",\"code\":%q,\"message\":\"Upstream stream did not complete\"}}\n\n", reason)
			_ = controller.Flush()
		}
	}
	if !request.Stream {
		if len(final) == 0 {
			fail(w, 502, "Missing terminal usage")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(final)
		}
	}
}
func receiptHeaders(h http.Header) http.Header {
	out := http.Header{}
	for k, v := range h {
		if strings.HasPrefix(k, "X-Codex-") || k == "Retry-After" || k == "Date" || k == "X-Request-Id" {
			out[k] = append([]string(nil), v...)
		}
	}
	return out
}

func errorCode(body []byte) string {
	var v struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	code := v.Error.Code
	if code == "" {
		code = v.Error.Type
	}
	if len(code) > 80 || strings.ContainsAny(code, "\r\n\x00") {
		return ""
	}
	return code
}

func memoryHeadroom() bool {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "MemAvailable:" {
			n, err := strconv.ParseUint(f[1], 10, 64)
			return err == nil && n >= 250*1024
		}
	}
	return false
}
func (g *Gateway) beat(parent context.Context, id string) bool {
	return g.beatWithState(parent, id, nil)
}
func (g *Gateway) beatWithState(parent context.Context, id string, original *http.Request) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	data, _ := json.Marshal(map[string]any{"id": id, "turn_state_received": original != nil})
	resp, err := g.controlRequest(ctx, "/heartbeat", data, original)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == 200
}
func (g *Gateway) deliver(entry record) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, _ := json.Marshal(entry.Receipt)
	resp, err := g.controlRequest(ctx, "/settle", data, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return false
	}
	entry.Done = true
	return g.store.Save(entry.Receipt.ID, &entry, false) == nil
}
func (g *Gateway) RunRecovery(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-g.wake:
		}
		if g.pending.Load() == 0 {
			continue
		}
		ids, err := g.store.IDs()
		if err != nil {
			continue
		}
		for _, id := range ids {
			if _, ok := g.active.Load(id); ok {
				continue
			}
			var entry record
			if g.store.Load(id, &entry) != nil || entry.Done {
				continue
			}
			if g.deliver(entry) {
				g.pending.Add(-1)
			} else {
				g.blocked.Store(true)
			}
		}
		if g.pending.Load() == 0 {
			g.blocked.Store(false)
		}
	}
}
func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	path := "/models"
	if v := r.URL.Query().Get("client_version"); v != "" {
		path += "?client_version=" + url.QueryEscape(v)
	}
	resp, err := g.controlRequest(r.Context(), path, nil, r)
	if err != nil {
		fail(w, 503, "Main unavailable")
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		fail(w, 503, "Models unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
