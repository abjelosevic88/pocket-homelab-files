package geo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Public lists of addresses that hide the visitor, refreshed daily.
var lists = map[string]string{
	"tor":        "https://check.torproject.org/torbulkexitlist",
	"relay":      "https://mask-api.icloud.com/egress-ip-ranges.csv",
	"vpn":        "https://raw.githubusercontent.com/X4BNet/lists_vpn/main/output/vpn/ipv4.txt",
	"datacenter": "https://raw.githubusercontent.com/X4BNet/lists_vpn/main/output/datacenter/ipv4.txt",
}

// Hidden is what the lists say about an address.
type Hidden struct {
	Tor, Relay, VPN, Datacenter bool
}

type span struct{ lo, hi [16]byte }

// ranges is a sorted, merged set of address spans.
type ranges []span

func (r ranges) has(a netip.Addr) bool {
	k := a.As16()
	i, _ := slices.BinarySearchFunc(r, k, func(s span, k [16]byte) int { return bytes.Compare(s.lo[:], k[:]) })
	// i is the first span starting after k (or at k)
	if i < len(r) && r[i].lo == k {
		return true
	}
	return i > 0 && bytes.Compare(k[:], r[i-1].hi[:]) <= 0
}

func lastAddr(p netip.Prefix) [16]byte {
	b := p.Masked().Addr().As16()
	bits := p.Bits()
	if p.Addr().Is4() {
		bits += 96
	}
	for i := bits; i < 128; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	return b
}

// parseRanges reads one address or prefix per line (the first CSV field).
func parseRanges(rd io.Reader) ranges {
	var out ranges
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, ','); i >= 0 {
			line = line[:i]
		}
		if line == "" || line[0] == '#' {
			continue
		}
		var p netip.Prefix
		var err error
		if strings.Contains(line, "/") {
			p, err = netip.ParsePrefix(line)
		} else {
			var a netip.Addr
			if a, err = netip.ParseAddr(line); err == nil {
				p = netip.PrefixFrom(a, a.BitLen())
			}
		}
		if err != nil {
			continue
		}
		out = append(out, span{p.Masked().Addr().As16(), lastAddr(p)})
	}
	slices.SortFunc(out, func(a, b span) int { return bytes.Compare(a.lo[:], b.lo[:]) })
	merged := out[:0]
	for _, s := range out {
		if n := len(merged); n > 0 && bytes.Compare(s.lo[:], merged[n-1].hi[:]) <= 0 {
			if bytes.Compare(s.hi[:], merged[n-1].hi[:]) > 0 {
				merged[n-1].hi = s.hi
			}
			continue
		}
		merged = append(merged, s)
	}
	return slices.Clip(merged)
}

type privacy struct {
	mu   sync.RWMutex
	sets map[string]ranges
}

func (g *DB) listPath(name string) string { return filepath.Join(g.dir, "list-"+name+".txt") }

func (g *DB) loadLists() {
	sets := map[string]ranges{}
	for name := range lists {
		f, err := os.Open(g.listPath(name))
		if err != nil {
			continue
		}
		sets[name] = parseRanges(f)
		f.Close()
	}
	g.priv.mu.Lock()
	g.priv.sets = sets
	g.priv.mu.Unlock()
}

func (g *DB) updateLists(ctx context.Context) error {
	var errs []string
	for name, url := range lists {
		if fi, err := os.Stat(g.listPath(name)); err == nil && time.Since(fi.ModTime()) < 20*time.Hour {
			continue
		}
		if err := g.fetchList(ctx, url, g.listPath(name)); err != nil {
			errs = append(errs, name+": "+err.Error())
		}
	}
	g.loadLists()
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (g *DB) fetchList(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", res.Status)
	}
	tmp, err := os.CreateTemp(g.dir, ".list-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(res.Body, 200<<20))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n < 100 {
		return fmt.Errorf("list came back empty")
	}
	return os.Rename(tmp.Name(), dst)
}

