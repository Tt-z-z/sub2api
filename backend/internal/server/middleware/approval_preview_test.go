package middleware

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApprovalPreviewTakesLastChineseText(t *testing.T) {
	body := []byte(`{"model":"ym-gpt-6-astra","messages":[{"role":"system","content":"前面的系统说明 sk-abcdefghijklmnopqrstuvwxyz"},{"role":"assistant","content":"中间回答"},{"role":"user","content":[{"type":"text","text":"第一句"},{"type":"text","text":"请看文件末尾这个函数"}]}]}`)
	got := approvalPreview(body)
	mustContain(t, got, "模型: ym-gpt-6-astra", "用户输入", "请看文件末尾这个函数")
	mustNotContain(t, got, "前面的系统说明", "第一句", "中间回答", "工具返回", "sk-abcdefghijklmnopqrstuvwxyz")
}

func TestApprovalPreviewEnglishFallsBackToLastContent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"system prompt"},{"role":"user","content":"latest question"}]}`)
	got := approvalPreview(body)
	mustContain(t, got, "latest question")
	mustNotContain(t, got, "system prompt")
}

func TestApprovalPreviewBareInputShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string", `{"input":"你好"}`, "你好"},
		{"item", `{"input":[{"type":"input_text","text":"画一只猫"}]}`, "画一只猫"},
		{"gemini", `{"contents":[{"role":"model","parts":[{"text":"模型回答"}]},{"role":"user","parts":[{"text":"解释这段"}]}]}`, "解释这段"},
		{"prompt", `{"prompt":"一只猫"}`, "一只猫"},
		{"embeddings", `{"input":["第一段","第二段"]}`, "第二段"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustContain(t, approvalPreview([]byte(tc.body)), tc.want)
		})
	}
}

func TestApprovalPreviewUnknownShapeIsExplicit(t *testing.T) {
	got := approvalPreview([]byte(`{"model":"x","tools":[],"metadata":{}}`))
	mustContain(t, got, "未解析到用户输入", "metadata", "model", "tools")
	mustNotContain(t, got, "用户输入\n")
}

func TestApprovalStoredPreviewKeepsRawTail(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"密钥 sk-abcdefghijklmnopqrstuvwxyz 然后是用户的话"}]}`)
	var packed struct {
		User string `json:"user"`
		Raw  string `json:"raw"`
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(approvalStoredPreview(body)), &packed) != nil {
		t.Fatal("stored preview is not json")
	}
	mustContain(t, packed.Text, "用户的话", "[密钥已隐藏]")
	mustNotContain(t, packed.Text, "sk-abcdefghijklmnopqrstuvwxyz")
	mustContain(t, packed.User, "用户的话")
	mustContain(t, packed.Raw, "messages", "[密钥已隐藏]")
	mustNotContain(t, packed.User, "sk-abcdefghijklmnopqrstuvwxyz")
	mustNotContain(t, packed.Raw, "sk-abcdefghijklmnopqrstuvwxyz")
	if !strings.Contains(packed.Raw, "用户的话") {
		t.Fatalf("raw missing user text: %s", packed.Raw)
	}
}

func mustContain(t *testing.T, got string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in %q", part, got)
		}
	}
}

func TestReplaceLastInputKeepsSurroundingJSON(t *testing.T) {
	body := []byte(`{"model":"ym-gpt-6-adtra","messages":[{"role":"system","content":"系统提示不要改"},{"role":"user","content":"<user_query>\n今天天气怎么样\n</user_query>"}]}`)
	next := "<user_query>\n晴天\n</user_query>"
	got, err := replaceLastInput(body, next)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(got), "系统提示不要改", "晴天", `"model":"ym-gpt-6-adtra"`)
	mustNotContain(t, string(got), "今天天气怎么样")
	if _, err := replaceLastInput(body, editableApprovalText(body)); err != nil || string(mustReplace(t, body, editableApprovalText(body))) != string(body) {
		t.Fatal("unchanged edit altered the request")
	}
	cleared, err := replaceLastInput(body, "")
	if err != nil || !strings.Contains(string(cleared), `"content":""`) || strings.Contains(string(cleared), "今天天气怎么样") {
		t.Fatalf("clear failed: %v %s", err, cleared)
	}
}

func mustReplace(t *testing.T, body []byte, next string) []byte {
	t.Helper()
	got, err := replaceLastInput(body, next)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustNotContain(t *testing.T, got string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(got, part) {
			t.Fatalf("unexpected %q in %q", part, got)
		}
	}
}
