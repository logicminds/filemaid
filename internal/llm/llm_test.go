package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/directory"
)

// toChatResponse converts the map[string]any test fixtures used by older tests
// into the concrete chatResponse type expected by parseResponse.
func toChatResponse(m map[string]any) chatResponse {
	var resp chatResponse
	if respRaw, ok := m["response"].(string); ok {
		resp.Response = respRaw
	}
	if msgRaw, ok := m["message"].(map[string]any); ok {
		if role, ok := msgRaw["role"].(string); ok {
			resp.Message.Role = role
		}
		if content, ok := msgRaw["content"].(string); ok {
			resp.Message.Content = content
		}
		if thinking, ok := msgRaw["thinking"].(string); ok {
			resp.Message.Thinking = thinking
		}
		if tcRaw, ok := msgRaw["tool_calls"].([]any); ok {
			for _, tc := range tcRaw {
				if tcMap, ok := tc.(map[string]any); ok {
					var call toolCall
					if fnRaw, ok := tcMap["function"].(map[string]any); ok {
						if name, ok := fnRaw["name"].(string); ok {
							call.Function.Name = name
						}
						if args, ok := fnRaw["arguments"].(string); ok {
							call.Function.Arguments = json.RawMessage(args)
						} else if argsMap, ok := fnRaw["arguments"].(map[string]any); ok {
							b, _ := json.Marshal(argsMap)
							call.Function.Arguments = b
						}
					}
					resp.Message.ToolCalls = append(resp.Message.ToolCalls, call)
				}
			}
		}
	}
	return resp
}

func baseConfig(t *testing.T, tmp string) *config.Config {
	t.Helper()
	return &config.Config{
		OllamaURL:         "http://localhost:11434",
		Model:             "dummy",
		MaxImageDimension: 1024,
		Categories: map[string]string{
			"Screenshots": filepath.Join(tmp, "Screenshots"),
			"Documents":   filepath.Join(tmp, "Documents"),
			"Images":      filepath.Join(tmp, "Images"),
			"Unknown":     filepath.Join(tmp, "review"),
		},
	}
}

func TestDecisionDefaults(t *testing.T) {
	d := NewDecision()
	if d.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", d.Category)
	}
	if d.Action != "review" {
		t.Errorf("Action = %q, want review", d.Action)
	}
	if len(d.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", d.Tags)
	}
	if d.Destination != "" {
		t.Errorf("Destination = %q, want empty", d.Destination)
	}
}

func TestExtractJSONPlain(t *testing.T) {
	raw := `{"category": "Images", "action": "move"}`
	got, ok := extractJSON(raw)
	if !ok {
		t.Fatal("expected ok")
	}
	if got["category"] != "Images" {
		t.Errorf("category = %v, want Images", got["category"])
	}
}

func TestExtractJSONWithMarkdownFence(t *testing.T) {
	raw := "```json\n{\"category\": \"Images\", \"action\": \"move\"}\n```"
	got, ok := extractJSON(raw)
	if !ok {
		t.Fatal("expected ok")
	}
	if got["category"] != "Images" {
		t.Errorf("category = %v, want Images", got["category"])
	}
}

func TestExtractJSONInvalidReturnsNone(t *testing.T) {
	_, ok := extractJSON("not json")
	if ok {
		t.Error("expected not ok")
	}
}

func TestParseResponseGenerateStyle(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Images", "tags": ["a"], "action": "move", "reason": "x"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Images" {
		t.Errorf("Category = %q, want Images", decision.Category)
	}
	if decision.Action != "move" {
		t.Errorf("Action = %q, want move", decision.Action)
	}
	if len(decision.Tags) != 1 || decision.Tags[0] != "a" {
		t.Errorf("Tags = %v, want [a]", decision.Tags)
	}
}

func TestParseResponseToolCallStyle(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"message": map[string]any{
			"tool_calls": []any{
				map[string]any{
					"function": map[string]any{
						"arguments": map[string]any{
							"category": "Documents",
							"tags":     []any{"doc"},
							"action":   "move",
							"reason":   "text file",
						},
					},
				},
			},
		},
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
	if len(decision.Tags) != 1 || decision.Tags[0] != "doc" {
		t.Errorf("Tags = %v, want [doc]", decision.Tags)
	}
}

