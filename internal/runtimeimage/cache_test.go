package runtimeimage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func fixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"bin", "share", "share/opensurge"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin/engine"), []byte("version one"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("engine", filepath.Join(root, "bin/alias")); err != nil {
		t.Fatal(err)
	}
	m, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return root, data
}

func TestPreparePreservesImageModesWithStrictUmask(t *testing.T) {
	source, data := fixture(t)
	cache := filepath.Join(t.TempDir(), "cache")
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	root, err := Prepare(source, cache, data)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "bin/engine"))
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("image executable mode changed: info=%v err=%v", info, err)
	}
}

func TestPrepareVerifiesAndReusesExactImage(t *testing.T) {
	source, data := fixture(t)
	cache := filepath.Join(t.TempDir(), "cache")
	root, err := Prepare(source, cache, data)
	if err != nil {
		t.Fatal(err)
	}
	if rootAgain, err := Prepare(source, cache, data); err != nil || rootAgain != root {
		t.Fatalf("reuse = %s %v", rootAgain, err)
	}
	link, err := os.Readlink(filepath.Join(root, "bin/alias"))
	if err != nil || link != "engine" {
		t.Fatalf("copied link = %s %v", link, err)
	}
}

func TestImageUpgradeCannotReuseOldExecutable(t *testing.T) {
	source, data := fixture(t)
	cache := filepath.Join(t.TempDir(), "cache")
	old, err := Prepare(source, cache, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin/engine"), []byte("version two"), 0755); err != nil {
		t.Fatal(err)
	}
	m, err := Generate(source)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(m)
	current, err := Prepare(source, cache, data)
	if err != nil || old == current {
		t.Fatalf("upgrade path = %s %v", current, err)
	}
	oldBytes, _ := os.ReadFile(filepath.Join(old, "bin/engine"))
	newBytes, _ := os.ReadFile(filepath.Join(current, "bin/engine"))
	if string(oldBytes) != "version one" || string(newBytes) != "version two" {
		t.Fatal("image versions mixed")
	}
}

func TestCorruptOrInjectedCacheFailsClosed(t *testing.T) {
	for _, mutation := range []string{"binary", "symlink", "extra", "permissions"} {
		t.Run(mutation, func(t *testing.T) {
			source, data := fixture(t)
			cache := filepath.Join(t.TempDir(), "cache")
			root, err := Prepare(source, cache, data)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "binary":
				err = os.WriteFile(filepath.Join(root, "bin/engine"), []byte("tampered"), 0755)
			case "symlink":
				err = os.Remove(filepath.Join(root, "bin/alias"))
				if err == nil {
					err = os.Symlink("/tmp/other", filepath.Join(root, "bin/alias"))
				}
			case "extra":
				err = os.WriteFile(filepath.Join(root, "bin/other"), []byte("injected"), 0755)
			case "permissions":
				err = os.Chmod(filepath.Join(root, "bin/engine"), 0777)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(source, cache, data); err == nil {
				t.Fatal("corrupt cache was reused")
			}
		})
	}
}

func TestCacheCannotFollowUntrustedDirectoryLink(t *testing.T) {
	source, data := fixture(t)
	outside := t.TempDir()
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(outside, cache); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(source, cache, data); err == nil {
		t.Fatal("followed cache directory symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside cache")
	}
}

func TestManifestRejectsTraversalAndSymlinkParents(t *testing.T) {
	for _, entries := range [][]Entry{
		{{Path: ".", Kind: "dir", Mode: 0755}, {Path: "../outside", Kind: "dir", Mode: 0755}},
		{{Path: ".", Kind: "dir", Mode: 0755}, {Path: "alias", Kind: "link", Link: "/tmp"}, {Path: "alias/outside", Kind: "dir", Mode: 0755}},
	} {
		if err := validate(Manifest{Version: 1, Entries: entries}); err == nil {
			t.Fatal("accepted unsafe manifest")
		}
	}
}
