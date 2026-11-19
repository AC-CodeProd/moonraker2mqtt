package setup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// The public bool API stays unchanged; inspect the same error-returning pipeline
// it uses without adding an external diagnostics or credential-logging API.
type portalResponseHandler interface{ handle(net.Conn) (bool, error) }

func portalResponsePipeline(t *testing.T, p *Portal) portalResponseHandler {
	t.Helper()
	h, ok := any(p).(portalResponseHandler)
	if !ok {
		t.Fatal("portal lacks an error-returning response pipeline")
	}
	return h
}

type portalResponseFaultConn struct {
	*memConn
	remaining, closes  int
	writeErr, closeErr error
}

func (c *portalResponseFaultConn) Write(p []byte) (int, error) {
	n := min(len(p), c.remaining)
	if _, err := c.output.Write(p[:n]); err != nil {
		return 0, err
	}
	c.remaining -= n
	if n != len(p) {
		return n, c.writeErr
	}
	return n, nil
}
func (c *portalResponseFaultConn) Close() error {
	c.closes++
	if err := c.memConn.Close(); err != nil {
		return err
	}
	return c.closeErr
}

type portalResponseCase struct {
	name, raw, body                          string
	code                                     int
	recovery, unavailable, stageFail, noSave bool
	stages                                   int
}

func portalResponseCases() []portalResponseCase {
	valid := post(string(payload()))
	return []portalResponseCase{
		{name: "request", raw: "GET /\n", code: 400, body: "Requête invalide"},
		{name: "page", raw: "GET / HTTP/1.1\r\nHost: 192.168.4.1\r\n\r\n", code: 200, body: "token|false|false"},
		{name: "path", raw: "GET /unknown HTTP/1.1\r\nHost: 192.168.4.1\r\n\r\n", code: 404, body: "Introuvable"},
		{name: "authorization", raw: strings.Replace(valid, "Token: token", "Token: wrong", 1), code: 403, body: "Accès refusé"},
		{name: "size", raw: post(strings.Repeat("x", MaxSettings+1)), code: 413, body: "Corps trop grand"},
		{name: "incomplete", raw: valid[:len(valid)-1], code: 400, body: "Corps incomplet"},
		{name: "settings", raw: post("{}"), code: 422, body: "Paramètres invalides"},
		{name: "unavailable", raw: valid, unavailable: true, code: 503, body: "Stockage inaccessible ; diagnostic nécessaire, aucun effacement autorisé."},
		{name: "confirmation", raw: repairPost(string(payload()), false), recovery: true, code: 409, body: "Confirmation explicite de l’effacement des deux secteurs requise."},
		{name: "recovery", raw: valid, recovery: true, code: 409, body: "Stockage corrompu, version non prise en charge ou générations ambiguës : utilisez la réparation confirmée, ou préservez les secteurs pour diagnostic."},
		{name: "stage failure", raw: valid, stageFail: true, stages: 1, code: 503, body: "Sauvegarde indisponible"},
		{name: "missing callback", raw: valid, noSave: true, code: 503, body: "Sauvegarde indisponible"},
		{name: "save", raw: valid, stages: 1, code: 202, body: "Paramètres préparés. Redémarrage pour enregistrer ; vérifiez ensuite le port série."},
		{name: "repair", raw: repairPost(string(payload()), true), recovery: true, stages: 1, code: 202, body: "Paramètres préparés. Redémarrage pour enregistrer ; vérifiez ensuite le port série."},
	}
}

