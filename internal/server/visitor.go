package server

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

// visitIn gathers what a request tells about who is opening a link.
// Location comes from Cloudflare: CF-IPCountry always, city and region
// only with its "Add visitor location headers" managed transform.
func visitIn(r *http.Request, tz, ref string) store.VisitIn {
	country := strings.ToUpper(r.Header.Get("Cf-Ipcountry"))
	if country == "XX" || country == "T1" {
		country = ""
	}
	lang := r.Header.Get("Accept-Language")
	if i := strings.IndexAny(lang, ",;"); i >= 0 {
		lang = lang[:i]
	}
	if ref == "" {
		ref = r.Referer()
	}
	if strings.Contains(ref, "/s/") && strings.Contains(ref, r.Host) {
		ref = "" // the share page itself
	}
	return store.VisitIn{IP: clientIP(r), Agent: r.UserAgent(), Country: country, Region: r.Header.Get("Cf-Region"), City: r.Header.Get("Cf-Ipcity"),
		Lang: strings.TrimSpace(lang), TZ: tz, Referer: ref}
}

var (
	reAndroidVer   = regexp.MustCompile(`Android (\d+)`)
	reIOSVer       = regexp.MustCompile(`(?:iPhone|CPU) OS (\d+)`)
	reAndroidModel = regexp.MustCompile(`Android [\d.]+; (?:[a-zA-Z-]+; )?([^;)]+?)(?: Build/[^;)]*)?[;)]`)
	browsers       = []struct{ name, token string }{
		{"Edge", "Edg"}, {"Opera", "OPR"}, {"Samsung Internet", "SamsungBrowser"}, {"Yandex", "YaBrowser"}, {"Vivaldi", "Vivaldi"},
		{"Firefox", "Firefox"}, {"Firefox", "FxiOS"}, {"Chrome", "CriOS"}, {"Edge", "EdgiOS"}, {"Chrome", "Chrome"}, {"Safari", "Version"},
	}
	inApps = []struct{ name, token string }{
		{"Facebook", "FBAN/FBIOS"}, {"Facebook", "FB_IAB/FB4A"}, {"Messenger", "FB_IAB/MESSENGER"}, {"Messenger", "FBAN/MessengerForiOS"},
		{"Instagram", "Instagram"}, {"WhatsApp", "WhatsApp"}, {"Telegram", "Telegram"}, {"Viber", "Viber"}, {"LinkedIn", "LinkedInApp"},
		{"Gmail", "GSA/"}, {"Snapchat", "Snapchat"}, {"TikTok", "musical_ly"}, {"TikTok", "BytedanceWebview"}, {"Slack", "Slack"}, {"Discord", "Discord"},
	}
)

// describeVisit fills in device and place: Cloudflare's country wins when
// it was recorded, the offline database adds the rest (and places old opens).
func (s *Server) describeVisit(v *store.SendVisit) {
	describeAgent(v)
	p := s.geo.Lookup(v.IP)
	if v.Country == "" {
		v.Country, v.Region, v.City = p.Country, p.Region, p.City
	} else if v.City == "" && p.Country == v.Country {
		v.Region, v.City = p.Region, p.City
	}
	if p.Country == v.Country {
		v.Lat, v.Lon = p.Lat, p.Lon
	}
	v.Network, v.ASN = p.Network, p.ASN
}

func (s *Server) describeVisits(ctx context.Context, uid int64, list []store.SendVisit) {
	seen := s.store.VisitsPerIP(ctx, uid)
	for i := range list {
		s.describeVisit(&list[i])
		list[i].Seen = seen[list[i].IP]
	}
}

