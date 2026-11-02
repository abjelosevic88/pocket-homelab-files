package server

import (
	"os"
	"testing"
)

// TestMain keeps the shell's live settings out of the tests. A shell that carries another app's
// CORE_*, ADMIN_*, NOTIFY_* or OIDC_* would otherwise register "files" with the real Pocket Core
// (rotating the live app's token and its sign-in redirect) or notify for real. Tests that need
// one of these set it themselves with t.Setenv.
func TestMain(m *testing.M) {
	for _, k := range []string{
		"CORE_URL", "CORE_APP_URL", "CORE_REGISTRATION_KEY",
		"ADMIN_EMAIL", "ADMIN_NAME", "ADMIN_PASSWORD",
		"NOTIFY_URL", "NOTIFY_KIND",
		"OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_NAME",
		"SMTP_HOST", "SMTP_USER", "SMTP_PASSWORD",
		"HOST", "BASE_URL", "PUBLIC_URL",
	} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
