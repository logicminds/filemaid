// Package fingerprint computes perceptual and structural fingerprints for files.
// These fingerprints support duplicate and near-duplicate detection for the
// rename feature.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/corona10/goimagehash"
	"github.com/logicminds/filemaid/internal/config"
)

const (
	headSize        = 4096
	tailSize        = 4096
	textSnippetSize = 8192
)

// MediaFingerprint holds type-specific signatures used to compare files.
type MediaFingerprint struct {
	MediaKind      string            `json:"media_kind"`
	Size           int64             `json:"size"`
	PerceptualHash string            `json:"perceptual_hash"`
	AVSignature    string            `json:"av_signature"`
	TextSignature  string            `json:"text_signature"`
	HeadHash       string            `json:"head_hash"`
	TailHash       string            `json:"tail_hash"`
	HashAlgorithm  string            `json:"hash_algorithm"`
	Metadata       map[string]string `json:"metadata"`
}

// ComputeFingerprint returns a MediaFingerprint for path. The kind of
// fingerprint depends on the file extension and, for audio/video, on whether
// cfg.RenameUseFFmpeg is enabled and ffmpeg is available.
func ComputeFingerprint(path string, cfg *config.Config) (MediaFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return MediaFingerprint{}, err
	}

	kind := mediaKind(path)
	switch kind {
	case "image":
		fp, err := imageFingerprint(path, info)
		if err == nil {
			return fp, nil
		}
		// Decoding failed; treat as binary.
		return binaryFingerprint(path, info)
	case "audio", "video":
		return avFingerprint(path, info, cfg)
	case "text", "pdf":
		return textFingerprint(path, info, kind)
	default:
		return binaryFingerprint(path, info)
	}
}

// CompareFiles returns a similarity score between 0 and 1 for two fingerprints.
func CompareFiles(a, b MediaFingerprint) float64 {
	if a.MediaKind != b.MediaKind {
		return 0
	}

	switch a.MediaKind {
	case "image":
		return compareImage(a, b)
	case "audio", "video":
		return compareAV(a, b)
	case "text", "pdf":
		return compareText(a, b)
	default:
		return compareBinary(a, b)
	}
}

// ValidateFFmpeg returns nil when ffmpeg is available on PATH. Otherwise it
// returns an error that includes installation help and a performance warning.
func ValidateFFmpeg() error {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return nil
	}
	return errors.New(
		"ffmpeg not found in PATH. Install it for accurate audio/video fingerprints " +
			"(e.g., 'brew install ffmpeg' on macOS). " +
			"Without ffmpeg, audio/video deduplication falls back to size + head/tail hashing, " +
			"which is slower and less accurate.")
}

func mediaKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif":
		return "image"
	case ".mp3", ".wav", ".aac", ".flac", ".m4a", ".ogg", ".wma":
		return "audio"
	case ".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".webm", ".m4v":
		return "video"
	case ".txt", ".md", ".go", ".py", ".js", ".ts", ".html", ".htm",
		".json", ".xml", ".csv", ".log", ".rs", ".c", ".cc", ".cpp", ".h":
		return "text"
	case ".pdf":
		return "pdf"
	default:
		return "binary"
	}
}

func imageFingerprint(path string, info os.FileInfo) (MediaFingerprint, error) {
	f, err := os.Open(path)
	if err != nil {
		return MediaFingerprint{}, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return MediaFingerprint{}, err
	}

	hash, err := goimagehash.PerceptionHash(img)
	if err != nil {
		return MediaFingerprint{}, err
	}

	return MediaFingerprint{
		MediaKind:      "image",
		Size:           info.Size(),
		PerceptualHash: hash.ToString(),
		HashAlgorithm:  "goimagehash-perception",
	}, nil
}

func avFingerprint(path string, info os.FileInfo, cfg *config.Config) (MediaFingerprint, error) {
	kind := mediaKind(path)
	fp := MediaFingerprint{
		MediaKind:     kind,
		Size:          info.Size(),
		HashAlgorithm: "sha256",
	}

	head, tail, err := headTailHash(path, info, headSize, tailSize)
	if err == nil {
		fp.HeadHash = head
		fp.TailHash = tail
	}

	if cfg != nil && cfg.RenameUseFFmpeg {
		if ffmpegPath, ok := ffmpegAvailable(cfg); ok {
			sig, meta, err := ffmpegSignature(path, ffmpegPath)
			if err == nil {
				fp.AVSignature = sig
				fp.Metadata = meta
				return fp, nil
			}
		}
	}

	fp.AVSignature = fmt.Sprintf("fallback:%d:%s:%s", info.Size(), fp.HeadHash, fp.TailHash)
	return fp, nil
}

