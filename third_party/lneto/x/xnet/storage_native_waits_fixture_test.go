//go:build native_storage_fixture

package xnet

import (
	"errors"
	"github.com/soypat/lneto/tcp"
	"net"
	"testing"
	"time"
)

func TestOwnedStorageDrainsActualNativeWaits(t *testing.T) {
	for _, op := range []string{"Read", "Write", "CloseWaitingWrite"} {
		t.Run(op, func(t *testing.T) {
			client, server, _, sc := newTCPStacks(t, 5678, 1500)
			var store tcp.DialStorage
			store.Init()
			entered := make(chan struct{}, 4)
			proceed := make(chan struct{})
			a := NewOwnedDial(&store)
			c, e := a.acquire(client, func(uint) time.Duration {
				// Admission closure precedes Abort. A resumed native operation can
				// retry backoff before Abort gets the stack lock. Observing retries
				// must never fill this channel and strand that real operation.
				select {
				case entered <- struct{}{}:
				default:
				}
				<-proceed
				return 0
			})
			if e != nil {
				t.Fatal(e)
			}
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, c, 8080, 1337)
			returned := make(chan error, 2)
			if op == "Read" {
				go func() { _, e := c.Read(make([]byte, 1)); returned <- e }()
			} else {
				go func() { _, e := c.Write(make([]byte, 4097)); returned <- e }()
			}
			<-entered
			expected := 1
			if op == "CloseWaitingWrite" {
				expected = 2
				go func() { returned <- c.Close() }()
				<-entered
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
					t.Fatal("retirement not reached")
				}
			}
			if _, _, e = store.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(e, tcp.ErrDialStorageBusy) {
				t.Fatal("lease reused with native wait in flight", e)
			}
			select {
			case e = <-retired:
				t.Fatal("retirement bypassed native wait", e)
			default:
			}
			close(proceed)
			for i := 0; i < expected; i++ {
				select {
				case e = <-returned:
					if e == nil {
						t.Error("old wait returned success after abort")
					}
				case <-time.After(time.Second):
					t.Fatal("old native operation did not join")
				}
			}
			if e = <-retired; e != nil {
				t.Fatal(e)
			}
			t.Log("REACHED real native", op, "backoff; interrupted under stack lock, held retirement, joined before backing clear")
		})
	}
}
