package edgegateway

import "testing"

func TestEffectiveOutput(t *testing.T) {
	for _, tc := range []struct {
		k, b string
		want bool
	}{
		{"response.created", `{}`, false}, {"response.output_item.added", `{"item":{"type":"function_call"}}`, false},
		{"response.output_text.delta", `{"delta":"OK"}`, true}, {"response.output_text.delta", `{"delta":""}`, false},
		{"response.function_call_arguments.delta", `{"delta":"{}"}`, true},
		{"response.reasoning_summary_text.delta", `{"delta":"Thinking"}`, true},
		{"response.custom_tool_call_input.delta", `{"delta":"patch"}`, true},
		{"response.output_item.done", `{"item":{"type":"function_call","arguments":"{}"}}`, true},
	} {
		if effectiveOutputEvent(tc.k, []byte(tc.b)) != tc.want {
			t.Fatal(tc.k)
		}
	}
}
