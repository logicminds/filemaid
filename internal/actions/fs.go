// Package actions applies classification decisions to the filesystem.
package actions

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

// tagColors is the ordered Finder tag color palette used when writing tags.
// Color numbers follow the macOS convention: 1 grey, 2 green, 3 purple,
// 4 blue, 5 yellow, 6 red, 7 orange. 0 means "no color" and is not used here.
var tagColors = []int{6, 4, 2, 5, 7, 3, 1}

// tagColor returns a deterministic color index for a tag name by hashing the
// name and cycling through the configured palette. The result is stable for a
// given tag, so the same tag always renders with the same color.
func tagColor(tag string) int {
	if tag == "filemaid" {
		return 6 // red for the reserved filemaid tag
	}
	sum := 0
	for i := 0; i < len(tag); i++ {
		sum += int(tag[i])
	}
	return tagColors[sum%len(tagColors)]
}

// FS abstracts filesystem and subprocess operations so tests can inject
// deterministic behavior.
type FS interface {
	Move(src, dest string) error
	MkdirAll(path string) error
	Exists(path string) bool
	SetTags(path string, tags []string)
	SetFinderComment(path string, comment string)
	Trash(path string) error
	// FlushMDImport flushes any deferred mdimport calls. It is optional for
	// test implementations; OSFS batches per-directory re-indexes and flushes
	// them here.
	FlushMDImport()
}

// OSFS is the production macOS implementation of FS.
type OSFS struct {
	batcher *mdimportBatcher
}

// NewOSFS returns a new OSFS instance.
func NewOSFS() *OSFS {
	return &OSFS{
		batcher: newMDImportBatcher(func(dir string) error { return run("mdimport", dir) }),
	}
}

