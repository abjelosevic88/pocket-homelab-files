package server

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
)

// Email templates: each person can word the email for links and for upload
// links (Settings → Email). {placeholders} are filled in; a line whose
// placeholders all came out empty is left out, so an email without a message
// or a password has no gaps.

const (
	defaultMailSubject = "{sender} sent you {title}"
	defaultMailBody    = `Hi,

{sender} shared {title} with you.

{message}

{link}

The link works {expires}. {limit}
{password_note}
{encrypted_note}

— sent with Pocket Files`
	defaultRequestSubject = "{sender} asks you for files: {title}"
	defaultRequestBody    = `Hi,

{sender} asks you for files: {title}.

{message}

Drop them here — you will not see what others sent:
{link}

The link works {expires}.
{password_note}

— sent with Pocket Files`
)

var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// fill replaces the placeholders and drops lines that only held empty ones.
func fill(tpl string, vars map[string]string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(tpl, "\r\n", "\n"), "\n") {
		had := placeholder.MatchString(line)
		got := placeholder.ReplaceAllStringFunc(line, func(k string) string {
			if v, ok := vars[strings.Trim(k, "{}")]; ok {
				return v
			}
			return k
		})
		if had && strings.TrimSpace(got) == "" {
			continue
		}
		out = append(out, strings.TrimRight(got, " "))
	}
	text := strings.Join(out, "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text) + "\n"
}

// mailHTML turns the filled text into a simple HTML email: paragraphs, and
// the link as a button.
func mailHTML(text, link, button string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#f4f6f8;font:15px/1.55 -apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#18181b">`)
	b.WriteString(`<div style="max-width:560px;margin:0 auto;background:#fff;border:1px solid #e6e6ea;border-radius:14px;padding:28px">`)
	for _, para := range strings.Split(strings.TrimSpace(text), "\n\n") {
		if strings.TrimSpace(para) == link {
			fmt.Fprintf(&b, `<p style="margin:22px 0;text-align:center"><a href="%s" style="display:inline-block;background:#0891b2;color:#fff;text-decoration:none;font-weight:600;padding:12px 22px;border-radius:10px">%s</a><br><a href="%s" style="font-size:12px;color:#71717a;word-break:break-all">%s</a></p>`,
				html.EscapeString(link), html.EscapeString(button), html.EscapeString(link), html.EscapeString(link))
			continue
		}
		esc := html.EscapeString(para)
		if link != "" {
			esc = strings.ReplaceAll(esc, html.EscapeString(link), fmt.Sprintf(`<a href="%s" style="color:#0e7490">%s</a>`, html.EscapeString(link), html.EscapeString(link)))
		}
		style := "margin:0 0 14px"
		if strings.HasPrefix(para, "— ") {
			style += ";color:#71717a;font-size:13px"
		}
		fmt.Fprintf(&b, `<p style="%s">%s</p>`, style, strings.ReplaceAll(esc, "\n", "<br>"))
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

func (s *Server) handleMailTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"subject": defaultMailSubject, "body": defaultMailBody,
		"request_subject": defaultRequestSubject, "request_body": defaultRequestBody,
		"placeholders": []string{"{sender}", "{title}", "{link}", "{message}", "{expires}", "{limit}", "{password_note}", "{encrypted_note}"},
	})
}

// handleMailPreview renders a template with sample values.
func (s *Server) handleMailPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Kind    string `json:"kind"` // send | request
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	sender := firstNonEmpty(st.SMTPFromName, u.Name, u.Email)
	vars := map[string]string{"sender": sender, "message": "Here are the photos from Saturday.", "expires": "until Fri 24 Oct 2026, 15:00", "limit": "It can be downloaded 5 times.", "password_note": "", "encrypted_note": ""}
	subject, body, button := firstNonEmpty(in.Subject, defaultMailSubject), firstNonEmpty(in.Body, defaultMailBody), "Open the files"
	if in.Kind == "request" {
		vars["title"], vars["link"], vars["limit"] = "Wedding photos", s.publicLink("r", "AbCd3fGh9k"), ""
		subject, body, button = firstNonEmpty(in.Subject, defaultRequestSubject), firstNonEmpty(in.Body, defaultRequestBody), "Upload files"
	} else {
		vars["title"], vars["link"] = "“Holiday photos”", s.publicLink("s", "AbCd3fGh9k")
	}
	text := fill(body, vars)
	writeJSON(w, http.StatusOK, map[string]string{"subject": strings.TrimSpace(fill(subject, vars)), "text": text, "html": mailHTML(text, vars["link"], button)})
}
