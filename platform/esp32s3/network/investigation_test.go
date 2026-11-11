package network

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvestigationNilRunnerReturn(t *testing.T) {
	if err := Session(context.Background(), func() bool { return true }, func(context.Context) error { return nil }, time.Millisecond, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
func TestInvestigationLinkLossOverridesRunnerError(t *testing.T) {
	sentinel := errors.New("offline protocol failure")
	err := Session(context.Background(), func() bool { return false }, func(ctx context.Context) error { <-ctx.Done(); return sentinel }, time.Millisecond, time.Second)
	if err != ErrDown {
		t.Fatalf("got %v", err)
	}
}
func TestInvestigationHealthyCleanupHasNoGraceDeadline(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan error, 1)
	sentinel := errors.New("offline startup failure")
	go func() {
		done <- Session(context.Background(), func() bool { return true }, func(context.Context) error { close(started); <-release; return sentinel }, time.Millisecond, 5*time.Millisecond)
	}()
	<-started
	select {
	case err := <-done:
		close(release)
		t.Fatalf("healthy cleanup unexpectedly bounded: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != sentinel {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner not joined")
	}
}
func TestInvestigationDiagnosticReasons(t *testing.T) {
	for i, e := range []error{nil, errors.New("offline"), context.Canceled, context.DeadlineExceeded, ErrDown, ErrShutdown} {
		if DiagnosticReason(e) != uint8(i) {
			t.Fatalf("reason %d: %d", i, DiagnosticReason(e))
		}
	}
}
