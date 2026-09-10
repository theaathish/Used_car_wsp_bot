package images

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	Dir string // DATA_DIR/images
}

func New(dataDir string) (*Store, error) {
	d := filepath.Join(dataDir, "images")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: d}, nil
}

// SaveData validates type by magic bytes (§36), dedups by content hash.
func (s *Store) SaveData(origName string, data []byte) (string, string, error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("empty file")
	}
	ext := strings.ToLower(filepath.Ext(origName))
	kind := sniff(data)
	if kind == "" {
		return "", "", fmt.Errorf("unsupported file type")
	}
	// Trust content, not the client-supplied extension.
	ext = kind
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	name := hash[:16] + ext
	dst := filepath.Join(s.Dir, name)
	if _, err := os.Stat(dst); err != nil {
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", "", err
		}
	}
	return "images/" + name, hash, nil
}

func sniff(b []byte) string {
	if len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF {
		return ".jpg"
	}
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return ".png"
	}
	if len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
		return ".webp"
	}
	return ""
}
