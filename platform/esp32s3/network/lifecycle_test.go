package network

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeDevice struct {
	calls               []string
	failures            map[string]int
	enabled, associated bool
	pumps, maxPumps     int
	loseDuringDHCP      bool
}

func (d *fakeDevice) step(s string) error {
	d.calls = append(d.calls, s)
	if d.failures[s] > 0 {
		d.failures[s]--
		return errors.New("secret-driver-value")
	}
	return nil
}
func (d *fakeDevice) Enable() error {
	if d.enabled {
		d.calls = append(d.calls, "enable-again")
		return errors.New("already enabled")
	}
	if err := d.step("enable"); err != nil {
		return err
	}
	d.enabled = true
	return nil
}
func (d *fakeDevice) Start() error { return d.step("start") }
func (d *fakeDevice) Associate(string, string) error {
	if err := d.step("associate"); err != nil {
		return err
	}
	d.associated = true
	return nil
}
func (d *fakeDevice) OpenStack() error {
	if err := d.step("stack"); err != nil {
		return err
	}
	d.pumps++
	if d.pumps > d.maxPumps {
		d.maxPumps = d.pumps
	}
	return nil
}
func (d *fakeDevice) DHCP() error {
	if d.loseDuringDHCP {
		d.associated = false
	}
	return d.step("dhcp")
}
func (d *fakeDevice) Associated() bool { return d.associated }
func (d *fakeDevice) DropStack() error {
	if err := d.step("drop"); err != nil {
		return err
	}
	d.pumps = 0
	return nil
}
func (d *fakeDevice) Stop() error {
	if err := d.step("stop"); err != nil {
		return err
	}
	d.associated = false
	return nil
}

func TestRetryAfterEachPostEnableFailure(t *testing.T) {
	for _, stage := range []string{"start", "associate", "stack", "dhcp"} {
		t.Run(stage, func(t *testing.T) {
			d := &fakeDevice{failures: map[string]int{stage: 1}}
			l := Lifecycle{Device: d}
			want := map[string]error{"start": ErrStart, "associate": ErrAssociate, "stack": ErrStack, "dhcp": ErrDHCP}[stage]
			if err := l.Connect("ssid-secret", "password-secret"); err != want {
				t.Fatalf("stage error leaked/replaced: got %v want %v", err, want)
			}
			if d.pumps != 0 || d.associated {
				t.Fatal("failure leaked active network")
			}
			if err := l.Connect("ssid-secret", "password-secret"); err != nil {
				t.Fatal(err)
			}
			if !l.Connected() {
				t.Fatal("retry did not acquire DHCP")
			}
			count := 0
			for _, c := range d.calls {
				if c == "enable" {
					count++
				}
				if c == "enable-again" {
					t.Fatal("initialization retried")
				}
			}
			if count != 1 || d.maxPumps != 1 {
				t.Fatal(d.calls, d.maxPumps)
			}
		})
	}
}

func TestDHCPResultAfterAssociationLossIsRejected(t *testing.T) {
	d := &fakeDevice{loseDuringDHCP: true}
	l := Lifecycle{Device: d}
	if err := l.Connect("ssid", "password"); err != ErrDown {
		t.Fatal("stale DHCP result accepted", err)
	}
	if l.Connected() || d.pumps != 0 {
		t.Fatal("lost link retained a usable stack")
	}
	d.loseDuringDHCP = false
	if err := l.Connect("ssid", "password"); err != nil {
		t.Fatal("fresh association/DHCP retry failed", err)
	}
	if err := l.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedLinkLossAndExplicitDisconnect(t *testing.T) {
	d := &fakeDevice{}
	l := Lifecycle{Device: d}
	for i := 0; i < 20; i++ {
		if err := l.Connect("ssid", "password"); err != nil {
			t.Fatal(err)
		}
		before := len(d.calls)
		if err := l.Connect("ssid", "password"); err != nil {
			t.Fatal(err)
		}
		if len(d.calls) != before {
			t.Fatal("healthy connect was not idempotent")
		}
		d.associated = false
		if l.Connected() {
			t.Fatal("retained DHCP address counted as association")
		}
		if err := l.Disconnect(); err != nil {
			t.Fatal(err)
		}
		if err := l.Disconnect(); err != nil {
			t.Fatal(err)
		}
	}
	if d.pumps != 0 || d.maxPumps != 1 {
		t.Fatal("pump leak")
	}
}

func TestEnableAndShutdownFailuresLatch(t *testing.T) {
	for _, stage := range []string{"enable", "drop", "stop"} {
		t.Run(stage, func(t *testing.T) {
			d := &fakeDevice{failures: map[string]int{stage: 1, "associate": 1}}
			l := Lifecycle{Device: d}
			err := l.Connect("ssid", "password")
			if err != ErrEnable && err != ErrShutdown {
				t.Fatal(err)
			}
			before := append([]string(nil), d.calls...)
			if l.Connect("ssid", "password") != err {
				t.Fatal("fatal state retried")
			}
			if !reflect.DeepEqual(before, d.calls) {
				t.Fatal("used faulty radio")
			}
			if stage == "drop" {
				for _, c := range d.calls {
					if c == "stop" {
						t.Fatal("stopped before pump joined")
					}
				}
			}
		})
	}
}

func TestBackoffBound(t *testing.T) {
	want := []time.Duration{5, 5, 10, 20, 30, 30, 30}
	for i, w := range want {
		if got := Backoff(i); got != w*time.Second {
			t.Fatal(i, got)
		}
	}
}

func TestSessionLinkLossWaitsForCleanup(t *testing.T) {
	cleaned := false
	err := Session(context.Background(), func() bool { return false }, func(ctx context.Context) error {
		<-ctx.Done()
		cleaned = true
		return nil
	}, time.Millisecond, time.Second)
	if err != ErrDown || !cleaned {
		t.Fatal(err, cleaned)
	}
}
func TestSessionShutdownTimeoutRequiresReset(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	err := Session(context.Background(), func() bool { return false }, func(context.Context) error {
		<-release
		return nil
	}, time.Millisecond, 5*time.Millisecond)
	if err != ErrShutdown {
		t.Fatal(err)
	}
}
func TestSessionRunnerErrorAndParentCancellation(t *testing.T) {
	sentinel := errors.New("session failed")
	if err := Session(context.Background(), func() bool { return true }, func(context.Context) error { return sentinel }, time.Millisecond, time.Second); err != sentinel {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Session(ctx, func() bool { return true }, func(ctx context.Context) error { <-ctx.Done(); return nil }, time.Millisecond, time.Second); err != context.Canceled {
		t.Fatal(err)
	}
}

// Reproduce the old Esplink sequence without radio imports/hardware: Enable
// succeeds, association fails, retry calls Enable and never associates again.
func TestLegacyEnableEveryRetryReproducesFailure(t *testing.T) {
	d := &fakeDevice{failures: map[string]int{"associate": 1}}
	legacy := func() error {
		if err := d.Enable(); err != nil {
			return err
		}
		if err := d.Start(); err != nil {
			return err
		}
		return d.Associate("ssid", "password")
	}
	if legacy() == nil {
		t.Fatal("first attempt unexpectedly succeeded")
	}
	if err := legacy(); err == nil || err.Error() != "already enabled" {
		t.Fatal(err)
	}
	t.Log("before: association failure -> retry blocked by already enabled; fixed lifecycle regression above succeeds")
}
