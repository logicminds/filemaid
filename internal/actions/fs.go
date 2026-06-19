// Package actions applies classification decisions to the filesystem.
package actions

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// FS abstracts filesystem and subprocess operations so tests can inject
// deterministic behavior.
type FS interface {
	Move(src, dest string) error
	MkdirAll(path string) error
	Exists(path string) bool
	SetTags(path string, tags []string)
	Trash(path string) error
}

// OSFS is the production macOS implementation of FS.
type OSFS struct{}

// NewOSFS returns a new OSFS instance.
func NewOSFS() *OSFS {
	return &OSFS{}
}

// Move attempts an os.Rename and falls back to copy + remove for cross-device
// moves.
func (o *OSFS) Move(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}

	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer sf.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}
	df, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	if _, err := io.Copy(df, sf); err != nil {
		df.Close()
		os.Remove(dest)
		return fmt.Errorf("copy: %w", err)
	}
	if err := df.Close(); err != nil {
		return fmt.Errorf("close dest: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source: %w", err)
	}
	return nil
}

// MkdirAll creates path and any missing parents.
func (o *OSFS) MkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

// Exists reports whether path exists.
func (o *OSFS) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SetTags writes Finder tags via xattr and re-indexes with mdimport. Errors are
// intentionally ignored to match the Python implementation.
func (o *OSFS) SetTags(path string, tags []string) {
	if len(tags) == 0 {
		return
	}
	plist := encodeStringArrayPlist(tags)
	hex := fmt.Sprintf("%x", plist)
	_ = run("xattr", "-w", "-x", "com.apple.metadata:_kMDItemUserTags", hex, path)
	_ = run("mdimport", path)
}

// Trash sends path to the Finder trash via osascript. Errors are ignored to
// match the Python implementation.
func (o *OSFS) Trash(path string) error {
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file %q`, path)
	return run("osascript", "-e", script)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// encodeStringArrayPlist emits a minimal binary plist containing a single
// array of UTF-8 strings. It is sufficient for Finder user tags.
func encodeStringArrayPlist(items []string) []byte {
	buf := bytes.NewBufferString("bplist00")
	offsets := make([]int, 0, len(items)+1)

	// Object 0: the array.
	offsets = append(offsets, buf.Len())
	if len(items) < 15 {
		buf.WriteByte(0xA0 | byte(len(items)))
	} else {
		buf.WriteByte(0xAF)
		writeInt(buf, int64(len(items)))
	}
	for i := 0; i < len(items); i++ {
		buf.WriteByte(byte(i + 1)) // object refs are 1 byte.
	}

	// Objects 1..N: the strings.
	for _, s := range items {
		offsets = append(offsets, buf.Len())
		data := []byte(s)
		if len(data) < 15 {
			buf.WriteByte(0x60 | byte(len(data)))
		} else {
			buf.WriteByte(0x6F)
			writeInt(buf, int64(len(data)))
		}
		buf.Write(data)
	}

	offsetTableOffset := buf.Len()
	for _, off := range offsets {
		buf.WriteByte(byte(off))
	}

	// Trailer: 5 unused bytes, sort version, offset int size, object ref size,
	// number of objects, top object index, offset table offset.
	buf.Write(make([]byte, 5))
	buf.WriteByte(0) // sort version
	buf.WriteByte(1) // offset int size
	buf.WriteByte(1) // object ref size
	writeUint64(buf, uint64(len(offsets)))
	writeUint64(buf, 0) // top object index
	writeUint64(buf, uint64(offsetTableOffset))

	return buf.Bytes()
}

func writeInt(buf *bytes.Buffer, n int64) {
	switch {
	case n >= 0 && n <= 0xFF:
		buf.WriteByte(0x10)
		buf.WriteByte(byte(n))
	case n <= 0xFFFF:
		buf.WriteByte(0x11)
		binary.Write(buf, binary.BigEndian, uint16(n))
	case n <= 0xFFFFFFFF:
		buf.WriteByte(0x12)
		binary.Write(buf, binary.BigEndian, uint32(n))
	default:
		buf.WriteByte(0x13)
		binary.Write(buf, binary.BigEndian, n)
	}
}

func writeUint64(buf *bytes.Buffer, n uint64) {
	binary.Write(buf, binary.BigEndian, n)
}

// RecordingFS records every operation performed against it. It performs real
// filesystem moves and directory creation so integration-style tests observe
// actual path changes, but SetTags and Trash are recorded only.
type RecordingFS struct {
	mu          sync.Mutex
	Moved       [][2]string
	Mkdirs      []string
	ExistsCalls []string
	Tags        []TagRecord
	Trashed     []string
}

// TagRecord captures a SetTags call.
type TagRecord struct {
	Path string
	Tags []string
}

// NewRecordingFS returns an empty RecordingFS.
func NewRecordingFS() *RecordingFS {
	return &RecordingFS{}
}

// Move records and performs an os.Rename.
func (r *RecordingFS) Move(src, dest string) error {
	r.mu.Lock()
	r.Moved = append(r.Moved, [2]string{src, dest})
	r.mu.Unlock()
	return os.Rename(src, dest)
}

// MkdirAll records and performs os.MkdirAll.
func (r *RecordingFS) MkdirAll(path string) error {
	r.mu.Lock()
	r.Mkdirs = append(r.Mkdirs, path)
	r.mu.Unlock()
	return os.MkdirAll(path, 0o755)
}

// Exists records and reports whether path exists.
func (r *RecordingFS) Exists(path string) bool {
	r.mu.Lock()
	r.ExistsCalls = append(r.ExistsCalls, path)
	r.mu.Unlock()
	_, err := os.Stat(path)
	return err == nil
}

// SetTags records the tag operation without shelling out.
func (r *RecordingFS) SetTags(path string, tags []string) {
	r.mu.Lock()
	cp := make([]string, len(tags))
	copy(cp, tags)
	r.Tags = append(r.Tags, TagRecord{Path: path, Tags: cp})
	r.mu.Unlock()
}

// Trash records the trash operation without moving the file.
func (r *RecordingFS) Trash(path string) error {
	r.mu.Lock()
	r.Trashed = append(r.Trashed, path)
	r.mu.Unlock()
	return nil
}

// failingFS wraps another FS and fails on selected operations.
type failingFS struct {
	FS
	failMove  bool
	failTrash bool
}

func (f *failingFS) Move(src, dest string) error {
	if f.failMove {
		return fmt.Errorf("injected move failure")
	}
	return f.FS.Move(src, dest)
}

func (f *failingFS) Trash(path string) error {
	if f.failTrash {
		return fmt.Errorf("injected trash failure")
	}
	return f.FS.Trash(path)
}
