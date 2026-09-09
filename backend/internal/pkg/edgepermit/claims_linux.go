package edgepermit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var (
	ErrAlreadyClaimed = errors.New("edge request already claimed; reconcile instead of retrying")
	ErrClaimsFull     = errors.New("edge claim store full")
	ErrClaimsClosed   = errors.New("edge claim store closed")
)

// ClaimStore provides node-local, crash-safe at-most-once execution. It does
// not implement billing settlement. Losing a response or crashing after a
// claim must trigger control-plane reconciliation, never a second execution.
// Records are intentionally not garbage-collected without settlement evidence.
type ClaimStore struct {
	mu    sync.Mutex
	dir   string
	lock  *os.File
	count int
	limit int
}

// OpenClaimStore requires a dedicated, private state directory and excludes a
// second process from using the same directory. The directory is persistent,
// not a temporary directory or an in-memory filesystem in production.
func OpenClaimStore(dir string, limit int) (*ClaimStore, error) {
	if !filepath.IsAbs(dir) || limit <= 0 {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	count := 0
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		h, err := hex.DecodeString(entry.Name())
		if err != nil || len(h) != sha256.Size || !entry.Type().IsRegular() {
			_ = f.Close()
			return nil, ErrInvalid
		}
		count++
	}
	return &ClaimStore{dir: dir, lock: f, count: count, limit: limit}, nil
}

// Claim must be called after Verify and before opening an upstream connection.
// The request ID is unique for the lifetime of the node, even if a permit is
// re-signed. A partial write/sync failure conservatively consumes the claim.
func (s *ClaimStore) Claim(requestID string) error {
	if requestID == "" || len(requestID) > 128 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return ErrClaimsClosed
	}
	h := sha256.Sum256([]byte(requestID))
	path := filepath.Join(s.dir, hex.EncodeToString(h[:]))
	if _, err := os.Lstat(path); err == nil {
		return ErrAlreadyClaimed
	} else if !os.IsNotExist(err) {
		return err
	}
	if s.count >= s.limit {
		return ErrClaimsFull
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return ErrAlreadyClaimed
	}
	if err != nil {
		return err
	}
	s.count++
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *ClaimStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}
