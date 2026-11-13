package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

const approvalEditRunes = approvalPreviewRunes

var errApprovalEdit = errors.New("approval edit cannot be applied")

// editableApprovalText is the exact user string an admin may replace.
// Text longer than the preview cap stays visible in truncated form but is not editable.
func editableApprovalText(body []byte) string {
	text := lastInputText(body)
	if text == "" || utf8.RuneCountInString(text) > approvalEditRunes {
		return ""
	}
	return previewSecret.ReplaceAllString(text, "[密钥已隐藏]")
}

// replaceLastInput swaps the same JSON string the approval preview selected.
// Surrounding bytes, including system instructions and earlier turns, stay intact.
func replaceLastInput(body []byte, next string) ([]byte, error) {
	if utf8.RuneCountInString(next) > approvalEditRunes || len(next) > 32_000 {
		return nil, errApprovalEdit
	}
	start, end, current, ok := lastInputSpan(body)
	if !ok || current != lastInputText(body) {
		return nil, errApprovalEdit
	}
	redacted := previewSecret.ReplaceAllString(current, "[密钥已隐藏]")
	if next == current || next == redacted {
		return body, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, errApprovalEdit
	}
	out := make([]byte, 0, len(body)-(end-start)+len(encoded))
	out = append(out, body[:start]...)
	out = append(out, encoded...)
	out = append(out, body[end:]...)
	if len(out) > 2*1024*1024 || !json.Valid(out) {
		return nil, errApprovalEdit
	}
	if !bytes.Equal(out[:start], body[:start]) || !bytes.Equal(out[start+len(encoded):], body[end:]) {
		return nil, errApprovalEdit
	}
	var got string
	if json.Unmarshal(out[start:start+len(encoded)], &got) != nil || got != next {
		return nil, errApprovalEdit
	}
	return out, nil
}

type inputSpan struct {
	start, end int
	text       string
	han        bool
}

func lastInputSpan(body []byte) (int, int, string, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var found []inputSpan
	if err := walkInputSpans(dec, body, "", &found); err != nil || len(found) == 0 {
		return 0, 0, "", false
	}
	chosen := -1
	for i := len(found) - 1; i >= 0; i-- {
		if found[i].han {
			chosen = i
			break
		}
	}
	if chosen < 0 {
		chosen = len(found) - 1
	}
	span := found[chosen]
	return span.start, span.end, span.text, true
}

func walkInputSpans(dec *json.Decoder, body []byte, key string, out *[]inputSpan) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case string:
		end := int(dec.InputOffset())
		start := openingJSONQuote(body, end)
		if start < 0 {
			return errApprovalEdit
		}
		var decoded string
		if json.Unmarshal(body[start:end], &decoded) != nil || decoded != value {
			return errApprovalEdit
		}
		if strings.TrimSpace(value) != "" && !skipInputKey(key) && (containsHan(value) || contentKey(key)) {
			*out = append(*out, inputSpan{start: start, end: end, text: value, han: containsHan(value)})
		}
	case json.Delim:
		if value == '{' {
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				child, _ := keyToken.(string)
				if err := walkInputSpans(dec, body, child, out); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
		} else if value == '[' {
			for dec.More() {
				if err := walkInputSpans(dec, body, key, out); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
		}
	}
	return nil
}

func openingJSONQuote(body []byte, end int) int {
	if end <= 1 || end > len(body) || body[end-1] != '"' {
		return -1
	}
	for i := end - 2; i >= 0; i-- {
		if body[i] != '"' {
			continue
		}
		slashes := 0
		for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 == 0 {
			return i
		}
	}
	return -1
}
