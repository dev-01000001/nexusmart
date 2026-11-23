package events

import (
	"sync"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
)

type fakeStreamManager struct {
	mu               sync.Mutex
	streams          map[string]*nats.StreamConfig
	failFirstAddName string
	failedFirstAdd   bool
}

func newFakeStreamManager() *fakeStreamManager {
	return &fakeStreamManager{
		streams: make(map[string]*nats.StreamConfig),
	}
}

func (f *fakeStreamManager) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg, ok := f.streams[stream]
	if !ok {
		return nil, nats.ErrStreamNotFound
	}
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	return &nats.StreamInfo{Config: cp}, nil
}

func (f *fakeStreamManager) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFirstAddName == cfg.Name && !f.failedFirstAdd {
		f.failedFirstAdd = true
		// Simulate sibling service creating the stream with partial legacy subjects concurrently.
		f.streams[cfg.Name] = &nats.StreamConfig{
			Name:     cfg.Name,
			Subjects: []string{"user.>", "order.>", "payment.>", "shipment.>"},
		}
		return nil, nats.ErrStreamNameAlreadyInUse
	}
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	f.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func (f *fakeStreamManager) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	f.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func TestEnsureStreams_UnionMergesCanonicalSubjectsAndConcurrentRetry(t *testing.T) {
	t.Run("creates primary and DLQ streams with all canonical subjects", func(t *testing.T) {
		mgr := newFakeStreamManager()
		if err := EnsureStreams(mgr, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
			t.Fatalf("EnsureStreams fresh: %v", err)
		}
		primary, err := mgr.StreamInfo(jetstream.DefaultPrimaryStream)
		if err != nil {
			t.Fatalf("StreamInfo primary: %v", err)
		}
		want := jetstream.CanonicalPrimarySubjects()
		if len(primary.Config.Subjects) != len(want) {
			t.Fatalf("primary subjects = %v, want %v", primary.Config.Subjects, want)
		}
		for i := range want {
			if primary.Config.Subjects[i] != want[i] {
				t.Fatalf("primary subjects[%d] = %q, want %q", i, primary.Config.Subjects[i], want[i])
			}
		}
		dlq, err := mgr.StreamInfo(jetstream.DefaultDLQStream)
		if err != nil || len(dlq.Config.Subjects) != 1 || dlq.Config.Subjects[0] != "dlq.>" {
			t.Fatalf("unexpected DLQ stream config: %+v err=%v", dlq, err)
		}
	})

	t.Run("union-merges missing product and promotion subjects without dropping existing custom subject", func(t *testing.T) {
		mgr := newFakeStreamManager()
		mgr.streams[jetstream.DefaultPrimaryStream] = &nats.StreamConfig{
			Name:     jetstream.DefaultPrimaryStream,
			Subjects: []string{"user.>", "order.>", "custom.audit.>"},
		}
		if err := EnsureStreams(mgr, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
			t.Fatalf("EnsureStreams union merge: %v", err)
		}
		info, err := mgr.StreamInfo(jetstream.DefaultPrimaryStream)
		if err != nil {
			t.Fatalf("StreamInfo: %v", err)
		}
		have := make(map[string]bool, len(info.Config.Subjects))
		for _, s := range info.Config.Subjects {
			have[s] = true
		}
		for _, req := range append(jetstream.CanonicalPrimarySubjects(), "custom.audit.>") {
			if !have[req] {
				t.Fatalf("expected subject %q preserved/merged, got %v", req, info.Config.Subjects)
			}
		}
	})

	t.Run("recovers safely on concurrent stream creation conflict", func(t *testing.T) {
		mgr := newFakeStreamManager()
		mgr.failFirstAddName = jetstream.DefaultPrimaryStream
		if err := EnsureStreams(mgr, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
			t.Fatalf("EnsureStreams concurrent retry: %v", err)
		}
		info, err := mgr.StreamInfo(jetstream.DefaultPrimaryStream)
		if err != nil {
			t.Fatalf("StreamInfo: %v", err)
		}
		have := make(map[string]bool, len(info.Config.Subjects))
		for _, s := range info.Config.Subjects {
			have[s] = true
		}
		for _, req := range jetstream.CanonicalPrimarySubjects() {
			if !have[req] {
				t.Fatalf("after concurrent conflict retry, missing subject %q in %v", req, info.Config.Subjects)
			}
		}
	})
}
