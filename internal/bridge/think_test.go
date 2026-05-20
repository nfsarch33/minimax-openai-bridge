package bridge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStripThinkTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no tags", "hello world", "hello world"},
		{"empty string", "", ""},
		{"simple think block", "<think>reasoning here</think>answer", "answer"},
		{"multiline think block", "<think>\nstep 1\nstep 2\n</think>\nthe answer is 42", "\nthe answer is 42"},
		{"think block at end", "prefix<think>reasoning</think>", "prefix"},
		{"think block in middle", "before<think>hidden</think>after", "beforeafter"},
		{"multiple think blocks", "<think>first</think>middle<think>second</think>end", "middleend"},
		{"nested-looking tags", "<think>outer<think>inner</think>rest</think>visible", "rest</think>visible"},
		{"empty think block", "<think></think>content", "content"},
		{"only think block", "<think>all reasoning</think>", ""},
		{"whitespace around", "  <think>reason</think>  answer  ", "    answer  "},
		{"case sensitive", "<THINK>not stripped</THINK>", "<THINK>not stripped</THINK>"},
		{"partial open tag", "<thin>not a think tag</thin>", "<thin>not a think tag</thin>"},
		{"think with newlines in tag", "<think\n>bad tag</think>", "<think\n>bad tag</think>"},
		{"content with angle brackets", "<think>reasoning</think>use <b>bold</b> text", "use <b>bold</b> text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StripThinkTags(tt.input)
			if got != tt.want {
				t.Errorf("StripThinkTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripThinkTagsFromChatResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		response    string
		wantContent string
	}{
		{
			name:        "strips think tags from message content",
			response:    `{"id":"abc","choices":[{"index":0,"message":{"role":"assistant","content":"<think>let me reason</think>the answer"},"finish_reason":"stop"}]}`,
			wantContent: "the answer",
		},
		{
			name:        "preserves response without think tags",
			response:    `{"id":"abc","choices":[{"index":0,"message":{"role":"assistant","content":"clean response"},"finish_reason":"stop"}]}`,
			wantContent: "clean response",
		},
		{
			name:        "handles multiline think block in JSON",
			response:    `{"id":"abc","choices":[{"index":0,"message":{"role":"assistant","content":"<think>line1\nline2\nline3</think>final answer"},"finish_reason":"stop"}]}`,
			wantContent: "final answer",
		},
		{
			name:        "handles null content gracefully",
			response:    `{"id":"abc","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
			wantContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StripThinkTagsFromChatResponse([]byte(tt.response))

			var parsed struct {
				Choices []struct {
					Message struct {
						Content *string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(got, &parsed); err != nil {
				t.Fatalf("unmarshal result: %v\nraw: %s", err, got)
			}
			gotContent := ""
			if len(parsed.Choices) > 0 && parsed.Choices[0].Message.Content != nil {
				gotContent = *parsed.Choices[0].Message.Content
			}
			if gotContent != tt.wantContent {
				t.Errorf("content = %q, want %q\nraw: %s", gotContent, tt.wantContent, got)
			}
		})
	}
}

func TestStripThinkTagsFromChatResponsePreservesUnknownFields(t *testing.T) {
	t.Parallel()
	input := `{"id":"abc","custom_field":123,"choices":[{"index":0,"message":{"role":"assistant","content":"<think>r</think>answer","extra":"kept"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10}}`
	got := StripThinkTagsFromChatResponse([]byte(input))

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := parsed["custom_field"]; !ok {
		t.Error("custom_field lost")
	}
	if _, ok := parsed["usage"]; !ok {
		t.Error("usage lost")
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(parsed["choices"], &choices); err != nil {
		t.Fatalf("unmarshal choices: %v", err)
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(choices[0]["message"], &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if _, ok := msg["extra"]; !ok {
		t.Error("message.extra lost")
	}
}

func TestStripThinkTagsFromChatResponsePassthroughOnBadJSON(t *testing.T) {
	t.Parallel()
	bad := []byte(`not json at all`)
	got := StripThinkTagsFromChatResponse(bad)
	if !bytes.Equal(got, bad) {
		t.Errorf("expected passthrough for bad JSON, got %s", got)
	}
}

func TestThinkFilterStreamSimple(t *testing.T) {
	t.Parallel()

	chunks := []string{
		`{"id":"1","choices":[{"delta":{"role":"assistant","content":""},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"<think>"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"reasoning here"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"</think>"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"the answer"},"index":0}]}`,
	}

	var sseInput bytes.Buffer
	for _, c := range chunks {
		fmt.Fprintf(&sseInput, "data: %s\n\n", c)
	}
	sseInput.WriteString("data: [DONE]\n\n")

	var output bytes.Buffer
	f := NewThinkFilter()
	scanner := bufio.NewScanner(&sseInput)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		out := f.ProcessSSELine(line)
		if out != "" {
			output.WriteString(out + "\n\n")
		}
	}

	result := output.String()
	if strings.Contains(result, "reasoning here") {
		t.Errorf("think content leaked through: %s", result)
	}
	if !strings.Contains(result, "the answer") {
		t.Errorf("real content missing: %s", result)
	}
	if !strings.Contains(result, "[DONE]") {
		t.Errorf("[DONE] missing: %s", result)
	}
}

func TestThinkFilterStreamSplitAcrossChunks(t *testing.T) {
	t.Parallel()

	chunks := []string{
		`{"id":"1","choices":[{"delta":{"content":"<thi"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"nk>inside"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"</thi"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":"nk>visible"},"index":0}]}`,
	}

	var sseInput bytes.Buffer
	for _, c := range chunks {
		fmt.Fprintf(&sseInput, "data: %s\n\n", c)
	}
	sseInput.WriteString("data: [DONE]\n\n")

	f := NewThinkFilter()
	var contents []string
	scanner := bufio.NewScanner(&sseInput)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		out := f.ProcessSSELine(line)
		if out != "" && !strings.Contains(out, "[DONE]") {
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			data := strings.TrimPrefix(out, "data: ")
			if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
				contents = append(contents, chunk.Choices[0].Delta.Content)
			}
		}
	}

	joined := strings.Join(contents, "")
	if strings.Contains(joined, "inside") {
		t.Errorf("think content leaked: %q", joined)
	}
	if !strings.Contains(joined, "visible") {
		t.Errorf("visible content missing: %q", joined)
	}
}

func TestHandleChatCompletionsStripsThinkTags(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"index":0,"message":{"role":"assistant","content":"<think>step1\nstep2</think>the actual answer"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKey:         "test-key",
		Model:          "MiniMax-M2.7-highspeed",
		DefaultType:    "db",
		Timeout:        time.Second,
	}, upstream.Client(), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"MiniMax-M2.7-highspeed","messages":[{"role":"user","content":"hello"}]}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}
	content := resp.Choices[0].Message.Content
	if strings.Contains(content, "<think>") {
		t.Errorf("think tags not stripped: %q", content)
	}
	if content != "the actual answer" {
		t.Errorf("content = %q, want %q", content, "the actual answer")
	}
}

func TestHandleChatCompletionsStreaming(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["stream"] != true {
			t.Error("expected stream=true in upstream request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"1","choices":[{"delta":{"role":"assistant","content":""},"index":0}]}`,
			`{"id":"1","choices":[{"delta":{"content":"<think>"},"index":0}]}`,
			`{"id":"1","choices":[{"delta":{"content":"reasoning"},"index":0}]}`,
			`{"id":"1","choices":[{"delta":{"content":"</think>"},"index":0}]}`,
			`{"id":"1","choices":[{"delta":{"content":"real output"},"index":0}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKey:         "test-key",
		Model:          "MiniMax-M2.7-highspeed",
		DefaultType:    "db",
		Timeout:        5 * time.Second,
	}, upstream.Client(), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"MiniMax-M2.7-highspeed","messages":[],"stream":true}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	result := rec.Body.String()
	if strings.Contains(result, "reasoning") {
		t.Errorf("think content leaked in streaming: %s", result)
	}
	if !strings.Contains(result, "real output") {
		t.Errorf("real content missing in streaming: %s", result)
	}
	if !strings.Contains(result, "[DONE]") {
		t.Errorf("[DONE] missing in streaming: %s", result)
	}
}

func TestHandleChatCompletionsNoThinkTagsPassthrough(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"index":0,"message":{"role":"assistant","content":"clean response"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	srv := NewServer(Config{
		MiniMaxBaseURL: upstream.URL + "/v1",
		APIKey:         "test-key",
		Model:          "test",
		DefaultType:    "db",
		Timeout:        time.Second,
	}, upstream.Client(), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test","messages":[]}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "clean response") {
		t.Errorf("clean content lost: %s", body)
	}
}
