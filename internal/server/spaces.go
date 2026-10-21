package server

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-files/internal/store"
	"github.com/abjelosevic88/pocket-homelab-files/internal/vfs"
)

// Names the WebDAV root shows next to a person's own files.
const (
	davShared = "Shared with me"
	davMounts = "Mounts"
)

func (s *Server) homeDir(uid int64) string {
	return filepath.Join(s.cfg.FilesDir, strconv.FormatInt(uid, 10))
}

func (s *Server) homeSpace(uid int64) *vfs.Space {
	return &vfs.Space{ID: "home", Name: "My files", Root: s.homeDir(uid), Perm: vfs.Edit, OwnerID: uid}
}

// space opens one of the places the person may reach.
func (s *Server) space(ctx context.Context, uid int64, id string) (*vfs.Space, error) {
	switch {
	case id == "" || id == "home":
		sp := s.homeSpace(uid)
		s.seedUser(ctx, uid)
		return sp, nil
	case strings.HasPrefix(id, "mount:"):
		n, _ := strconv.ParseInt(strings.TrimPrefix(id, "mount:"), 10, 64)
		m, err := s.store.MountFor(ctx, uid, n)
		if err != nil {
			return nil, err
		}
		return s.mountSpace(m)
	case strings.HasPrefix(id, "share:"):
		n, _ := strconv.ParseInt(strings.TrimPrefix(id, "share:"), 10, 64)
		sh, err := s.store.ShareFor(ctx, uid, n)
		if err != nil {
			return nil, err
		}
		return s.shareSpace(sh), nil
	}
	return nil, store.ErrNotFound
}

func (s *Server) mountSpace(m *store.Mount) (*vfs.Space, error) {
	if !s.cfg.MountAllowed(m.Path) {
		return nil, fmt.Errorf("%w: %s is no longer under MOUNT_ROOTS", vfs.ErrForbidden, m.Name)
	}
	perm := vfs.ParsePerm(m.Perm)
	if m.ReadOnly && perm > vfs.View {
		perm = vfs.View
	}
	return &vfs.Space{ID: "mount:" + strconv.FormatInt(m.ID, 10), Name: m.Name, Root: m.Path, Perm: perm}, nil
}

func (s *Server) shareSpace(sh *store.Share) *vfs.Space {
	sp := &vfs.Space{ID: "share:" + strconv.FormatInt(sh.ID, 10), Name: path.Base(sh.Path), Perm: vfs.ParsePerm(sh.Perm), OwnerID: sh.OwnerID, Base: sh.Path}
	full := filepath.Join(s.homeDir(sh.OwnerID), filepath.FromSlash(sh.Path))
	if sh.IsDir {
		sp.Root = full
	} else {
		sp.Root, sp.File = filepath.Dir(full), filepath.Base(full)
	}
	if sh.Path == "/" {
		sp.Name = firstNonEmpty(sh.OwnerName, sh.OwnerEmail) + "'s files"
	}
	return sp
}

// spaceFromQuery opens ?space= for the signed-in person.
func (s *Server) spaceFromQuery(r *http.Request) (*vfs.Space, error) {
	return s.space(r.Context(), me(r), r.URL.Query().Get("space"))
}

type spaceView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // home | mount | share
	Name     string `json:"name"`
	Perm     string `json:"perm"`
	IsDir    bool   `json:"is_dir"`
	Owner    string `json:"owner,omitempty"`
	Color    string `json:"color,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// handleSpaces lists the places in the sidebar: home, mounts, shares.
func (s *Server) handleSpaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := me(r)
	out := []spaceView{{ID: "home", Kind: "home", Name: "My files", Perm: "edit", IsDir: true}}
	mounts, err := s.store.MountsFor(ctx, uid)
	if err != nil {
		s.fail(w, err, "mounts")
		return
	}
	for _, m := range mounts {
		out = append(out, spaceView{ID: "mount:" + strconv.FormatInt(m.ID, 10), Kind: "mount", Name: m.Name, Perm: m.Perm, IsDir: true, Color: m.Color, ReadOnly: m.ReadOnly})
	}
	shares, err := s.store.SharesWith(ctx, uid)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	for i := range shares {
		sp := s.shareSpace(&shares[i])
		out = append(out, spaceView{ID: sp.ID, Kind: "share", Name: sp.Name, Perm: shares[i].Perm, IsDir: shares[i].IsDir, Owner: firstNonEmpty(shares[i].OwnerName, shares[i].OwnerEmail)})
	}
	writeJSON(w, http.StatusOK, out)
}

// davNames gives every share and mount a unique folder name for WebDAV.
func (s *Server) davNames(ctx context.Context, uid int64) (shares map[string]*vfs.Space, mounts map[string]*vfs.Space) {
	shares, mounts = map[string]*vfs.Space{}, map[string]*vfs.Space{}
	if list, err := s.store.SharesWith(ctx, uid); err == nil {
		for i := range list {
			sp := s.shareSpace(&list[i])
			name := sp.Name
			if sp.File != "" {
				name = sp.File
			}
			if _, taken := shares[name]; taken {
				name = fmt.Sprintf("%s (%s)", name, firstNonEmpty(list[i].OwnerName, list[i].OwnerEmail))
			}
			if _, taken := shares[name]; taken {
				name = fmt.Sprintf("%s (%d)", name, list[i].ID)
			}
			shares[name] = sp
		}
	}
	if list, err := s.store.MountsFor(ctx, uid); err == nil {
		for i := range list {
			if sp, err := s.mountSpace(&list[i]); err == nil {
				mounts[list[i].Name] = sp
			}
		}
	}
	return
}

func sortedKeys(m map[string]*vfs.Space) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyStr(v ...string) string { return firstNonEmpty(v...) }
