package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"
)

// This tests the exact pinned production backend, not net.Pipe's stronger Close
// semantics. SYN/SYNACK/ACK are exchanged by the existing real stack harness;
// afterwards outgoing packets are blackholed, so an application write cannot
// finish. A 30 s write deadline matches candidate writeGeneration.
func TestRecoveryNativeCloseDuringInflightSend(t *testing.T) {
	client, server, cc, sc := newTCPStacks(t, 5678, 1500)
	// Exact production firmware Tx/Rx/queue/backoff settings, before opening.
	if e := cc.Configure(tcp.ConnConfig{RxBuf: make([]byte, 1024), TxBuf: make([]byte, 4096), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 5 * time.Millisecond }}); e != nil {
		t.Fatal(e)
	}
	testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
	if cc.State() != tcp.StateEstablished {
		t.Fatal("not established")
	}
	stack := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) {
		return tcpconn{Conn: cc}, nil
	})
	fd, e := stack.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		t.Fatal(e)
	}
	if e = stack.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); e != nil {
		t.Fatal(e)
	}
	sendDone, closeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := stack.Send(fd, make([]byte, cc.FreeOutput()+1), 0, time.Now().Add(30*time.Second))
		sendDone <- e
	}()
	reached := time.Now().Add(time.Second)
	for cc.FreeOutput() != 0 && time.Now().Before(reached) {
		runtime.Gosched()
	}
	if cc.FreeOutput() != 0 {
		t.Fatal("did not reach actual full output buffer")
	}
	t.Log("TARGET_GEOMETRY Tx4096 Rx1024 Queue4 Backoff5ms; attempted 4097-byte Send; no completed transmission or ACK is asserted; no raised firmware limit")
	closeStarted := make(chan struct{})
	go func() { close(closeStarted); closeDone <- stack.Close(fd) }()
	<-closeStarted
	// Keep the real packet egress running; there are no responses/ACKs. This is
	// not a stopped scheduler or stopped network pump. No new workers per tick.
	until := time.Now().Add(10100 * time.Millisecond)
	b := make([]byte, 1600)
	var iterations int
	closeReturned := false
	for time.Now().Before(until) {
		_, _ = client.EgressEthernet(b)
		iterations++
		select {
		case <-closeDone:
			closeReturned = true
		default:
		}
		if closeReturned {
			break
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("REACHED full TX, close called, egress ticks=%d state=%s elapsed exceeds production supervisor grace10s; closeReturned=%t", iterations, cc.State(), closeReturned)
	// A privileged backend-specific Abort is the positive control. It changes
	// connid, unblocking both the actual writer and Close. The public TinyGo
	// net.Conn facade does not expose this method; do not pretend it does.
	cc.Abort()
	select {
	case e = <-sendDone:
		t.Log("ABORT_POSITIVE_CONTROL_SEND_RETURN", e)
	case <-time.After(time.Second):
		t.Fatal("native writer failed to join after Abort")
	}
	if !closeReturned {
		select {
		case e = <-closeDone:
			t.Log("ABORT_POSITIVE_CONTROL_CLOSE_RETURN", e)
		case <-time.After(time.Second):
			t.Fatal("native Close failed to join after Abort")
		}
	}
	if e = stack.Close(fd); e == nil {
		t.Fatal("fd was not deleted by reached first Close")
	}
	if closeReturned {
		t.Fatal("blocker no longer reproduces: re-evaluate architecture")
	}
	if os.Getenv("EXPECT_FIXED") == "1" {
		t.Fatal("FIXED_REQUIREMENT_RED: selected native Close cannot interrupt an in-flight 30s send within existing 10s supervisor grace; replacing workers now or returning before join is unsafe")
	}
	t.Log("BUG_CONFIRMATION_PASS_NOT_FIRMWARE_GREEN")
}
