//go:build native_storage_fixture

package xnet

import (
	"bufio"
	"errors"
	"github.com/soypat/lneto/internal/tinygotcpfixture"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestTinyGoBerkeleyNativeDeadlineContract(t *testing.T) {
	server, client, sc, cc := newTCPStacks(t, 5678, 1500)
	tst := testerFrom(t, 1500)
	tst.TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
	s := &StackBerkeley{nextFD: 4, conns: []socket[net.Conn]{{sockfd: 3, sock: tcpconn{Conn: cc}}}}
	raw := tinygotcpfixture.Bind(s, 3)
	future := time.Now().Add(time.Hour)
	expired := time.Now().Add(-time.Hour)
	for _, tt := range []struct {
		name  string
		value time.Time
	}{{"future", future}, {"expired", expired}, {"zero", time.Time{}}} {
		t.Run("read_"+tt.name, func(t *testing.T) {
			if e := cc.SetDeadline(expired); e != nil {
				t.Fatal(e)
			}
			if e := raw.SetReadDeadline(tt.value); e != nil {
				t.Fatal(e)
			}
			_, _ = raw.Read(nil)
			r, w := cc.FixtureDeadlines()
			if !r.Equal(tt.value) || !w.Equal(expired) {
				t.Fatalf("READ_FORWARD_REQUIREMENT: got (%v,%v), want (%v,%v)", r, w, tt.value, expired)
			}
		})
		t.Run("write_"+tt.name, func(t *testing.T) {
			if e := cc.SetDeadline(expired); e != nil {
				t.Fatal(e)
			}
			if e := raw.SetWriteDeadline(tt.value); e != nil {
				t.Fatal(e)
			}
			_, e := raw.Write(nil)
			r, w := cc.FixtureDeadlines()
			if !w.Equal(tt.value) || !r.Equal(expired) {
				t.Fatalf("WRITE_FORWARD_REQUIREMENT: got (%v,%v), want (%v,%v)", r, w, expired, tt.value)
			}
			if tt.value.Equal(expired) {
				if !errors.Is(e, os.ErrDeadlineExceeded) {
					t.Fatalf("expired write: %v", e)
				}
			} else if e != nil {
				t.Fatalf("cleared/future write: %v", e)
			}
		})
	}
	t.Run("real_HTTP101_then_idle_then_data", func(t *testing.T) {
		// Short fixture bound, not a changed production timeout. Real monotonic time,
		// real TCP handshake, real HTTP parser, and native reads on both sides of clear.
		bound := time.Now().Add(40 * time.Millisecond)
		_ = raw.SetDeadline(bound)
		response := []byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if _, e := sc.Write(response); e != nil {
			t.Fatal(e)
		}
		tst.TCPExchange(tcpExpectExchange{SourceIdx: 0, WantFlags: pshack, WantData: response}, server, client)
		br := bufio.NewReader(raw)
		res, e := http.ReadResponse(br, nil)
		if e != nil || res.StatusCode != 101 {
			t.Fatalf("real HTTP101: response=%v error=%v", res, e)
		}
		r, _ := cc.FixtureDeadlines()
		if !r.Equal(bound) {
			t.Fatal("handshake bound did not reach native")
		}
		_ = raw.SetDeadline(time.Time{})
		time.Sleep(max(0, time.Until(bound)) + 5*time.Millisecond)
		// Start the actual native read before delivering application bytes. OLD sees
		// the expired handshake deadline and returns; NEW waits for this native data.
		read := make(chan error, 1)
		go func() {
			b := make([]byte, 1)
			n, e := raw.Read(b)
			if e == nil && (n != 1 || b[0] != 'x') {
				e = errors.New("wrong payload")
			}
			read <- e
		}()
		time.Sleep(5 * time.Millisecond)
		if _, e = sc.Write([]byte("x")); e != nil {
			t.Fatal(e)
		}
		b := make([]byte, 1600)
		for i := 0; i < 4; i++ {
			n, _ := server.EgressEthernet(b)
			if n > 0 {
				if e = client.IngressEthernet(b[:n]); e != nil {
					t.Fatal(e)
				}
			}
		}
		select {
		case e = <-read:
			if e != nil {
				t.Fatalf("POST101_NATIVE_READ_REQUIREMENT: %v", e)
			}
		case <-time.After(time.Second):
			cc.Abort()
			<-read
			t.Fatal("native post101 read did not join")
		}
		_, e = raw.Write(nil)
		r, w := cc.FixtureDeadlines()
		if !r.IsZero() || !w.IsZero() || e != nil {
			t.Fatalf("POST101_CLEAR_REQUIREMENT: r=%v w=%v error=%v", r, w, e)
		}
	})
	t.Log("REACHED: TinyGo verbatim methods -> real StackBerkeley -> real tcp.Conn; host source projection, not a full TinyGo package build")
}
