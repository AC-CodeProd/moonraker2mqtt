package setup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"moonraker2mqtt/platform/esp32s3/store"
	"strings"
	"testing"
)

func repairPost(body string, confirmed bool) string {
	raw := strings.Replace(post(body), "POST /settings", "POST /repair", 1)
	if confirmed {
		raw = strings.Replace(raw, "Content-Length:", "X-Setup-Repair: erase-two-settings-slots\r\nContent-Length:", 1)
	}
	return raw
}
func TestRecoveryPortalConfirmationAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, raw, code             string
		recovery, unavailable, fail bool
	}{
		{"normal preserves evidence", post(string(payload())), "409", true, false, false},
		{"unconfirmed", repairPost(string(payload()), false), "409", true, false, false},
		{"wrong confirmation", strings.Replace(repairPost(string(payload()), true), "erase-two-settings-slots", "yes", 1), "409", true, false, false},
		{"healthy refuses repair", repairPost(string(payload()), true), "409", false, false, false},
		{"wrong token", strings.Replace(repairPost(string(payload()), true), "Token: token", "Token: wrong", 1), "403", true, false, false},
		{"wrong origin", strings.Replace(repairPost(string(payload()), true), "Origin: http://192.168.4.1", "Origin: http://evil", 1), "403", true, false, false},
		{"bad settings", repairPost("{}", true), "422", true, false, false},
		{"bounded", repairPost(strings.Repeat("x", MaxSettings+1), true), "413", true, false, false},
		{"confirmed", repairPost(string(payload()), true), "202", true, false, false},
		{"staging failure", repairPost(string(payload()), true), "503", true, false, true},
		{"inaccessible repair", repairPost(string(payload()), true), "503", true, true, false},
		{"inaccessible save", post(string(payload())), "503", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saves, repairs := 0, 0
			p := Portal{Token: "token", Page: Page, Recovery: tc.recovery, Unavailable: tc.unavailable,
				Save: func([]byte) error { saves++; return nil },
				Repair: func([]byte) error {
					repairs++
					if tc.fail {
						return errors.New("private staging error")
					}
					return nil
				}}
			response, accepted := exchange(t, p, tc.raw)
			if !strings.HasPrefix(response, "HTTP/1.1 "+tc.code) || accepted != (tc.code == "202") {
				t.Fatal(response, accepted)
			}
			expected := 0
			if tc.code == "202" || tc.fail {
				expected = 1
			}
			if saves != 0 || repairs != expected || strings.Contains(response, "private") {
				t.Fatal("unexpected staging/reflection", saves, repairs)
			}
			response, _ = exchange(t, p, "GET / HTTP/1.1\r\nHost: 192.168.4.1\r\n\r\n")
			if strings.Contains(response, "{{") || !strings.Contains(response, "name=\"repair\" type=\"checkbox\"") {
				t.Fatal("unrendered recovery page")
			}
		})
	}
}
func TestConfirmedRecoveryRebootLifecycle(t *testing.T) {
	for _, state := range []string{"corrupt", "unsupported", "ambiguous"} {
		t.Run(state, func(t *testing.T) {
			n := newNOR()
			s := store.Store{Flash: n}
			switch state {
			case "corrupt":
				n.data[0][0] = 0
			case "unsupported":
				if err := s.Save(payload()); err != nil {
					t.Fatal(err)
				}
				n.data[0][4] = 2
			case "ambiguous":
				if err := s.Save(payload()); err != nil {
					t.Fatal(err)
				}
				n.data[1] = n.data[0]
			}
			_, err := s.Load()
			if !store.RecoveryRequired(err) {
				t.Fatal(err)
			}
			baseline := n.data
			writes := n.writes
			if e := s.Save(payload()); e != err || n.data != baseline || n.writes != writes {
				t.Fatal("normal save erased evidence", e)
			}
			rtc := make([]byte, PendingSize)
			n.radio = true
			p := Portal{Token: "token", Recovery: true, Repair: func(b []byte) error { return StageOperation(rtc, b, RepairOperation) }}
			_, accepted := exchange(t, p, repairPost(string(payload()), false))
			if accepted || n.data != baseline {
				t.Fatal("unconfirmed repair")
			}
			_, accepted = exchange(t, p, repairPost(string(payload()), true))
			if !accepted || n.data != baseline || n.writes != writes {
				t.Fatal("radio-live flash operation")
			}
			n.radio = false
			pending, e := TakeOperation(rtc, true)
			if e != nil || pending == nil || pending.Operation != RepairOperation {
				t.Fatal(e)
			}
			if e = ApplyPending(&s, pending); e != nil {
				t.Fatal(e)
			}
			r, e := s.Load()
			if e != nil || !bytes.Equal(r.Payload, payload()) || r.Generation != 1 || r.Slot != 0 {
				t.Fatal("repair not durable", e)
			}
			for _, v := range n.data[1] {
				if v != 255 {
					t.Fatal("second slot not reset")
				}
			}
			if pending, e = TakeOperation(rtc, true); e != nil || pending != nil {
				t.Fatal("repair replay")
			}
			cold := make([]byte, PendingSize)
			if pending, e = TakeOperation(cold, false); e != nil || pending != nil {
				t.Fatal("cold pending")
			}
			r, e = s.Load()
			if e != nil || !bytes.Equal(r.Payload, payload()) {
				t.Fatal("cold durability", e)
			}
		})
	}
}

