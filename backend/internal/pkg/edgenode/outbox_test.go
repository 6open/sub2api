package edgenode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestOutboxRecoversAfterRestartWithoutDoubleApply(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	var charged atomic.Int32
	var calls atomic.Int32
	box, err := NewOutbox(dir, func(context.Context, []byte) error {
		charged.CompareAndSwap(0, 1)
		calls.Add(1)
		return errors.New("connection lost after idempotent billing commit")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := box.Put("key:request", []byte(`{"request_id":"request"}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return calls.Load() > 0 && !AccountingReady() })
	box.Close()
	box, err = NewOutbox(dir, func(context.Context, []byte) error {
		charged.CompareAndSwap(0, 1)
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	eventually(t, func() bool { return AccountingReady() && calls.Load() > 1 })
	if charged.Load() != 1 {
		t.Fatal("double billing")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != ".lock" {
		t.Fatal("settled entry not removed")
	}
}

func TestCorruptOutboxFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "incomplete.tmp"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	box, err := NewOutbox(dir, func(context.Context, []byte) error { t.Error("partial entry applied"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	box.Close()
	if AccountingReady() {
		t.Fatal("corrupt outbox reported ready")
	}
	if _, err := os.Stat(filepath.Join(dir, "incomplete.tmp")); err != nil {
		t.Fatal("incomplete financial record was discarded")
	}
}

func TestOutboxRejectsConflictingReplayAndSecondProcess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	box, err := NewOutbox(dir, func(context.Context, []byte) error { return errors.New("offline") })
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	if other, err := NewOutbox(dir, func(context.Context, []byte) error { return nil }); err == nil {
		other.Close()
		t.Fatal("second process accepted")
	}
	if err := box.Put("one", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := box.Put("one", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := box.Put("one", []byte("b")); err == nil {
		t.Fatal("conflicting financial record accepted")
	}
	if AccountingReady() {
		t.Fatal("failed persistence reported healthy")
	}
}
