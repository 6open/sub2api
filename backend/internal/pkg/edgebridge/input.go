package edgebridge

import (
	"encoding/json"
	"github.com/tidwall/gjson"
)

func ClientToolsOnly(data []byte) bool {
	var v struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	var check func([]map[string]json.RawMessage, int) bool
	check = func(tools []map[string]json.RawMessage, depth int) bool {
		if depth > 8 {
			return false
		}
		for _, tool := range tools {
			var kind string
			if json.Unmarshal(tool["type"], &kind) != nil {
				return false
			}
			switch kind {
			case "function", "custom":
			case "namespace":
				var children []map[string]json.RawMessage
				if json.Unmarshal(tool["tools"], &children) != nil || !check(children, depth+1) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	return check(v.Tools, 0)
}

// SupportedInput permits image inputs (including historical screenshots).
// HTTP ingress limits the entire JSON body, including base64, to MaxBody.
func SupportedInput(data []byte) bool {
	if len(data) > MaxBody {
		return false
	}
	if !json.Valid(data) {
		return false
	}
	var walk func(gjson.Result, int) bool
	walk = func(v gjson.Result, depth int) bool {
		if depth > 64 {
			return false
		}
		if v.IsObject() {
			switch v.Get("type").String() {
			case "input_audio", "input_file", "image_generation", "web_search", "web_search_preview", "computer_use_preview":
				return false
			}
		}
		ok := true
		if v.IsObject() || v.IsArray() {
			v.ForEach(func(_, child gjson.Result) bool { ok = walk(child, depth+1); return ok })
		}
		return ok
	}
	return walk(gjson.ParseBytes(data), 0)
}
