package subscription

import "encoding/json"

// jsonUnmarshal 是 encoding/json 的薄封装（保持包内调用简洁）。
func jsonUnmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}
