package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func baseConfig(t *testing.T, tmp string) *config.Config {
	t.Helper()
	return &config.Config{
		OllamaURL: "http://localhost:11434",
		Model:     "dummy",
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
	if !ok {
		t.Fatal("expected ok")
	}
	if decision.Subcategory != "cat" {
		t.Errorf("Subcategory = %q, want cat", decision.Subcategory)
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

	prompt, images, err := buildPrompt(img, cfg)
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

	prompt, _, err := buildPrompt(img, cfg)
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
	decision, err := client.Classify(textFile, cfg)
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

func TestClassifyUsesGenerateEndpoint(t *testing.T) {
	tmp := t.TempDir()
	cfg := baseConfig(t, tmp)

	textFile := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var urls []string
	transport := &fakeTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			urls = append(urls, req.URL.String())
			if strings.HasSuffix(req.URL.String(), "/api/tags") {
				return modelListResponse(cfg.Model), nil
			}
			body := readRequestBody(req)
			if strings.HasSuffix(req.URL.String(), "/api/chat") {
				if body["model"] != "dummy" {
					t.Errorf("chat model = %v, want dummy", body["model"])
				}
				return nil, io.EOF
			}
			return jsonResponse(map[string]any{
				"response": `{"category": "Documents", "tags": ["txt"], "action": "move", "reason": "text"}`,
			}), nil
		},
	}

	client := NewClient(transport)
	decision, err := client.Classify(textFile, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Documents" {
		t.Errorf("Category = %q, want Documents", decision.Category)
	}

	var generateCalled bool
	for _, u := range urls {
		if strings.HasSuffix(u, "/api/generate") {
			generateCalled = true
		}
	}
	if !generateCalled {
		t.Errorf("expected /api/generate to be called, got URLs %v", urls)
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
	decision, err := client.Classify(textFile, cfg)
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
			var count int
			if images, ok := body["images"].([]any); ok {
				count = len(images)
			}
			imageCounts = append(imageCounts, count)

			if count > 0 {
				return nil, io.EOF
			}
			return jsonResponse(map[string]any{
				"response": `{"category": "Images", "tags": [], "action": "move", "reason": "metadata"}`,
			}), nil
		},
	}

	client := NewClient(transport)
	decision, err := client.Classify(img, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Category != "Images" {
		t.Errorf("Category = %q, want Images", decision.Category)
	}

	foundZero := false
	for _, c := range imageCounts {
		if c == 0 {
			foundZero = true
		}
	}
	if !foundZero {
		t.Errorf("expected a call without images, got counts %v", imageCounts)
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
	decision, err := client.Classify(textFile, cfg)
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

	prompt, images, err := buildPrompt(textFile, cfg)
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

	prompt, images, err := buildPrompt(img, cfg)
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
	decision, err := classifier.Classify(textFile, cfg)
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
	decision, ok := parseResponse(data, categorySet(cfg.Categories))
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
	_, err := client.Classify("", cfg)
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