func portalResponseFixture(t *testing.T, tc portalResponseCase, budget int, writeErr error) (*Portal, *portalResponseFaultConn, []byte, *int, *int) {
	t.Helper()
	rtc := make([]byte, PendingSize)
	stages, cancels := 0, 0
	op := SaveOperation
	if tc.name == "repair" {
		op = RepairOperation
	}
	stage := func(b []byte) error {
		stages++
		if err := StageOperation(rtc, b, op); err != nil {
			t.Fatal(err)
		}
		if tc.stageFail {
			return errors.New("private staging failure")
		}
		return nil
	}
	p := &Portal{Token: "token", Page: "{{TOKEN}}|{{RECOVERY}}|{{UNAVAILABLE}}", Recovery: tc.recovery, Unavailable: tc.unavailable, Save: stage, Repair: stage, Cancel: func() { cancels++; binary.LittleEndian.PutUint32(rtc, 0) }}
	if tc.noSave {
		p.Save = nil
	}
	c := &portalResponseFaultConn{memConn: &memConn{reader: strings.NewReader(tc.raw)}, remaining: budget, writeErr: writeErr}
	return p, c, rtc, &stages, &cancels
}

func TestPortalResponsePipelineSendsAndErrors(t *testing.T) {
	want := errors.New("response transport failure")
	for _, tc := range portalResponseCases() {
		t.Run(tc.name, func(t *testing.T) {
			var wire bytes.Buffer
			kind := "text/plain"
			if tc.code == 200 {
				kind = "text/html; charset=utf-8"
			}
			if tc.code == 202 {
				kind = "text/plain; charset=utf-8"
			}
			if err := respond(&wire, tc.code, kind, tc.body); err != nil {
				t.Fatal(err)
			}
			expected := wire.String()
			for _, public := range []bool{false, true} {
				for _, short := range []bool{false, true} {
					for _, budget := range []int{0, len(expected) / 2, len(expected) - 1, len(expected)} {
						injected, expectedErr := want, want
						if short {
							injected, expectedErr = nil, io.ErrShortWrite
						}
						p, c, rtc, stages, cancels := portalResponseFixture(t, tc, budget, injected)
						var accepted bool
						if public {
							accepted = p.Handle(c)
						} else {
							var err error
							accepted, err = portalResponsePipeline(t, p).handle(c)
							if budget == len(expected) {
								if err != nil {
									t.Fatal(err)
								}
							} else if !errors.Is(err, expectedErr) {
								t.Fatalf("lost response error: %v", err)
							}
						}
						wantAccepted := tc.code == 202 && budget == len(expected)
						wantCancels := 0
						if tc.stages != 0 && !wantAccepted {
							wantCancels = 1
						}
						if accepted != wantAccepted || *stages != tc.stages || *cancels != wantCancels || c.closes != 1 || c.calls != 1 || !c.closed || c.output.String() != expected[:budget] {
							t.Fatalf("public=%t short=%t budget=%d: accepted=%t stages=%d cancels=%d closes=%d deadlines=%d prefix=%t", public, short, budget, accepted, *stages, *cancels, c.closes, c.calls, c.output.String() == expected[:budget])
						}
						pending, err := TakeOperation(rtc, true)
						if err != nil || (pending != nil) != wantAccepted {
							t.Fatalf("pending replay: %v", err)
						}
						if strings.Contains(c.output.String(), "SECRET") || strings.Contains(c.output.String(), "private staging") {
							t.Fatal("private input reflected")
						}
					}
				}
			}
		})
	}
}

func TestPortalResponsePipelinePreservesCleanupErrors(t *testing.T) {
	writeErr, closeErr := errors.New("response failed"), errors.New("close failed")
	for _, unsupported := range []bool{false, true} {
		tc := portalResponseCase{name: "save", raw: post(string(payload())), stages: 1, code: 202}
		p, c, _, stages, cancels := portalResponseFixture(t, tc, 0, writeErr)
		c.unsupported, c.closeErr = unsupported, closeErr
		accepted, err := portalResponsePipeline(t, p).handle(c)
		want := writeErr
		wantStages := 1
		if unsupported {
			want, wantStages = io.ErrClosedPipe, 0
		}
		if accepted || !errors.Is(err, want) || !errors.Is(err, closeErr) || c.closes != 1 || *stages != wantStages || *cancels != wantStages {
			t.Fatalf("lost primary/cleanup error: %v", err)
		}
	}
}
