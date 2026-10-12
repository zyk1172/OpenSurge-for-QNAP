// Package runtimeimage isolates executable pages from Docker's image storage.
// The trusted image supplies the manifest; /data supplies only a verified cache.
package runtimeimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Entry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	Link   string `json:"link,omitempty"`
}

type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Outside /usr: a creation-time cache bind must never replace the trust anchor.
const ManifestPath = "/opt/opensurge/runtime-image.json"

func digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Generate(root string) (Manifest, error) {
	m := Manifest{Version: 1}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Path: filepath.ToSlash(rel), Mode: uint32(info.Mode().Perm() &^ 0022)}
		switch {
		case d.IsDir():
			e.Kind = "dir"
		case info.Mode().IsRegular():
			e.Kind = "file"
			e.SHA256, err = digest(path)
		case info.Mode()&os.ModeSymlink != 0:
			e.Kind = "link"
			// Symlink permissions are not an access boundary and differ across OSes.
			e.Mode = 0755
			e.Link, err = os.Readlink(path)
		default:
			return fmt.Errorf("unsupported image entry %s", rel)
		}
		if err != nil {
			return err
		}
		m.Entries = append(m.Entries, e)
		return nil
	})
	return m, err
}

func validate(m Manifest) error {
	if m.Version != 1 || len(m.Entries) == 0 || m.Entries[0].Path != "." || m.Entries[0].Kind != "dir" {
		return fmt.Errorf("invalid runtime image manifest")
	}
	seen := make(map[string]string)
	for _, e := range m.Entries {
		if filepath.IsAbs(e.Path) || filepath.ToSlash(filepath.Clean(e.Path)) != e.Path || e.Path == ".." || strings.HasPrefix(e.Path, "../") {
			return fmt.Errorf("unsafe manifest path %q", e.Path)
		}
		if _, exists := seen[e.Path]; exists {
			return fmt.Errorf("duplicate manifest path %q", e.Path)
		}
		if e.Path != "." && seen[filepath.ToSlash(filepath.Dir(e.Path))] != "dir" {
			return fmt.Errorf("manifest parent is not a directory: %q", e.Path)
		}
		if e.Mode&0022 != 0 || e.Mode > 0777 {
			return fmt.Errorf("unsafe image mode for %s", e.Path)
		}
		switch e.Kind {
		case "file":
			b, err := hex.DecodeString(e.SHA256)
			if err != nil || len(b) != sha256.Size {
				return fmt.Errorf("invalid image hash: %s", e.Path)
			}
		case "link":
			if e.Link == "" {
				return fmt.Errorf("empty image link: %s", e.Path)
			}
		case "dir":
		default:
			return fmt.Errorf("invalid image entry kind: %s", e.Path)
		}
		seen[e.Path] = e.Kind
	}
	return nil
}

func ownedPrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("runtime cache must be an owned private directory: %s", path)
	}
	return nil
}

func Verify(root string, m Manifest) error {
	if err := validate(m); err != nil {
		return err
	}
	actual, err := Generate(root)
	if err != nil {
		return err
	}
	if len(actual.Entries) != len(m.Entries) {
		return fmt.Errorf("runtime cache entry count changed")
	}
	for i, expected := range m.Entries {
		if actual.Entries[i] != expected {
			return fmt.Errorf("runtime cache mismatch: %s", expected.Path)
		}
		// Generate normalizes permissions, so inspect the actual mode as well.
		info, err := os.Lstat(filepath.Join(root, expected.Path))
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("runtime cache ownership/permissions changed: %s", expected.Path)
		}
	}
	return nil
}

// Prepare never reuses another image's /usr. Every file and symlink is checked
// against the manifest read from this image before a cached tree is mounted.
// A corrupt cache fails closed and is retained for diagnosis.
func Prepare(source, cacheRoot string, data []byte) (string, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	if err := validate(m); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheRoot, 0700); err != nil {
		return "", err
	}
	if err := ownedPrivateDirectory(cacheRoot); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	destination := filepath.Join(cacheRoot, hex.EncodeToString(sum[:]))
	if _, err := os.Lstat(destination); err == nil {
		if err := ownedPrivateDirectory(destination); err != nil {
			return "", err
		}
		if err := Verify(filepath.Join(destination, "usr"), m); err != nil {
			return "", err
		}
		return filepath.Join(destination, "usr"), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	temp, err := os.MkdirTemp(cacheRoot, ".building-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp) // only this invocation's newly-created staging tree
	root := filepath.Join(temp, "usr")
	for _, e := range m.Entries {
		target := filepath.Join(root, e.Path)
		switch e.Kind {
		case "dir":
			err = os.Mkdir(target, os.FileMode(e.Mode))
			if err == nil {
				// Preserve the image mode even when the container has a strict umask.
				err = os.Chmod(target, os.FileMode(e.Mode))
			}
		case "link":
			err = os.Symlink(e.Link, target)
		case "file":
			err = copyFile(filepath.Join(source, e.Path), target, os.FileMode(e.Mode))
		}
		if err != nil {
			return "", fmt.Errorf("copy runtime image %s: %w", e.Path, err)
		}
	}
	if err := Verify(root, m); err != nil {
		return "", err
	}
	if err := os.Rename(temp, destination); err != nil {
		return "", err
	}
	return filepath.Join(destination, "usr"), nil
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
