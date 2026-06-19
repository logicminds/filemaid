// Package llm provides an Ollama-based file classifier.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
}

// NewDecision returns a Decision with the required default values.
func NewDecision() Decision {
	return Decision{
		Category: "Unknown",
		Action:   "review",
	}
}

// Classifier turns a file path into a classification Decision.
type Classifier interface {
	// Classify classifies a single file and returns a Decision.
	Classify(path string, cfg *config.Config) (Decision, error)
	// Validate checks that the configured Ollama model is reachable and can
	// generate a response. Commands that depend on classification should call
	// Validate before touching any files.
	Validate(cfg *config.Config) error
}

// HTTPTransport abstracts the HTTP layer so tests can fake Ollama responses.
// It matches the standard http.RoundTripper signature.
type HTTPTransport interface {
	RoundTrip(req *http.Request) (*http.Response, error)
}

// Client is an Ollama-backed Classifier.
type Client struct {
	transport HTTPTransport
}

// NewClient creates a classifier. If transport is nil, the default HTTP
// transport is used.
func NewClient(transport HTTPTransport) *Client {
	return &Client{transport: transport}
}

// Classify classifies a single file using Ollama.
func (c *Client) Classify(path string, cfg *config.Config) (Decision, error) {
	if err := c.checkModel(cfg.OllamaURL, cfg.Model); err != nil {
		return Decision{}, err
	}

	prompt, images, err := buildPrompt(path, cfg)
	if err != nil {
		d := NewDecision()
		d.Reason = fmt.Sprintf("build prompt error: %v", err)
		return d, nil
	}

	categories := categorySet(cfg.Categories)
	endpoint := "auto"

	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")
	model := cfg.Model

	var lastErr string

	tryGenerate := func(useImages bool) (map[string]any, error) {
		localImages := images
		if !useImages {
			localImages = nil
		}
		return c.requestGenerate(ollamaURL, model, prompt, localImages)
	}

	tryChat := func(useImages bool) (map[string]any, error) {
		localImages := images
		if !useImages {
			localImages = nil
		}
		return c.requestChat(ollamaURL, model, prompt, localImages, categories)
	}

	strategies := []func(bool) (map[string]any, error){}
	if endpoint == "auto" || endpoint == "chat" {
		strategies = append(strategies, tryChat)
	}
	if endpoint == "auto" || endpoint == "generate" {
		strategies = append(strategies, tryGenerate)
	}

	for _, strategy := range strategies {
		for _, useImages := range []bool{true, false} {
			if useImages && len(images) == 0 {
				continue
			}
			data, err := strategy(useImages)
			if err != nil {
				lastErr = err.Error()
				continue
			}
			decision, ok := parseResponse(data, categories)
			if ok {
				return decision, nil
			}
		}
	}

	d := NewDecision()
	if lastErr != "" {
		d.Reason = fmt.Sprintf("ollama error: %s", lastErr)
	} else {
		d.Reason = "could not parse model response"
	}
	return d, nil
}

// checkModel asks Ollama whether the configured model exists locally. It
// returns a clear error so callers can warn the user and exit instead of
// retrying unknown models and crashing.
func (c *Client) checkModel(ollamaURL, model string) error {
	url := strings.TrimRight(ollamaURL, "/") + "/api/tags"
	transport := c.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("could not reach Ollama at %s: %w", ollamaURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %d listing models: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading ollama model list: %w", err)
	}

	var list struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("parsing ollama model list: %w", err)
	}

	for _, m := range list.Models {
		if modelMatch(model, m.Name) {
			return nil
		}
	}
	return fmt.Errorf("model %q not found in Ollama; run `filemaid setup` or `ollama pull %s`", model, model)
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

