package handler

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
)

// Existing gateway errors/heartbeats are captured during authorization. They
// must not commit the control response before the execution plan is available.
type edgeCaptureWriter struct {
	rec    *httptest.ResponseRecorder
	size   int
	status int
}

func newEdgeCaptureWriter() *edgeCaptureWriter {
	return &edgeCaptureWriter{rec: httptest.NewRecorder(), size: -1, status: 200}
}
func (w *edgeCaptureWriter) Header() http.Header { return w.rec.Header() }
func (w *edgeCaptureWriter) WriteHeader(code int) {
	if !w.Written() {
		w.status = code
	}
}
func (w *edgeCaptureWriter) WriteHeaderNow() {
	if !w.Written() {
		w.rec.WriteHeader(w.status)
		w.size = 0
	}
}
func (w *edgeCaptureWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(p)
	if w.rec.Body.Len()+len(p) <= 65536 {
		_, _ = w.rec.Write(p)
	}
	return len(p), nil
}
func (w *edgeCaptureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *edgeCaptureWriter) Status() int                       { return w.status }
func (w *edgeCaptureWriter) Size() int                         { return w.size }
func (w *edgeCaptureWriter) Written() bool                     { return w.size >= 0 }
func (w *edgeCaptureWriter) Flush()                            { w.WriteHeaderNow() }
func (w *edgeCaptureWriter) CloseNotify() <-chan bool          { return make(chan bool) }
func (w *edgeCaptureWriter) Pusher() http.Pusher               { return nil }
func (w *edgeCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("websocket unavailable during edge authorization")
}
