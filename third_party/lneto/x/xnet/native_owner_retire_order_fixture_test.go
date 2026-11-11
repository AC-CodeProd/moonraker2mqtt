//go:build native_storage_fixture

package xnet

import (
	"context"
	"errors"
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeOwnerRetireInterruptsBeforeWaitingClose(t *testing.T) {
	client, server, _, sc := newTCPStacks(t, 5678, 1500)
	entered, closeEntered, proceed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	var backoffs atomic.Int32
	backoff := lneto.BackoffStrategy(func(uint) time.Duration {
		if armed.Load() {
			n := backoffs.Add(1)
			if n == 1 {
				close(entered)
			}
			if n == 2 {
				close(closeEntered)
			}
			<-proceed
			return 0
		}
		return time.Millisecond
	})
	sg := client.StackGo(backoff, StackGoConfig{ListenerPoolConfig: TCPPoolConfig{PoolSize: 2, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024, NewBackoff: func() lneto.BackoffStrategy { return backoff }}})
	if e := server.ListenTCP4(sc, 8080); e != nil {
		t.Fatal(e)
	}
	stop, pumpDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(pumpDone)
		b := make([]byte, 1600)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, _ := client.EgressEthernet(b)
			if n > 0 {
				_ = server.IngressEthernet(b[:n])
			}
			n, _ = server.EgressEthernet(b)
			if n > 0 {
				_ = client.IngressEthernet(b[:n])
			}
			time.Sleep(time.Millisecond)
		}
	}()
	owner := NewNativeDialOwner(sg)
	stream, e := owner.DialContext(context.Background(), "tcp", net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.AddrFrom4(server.Addr4()), 8080)))
	close(stop)
	<-pumpDone
	if e != nil {
		t.Fatal(e)
	}
	c := owner.attempt.conn
	interrupt, e := stream.GenerationInterrupt()
	if e != nil {
		t.Fatal(e)
	}
	armed.Store(true)
	writeDone := make(chan error, 1)
	go func() { _, e := stream.Send(make([]byte, 4097), time.Now().Add(time.Minute)); writeDone <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("real native full-TX backoff not reached")
	}
	retired := make(chan error, 1)
	go func() { retired <- owner.Retire() }()
	admissionClosed, watchDone, watchStop := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			select {
			case <-watchStop:
				return
			default:
			}
			pin, e := c.PinNativeCall()
			if errors.Is(e, net.ErrClosed) {
				close(admissionClosed)
				return
			}
			if pin {
				c.UnpinNativeCall()
			}
			runtime.Gosched()
		}
	}()
	old := false
	select {
	case <-admissionClosed:
		if _, _, e = owner.storage.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(e, tcp.ErrDialStorageBusy) {
			t.Error("lease released while real native Send held", e)
		}
		select {
		case e = <-retired:
			t.Fatal("owner retired before Send return", e)
		default:
		}
	case <-closeEntered:
		old = true
		t.Error("RETIRE_ADMISSION_BEFORE_CLOSE_REQUIREMENT: owner entered graceful Close while native writer held; native admissions still open")
	case <-time.After(time.Second):
		old = true
		t.Error("owner admission/Close barrier not reached")
	}
	close(watchStop)
	<-watchDone
	if old {
		_ = interrupt()
	} // OLD harness teardown only, not a fixed-behaviour oracle.
	close(proceed)
	if e = <-writeDone; e == nil {
		t.Error("blocked old writer returned success")
	}
	select {
	case e = <-retired:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("retire did not join actual native writer/Close")
	}
	stream.stack.mu.Lock()
	still := stream.stack.getConn(stream.fd).isvalid()
	stream.stack.mu.Unlock()
	if still {
		t.Fatal("old private FD retained after retirement")
	}
	t.Log("REACHED owner API itself, real Send/full-TX native wait; closed admissions+abort precede native Close; fd detached and native return joined before lease free")
}
