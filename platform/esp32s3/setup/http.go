package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const MaxHeaders = 2048

// One request per connection, one connection handled at a time. All errors are
// fixed public messages: never reflect credentials or decoder input.
type Portal struct {
	Token       string
	Save        func([]byte) error // stage only, never flash; called after validation
	Page        string
	Recovery    bool               // normal saves blocked; requires separate confirmed repair
	Unavailable bool               // backend I/O errors: no staging or destructive recovery
	Repair      func([]byte) error // stage explicit repair only, never flash
	Cancel      func()             // required if callbacks publish RTC; failure invalidates it, retry must resubmit
}

func (p *Portal) Handle(c net.Conn) (accepted bool) {
	deadline := time.Now().Add(8 * time.Second)
	capacity := responseCapacity(c)
	staged := false
	defer func() {
		// Even GET/error responses use the buffered stack. Keep the stack pump
		// alive until peer ACK or a hard bound; Close only initiates TCP close.
		drainDeadline := time.Now().Add(2 * time.Second)
		if deadline.Before(drainDeadline) {
			drainDeadline = deadline
		}
		if awaitResponse(c, capacity, drainDeadline) != nil {
			accepted = false
		}
		if c.Close() != nil {
			accepted = false
		}
		if staged && !accepted && p.Cancel != nil {
			p.Cancel()
		}
	}()
	if c.SetDeadline(deadline) != nil {
		return false
	} // fail closed if deadlines unsupported
	r := bufio.NewReaderSize(c, 512)
	method, path, h, err := request(r)
	if err != nil {
		respond(c, 400, "text/plain", "Requête invalide")
		return
	}
	if method == "GET" && path == "/" {
		page := strings.ReplaceAll(p.Page, "{{TOKEN}}", p.Token)
		page = strings.ReplaceAll(page, "{{RECOVERY}}", strconv.FormatBool(p.Recovery))
		page = strings.ReplaceAll(page, "{{UNAVAILABLE}}", strconv.FormatBool(p.Unavailable))
		respond(c, 200, "text/html; charset=utf-8", page)
		return
	}
	if method != "POST" || (path != "/settings" && path != "/repair") {
		respond(c, 404, "text/plain", "Introuvable")
		return
	}
	if h["content-type"] != "application/json" || h["x-setup-token"] != p.Token || p.Token == "" || h["origin"] != "http://192.168.4.1" {
		respond(c, 403, "text/plain", "Accès refusé")
		return
	}
	n, err := strconv.Atoi(h["content-length"])
	if err != nil || n < 1 || n > MaxSettings {
		respond(c, 413, "text/plain", "Corps trop grand")
		return
	}
	body := make([]byte, n)
	if _, err = io.ReadFull(r, body); err != nil {
		respond(c, 400, "text/plain", "Corps incomplet")
		return
	}
	if _, err = Decode(body); err != nil {
		respond(c, 422, "text/plain", "Paramètres invalides")
		return
	}
	if p.Unavailable {
		respond(c, 503, "text/plain", "Stockage inaccessible ; diagnostic nécessaire, aucun effacement autorisé.")
		return
	}
	save := p.Save
	if path == "/repair" {
		if !p.Recovery || h["x-setup-repair"] != "erase-two-settings-slots" {
			respond(c, 409, "text/plain", "Confirmation explicite de l’effacement des deux secteurs requise.")
			return
		}
		save = p.Repair
	} else if p.Recovery {
		respond(c, 409, "text/plain", "Stockage corrompu, version non prise en charge ou générations ambiguës : utilisez la réparation confirmée, ou préservez les secteurs pour diagnostic.")
		return
	}
	staged = save != nil // cancellation also covers a partially failed stage
	if save == nil || save(body) != nil {
		respond(c, 503, "text/plain", "Sauvegarde indisponible")
		return
	}
	// 202 is NOT a durable-save claim. A failed write/ACK/close cancels the
	// pending operation (including repair); no automatic reset or later replay.
	if respond(c, 202, "text/plain; charset=utf-8", "Paramètres préparés. Redémarrage pour enregistrer ; vérifiez ensuite le port série.") != nil {
		return false
	}
	return true
}
func request(r *bufio.Reader) (string, string, map[string]string, error) {
	total := 0
	line := func() (string, error) {
		var b []byte
		for {
			x, e := r.ReadByte()
			if e != nil {
				return "", e
			}
			total++
			if total > MaxHeaders {
				return "", errors.New("headers limit")
			}
			b = append(b, x)
			if x == '\n' {
				if len(b) < 2 || b[len(b)-2] != '\r' {
					return "", errors.New("line ending")
				}
				return string(b[:len(b)-2]), nil
			}
		}
	}
	l, e := line()
	if e != nil {
		return "", "", nil, e
	}
	fields := strings.Split(l, " ")
	if len(fields) != 3 || fields[2] != "HTTP/1.1" {
		return "", "", nil, errors.New("request line")
	}
	h := make(map[string]string)
	for {
		l, e = line()
		if e != nil {
			return "", "", nil, e
		}
		if l == "" {
			break
		}
		i := strings.IndexByte(l, ':')
		if i <= 0 {
			return "", "", nil, errors.New("header")
		}
		key := strings.ToLower(l[:i])
		if strings.ContainsAny(key, " \t") {
			return "", "", nil, errors.New("header name")
		}
		if _, exists := h[key]; exists {
			return "", "", nil, errors.New("duplicate header")
		}
		h[key] = strings.TrimSpace(l[i+1:])
	}
	if h["host"] != "192.168.4.1" && h["host"] != "192.168.4.1:80" {
		return "", "", nil, errors.New("host")
	}
	_, te := h["transfer-encoding"]
	_, expect := h["expect"]
	if te || expect {
		return "", "", nil, errors.New("unsupported framing")
	}
	return fields[0], fields[1], h, nil
}
func respond(w io.Writer, code int, kind, body string) error {
	response := fmt.Sprintf("HTTP/1.1 %d Response\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'\r\n\r\n%s", code, kind, len(body), body)
	n, err := io.WriteString(w, response)
	if err == nil && n != len(response) {
		return io.ErrShortWrite
	}
	return err
}