func textFingerprint(path string, info os.FileInfo, kind string) (MediaFingerprint, error) {
	f, err := os.Open(path)
	if err != nil {
		return MediaFingerprint{}, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(textSnippetSize)))
	if err != nil {
		return MediaFingerprint{}, err
	}

	head, tail, err := headTailHash(path, info, headSize, tailSize)
	if err != nil {
		return MediaFingerprint{}, err
	}

	return MediaFingerprint{
		MediaKind:     kind,
		Size:          info.Size(),
		TextSignature: normalizeText(data),
		HeadHash:      head,
		TailHash:      tail,
		HashAlgorithm: "sha256",
	}, nil
}

func binaryFingerprint(path string, info os.FileInfo) (MediaFingerprint, error) {
	head, tail, err := headTailHash(path, info, headSize, tailSize)
	if err != nil {
		return MediaFingerprint{}, err
	}

	return MediaFingerprint{
		MediaKind:     "binary",
		Size:          info.Size(),
		HeadHash:      head,
		TailHash:      tail,
		HashAlgorithm: "sha256",
	}, nil
}

func headTailHash(path string, info os.FileInfo, headSize, tailSize int) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	head := make([]byte, headSize)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", err
	}
	head = head[:n]

	var tail []byte
	size := info.Size()
	if size > int64(tailSize) {
		if _, err := f.Seek(-int64(tailSize), io.SeekEnd); err != nil {
			return "", "", err
		}
		tail = make([]byte, tailSize)
		tn, err := io.ReadFull(f, tail)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", "", err
		}
		tail = tail[:tn]
	} else {
		tail = head
	}

	return hashBytes(head), hashBytes(tail), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func normalizeText(data []byte) string {
	var b strings.Builder
	for _, r := range string(data) {
		switch {
		case r >= 32 && r < 127:
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	if len(s) > textSnippetSize {
		s = s[:textSnippetSize]
	}
	return s
}

func compareImage(a, b MediaFingerprint) float64 {
	if a.PerceptualHash == "" || b.PerceptualHash == "" {
		return 0
	}
	ha, err := goimagehash.ImageHashFromString(a.PerceptualHash)
	if err != nil {
		return 0
	}
	hb, err := goimagehash.ImageHashFromString(b.PerceptualHash)
	if err != nil {
		return 0
	}
	dist, err := ha.Distance(hb)
	if err != nil {
		return 0
	}
	const maxDist = 64
	if dist > maxDist {
		return 0
	}
	return 1.0 - float64(dist)/maxDist
}

func compareAV(a, b MediaFingerprint) float64 {
	if a.AVSignature == "" || b.AVSignature == "" {
		return 0
	}
	if a.AVSignature == b.AVSignature {
		return 1.0
	}
	return 0.0
}

func compareText(a, b MediaFingerprint) float64 {
	if a.TextSignature == "" || b.TextSignature == "" {
		return 0
	}
	if a.TextSignature == b.TextSignature {
		return 1.0
	}
	common := commonPrefixLen(a.TextSignature, b.TextSignature)
	maxLen := len(a.TextSignature)
	if len(b.TextSignature) > maxLen {
		maxLen = len(b.TextSignature)
	}
	if maxLen == 0 {
		return 0
	}
	return float64(common) / float64(maxLen)
}

func compareBinary(a, b MediaFingerprint) float64 {
	if a.Size != b.Size {
		return 0
	}
	if a.HeadHash != "" && a.HeadHash == b.HeadHash {
		return 1.0
	}
	return 0
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func ffmpegAvailable(cfg *config.Config) (string, bool) {
	if cfg != nil && cfg.ExternalTools.FFmpeg != "" {
		if _, err := os.Stat(cfg.ExternalTools.FFmpeg); err == nil {
			return cfg.ExternalTools.FFmpeg, true
		}
		if p, err := exec.LookPath(cfg.ExternalTools.FFmpeg); err == nil {
			return p, true
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, true
	}
	return "", false
}

func ffmpegSignature(path, ffmpegPath string) (string, map[string]string, error) {
	cmd := exec.Command(ffmpegPath, "-i", path, "-f", "ffmetadata", "-")
	var out strings.Builder
	var errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	_ = cmd.Run() // ffmpeg often exits non-zero for ffmetadata; rely on output.

	metadata := parseFFMetadata(out.String())
	duration := parseDuration(errOut.String())
	if duration == "" && len(metadata) == 0 {
		return "", nil, errors.New("ffmpeg produced no usable metadata")
	}

	var b strings.Builder
	b.WriteString("ffmpeg:")
	b.WriteString("duration=")
	b.WriteString(duration)
	b.WriteString(";")
	for _, k := range sortedKeys(metadata) {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(metadata[k])
		b.WriteString(";")
	}
	return b.String(), metadata, nil
}

func parseFFMetadata(s string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if k != "" {
			m[k] = v
		}
	}
	return m
}

func parseDuration(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "Duration: "
		if idx := strings.Index(line, prefix); idx >= 0 {
			rest := line[idx+len(prefix):]
			if end := strings.Index(rest, ","); end >= 0 {
				return strings.TrimSpace(rest[:end])
			}
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
