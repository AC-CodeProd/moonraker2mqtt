//go:build native_storage_fixture

package xnet

import (
	"context"
	"errors"
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
	"net"
	"testing"
	"time"
)

type storageNativeSeam struct {
	tcpconn
	operation        string
	entered, proceed chan struct{}
}

func (s *storageNativeSeam) Read(b []byte) (int, error) {
	if s.operation == "Recv" {
		close(s.entered)
		<-s.proceed
	}
	return s.Conn.Read(b)
}
func (s *storageNativeSeam) Write(b []byte) (int, error) {
	if s.operation == "Send" {
		close(s.entered)
		<-s.proceed
	}
	return s.Conn.Write(b)
}
func (s *storageNativeSeam) Close() error {
	if s.operation == "Close" {
		close(s.entered)
		<-s.proceed
	}
	return s.Conn.Close()
}

func TestOwnedStorageDrainsRealBerkeleySnapshots(t *testing.T) {
	for _, op := range []string{"Send", "Recv", "Close"} {
		t.Run(op, func(t *testing.T) {
			client, server, _, sc := newTCPStacks(t, 5678, 1500)
			var store tcp.DialStorage
			store.Init()
			a := NewOwnedDial(&store)
			cc, e := a.acquire(client, func(uint) time.Duration { return time.Millisecond })
			if e != nil {
				t.Fatal(e)
			}
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
			entered, proceed := make(chan struct{}), make(chan struct{})
			seam := &storageNativeSeam{tcpconn: tcpconn{Conn: cc}, operation: op, entered: entered, proceed: proceed}
			s := &StackBerkeley{nextFD: 4, conns: []socket[net.Conn]{{sockfd: 3, sock: seam}}}
			returned := make(chan error, 1)
			go func() {
				switch op {
				case "Send":
					_, e = s.Send(3, []byte("x"), 0, time.Time{})
				case "Recv":
					_, e = s.Recv(3, make([]byte, 1), 0, time.Time{})
				case "Close":
					e = s.Close(3)
				}
				returned <- e
			}()
			<-entered // Inside the actual Berkeley lookup snapshot, before native dispatch.
			retired := make(chan error, 1)
			go func() { retired <- a.Retire() }()
			// Wait until admissions close. Pin is real production code; no wall-clock
			// inference that a goroutine reached retirement.
			timeout := time.Now().Add(time.Second)
			for {
				end, err := cc.PinNativeCall()
				if errors.Is(err, net.ErrClosed) {
					break
				}
				if end {
					cc.UnpinNativeCall()
				}
				if time.Now().After(timeout) {
					t.Fatal("retirement admission barrier not reached")
				}
			}
			if _, _, err := store.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(err, tcp.ErrDialStorageBusy) {
				t.Fatal("lease released with old native snapshot", err)
			}
			select {
			case err := <-retired:
				t.Fatal("retirement bypassed snapshot", err)
			default:
			}
			close(proceed)
			<-returned
			if err := <-retired; err != nil {
				t.Fatal(err)
			}
			n, token, err := store.Acquire(func(uint) time.Duration { return 0 })
			if err != nil {
				t.Fatal(err)
			}
			if n == cc {
				t.Fatal("Conn was reused")
			}
			if _, err = cc.Write([]byte("stale")); !errors.Is(err, net.ErrClosed) {
				t.Fatal("old native capability admitted", err)
			}
			_ = store.Retire(n, token, func() error { n.Abort(); return nil })
			t.Log("REACHED actual Berkeley", op, "lookup snapshot -> retirement blocked -> real return -> lease free")
		})
	}
}

func TestOwnedStorageDialFailureUnwinds(t *testing.T) {
	client, _, _, _ := newTCPStacks(t, 5678, 1500)
	var store tcp.DialStorage
	store.Init()
	a := NewOwnedDial(&store)
	sg := client.StackGo(func(uint) time.Duration { return time.Millisecond }, StackGoConfig{TCPDialTimeout: time.Millisecond, TCPDialRetries: 1, ListenerPoolConfig: TCPPoolConfig{QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024, NewBackoff: func() lneto.BackoffStrategy { return func(uint) time.Duration { return time.Millisecond } }}})
	ctx, cancel := context.WithCancel(a.Context(context.Background()))
	cancel()
	_, _ = sg.Socket(ctx, "tcp4", AF_INET, SOCK_STREAM, nil, &net.TCPAddr{IP: net.IP{10, 0, 0, 2}, Port: 8080})
	next, token, e := store.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal("failed native dial retained storage", e)
	}
	_ = store.Retire(next, token, func() error { next.Abort(); return nil })
}
