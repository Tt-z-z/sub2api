package middleware

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	approvalPreviewRunes = 8000
	approvalRawBytes     = 120000
)

var previewSecret = regexp.MustCompile(`(?i)(?:sk-[A-Za-z0-9_\-]{8,}|Bearer\s+\S+|AIza[0-9A-Za-z_\-]{8,}|GOCSPX-\S+)`)

// approvalStoredPreview packs the extracted user text and a redacted tail of
// the original body. The audit stream never receives this value.
func approvalStoredPreview(body []byte) string {
	packed, err := json.Marshal(struct {
		User string `json:"user"`
		Raw  string `json:"raw"`
		Text string `json:"text,omitempty"`
	}{
		User: approvalPreview(body),
		Raw:  approvalRaw(body),
		Text: editableApprovalText(body),
	})
	if err != nil {
		return approvalPreview(body)
	}
	return string(packed)
}

func approvalPreview(body []byte) string {
	text := formatApprovalPreview(body)
	return previewSecret.ReplaceAllString(text, "[密钥已隐藏]")
}

func approvalRaw(body []byte) string {
	text := previewSecret.ReplaceAllString(string(body), "[密钥已隐藏]")
	if len(text) > approvalRawBytes {
		text = text[len(text)-approvalRawBytes:]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[1:]
		}
		text = "…仅显示请求末尾\n" + text
	}
	return text
}

func formatApprovalPreview(body []byte) string {
	var data map[string]json.RawMessage
	if json.Unmarshal(body, &data) != nil {
		return "未解析到用户输入。请求不是 JSON。"
	}
	text := lastInputText(body)
	if strings.TrimSpace(text) == "" {
		note := "未解析到用户输入。顶层字段: " + topLevelKeys(data)
		if head := modelLine(data["model"]); head != "" {
			return head + "\n\n" + note
		}
		return note
	}
	runes := []rune(text)
	if len(runes) > approvalPreviewRunes {
		text = "…用户输入过长，只保留末尾\n" + string(runes[len(runes)-approvalPreviewRunes:])
	}
	bodyText := "用户输入\n" + strings.TrimSpace(text)
	if head := modelLine(data["model"]); head != "" {
		return head + "\n\n" + bodyText
	}
	return bodyText
}

// lastInputText walks the JSON in order and returns the last Chinese string.
// English input still works: if nothing contains Han, the last content string is used.
func lastInputText(body []byte) string {
	candidates := make([]string, 0, 8)
	collectInputStrings(json.RawMessage(body), "", &candidates)
	for i := len(candidates) - 1; i >= 0; i-- {
		if containsHan(candidates[i]) {
			return candidates[i]
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return ""
}

func collectInputStrings(raw json.RawMessage, key string, out *[]string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	switch raw[0] {
	case '"':
		var text string
		if json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) == "" || skipInputKey(key) {
			return
		}
		if containsHan(text) || contentKey(key) {
			*out = append(*out, text)
		}
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return
		}
		for _, item := range items {
			collectInputStrings(item, key, out)
		}
	case '{':
		dec := json.NewDecoder(bytes.NewReader(raw))
		if _, err := dec.Token(); err != nil {
			return
		}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return
			}
			childKey, _ := token.(string)
			var child json.RawMessage
			if dec.Decode(&child) != nil {
				return
			}
			collectInputStrings(child, childKey, out)
		}
	}
}

func skipInputKey(key string) bool {
	switch strings.ToLower(key) {
	case "type", "role", "model", "name", "id", "call_id", "tool_call_id", "status", "mime_type", "encoding", "url":
		return true
	default:
		return false
	}
}

func contentKey(key string) bool {
	switch strings.ToLower(key) {
	case "text", "content", "input", "prompt":
		return true
	default:
		return false
	}
}

func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func modelLine(raw json.RawMessage) string {
	var model string
	if json.Unmarshal(raw, &model) == nil && strings.TrimSpace(model) != "" {
		return "模型: " + model
	}
	return ""
}

func topLevelKeys(data map[string]json.RawMessage) string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sortStrings(keys)
	if len(keys) == 0 {
		return "（空对象）"
	}
	if len(keys) > 12 {
		keys = keys[:12]
	}
	return strings.Join(keys, ", ")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
