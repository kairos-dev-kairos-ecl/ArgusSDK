//go:build llmlocal

package llmsignal

import (
	"context"
	"sync"
	"time"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/collector/euc"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/connector"
)

// fakeConnector is a connector.Connector that records every delivered batch.
// It lets the pipeline test assert end-to-end delivery without a real network.
type fakeConnector struct {
	name string
	mu   sync.Mutex
	got  []connector.SignalBatch
}

func newFakeConnector(name string) *fakeConnector { return &fakeConnector{name: name} }

func (f *fakeConnector) Name() string                       { return f.name }
func (f *fakeConnector) Connect(context.Context) error      { return nil }
func (f *fakeConnector) Health(context.Context) error       { return nil }
func (f *fakeConnector) Close() error                       { return nil }

func (f *fakeConnector) Send(_ context.Context, batch *connector.SignalBatch) (*connector.DeliveryAck, error) {
	f.mu.Lock()
	f.got = append(f.got, *batch)
	f.mu.Unlock()
	return &connector.DeliveryAck{
		BatchID:   batch.BatchID,
		Status:    "delivered",
		Timestamp: time.Now(),
	}, nil
}

func (f *fakeConnector) received() []connector.SignalBatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]connector.SignalBatch, len(f.got))
	copy(out, f.got)
	return out
}

// fakeOSCollector emits a fixed set of euc.Observations once, then idles until
// Close. It stands in for the platform OS collectors (linux/windows/darwin),
// which are currently stubs — see euc_local_inference_test.go for the caveat.
type fakeOSCollector struct {
	obs []euc.Observation
}

func newFakeOSCollector(obs ...euc.Observation) *fakeOSCollector {
	return &fakeOSCollector{obs: obs}
}

func (f *fakeOSCollector) Start(ctx context.Context, out chan<- euc.Observation) error {
	go func() {
		for _, o := range f.obs {
			select {
			case out <- o:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (f *fakeOSCollector) Close() error { return nil }
