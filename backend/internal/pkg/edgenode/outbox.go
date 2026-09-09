package edgenode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var accountingReady atomic.Bool

func AccountingReady() bool { return accountingReady.Load() }

type Outbox struct {
	fault atomic.Bool
	mu    sync.Mutex
	dir   string
	lock  *os.File
	apply func(context.Context, []byte) error
	wake  chan struct{}
	stop  chan struct{}
	wg    sync.WaitGroup
}

func NewOutbox(dir string, apply func(context.Context, []byte) error) (*Outbox, error) {
	if !filepath.IsAbs(dir) || apply == nil {
		return nil, errors.New("invalid outbox configuration")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("outbox directory must be private")
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, err
	}
	o := &Outbox{dir: dir, lock: lock, apply: apply, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	accountingReady.Store(false)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			o.drain()
			select {
			case <-o.stop:
				return
			case <-o.wake:
			case <-ticker.C:
			}
		}
	}()
	return o, nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (o *Outbox) Put(id string, data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	fail := func(err error) error { o.fault.Store(true); accountingReady.Store(false); return err }
	if o.lock == nil || id == "" || len(data) == 0 || len(data) > 1<<20 {
		return fail(errors.New("invalid outbox entry"))
	}
	h := sha256.Sum256([]byte(id))
	path := filepath.Join(o.dir, hex.EncodeToString(h[:])+".json")
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, data) {
			return nil
		}
		return fail(errors.New("conflicting outbox entry"))
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	entries, err := os.ReadDir(o.dir)
	if err != nil {
		return fail(err)
	}
	if len(entries) > 1000 {
		return fail(errors.New("outbox capacity reached"))
	}
	f, err := os.CreateTemp(o.dir, "entry-*.tmp")
	if err != nil {
		return fail(err)
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return fail(err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fail(err)
	}
	if err = f.Close(); err != nil {
		return fail(err)
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fail(err)
	}
	if err = syncDir(o.dir); err != nil {
		return fail(err)
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return nil
}

func (o *Outbox) drain() {
	o.mu.Lock()
	entries, err := os.ReadDir(o.dir)
	o.mu.Unlock()
	if err != nil {
		accountingReady.Store(false)
		return
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			accountingReady.Store(false)
			return
		}
		path := filepath.Join(o.dir, entry.Name())
		info, err := entry.Info()
		if err != nil || info.Size() > 1<<20 {
			accountingReady.Store(false)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			accountingReady.Store(false)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = o.apply(ctx, data)
		cancel()
		if err != nil {
			accountingReady.Store(false)
			return
		}
		o.mu.Lock()
		err = os.Remove(path)
		if err == nil {
			err = syncDir(o.dir)
		}
		o.mu.Unlock()
		if err != nil {
			accountingReady.Store(false)
			return
		}
	}
	accountingReady.Store(!o.fault.Load())
}

func (o *Outbox) Close() {
	close(o.stop)
	o.wg.Wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lock.Close()
	o.lock = nil
}