// newOSFSWithBatcher returns an OSFS that calls run for each directory flush.
// It is intended for tests that need to observe mdimport batching.
func newOSFSWithBatcher(run func(dir string) error) *OSFS {
	return &OSFS{batcher: newMDImportBatcher(run)}
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

// isCrossDeviceError reports whether err is a cross-device link/rename error.
func isCrossDeviceError(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

// copyTree recursively copies src to dest. Both files and directories are
// recreated; symlinks are recreated pointing to the original target. Permissions
// are preserved. It is used as a fallback when os.Rename cannot move a directory
// across devices.
func copyTree(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if !srcInfo.IsDir() {
		return copyFile(src, dest, srcInfo.Mode())
	}

	if err := os.MkdirAll(dest, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}
	if err := os.Chmod(dest, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod dest dir: %w", err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		destPath := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if d.IsDir() {
			if err := os.Mkdir(destPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("mkdir %s: %w", destPath, err)
			}
			return os.Chmod(destPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			return os.Symlink(target, destPath)
		}
		return copyFile(path, destPath, info.Mode())
	})
}

// copyFile copies a regular file and preserves its permissions.
func copyFile(src, dest string, mode os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer sf.Close()

	df, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
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
	if err := os.Chmod(dest, mode.Perm()); err != nil {
		return fmt.Errorf("chmod dest: %w", err)
	}
	return nil
}

// removeTree removes a directory tree. It is a thin wrapper around
// os.RemoveAll so the cross-device fallback in ApplyDirectory is explicit.
func removeTree(src string) error {
	return os.RemoveAll(src)
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

// SetTags writes Finder tags via xattr and defers the per-directory mdimport
// re-index. Errors are logged but do not abort processing so a single xattr
// failure does not prevent the file move.
func (o *OSFS) SetTags(path string, tags []string) {
	if len(tags) == 0 {
		return
	}
	existing, err := readFinderTags(path)
	if err != nil {
		slog.Warn("failed to read existing Finder tags; writing new tags only", "path", path, "error", err)
		existing = nil
	}
	merged := mergeTags(existing, tags)
	plist := encodeStringArrayPlist(merged)
	hex := fmt.Sprintf("%x", plist)
	if err := run("xattr", "-w", "-x", "com.apple.metadata:_kMDItemUserTags", hex, path); err != nil {
		slog.Warn("failed to write Finder tags", "path", path, "error", err)
	}
	o.batcher.Add(filepath.Dir(path))
}

// SetFinderComment writes the LLM reason as a Finder comment via xattr and
// defers the per-directory mdimport re-index. Errors are logged but do not
// abort processing.
func (o *OSFS) SetFinderComment(path string, comment string) {
	if comment == "" {
		return
	}
	existing, err := readFinderComment(path)
	if err != nil {
		slog.Warn("failed to read existing Finder comment; writing new comment only", "path", path, "error", err)
		existing = ""
	}
	merged := mergeFinderComment(existing, comment)
	plist := encodeStringPlist(merged)
	hex := fmt.Sprintf("%x", plist)
	if err := run("xattr", "-w", "-x", "com.apple.metadata:kMDItemFinderComment", hex, path); err != nil {
		slog.Warn("failed to write Finder comment", "path", path, "error", err)
	}
	o.batcher.Add(filepath.Dir(path))
}

// FlushMDImport flushes any deferred mdimport calls.
func (o *OSFS) FlushMDImport() {
	o.batcher.Flush()
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

// mdimportBatcher collects directories that need a Spotlight re-index and
// flushes them with a single mdimport invocation per directory.
// A generation counter prevents a timer callback from processing directories
// that Flush() has already handled.
type mdimportBatcher struct {
	mu      sync.Mutex
	dirs    map[string]bool
	timer   *time.Timer
	delay   time.Duration
	run     func(dir string) error
	version int
}

func newMDImportBatcher(run func(dir string) error) *mdimportBatcher {
	return &mdimportBatcher{
		dirs:  make(map[string]bool),
		delay: 100 * time.Millisecond,
		run:   run,
	}
}

// Add registers a directory for a deferred mdimport re-index.
func (b *mdimportBatcher) Add(dir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirs[dir] = true
	if b.timer == nil {
		version := b.version
		b.timer = time.AfterFunc(b.delay, func() { b.flush(version) })
	}
}

// Flush immediately runs mdimport for every queued directory.
func (b *mdimportBatcher) Flush() {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	dirs := make([]string, 0, len(b.dirs))
	for dir := range b.dirs {
		dirs = append(dirs, dir)
	}
	b.dirs = make(map[string]bool)
	b.version++
	b.mu.Unlock()
	for _, dir := range dirs {
		_ = b.run(dir)
	}
}

func (b *mdimportBatcher) flush(version int) {
	b.mu.Lock()
	// If the timer was stopped by Flush() or a newer generation has already
	// handled these directories, do nothing.
	if b.timer == nil || version != b.version {
		b.mu.Unlock()
		return
	}
	dirs := make([]string, 0, len(b.dirs))
	for dir := range b.dirs {
		dirs = append(dirs, dir)
	}
	b.dirs = make(map[string]bool)
	b.timer = nil
	b.mu.Unlock()
	for _, dir := range dirs {
		_ = b.run(dir)
	}
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

	// Objects 1..N: the strings. Finder tags expect each name to be followed
	// by a newline and a colour index (0 = no colour).
	for _, s := range items {
		offsets = append(offsets, buf.Len())
		data := []byte(fmt.Sprintf("%s\n%d", s, tagColor(s)))
		if len(data) < 15 {
			buf.WriteByte(0x50 | byte(len(data)))
		} else {
			buf.WriteByte(0x5F)
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

// encodeStringPlist emits a minimal binary plist containing a single string.
// The string is encoded as UTF-16BE, which is the standard representation for
// bplist strings and is what macOS expects for Finder comments stored in
func encodeStringPlist(s string) []byte {
	buf := bytes.NewBufferString("bplist00")
	// Encode as UTF-16BE to match the bplist string format used by macOS.
	utf16CodeUnits := utf16.Encode([]rune(s))
	data := make([]byte, 0, len(utf16CodeUnits)*2)
	for _, r := range utf16CodeUnits {
		data = append(data, byte(r>>8), byte(r))
	}
	offset := buf.Len()
	if len(utf16CodeUnits) < 15 {
		buf.WriteByte(0x60 | byte(len(utf16CodeUnits)))
	} else {
		buf.WriteByte(0x6F)
		writeInt(buf, int64(len(utf16CodeUnits)))
	}
	buf.Write(data)

	offsetTableOffset := buf.Len()
	buf.WriteByte(byte(offset))

	// Trailer: 5 unused bytes, sort version, offset int size, object ref size,
	// number of objects, top object index, offset table offset.
	buf.Write(make([]byte, 5))
	buf.WriteByte(0)    // sort version
	buf.WriteByte(1)    // offset int size
	buf.WriteByte(1)    // object ref size
	writeUint64(buf, 1) // number of objects
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

// readFinderTags returns the existing Finder tags stored on path.
func readFinderTags(path string) ([]string, error) {
	out, err := exec.Command("xattr", "-p", "-x", "com.apple.metadata:_kMDItemUserTags", path).Output()
	if err != nil {
		return nil, err
	}
	hexStr := strings.Join(strings.Fields(string(out)), "")
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, err
	}
	return parseStringArrayPlist(data)
}

// readFinderComment returns the existing Finder comment stored on path.
func readFinderComment(path string) (string, error) {
	out, err := exec.Command("xattr", "-p", "-x", "com.apple.metadata:kMDItemFinderComment", path).Output()
	if err != nil {
		return "", err
	}
	hexStr := strings.Join(strings.Fields(string(out)), "")
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	return parseStringPlist(data)
}

// parseStringArrayPlist parses a minimal binary plist containing a single
// array of strings. Finder tags use data strings with a trailing "\n<color>"
// suffix, which is stripped from the returned values.
func parseStringArrayPlist(data []byte) ([]string, error) {
	if len(data) < 32 || string(data[:8]) != "bplist00" {
		return nil, fmt.Errorf("invalid bplist header")
	}

	trailer := data[len(data)-32:]
	offsetIntSize := int(trailer[6])
	objectRefSize := int(trailer[7])
	numObjects := int(binary.BigEndian.Uint64(trailer[8:16]))
	topObject := int(binary.BigEndian.Uint64(trailer[16:24]))
	offsetTableOffset := int(binary.BigEndian.Uint64(trailer[24:32]))

	if offsetIntSize != 1 || objectRefSize != 1 {
		return nil, fmt.Errorf("unsupported offset/ref size: %d/%d", offsetIntSize, objectRefSize)
	}
	if numObjects < 1 || topObject >= numObjects {
		return nil, fmt.Errorf("invalid object count or top object")
	}

	offsetTable := make([]int, numObjects)
	for i := 0; i < numObjects; i++ {
		off := offsetTableOffset + i*offsetIntSize
		if off < 0 || off >= len(data) {
			return nil, fmt.Errorf("offset table entry %d out of range", i)
		}
		offsetTable[i] = int(data[off])
	}

	var parseObject func(int) ([]string, error)
	parseObject = func(idx int) ([]string, error) {
		if idx < 0 || idx >= numObjects {
			return nil, fmt.Errorf("object index out of range: %d", idx)
		}
		off := offsetTable[idx]
		if off < 0 || off >= len(data) {
			return nil, fmt.Errorf("object offset out of range: %d", off)
		}
		marker := data[off]
		switch {
		case marker&0xF0 == 0xA0:
			count, start, err := readCount(data, off, marker&0x0F)
			if err != nil {
				return nil, err
			}
			var result []string
			for i := 0; i < count; i++ {
				refOff := start + i*objectRefSize
				if refOff >= len(data) {
					return nil, fmt.Errorf("array ref %d out of range", i)
				}
				ref := int(data[refOff])
				items, err := parseObject(ref)
				if err != nil {
					return nil, err
				}
				result = append(result, items...)
			}
			return result, nil
		case marker&0xF0 == 0x50:
			length, start, err := readCount(data, off, marker&0x0F)
			if err != nil {
				return nil, err
			}
			end := start + length
			if end > len(data) {
				return nil, fmt.Errorf("data string extends past data")
			}
			s := string(data[start:end])
			if i := strings.LastIndex(s, "\n"); i >= 0 {
				s = s[:i]
			}
			return []string{s}, nil
		case marker&0xF0 == 0x60:
			length, start, err := readCount(data, off, marker&0x0F)
			if err != nil {
				return nil, err
			}
			end := start + length*2
			if end > len(data) {
				return nil, fmt.Errorf("unicode string extends past data")
			}
			runes := make([]uint16, length)
			for i := 0; i < length; i++ {
				runes[i] = binary.BigEndian.Uint16(data[start+i*2 : start+(i+1)*2])
			}
			return []string{string(utf16.Decode(runes))}, nil
		default:
			return nil, fmt.Errorf("unexpected object marker: 0x%02X", marker)
		}
	}

	return parseObject(topObject)
}

// parseStringPlist parses a minimal binary plist containing a single Unicode
// string. It is sufficient for the Finder comment xattr format.
func parseStringPlist(data []byte) (string, error) {
	items, err := parseStringArrayPlist(data)
	if err != nil {
		return "", err
	}
	if len(items) != 1 {
		return "", fmt.Errorf("expected 1 object, got %d", len(items))
	}
	return items[0], nil
}

// readCount reads the count and data start offset for a bplist object marker.
func readCount(data []byte, off int, low byte) (count int, start int, err error) {
	if low != 0x0F {
		return int(low), off + 1, nil
	}
	if off+1 >= len(data) {
		return 0, 0, fmt.Errorf("extended count marker truncated")
	}
	switch data[off+1] {
	case 0x10:
		if off+3 >= len(data) {
			return 0, 0, fmt.Errorf("extended count truncated")
		}
		return int(data[off+2]), off + 3, nil
	case 0x11:
		if off+4 >= len(data) {
			return 0, 0, fmt.Errorf("extended count truncated")
		}
		return int(binary.BigEndian.Uint16(data[off+2 : off+4])), off + 4, nil
	case 0x12:
		if off+6 >= len(data) {
			return 0, 0, fmt.Errorf("extended count truncated")
		}
		return int(binary.BigEndian.Uint32(data[off+2 : off+6])), off + 6, nil
	case 0x13:
		if off+10 >= len(data) {
			return 0, 0, fmt.Errorf("extended count truncated")
		}
		return int(binary.BigEndian.Uint64(data[off+2 : off+10])), off + 10, nil
	default:
		return 0, 0, fmt.Errorf("unsupported extended count marker: 0x%02X", data[off+1])
	}
}

// mergeTags merges new tags into existing tags with case-insensitive
// deduplication. When a new tag collides case-insensitively with an existing
// tag, the new tag's casing replaces the existing one. Existing tags are kept
// in order, followed by genuinely new tags.
func mergeTags(existing, newTags []string) []string {
	seen := make(map[string]string, len(existing)+len(newTags))
	var result []string

	add := func(tag string) {
		lower := strings.ToLower(tag)
		if prev, ok := seen[lower]; ok {
			if prev == tag {
				return
			}
			for i := range result {
				if strings.ToLower(result[i]) == lower {
					result[i] = tag
					break
				}
			}
			seen[lower] = tag
			return
		}
		seen[lower] = tag
		result = append(result, tag)
	}

	for _, tag := range existing {
		add(tag)
	}
	for _, tag := range newTags {
		add(tag)
	}
	return result
}

// mergeFinderComment appends reason to an existing Finder comment, separated
// by "; ". It avoids exact duplicates and skips appending when reason is
// already contained case-insensitively in existing.
func mergeFinderComment(existing, reason string) string {
	if existing == "" {
		return reason
	}
	lowerExisting := strings.ToLower(existing)
	lowerReason := strings.ToLower(reason)
	if strings.EqualFold(existing, reason) || strings.Contains(lowerExisting, lowerReason) {
		return existing
	}
	return existing + "; " + reason
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
	Comments    []CommentRecord
	Trashed     []string
}

// TagRecord captures a SetTags call.
type TagRecord struct {
	Path string
	Tags []string
}

// CommentRecord captures a SetFinderComment call.
type CommentRecord struct {
	Path    string
	Comment string
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

// SetFinderComment records the comment operation without shelling out.
func (r *RecordingFS) SetFinderComment(path string, comment string) {
	if comment == "" {
		return
	}
	r.mu.Lock()
	r.Comments = append(r.Comments, CommentRecord{Path: path, Comment: comment})
	r.mu.Unlock()
}

// FlushMDImport is a no-op for RecordingFS.
func (r *RecordingFS) FlushMDImport() {}

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
