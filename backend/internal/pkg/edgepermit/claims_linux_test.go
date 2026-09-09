package edgepermit

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestClaimPersistenceAndCapacity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claims")
	s, err := OpenClaimStore(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if second, err := OpenClaimStore(dir, 1); err == nil {
		second.Close()
		t.Fatal("second process lock accepted")
	}
	if err := s.Claim("one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim("one"); err != ErrAlreadyClaimed {
		t.Fatal(err)
	}
	if err := s.Claim("two"); err != ErrClaimsFull {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim("one"); err != ErrClaimsClosed {
		t.Fatal(err)
	}
	reopened, err := OpenClaimStore(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Claim("one"); err != ErrAlreadyClaimed {
		t.Fatal(err)
	}
	if err := reopened.Claim("two"); err != ErrClaimsFull {
		t.Fatal(err)
	}
}

func TestConcurrentClaimExactlyOneWinner(t *testing.T) {
	s, err := OpenClaimStore(filepath.Join(t.TempDir(), "claims"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Claim("one")
			if err == nil {
				winners.Add(1)
			} else if err != ErrAlreadyClaimed {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("got %d executions", winners.Load())
	}
}

func TestClaimRejectUnsafeStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claims")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenClaimStore(dir, 1); err == nil {
		s.Close()
		t.Fatal("public directory accepted")
	}
	if s, err := OpenClaimStore("relative", 1); err == nil {
		s.Close()
		t.Fatal("relative directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenClaimStore(dir, 1); err == nil {
		s.Close()
		t.Fatal("corrupt directory accepted")
	}
}

func TestClaimStoreBelowSystemdStateSymlink(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private-state")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "service-state")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	s, err := OpenClaimStore(filepath.Join(link, "claims"), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Claim("request"); err != nil {
		t.Fatal(err)
	}
}
