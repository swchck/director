package registry_test

import (
	"context"
	"sync"
	"testing"

	"github.com/swchck/director/registry"
)

type memoryRegistry struct {
	mu        sync.Mutex
	instances map[string]string // instanceID -> serviceName
}

func newMemoryRegistry() *memoryRegistry {
	return &memoryRegistry{
		instances: make(map[string]string),
	}
}

func (r *memoryRegistry) Register(_ context.Context, instanceID, serviceName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.instances[instanceID] = serviceName
	return nil
}

func (r *memoryRegistry) Heartbeat(_ context.Context, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.instances[instanceID]; !ok {
		return registry.ErrInstanceNotFound
	}

	return nil
}

func (r *memoryRegistry) Deregister(_ context.Context, instanceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.instances, instanceID)
	return nil
}

func (r *memoryRegistry) AliveCount(_ context.Context, serviceName string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for _, svc := range r.instances {
		if svc == serviceName {
			count++
		}
	}

	return count, nil
}

func (r *memoryRegistry) AliveInstances(_ context.Context, serviceName string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var ids []string
	for id, svc := range r.instances {
		if svc == serviceName {
			ids = append(ids, id)
		}
	}

	return ids, nil
}

func TestMemoryRegistry_RegisterAndAliveCount(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	if err := reg.Register(ctx, "inst-1", "my-service"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	count, err := reg.AliveCount(ctx, "my-service")
	if err != nil {
		t.Fatalf("AliveCount: %v", err)
	}

	if count != 1 {
		t.Errorf("AliveCount = %d, want 1", count)
	}
}

func TestMemoryRegistry_MultipleInstances(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	reg.Register(ctx, "inst-1", "svc-a")
	reg.Register(ctx, "inst-2", "svc-a")
	reg.Register(ctx, "inst-3", "svc-b")

	count, _ := reg.AliveCount(ctx, "svc-a")
	if count != 2 {
		t.Errorf("AliveCount(svc-a) = %d, want 2", count)
	}

	count, _ = reg.AliveCount(ctx, "svc-b")
	if count != 1 {
		t.Errorf("AliveCount(svc-b) = %d, want 1", count)
	}

	count, _ = reg.AliveCount(ctx, "svc-c")
	if count != 0 {
		t.Errorf("AliveCount(svc-c) = %d, want 0", count)
	}
}

func TestMemoryRegistry_Deregister(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	reg.Register(ctx, "inst-1", "svc")
	reg.Register(ctx, "inst-2", "svc")

	reg.Deregister(ctx, "inst-1")

	count, _ := reg.AliveCount(ctx, "svc")
	if count != 1 {
		t.Errorf("AliveCount after deregister = %d, want 1", count)
	}
}

func TestMemoryRegistry_Heartbeat_UnknownInstance(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	err := reg.Heartbeat(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for heartbeat on unknown instance")
	}
}

func TestMemoryRegistry_Heartbeat_KnownInstance(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	reg.Register(ctx, "inst-1", "svc")

	if err := reg.Heartbeat(ctx, "inst-1"); err != nil {
		t.Errorf("Heartbeat: %v", err)
	}
}

func TestMemoryRegistry_ReRegister(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	reg.Register(ctx, "inst-1", "svc-a")
	reg.Register(ctx, "inst-1", "svc-b") // re-register with different service

	count, _ := reg.AliveCount(ctx, "svc-a")
	if count != 0 {
		t.Errorf("AliveCount(svc-a) = %d, want 0 after re-register", count)
	}

	count, _ = reg.AliveCount(ctx, "svc-b")
	if count != 1 {
		t.Errorf("AliveCount(svc-b) = %d, want 1 after re-register", count)
	}
}

func TestMemoryRegistry_ImplementsRegistry(t *testing.T) {
	var _ registry.Registry = newMemoryRegistry()
}

func TestErrInstanceNotFound(t *testing.T) {
	if registry.ErrInstanceNotFound == nil {
		t.Error("ErrInstanceNotFound should not be nil")
	}

	if registry.ErrInstanceNotFound.Error() != "registry: instance not found" {
		t.Errorf("ErrInstanceNotFound = %q", registry.ErrInstanceNotFound.Error())
	}
}

func TestMemoryRegistry_ConcurrentAccess(t *testing.T) {
	reg := newMemoryRegistry()
	ctx := context.Background()

	var wg sync.WaitGroup
	const n = 50

	for i := range n {
		wg.Go(func() {
			reg.Register(ctx, "inst-"+string(rune('A'+i%26)), "svc")
		})
	}
	wg.Wait()

	for i := range n {
		wg.Go(func() {
			reg.Heartbeat(ctx, "inst-"+string(rune('A'+i%26)))
		})
		wg.Go(func() {
			reg.AliveCount(ctx, "svc")
		})
	}
	wg.Wait()
}
