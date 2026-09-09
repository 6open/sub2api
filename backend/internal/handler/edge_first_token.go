package handler

// Legacy edge receipts use zero when no text delta was observed. Keep the
// signed receipt unchanged, but store unknown latency as NULL in usage logs.
func edgeFirstTokenMillis(ms int) *int {
	if ms <= 0 {
		return nil
	}
	return &ms
}
