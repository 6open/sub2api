package edgegateway

import "encoding/json"

func effectiveOutputEvent(kind string, raw []byte) bool {
	var v struct {
		Delta string `json:"delta"`
		Item  struct {
			Type      string `json:"type"`
			Arguments string `json:"arguments"`
			Input     string `json:"input"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch kind {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		return v.Delta != ""
	case "response.output_item.done":
		return (v.Item.Type == "function_call" && v.Item.Arguments != "") || (v.Item.Type == "custom_tool_call" && v.Item.Input != "")
	}
	return false
}
