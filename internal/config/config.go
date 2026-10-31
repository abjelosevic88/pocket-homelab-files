// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume works out of the box. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	PublicPort     int // second listener that serves only share and request pages (0 = off)
	BaseURL        string
	PublicURL      string // where share links point (default BASE_URL)
	DataDir        string
	DBPath         string
	FilesDir       string // one folder per person
	SendsDir       string // uploaded send blobs
	TrashDir       string
	ThumbsDir      string
	CloudDir       string // copies of connected drives, one folder per drive
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool
	Timezone       string

	// MountRoots are the server folders an admin may mount (and anything
	// below them). Empty = mounting is off.
	MountRoots []string

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string
	OIDCAutoCreate   bool

	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string

	NotifyURL  string
	NotifyKind string // apprise | ntfy | webhook

	// Connected drives (docs/clouds.md): rclone does the copying; the OAuth
	// clouds need the box owner's own client registrations.
	// SearchMountText also reads the documents inside mounts (default: names only).
	SearchMountText bool

	Rclone                string
	CloudSyncConcurrency  int
	GoogleClientID        string
	GoogleClientSecret    string
	MicrosoftClientID     string
	MicrosoftClientSecret string
	MicrosoftTenant       string
	DropboxAppKey         string
	DropboxAppSecret      string

	// SMTP for the owner, seeded once into their email settings.
	SMTPHost, SMTPUser, SMTPPassword, SMTPFrom, SMTPFromName, SMTPTLS string
	SMTPPort                                                          int

	MaxSendMB      int64 // per send, all files together (0 = no limit)
	MaxSendDays    int   // longest expiry a send may have (0 = "never" allowed)
	MaxRequestMB   int64 // per file uploaded through a request link (0 = no limit)
	PublicUploadMB int64 // chunk size limit on the public listener (Cloudflare caps a request at 100 MB)

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		PublicPort:     envInt("PUBLIC_PORT", 0),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		PublicURL:      strings.TrimRight(env("PUBLIC_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-files.db"),
		FilesDir:       env("FILES_DIR", dataDir+"/files"),
		SendsDir:       env("SENDS_DIR", dataDir+"/sends"),
		TrashDir:       env("TRASH_DIR", dataDir+"/trash"),
		ThumbsDir:      env("THUMBS_DIR", dataDir+"/thumbs"),
		CloudDir:       env("CLOUD_DIR", dataDir+"/cloud"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),
		Timezone:       env("TZ", "UTC"),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),

		OIDCIssuer:       strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:     env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: env("OIDC_CLIENT_SECRET", ""),
		OIDCName:         env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:   envBool("OIDC_AUTO_CREATE", true),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),

		NotifyURL:  env("NOTIFY_URL", ""),
		NotifyKind: strings.ToLower(env("NOTIFY_KIND", "apprise")),

		SearchMountText:       envBool("SEARCH_MOUNT_TEXT", false),
		Rclone:                env("RCLONE", "rclone"),
		CloudSyncConcurrency:  envInt("CLOUD_SYNC_CONCURRENCY", 2),
		GoogleClientID:        env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:    env("GOOGLE_CLIENT_SECRET", ""),
		MicrosoftClientID:     env("MICROSOFT_CLIENT_ID", ""),
		MicrosoftClientSecret: env("MICROSOFT_CLIENT_SECRET", ""),
		MicrosoftTenant:       env("MICROSOFT_TENANT", "common"),
		DropboxAppKey:         env("DROPBOX_APP_KEY", ""),
		DropboxAppSecret:      env("DROPBOX_APP_SECRET", ""),

		SMTPHost:     env("SMTP_HOST", ""),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPUser:     env("SMTP_USER", ""),
		SMTPPassword: env("SMTP_PASSWORD", ""),
		SMTPFrom:     env("SMTP_FROM", ""),
		SMTPFromName: env("SMTP_FROM_NAME", ""),
		SMTPTLS:      strings.ToLower(env("SMTP_TLS", "starttls")),

		MaxSendMB:      int64(envInt("MAX_SEND_MB", 10240)),
		MaxSendDays:    envInt("MAX_SEND_DAYS", 0),
		MaxRequestMB:   int64(envInt("MAX_REQUEST_MB", 0)),
		PublicUploadMB: int64(envInt("PUBLIC_CHUNK_MB", 64)),

		DemoData: envBool("DEMO_DATA", false),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	if c.PublicURL == "" {
		c.PublicURL = c.BaseURL
	}
	for _, p := range strings.FieldsFunc(env("MOUNT_ROOTS", ""), func(r rune) bool { return r == ',' || r == ':' || r == ';' }) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("MOUNT_ROOTS entries must be absolute paths (got %q)", p)
		}
		c.MountRoots = append(c.MountRoots, filepath.Clean(p))
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.PublicPort < 0 || c.PublicPort > 65535 || (c.PublicPort != 0 && c.PublicPort == c.Port) {
		return nil, fmt.Errorf("PUBLIC_PORT must differ from PORT (got %d)", c.PublicPort)
	}
	if c.CloudSyncConcurrency < 1 {
		c.CloudSyncConcurrency = 1
	}
	if c.PublicUploadMB < 1 {
		c.PublicUploadMB = 64
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return nil, fmt.Errorf("TZ %q is not a known time zone", c.Timezone)
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// PublicAddr returns host:port of the share-only listener.
func (c *Config) PublicAddr() string { return fmt.Sprintf("%s:%d", c.Host, c.PublicPort) }

// MountAllowed reports whether p is one of MountRoots or inside one.
func (c *Config) MountAllowed(p string) bool {
	p = filepath.Clean(p)
	for _, root := range c.MountRoots {
		if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) || root == "/" {
			return true
		}
	}
	return false
}
