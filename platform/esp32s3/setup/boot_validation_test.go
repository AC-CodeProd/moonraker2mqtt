package setup

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"moonraker2mqtt/platform/esp32s3/store"
)

func bootInvalidSettings() []struct {
	name   string
	change func(*Settings)
} {
	return []struct {
		name   string
		change func(*Settings)
	}{
		{"prefix-leading-slash", func(s *Settings) { s.TopicPrefix = "/moonraker" }},
		{"prefix-trailing-slash", func(s *Settings) { s.TopicPrefix = "moonraker/" }},
		{"prefix-slash-only", func(s *Settings) { s.TopicPrefix = "/" }},
		{"prefix-ascii-space", func(s *Settings) { s.TopicPrefix = "   " }},
		{"prefix-unicode-space", func(s *Settings) { s.TopicPrefix = "\u00a0\u2003" }},
		{"client-ascii-space", func(s *Settings) { s.ClientID = "   " }},
		{"client-unicode-space", func(s *Settings) { s.ClientID = "\u00a0\u2003" }},
		{"prefix-private-marker", func(s *Settings) { s.TopicPrefix = "private-marker/" }},
	}
}

func bootSettingsJSON(t *testing.T, s Settings) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type bootValidationNOR struct {
	*nor
	reads int
}

func (n *bootValidationNOR) Read(slot, offset int, b []byte) error {
	n.reads++
	return n.nor.Read(slot, offset, b)
}

func TestSettingsBootValidationRejectsBeforeStaging(t *testing.T) {
	for _, tc := range bootInvalidSettings() {
		t.Run(tc.name, func(t *testing.T) {
			s := good()
			tc.change(&s)
			if s.Config().Validate() == nil {
				t.Fatal("negative fixture is boot-valid")
			}
			if err := s.Validate(); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("boot-invalid setting accepted or reflected", err)
			}
			body := bootSettingsJSON(t, s)
			if _, err := Decode(body); err == nil {
				t.Fatal("Decode admitted boot-invalid settings")
			}
			for _, op := range []Operation{SaveOperation, RepairOperation} {
				rtc := make([]byte, PendingSize)
				if err := Stage(rtc, payload()); err != nil {
					t.Fatal(err)
				}
				before := append([]byte(nil), rtc...)
				if err := StageOperation(rtc, body, op); err == nil || !bytes.Equal(rtc, before) {
					t.Fatal("invalid staging changed retained bytes", op, err)
				}
				n := &bootValidationNOR{nor: newNOR()}
				st := store.Store{Flash: n}
				if err := st.Save(payload()); err != nil {
					t.Fatal(err)
				}
				data, writes, reads := n.data, n.writes, n.reads
				if err := ApplyPending(&st, &Pending{Operation: op, Payload: body}); err == nil || n.data != data || n.writes != writes || n.reads != reads {
					t.Fatal("invalid boot handoff reached backend", op, err)
				}
			}
		})
	}
}

