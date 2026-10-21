package server

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-files/internal/mailer"
	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// Links go out by email from the person's own SMTP account (Settings →
// Email), like invoices from Pocket Invoicing. An encrypted link's key lives
// only in the browser, so the browser hands the whole link over for the one
// message; it is used to write the email and never stored.

// seedOwnerSMTP copies SMTP_* from the environment into the owner's email
// settings once.
func (s *Server) seedOwnerSMTP(ctx context.Context) {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return
	}
	owner := s.store.OwnerID(ctx)
	if owner == 0 || s.store.UserSetting(ctx, owner, "smtp.seeded") == "1" {
		return
	}
	st, err := s.store.GetSettings(ctx, owner)
	if err != nil {
		return
	}
	if st.SMTPHost == "" {
		st.SMTPHost, st.SMTPPort, st.SMTPUser, st.SMTPPassword = s.cfg.SMTPHost, s.cfg.SMTPPort, s.cfg.SMTPUser, s.cfg.SMTPPassword
		st.SMTPFrom, st.SMTPFromName, st.SMTPTLS = s.cfg.SMTPFrom, s.cfg.SMTPFromName, s.cfg.SMTPTLS
		_ = s.store.SaveUserSettings(ctx, owner, st)
	}
	_ = s.store.SetUserSetting(ctx, owner, "smtp.seeded", "1")
}

func (s *Server) mailConfig(ctx context.Context, u *store.User) (mailer.Config, error) {
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil {
		return mailer.Config{}, err
	}
	cfg := mailer.Config{Host: st.SMTPHost, Port: st.SMTPPort, User: st.SMTPUser, Password: st.SMTPPassword, From: st.SMTPFrom, FromName: firstNonEmpty(st.SMTPFromName, u.Name), TLS: st.SMTPTLS}
	if !cfg.Configured() {
		return cfg, badRequest("email is not set up yet (Settings → Email)")
	}
	return cfg, nil
}

// recipients reads "a@x.com, b@y.com" (up to 20 addresses).
func recipients(list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, item := range list {
		item = strings.NewReplacer(";", ",", "\n", ",").Replace(item)
		addrs, err := mail.ParseAddressList(item)
		if err != nil { // "a@x.com b@y.com": one by one
			addrs = nil
			for _, part := range strings.FieldsFunc(item, func(r rune) bool { return r == ',' || r == ' ' }) {
				a, err := mail.ParseAddress(part)
				if err != nil {
					return nil, badRequest(fmt.Sprintf("%q is not an email address", part))
				}
				addrs = append(addrs, a)
			}
		}
		for _, a := range addrs {
			addr := strings.ToLower(a.Address)
			if !seen[addr] {
				seen[addr] = true
				out = append(out, addr)
			}
		}
	}
	if len(out) == 0 {
		return nil, badRequest("add at least one email address")
	}
	if len(out) > 20 {
		return nil, badRequest("at most 20 addresses at a time")
	}
	return out, nil
}

type emailIn struct {
	To      []string `json:"to"`
	Message string   `json:"message"`
	Link    string   `json:"link"` // encrypted sends: the whole link, key included
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in emailIn
	_ = decode(r, &in)
	if len(in.To) == 0 {
		in.To = []string{u.Email}
	}
	to, err := recipients(in.To)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	cfg, err := s.mailConfig(ctx, u)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	if err := mailer.Send(cfg, mailer.Message{To: to, Subject: "Pocket Files test email", Body: "If you can read this, Pocket Files can email your links.\n\n— Pocket Files"}); err != nil {
		writeErr(w, http.StatusBadGateway, "the mail server said: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": len(to)})
}

func untilText(t string) string {
	if t == "" {
		return "It does not expire."
	}
	return "It works until " + store.ParseTime(t).Local().Format("Mon 2 Jan 2006, 15:04") + "."
}