// Hidden checks an address against the lists.
func (g *DB) Hidden(ip string) Hidden {
	var h Hidden
	if g == nil {
		return h
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return h
	}
	a = a.Unmap()
	g.priv.mu.RLock()
	defer g.priv.mu.RUnlock()
	s := g.priv.sets
	h.Tor, h.Relay, h.VPN, h.Datacenter = s["tor"].has(a), s["relay"].has(a), s["vpn"].has(a), s["datacenter"].has(a)
	return h
}

// hostingWords mark a provider that rents servers rather than connecting homes and phones.
var hostingWords = []string{"m247", "datacamp", "cdn77", "digitalocean", "ovh", "hetzner", "linode", "akamai", "amazon", "google cloud", "google llc",
	"microsoft", "oracle", "vultr", "choopa", "contabo", "leaseweb", "packethub", "clouvider", "zenlayer", "tzulo", "performive", "quadranet",
	"psychz", "xtom", "ipxo", "global secure layer", "hostroyale", "hosting", "datacenter", "data center", "server", "vpn", "proxy", "colocation",
	"cloudflare", "alibaba", "tencent", "scaleway", "upcloud", "ionos", "hostinger", "kamatera", "g-core", "gcore", "stark industries", "aeza",
	"melbicom", "servers.com", "cogent", "frantech", "buyvm", "private layer", "nordvpn", "expressvpn", "surfshark", "proton", "mullvad", "31173"}

// HostingNetwork reports whether a provider name looks like a server host.
func HostingNetwork(org string) bool {
	o := strings.ToLower(org)
	for _, w := range hostingWords {
		if strings.Contains(o, w) {
			return true
		}
	}
	return false
}

// zones maps time zones and countries both ways, from the system's
// zone.tab and zone1970.tab.
type zones struct {
	once      sync.Once
	byZone    map[string][]string
	byCountry map[string][]string
}

var tzTables = &zones{}

func (z *zones) load() {
	z.byZone, z.byCountry = map[string][]string{}, map[string][]string{}
	dir := "/usr/share/zoneinfo"
	if d := os.Getenv("ZONEINFO"); d != "" {
		dir = d
	}
	for _, tab := range []string{"zone.tab", "zone1970.tab"} {
		b, err := os.ReadFile(filepath.Join(dir, tab))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, "\t")
			if len(f) < 3 || strings.HasPrefix(line, "#") {
				continue
			}
			for _, cc := range strings.Split(f[0], ",") {
				z.byZone[f[2]] = append(z.byZone[f[2]], cc)
				z.byCountry[cc] = append(z.byCountry[cc], f[2])
			}
		}
	}
}

// Clock compares a browser's time zone with the country of its address:
// Same (or unknown), SameClock (another country on the same clock — a
// neighbour, roaming, or a nearby VPN server) or OtherClock (a different
// UTC offset right now: the device and its address are far apart).
type Clock int

const (
	ClockSame Clock = iota
	ClockSameOffset
	ClockOther
)

// CompareClock checks tz against country; also returns the two UTC offsets
// ("UTC+1", "UTC-5") for the explanation.
func CompareClock(tz, country string, now time.Time) (Clock, string, string) {
	z := tzTables
	z.once.Do(z.load)
	if tz == "" || country == "" || tz == "UTC" || strings.HasPrefix(tz, "Etc/") || slices.Contains(z.byZone[tz], country) {
		return ClockSame, "", ""
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || len(z.byCountry[country]) == 0 {
		return ClockSame, "", ""
	}
	_, mine := now.In(loc).Zone()
	var theirs []int
	for _, name := range z.byCountry[country] {
		if l, err := time.LoadLocation(name); err == nil {
			_, off := now.In(l).Zone()
			if off == mine {
				return ClockSameOffset, utcOffset(mine), utcOffset(off)
			}
			theirs = append(theirs, off)
		}
	}
	if len(theirs) == 0 {
		return ClockSame, "", ""
	}
	return ClockOther, utcOffset(mine), utcOffset(theirs[0])
}

func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	if sec%3600 == 0 {
		return fmt.Sprintf("UTC%s%d", sign, sec/3600)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, sec/3600, sec%3600/60)
}
