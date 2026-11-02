// Package geo places an IP address offline with DB-IP's free "Lite"
// databases (CC BY 4.0, https://db-ip.com): country, region, city and
// the network it belongs to. The files are fetched once a month into the
// data folder; no visitor address ever leaves the box.
package geo

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Place is what the databases know about an address.
type Place struct {
	Country string  `json:"country,omitempty"` // ISO code
	Region  string  `json:"region,omitempty"`
	City    string  `json:"city,omitempty"`
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Network string  `json:"network,omitempty"` // the provider, e.g. "Telekom Srpske"
	ASN     uint    `json:"asn,omitempty"`
}

// DB looks addresses up; the zero value (or a nil *DB) knows nothing.
type DB struct {
	dir       string
	log       *slog.Logger
	mu        sync.RWMutex
	city, asn *maxminddb.Reader
	priv      privacy
}

var files = map[string]string{"city": "dbip-city-lite", "asn": "dbip-asn-lite"}

// Open uses whatever is already in dir; Run keeps it current.
func Open(dir string, log *slog.Logger) *DB {
	g := &DB{dir: dir, log: log}
	g.load()
	g.loadLists()
	return g
}

func (g *DB) path(kind string) string { return filepath.Join(g.dir, files[kind]+".mmdb") }

func (g *DB) load() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for kind, r := range map[string]**maxminddb.Reader{"city": &g.city, "asn": &g.asn} {
		nr, err := maxminddb.Open(g.path(kind))
		if err != nil {
			continue
		}
		if *r != nil {
			_ = (*r).Close()
		}
		*r = nr
	}
}

// Run downloads the databases when missing or older than a month, then
// checks daily.
func (g *DB) Run(ctx context.Context) {
	for {
		if g.stale() {
			if err := g.update(ctx); err != nil {
				g.log.Warn("ip location database not updated", "err", err)
			} else {
				g.load()
				g.log.Info("ip location database updated")
			}
		}
		if err := g.updateLists(ctx); err != nil {
			g.log.Warn("vpn / tor / private relay lists not all updated", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func (g *DB) stale() bool {
	for kind := range files {
		fi, err := os.Stat(g.path(kind))
		if err != nil || time.Since(fi.ModTime()) > 32*24*time.Hour {
			return true
		}
	}
	return false
}

func (g *DB) update(ctx context.Context) error {
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return err
	}
	for kind, name := range files {
		var err error
		// this month's file appears a day or two in; fall back to last month's
		for _, m := range []time.Time{time.Now().UTC(), time.Now().UTC().AddDate(0, -1, 0)} {
			if err = g.fetch(ctx, fmt.Sprintf("https://download.db-ip.com/free/%s-%s.mmdb.gz", name, m.Format("2006-01")), g.path(kind)); err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
	}
	return nil
}

func (g *DB) fetch(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(g.dir, ".geo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, zr); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := maxminddb.Open(tmp.Name()); err != nil {
		return fmt.Errorf("bad database: %w", err)
	}
	return os.Rename(tmp.Name(), dst)
}

type names struct {
	En string `maxminddb:"en"`
}

// Lookup places ip; an unknown or private address gives an empty Place.
func (g *DB) Lookup(ip string) Place {
	var p Place
	if g == nil {
		return p
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.IsPrivate() || addr.IsLoopback() {
		return p
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.city != nil {
		var rec struct {
			Country struct {
				ISO string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
			City struct {
				Names names `maxminddb:"names"`
			} `maxminddb:"city"`
			Subdivisions []struct {
				Names names `maxminddb:"names"`
			} `maxminddb:"subdivisions"`
			Location struct {
				Lat float64 `maxminddb:"latitude"`
				Lon float64 `maxminddb:"longitude"`
			} `maxminddb:"location"`
		}
		if g.city.Lookup(addr).Decode(&rec) == nil {
			p.Country, p.City, p.Lat, p.Lon = rec.Country.ISO, rec.City.Names.En, rec.Location.Lat, rec.Location.Lon
			if len(rec.Subdivisions) > 0 {
				p.Region = rec.Subdivisions[0].Names.En
			}
		}
	}
	if g.asn != nil {
		var rec struct {
			ASN uint   `maxminddb:"autonomous_system_number"`
			Org string `maxminddb:"autonomous_system_organization"`
		}
		if g.asn.Lookup(addr).Decode(&rec) == nil {
			p.ASN, p.Network = rec.ASN, rec.Org
		}
	}
	return p
}
