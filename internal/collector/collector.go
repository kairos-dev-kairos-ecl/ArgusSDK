// Package collector defines the OS-agnostic interface for all signal sources.
// Each collector implementation runs independently and pushes signal batches
// to a shared ingest channel consumed by the agent's dispatch layer.
package collector

import (
	"context"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// Collector is the base interface every signal source must implement.
type Collector interface {
	// Name returns a stable identifier for this collector (e.g. "llm_grpc", "euc_dns").
	Name() string

	// Start begins collection and emits batches on the provided channel.
	// The implementation owns its own goroutines; Start returns immediately.
	// The context controls the lifetime of all internal goroutines.
	Start(ctx context.Context, out chan<- signal.Batch) error

	// Health returns a non-nil error if the collector is unable to collect signals.
	Health(ctx context.Context) error

	// Close stops collection and releases resources. Safe to call after Start errors.
	Close() error
}
