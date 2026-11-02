package geo

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestRanges(t *testing.T) {
	r := parseRanges(strings.NewReader("# comment\n10.0.0.0/8\n10.1.0.0/16\n192.168.1.7\n172.224.226.0/27,GB,GB-EN,London,\n2a02:26f7::/32\n"))
	for ip, want := range map[string]bool{"10.200.3.4": true, "11.0.0.0": false, "192.168.1.7": true, "192.168.1.8": false,
		"172.224.226.31": true, "172.224.226.32": false, "2a02:26f7:1::1": true, "2a02:26f8::1": false, "9.255.255.255": false} {
		if got := r.has(netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s: got %v", ip, got)
		}
	}
}

func TestCompareClock(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if c, _, _ := CompareClock("Europe/Sarajevo", "BA", now); c != ClockSame {
		t.Errorf("BA in BA: %v", c)
	}
	if len(tzTables.byZone) == 0 {
		t.Skip("no zone tables on this system")
	}
	if c, _, _ := CompareClock("Europe/Sarajevo", "RS", now); c != ClockSameOffset {
		t.Errorf("BA clock in RS: %v", c)
	}
	if c, a, b := CompareClock("Europe/Sarajevo", "US", now); c != ClockOther || a != "UTC+1" || b == "" {
		t.Errorf("BA clock in US: %v %s %s", c, a, b)
	}
	if c, _, _ := CompareClock("UTC", "US", now); c != ClockSame {
		t.Error("UTC should not count")
	}
	if !HostingNetwork("M247 Europe SRL") || HostingNetwork("Telekom Srpske") {
		t.Error("hosting words")
	}
}