func TestPortalBootValidationRejectsWithoutMutation(t *testing.T) {
	for _, tc := range bootInvalidSettings() {
		t.Run(tc.name, func(t *testing.T) {
			for _, repair := range []bool{false, true} {
				s := good()
				tc.change(&s)
				rtc := make([]byte, PendingSize)
				if err := Stage(rtc, payload()); err != nil {
					t.Fatal(err)
				}
				beforeRTC := append([]byte(nil), rtc...)
				n := &bootValidationNOR{nor: newNOR()}
				st := store.Store{Flash: n}
				if err := st.Save(payload()); err != nil {
					t.Fatal(err)
				}
				before, err := st.Load()
				if err != nil {
					t.Fatal(err)
				}
				data, writes, reads := n.data, n.writes, n.reads
				saves, repairs, cancels := 0, 0, 0
				p := Portal{Token: "token", Recovery: repair,
					Save:   func(b []byte) error { saves++; return StageOperation(rtc, b, SaveOperation) },
					Repair: func(b []byte) error { repairs++; return StageOperation(rtc, b, RepairOperation) },
					Cancel: func() { cancels++ },
				}
				body := string(bootSettingsJSON(t, s))
				raw := post(body)
				if repair {
					raw = repairPost(body, true)
				}
				response, accepted := exchange(t, p, raw)
				if accepted || !strings.HasPrefix(response, "HTTP/1.1 422 ") || saves != 0 || repairs != 0 || cancels != 0 {
					t.Fatal("invalid POST reached staging or reset acceptance", repair, saves, repairs, cancels, response)
				}
				if strings.Contains(response, "private-marker") || strings.Contains(response, s.APIKey) || strings.Contains(response, s.MQTTPassword) {
					t.Fatal("invalid response reflected private data")
				}
				if !bytes.Equal(rtc, beforeRTC) || n.data != data || n.writes != writes || n.reads != reads {
					t.Fatal("invalid POST mutated RTC or durable state")
				}
				after, err := st.Load()
				if err != nil || after.Generation != before.Generation || after.Slot != before.Slot || !bytes.Equal(after.Payload, before.Payload) {
					t.Fatal("previous durable settings changed", err)
				}
			}
		})
	}
}

func TestPortalAcceptedSettingsSurviveBootValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Settings)
	}{
		{"default", func(*Settings) {}},
		{"nested-prefix", func(s *Settings) { s.TopicPrefix = "moonraker/printer"; s.PollSeconds = 1 }},
		{"nonempty-padded-fields", func(s *Settings) { s.ClientID = " client "; s.TopicPrefix = " moonraker " }},
		{"maximum-lengths", func(s *Settings) {
			s.ClientID = strings.Repeat("c", 64)
			s.TopicPrefix = strings.Repeat("p", 128)
			s.PollSeconds = 3600
		}},
		{"nonempty-unicode", func(s *Settings) { s.ClientID = "imprimante-é"; s.TopicPrefix = "maison/étage" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, repair := range []bool{false, true} {
				s := good()
				tc.change(&s)
				body := bootSettingsJSON(t, s)
				n := newNOR()
				st := store.Store{Flash: n}
				if err := st.Save(payload()); err != nil {
					t.Fatal(err)
				}
				if repair {
					n.data[0][0] = 0
				}
				baseline, writes := n.data, n.writes
				rtc := make([]byte, PendingSize)
				p := Portal{Token: "token", Recovery: repair,
					Save:   func(b []byte) error { return StageOperation(rtc, b, SaveOperation) },
					Repair: func(b []byte) error { return StageOperation(rtc, b, RepairOperation) },
				}
				raw := post(string(body))
				if repair {
					raw = repairPost(string(body), true)
				}
				n.radio = true
				response, accepted := exchange(t, p, raw)
				if !accepted || !strings.HasPrefix(response, "HTTP/1.1 202 ") || n.data != baseline || n.writes != writes {
					t.Fatal("valid POST not staged without flash", repair, response)
				}
				n.radio = false
				pending, err := TakeOperation(rtc, true)
				if err != nil || pending == nil {
					t.Fatal("simulated software-reset handoff failed", err)
				}
				wantOp := SaveOperation
				if repair {
					wantOp = RepairOperation
				}
				if pending.Operation != wantOp {
					t.Fatal("pending operation changed")
				}
				if err = ApplyPending(&st, pending); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					// Fresh Store models reloading durable bytes, not a physical cold boot.
					reloaded := store.Store{Flash: n}
					record, loadErr := reloaded.Load()
					if loadErr != nil || !bytes.Equal(record.Payload, body) {
						t.Fatal("durable settings differ", loadErr)
					}
					loaded, decodeErr := Decode(record.Payload)
					if decodeErr != nil || loaded != s {
						t.Fatal("settings normalized or rejected", decodeErr)
					}
					if err = loaded.Config().Validate(); err != nil {
						t.Fatal("saved configuration rejected at boot", err)
					}
				}
				if replay, err := TakeOperation(rtc, true); err != nil || replay != nil {
					t.Fatal("pending operation replayed", err)
				}
			}
		})
	}
}
