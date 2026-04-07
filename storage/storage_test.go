package storage_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/swchck/director/storage"
)

type memoryStorage struct {
	mu        sync.Mutex
	snapshots map[string]*storage.Snapshot // key = collection:version
	applyLog  map[string]int               // key = collection:version
	lockHeld  bool
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		snapshots: make(map[string]*storage.Snapshot),
		applyLog:  make(map[string]int),
	}
}

func (s *memoryStorage) SaveSnapshot(_ context.Context, collection, version string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	s.snapshots[key] = &storage.Snapshot{
		Collection: collection,
		Version:    version,
		Content:    content,
		Status:     storage.StatusPending,
		CreatedAt:  time.Now(),
	}

	return nil
}

func (s *memoryStorage) ActivateSnapshot(_ context.Context, collection, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deactivate old.
	for _, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			snap.Status = storage.StatusInactive
		}
	}

	key := collection + ":" + version
	snap, ok := s.snapshots[key]
	if !ok {
		return storage.ErrSnapshotNotFound
	}

	snap.Status = storage.StatusActive
	return nil
}

func (s *memoryStorage) GetActiveSnapshot(_ context.Context, collection string) (*storage.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, snap := range s.snapshots {
		if snap.Collection == collection && snap.Status == storage.StatusActive {
			return snap, nil
		}
	}

	return nil, storage.ErrSnapshotNotFound
}

func (s *memoryStorage) GetSnapshot(_ context.Context, collection, version string) (*storage.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	snap, ok := s.snapshots[key]
	if !ok {
		return nil, storage.ErrSnapshotNotFound
	}

	return snap, nil
}

func (s *memoryStorage) FailSnapshot(_ context.Context, collection, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	snap, ok := s.snapshots[key]
	if !ok {
		return storage.ErrSnapshotNotFound
	}

	snap.Status = storage.StatusFailed
	return nil
}

func (s *memoryStorage) LogApply(_ context.Context, _, collection, version, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if status == "applied" {
		key := collection + ":" + version
		s.applyLog[key]++
	}

	return nil
}

func (s *memoryStorage) CountApplied(_ context.Context, collection, version string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := collection + ":" + version
	return s.applyLog[key], nil
}

func (s *memoryStorage) AcquireLock(_ context.Context, _ int64) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lockHeld {
		return nil, storage.ErrLockNotAcquired
	}

	s.lockHeld = true
	return func() {
		s.mu.Lock()
		s.lockHeld = false
		s.mu.Unlock()
	}, nil
}

func TestMemoryStorage_SaveAndGetSnapshot(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	err := store.SaveSnapshot(ctx, "products", "v1", []byte(`[{"id":1}]`))
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	snap, err := store.GetSnapshot(ctx, "products", "v1")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}

	if snap.Collection != "products" {
		t.Errorf("Collection = %q", snap.Collection)
	}

	if snap.Version != "v1" {
		t.Errorf("Version = %q", snap.Version)
	}

	if string(snap.Content) != `[{"id":1}]` {
		t.Errorf("Content = %q", snap.Content)
	}

	if snap.Status != storage.StatusPending {
		t.Errorf("Status = %q, want 'pending'", snap.Status)
	}
}

func TestMemoryStorage_GetSnapshot_NotFound(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	_, err := store.GetSnapshot(ctx, "missing", "v1")
	if !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("err = %v, want ErrSnapshotNotFound", err)
	}
}

func TestMemoryStorage_ActivateSnapshot(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	if err := store.SaveSnapshot(ctx, "products", "v1", []byte(`v1`)); err != nil {
		t.Fatalf("SaveSnapshot v1: %v", err)
	}
	if err := store.SaveSnapshot(ctx, "products", "v2", []byte(`v2`)); err != nil {
		t.Fatalf("SaveSnapshot v2: %v", err)
	}

	// Activate v1.
	if err := store.ActivateSnapshot(ctx, "products", "v1"); err != nil {
		t.Fatalf("Activate v1: %v", err)
	}

	snap, err := store.GetActiveSnapshot(ctx, "products")
	if err != nil {
		t.Fatalf("GetActive after v1: %v", err)
	}
	if snap.Version != "v1" {
		t.Errorf("active = %q, want v1", snap.Version)
	}

	// Activate v2 — v1 should become inactive.
	if err := store.ActivateSnapshot(ctx, "products", "v2"); err != nil {
		t.Fatalf("Activate v2: %v", err)
	}

	snap, err = store.GetActiveSnapshot(ctx, "products")
	if err != nil {
		t.Fatalf("GetActive after v2: %v", err)
	}
	if snap.Version != "v2" {
		t.Errorf("active = %q, want v2", snap.Version)
	}

	// v1 should no longer be active.
	v1, _ := store.GetSnapshot(ctx, "products", "v1")
	if v1.Status != storage.StatusInactive {
		t.Errorf("v1 status = %q, want 'inactive'", v1.Status)
	}
}

