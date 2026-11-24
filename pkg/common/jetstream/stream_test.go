package jetstream

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type fakeStreamManager struct {
	mu          sync.Mutex
	streams     map[string]*nats.StreamConfig
	addCalls    int
	updateCalls int
	infoCalls   int

	onAdd    func(cfg *nats.StreamConfig) (*nats.StreamInfo, error)
	onUpdate func(cfg *nats.StreamConfig) (*nats.StreamInfo, error)
}

func newFakeStreamManager() *fakeStreamManager {
	return &fakeStreamManager{
		streams: make(map[string]*nats.StreamConfig),
	}
}

func cloneStreamConfig(cfg *nats.StreamConfig) *nats.StreamConfig {
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	return &cp
}

func (f *fakeStreamManager) StreamInfo(name string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infoCalls++
	cfg, ok := f.streams[name]
	if !ok {
		return nil, nats.ErrStreamNotFound
	}
	return &nats.StreamInfo{Config: *cloneStreamConfig(cfg)}, nil
}

func (f *fakeStreamManager) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	if f.onAdd != nil {
		return f.onAdd(cfg)
	}
	if _, exists := f.streams[cfg.Name]; exists {
		return nil, nats.ErrStreamNameAlreadyInUse
	}
	f.streams[cfg.Name] = cloneStreamConfig(cfg)
	return &nats.StreamInfo{Config: *cloneStreamConfig(cfg)}, nil
}

func (f *fakeStreamManager) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if f.onUpdate != nil {
		return f.onUpdate(cfg)
	}
	if _, exists := f.streams[cfg.Name]; !exists {
		return nil, nats.ErrStreamNotFound
	}
	f.streams[cfg.Name] = cloneStreamConfig(cfg)
	return &nats.StreamInfo{Config: *cloneStreamConfig(cfg)}, nil
}

func TestMergeSubjectsPreservesExistingAndAppendsMissing(t *testing.T) {
	merged, added := MergeSubjects(
		[]string{"user.>", "custom.>", "order.>"},
		[]string{"order.>", "product.>", "promotion.>"},
	)
	if !added {
		t.Fatalf("expected added=true")
	}
	want := []string{"user.>", "custom.>", "order.>", "product.>", "promotion.>"}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}

	merged2, added2 := MergeSubjects(want, []string{"user.>", "product.>"})
	if added2 {
		t.Fatalf("expected added=false when all required subjects already exist")
	}
	if !reflect.DeepEqual(merged2, want) {
		t.Fatalf("merged2 = %v, want %v", merged2, want)
	}
}

func TestEnsureStreamsCreatesPrimaryWithCanonicalSubjectsAndDLQ(t *testing.T) {
	mgr := newFakeStreamManager()
	if err := EnsureStreams(mgr, "", ""); err != nil {
		t.Fatalf("EnsureStreams error: %v", err)
	}

	primary := mgr.streams[DefaultPrimaryStream]
	if primary == nil {
		t.Fatalf("expected stream %s to be created", DefaultPrimaryStream)
	}
	wantPrimary := []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "promotion.>", "review.>"}
	if !reflect.DeepEqual(primary.Subjects, wantPrimary) {
		t.Fatalf("primary subjects = %v, want %v", primary.Subjects, wantPrimary)
	}
	if primary.Duplicates != 2*time.Minute {
		t.Fatalf("duplicates = %v, want 2m", primary.Duplicates)
	}

	dlq := mgr.streams[DefaultDLQStream]
	if dlq == nil || !reflect.DeepEqual(dlq.Subjects, []string{"dlq.>"}) {
		t.Fatalf("unexpected dlq stream: %+v", dlq)
	}
}

func TestEnsureStreamMergesMissingSubjectsWithoutDroppingExistingOrClobberingConfig(t *testing.T) {
	mgr := newFakeStreamManager()
	mgr.streams["NEXUSMART"] = &nats.StreamConfig{
		Name:       "NEXUSMART",
		Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>", "audit.>"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
		MaxBytes:   1024 * 1024,
	}

	if err := EnsurePrimaryStream(mgr, "NEXUSMART"); err != nil {
		t.Fatalf("EnsurePrimaryStream error: %v", err)
	}

	if mgr.updateCalls != 1 {
		t.Fatalf("updateCalls = %d, want 1", mgr.updateCalls)
	}
	got := mgr.streams["NEXUSMART"]
	want := []string{"user.>", "order.>", "payment.>", "shipment.>", "audit.>", "product.>", "promotion.>", "review.>"}
	if !reflect.DeepEqual(got.Subjects, want) {
		t.Fatalf("subjects = %v, want %v", got.Subjects, want)
	}
	if got.MaxBytes != 1024*1024 {
		t.Fatalf("MaxBytes clobbered: got %d, want %d", got.MaxBytes, 1024*1024)
	}

	// Second call must be a no-op (no unnecessary UpdateStream).
	if err := EnsurePrimaryStream(mgr, "NEXUSMART"); err != nil {
		t.Fatalf("second EnsurePrimaryStream error: %v", err)
	}
	if mgr.updateCalls != 1 {
		t.Fatalf("second call should not update stream, updateCalls = %d", mgr.updateCalls)
	}
}

func TestEnsureStreamHandlesConcurrentAddAndUpdateConflicts(t *testing.T) {
	mgr := newFakeStreamManager()

	// Simulate another service creating NEXUSMART with old 4 subjects right before our AddStream,
	// then a transient conflict on first UpdateStream while another sibling adds "product.>".
	mgr.onAdd = func(_ *nats.StreamConfig) (*nats.StreamInfo, error) {
		mgr.streams["NEXUSMART"] = &nats.StreamConfig{
			Name:       "NEXUSMART",
			Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
			Retention:  nats.LimitsPolicy,
			Storage:    nats.FileStorage,
			Duplicates: 2 * time.Minute,
		}
		return nil, nats.ErrStreamNameAlreadyInUse
	}

	conflictOnce := true
	mgr.onUpdate = func(cfg *nats.StreamConfig) (*nats.StreamInfo, error) {
		if conflictOnce {
			conflictOnce = false
			// Sibling service concurrently added "product.>" and "custom.>"
			mgr.streams["NEXUSMART"].Subjects = []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "custom.>"}
			return nil, errors.New("nats: stream update conflict")
		}
		mgr.streams[cfg.Name] = cloneStreamConfig(cfg)
		return &nats.StreamInfo{Config: *cloneStreamConfig(cfg)}, nil
	}

	if err := EnsurePrimaryStream(mgr, "NEXUSMART"); err != nil {
		t.Fatalf("EnsurePrimaryStream error under concurrency: %v", err)
	}

	got := mgr.streams["NEXUSMART"].Subjects
	want := []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "custom.>", "promotion.>", "review.>"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("subjects after concurrent merge = %v, want %v", got, want)
	}
	if mgr.addCalls != 1 || mgr.updateCalls != 2 {
		t.Fatalf("addCalls=%d updateCalls=%d, want 1 and 2", mgr.addCalls, mgr.updateCalls)
	}
}
