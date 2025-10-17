package httpp

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"
)

type writeTimeoutWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

func (w *writeTimeoutWriter) Header() http.Header {
	return w.w.Header()
}

func (w *writeTimeoutWriter) Write(p []byte) (int, error) {
	w.rc.SetWriteDeadline(time.Now().Add(w.timeout)) //nolint:errcheck
	return w.w.Write(p)
}

func (w *writeTimeoutWriter) WriteHeader(statusCode int) {
	w.rc.SetWriteDeadline(time.Now().Add(w.timeout)) //nolint:errcheck
	w.w.WriteHeader(statusCode)
}

// Hijack implements http.Hijacker interface for WebSocket support
func (w *writeTimeoutWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

// apply write deadline before every Write() call.
// this allows to write long responses, splitted in chunks,
// without causing timeouts.
type handlerWriteTimeout struct {
	h       http.Handler
	timeout time.Duration
}

func (h *handlerWriteTimeout) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ww := &writeTimeoutWriter{
		w:       w,
		rc:      http.NewResponseController(w),
		timeout: h.timeout,
	}

	h.h.ServeHTTP(ww, r)
}