func TestParseResponseToolCallArgumentsAsString(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"message": map[string]any{
			"tool_calls": []any{
				map[string]any{
					"function": map[string]any{
						"arguments": `{"category": "Documents", "tags": ["doc"], "action": "move", "reason": "text file"}`,
					},
				},
			},
		},
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
}
func TestParseResponseMessageContentJSON(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"message": map[string]any{
			"content": `{"category": "Images", "subcategory": "cat", "tags": ["photo"], "action": "move", "destination": "", "reason": "photo"}`,
		},
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Images" {
		t.Errorf("Category = %q, want Images", decision.Category)
	}
	if decision.Subcategory != "cat" {
		t.Errorf("Subcategory = %q, want cat", decision.Subcategory)
	}
	if len(decision.Tags) != 1 || decision.Tags[0] != "photo" {
		t.Errorf("Tags = %v, want [photo]", decision.Tags)
	}
}

func TestParseResponseMessageThinkingJSON(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"message": map[string]any{
			"thinking": `Some reasoning here... {"category": "Screenshots", "subcategory": "browser", "tags": ["web"], "action": "move", "destination": "", "reason": "webpage screenshot"}`,
		},
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Screenshots" {
		t.Errorf("Category = %q, want Screenshots", decision.Category)
	}
	if decision.Action != "move" {
		t.Errorf("Action = %q, want move", decision.Action)
	}
}

func TestParseResponseMessageContentFencedJSON(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"message": map[string]any{
			"content": "```json\n{\"category\": \"Documents\", \"tags\": [\" fenced\"], \"action\": \"move\", \"reason\": \"x\"}\n```",
		},
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
}

func TestParseResponseUnknownCategoryCoerced(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Banana", "tags": [], "action": "move", "reason": "x"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", decision.Category)
	}
}

func TestParseResponseInvalidActionCoerced(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Images", "tags": [], "action": "explode", "reason": "x"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Action != "review" {
		t.Errorf("Action = %q, want review", decision.Action)
	}
}

func TestParseResponseSubcategory(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Images", "subcategory": "cat", "tags": ["photo"], "action": "move", "reason": "x"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Subcategory != "cat" {
		t.Errorf("Subcategory = %q, want cat", decision.Subcategory)
	}
}
func TestParseResponseNewNameAndQuality(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Documents", "tags": ["receipt"], "action": "move", "reason": "organized", "new_name": "Grocery Receipt.pdf", "name_quality": 2}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "/tmp/scan.pdf")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.NewName != "Grocery Receipt.pdf" {
		t.Errorf("NewName = %q, want Grocery Receipt.pdf", decision.NewName)
	}
	if decision.NameQuality != 2 {
		t.Errorf("NameQuality = %d, want 2", decision.NameQuality)
	}
}

func TestParseResponseNewNameExtensionCorrection(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x", "new_name": "Report.txt", "name_quality": 1}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "/tmp/Report.PDF")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.NewName != "Report.PDF" {
		t.Errorf("NewName = %q, want Report.PDF", decision.NewName)
	}
}

func TestParseResponseNewNameAddsMissingExtension(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Images", "tags": [], "action": "move", "reason": "x", "new_name": "Birthday", "name_quality": 4}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "/tmp/IMG_1234.JPG")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.NewName != "Birthday.JPG" {
		t.Errorf("NewName = %q, want Birthday.JPG", decision.NewName)
	}
}

func TestParseResponseNameQualityClamping(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	for _, tc := range []struct {
		in, want int
	}{
		{-3, 1},
		{0, 1},
		{1, 1},
		{5, 5},
		{8, 5},
		{10, 5},
	} {
		data := map[string]any{
			"response": fmt.Sprintf(`{"category": "Unknown", "tags": [], "action": "review", "reason": "x", "name_quality": %d}`, tc.in),
		}
		decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
		if !ok {
			t.Fatalf("expected ok for quality %d", tc.in)
		}
		if decision.NameQuality != tc.want {
			t.Errorf("NameQuality for input %d = %d, want %d", tc.in, decision.NameQuality, tc.want)
		}
	}
}

func TestParseResponseMissingRenameFieldsDefaults(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "/tmp/file.txt")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.NewName != "" {
		t.Errorf("NewName = %q, want empty", decision.NewName)
	}
	if decision.NameQuality != 0 {
		t.Errorf("NameQuality = %d, want 0", decision.NameQuality)
	}
}

func TestParseResponseInvalidNameQualityIgnored(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x", "name_quality": "high"}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.NameQuality != 0 {
		t.Errorf("NameQuality = %d, want 0 for invalid type", decision.NameQuality)
	}
}
func TestToolSchemaIncludesRenameFields(t *testing.T) {
	schema := toolSchema([]string{"Images"})
	fn, ok := schema["function"].(map[string]any)
	if !ok {
		t.Fatal("missing function map")
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("missing parameters map")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties map")
	}
	if _, ok := props["new_name"]; !ok {
		t.Error("missing new_name property")
	}
	if q, ok := props["name_quality"].(map[string]any); ok {
		if q["type"] != "integer" {
			t.Errorf("name_quality type = %v, want integer", q["type"])
		}
		if q["minimum"] != 1 {
			t.Errorf("name_quality minimum = %v, want 1", q["minimum"])
		}
		if q["maximum"] != 5 {
			t.Errorf("name_quality maximum = %v, want 5", q["maximum"])
		}
	} else {
		t.Error("missing name_quality property")
	}
}

