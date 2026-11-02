package server

import (
	"testing"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
)

func TestDescribeAgent(t *testing.T) {
	cases := []struct{ ua, want string }{
		{"Mozilla/5.0 (Linux; Android 14; SM-A528B Build/UP1A.231005.007; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/156.0.8078.47 Mobile Safari/537.36 [FB_IAB/FB4A;FBAV/585.0.0.53.94;]",
			"Android 14 phone (SM-A528B) · Facebook app · 🇧🇦 Bosnia and Herzegovina, Banja Luka · 1.2.3.4"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:159.0) Gecko/20100101 Firefox/159.0", "Firefox 159 on macOS computer · 🇧🇦 Bosnia and Herzegovina, Banja Luka · 1.2.3.4"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", "Safari 18 on iOS 18 phone · 🇧🇦 Bosnia and Herzegovina, Banja Luka · 1.2.3.4"},
		{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Mobile Safari/537.36", "Chrome 156 on Android 10 phone · 🇧🇦 Bosnia and Herzegovina, Banja Luka · 1.2.3.4"},
		{"curl/8.5.0", "curl/8.5.0 · 🇧🇦 Bosnia and Herzegovina, Banja Luka · 1.2.3.4"},
	}
	for _, c := range cases {
		v := store.SendVisit{Agent: c.ua, IP: "1.2.3.4", Country: "BA", City: "Banja Luka"}
		describeAgent(&v)
		got := visitSummary(v)
		if got != c.want {
			t.Errorf("%s\n got %q\nwant %q", c.ua, got, c.want)
		}
	}
	if ordinal(2) != "2nd" || ordinal(11) != "11th" || ordinal(23) != "23rd" {
		t.Error("ordinal")
	}
}
