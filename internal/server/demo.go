package server

import (
	"context"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// seedDemo puts a few made-up files into the first person's empty home.
func (s *Server) seedDemo(ctx context.Context) error {
	owner := s.store.OwnerID(ctx)
	if owner == 0 {
		u, err := s.firstOrSystemUser(ctx, "admin@localhost", "Admin")
		if err != nil {
			return err
		}
		owner = u.ID
	}
	home := s.homeSpace(owner)
	s.seedUser(ctx, owner)
	if list, _ := home.List("/", true); len(list) > 0 {
		return nil
	}
	files := map[string]string{
		"Documents/Trip to Kotor.md":         "# Kotor, May\n\n- Ferry from Lepetane at 9:10\n- Walls before noon, it gets hot\n- Dinner at the old town square\n",
		"Documents/Budget 2026.csv":          "month,rent,food,travel\nJan,650,310,0\nFeb,650,295,120\nMar,650,330,480\n",
		"Documents/Notes/ideas.txt":          "A shelf for the hallway.\nCall the plumber about the bathroom tap.\n",
		"Projects/website/index.html":        "<!doctype html><title>Hello</title><h1>Hello</h1>\n",
		"Projects/website/README.md":         "# Website\n\nStatic page, deployed by hand.\n",
		"Music/Playlists/Sunday morning.m3u": "#EXTM3U\n01 - Intro.flac\n02 - Coffee.flac\n",
	}
	for p, body := range files {
		dir := "/" + p[:strings.LastIndex(p, "/")]
		name := p[strings.LastIndex(p, "/")+1:]
		if _, err := home.Write(dir, name, strings.NewReader(body), vfs.Skip, time.Now().Add(-48*time.Hour)); err != nil {
			return err
		}
	}
	_ = home.Mkdir("/Photos")
	s.log.Info("demo files created", "user", owner)
	return nil
}