func TestMemoryStorage_ActivateSnapshot_NotFound(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	err := store.ActivateSnapshot(ctx, "missing", "v1")
	if !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("err = %v, want ErrSnapshotNotFound", err)
	}
}

func TestMemoryStorage_GetActiveSnapshot_NotFound(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	_, err := store.GetActiveSnapshot(ctx, "products")
	if !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("err = %v, want ErrSnapshotNotFound", err)
	}
}

func TestMemoryStorage_FailSnapshot(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	if err := store.SaveSnapshot(ctx, "products", "v1", []byte(`data`)); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	if err := store.FailSnapshot(ctx, "products", "v1"); err != nil {
		t.Fatalf("FailSnapshot: %v", err)
	}

	snap, _ := store.GetSnapshot(ctx, "products", "v1")
	if snap.Status != storage.StatusFailed {
		t.Errorf("Status = %q, want 'failed'", snap.Status)
	}
}

func TestMemoryStorage_FailSnapshot_NotFound(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	err := store.FailSnapshot(ctx, "missing", "v1")
	if !errors.Is(err, storage.ErrSnapshotNotFound) {
		t.Errorf("err = %v, want ErrSnapshotNotFound", err)
	}
}

func TestMemoryStorage_LogApplyAndCountApplied(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	store.LogApply(ctx, "inst-1", "products", "v1", "applied")
	store.LogApply(ctx, "inst-2", "products", "v1", "applied")
	store.LogApply(ctx, "inst-3", "products", "v1", "error") // should not count

	count, err := store.CountApplied(ctx, "products", "v1")
	if err != nil {
		t.Fatalf("CountApplied: %v", err)
	}

	if count != 2 {
		t.Errorf("CountApplied = %d, want 2", count)
	}
}

func TestMemoryStorage_AcquireLock(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	release, err := store.AcquireLock(ctx, 12345)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	// Second acquire should fail.
	_, err = store.AcquireLock(ctx, 12345)
	if !errors.Is(err, storage.ErrLockNotAcquired) {
		t.Errorf("second AcquireLock: err = %v, want ErrLockNotAcquired", err)
	}

	// Release and re-acquire.
	release()

	release2, err := store.AcquireLock(ctx, 12345)
	if err != nil {
		t.Errorf("re-AcquireLock after release: %v", err)
	}
	if release2 != nil {
		release2()
	}
}

func TestMemoryStorage_ImplementsStorage(t *testing.T) {
	var _ storage.Storage = newMemoryStorage()
}

func TestSentinelErrors(t *testing.T) {
	if storage.ErrSnapshotNotFound == nil {
		t.Error("ErrSnapshotNotFound should not be nil")
	}
	if storage.ErrLockNotAcquired == nil {
		t.Error("ErrLockNotAcquired should not be nil")
	}
}

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status storage.Status
		want   string
	}{
		{storage.StatusPending, "pending"},
		{storage.StatusActive, "active"},
		{storage.StatusInactive, "inactive"},
		{storage.StatusFailed, "failed"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("Status %q != %q", tt.status, tt.want)
		}
	}
}

func TestSnapshot_Fields(t *testing.T) {
	now := time.Now()
	snap := storage.Snapshot{
		Collection: "items",
		Version:    "2025-01-01T00:00:00Z",
		Content:    []byte(`{"data":"test"}`),
		Status:     storage.StatusActive,
		CreatedAt:  now,
	}

	if snap.Collection != "items" {
		t.Errorf("Collection = %q", snap.Collection)
	}
	if snap.Version != "2025-01-01T00:00:00Z" {
		t.Errorf("Version = %q", snap.Version)
	}
	if string(snap.Content) != `{"data":"test"}` {
		t.Errorf("Content = %q", snap.Content)
	}
	if snap.Status != storage.StatusActive {
		t.Errorf("Status = %q", snap.Status)
	}
	if !snap.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v", snap.CreatedAt)
	}
}

func TestMemoryStorage_ConcurrentLock(t *testing.T) {
	store := newMemoryStorage()
	ctx := context.Background()

	const n = 50
	acquired := make(chan struct{}, n)

	var wg sync.WaitGroup

	for range n {
		wg.Go(func() {
			release, err := store.AcquireLock(ctx, 999)
			if err == nil {
				acquired <- struct{}{}
				release()
			}
		})
	}

	wg.Wait()
	close(acquired)

	count := 0
	for range acquired {
		count++
	}

	if count == 0 {
		t.Error("no goroutine acquired the lock")
	}
}
