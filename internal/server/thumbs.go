package server

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Thumbnails for JPEG, PNG, GIF and WebP, cached by file identity (path,
// size, modification time) so a changed file gets a new one.

const thumbMaxSource = 60 << 20

var thumbSem = make(chan struct{}, 3)
var thumbLocks sync.Map

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	sp, err := s.spaceFromQuery(r)
	if err != nil {
		s.fail(w, err, "open")
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := sp.Resolve(rel)
	if err != nil {
		s.fail(w, err, "thumb")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	size := 320
	if n, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && n >= 64 && n <= 1600 {
		size = n
	}
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
	default:
		http.NotFound(w, r)
		return
	}
	if fi.Size() > thumbMaxSource {
		http.NotFound(w, r)
		return
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", abs, fi.Size(), fi.ModTime().UnixNano(), size)))
	key := hex.EncodeToString(sum[:])
	cache := filepath.Join(s.cfg.ThumbsDir, key[:2], key+".jpg")
	etag := `"` + key[:16] + `"`
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if f, err := os.Open(cache); err == nil {
		defer f.Close()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.Copy(w, f)
		return
	}
	mu, _ := thumbLocks.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer func() { mu.(*sync.Mutex).Unlock(); thumbLocks.Delete(key) }()
	thumbSem <- struct{}{}
	data, err := makeThumb(abs, size)
	<-thumbSem
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = os.MkdirAll(filepath.Dir(cache), 0o755)
	_ = os.WriteFile(cache, data, 0o644)
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

func makeThumb(p string, size int) ([]byte, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > 80_000_000 {
		return nil, fmt.Errorf("image too large")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	scale := float64(size) / float64(max(b.Dx(), b.Dy()))
	if scale > 1 {
		scale = 1
	}
	tw, th := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	out := orient(dst, jpegOrientation(raw))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jpegOrientation reads the EXIF orientation tag (1 when absent).
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xE1 && i+4+n <= len(b) && n > 8 && string(b[i+4:i+10]) == "Exif\x00\x00" {
			return exifOrientation(b[i+10 : i+2+n])
		}
		if marker == 0xDA {
			return 1
		}
		i += 2 + n
	}
	return 1
}

func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient applies an EXIF orientation to an image.
func orient(src *image.RGBA, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.SetRGBA(nx, ny, src.RGBAAt(x, y))
		}
	}
	return dst
}

// cleanThumbs drops cached thumbnails nobody asked for in 60 days.
func (s *Server) cleanThumbs() {
	cut := time.Now().AddDate(0, 0, -60)
	_ = filepath.Walk(s.cfg.ThumbsDir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && fi.ModTime().Before(cut) {
			_ = os.Remove(p)
		}
		return nil
	})
}
