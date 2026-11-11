//go:build native_storage_fixture

package xnet

import (
	"errors"
	"github.com/soypat/lneto/tcp"
	"net"
	"sync"
	"testing"
	"time"
)

type storageCallbackSeam struct {
	c                *tcp.Conn
	mode             string
	entered, proceed chan struct{}
	once             sync.Once
}

func (s *storageCallbackSeam) LocalPort() uint16     { return s.c.LocalPort() }
func (s *storageCallbackSeam) Protocol() uint64      { return s.c.Protocol() }
func (s *storageCallbackSeam) ConnectionID() *uint64 { return s.c.ConnectionID() }
func (s *storageCallbackSeam) Demux(b []byte, o int) error {
	if s.mode == "ingress" {
		s.once.Do(func() { close(s.entered) })
		<-s.proceed
	}
	return s.c.Demux(b, o)
}
func (s *storageCallbackSeam) Encapsulate(b []byte, i, o int) (int, error) {
	if s.mode == "egress" {
		s.once.Do(func() { close(s.entered) })
		<-s.proceed
	}
	return s.c.Encapsulate(b, i, o)
}

func TestOwnedStorageWaitsStackCallbacks(t *testing.T) {
	for _, mode := range []string{"ingress", "egress"} {
		t.Run(mode, func(t *testing.T) {
			client, server, _, sc := newTCPStacks(t, 5678, 1500)
			var store tcp.DialStorage
			store.Init()
			a := NewOwnedDial(&store)
			c, e := a.acquire(client, func(uint) time.Duration { return time.Millisecond })
			if e != nil {
				t.Fatal(e)
			}
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, c, 8080, 1337)
			entered, proceed := make(chan struct{}), make(chan struct{})
			seam := &storageCallbackSeam{c: c, mode: mode, entered: entered, proceed: proceed}
			oldDemux, oldEgress := seam.Demux, seam.Encapsulate
			mac := server.HardwareAddr()
			client.mu.Lock()
			client.tcps.DetachTCP(c)
			e = client.tcps.RegisterMACFiltered(seam, mac[:])
			client.mu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			b := make([]byte, 1600)
			if mode == "ingress" {
				if _, e = sc.Write([]byte("old packet")); e != nil {
					t.Fatal(e)
				}
				n, err := server.EgressEthernet(b)
				if err != nil || n == 0 {
					t.Fatal("ingress packet not produced", n, err)
				}
				b = b[:n]
			}
			callbackDone := make(chan struct{})
			go func() {
				defer close(callbackDone)
				if mode == "ingress" {
					_ = client.IngressEthernet(b)
				} else {
					_, _ = client.EgressEthernet(b)
				}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("actual stack callback not reached")
			}
			retired := make(chan error, 1)
			go func() { retired <- a.Retire() }()
			bound := time.Now().Add(time.Second)
			for {
				p, e := c.PinNativeCall()
				if errors.Is(e, net.ErrClosed) {
					break
				}
				if p {
					c.UnpinNativeCall()
				}
				if time.Now().After(bound) {
					t.Fatal("retirement admission not reached")
				}
			}
			if _, _, e = store.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(e, tcp.ErrDialStorageBusy) {
				t.Fatal("lease reused with stack callback outstanding", e)
			}
			select {
			case e = <-retired:
				t.Fatal("retirement bypassed stack callback", e)
			default:
			}
			close(proceed)
			<-callbackDone
			if e = <-retired; e != nil {
				t.Fatal(e)
			}
			next, token, e := store.Acquire(func(uint) time.Duration { return 0 })
			if e != nil {
				t.Fatal(e)
			}
			// Retained callback snapshot from the old node, invoked after reuse, is
			// denied by the old Conn's permanent admission tombstone.
			if _, e = oldEgress(make([]byte, 1600), 0, 20); !errors.Is(e, net.ErrClosed) {
				t.Fatal("stale egress callback admitted", e)
			}
			if e = oldDemux(make([]byte, 64), 20); !errors.Is(e, net.ErrClosed) {
				t.Fatal("stale ingress callback admitted", e)
			}
			_ = store.Retire(next, token, func() error { next.Abort(); return nil })
			t.Log("REACHED real stack", mode, "callback held under stack lock, retirement held, returned, node detached, lease reused; stale Conn callbacks refused")
		})
	}
}