func (s *Server) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	sd, err := s.store.OwnSend(ctx, u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	var in emailIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	to, err := recipients(in.To)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	if !store.SendAvailable(sd) {
		writeErr(w, http.StatusGone, "this link has expired or was used up")
		return
	}
	link := s.publicLink("s", sd.ID)
	if sd.Encrypted {
		base, key, ok := strings.Cut(in.Link, "#")
		if !ok || key == "" || base != link {
			writeErr(w, http.StatusBadRequest, "an encrypted link can only be emailed from the browser that made it (its key lives there)")
			return
		}
		link = in.Link
	}
	cfg, err := s.mailConfig(ctx, u)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	who := cfg.FromName
	what := firstNonEmpty(sd.Label, "files")
	subject := fmt.Sprintf("%s sent you %s", who, what)
	intro := fmt.Sprintf("%s shared %s with you.", who, quoteLabel(sd.Label, "some files"))
	if sd.Kind == "note" {
		subject, intro = fmt.Sprintf("%s sent you a note", who), fmt.Sprintf("%s sent you a note.", who)
	}
	var b strings.Builder
	b.WriteString("Hi,\n\n" + intro + "\n\n")
	if m := strings.TrimSpace(in.Message); m != "" {
		b.WriteString(m + "\n\n")
	}
	b.WriteString(link + "\n\n")
	b.WriteString(untilText(sd.ExpiresAt))
	if sd.MaxDownloads == 1 {
		b.WriteString(" It opens once, then it is gone.")
	} else if sd.MaxDownloads > 0 {
		b.WriteString(fmt.Sprintf(" It can be downloaded %d times.", sd.MaxDownloads-sd.Downloads))
	}
	b.WriteString("\n")
	if sd.HasPassword {
		b.WriteString(fmt.Sprintf("It needs a password, which %s will give you separately.\n", who))
	}
	if sd.Encrypted {
		b.WriteString("The files are end-to-end encrypted: the part after # is the key, so keep the link whole.\n")
	}
	b.WriteString("\n— sent with Pocket Files\n")
	if err := mailer.Send(cfg, mailer.Message{To: to, Subject: subject, Body: b.String()}); err != nil {
		writeErr(w, http.StatusBadGateway, "the mail server said: "+err.Error())
		return
	}
	s.log.Info("link emailed", "send", sd.ID, "to", len(to), "by", u.Email)
	s.publish(ctx, "files.send_emailed", "send:"+sd.ID, u.ID, map[string]any{"id": sd.ID, "label": sd.Label, "to": to})
	writeJSON(w, http.StatusOK, map[string]any{"sent": len(to), "to": to})
}

func (s *Server) handleEmailRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	rq, err := s.store.OwnRequest(ctx, u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err, "request")
		return
	}
	var in emailIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	to, err := recipients(in.To)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	if !store.RequestOpen(rq) {
		writeErr(w, http.StatusGone, "this upload link no longer takes files")
		return
	}
	cfg, err := s.mailConfig(ctx, u)
	if err != nil {
		s.fail(w, err, "email")
		return
	}
	who := cfg.FromName
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Hi,\n\n%s asks you for files: %s.\n\n", who, rq.Title))
	if m := strings.TrimSpace(firstNonEmpty(in.Message, rq.Message)); m != "" {
		b.WriteString(m + "\n\n")
	}
	b.WriteString("Drop them here — you will not see what others sent:\n" + s.publicLink("r", rq.ID) + "\n\n")
	b.WriteString(untilText(rq.ExpiresAt) + "\n")
	if rq.PasswordHash != "" {
		b.WriteString(fmt.Sprintf("It needs a password, which %s will give you separately.\n", who))
	}
	b.WriteString("\n— sent with Pocket Files\n")
	if err := mailer.Send(cfg, mailer.Message{To: to, Subject: fmt.Sprintf("%s asks you for files: %s", who, rq.Title), Body: b.String()}); err != nil {
		writeErr(w, http.StatusBadGateway, "the mail server said: "+err.Error())
		return
	}
	s.log.Info("upload link emailed", "request", rq.ID, "to", len(to), "by", u.Email)
	writeJSON(w, http.StatusOK, map[string]any{"sent": len(to), "to": to})
}

func quoteLabel(label, def string) string {
	if label == "" {
		return def
	}
	return "“" + label + "”"
}
