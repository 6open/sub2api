package handler

import "testing"

func TestEdgeFirstTokenMillis(t *testing.T) {
	for _, ms := range []int{0, -1} {
		if edgeFirstTokenMillis(ms) != nil {
			t.Fatal("unknown latency must be nil")
		}
	}
	ms := edgeFirstTokenMillis(3169)
	if ms == nil || *ms != 3169 {
		t.Fatal("measured latency must be preserved")
	}
}
