package edgegateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"
)

// Only the Codex text execution path is delegated. Other modalities and API
// shapes retain the main gateway's parsers, policies, streaming and billing.
func directResponses(r *http.Request, data []byte) bool {
	if r.Method != "POST" || r.URL.Path != "/v1/responses" || !edgebridge.SupportedInput(data) || !edgebridge.ClientToolsOnly(data) {
		return false
	}
	var v struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return (strings.HasPrefix(v.Model, "gpt-5") && !strings.Contains(v.Model, "spark")) || v.Model == "gpt-6-astra" || strings.HasPrefix(strings.ToLower(v.Model), "grok")
}

func newMainProxy(target *url.URL) *httputil.ReverseProxy {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = edgebridge.MaxConcurrent
	transport.MaxIdleConnsPerHost = edgebridge.MaxConcurrent
	transport.MaxIdleConns = edgebridge.MaxConcurrent
	transport.ResponseHeaderTimeout = 120 * time.Second
	return &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = target.Host
			p.Out.Header.Del(edgebridge.NodeHeader)
			for _, h := range []string{"X-Real-IP", "X-Forwarded-For", "Forwarded", "CF-Connecting-IP", "True-Client-IP", "X-Lklb-Client-IP"} {
				p.Out.Header.Del(h)
			}
		},
		Transport: transport, FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			fail(w, 502, "Main gateway connection failed")
		},
	}
}

func allowedExecutionURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Port() != "" || u.User != nil || u.RawQuery != "" {
		return false
	}
	if u.Host == "chatgpt.com" && u.Path == "/backend-api/codex/responses" {
		return true
	}
	if (u.Host == "cli-chat-proxy.grok.com" || u.Host == "api.x.ai") && (u.Path == "/v1/responses" || u.Path == "/responses") {
		return true
	}
	return false
}

func (g *Gateway) relayMain(w http.ResponseWriter, r *http.Request, data []byte) {
	// Reuse the control endpoint's auth + pilot eligibility check before sending
	// any request to the main gateway. Main performs actual admission and billing.
	auth, err := g.controlRequest(r.Context(), "/models", nil, r)
	if err != nil {
		fail(w, 503, "Main authorization unavailable")
		return
	}
	b, err := io.ReadAll(io.LimitReader(auth.Body, (1<<20)+1))
	auth.Body.Close()
	if err != nil || len(b) > 1<<20 {
		fail(w, 503, "Main authorization unavailable")
		return
	}
	if auth.StatusCode != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(auth.StatusCode)
		_, _ = w.Write(b)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.executionTimeout)
	defer cancel()
	out := r.Clone(ctx)
	out.Body = io.NopCloser(bytes.NewReader(data))
	out.ContentLength = int64(len(data))
	out.TransferEncoding = nil
	w.Header().Set("X-Lklb-Execution-Route", "main")
	g.mainProxy.ServeHTTP(w, out)
}