func TestPromptIncludesRenameFields(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)
	path := filepath.Join(tmp, "IMG_0001.jpg")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt, _, err := buildPrompt(path, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "new_name") {
		t.Error("prompt missing new_name")
	}
	if !strings.Contains(prompt, "name_quality") {
		t.Error("prompt missing name_quality")
	}
	if !strings.Contains(prompt, "preserve the original extension") {
		t.Error("prompt missing extension preservation instruction")
	}
}

func TestBuildPromptIncludesSubcategoryForImages(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)
	cfg.SubcategorizeImages = true

	img := filepath.Join(tmp, "cat.png")
	if err := os.WriteFile(img, []byte("fake-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) == 0 {
		t.Error("expected image base64 payload")
	}
	if !strings.Contains(prompt, "For image files, also provide") {
		t.Errorf("prompt missing subcategory instructions")
	}
}

func TestBuildPromptOmitsSubcategoryWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)
	cfg.SubcategorizeImages = false

	img := filepath.Join(tmp, "cat.png")
	if err := os.WriteFile(img, []byte("fake-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, _, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "For image files, also provide") {
		t.Errorf("prompt should not contain subcategory instructions when disabled")
	}
}

type fakeTransport struct {
	handler func(req *http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.handler(req)
}

func readRequestBody(req *http.Request) map[string]any {
	data, _ := io.ReadAll(req.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	return body
}

// chatImageCount returns the number of image payloads attached to the user
// message in an /api/chat request body.
func chatImageCount(body map[string]any) int {
	msgs, ok := body["messages"].([]any)
	if !ok {
		return 0
	}
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		imgs, _ := msg["images"].([]any)
		return len(imgs)
	}
	return 0
}

func jsonResponse(body map[string]any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
	}
}

// modelListResponse returns an Ollama /api/tags response containing the
// configured model so checkModel passes.
func modelListResponse(model string) *http.Response {
	return jsonResponse(map[string]any{
		"models": []any{
			map[string]any{"name": model},
		},
	})
}

// fakeDecisionCache is an in-memory DecisionCache for tests.
type fakeDecisionCache struct {
	mu        sync.Mutex
	decisions map[string]Decision
}

func (f *fakeDecisionCache) FindDecisionByHash(sha256 string) (Decision, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decisions == nil {
		return Decision{}, false, nil
	}
	d, ok := f.decisions[sha256]
	return d, ok, nil
}

func (f *fakeDecisionCache) RecordDecision(sha256 string, decision Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decisions == nil {
		f.decisions = make(map[string]Decision)
	}
	f.decisions[sha256] = decision
	return nil
}

// fakeDirectoryDecisionCache is an in-memory DirectoryDecisionCache for tests.
type fakeDirectoryDecisionCache struct {
	mu        sync.Mutex
	decisions map[string]DirectoryDecision
}

func (f *fakeDirectoryDecisionCache) FindDirectoryDecision(key string) (DirectoryDecision, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decisions == nil {
		return DirectoryDecision{}, false, nil
	}
	d, ok := f.decisions[key]
	return d, ok, nil
}

func (f *fakeDirectoryDecisionCache) RecordDirectoryDecision(key string, decision DirectoryDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decisions == nil {
		f.decisions = make(map[string]DirectoryDecision)
	}
	f.decisions[key] = decision
	return nil
}

func TestDirectoryDecisionDefaults(t *testing.T) {
	d := NewDirectoryDecision()
	if d.Recommendation != "review" {
		t.Errorf("Recommendation = %q, want review", d.Recommendation)
	}
	if d.Reason != "" {
		t.Errorf("Reason = %q, want empty", d.Reason)
	}
	if d.Category != "" {
		t.Errorf("Category = %q, want empty", d.Category)
	}
	if len(d.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", d.Tags)
	}
}

