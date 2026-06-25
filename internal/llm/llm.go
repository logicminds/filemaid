// Package llm provides an Ollama-based file classifier.
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/directory"
)

// Decision holds the classifier's output for a single file.
type Decision struct {
	Category    string   `json:"category"`
	Subcategory string   `json:"subcategory"`
	Tags        []string `json:"tags"`
	Action      string   `json:"action"` // move | delete | review
	Destination string   `json:"destination"`
	Reason      string   `json:"reason"`
	NewName     string   `json:"new_name"`
	NameQuality int      `json:"name_quality"`
}

// Metrics holds LLM telemetry for a single classification.
type Metrics struct {
	DurationMs       int64   `json:"duration_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	TokensPerSec     float64 `json:"tokens_per_sec"`
	ContextSize      int     `json:"context_size"`
}


var (
	imgPattern      = regexp.MustCompile(`^img_\d+`)
	dscPattern      = regexp.MustCompile(`^dsc_\d+`)
	pxlPattern      = regexp.MustCompile(`^pxl_\d+`)
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timestampPattern = regexp.MustCompile(`^\d{8}_\d{6}$`)
)
// DefaultContextSize is the num_ctx option sent to Ollama for classification.
const DefaultContextSize = 4096
// systemPrompt is sent as the system message on every /api/chat request so the
// classifier instructions always match the running code, regardless of what
// system prompt was baked into the Ollama Modelfile at setup time.
const systemPrompt = `You are a macOS file classifier for the filemaid organizer. Your job is to classify files into exactly one category from the user's configured categories, which are provided in each user message along with the file metadata.

Analyze the filename, extension, path, any provided text snippet, and any attached image. Be conservative:
- Use action "move" when the category is clearly a good fit.
- Use action "delete" only for obvious trash, installers, or duplicates.
- Use action "review" when the file is ambiguous, sensitive, personal, or cannot be classified.

When a rename is requested, you MUST always provide new_name and name_quality. If the current filename is already specific, content-descriptive, and search-friendly (e.g., "Q1 Sales Report.pdf", "Invoice - Acme - 2024-03.pdf", "Birthday Party Photo - Sarah.jpg"), set new_name to the current filename and name_quality to 5.
For generic, templated, camera-generated, timestamp-only, AI-generated, or non-descriptive filenames, suggest a specific, descriptive new_name. Examples of names that MUST be renamed:
- IMG_1234.jpg, DSC_0001.png, PXL_20240101_000000000.jpg
- Screenshot 2024-01-01.png, Screen Shot 2024-01-01 at 12.00.00 AM.png
- Document.pdf, scan.pdf, image.png, file.jpg, download.pdf, Download (1).zip
- 2024-01-01.pdf, 20240101_120000.png, Untitled.png
- Gemini_Generated_Image_s5a4vcs5a4vcs5a4.png, DALL-EGeneratedImage.png, Midjourney image.png

The new filename must preserve the original extension.

Output a single valid JSON object and nothing else. Do not use markdown code fences, explanations, or extra text.

Required JSON schema:
{
  "category": "...",
  "subcategory": "...",
  "tags": ["..."],
  "action": "move|delete|review",
  "destination": "",
  "reason": "...",
  "new_name": "...",
  "name_quality": 3
}

Leave destination empty. Provide 1-3 concise Finder tags. Keep the reason brief.`

// NewDecision returns a Decision with the required default values.
func NewDecision() Decision {
	return Decision{
		Category: "Unknown",
		Action:   "review",
	}
}

// Classifier turns a file path into a classification Decision.
type Classifier interface {
	// Classify classifies a single file and returns a Decision and telemetry.
	// dirCtx is optional; when non-nil the classifier may include directory
	// ancestry information in the prompt and cache key.
	Classify(ctx context.Context, path string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (Decision, Metrics, error)
	// Validate checks that the configured Ollama model is reachable and can
	// generate a response. Commands that depend on classification should call
	// Validate before touching any files.
	Validate(cfg *config.Config) error
}

// DecisionCache stores and retrieves classification decisions keyed by content
// hash so repeated identical files can skip LLM calls.
type DecisionCache interface {
	FindDecisionByHash(sha256 string) (Decision, bool, error)
	RecordDecision(sha256 string, decision Decision) error
}

// DirectoryDecision holds the classifier's output for a single directory.
type DirectoryDecision struct {
	Recommendation string   `json:"recommendation"`        // keep|review|trash|archive
	Action         string   `json:"action,omitempty"`      // move, or empty
	Destination    string   `json:"destination,omitempty"` // optional explicit destination
	Reason         string   `json:"reason"`
	Category       string   `json:"category,omitempty"`
	Tags           []string `json:"tags,omitempty"`
}

// NewDirectoryDecision returns a DirectoryDecision with the required default values.
func NewDirectoryDecision() DirectoryDecision {
	return DirectoryDecision{
		Recommendation: "review",
	}
}

// DirectoryClassifier classifies a directory from bounded metadata.
type DirectoryClassifier interface {
	ClassifyDirectory(ctx context.Context, meta *directory.Metadata, cfg *config.Config) (DirectoryDecision, Metrics, error)
}

// DirectoryDecisionCache stores and retrieves directory classification decisions
// keyed by a stable digest so repeated identical directories can skip LLM calls.
type DirectoryDecisionCache interface {
	FindDirectoryDecision(key string) (DirectoryDecision, bool, error)
	RecordDirectoryDecision(key string, decision DirectoryDecision) error
}

// HTTPTransport abstracts the HTTP layer so tests can fake Ollama responses.
// It matches the standard http.RoundTripper signature.
type HTTPTransport interface {
	RoundTrip(req *http.Request) (*http.Response, error)
}

// Client is an Ollama-backed Classifier.
type Client struct {
	transport      HTTPTransport
	cache          DecisionCache
	directoryCache DirectoryDecisionCache

	mu      sync.Mutex
	checked map[modelKey]checkResult
}

type modelKey struct {
	url   string
	model string
}

type checkResult struct {
	resolved string
	err      error
}

// NewClient creates a classifier. If transport is nil, the default HTTP
// transport is used.
func NewClient(transport HTTPTransport) *Client {
	return &Client{
		transport: transport,
		checked:   make(map[modelKey]checkResult),
	}
}

// SetDecisionCache attaches a decision cache to the client. When set, Classify
// checks the cache by file hash before calling Ollama and records the result
// after a successful classification.
func (c *Client) SetDecisionCache(cache DecisionCache) {
	c.cache = cache
}

// SetDirectoryDecisionCache attaches a directory decision cache to the client.
func (c *Client) SetDirectoryDecisionCache(cache DirectoryDecisionCache) {
	c.directoryCache = cache
}

// Classify classifies a single file using Ollama.
//
// Transient errors such as timeouts, connection failures, or model loading
// states are retried up to cfg.LLMRetryAttempts times with exponential backoff.
// Only non-transient errors or exhausted retries fall back to an Unknown/review
// decision. Model resolution errors are returned immediately because a missing
// model is not a retryable condition.
func (c *Client) Classify(ctx context.Context, path string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (Decision, Metrics, error) {
	start := time.Now()

	cacheKey := fileHash
	if dirCtx != nil && !dirCtx.None() {
		cacheKey = contextualCacheKey(fileHash, dirCtx)
	}
	if cacheKey != "" && c.cache != nil {
		if d, ok, err := c.cache.FindDecisionByHash(cacheKey); err == nil && ok {
			return d, Metrics{}, nil
		}
	}

	resolvedModel, err := c.checkModel(ctx, cfg.OllamaURL, cfg.Model)
	if err != nil {
		return Decision{}, Metrics{}, err
	}

	prompt, images, err := buildPrompt(path, cfg, dirCtx)
	if err != nil {
		d := NewDecision()
		d.Reason = fmt.Sprintf("build prompt error: %v", err)
		return d, Metrics{}, nil
	}
	categories := categorySet(cfg.Categories)
	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")
	model := resolvedModel

	attempts := cfg.LLMRetryAttempts
	if attempts < 0 {
		attempts = 0
	}
	baseDelay := time.Duration(cfg.LLMRetryBaseDelay)
	if baseDelay <= 0 {
		baseDelay = 2 * time.Second
	}

	var decision Decision
	var resp chatResponse
	transient := false

	for i := 0; i <= attempts; i++ {
		if i > 0 && transient {
			delay := baseDelay * time.Duration(1<<(i-1))
			const maxDelay = 30 * time.Second
			if delay > maxDelay {
				delay = maxDelay
			}
			slog.Debug("retrying transient LLM error", "attempt", i, "delay", delay, "error", err)
			time.Sleep(delay)
		}

		decision, resp, err = c.tryChat(ctx, ollamaURL, model, prompt, images, categories, time.Duration(cfg.RequestTimeout), path)
		if err == nil {
			break
		}
		transient = isTransientError(err)
		if !transient {
			break
		}
	}

	metrics := metricsFromChatResponse(resp)
	metrics.DurationMs = time.Since(start).Milliseconds()
	metrics.ContextSize = DefaultContextSize

	if err == nil {
		if cacheKey != "" && c.cache != nil {
			_ = c.cache.RecordDecision(cacheKey, decision)
		}
		return decision, metrics, nil
	}

	d := NewDecision()
	if transient {
		d.Reason = fmt.Sprintf("ollama error after %d retries: %s", attempts, err.Error())
	} else {
		d.Reason = fmt.Sprintf("ollama error: %s", err.Error())
	}
	// Do not cache transient errors (timeouts, unreachable Ollama, etc.):
	// the next run should retry instead of replaying a failed decision forever.
	return d, metrics, nil
}

// tryChat performs a single chat classification attempt, including the
// image-to-text fallback when the attached image causes a vision error.
func (c *Client) tryChat(ctx context.Context, ollamaURL, model, prompt string, images []string, categories map[string]bool, timeout time.Duration, path string) (Decision, chatResponse, error) {
	try := func(imgs []string) (Decision, chatResponse, error) {
		resp, err := c.requestChat(ctx, ollamaURL, model, prompt, imgs, categories, timeout)
		if err != nil {
			return Decision{}, chatResponse{}, err
		}
		if resp.Error != "" {
			return Decision{}, resp, errors.New(resp.Error)
		}
		if decision, ok := parseResponse(resp, categories, path); ok {
			return decision, resp, nil
		}
		return Decision{}, resp, errors.New("could not parse model response")
	}

	decision, resp, err := try(images)
	if err != nil && len(images) > 0 && isImageRelatedError(err) {
		decision, resp, err = try(nil)
	}
	return decision, resp, err
}

// isTransientError reports whether an error from Ollama is likely temporary
// and worth retrying. Non-transient errors include malformed responses, model
// not found, and user cancellation.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// User cancellation should not be retried.
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() || netErr.Temporary() {
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	transientPhrases := []string{
		"connection refused",
		"no connection",
		"broken pipe",
		"reset by peer",
		"dial tcp",
		"no such host",
		"model is loading",
		"loading model",
		"model loading",
		"runner process",
		"server busy",
		"temporarily unavailable",
		"context deadline exceeded",
	}
	for _, phrase := range transientPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// IsTransientReason reports whether a review reason string indicates a
// transient LLM failure that may succeed if retried. It mirrors the detection
// used by isTransientError for the error-to-reason conversion.
func IsTransientReason(reason string) bool {
	if reason == "" {
		return false
	}
	msg := strings.ToLower(reason)
	if !strings.Contains(msg, "ollama error") {
		return false
	}
	transientPhrases := []string{
		"after",
		"context deadline exceeded",
		"connection refused",
		"no connection",
		"broken pipe",
		"reset by peer",
		"dial tcp",
		"no such host",
		"model is loading",
		"loading model",
		"model loading",
		"runner process",
		"server busy",
		"temporarily unavailable",
		"eof",
	}
	for _, phrase := range transientPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// contextualCacheKey returns a deterministic cache key that combines a file's
// content hash with its directory context so the same file in different
// contexts can receive different cached decisions.
func contextualCacheKey(fileHash string, dirCtx *directory.Context) string {
	h := sha256.New()
	h.Write([]byte(fileHash))
	if dirCtx != nil && !dirCtx.None() {
		b, _ := json.Marshal(dirCtx)
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (c *Client) ClassifyDirectory(ctx context.Context, meta *directory.Metadata, cfg *config.Config) (DirectoryDecision, Metrics, error) {
	key := directoryCacheKey(meta, cfg)
	if key != "" && c.directoryCache != nil {
		if d, ok, err := c.directoryCache.FindDirectoryDecision(key); err == nil && ok {
			return d, Metrics{}, nil
		}
	}

	resolvedModel, err := c.checkModel(ctx, cfg.OllamaURL, cfg.Model)
	if err != nil {
		return DirectoryDecision{}, Metrics{}, err
	}

	prompt := buildDirectoryPrompt(meta, cfg)
	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")
	model := resolvedModel

	start := time.Now()
	resp, err := c.requestDirectoryChat(ctx, ollamaURL, model, prompt, time.Duration(cfg.RequestTimeout))
	if err != nil {
		return NewDirectoryDecision(), Metrics{}, err
	}
	if resp.Error != "" {
		return NewDirectoryDecision(), Metrics{}, errors.New(resp.Error)
	}

	decision := parseDirectoryResponse(resp)
	metrics := metricsFromChatResponse(resp)
	metrics.DurationMs = time.Since(start).Milliseconds()
	metrics.ContextSize = DefaultContextSize

	if key != "" && c.directoryCache != nil {
		_ = c.directoryCache.RecordDirectoryDecision(key, decision)
	}
	return decision, metrics, nil
}

// isImageRelatedError reports whether an error from Ollama is likely caused
// by the attached image (for example, the model does not support vision or
// the image payload could not be decoded). These errors trigger a single
// retry without images.
func isImageRelatedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, term := range []string{"image", "images", "vision", "visual", "base64", "decode", "encoding", "multimodal"} {
		if strings.Contains(msg, term) {
			return true
		}
	}
	return false
}

// checkModel asks Ollama whether the configured model exists locally and
// returns the actual tag name to use (e.g. filemaid-gemma4-26b:vhash when the
// configured name has no :latest tag). It returns a clear error so callers can
// warn the user and exit instead of retrying unknown models and crashing.
// Results are cached per (url, model) on the Client so repeated checks do not
// contact Ollama again.
func (c *Client) checkModel(ctx context.Context, ollamaURL, model string) (string, error) {
	c.mu.Lock()
	res, ok := c.checked[modelKey{url: ollamaURL, model: model}]
	c.mu.Unlock()
	if ok {
		return res.resolved, res.err
	}

	resolved, err := c.fetchCheckModel(ctx, ollamaURL, model)

	c.mu.Lock()
	c.checked[modelKey{url: ollamaURL, model: model}] = checkResult{resolved: resolved, err: err}
	c.mu.Unlock()
	return resolved, err
}

// fetchCheckModel performs the actual Ollama /api/tags request and returns
// the exact model name to use, which may include a tag when the configured
// model name lacks one.
func (c *Client) fetchCheckModel(ctx context.Context, ollamaURL, model string) (string, error) {
	url := strings.TrimRight(ollamaURL, "/") + "/api/tags"
	transport := c.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return "", fmt.Errorf("could not reach Ollama at %s: %w", ollamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d listing models: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ollama model list: %w", err)
	}

	var list struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("parsing ollama model list: %w", err)
	}

	for _, m := range list.Models {
		if modelMatch(model, m.Name) {
			// Prefer an exact match (including latest) when available; otherwise
			// return the first matching tagged name so a missing :latest tag does
			// not break existing installs.
			if m.Name == model || m.Name == model+":latest" {
				return m.Name, nil
			}
			return m.Name, nil
		}
	}
	return "", fmt.Errorf("model %q not found in Ollama; run `filemaid setup` or `ollama pull %s`", model, model)
}

// modelMatch reports whether the configured model name want matches the name
// returned by Ollama's /api/tags endpoint. A want value with no tag is treated
// as a family name and matches any tag for that model, consistent with setup.
func modelMatch(want, have string) bool {
	if want == have {
		return true
	}
	// If want has no tag, accept any tag for the same base model.
	if !strings.Contains(want, ":") {
		return strings.HasPrefix(have, want+":")
	}
	return false
}

// Validate checks that Ollama is reachable and that the configured models can
// generate a response. It returns an error if a model is missing, Ollama is
// unreachable, or the model fails to generate. Commands should call Validate
// before performing any file operations that depend on classification.
func (c *Client) Validate(cfg *config.Config) error {
	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")

	resolvedModel, err := c.checkModel(context.Background(), ollamaURL, cfg.Model)
	if err != nil {
		return err
	}

	for _, model := range []string{cfg.ImageModel, cfg.TextModel} {
		if model == "" {
			continue
		}
		if _, err := c.checkModel(context.Background(), ollamaURL, model); err != nil {
			return err
		}
	}

	timeout := time.Duration(cfg.RequestTimeout)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	body := map[string]any{
		"model":  resolvedModel,
		"prompt": "Reply with the single word OK.",
		"stream": false,
		"options": map[string]any{
			"temperature": 0,
			"num_predict": 3,
			"num_ctx":     8192,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var resp generateResponse
	if err := c.postJSON(ctx, ollamaURL+"/api/generate", body, timeout, &resp); err != nil {
		return fmt.Errorf("model %q validation failed: %w", cfg.Model, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("model %q validation failed: %s", cfg.Model, resp.Error)
	}
	return nil
}

var imageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".heic": true,
}

// IsImageFile reports whether path has an extension treated as an image for
// classification purposes. It is used by callers to route files to the
// configured image model.
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExts[ext]
}

var textExts = map[string]bool{
	".txt":   true,
	".md":    true,
	".csv":   true,
	".json":  true,
	".xml":   true,
	".yaml":  true,
	".yml":   true,
	".py":    true,
	".js":    true,
	".ts":    true,
	".jsx":   true,
	".tsx":   true,
	".html":  true,
	".css":   true,
	".sh":    true,
	".zsh":   true,
	".bash":  true,
	".swift": true,
	".c":     true,
	".cpp":   true,
	".h":     true,
	".rs":    true,
	".go":    true,
	".java":  true,
	".kt":    true,
	".rb":    true,
	".php":   true,
	".pl":    true,
	".sql":   true,
}

const promptTemplate = `Classify this file into exactly one category from: %s.
%s

Choose the action based on confidence:
- Use "move" when the category is clearly a good fit.
- Use "delete" only for obvious trash, installers, or duplicates.
- Use "review" when the file is ambiguous, sensitive, personal, or cannot be classified.

File:
- path: %s
- name: %s
- extension: %s
- size: %d bytes
- modified: %s
%s

Return a single compact JSON object and nothing else. Leave destination empty.

Rename guidance:
- You MUST always provide new_name and name_quality. If the current filename is already specific, content-descriptive, and search-friendly (e.g., "Q1 Sales Report.pdf", "Invoice - Acme - 2024-03.pdf", "Birthday Party Photo - Sarah.jpg"), set new_name to the current filename and name_quality to 5.
- For generic, templated, camera-generated, timestamp-only, AI-generated, or non-descriptive filenames, suggest a specific, descriptive new_name (preserve the original extension). Examples of names that MUST be renamed: "IMG_1234.jpg", "DSC_0001.png", "PXL_20240101_000000000.jpg", "Screenshot 2024-01-01.png", "Screen Shot 2024-01-01 at 12.00.00 AM.png", "Document.pdf", "scan.pdf", "image.png", "file.jpg", "download.pdf", "Download (1).zip", "2024-01-01.pdf", "20240101_120000.png", "Untitled.png", "Gemini_Generated_Image_s5a4vcs5a4vcs5a4.png", "DALL-EGeneratedImage.png", "Midjourney image.png".
- Rate name_quality 1-5 based on how clearly better the suggested name is than the current name.

{"category": "...", "subcategory": "...", "tags": ["..."], "action": "...", "destination": "", "reason": "...", "new_name": "...", "name_quality": 3}`

const subcategoryInstructions = `For image files, also provide a concise subcategory describing the main subject or scene (e.g., cat, dog, baby, kid, woman, wedding, car, nature, food, selfie, document-photo). For screenshots, describe the app or context (e.g., Safari, Terminal, Slack, VS Code: browser, lock-screen, menu-bar). The subcategory will be added as a Finder tag.`

// encodeImageToJPEG re-encodes img as a JPEG with the given quality.
func encodeImageToJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resizeImage scales img down so that its largest dimension is at most maxDim.
// Images already within the limit are returned unchanged.
func resizeImage(img image.Image, maxDim int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}

	var newW, newH int
	if w > h {
		newW = maxDim
		newH = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		newH = maxDim
		newW = int(float64(w) * float64(maxDim) / float64(h))
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return bilinearResize(img, newW, newH)
}

// resizeImageBytes decodes b, resizes it, and re-encodes the result as JPEG.
func resizeImageBytes(b []byte, maxDim int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	resized := resizeImage(img, maxDim)
	return encodeImageToJPEG(resized, 85)
}

// bilinearResize returns a new RGBA image scaled to dstW x dstH using bilinear
// interpolation.
func bilinearResize(src image.Image, dstW, dstH int) image.Image {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xRatio := float64(srcW) / float64(dstW)
	yRatio := float64(srcH) / float64(dstH)

	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx := (float64(x)+0.5)*xRatio - 0.5 + float64(bounds.Min.X)
			sy := (float64(y)+0.5)*yRatio - 0.5 + float64(bounds.Min.Y)
			dst.Set(x, y, bilinearSample(src, sx, sy))
		}
	}
	return dst
}

// bilinearSample samples src at (x, y) using bilinear interpolation.
func bilinearSample(src image.Image, x, y float64) color.Color {
	bounds := src.Bounds()
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	x1 := x0 + 1
	y1 := y0 + 1

	if x0 < bounds.Min.X {
		x0 = bounds.Min.X
	}
	if y0 < bounds.Min.Y {
		y0 = bounds.Min.Y
	}
	if x1 >= bounds.Max.X {
		x1 = bounds.Max.X - 1
	}
	if y1 >= bounds.Max.Y {
		y1 = bounds.Max.Y - 1
	}
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}

	fx := x - float64(x0)
	fy := y - float64(y0)
	if fx < 0 {
		fx = 0
	}
	if fx > 1 {
		fx = 1
	}
	if fy < 0 {
		fy = 0
	}
	if fy > 1 {
		fy = 1
	}

	c00 := src.At(x0, y0)
	c10 := src.At(x1, y0)
	c01 := src.At(x0, y1)
	c11 := src.At(x1, y1)

	r00, g00, b00, a00 := c00.RGBA()
	r10, g10, b10, a10 := c10.RGBA()
	r01, g01, b01, a01 := c01.RGBA()
	r11, g11, b11, a11 := c11.RGBA()

	r := lerp(lerp(r00, r10, fx), lerp(r01, r11, fx), fy)
	g := lerp(lerp(g00, g10, fx), lerp(g01, g11, fx), fy)
	b := lerp(lerp(b00, b10, fx), lerp(b01, b11, fx), fy)
	a := lerp(lerp(a00, a10, fx), lerp(a01, a11, fx), fy)

	return color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)}
}

// lerp linearly interpolates between a and b by t (0..1).
func lerp(a, b uint32, t float64) uint32 {
	return uint32(float64(a)*(1.0-t) + float64(b)*t)
}

func buildPrompt(path string, cfg *config.Config, dirCtx *directory.Context) (string, []string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}

	cats := make([]string, 0, len(cfg.Categories))
	for k := range cfg.Categories {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	categories := strings.Join(cats, ", ")

	mtime := time.Unix(stat.ModTime().Unix(), 0).Format("2006-01-02T15:04:05")
	ext := strings.ToLower(filepath.Ext(path))

	var images []string
	var extras []string
	var subcatExtra string
	if dirCtx != nil && !dirCtx.None() {
		ctxLines := []string{"Directory context:"}
		ctxLines = append(ctxLines, fmt.Sprintf("- ancestor: %s", dirCtx.Ancestor))
		ctxLines = append(ctxLines, fmt.Sprintf("- depth: %d", dirCtx.Depth))
		if dirCtx.Marker != "" {
			ctxLines = append(ctxLines, fmt.Sprintf("- project marker: %s", dirCtx.Marker))
		}
		extras = append(extras, strings.Join(ctxLines, "\n"))
	}
	if imageExts[ext] {
		extras = append(extras, "The image is attached; use its content to classify.")
		if cfg.SubcategorizeImages {
			subcatExtra = subcategoryInstructions
		}
		b, err := os.ReadFile(path)
		if err == nil {
			maxDim := cfg.MaxImageDimension
			if maxDim < 64 {
				maxDim = 64
			}
			resized, resizeErr := resizeImageBytes(b, maxDim)
			if resizeErr == nil {
				images = append(images, base64.StdEncoding.EncodeToString(resized))
			} else {
				images = append(images, base64.StdEncoding.EncodeToString(b))
			}
		}
	} else if textExts[ext] {
		snippet := readTextSnippet(path, 2048)
		extras = append(extras, fmt.Sprintf("First 2048 bytes:\n%s", snippet))
	}

	if note := renameNote(filepath.Base(path)); note != "" {
		extras = append(extras, note)
	}

	prompt := fmt.Sprintf(
		promptTemplate,
		categories,
		subcatExtra,
		path,
		filepath.Base(path),
		ext,
		stat.Size(),
		mtime,
		strings.Join(extras, "\n"),
	)
	return prompt, images, nil
}

const directoryPromptTemplate = `You are a directory analyst. Based on the bounded metadata below, recommend what to do with this directory.

Choose exactly one recommendation: keep, review, trash, or archive.
keep = the directory is clearly valuable and should stay where it is.
review = the directory is ambiguous, sensitive, personal, or cannot be confidently classified.
trash = the directory is obvious junk, cache, or temporary data with no lasting value.
archive = the directory contains valuable historical data that should be preserved but is not actively needed.

If the recommendation is review or archive, you may also recommend moving the whole directory as an atomic project:
- action: set to "move" when the directory is a cohesive project that should be relocated as a unit. Leave empty for ordinary review or archive decisions.
- destination: optional explicit destination path or category label. Leave empty unless you want to override the configured project directory category.

Directory:
path: %s
name: %s
size: %d bytes
child count: %d
modified: %s
extensions: %s
project markers: %s
%s
%s

Return a single compact JSON object and nothing else.
{"recommendation": "keep|review|trash|archive", "action": "move|", "destination": "", "reason": "...", "category": "...", "tags": ["..."]}`

func buildDirectoryPrompt(meta *directory.Metadata, cfg *config.Config) string {
	mtime := meta.Mtime.Format("2006-01-02T15:04:05")
	if meta.Mtime.IsZero() {
		mtime = "unknown"
	}

	exts := make([]string, 0, len(meta.Extensions))
	for ext := range meta.Extensions {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	extParts := make([]string, 0, len(exts))
	for _, ext := range exts {
		extParts = append(extParts, fmt.Sprintf("%s:%d", ext, meta.Extensions[ext]))
	}

	var extras []string
	if meta.IsAppBundle {
		extras = append(extras, "This is an application bundle; treat it as an opaque directory.")
	}
	if meta.Truncated != "" {
		extras = append(extras, fmt.Sprintf("Note: child enumeration was truncated due to %s.", meta.Truncated))
	}
	if len(meta.ReadErrors) > 0 {
		extras = append(extras, fmt.Sprintf("Read errors on %d children.", len(meta.ReadErrors)))
	}

	markers := "none"
	if len(meta.Markers) > 0 {
		markers = strings.Join(meta.Markers, ", ")
	}

	var snippetsSection string
	if len(meta.Snippets) > 0 {
		names := make([]string, 0, len(meta.Snippets))
		for name := range meta.Snippets {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s:\n%s", name, meta.Snippets[name]))
		}
		snippetsSection = "Representative text snippets:\n" + strings.Join(parts, "\n---\n")
	}

	return fmt.Sprintf(
		directoryPromptTemplate,
		meta.Path,
		meta.Base,
		meta.Size,
		meta.ChildCount,
		mtime,
		strings.Join(extParts, ", "),
		markers,
		strings.Join(extras, "\n"),
		snippetsSection,
	)
}

func directoryToolSchema() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "classify_directory",
			"description": "Classify a directory and recommend what to do with it",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recommendation": map[string]any{
						"type":        "string",
						"enum":        []string{"keep", "review", "trash", "archive"},
						"description": "keep = valuable, review = ambiguous/sensitive, trash = obvious junk, archive = valuable historical data",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Brief explanation for the recommendation",
					},
					"category": map[string]any{
						"type":        "string",
						"description": "Optional category label for the directory",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional descriptive tags",
					},
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"move", ""},
						"description": "Set to 'move' for review/archive recommendations when the directory is a cohesive project that should move as a whole. Leave empty otherwise.",
					},
					"destination": map[string]any{
						"type":        "string",
						"description": "Optional explicit destination path or category label when action is 'move'. Leave empty to use the configured project directory category.",
					},
				},
				"required": []string{"recommendation", "reason"},
			},
		},
	}
}

func (c *Client) requestDirectoryChat(ctx context.Context, ollamaURL, model, prompt string, timeout time.Duration) (chatResponse, error) {
	var resp chatResponse
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"tools":      []map[string]any{directoryToolSchema()},
		"stream":     false,
		"keep_alive": "5m",
		"think":      false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 256,
			"num_ctx":     DefaultContextSize,
		},
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if err := c.postJSON(ctx, ollamaURL+"/api/chat", body, timeout, &resp); err != nil {
		return chatResponse{}, err
	}
	return resp, nil
}

func parseDirectoryResponse(resp chatResponse) DirectoryDecision {
	d := NewDirectoryDecision()

	msg := resp.Message
	if len(msg.ToolCalls) > 0 {
		call := msg.ToolCalls[0]
		if call.Function.Name == "classify_directory" || call.Function.Name == "" {
			if args, ok := parseArguments(call.Function.Arguments); ok {
				return buildDirectoryDecision(args)
			}
		}
	}

	if raw := strings.TrimSpace(resp.Response); raw != "" {
		if parsed, ok := extractJSON(raw); ok {
			return buildDirectoryDecision(parsed)
		}
	}

	return d
}

func buildDirectoryDecision(m map[string]any) DirectoryDecision {
	d := NewDirectoryDecision()

	if rec, ok := stringField(m, "recommendation"); ok {
		switch rec {
		case "keep", "review", "trash", "archive":
			d.Recommendation = rec
		}
	}

	d.Reason, _ = stringField(m, "reason")
	d.Category, _ = stringField(m, "category")
	d.Tags = stringSliceField(m, "tags")

	if action, ok := stringField(m, "action"); ok {
		if action == "move" {
			d.Action = "move"
		}
	}
	d.Destination, _ = stringField(m, "destination")

	return d
}

func directoryCacheKey(meta *directory.Metadata, cfg *config.Config) string {
	if meta == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", meta.Path)
	fmt.Fprintf(h, "%d\n", meta.Size)
	fmt.Fprintf(h, "%d\n", meta.Mtime.Unix())

	if cfg != nil {
		fmt.Fprintf(h, "cat:%s\n", cfg.ProjectDirCategory)
		markers := make([]string, len(cfg.ProjectMarkers))
		copy(markers, cfg.ProjectMarkers)
		sort.Strings(markers)
		for _, m := range markers {
			fmt.Fprintf(h, "marker:%s\n", m)
		}
	}

	exts := make([]string, 0, len(meta.Extensions))
	for ext := range meta.Extensions {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	for _, ext := range exts {
		fmt.Fprintf(h, "%s:%d\n", ext, meta.Extensions[ext])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func readTextSnippet(path string, limit int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return ""
	}
	return string(buf[:n])
}

func categorySet(categories map[string]string) map[string]bool {
	set := make(map[string]bool, len(categories))
	for k := range categories {
		set[k] = true
	}
	return set
}

func (c *Client) requestGenerate(ctx context.Context, ollamaURL, model, prompt string, images []string, timeout time.Duration) (generateResponse, error) {
	var resp generateResponse
	body := map[string]any{
		"model":      model,
		"prompt":     prompt,
		"images":     images,
		"stream":     false,
		"keep_alive": "5m",
		"think":      false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 512,
			"num_ctx":     DefaultContextSize,
		},
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if err := c.postJSON(ctx, ollamaURL+"/api/generate", body, timeout, &resp); err != nil {
		return generateResponse{}, err
	}
	return resp, nil
}

func (c *Client) requestChat(ctx context.Context, ollamaURL, model, prompt string, images []string, categories map[string]bool, timeout time.Duration) (chatResponse, error) {
	var resp chatResponse
	// systemPrompt is sent as a request-level system message so classification
	// instructions always match the running code, overriding any system prompt
	// baked into the Ollama Modelfile at setup time.
	catList := make([]string, 0, len(categories))
	for k := range categories {
		catList = append(catList, k)
	}
	sort.Strings(catList)
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": prompt,
				"images":  images,
			},
		},
		"tools":      []map[string]any{toolSchema(catList)},
		"stream":     false,
		"keep_alive": "5m",
		"think":      false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 512,
			"num_ctx":     DefaultContextSize,
		},
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if err := c.postJSON(ctx, ollamaURL+"/api/chat", body, timeout, &resp); err != nil {
		return chatResponse{}, err
	}
	return resp, nil
}

func toolSchema(categories []string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "classify_file",
			"description": "Classify a file and decide what to do with it",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{
						"type": "string",
						"enum": categories,
					},
					"subcategory": map[string]any{
						"type":        "string",
						"description": "Concise subject or scene description for images and screenshots",
					},
					"tags": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"new_name": map[string]any{
						"type":        "string",
						"description": "Suggested new filename; must preserve the original file extension. Provide a specific, descriptive name for generic, templated, camera-generated, timestamp-only, or non-descriptive filenames (e.g., IMG_1234.jpg, Screenshot 2024-01-01.png, Document.pdf, scan.pdf, Download (1).zip). Only omit new_name when the current filename is already specific, content-descriptive, and search-friendly.",
					},
					"name_quality": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     5,
						"description": "Quality rating of the suggested new_name from 1 (poor suggestion) to 5 (excellent suggestion); higher values mean the suggested name is more clearly better than the current name",
					},
				},
				"required": []string{"category", "tags", "action", "reason", "new_name", "name_quality"},
			},
		},
	}
}

func (c *Client) postJSON(ctx context.Context, url string, body any, timeout time.Duration, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	transport := c.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return err
	}
	return nil
}

// chatMessage is a single message in an Ollama /api/chat response.
type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls"`
	Thinking  string     `json:"thinking"`
}

