// Package mailer sends email over SMTP (links to sends and upload links).
package mailer

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config is SMTP configuration.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	FromName string
	TLS      string // none | starttls | tls
	BCC      string
}

// Attachment is a file to attach.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is an outgoing email.
type Message struct {
	To          []string
	Subject     string
	Body        string
	Attachments []Attachment
}

// Configured reports whether SMTP is usable.
func (c Config) Configured() bool { return c.Host != "" && c.From != "" }

// Send delivers the message.
func Send(cfg Config, m Message) error {
	if !cfg.Configured() {
		return fmt.Errorf("SMTP is not configured (Settings → Email)")
	}
	if len(m.To) == 0 {
		return fmt.Errorf("no recipient address")
	}
	from := cfg.From
	if cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", cfg.FromName), cfg.From)
	}
	rcpts := append([]string{}, m.To...)
	if cfg.BCC != "" {
		for _, b := range strings.Split(cfg.BCC, ",") {
			if b = strings.TrimSpace(b); b != "" {
				rcpts = append(rcpts, b)
			}
		}
	}
	data := build(from, m)

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var client *smtp.Client
	var err error
	switch cfg.TLS {
	case "tls", "ssl":
		conn, derr := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", addr, &tls.Config{ServerName: cfg.Host})
		if derr != nil {
			return derr
		}
		client, err = smtp.NewClient(conn, cfg.Host)
	default:
		conn, derr := net.DialTimeout("tcp", addr, 20*time.Second)
		if derr != nil {
			return derr
		}
		client, err = smtp.NewClient(conn, cfg.Host)
		if err == nil && cfg.TLS == "starttls" {
			if ok, _ := client.Extension("STARTTLS"); ok {
				err = client.StartTLS(&tls.Config{ServerName: cfg.Host})
			}
		}
	}
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.User != "" {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("rcpt %s: %w", r, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func build(from string, m Message) []byte {
	var b bytes.Buffer
	boundary := fmt.Sprintf("pf-%d", time.Now().UnixNano())
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(m.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\n")
	if len(m.Attachments) == 0 {
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", m.Body)
		return b.Bytes()
	}
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, m.Body)
	for _, a := range m.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; name=%q\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=%q\r\n\r\n", boundary, ct, a.Filename, a.Filename)
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "\r\n")
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}