// describeAgent reads browser, system, device and in-app browser from a user agent.
func describeAgent(v *store.SendVisit) {
	ua := v.Agent
	if ua == "" {
		return
	}
	for _, a := range inApps {
		if strings.Contains(ua, a.token) {
			v.App = a.name
			break
		}
	}
	switch {
	case strings.Contains(ua, "iPhone"):
		v.OS, v.Device, v.Model = "iOS", "phone", "iPhone"
	case strings.Contains(ua, "iPad"):
		v.OS, v.Device, v.Model = "iPadOS", "tablet", "iPad"
	case strings.Contains(ua, "Android"):
		v.OS, v.Device = "Android", "phone"
		if !strings.Contains(ua, "Mobile") {
			v.Device = "tablet"
		}
		if m := reAndroidModel.FindStringSubmatch(ua); m != nil && m[1] != "K" && !strings.HasPrefix(m[1], "wv") {
			v.Model = strings.TrimSpace(m[1])
		}
	case strings.Contains(ua, "Windows"):
		v.OS, v.Device = "Windows", "computer"
	case strings.Contains(ua, "Macintosh"):
		v.OS, v.Device = "macOS", "computer"
	case strings.Contains(ua, "CrOS"):
		v.OS, v.Device = "ChromeOS", "computer"
	case strings.Contains(ua, "Linux"):
		v.OS, v.Device = "Linux", "computer"
	}
	if m := reAndroidVer.FindStringSubmatch(ua); m != nil {
		v.OS += " " + m[1]
	} else if m := reIOSVer.FindStringSubmatch(ua); m != nil {
		v.OS += " " + m[1]
	}
	for _, b := range browsers {
		if strings.Contains(ua, b.token+"/") {
			v.Browser = b.name
			if m := regexp.MustCompile(b.token + `/(\d+)`).FindStringSubmatch(ua); m != nil {
				v.Browser += " " + m[1]
			}
			break
		}
	}
	if v.Browser == "" && v.Device == "" {
		v.Browser = truncateRunes(ua, 40) // a tool such as curl or wget
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// visitSummary is the one line about a visitor that goes into the alert:
// "Android 14 phone (SM-A528B) · Facebook app · 🇧🇦 Bosnia and Herzegovina, Sarajevo · 188.129.80.66".
func visitSummary(v store.SendVisit) string {
	var parts []string
	dev := strings.TrimSpace(v.OS + " " + v.Device)
	if v.Model != "" && v.Model != "iPhone" && v.Model != "iPad" {
		dev += " (" + v.Model + ")"
	}
	if v.App != "" {
		dev += " · " + v.App + " app"
	} else if v.Browser != "" && dev != "" {
		dev = v.Browser + " on " + dev
	} else if v.Browser != "" {
		dev = v.Browser
	}
	if dev != "" {
		parts = append(parts, dev)
	}
	if where := placeOf(v); where != "" {
		parts = append(parts, where)
	} else if v.TZ != "" {
		parts = append(parts, "time zone "+v.TZ)
	}
	if v.Network != "" {
		parts = append(parts, v.Network)
	}
	if v.IP != "" {
		parts = append(parts, v.IP)
	}
	return strings.Join(parts, " · ")
}

func placeOf(v store.SendVisit) string {
	if v.Country == "" {
		return ""
	}
	where := flagOf(v.Country) + " " + countryName(v.Country)
	if v.City != "" {
		where += ", " + v.City
	}
	return where
}

func flagOf(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	return string(rune(0x1F1E6+rune(cc[0]-'A'))) + string(rune(0x1F1E6+rune(cc[1]-'A')))
}

var countries = map[string]string{
	"BA": "Bosnia and Herzegovina", "RS": "Serbia", "HR": "Croatia", "ME": "Montenegro", "SI": "Slovenia", "MK": "North Macedonia",
	"XK": "Kosovo", "AL": "Albania", "BG": "Bulgaria", "RO": "Romania", "HU": "Hungary", "AT": "Austria", "DE": "Germany",
	"CH": "Switzerland", "IT": "Italy", "FR": "France", "ES": "Spain", "PT": "Portugal", "NL": "Netherlands", "BE": "Belgium",
	"LU": "Luxembourg", "GB": "United Kingdom", "IE": "Ireland", "DK": "Denmark", "SE": "Sweden", "NO": "Norway", "FI": "Finland",
	"IS": "Iceland", "PL": "Poland", "CZ": "Czechia", "SK": "Slovakia", "GR": "Greece", "TR": "Türkiye", "CY": "Cyprus", "MT": "Malta",
	"UA": "Ukraine", "RU": "Russia", "BY": "Belarus", "LT": "Lithuania", "LV": "Latvia", "EE": "Estonia", "MD": "Moldova",
	"US": "United States", "CA": "Canada", "MX": "Mexico", "BR": "Brazil", "AR": "Argentina", "AU": "Australia", "NZ": "New Zealand",
	"CN": "China", "JP": "Japan", "KR": "South Korea", "IN": "India", "SG": "Singapore", "AE": "United Arab Emirates", "IL": "Israel",
	"ZA": "South Africa", "EG": "Egypt",
}

func countryName(cc string) string {
	if n, ok := countries[cc]; ok {
		return n
	}
	return cc
}

func ordinal(n int) string {
	suf := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suf = "st"
		case 2:
			suf = "nd"
		case 3:
			suf = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suf)
}