func TestParseDirectoryResponseToolCall(t *testing.T) {
	resp := chatResponse{
		Message: chatMessage{
			Role: "assistant",
			ToolCalls: []toolCall{
				{
					Function: functionCall{
						Name:      "classify_directory",
						Arguments: json.RawMessage(`{"recommendation": "archive", "reason": "old project", "category": "Projects", "tags": ["code", "backup"]}`),
					},
				},
			},
		},
	}
	got := parseDirectoryResponse(resp)
	if got.Recommendation != "archive" {
		t.Errorf("Recommendation = %q, want archive", got.Recommendation)
	}
	if got.Reason != "old project" {
		t.Errorf("Reason = %q, want old project", got.Reason)
	}
	if got.Category != "Projects" {
		t.Errorf("Category = %q, want Projects", got.Category)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "code" || got.Tags[1] != "backup" {
		t.Errorf("Tags = %v, want [code backup]", got.Tags)
	}
}

func TestParseDirectoryResponseGenerateStyle(t *testing.T) {
	resp := chatResponse{
		Response: `{"recommendation": "trash", "reason": "node_modules cache", "category": "Cache", "tags": ["npm"]}`,
	}
	got := parseDirectoryResponse(resp)
	if got.Recommendation != "trash" {
		t.Errorf("Recommendation = %q, want trash", got.Recommendation)
	}
	if got.Reason != "node_modules cache" {
		t.Errorf("Reason = %q, want node_modules cache", got.Reason)
	}
	if got.Category != "Cache" {
		t.Errorf("Category = %q, want Cache", got.Category)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "npm" {
		t.Errorf("Tags = %v, want [npm]", got.Tags)
	}
}

func TestParseDirectoryResponseInvalidDefaultsToReview(t *testing.T) {
	cases := []chatResponse{
		{Response: "not json"},
		{Response: `{"recommendation": "delete", "reason": "invalid action"}`},
		{Response: `{"reason": "missing recommendation"}`},
	}
	for i, resp := range cases {
		got := parseDirectoryResponse(resp)
		if got.Recommendation != "review" {
			t.Errorf("case %d: Recommendation = %q, want review", i, got.Recommendation)
		}
	}
}

func TestBuildDirectoryPrompt(t *testing.T) {
	meta := &directory.Metadata{
		Path:       "/downloads/project",
		Base:       "project",
		Size:       1234,
		ChildCount: 5,
		Mtime:      time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Extensions: map[string]int{".go": 2, ".md": 1},
		Markers:    []string{".git"},
	}
	cfg := &config.Config{}
	prompt := buildDirectoryPrompt(meta, cfg)

	for _, want := range []string{"/downloads/project", "project", "1234", "5", "2024-01-02T03:04:05", ".git", ".go:2", ".md:1"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestDirectoryCacheKeyStability(t *testing.T) {
	meta := &directory.Metadata{
		Path:       "/downloads/project",
		Base:       "project",
		Size:       100,
		ChildCount: 2,
		Mtime:      time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Extensions: map[string]int{".go": 1, ".md": 1},
	}
	key1 := directoryCacheKey(meta)
	key2 := directoryCacheKey(meta)
	if key1 == "" {
		t.Fatal("expected non-empty key")
	}
	if key1 != key2 {
		t.Fatalf("keys differ: %q vs %q", key1, key2)
	}

	meta.Size = 200
	key3 := directoryCacheKey(meta)
	if key1 == key3 {
		t.Error("expected key to change when size changes")
	}
}

func TestClassifyDirectoryUsesChatEndpoint(t *testing.T) {
	cfg := &config.Config{
		OllamaURL: "http://localhost:11434",
		Model:     "dummy",
	}
	meta := &directory.Metadata{
		Path:       t.TempDir(),
		Base:       "testdir",
		Size:       100,
		ChildCount: 0,
	}

	var calledURL string
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			calledURL = req.URL.String()
			if strings.HasSuffix(calledURL, "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			body := readRequestBody(req)
			if body["model"] != "dummy" {
				t.Errorf("model = %v, want dummy", body["model"])
			}
			tools, ok := body["tools"].([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("expected 1 tool, got %v", body["tools"])
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"recommendation": "archive",
									"reason":         "old project",
									"category":       "Projects",
									"tags":           []any{"code"},
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	decision, _, err := client.ClassifyDirectory(context.Background(), meta, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Recommendation != "archive" {
		t.Errorf("Recommendation = %q, want archive", decision.Recommendation)
	}
	if !strings.HasSuffix(calledURL, "/api/chat") {
		t.Errorf("URL = %q, want /api/chat suffix", calledURL)
	}
}

func TestClassifyDirectoryCache(t *testing.T) {
	cfg := &config.Config{
		OllamaURL: "http://localhost:11434",
		Model:     "dummy",
	}
	meta := &directory.Metadata{
		Path:       "/downloads/project",
		Base:       "project",
		Size:       100,
		ChildCount: 0,
	}

	cache := &fakeDirectoryDecisionCache{}
	key := directoryCacheKey(meta)
	if err := cache.RecordDirectoryDecision(key, DirectoryDecision{Recommendation: "keep", Reason: "cached"}); err != nil {
		t.Fatal(err)
	}

	var chatCalls int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			chatCalls++
			t.Errorf("unexpected /api/chat call")
			return nil, io.EOF
		},
	}

	client := NewClient(transport)
	client.SetDirectoryDecisionCache(cache)
	decision, _, err := client.ClassifyDirectory(context.Background(), meta, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Recommendation != "keep" {
		t.Errorf("Recommendation = %q, want keep", decision.Recommendation)
	}
	if chatCalls != 0 {
		t.Errorf("expected 0 /api/chat calls, got %d", chatCalls)
	}
}

func TestClassifyDirectoryRecordsDecisionInCache(t *testing.T) {
	cfg := &config.Config{
		OllamaURL: "http://localhost:11434",
		Model:     "dummy",
	}
	meta := &directory.Metadata{
		Path:       "/downloads/project",
		Base:       "project",
		Size:       100,
		ChildCount: 0,
	}

	cache := &fakeDirectoryDecisionCache{}
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"recommendation": "trash",
									"reason":         "temp files",
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	client.SetDirectoryDecisionCache(cache)
	if _, _, err := client.ClassifyDirectory(context.Background(), meta, cfg); err != nil {
		t.Fatal(err)
	}

	key := directoryCacheKey(meta)
	d, ok, err := cache.FindDirectoryDecision(key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected directory decision to be cached")
	}
	if d.Recommendation != "trash" {
		t.Errorf("cached Recommendation = %q, want trash", d.Recommendation)
	}
	if d.Reason != "temp files" {
		t.Errorf("cached Reason = %q, want temp files", d.Reason)
	}
}

func TestClassifyUsesChatEndpoint(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calledURL string
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			calledURL = req.URL.String()
			if strings.HasSuffix(calledURL, "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			body := readRequestBody(req)
			if body["model"] != "dummy" {
				t.Errorf("model = %v, want dummy", body["model"])
			}
			if opts, ok := body["options"].(map[string]any); ok {
				if opts["num_predict"] != float64(512) {
					t.Errorf("num_predict = %v, want 512", opts["num_predict"])
				}
				if opts["num_ctx"] != float64(4096) {
					t.Errorf("num_ctx = %v, want 4096", opts["num_ctx"])
				}
			} else {
				t.Error("missing options in request body")
			}
			if body["keep_alive"] != "5m" {
				t.Errorf("keep_alive = %v, want 5m", body["keep_alive"])
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"category": "Documents",
									"tags":     []any{"txt"},
									"action":   "move",
									"reason":   "text",
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
	if !strings.HasSuffix(calledURL, "/api/chat") {
		t.Errorf("URL = %q, want /api/chat suffix", calledURL)
	}
}

func TestClassifyReturnsMetrics(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/api/tags") {
				return jsonResponse(map[string]any{"models": []any{map[string]any{"name": "dummy"}}}), nil
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"category": "Documents",
									"tags":     []any{"txt"},
									"action":   "move",
									"reason":   "text",
								},
							},
						},
					},
				},
				"prompt_eval_count": 10,
				"eval_count":        5,
				"eval_duration":     500000000,
			}), nil
		},
	}

	client := NewClient(transport)
	_, metrics, err := client.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", metrics.PromptTokens)
	}
	if metrics.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", metrics.CompletionTokens)
	}
	if metrics.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", metrics.TotalTokens)
	}
	if metrics.ContextSize != DefaultContextSize {
		t.Errorf("ContextSize = %d, want %d", metrics.ContextSize, DefaultContextSize)
	}
	wantTPS := 10.0
	if metrics.TokensPerSec != wantTPS {
		t.Errorf("TokensPerSec = %v, want %v", metrics.TokensPerSec, wantTPS)
	}
	if metrics.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want non-negative", metrics.DurationMs)
	}
}

