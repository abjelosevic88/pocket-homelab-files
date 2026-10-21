package store

import (
	"context"
	"strconv"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Files"

func defaults() Settings {
	return Settings{
		Title: DefaultTitle, View: "list", Sort: "name", NotifyKind: "apprise",
		NotifySends: true, NotifyUploads: true, SendExpiry: "7d", SendDownloads: 0, SendEncrypted: true, TrashDays: 30,
		SMTPPort: 587, SMTPTLS: "starttls",
	}
}

// GetSettings loads the box settings plus the preferences of userID, with
// defaults applied. userID 0 gives the box settings with default preferences.
func (s *Store) GetSettings(ctx context.Context, userID int64) (Settings, error) {
	st := defaults()
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('title', 'setup_complete')
		UNION ALL SELECT key, value FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		switch k {
		case "title":
			if v != "" {
				st.Title = v
			}
		case "setup_complete":
			st.SetupComplete = v == "1"
		case "show_hidden":
			st.ShowHidden = v == "1"
		case "view":
			if v == "list" || v == "grid" {
				st.View = v
			}
		case "sort":
			switch v {
			case "name", "modified", "size", "type":
				st.Sort = v
			}
		case "sort_desc":
			st.SortDesc = v == "1"
		case "timezone":
			st.Timezone = v
		case "notify_kind":
			if v != "" {
				st.NotifyKind = v
			}
		case "notify_url":
			st.NotifyURL = v
		case "notify_token":
			st.NotifyToken = v
		case "notify_sends":
			st.NotifySends = v != "0"
		case "notify_uploads":
			st.NotifyUploads = v != "0"
		case "send_expiry":
			if v != "" {
				st.SendExpiry = v
			}
		case "send_downloads":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				st.SendDownloads = n
			}
		case "send_encrypted":
			st.SendEncrypted = v != "0"
		case "smtp_host":
			st.SMTPHost = v
		case "smtp_port":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				st.SMTPPort = n
			}
		case "smtp_user":
			st.SMTPUser = v
		case "smtp_password":
			st.SMTPPassword = v
			st.SMTPPasswordSet = v != ""
		case "smtp_from":
			st.SMTPFrom = v
		case "smtp_from_name":
			st.SMTPFromName = v
		case "smtp_tls":
			switch v {
			case "none", "starttls", "tls":
				st.SMTPTLS = v
			}
		case "trash_days":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				st.TrashDays = n
			}
		}
	}
	return st, rows.Err()
}

// SaveBoxSettings stores the box-wide settings.
func (s *Store) SaveBoxSettings(ctx context.Context, st Settings) error {
	for k, v := range map[string]string{"title": st.Title, "setup_complete": b01(st.SetupComplete)} {
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

// SaveUserSettings stores one person's preferences.
func (s *Store) SaveUserSettings(ctx context.Context, userID int64, st Settings) error {
	for k, v := range map[string]string{
		"show_hidden": b01(st.ShowHidden), "view": st.View, "sort": st.Sort, "sort_desc": b01(st.SortDesc), "timezone": st.Timezone,
		"notify_kind": st.NotifyKind, "notify_url": st.NotifyURL, "notify_token": st.NotifyToken,
		"notify_sends": b01(st.NotifySends), "notify_uploads": b01(st.NotifyUploads),
		"send_expiry": st.SendExpiry, "send_downloads": strconv.Itoa(st.SendDownloads), "send_encrypted": b01(st.SendEncrypted),
		"trash_days": strconv.Itoa(st.TrashDays),
		"smtp_host":  st.SMTPHost, "smtp_port": strconv.Itoa(st.SMTPPort), "smtp_user": st.SMTPUser, "smtp_password": st.SMTPPassword,
		"smtp_from": st.SMTPFrom, "smtp_from_name": st.SMTPFromName, "smtp_tls": st.SMTPTLS,
	} {
		if err := s.SetUserSetting(ctx, userID, k, v); err != nil {
			return err
		}
	}
	return nil
}

// UserSetting reads one preference ("" when unset).
func (s *Store) UserSetting(ctx context.Context, userID int64, key string) string {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT value FROM user_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&v)
	return v
}

// SetUserSetting writes one preference.
func (s *Store) SetUserSetting(ctx context.Context, userID int64, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?) ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, userID, key, value)
	return err
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
