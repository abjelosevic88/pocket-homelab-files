package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP accepts one plain SMTP session at a time and keeps each message.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []string
	rcpt [][]string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	say("220 fake")
	var rcpt []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL"):
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			rcpt = append(rcpt, strings.Trim(strings.TrimSpace(line[strings.Index(line, ":")+1:]), "<>"))
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.msgs, f.rcpt = append(f.msgs, b.String()), append(f.rcpt, rcpt)
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func TestEmailLinks(t *testing.T) {
	s, h := testServer(t)
	smtp := newFakeSMTP(t)
	alice := newPerson(t, s, h, "alice@example.com", "admin")
	upload(t, alice, "home", "/", "contract.pdf", "pdf")
	w, sd := alice.do("POST", "/api/v1/sends", map[string]any{"kind": "live", "label": "Contract", "live": map[string]any{"space": "home", "paths": []string{"/contract.pdf"}}, "expires": "1d", "password": "pw123456"})
	if w.Code != 201 {
		t.Fatalf("send: %d", w.Code)
	}
	id := sd["id"].(string)
	if w, _ := alice.do("POST", "/api/v1/sends/"+id+"/email", map[string]any{"to": []string{"bob@example.com"}}); w.Code != 400 || !strings.Contains(w.Body.String(), "Settings → Email") {
		t.Fatalf("unconfigured email: %d %s", w.Code, w.Body.String())
	}
	port := smtp.ln.Addr().(*net.TCPAddr).Port
	if w, _ := alice.do("PUT", "/api/v1/settings", map[string]any{"smtp_host": "127.0.0.1", "smtp_port": port, "smtp_tls": "none", "smtp_from": "alice@example.com", "smtp_from_name": "Alice", "smtp_password": "secret"}); w.Code != 200 {
		t.Fatalf("settings: %d", w.Code)
	}
	if _, st := alice.do("GET", "/api/v1/settings", nil); st["smtp_password"] != "" || st["smtp_password_set"] != true {
		t.Fatalf("password echoed: %v", st)
	}
	w, out := alice.do("POST", "/api/v1/sends/"+id+"/email", map[string]any{"to": []string{"Bob <bob@example.com>, carol@example.com"}, "message": "Sign page 3."})
	if w.Code != 200 || out["sent"].(float64) != 2 {
		t.Fatalf("email: %d %s", w.Code, w.Body.String())
	}
	msg := smtp.msgs[0]
	for _, want := range []string{"Subject: =?utf-8?q?Alice_sent_you_", "Alice shared “Contract” with you.", "Sign page 3.", ">Open the files</a>", "https://send.example/s/" + id, "password"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "pw123456") {
		t.Fatal("the password went out in the email")
	}
	if strings.Join(smtp.rcpt[0], ",") != "bob@example.com,carol@example.com" {
		t.Fatalf("recipients: %v", smtp.rcpt[0])
	}

	// encrypted: only with the whole link (key) from the browser
	sum := sha256.Sum256([]byte("auth"))
	_, enc := alice.do("POST", "/api/v1/sends", map[string]any{"kind": "files", "encrypted": true, "meta": "eA", "auth_hash": hex.EncodeToString(sum[:]), "files": []map[string]any{{"expected": 1}}, "expires": "1h"})
	eid := enc["id"].(string)
	alice.do("PUT", "/api/v1/sends/"+eid+"/files/0?offset=0", "x")
	alice.do("POST", "/api/v1/sends/"+eid+"/finish", nil)
	if w, _ := alice.do("POST", "/api/v1/sends/"+eid+"/email", map[string]any{"to": []string{"bob@example.com"}}); w.Code != 400 {
		t.Fatalf("encrypted without key: %d", w.Code)
	}
	if w, _ := alice.do("POST", "/api/v1/sends/"+eid+"/email", map[string]any{"to": []string{"bob@example.com"}, "link": "https://evil.example/s/" + eid + "#k"}); w.Code != 400 {
		t.Fatalf("foreign link accepted: %d", w.Code)
	}
	full := "https://send.example/s/" + eid + "#thekey"
	if w, _ := alice.do("POST", "/api/v1/sends/"+eid+"/email", map[string]any{"to": []string{"bob@example.com"}, "link": full}); w.Code != 200 || !strings.Contains(smtp.msgs[1], full) {
		t.Fatalf("encrypted email: %d", w.Code)
	}

	// upload link
	alice.do("POST", "/api/v1/fs/mkdir", map[string]any{"space": "home", "path": "/In"})
	_, rq := alice.do("POST", "/api/v1/requests", map[string]any{"title": "Photos", "space": "home", "path": "/In"})
	if w, _ := alice.do("POST", "/api/v1/requests/"+rq["id"].(string)+"/email", map[string]any{"to": []string{"dan@example.com"}}); w.Code != 200 || !strings.Contains(smtp.msgs[2], "/r/"+rq["id"].(string)) {
		t.Fatalf("request email: %d", w.Code)
	}
	// someone else's send
	bob := newPerson(t, s, h, "bob@example.com", "member")
	if w, _ := bob.do("POST", "/api/v1/sends/"+id+"/email", map[string]any{"to": []string{"x@example.com"}}); w.Code != 404 {
		t.Fatalf("bob emailed alice's link: %d", w.Code)
	}
	// a custom template; the empty {message} line disappears
	alice.do("PUT", "/api/v1/settings", map[string]any{"mail_subject": "Files from {sender}", "mail_body": "Hello!\n\n{message}\n\nGet them: {link}\n{password_note}\nCheers"})
	if w, _ := alice.do("POST", "/api/v1/sends/"+id+"/email", map[string]any{"to": []string{"bob@example.com"}}); w.Code != 200 {
		t.Fatalf("templated email: %d", w.Code)
	}
	last := smtp.msgs[len(smtp.msgs)-1]
	if !strings.Contains(last, "Subject: Files from Alice") || !strings.Contains(last, "Hello!\r\n\r\nGet them: https://send.example/s/"+id+"\r\nIt needs a password") && !strings.Contains(last, "Hello!\n\nGet them: https://send.example/s/"+id+"\nIt needs a password") {
		t.Fatalf("template not applied:\n%s", last)
	}
	_ = strconv.Itoa
}
