package middleware

import "encoding/json"

// 缺失或错误类型也不允许以后台任务模式绕过取消。
func responseBackground(body []byte) bool {
	var data map[string]json.RawMessage
	if json.Unmarshal(body, &data) != nil {
		return true
	}
	if raw, ok := data["background"]; ok {
		var value bool
		if json.Unmarshal(raw, &value) != nil || value {
			return true
		}
	}
	return false
}
