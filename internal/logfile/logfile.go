// Package logfile bounds diagnostic reads and manages the gateway's own logs.
package logfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxSize   = 10 << 20
	ReadLimit = 256 << 10
)

type Tail struct {
	Lines     []string
	Size      int64
	Truncated bool
}

// ReadTail reads at most maxBytes, even when the log is several GB. Discard a
// partial leading line so neither parsers nor secret redaction see half a token.
func ReadTail(path string, limit int, maxBytes int64) (Tail, error) {
	result := Tail{Lines: []string{}}
	if limit <= 0 || maxBytes <= 0 {
		return result, nil
	}
	f, err := openRegular(path, os.O_RDONLY)
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	result.Size = info.Size()
	start := max(int64(0), result.Size-maxBytes)
	data, err := io.ReadAll(io.NewSectionReader(f, start, result.Size-start))
	if err != nil {
		return result, err
	}
	result.Truncated = start > 0
	if start > 0 {
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			data = data[index+1:]
		} else {
			data = nil
		}
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return result, nil
	}
	result.Lines = strings.Split(text, "\n")
	if len(result.Lines) > limit {
		result.Truncated = true
		result.Lines = result.Lines[len(result.Lines)-limit:]
	}
	return result, nil
}

// openRegular refuses symlinks and detects replacement between Lstat and Open.
func openRegular(path string, flags int) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("log is not a regular file: %s", path)
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("log changed while opening: %s", path)
	}
	return f, nil
}

func previousPaths(path string) (string, string) {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + "-previous" + ext, base + "-previous-2" + ext
}

// Rotate uses copytruncate for live O_APPEND writers: their existing file
// descriptor/inode stays valid. Writes between the snapshot and truncate can
// be lost; this is diagnostic retention, never a business-data journal.
// Startup rotation runs only after the old writer has stopped.
func Rotate(path string, maxBytes int64, startup bool) error {
	if maxBytes <= 0 {
		return errors.New("log size must be positive")
	}
	f, err := openRegular(path, os.O_RDWR)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 || (!startup && info.Size() < maxBytes) {
		return nil
	}
	previous, older := previousPaths(path)
	for _, name := range []string{previous, older} {
		if i, err := os.Lstat(name); err == nil && !i.Mode().IsRegular() {
			return fmt.Errorf("log history is not a regular file: %s", name)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if startup {
		// With the writer stopped, rename rather than allocate another full log.
		// In-place compaction of an oversized archive keeps the newest bytes and
		// lets startup work even when there is insufficient room for a full copy.
		if err := os.Rename(previous, older); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			return fmt.Errorf("log replaced during startup archive: %s", path)
		}
		if err := os.Rename(path, previous); err != nil {
			return err
		}
		if info.Size() > maxBytes {
			if _, err := io.Copy(io.NewOffsetWriter(f, 0), io.NewSectionReader(f, info.Size()-maxBytes, maxBytes)); err != nil {
				return err
			}
			if err := f.Truncate(maxBytes); err != nil {
				return err
			}
		}
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".log-rotation-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o640); err != nil {
		return err
	}
	start := max(int64(0), info.Size()-maxBytes)
	if _, err := io.Copy(tmp, io.NewSectionReader(f, start, info.Size()-start)); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(previous, older); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp.Name(), previous); err != nil {
		return err
	}
	// Never truncate a replacement path or a file we did not open.
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("log replaced during rotation: %s", path)
	}
	return f.Truncate(0)
}
