package edgebridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
)

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Store struct {
	mu   sync.Mutex
	dir  string
	lock *os.File
}

func OpenStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("absolute state directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private state directory required")
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return &Store{dir: dir, lock: f}, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func (s *Store) Load(id string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) {
		return errors.New("invalid record ID")
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("oversized record")
	}
	return json.Unmarshal(data, v)
}
func (s *Store) Save(id string, v any, create bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID.MatchString(id) {
		return errors.New("invalid record ID")
	}
	path := filepath.Join(s.dir, id+".json")
	if create {
		if _, err := os.Stat(path); err == nil {
			return os.ErrExist
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	if create && len(entries) > 10000 {
		return errors.New("state capacity reached")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("record too large")
	}
	f, err := os.CreateTemp(s.dir, ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) IDs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		name := e.Name()
		if len(name) == 37 && filepath.Ext(name) == ".json" && validID.MatchString(name[:32]) {
			ids = append(ids, name[:32])
		}
	}
	return ids, nil
}