type failingRepairNOR struct {
	*nor
	attempts int
	err      error
}

func (n *failingRepairNOR) Erase(slot int) error {
	n.attempts++
	return n.err
}
func TestFailedRepairIsNotReplayed(t *testing.T) {
	n := &failingRepairNOR{nor: newNOR(), err: errors.New("erase failed")}
	n.data[0][0] = 0
	s := store.Store{Flash: n}
	rtc := make([]byte, PendingSize)
	if e := StageOperation(rtc, payload(), RepairOperation); e != nil {
		t.Fatal(e)
	}
	pending, e := TakeOperation(rtc, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = ApplyPending(&s, pending); e != n.err || n.attempts != 1 {
		t.Fatal("repair error lost", e)
	}
	pending, e = TakeOperation(rtc, true)
	if e != nil || pending != nil {
		t.Fatal("failed repair replayed", e)
	}
	if e = ApplyPending(&s, pending); e != nil || n.attempts != 1 {
		t.Fatal("automatic retry", e)
	}
}

func TestPendingOperationValidation(t *testing.T) {
	for _, op := range []Operation{SaveOperation, RepairOperation} {
		for _, soft := range []bool{false, true} {
			b := make([]byte, PendingSize)
			if e := StageOperation(b, payload(), op); e != nil {
				t.Fatal(e)
			}
			p, e := TakeOperation(b, soft)
			if e != nil || (p != nil) != soft {
				t.Fatal("reset selection", e)
			}
			if p != nil && p.Operation != op {
				t.Fatal("operation lost")
			}
		}
	}
	for _, change := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b[12:], 0) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[12:], 3) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[12:], uint32(SaveOperation)) }, // valid op, wrong CRC
		func(b []byte) { b[8] ^= 1 }, func(b []byte) { b[16] ^= 1 }, func(b []byte) { binary.LittleEndian.PutUint32(b[4:], MaxSettings+1) },
	} {
		b := make([]byte, PendingSize)
		StageOperation(b, payload(), RepairOperation)
		change(b)
		if p, e := TakeOperation(b, true); e == nil || p != nil {
			t.Fatal("invalid pending accepted")
		}
		if p, e := TakeOperation(b, true); e != nil || p != nil {
			t.Fatal("invalid handoff replayed")
		}
	}
	b := make([]byte, PendingSize)
	if StageOperation(b, payload(), Operation(99)) == nil {
		t.Fatal("unknown staged")
	}
	StageOperation(b, payload(), RepairOperation)
	if p, e := Take(b, true); e == nil || p != nil {
		t.Fatal("repair downgraded to save")
	}
	n := newNOR()
	n.radio = true // any backend access panics, including reads
	s := store.Store{Flash: n}
	for _, p := range []*Pending{{Operation: 99, Payload: payload()}, {Operation: RepairOperation, Payload: []byte("{}")}, {Operation: SaveOperation, Payload: []byte("{}")}} {
		if ApplyPending(&s, p) == nil {
			t.Fatal("invalid boot operation accepted")
		}
	}
}
