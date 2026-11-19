package websocket

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
)

type serverWithErrors interface {
	ServeHTTPWithError(http.ResponseWriter, *http.Request) error
}

func errorReturningServer(t *testing.T, server Server) serverWithErrors {
	t.Helper()
	handler, ok := any(server).(serverWithErrors)
	if !ok {
		t.Fatal("server close errors are not observable")
	}
	return handler
}

type serverCloseConn struct {
	*rejectionTestConn
	closeErr error
}

func (c *serverCloseConn) Close() error {
	if err := c.rejectionTestConn.Close(); err != nil {
		return err
	}
	return c.closeErr
}

type serverCloseWriter struct {
	*rejectionHTTPWriter
	owned     *serverCloseConn
	hijackErr error
}

func (w *serverCloseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacks++
	if w.hijackErr != nil {
		return nil, nil, w.hijackErr
	}
	return w.owned, w.buf, nil
}

func TestServerCloseErrorPreservesHandshakeCause(t *testing.T) {
	writeErr, closeErr := errors.New("handshake transport failure"), errors.New("owned close failure")
	for _, kind := range []string{"success", "version", "method", "policy", "selection"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, expected, _ := serverRejectionFixture(t, kind, 0, 16, writeErr)
			for _, budget := range []int{0, len(expected) / 2, len(expected)} {
				for _, failedClose := range []bool{false, true} {
					req, base, hook, _, cause := serverRejectionFixture(t, kind, budget, 16, writeErr)
					owned := &serverCloseConn{rejectionTestConn: base.conn}
					if failedClose {
						owned.closeErr = closeErr
					}
					w := &serverCloseWriter{rejectionHTTPWriter: base, owned: owned}
					calls := 0
					server := Server{Handshake: hook, Handler: func(conn *Conn) {
						calls++
						var got string
						if err := Message.Receive(conn, &got); err != nil || got != "hello" {
							t.Errorf("prefetched frame=%q error=%v", got, err)
						}
					}}
					err := errorReturningServer(t, server).ServeHTTPWithError(w, req)
					if cause != nil && !errors.Is(err, cause) {
						t.Fatalf("primary rejection lost: %v", err)
					}
					if budget < len(expected) && !errors.Is(err, writeErr) {
						t.Fatalf("primary write error lost: %v", err)
					}
					if failedClose && !errors.Is(err, closeErr) {
						t.Fatalf("close error lost: %v", err)
					}
					if !failedClose && budget == len(expected) && err != cause {
						t.Fatalf("original error identity changed: %v", err)
					}
					wantCalls := 0
					if kind == "success" && budget == len(expected) {
						wantCalls = 1
					}
					if calls != wantCalls || base.conn.closes != 1 || base.hijacks != 1 || base.conn.output.String() != expected[:budget] {
						t.Fatalf("handler=%d closes=%d hijacks=%d response prefix match=%t", calls, base.conn.closes, base.hijacks, base.conn.output.String() == expected[:budget])
					}
				}
			}
		})
	}
}

func TestServerClosePublicHTTPCompatibility(t *testing.T) {
	for _, simpleHandler := range []bool{false, true} {
		for _, kind := range []string{"success", "version"} {
			for _, failWrite := range []bool{false, true} {
				req, base, hook, expected, _ := serverRejectionFixture(t, kind, 4096, 16, io.ErrClosedPipe)
				req.Header.Set("Origin", "http://example.invalid")
				if failWrite {
					base.conn.output.remaining = 0
					expected = ""
				}
				w := &serverCloseWriter{rejectionHTTPWriter: base, owned: &serverCloseConn{rejectionTestConn: base.conn, closeErr: errors.New("close failed")}}
				calls := 0
				handler := Handler(func(conn *Conn) {
					calls++
					var got string
					if err := Message.Receive(conn, &got); err != nil || got != "hello" {
						t.Errorf("prefetched frame=%q error=%v", got, err)
					}
				})
				if simpleHandler {
					handler.ServeHTTP(w, req)
				} else {
					(Server{Handler: handler, Handshake: hook}).ServeHTTP(w, req)
				}
				wantCalls := 0
				if kind == "success" && !failWrite {
					wantCalls = 1
				}
				if calls != wantCalls || base.conn.closes != 1 || base.hijacks != 1 || base.conn.output.String() != expected {
					t.Fatal("http.Handler compatibility or owned cleanup changed")
				}
			}
		}
	}
}

func TestServerClosePanicAndHijackOwnership(t *testing.T) {
	panicValue := errors.New("handler panic")
	req, base, hook, _, _ := serverRejectionFixture(t, "success", 4096, 16, io.ErrClosedPipe)
	w := &serverCloseWriter{rejectionHTTPWriter: base, owned: &serverCloseConn{rejectionTestConn: base.conn, closeErr: errors.New("close failed")}}
	handler := errorReturningServer(t, Server{Handshake: hook, Handler: func(*Conn) { panic(panicValue) }})
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Errorf("handler panic replaced: %v", got)
			}
		}()
		if err := handler.ServeHTTPWithError(w, req); err != nil {
			t.Errorf("panic unexpectedly returned as an error: %v", err)
		}
	}()
	if base.conn.closes != 1 {
		t.Fatal("panic did not close owned connection once")
	}
	req, base, hook, _, _ = serverRejectionFixture(t, "success", 4096, 16, io.ErrClosedPipe)
	w = &serverCloseWriter{rejectionHTTPWriter: base, owned: &serverCloseConn{rejectionTestConn: base.conn}, hijackErr: errors.New("hijack failed")}
	handler = errorReturningServer(t, Server{Handshake: hook})
	func() {
		defer func() {
			if got := recover(); got != "Hijack failed: hijack failed" {
				t.Errorf("hijack panic changed: %v", got)
			}
		}()
		if err := handler.ServeHTTPWithError(w, req); err != nil {
			t.Errorf("hijack panic unexpectedly returned: %v", err)
		}
	}()
	if base.conn.closes != 0 || base.hijacks != 1 {
		t.Fatal("failed hijack transferred connection ownership")
	}
}
