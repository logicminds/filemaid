// Package llm provides an Ollama-based file classifier.
package llm

import (
	"bytes"
	"context"
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
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/logicminds/filemaid/internal/config"
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

// DefaultContextSize is the num_ctx option sent to Ollama for classification.
const DefaultContextSize = 4096

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
	Classify(ctx context.Context, path string, fileHash string, cfg *config.Config) (Decision, Metrics, error)
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

// HTTPTransport abstracts the HTTP layer so tests can fake Ollama responses.
// It matches the standard http.RoundTripper signature.
type HTTPTransport interface {
	RoundTrip(req *http.Request) (*http.Response, error)
}

// Client is an Ollama-backed Classifier.
type Client struct {
	transport HTTPTransport
	cache     DecisionCache

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

// Classify classifies a single file using Ollama.
func (c *Client) Classify(ctx context.Context, path string, fileHash string, cfg *config.Config) (Decision, Metrics, error) {
	if fileHash != "" && c.cache != nil {
		if d, ok, err := c.cache.FindDecisionByHash(fileHash); err == nil && ok {
			return d, Metrics{}, nil
		}
	}

	resolvedModel, err := c.checkModel(ctx, cfg.OllamaURL, cfg.Model)
	if err != nil {
		return Decision{}, Metrics{}, err
	}

	prompt, images, err := buildPrompt(path, cfg)
	if err != nil {
		d := NewDecision()
		d.Reason = fmt.Sprintf("build prompt error: %v", err)
		return d, Metrics{}, nil
	}

	categories := categorySet(cfg.Categories)
	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")
	model := resolvedModel

	tryChat := func(imgs []string) (Decision, chatResponse, error) {
		resp, err := c.requestChat(ctx, ollamaURL, model, prompt, imgs, categories, time.Duration(cfg.RequestTimeout))
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

	start := time.Now()
	decision, resp, err := tryChat(images)
	if err != nil && len(images) > 0 && isImageRelatedError(err) {
		decision, resp, err = tryChat(nil)
	}

	metrics := metricsFromChatResponse(resp)
	metrics.DurationMs = time.Since(start).Milliseconds()
	metrics.ContextSize = DefaultContextSize

	if err == nil {
		if fileHash != "" && c.cache != nil {
			_ = c.cache.RecordDecision(fileHash, decision)
		}
		return decision, metrics, nil
	}

	d := NewDecision()
	d.Reason = fmt.Sprintf("ollama error: %s", err.Error())
	if fileHash != "" && c.cache != nil {
		_ = c.cache.RecordDecision(fileHash, d)
	}
	return d, metrics, nil
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

Return a single compact JSON object and nothing else. Leave destination empty. If the current filename is poor or misleading, suggest a better one in "new_name" (preserve the original extension) and rate the quality of that suggestion in "name_quality" (integer 1-5, where 5 is excellent). Omit new_name when the current name is already good.
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

func buildPrompt(path string, cfg *config.Config) (string, []string, error) {
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
	// requestChat intentionally sends only a user message. The model's
	// Modelfile supplies the full classifier SYSTEM prompt, and a request-level
	// system message would override it. The user message carries dynamic data:
	// the configured category list, per-file metadata, and optional snippet/image.
	catList := make([]string, 0, len(categories))
	for k := range categories {
		catList = append(catList, k)
	}
	sort.Strings(catList)
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
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
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"move", "delete", "review"},
						"description": "move = clearly classifiable, delete = obvious trash/duplicate, review = ambiguous/sensitive/unknown",
					},
					"reason": map[string]any{"type": "string"},
					"new_name": map[string]any{
						"type":        "string",
						"description": "Suggested new filename; must preserve the original file extension",
					},
					"name_quality": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     5,
						"description": "Quality rating of the suggested new_name from 1 (poor suggestion) to 5 (excellent suggestion); higher values mean the suggested name is more clearly better than the current name",
					},
				},
				"required": []string{"category", "tags", "action", "reason"},
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