// chatResponse is the response body from Ollama's /api/chat endpoint.
type chatResponse struct {
	Error    string      `json:"error"`
	Response string      `json:"response"`
	Message  chatMessage `json:"message"`

	PromptEvalCount    int   `json:"prompt_eval_count"`
	EvalCount          int   `json:"eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalDuration       int64 `json:"eval_duration"`
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
}

func parseResponse(resp chatResponse, categories map[string]bool, path string) (Decision, bool) {
	msg := resp.Message
	for _, tc := range msg.ToolCalls {
		args, ok := parseArguments(tc.Function.Arguments)
		if ok {
			return buildDecision(args, categories, "", path), true
		}
	}


	// Some models return raw JSON in the assistant message content
	// instead of using tool_calls. Try to extract JSON from there as a
	// fallback before falling back to the legacy /api/generate response.
	if content := strings.TrimSpace(msg.Content); content != "" {
		if parsed, ok := extractJSON(content); ok {
			dest, _ := stringField(parsed, "destination")
			return buildDecision(parsed, categories, dest, path), true
		}
	}

	// Models with a visible reasoning/thinking field may embed the final
	// JSON decision inside that field. Use it as a last resort for chat
	// responses before giving up on the chat endpoint.
	if thinking := strings.TrimSpace(msg.Thinking); thinking != "" {
		if parsed, ok := extractJSON(thinking); ok {
			dest, _ := stringField(parsed, "destination")
			return buildDecision(parsed, categories, dest, path), true
		}
	}

	// Legacy /api/generate response path.
	if resp := strings.TrimSpace(resp.Response); resp != "" {
		if parsed, ok := extractJSON(resp); ok {
			dest, _ := stringField(parsed, "destination")
			return buildDecision(parsed, categories, dest, path), true
		}
	}

	return Decision{}, false
}

// toolCall is a tool invocation returned by a model in /api/chat.
type toolCall struct {
	Function functionCall `json:"function"`
}

// functionCall carries the name and arguments of a tool call.
type functionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// generateResponse is the response body from Ollama's /api/generate endpoint.
type generateResponse struct {
	Error    string `json:"error"`
	Response string `json:"response"`

	PromptEvalCount    int   `json:"prompt_eval_count"`
	EvalCount          int   `json:"eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalDuration       int64 `json:"eval_duration"`
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
}