func TestClassifyDoesNotFallbackToGenerate(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				t.Errorf("unexpected /api/generate call; Classify should use only /api/chat")
			}
			return nil, io.EOF
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", decision.Category)
	}
	if decision.Action != "review" {
		t.Errorf("Action = %q, want review", decision.Action)
	}
	if !strings.Contains(decision.Reason, "EOF") {
		t.Errorf("Reason = %q, want EOF mentioned", decision.Reason)
	}
}

func TestClassifyFallsBackOnError(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			return nil, io.EOF
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", decision.Category)
	}
	if decision.Action != "review" {
		t.Errorf("Action = %q, want review", decision.Action)
	}
	if !strings.Contains(decision.Reason, "EOF") {
		t.Errorf("Reason = %q, want EOF mentioned", decision.Reason)
	}
}
func TestClassifyRetriesWithoutImagesOnFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	img := filepath.Join(tmp, "img.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	var imageCounts []int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			body := readRequestBody(req)
			count := chatImageCount(body)
			imageCounts = append(imageCounts, count)

			if count > 0 {
				return nil, errors.New("model does not support images")
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"category": "Images",
									"tags":     []any{"png"},
									"action":   "move",
									"reason":   "image",
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), img, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Images" {
		t.Errorf("Category = %q, want Images", decision.Category)
	}
	if len(imageCounts) != 2 {
		t.Errorf("expected 2 calls, got %d: %v", len(imageCounts), imageCounts)
	}
	if imageCounts[0] == 0 {
		t.Errorf("expected first call with images, got %v", imageCounts)
	}
	if imageCounts[1] != 0 {
		t.Errorf("expected second call without images, got %v", imageCounts)
	}
}

