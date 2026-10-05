package store

import "testing"

// Record every operation and inject a failure at an exact boundary. Out-of-scope
// slots/offsets panic, including during explicit repair.
type repairBackend struct {
	*memory
	calls  int
	failAt int
	slots  []int
}

func (b *repairBackend) step(slot int) error {
	if slot < 0 || slot > 1 {
		panic("out-of-scope slot")
	}
	b.calls++
	b.slots = append(b.slots, slot)
	if b.calls == b.failAt {
		return errCut
	}
	return nil
}
func (b *repairBackend) Read(s, o int, p []byte) error {
	if e := b.step(s); e != nil {
		return e
	}
	return b.memory.Read(s, o, p)
}
func (b *repairBackend) Erase(s int) error {
	if e := b.step(s); e != nil {
		return e
	}
	return b.memory.Erase(s)
}
func (b *repairBackend) Program(s, o int, p []byte) error {
	if e := b.step(s); e != nil {
		return e
	}
	return b.memory.Program(s, o, p)
}
func TestRepairPreconditionsNoBackendOperations(t *testing.T) {
	b := &repairBackend{memory: blank()}
	s := Store{Flash: b}
	for _, tc := range []struct {
		p         []byte
		confirmed bool
		want      error
	}{
		{[]byte("new"), false, ErrConfirmation},
		{make([]byte, MaxPayload+1), true, ErrSize},
	} {
		if e := s.Repair(tc.p, tc.confirmed); e != tc.want || b.calls != 0 {
			t.Fatal("precondition touched backend", e, b.calls)
		}
	}
	for _, healthy := range []bool{false, true} {
		b = &repairBackend{memory: blank()}
		s.Flash = b
		if healthy {
			mustSave(t, &s, []byte("keep"))
		}
		before := b.data
		changed := b.changed
		if e := s.Repair([]byte("new"), true); e != ErrRepairNotRequired || b.data != before || b.changed != changed {
			t.Fatal("unnecessary reset", e)
		}
	}
}
func TestRepairBackendErrorsAndScope(t *testing.T) {
	base := blank()
	base.data[0][0] = 0
	base.data[1][0] = 0
	full := &repairBackend{memory: blank()}
	full.data = base.data
	if e := (&Store{Flash: full}).Repair([]byte("replacement"), true); e != nil {
		t.Fatal(e)
	}
	for fail := 1; fail <= full.calls; fail++ {
		m := *base
		b := &repairBackend{memory: &m, failAt: fail}
		if e := (&Store{Flash: b}).Repair([]byte("replacement"), true); e != errCut || b.calls != fail {
			t.Fatalf("failure %d not propagated/stopped: %v calls %d", fail, e, b.calls)
		}
		if fail <= 2 && b.changed != 0 {
			t.Fatal("scan failure erased evidence")
		}
	}
	m := *base
	m.silent = true
	if e := (&Store{Flash: &m}).Repair([]byte("new"), true); e != ErrVerify {
		t.Fatal("silent erase failure ignored", e)
	}
	// Interrupted destructive repair does not promise old data survives. A later
	// ordinary Load must never accept a partial replacement as a valid record.
	for _, cut := range []int{0, 1, 4095, 4096, 4097, 8191, 8192, 12288, 12300} {
		m := *base
		m.budget = cut
		s := Store{Flash: &m}
		e := s.Repair([]byte("replacement"), true)
		if e == nil {
			t.Fatalf("cut %d unexpectedly succeeded", cut)
		}
		m.budget = -1
		if r, re := s.Load(); re == nil && string(r.Payload) != "replacement" {
			t.Fatal("torn repair accepted")
		}
	}
}
