package bridge

import (
	"encoding/json"
	"regexp"
	"strings"
)

var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// StripThinkTags removes all <think>...</think> blocks from s.
// Handles multiline content and multiple blocks.
func StripThinkTags(s string) string {
	return thinkTagRe.ReplaceAllString(s, "")
}

// StripThinkTagsFromChatResponse strips <think> tags from
// choices[].message.content in an OpenAI-format chat completion
// JSON response. Unknown fields are preserved. If the input is
// not valid JSON, it is returned unchanged.
func StripThinkTagsFromChatResponse(data []byte) []byte {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return data
	}
	rawChoices, ok := resp["choices"]
	if !ok {
		return data
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(rawChoices, &choices); err != nil {
		return data
	}

	modified := false
	for i, choice := range choices {
		rawMsg, ok := choice["message"]
		if !ok {
			continue
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMsg, &message); err != nil {
			continue
		}
		rawContent, ok := message["content"]
		if !ok {
			continue
		}
		var content string
		if err := json.Unmarshal(rawContent, &content); err != nil {
			continue
		}
		stripped := StripThinkTags(content)
		if stripped == content {
			continue
		}
		modified = true
		encoded, err := json.Marshal(stripped)
		if err != nil {
			continue
		}
		message["content"] = encoded
		msgBytes, err := json.Marshal(message)
		if err != nil {
			continue
		}
		choices[i]["message"] = msgBytes
	}

	if !modified {
		return data
	}

	choicesBytes, err := json.Marshal(choices)
	if err != nil {
		return data
	}
	resp["choices"] = choicesBytes
	result, err := json.Marshal(resp)
	if err != nil {
		return data
	}
	return result
}

// ThinkFilter tracks state across streaming SSE chunks to strip
// <think>...</think> content that may span multiple chunks.
type ThinkFilter struct {
	inside bool   // currently inside a <think> block
	buf    string // partial tag buffer for cross-chunk detection
}

func NewThinkFilter() *ThinkFilter {
	return &ThinkFilter{}
}

// ProcessSSELine handles one SSE line (including the "data: " prefix).
// Returns the processed line to forward, or "" if the line should be
// suppressed (e.g. chunk contained only think content).
func (f *ThinkFilter) ProcessSSELine(line string) string {
	if !strings.HasPrefix(line, "data: ") {
		return line
	}
	payload := strings.TrimPrefix(line, "data: ")

	if payload == "[DONE]" {
		return line
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content *string `json:"content"`
				Role    string  `json:"role"`
			} `json:"delta"`
			Index int `json:"index"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return line
	}
	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == nil {
		return line
	}

	content := *chunk.Choices[0].Delta.Content
	filtered := f.filter(content)

	if filtered == content {
		return line
	}

	if filtered == "" {
		return ""
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return line
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(raw["choices"], &choices); err != nil {
		return line
	}
	var delta map[string]json.RawMessage
	if err := json.Unmarshal(choices[0]["delta"], &delta); err != nil {
		return line
	}
	enc, _ := json.Marshal(filtered)
	delta["content"] = enc
	choices[0]["delta"], _ = json.Marshal(delta)
	raw["choices"], _ = json.Marshal(choices)
	result, _ := json.Marshal(raw)
	return "data: " + string(result)
}

// filter processes content through the state machine, stripping
// think-tag content even when tags span multiple chunks.
func (f *ThinkFilter) filter(content string) string {
	text := f.buf + content
	f.buf = ""

	var out strings.Builder

	for len(text) > 0 {
		if f.inside {
			closeIdx := strings.Index(text, "</think>")
			if closeIdx < 0 {
				partialClose := longestSuffix(text, "</think>")
				if partialClose > 0 {
					f.buf = text[len(text)-partialClose:]
				}
				return out.String()
			}
			text = text[closeIdx+len("</think>"):]
			f.inside = false
			continue
		}

		openIdx := strings.Index(text, "<think>")
		if openIdx < 0 {
			partialOpen := longestSuffix(text, "<think>")
			if partialOpen > 0 {
				out.WriteString(text[:len(text)-partialOpen])
				f.buf = text[len(text)-partialOpen:]
			} else {
				out.WriteString(text)
			}
			return out.String()
		}
		out.WriteString(text[:openIdx])
		text = text[openIdx+len("<think>"):]
		f.inside = true
	}

	return out.String()
}

// longestSuffix returns the length of the longest suffix of s that
// is a prefix of target. Used to detect partial tags at chunk boundaries.
func longestSuffix(s, target string) int {
	maxLen := len(target) - 1
	if maxLen > len(s) {
		maxLen = len(s)
	}
	for length := maxLen; length > 0; length-- {
		if strings.HasSuffix(s, target[:length]) {
			return length
		}
	}
	return 0
}
