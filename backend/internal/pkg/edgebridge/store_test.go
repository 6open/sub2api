package edgebridge

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreDurabilityAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if err := s.Save(id, map[string]string{"state": "authorized"}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(id, map[string]string{}, true); !os.IsExist(err) {
		t.Fatal("duplicate accepted")
	}
	if err := s.Save("../escape", nil, false); err == nil {
		t.Fatal("unsafe path")
	}
	if second, err := OpenStore(path); err == nil {
		second.Close()
		t.Fatal("second owner accepted")
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var data map[string]string
	if err := s.Load(id, &data); err != nil || data["state"] != "authorized" {
		t.Fatal("lost state")
	}
}
func TestReceiptBoundsAndTextOnly(t *testing.T) {
	r := Receipt{ID: "x", Outcome: "completed", Status: 200, InputTokens: 10, OutputTokens: 2, CachedTokens: 3}
	if r.Validate() != nil {
		t.Fatal("valid receipt rejected")
	}
	r.CachedTokens = math.MaxInt
	r.CacheWriteTokens = math.MaxInt
	if r.Validate() == nil {
		t.Fatal("overflow accepted")
	}
	if !SupportedInput([]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,abc"}]},{"role":"user","content":"hello"}]}`)) {
		t.Fatal("historical image input rejected")
	}
	if SupportedInput([]byte(`{"input":[{"type":"input_audio"}]}`)) {
		t.Fatal("audio accepted")
	}
	if SupportedInput([]byte(strings.Repeat(" ", MaxBody+1))) {
		t.Fatal("oversize accepted")
	}
	imageReceipt := Receipt{ID: "image", Outcome: "completed", Status: 200, InputTokens: 100, ImageInputTokens: 80}
	if imageReceipt.Validate() != nil {
		t.Fatal("valid image usage rejected")
	}
	imageReceipt.ImageInputTokens = 101
	if imageReceipt.Validate() == nil {
		t.Fatal("excess image tokens accepted")
	}
	if !ClientToolsOnly([]byte(`{"tools":[{"type":"namespace","tools":[{"type":"function","name":"test"},{"type":"custom","name":"patch"}]}]}`)) {
		t.Fatal("client tools rejected")
	}
	if ClientToolsOnly([]byte(`{"tools":[{"type":"web_search"}]}`)) {
		t.Fatal("server-side tool accepted")
	}
}
