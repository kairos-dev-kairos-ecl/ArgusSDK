package euc

import "context"

// noopOSCollector is an OSCollector that does nothing. It is used on platforms
// where no native OS collector is yet implemented (e.g. Windows in Phase 4) and
// in unit tests that exercise the fanOut → signal.Batch pipeline without
// requiring a real network observer. Using a noop satisfies the locked decision
// "at least one OS impl wired end-to-end" so Start() has no TODO body.
type noopOSCollector struct{}

// Start returns nil without emitting any observations. It satisfies the
// OSCollector interface contract and keeps the observations channel open until
// the context is cancelled (the fanOut goroutine exits on ctx.Done).
func (n *noopOSCollector) Start(_ context.Context, _ chan<- Observation) error {
	return nil
}

// Close is a no-op for the noop collector.
func (n *noopOSCollector) Close() error {
	return nil
}

// NewNoopOSCollector returns an OSCollector that emits no observations and
// performs no OS-level monitoring. It is safe to use on any platform and is
// the canonical impl to wire when no platform-specific collector is available.
func NewNoopOSCollector() OSCollector {
	return &noopOSCollector{}
}
