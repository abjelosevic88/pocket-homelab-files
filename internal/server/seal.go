package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The sign-ins of connected drives (OAuth tokens, iCloud cookies, pasted rclone remotes)
// are sealed with AES-256-GCM under DATA_DIR/secret.key (32 random bytes, made on first
// use). The key is never in the database or the API; losing it means connecting the
// drives again.

const sealPrefix = "v1:"

func (s *Server) sealKeyBytes() ([]byte, error) {
	s.sealOnce.Do(func() {
		p := filepath.Join(s.cfg.DataDir, "secret.key")
		b, err := os.ReadFile(p)
		switch {
		case err == nil && len(b) == 32:
		case err == nil:
			s.sealErr = fmt.Errorf("%s must hold exactly 32 bytes (it has %d)", p, len(b))
			return
		case errors.Is(err, os.ErrNotExist):
			b = make([]byte, 32)
			if _, err := io.ReadFull(rand.Reader, b); err != nil {
				s.sealErr = err
				return
			}
			if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
				s.sealErr = err
				return
			}
			if err := os.WriteFile(p, b, 0o600); err != nil {
				s.sealErr = fmt.Errorf("write %s: %w", p, err)
				return
			}
			s.log.Info("created the key that seals connected drives' sign-ins", "path", p)
		default:
			s.sealErr = err
			return
		}
		s.sealKey = b
	})
	return s.sealKey, s.sealErr
}

func (s *Server) gcm() (cipher.AEAD, error) {
	key, err := s.sealKeyBytes()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts a secret for the database ("" stays "").
func (s *Server) seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return sealPrefix + base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// unseal decrypts what seal made ("" stays "").
func (s *Server) unseal(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, sealPrefix) {
		return "", errors.New("the stored sign-in is not sealed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, sealPrefix))
	if err != nil {
		return "", err
	}
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(raw) < g.NonceSize() {
		return "", errors.New("the stored sign-in is damaged")
	}
	plain, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the stored sign-in cannot be opened (was DATA_DIR/secret.key replaced?) — connect the drive again")
	}
	return string(plain), nil
}
