package fingerprint

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestImagePerceptualHash(t *testing.T) {
	tmp := t.TempDir()
	img1 := createPNG(t, tmp, "img1.png", image.NewRGBA(image.Rect(0, 0, 64, 64)))

	fp, err := ComputeFingerprint(img1, config.Defaults())
	if err != nil {
		t.Fatalf("ComputeFingerprint image: %v", err)
	}
	if fp.MediaKind != "image" {
		t.Errorf("MediaKind = %q, want image", fp.MediaKind)
	}
	if fp.PerceptualHash == "" {
		t.Error("PerceptualHash is empty")
	}
	if fp.HashAlgorithm != "goimagehash-perception" {
		t.Errorf("HashAlgorithm = %q, want goimagehash-perception", fp.HashAlgorithm)
	}
}

func TestImageComparison(t *testing.T) {
	tmp := t.TempDir()

	src := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	img1 := createPNG(t, tmp, "img1.png", src)

	different := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range different.Pix {
		different.Pix[i] = byte((i + 128) % 256)
	}
	img2 := createPNG(t, tmp, "img2.png", different)

	fp1, err := ComputeFingerprint(img1, config.Defaults())
	if err != nil {
		t.Fatalf("ComputeFingerprint img1: %v", err)
	}
	fp2, err := ComputeFingerprint(img2, config.Defaults())
	if err != nil {
		t.Fatalf("ComputeFingerprint img2: %v", err)
	}

	if fp1.PerceptualHash == fp2.PerceptualHash {
		t.Error("different images produced identical perceptual hashes")
	}

	same := CompareFiles(fp1, fp1)
	if same != 1.0 {
		t.Errorf("CompareFiles(identical) = %v, want 1.0", same)
	}

	diff := CompareFiles(fp1, fp2)
	if diff >= 0.99 {
		t.Errorf("CompareFiles(different) = %v, want < 0.99", diff)
	}
	if diff < 0 {
		t.Errorf("CompareFiles(different) = %v, want >= 0", diff)
	}
}

func TestCompareFilesDifferentKinds(t *testing.T) {
	tmp := t.TempDir()
	img := createPNG(t, tmp, "img.png", image.NewRGBA(image.Rect(0, 0, 8, 8)))
	txt := filepath.Join(tmp, "doc.txt")
	if err := os.WriteFile(txt, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	fpImg, err := ComputeFingerprint(img, config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	fpTxt, err := ComputeFingerprint(txt, config.Defaults())
	if err != nil {
		t.Fatal(err)
	}

	if got := CompareFiles(fpImg, fpTxt); got != 0 {
		t.Errorf("CompareFiles(image, text) = %v, want 0", got)
	}
}

func TestValidateFFmpegMissing(t *testing.T) {
	t.Setenv("PATH", "")
	err := ValidateFFmpeg()
	if err == nil {
		t.Fatal("ValidateFFmpeg() = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "brew install ffmpeg") {
		t.Errorf("error missing install help: %q", msg)
	}
	if !strings.Contains(msg, "slower and less accurate") {
		t.Errorf("error missing performance warning: %q", msg)
	}
}

func TestValidateFFmpegPresent(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "ffmpeg")
	var script string
	if runtime.GOOS == "windows" {
		script = "@echo off\n"
	} else {
		script = "#!/bin/sh\n"
	}
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)

	if err := ValidateFFmpeg(); err != nil {
		t.Fatalf("ValidateFFmpeg() = %v, want nil", err)
	}
}

func TestAVFingerprintFallbackWhenFFmpegDisabled(t *testing.T) {
	tmp := t.TempDir()
	video := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(video, []byte("fake video data not valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.RenameUseFFmpeg = false

	fp, err := ComputeFingerprint(video, cfg)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp.MediaKind != "video" {
		t.Errorf("MediaKind = %q, want video", fp.MediaKind)
	}
	if !strings.HasPrefix(fp.AVSignature, "fallback:") {
		t.Errorf("AVSignature = %q, want fallback prefix", fp.AVSignature)
	}
	if fp.HeadHash == "" {
		t.Error("HeadHash is empty")
	}
	if fp.TailHash == "" {
		t.Error("TailHash is empty")
	}
}

func TestAVFingerprintFallbackWhenFFmpegMissing(t *testing.T) {
	tmp := t.TempDir()
	audio := filepath.Join(tmp, "song.mp3")
	if err := os.WriteFile(audio, []byte("fake audio data not valid"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.RenameUseFFmpeg = true
	cfg.ExternalTools.FFmpeg = filepath.Join(tmp, "does-not-exist")
	t.Setenv("PATH", "")

	fp, err := ComputeFingerprint(audio, cfg)
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp.MediaKind != "audio" {
		t.Errorf("MediaKind = %q, want audio", fp.MediaKind)
	}
	if !strings.HasPrefix(fp.AVSignature, "fallback:") {
		t.Errorf("AVSignature = %q, want fallback prefix", fp.AVSignature)
	}
}

func TestTextFingerprint(t *testing.T) {
	tmp := t.TempDir()
	txt := filepath.Join(tmp, "doc.txt")
	content := "  hello   world\nthis is a test file  "
	if err := os.WriteFile(txt, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fp, err := ComputeFingerprint(txt, config.Defaults())
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp.MediaKind != "text" {
		t.Errorf("MediaKind = %q, want text", fp.MediaKind)
	}
	want := "hello world this is a test file"
	if fp.TextSignature != want {
		t.Errorf("TextSignature = %q, want %q", fp.TextSignature, want)
	}
	if fp.HeadHash == "" {
		t.Error("HeadHash is empty")
	}
}

func TestTextComparison(t *testing.T) {
	a := MediaFingerprint{MediaKind: "text", TextSignature: "hello world"}
	b := MediaFingerprint{MediaKind: "text", TextSignature: "hello world"}
	if got := CompareFiles(a, b); got != 1.0 {
		t.Errorf("CompareFiles(identical text) = %v, want 1.0", got)
	}

	c := MediaFingerprint{MediaKind: "text", TextSignature: "hello there"}
	if got := CompareFiles(a, c); got <= 0 {
		t.Errorf("CompareFiles(similar text) = %v, want > 0", got)
	}
}

func TestBinaryFingerprint(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "data.bin")
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}

	fp, err := ComputeFingerprint(bin, config.Defaults())
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp.MediaKind != "binary" {
		t.Errorf("MediaKind = %q, want binary", fp.MediaKind)
	}
	if fp.Size != 4 {
		t.Errorf("Size = %d, want 4", fp.Size)
	}
	if fp.HeadHash == "" {
		t.Error("HeadHash is empty")
	}

	copy := filepath.Join(tmp, "data-copy.bin")
	if err := os.WriteFile(copy, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	fp2, err := ComputeFingerprint(copy, config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if got := CompareFiles(fp, fp2); got != 1.0 {
		t.Errorf("CompareFiles(identical binary) = %v, want 1.0", got)
	}
}

func TestMediaKind(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"photo.png", "image"},
		{"photo.jpg", "image"},
		{"song.mp3", "audio"},
		{"clip.mp4", "video"},
		{"notes.txt", "text"},
		{"report.pdf", "pdf"},
		{"unknown.dat", "binary"},
	}
	for _, tc := range cases {
		if got := mediaKind(tc.path); got != tc.want {
			t.Errorf("mediaKind(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func createPNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