// metricsFromChatResponse builds Metrics from Ollama timing fields.
func metricsFromChatResponse(resp chatResponse) Metrics {
	var m Metrics
	m.PromptTokens = resp.PromptEvalCount
	m.CompletionTokens = resp.EvalCount
	m.TotalTokens = resp.PromptEvalCount + resp.EvalCount
	if resp.EvalDuration > 0 {
		m.TokensPerSec = float64(resp.EvalCount) / (float64(resp.EvalDuration) / 1e9)
	}
	return m
}

// parseArguments handles tool-call arguments that may be a JSON object or a
// JSON string containing an object.
func parseArguments(raw json.RawMessage) (map[string]any, bool) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func extractJSON(raw string) (map[string]any, bool) {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) == 2 {
			text = parts[1]
		}
		text = strings.TrimSpace(text)
		if strings.HasSuffix(text, "```") {
			text = strings.TrimSuffix(text, "```")
			text = strings.TrimSpace(text)
		}
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, false
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, false
	}
	return out, true
}

func buildDecision(m map[string]any, categories map[string]bool, destination, path string) Decision {
	d := NewDecision()
	d.Destination = destination

	if cat, ok := stringField(m, "category"); ok {
		if categories[cat] {
			d.Category = cat
		}
	}

	d.Subcategory, _ = stringField(m, "subcategory")
	d.Tags = stringSliceField(m, "tags")

	if action, ok := stringField(m, "action"); ok {
		switch action {
		case "move", "delete", "review":
			d.Action = action
		}
	}

	d.Reason, _ = stringField(m, "reason")

	if newName, ok := stringField(m, "new_name"); ok {
		d.NewName = validateNewName(path, newName)
	}

	if q, ok := intField(m, "name_quality"); ok {
		if q < 1 {
			q = 1
		} else if q > 5 {
			q = 5
		}
		d.NameQuality = q
	} else if d.NewName != "" {
		// Models sometimes omit name_quality even when they provide a new_name.
		// Default to the minimum accepted quality so the rename threshold can
		// still apply.
		d.NameQuality = 1
	}

	return d
}