func TestClassifyDoesNotRetryWithoutImagesOnGenericError(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	img := filepath.Join(tmp, "img.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	var callCount int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			callCount++
			return nil, errors.New("ollama is offline")
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), img, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", decision.Category)
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 chat call for a generic error, got %d", callCount)
	}
	if !strings.Contains(decision.Reason, "ollama is offline") {
		t.Errorf("Reason = %q, want error mentioned", decision.Reason)
	}
}

func TestClassifySkipsHiddenFilesNoSpecialHandling(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, ".hidden")
	if err := os.WriteFile(textFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x"}`,
			}), nil
		},
	}

	client := NewClient(transport)
	decision, _, err := client.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
}

func TestBuildPromptIncludesTextSnippet(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(textFile, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want empty", images)
	}
	if !strings.Contains(prompt, "hello world") {
		t.Errorf("prompt missing snippet: %q", prompt)
	}
	if !strings.Contains(prompt, "Documents") {
		t.Errorf("prompt missing categories: %q", prompt)
	}
}

func TestBuildPromptIncludesImageBase64(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	img := filepath.Join(tmp, "img.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want 1", images)
	}
	if images[0] == "" {
		t.Error("image base64 is empty")
	}
	if !strings.Contains(prompt, "The image is attached") {
		t.Errorf("prompt missing image note: %q", prompt)
	}
}
func TestBuildPromptResizesLargeImage(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	// Create a 2000x1000 PNG image.
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	img := filepath.Join(tmp, "large.png")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, src); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want 1", images)
	}
	if !strings.Contains(prompt, "The image is attached") {
		t.Errorf("prompt missing image note")
	}

	decoded, err := base64.StdEncoding.DecodeString(images[0])
	if err != nil {
		t.Fatal(err)
	}
	resized, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	bounds := resized.Bounds()
	if bounds.Dx() > cfg.MaxImageDimension || bounds.Dy() > cfg.MaxImageDimension {
		t.Errorf("resized dimensions %dx%d exceed max %d", bounds.Dx(), bounds.Dy(), cfg.MaxImageDimension)
	}

	// Aspect ratio should be preserved: width still greater than height.
	if bounds.Dx() <= bounds.Dy() {
		t.Errorf("aspect ratio not preserved: got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestBuildPromptUsesConfiguredMaxImageDimension(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)
	cfg.MaxImageDimension = 128

	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	img := filepath.Join(tmp, "medium.png")
	if err := os.WriteFile(img, encodePNG(t, src), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want 1", images)
	}
	if !strings.Contains(prompt, "The image is attached") {
		t.Errorf("prompt missing image note")
	}

	decoded, err := base64.StdEncoding.DecodeString(images[0])
	if err != nil {
		t.Fatal(err)
	}
	resized, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	bounds := resized.Bounds()
	if bounds.Dx() > 128 || bounds.Dy() > 128 {
		t.Errorf("resized dimensions %dx%d exceed configured max 128", bounds.Dx(), bounds.Dy())
	}
	if bounds.Dx() <= bounds.Dy() {
		t.Errorf("aspect ratio not preserved: got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestBuildPromptEnforcesMinImageDimension(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)
	cfg.MaxImageDimension = 32 // below sane minimum

	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	img := filepath.Join(tmp, "medium.png")
	if err := os.WriteFile(img, encodePNG(t, src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want 1", images)
	}

	decoded, err := base64.StdEncoding.DecodeString(images[0])
	if err != nil {
		t.Fatal(err)
	}
	resized, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	bounds := resized.Bounds()
	if bounds.Dx() > 64 || bounds.Dy() > 64 {
		t.Errorf("resized dimensions %dx%d exceed enforced min max 64", bounds.Dx(), bounds.Dy())
	}
}

func encodePNG(t *testing.T, src image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildPromptDoesNotModifySourceImage(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	img := filepath.Join(tmp, "large.png")

	var before bytes.Buffer
	if err := png.Encode(io.Writer(&before), src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, before.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := buildPrompt(img, cfg, nil); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after) {
		t.Errorf("source image bytes changed on disk")
	}
}

func TestBuildPromptImageFallbackOnDecodeFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	img := filepath.Join(tmp, "fake.jpg")
	original := []byte("not-a-valid-jpeg")
	if err := os.WriteFile(img, original, 0o644); err != nil {
		t.Fatal(err)
	}

	_, images, err := buildPrompt(img, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images = %v, want 1", images)
	}
	got, err := base64.StdEncoding.DecodeString(images[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("fallback payload mismatch: got %q, want %q", got, original)
	}
}

func TestBuildPromptNonImageUnchanged(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	path := filepath.Join(tmp, "doc.txt")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	prompt, images, err := buildPrompt(path, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want empty", images)
	}
	if !strings.Contains(prompt, "First 2048 bytes") {
		t.Errorf("prompt missing text snippet: %q", prompt)
	}
}

func TestClassifierInterface(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x"}`,
			}), nil
		},
	}

	var classifier Classifier = NewClient(transport)
	decision, _, err := classifier.Classify(context.Background(), textFile, "", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}
}

func TestExtractJSONMissingBraces(t *testing.T) {
	_, ok := extractJSON("no braces here")
	if ok {
		t.Error("expected not ok")
	}
}

func TestExtractJSONTrailingGarbage(t *testing.T) {
	raw := `some text {"category": "Images", "action": "move"} more text`
	got, ok := extractJSON(raw)
	if !ok {
		t.Fatal("expected ok")
	}
	if got["category"] != "Images" {
		t.Errorf("category = %v, want Images", got["category"])
	}
}

func TestParseResponseMissingFieldsUsesDefaults(t *testing.T) {
	cfg := baseConfig(t, t.TempDir())
	data := map[string]any{
		"response": `{}`,
	}
	decision, ok := parseResponse(toChatResponse(data), categorySet(cfg.Categories), "")
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Category != "Unknown" {
		t.Errorf("Category = %q, want Unknown", decision.Category)
	}
	if decision.Action != "review" {
		t.Errorf("Action = %q, want review", decision.Action)
	}
}
func TestCheckModelMissingModel(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return jsonResponse(map[string]any{
					"models": []any{
						map[string]any{"name": "other-model"},
					},
				}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	_, _, err := client.Classify(context.Background(), "", "", cfg, nil)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	if !strings.Contains(err.Error(), "not found in Ollama") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateSucceedsWhenModelExistsAndGenerates(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse("filemaid-test"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				body := readRequestBody(req)
				if body["model"] != "filemaid-test" {
					t.Errorf("expected model filemaid-test, got %v", body["model"])
				}
				return jsonResponse(map[string]any{"response": "OK"}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestValidateFailsWhenModelMissing(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return jsonResponse(map[string]any{
					"models": []any{
						map[string]any{"name": "other-model"},
					},
				}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	if err := client.Validate(cfg); err == nil {
		t.Fatal("expected error when model is missing")
	}
}

func TestValidateFailsWhenImageModelMissing(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return jsonResponse(map[string]any{
					"models": []any{
						map[string]any{"name": "filemaid-test"},
						map[string]any{"name": "filemaid-metadata"},
					},
				}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	cfg.ImageModel = "filemaid-vision"
	cfg.TextModel = "filemaid-metadata"
	if err := client.Validate(cfg); err == nil {
		t.Fatal("expected error when image model is missing")
	}
}

func TestValidateFailsWhenGenerateErrors(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse("filemaid-test"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				return nil, errors.New("ollama generate failed")
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	err := client.Validate(cfg)
	if err == nil {
		t.Fatal("expected error when generate fails")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected validation failed error, got %v", err)
	}
}

func TestValidateUsesConfiguredOllamaURL(t *testing.T) {
	hosts := []string{}
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			hosts = append(hosts, req.URL.Host)
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse("filemaid-test"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				return jsonResponse(map[string]any{"response": "OK"}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	cfg.OllamaURL = "http://custom-ollama:11434"
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	for _, host := range hosts {
		if host != "custom-ollama:11434" {
			t.Errorf("expected requests to custom-ollama:11434, got %s", host)
		}
	}
}

func TestModelMatch(t *testing.T) {
	cases := []struct {
		want    string
		have    string
		matched bool
	}{
		{"filemaid-test", "filemaid-test", true},
		{"filemaid-test", "filemaid-test:latest", true},
		{"filemaid-test", "filemaid-test:v2", true},
		{"filemaid-test:latest", "filemaid-test:latest", true},
		{"filemaid-test:v2", "filemaid-test:v2", true},
		{"filemaid-test:v2", "filemaid-test:latest", false},
		{"filemaid-test", "other-model:latest", false},
		{"filemaid", "filemaid2:latest", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s", tc.want, tc.have), func(t *testing.T) {
			got := modelMatch(tc.want, tc.have)
			if got != tc.matched {
				t.Errorf("modelMatch(%q, %q) = %v, want %v", tc.want, tc.have, got, tc.matched)
			}
		})
	}
}

func TestValidateSucceedsWhenModelHasImplicitLatestTag(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse("filemaid-test:latest"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				return jsonResponse(map[string]any{"response": "OK"}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestValidateSucceedsWhenModelHasDifferentTag(t *testing.T) {
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse("filemaid-test:v2"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				return jsonResponse(map[string]any{"response": "OK"}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestCheckModelCached(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var tagsCalls int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				tagsCalls++
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"category": "Documents",
									"tags":     []any{"txt"},
									"action":   "move",
									"reason":   "text",
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	if _, _, err := client.Classify(context.Background(), textFile, "", cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.Classify(context.Background(), textFile, "", cfg, nil); err != nil {
		t.Fatal(err)
	}
	if tagsCalls != 1 {
		t.Errorf("expected 1 /api/tags call, got %d", tagsCalls)
	}
}

func TestCheckModelCacheInvalidatedOnModelChange(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var tagsCalls int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				tagsCalls++
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"response": `{"category": "Documents", "tags": [], "action": "move", "reason": "x"}`,
			}), nil
		},
	}

	client := NewClient(transport)
	if _, _, err := client.Classify(context.Background(), textFile, "", cfg, nil); err != nil {
		t.Fatal(err)
	}

	cfg.Model = "other-model"
	if _, _, err := client.Classify(context.Background(), textFile, "", cfg, nil); err != nil {
		t.Fatal(err)
	}

	if tagsCalls != 2 {
		t.Errorf("expected 2 /api/tags calls after model change, got %d", tagsCalls)
	}
}

func TestClassifyHashCache(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := &fakeDecisionCache{}
	if err := cache.RecordDecision("hash1", Decision{
		Category: "Images",
		Action:   "move",
		Reason:   "cached",
	}); err != nil {
		t.Fatal(err)
	}

	var tagsCalls, chatCalls int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				tagsCalls++
				return modelListResponse(cfg.Model), nil
			}
			chatCalls++
			t.Errorf("unexpected /api/chat call")
			return nil, io.EOF
		},
	}

	client := NewClient(transport)
	client.SetDecisionCache(cache)

	decision, _, err := client.Classify(context.Background(), textFile, "hash1", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Images" {
		t.Errorf("Category = %q, want Images", decision.Category)
	}
	if tagsCalls != 0 {
		t.Errorf("expected 0 /api/tags calls, got %d", tagsCalls)
	}
	if chatCalls != 0 {
		t.Errorf("expected 0 /api/chat calls, got %d", chatCalls)
	}
}

func TestClassifyRecordsDecisionInCache(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := &fakeDecisionCache{}
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			return jsonResponse(map[string]any{
				"message": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"function": map[string]any{
								"arguments": map[string]any{
									"category": "Documents",
									"tags":     []any{"txt"},
									"action":   "move",
									"reason":   "text",
								},
							},
						},
					},
				},
			}), nil
		},
	}

	client := NewClient(transport)
	client.SetDecisionCache(cache)

	if _, _, err := client.Classify(context.Background(), textFile, "hash1", cfg, nil); err != nil {
		t.Fatal(err)
	}

	d, ok, err := cache.FindDecisionByHash("hash1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected decision to be cached")
	}
	if d.Category != "Documents" {
		t.Errorf("cached Category = %q, want Documents", d.Category)
	}
	if d.Action != "move" {
		t.Errorf("cached Action = %q, want move", d.Action)
	}
}

func TestValidateUsesCachedModelCheck(t *testing.T) {
	var tagsCalls int
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				tagsCalls++
				return modelListResponse("filemaid-test"), nil
			}
			if strings.HasSuffix(req.URL.String(), "/api/generate") {
				return jsonResponse(map[string]any{"response": "OK"}), nil
			}
			return jsonResponse(map[string]any{}), nil
		},
	}

	client := NewClient(transport)
	cfg := baseConfig(t, t.TempDir())
	cfg.Model = "filemaid-test"
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("first Validate failed: %v", err)
	}
	if err := client.Validate(cfg); err != nil {
		t.Fatalf("second Validate failed: %v", err)
	}
	if tagsCalls != 1 {
		t.Errorf("expected 1 /api/tags call, got %d", tagsCalls)
	}
}
