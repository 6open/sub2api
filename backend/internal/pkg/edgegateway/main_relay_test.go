package edgegateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDirectModelRouting(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.4-mini", "gpt-5.2", "gpt-6-astra"} {
		if !directResponses(request(), []byte(`{"model":"`+model+`","input":"hello"}`)) {
			t.Fatal(model)
		}
	}
	for _, body := range []string{`{"model":"gpt-image-2"}`, `{"model":"gpt-5.3-codex-spark"}`, `{"model":"gpt-5.6-sol","tools":[{"type":"web_search"}]}`, `{"model":"gpt-6-astra","tools":[{"type":"image_generation"}]}`} {
		if directResponses(request(), []byte(body)) {
			t.Fatal("specialized operation delegated")
		}
	}
}

func TestCatalogAndMainFallback(t *testing.T) {
	f, _ := fixture(t)
	var allowed atomic.Bool
	allowed.Store(true)
	catalog := `{"object":"list","data":[{"id":"gpt-5.6-luna"},{"id":"gpt-image-2"},{"id":"gpt-4o-audio-preview"}]}`
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed.Load() {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, catalog)
	}))
	defer control.Close()
	var calls atomic.Int32
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer test-admin" {
			t.Error("lost main routing or auth")
		}
		if r.Header.Get("X-Lklb-Client-IP") != "" || r.Header.Get("X-Real-IP") != "" {
			t.Error("spoofed IP forwarded")
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"model":"gpt-image-2","prompt":"test"}` {
			t.Error("body changed")
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer main.Close()
	g, err := New(control.URL, strings.Repeat("s", 32), f.pub, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.store.Close()
	target, _ := url.Parse(main.URL)
	g.mainProxy = newMainProxy(target)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if w.Body.String() != catalog {
		t.Fatal("catalog filtered")
	}
	makeReq := func() *http.Request {
		r := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","prompt":"test"}`))
		r.Header.Set("Authorization", "Bearer test-admin")
		r.Header.Set("X-Lklb-Client-IP", "1.2.3.4")
		return r
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, makeReq())
	if w.Code != 200 || calls.Load() != 1 || g.pending.Load() != 0 {
		t.Fatal("main fallback failed or double settlement")
	}
	allowed.Store(false)
	w = httptest.NewRecorder()
	g.ServeHTTP(w, makeReq())
	if w.Code != 403 || calls.Load() != 1 {
		t.Fatal("pilot bypass")
	}
}