// Validate checks that Ollama is reachable and that the configured model can
// generate a response. It returns an error if the model is missing, Ollama is
// unreachable, or the model fails to generate. Commands should call Validate
// before performing any file operations that depend on classification.
func (c *Client) Validate(cfg *config.Config) error {
	if err := c.checkModel(cfg.OllamaURL, cfg.Model); err != nil {
		return err
	}

	ollamaURL := strings.TrimRight(cfg.OllamaURL, "/")
	body := map[string]any{
		"model":  cfg.Model,
		"prompt": "Reply with the single word OK.",
		"stream": false,
		"options": map[string]any{
			"temperature": 0,
			"num_predict": 3,
			"num_ctx":     8192,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := c.postJSON(ctx, ollamaURL+"/api/generate", body, 60*time.Second)
	if err != nil {
		return fmt.Errorf("model %q validation failed: %w", cfg.Model, err)
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

const promptTemplate = `You are a macOS file classifier. Pick exactly one category from: %s. Suggest 1-3 concise Finder tags. Decide the action.

Actions:
- move: the file clearly belongs to a category.
- delete: only obvious trash, installers, or duplicates.
- review: ambiguous, sensitive, or cannot classify.
%s
File:
- path: %s
- name: %s
- extension: %s
- size: %d bytes
- modified: %s
%s

Return a single compact JSON object and nothing else. Leave destination empty.
{"category": "...", "subcategory": "...", "tags": ["..."], "action": "...", "destination": "", "reason": "..."}`

const subcategoryInstructions = `
For image files, also provide a concise subcategory describing the main subject or scene (e.g., cat, dog, baby, kid, woman, wedding, car, nature, food, selfie, document-photo). For screenshots, describe the app or context (e.g., Safari, Terminal, Slack, VS Code, browser, lock-screen, menu-bar). The subcategory will be added as a Finder tag.`

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
			images = append(images, base64.StdEncoding.EncodeToString(b))
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

func (c *Client) requestGenerate(ollamaURL, model, prompt string, images []string) (map[string]any, error) {
	body := map[string]any{
		"model": model,
		"system": ("You classify files for a macOS file manager. " +
			"Output valid JSON only with keys category, subcategory, tags, action, destination, reason. " +
			"No markdown, no code fences, no extra text."),
		"prompt": prompt,
		"images": images,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 512,
			"num_ctx":     8192,
		},
	}
	return c.postJSON(context.Background(), ollamaURL+"/api/generate", body, 120*time.Second)
}

func (c *Client) requestChat(ollamaURL, model, prompt string, images []string, categories map[string]bool) (map[string]any, error) {
	catList := make([]string, 0, len(categories))
	for k := range categories {
		catList = append(catList, k)
	}
	sort.Strings(catList)
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role": "system",
				"content": ("You classify files for a macOS file manager. Use the classify_file tool. " +
					"If the category is clear, action should be move. " +
					"Use delete only for obvious trash, installers, or duplicates. " +
					"Use review only when ambiguous, sensitive, or unclassifiable."),
			},
			{
				"role":    "user",
				"content": prompt,
				"images":  images,
			},
		},
		"tools":  []map[string]any{toolSchema(catList)},
		"stream": false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 512,
			"num_ctx":     8192,
		},
	}
	return c.postJSON(context.Background(), ollamaURL+"/api/chat", body, 120*time.Second)
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
						"type": "string",
						"enum": []string{"move", "delete", "review"},
					},
					"reason": map[string]any{"type": "string"},
				},
				"required": []string{"category", "tags", "action", "reason"},
			},
		},
	}
}

func (c *Client) postJSON(ctx context.Context, url string, body any, timeout time.Duration) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	transport := c.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func parseResponse(data map[string]any, categories map[string]bool) (Decision, bool) {
	if msgRaw, ok := data["message"]; ok {
		if msg, ok := msgRaw.(map[string]any); ok {
			if tcRaw, ok := msg["tool_calls"]; ok {
				if tcList, ok := tcRaw.([]any); ok && len(tcList) > 0 {
					if tc, ok := tcList[0].(map[string]any); ok {
						if fnRaw, ok := tc["function"]; ok {
							if fn, ok := fnRaw.(map[string]any); ok {
								if argsRaw, ok := fn["arguments"]; ok {
									args, ok := argsRaw.(map[string]any)
									if !ok {
										if s, ok := argsRaw.(string); ok {
											var parsed map[string]any
											if err := json.Unmarshal([]byte(s), &parsed); err == nil {
												args = parsed
											}
										}
									}
									if args != nil {
										return buildDecision(args, categories, ""), true
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if respRaw, ok := data["response"]; ok {
		if resp, ok := respRaw.(string); ok {
			if parsed, ok := extractJSON(resp); ok {
				dest, _ := stringField(parsed, "destination")
				return buildDecision(parsed, categories, dest), true
			}
		}
	}

	return Decision{}, false
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

func buildDecision(m map[string]any, categories map[string]bool, destination string) Decision {
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
	return d
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