// validateNewName ensures the suggested filename preserves the original file
// extension. If the LLM changes the extension or omits it, the original
// extension is restored or appended.
func validateNewName(path, name string) string {
	origExt := filepath.Ext(path)
	newExt := filepath.Ext(name)
	if origExt == "" {
		return name
	}
	if newExt == "" {
		return name + origExt
	}
	if !strings.EqualFold(newExt, origExt) {
		base := strings.TrimSuffix(name, newExt)
		return base + origExt
	}
	return name
}

// renameNote returns a strong instruction to rename when filename matches
// common generic/templated/AI-generated patterns. It is appended to the user
// prompt as an extra nudge for models that otherwise keep these names.
func renameNote(name string) string {
	base := strings.TrimSpace(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	lower := strings.ToLower(base)
	lowerStem := strings.ToLower(stem)

	generic := false
	switch {
	case imgPattern.MatchString(lower):
		generic = true
	case dscPattern.MatchString(lower):
		generic = true
	case pxlPattern.MatchString(lower):
		generic = true
	case strings.HasPrefix(lower, "screenshot "):
		generic = true
	case strings.HasPrefix(lower, "screen shot "):
		generic = true
	case lower == "document"+ext, lower == "scan"+ext, lower == "image"+ext, lower == "file"+ext, lower == "download"+ext, lower == "untitled"+ext:
		generic = true
	case strings.HasPrefix(lower, "download ("):
		generic = true
	case datePattern.MatchString(lowerStem), timestampPattern.MatchString(lowerStem):
		generic = true
	case strings.HasPrefix(lower, "gemini_generated_image_"):
		generic = true
	case strings.HasPrefix(lower, "dall-e"):
		generic = true
	case strings.HasPrefix(lower, "midjourney"):
		generic = true
	}

	if !generic {
		return ""
	}
	return fmt.Sprintf("NOTE: The filename %q is generic/templated/AI-generated. You MUST suggest a descriptive new_name (keep extension %s) instead of keeping this name.", base, ext)
}
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

func stringSliceField(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	if s, ok := v.([]any); ok {
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	if s, ok := v.([]string); ok {
		return s
	}
	return nil
}
func intField(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}
