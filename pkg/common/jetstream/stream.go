package jetstream

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// DefaultPrimaryStream is the canonical NexusMart JetStream stream name.
	DefaultPrimaryStream = "NEXUSMART"
	// DefaultDLQStream is the canonical NexusMart dead-letter JetStream stream name.
	DefaultDLQStream = "NEXUSMART_DLQ"
	// DefaultDuplicateWindow is the JetStream deduplication window on Nats-Msg-Id.
	DefaultDuplicateWindow = 2 * time.Minute

	maxEnsureAttempts = 5
)

// StreamManager is the minimal subset of nats.JetStreamContext required to ensure streams.
type StreamManager interface {
	StreamInfo(stream string, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	AddStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	UpdateStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error)
}

// CanonicalPrimarySubjects returns a fresh slice containing all canonical NexusMart primary stream subjects.
func CanonicalPrimarySubjects() []string {
	return []string{
		"user.>",
		"order.>",
		"payment.>",
		"shipment.>",
		"product.>",
		"promotion.>",
	}
}

// MergeSubjects returns the union of existing and required subjects while preserving
// every existing subject in order and appending only missing required subjects.
// added is true if at least one new subject was appended.
func MergeSubjects(existing, required []string) (merged []string, added bool) {
	seen := make(map[string]struct{}, len(existing)+len(required))
	merged = make([]string, 0, len(existing)+len(required))

	for _, raw := range existing {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		merged = append(merged, s)
	}

	for _, raw := range required {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		merged = append(merged, s)
		added = true
	}

	return merged, added
}

// EnsureStreams idempotently creates or union-updates both the primary NEXUSMART stream
// (with all canonical subjects) and the NEXUSMART_DLQ stream.
func EnsureStreams(js StreamManager, primaryStream, dlqStream string) error {
	if err := EnsurePrimaryStream(js, primaryStream); err != nil {
		return err
	}

	dlqName := strings.TrimSpace(dlqStream)
	if dlqName == "" {
		dlqName = DefaultDLQStream
	}
	if err := EnsureStream(js, &nats.StreamConfig{
		Name:      dlqName,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("ensure dlq stream %s: %w", dlqName, err)
	}
	return nil
}

// EnsurePrimaryStream idempotently creates or union-updates the primary NEXUSMART stream
// with all canonical subjects.
func EnsurePrimaryStream(js StreamManager, primaryStream string) error {
	name := strings.TrimSpace(primaryStream)
	if name == "" {
		name = DefaultPrimaryStream
	}

	if err := EnsureStream(js, &nats.StreamConfig{
		Name:       name,
		Subjects:   CanonicalPrimarySubjects(),
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: DefaultDuplicateWindow,
	}); err != nil {
		return fmt.Errorf("ensure primary stream %s: %w", name, err)
	}
	return nil
}

// EnsureStream ensures a JetStream stream exists and contains at least cfg.Subjects.
// If the stream already exists, it preserves existing subjects and stream configuration,
// appending only missing subjects via UpdateStream.
//
// Duplicate-create races (AddStream) and transient update errors are retried up to
// maxEnsureAttempts times. Note that JetStream UpdateStream is last-writer-wins (not CAS):
// concurrent callers requesting different subject sets can overwrite each other's additions,
// so all services sharing a stream must converge on the same canonical subject list
// (see CanonicalPrimarySubjects).
func EnsureStream(js StreamManager, cfg *nats.StreamConfig) error {
	if js == nil {
		return errors.New("jetstream manager is nil")
	}
	if cfg == nil || strings.TrimSpace(cfg.Name) == "" {
		return errors.New("stream config name is required")
	}

	desired := *cfg
	desired.Name = strings.TrimSpace(cfg.Name)
	desired.Subjects, _ = MergeSubjects(nil, cfg.Subjects)

	var lastErr error
	for attempt := 0; attempt < maxEnsureAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
		}

		info, err := js.StreamInfo(desired.Name)
		if err != nil {
			if !errors.Is(err, nats.ErrStreamNotFound) {
				return fmt.Errorf("inspect stream %s: %w", desired.Name, err)
			}
			_, addErr := js.AddStream(&desired)
			if addErr == nil {
				return nil
			}
			lastErr = addErr
			if isConcurrentStreamErr(addErr) {
				continue
			}
			// Re-check whether a sibling service created the stream concurrently.
			if _, checkErr := js.StreamInfo(desired.Name); checkErr == nil {
				continue
			}
			return fmt.Errorf("create stream %s: %w", desired.Name, addErr)
		}

		merged, added := MergeSubjects(info.Config.Subjects, desired.Subjects)
		if !added {
			return nil
		}

		updated := info.Config
		updated.Subjects = merged
		if _, updErr := js.UpdateStream(&updated); updErr == nil {
			return nil
		} else {
			lastErr = updErr
		}
	}

	return fmt.Errorf("ensure stream %s failed after %d attempts: %w", desired.Name, maxEnsureAttempts, lastErr)
}

func isConcurrentStreamErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already in use") || strings.Contains(msg, "overlap")
}
