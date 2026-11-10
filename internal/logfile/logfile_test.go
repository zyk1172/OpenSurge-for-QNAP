package logfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadTailBoundsSparseFileAndDiscardsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(8 << 30); err != nil {
		t.Fatal(err)
	}
	content := []byte("partial-token\nfirst\nsecond\n")
	if _, err := f.WriteAt(content, (8<<30)-int64(len(content))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	tail, err := ReadTail(path, 2, 64)
	if err != nil || tail.Size != 8<<30 || !tail.Truncated || !reflect.DeepEqual(tail.Lines, []string{"first", "second"}) {
		t.Fatalf("tail=%+v error=%v", tail, err)
	}
}

func TestReadTailLongLineAndLineLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	os.WriteFile(path, []byte(strings.Repeat("secret", 100)), 0o600)
	tail, err := ReadTail(path, 80, 32)
	if err != nil || len(tail.Lines) != 0 || !tail.Truncated {
		t.Fatalf("partial token leaked: %+v %v", tail, err)
	}
	os.WriteFile(path, []byte("one\ntwo\nthree"), 0o600)
	tail, err = ReadTail(path, 2, 64)
	if err != nil || !tail.Truncated || !reflect.DeepEqual(tail.Lines, []string{"two", "three"}) {
		t.Fatalf("tail=%+v %v", tail, err)
	}
}

func TestRotationKeepsLiveAppendDescriptorAndBoundsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, _ := f.Stat()
	for _, generation := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		if _, err := f.WriteString(generation); err != nil {
			t.Fatal(err)
		}
		if err := Rotate(path, 4, false); err != nil {
			t.Fatal(err)
		}
	}
	current, _ := os.Stat(path)
	if !os.SameFile(before, current) || current.Size() != 0 {
		t.Fatal("rotation replaced active inode")
	}
	f.WriteString("new")
	for name, want := range map[string]string{"x.log": "new", "x-previous.log": "cccc", "x-previous-2.log": "bbbb"} {
		got, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
		if err != nil || string(got) != want {
			t.Fatalf("%s=%q %v", name, got, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 3 {
		t.Fatal("unbounded history")
	}
}

func TestRotationRejectsSymlinksWithoutTruncatingSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	victim := filepath.Join(dir, "other")
	os.WriteFile(victim, []byte("preserve"), 0o600)
	os.Symlink(victim, path)
	if _, err := ReadTail(path, 80, 64); err == nil {
		t.Fatal("read followed symlink")
	}
	if err := Rotate(path, 4, false); err == nil {
		t.Fatal("rotation followed symlink")
	}
	os.Remove(path)
	os.WriteFile(path, []byte("current"), 0o600)
	os.Symlink(victim, filepath.Join(dir, "x-previous.log"))
	if err := Rotate(path, 4, false); err == nil {
		t.Fatal("rotation accepted symlink history")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "current" {
		t.Fatal("source truncated after archive failure")
	}
	got, _ = os.ReadFile(victim)
	if string(got) != "preserve" {
		t.Fatal("unrelated file changed")
	}
}

func TestStartupArchiveRetainsSmallLogAndCapsLargeLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smartdns.log")
	for _, data := range []string{"first", "0123456789"} {
		os.WriteFile(path, []byte(data), 0o600)
		if err := Rotate(path, 5, true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("startup did not archive stopped log")
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "smartdns-previous.log"))
	if string(got) != "56789" {
		t.Fatal(string(got))
	}
	got, _ = os.ReadFile(filepath.Join(dir, "smartdns-previous-2.log"))
	if string(got) != "first" {
		t.Fatal(string(got))
	}
}

func TestStartupCompactionHandlesOverlappingReadWriteRanges(t *testing.T) {
	for _, extra := range []int{1, 100_001} {
		path := filepath.Join(t.TempDir(), "x.log")
		data := strings.Repeat("abcdefg", 40_000)[:100_000+extra]
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Rotate(path, 100_000, true); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(filepath.Dir(path), "x-previous.log"))
		if err != nil || string(got) != data[extra:] {
			t.Fatalf("overlap compaction failed with extra=%d: %v", extra, err)
		}
	}
}
