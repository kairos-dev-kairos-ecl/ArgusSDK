// Package connector implements the output adapter framework for argus-agent.
// Structure mirrors internal/notify in ArgusXDR (Notifier → Connector,
// NotificationRequest → SignalBatch, NotificationResponse → DeliveryAck),
// adapted for signal streaming rather than alert delivery.
package connector

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/pkg/signal"
)

// SignalBatch is the unit delivered to a Connector.
// It carries the signals plus the per-batch metadata the connector needs
// (instance auth, OCSF toggle, sequence number for ordering).
type SignalBatch struct {
	BatchID    string // ULID for dedup / ack tracking
	InstanceID string // SDK instance ID (server-assigned after registration)
	GroupID    string
	Signals    []signal.Signal
	ReceivedAt time.Time

	// AppID is the application identifier sourced from the originating
	// signal.Batch.AppID. It is populated by the agent ingest loop and consumed
	// only by the argusxdr connector (which maps it to the proto AppId field).
	// kafka, splunk_hec, elastic, and syslog connectors do not read this field.
	AppID string

	// Env is the deployment environment label (e.g. "dev", "staging", "prod")
	// sourced from signal.Batch.Env. Populated by the agent ingest loop;
	// consumed only by the argusxdr connector (proto Env field).
	// kafka, splunk_hec, elastic, and syslog connectors do not read this field.
	Env string

	// UseOCSF instructs the connector to translate to OCSF before delivery.
	// Set to true for all non-ArgusXDR connectors (Mode 2).
	UseOCSF bool
}

// DeliveryAck is the response from a Connector after attempting delivery.
type DeliveryAck struct {
	BatchID   string
	Status    string // "delivered" | "retrying" | "failed"
	RemoteID  string // Connector-assigned ID if available (e.g. Kafka offset)
	Error     string
	Timestamp time.Time
}

// Connector is the interface every output adapter must implement.
// It is the SDK analogue of notify.Notifier.
type Connector interface {
	// Name returns a stable, unique identifier (e.g. "argusxdr", "kafka", "splunk_hec").
	Name() string

	// Connect establishes or validates the connection to the destination.
	// Called once at agent start and again after circuit-breaker recovery.
	Connect(ctx context.Context) error

	// Send delivers a batch of signals to the destination.
	// Implementations own retry logic at the single-attempt level;
	// the Dispatcher owns cross-attempt circuit-breaker logic.
	Send(ctx context.Context, batch *SignalBatch) (*DeliveryAck, error)

	// Health checks whether the destination is reachable.
	// Called by the Dispatcher on a heartbeat interval and after failures.
	Health(ctx context.Context) error

	// Close releases resources held by this connector (connections, goroutines).
	Close() error
}

// ConnectorRegistry manages registered Connectors.
// Thread-safe; safe to register/unregister while the Dispatcher is running.
type ConnectorRegistry struct {
	mu         sync.RWMutex
	connectors map[string]Connector
	logger     *zap.Logger
}

// NewConnectorRegistry creates an empty registry.
func NewConnectorRegistry(logger *zap.Logger) *ConnectorRegistry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ConnectorRegistry{
		connectors: make(map[string]Connector),
		logger:     logger,
	}
}

// Register adds a Connector. Returns error if the name is already taken.
func (r *ConnectorRegistry) Register(c Connector) error {
	if c == nil {
		return fmt.Errorf("connector cannot be nil")
	}
	name := c.Name()
	if name == "" {
		return fmt.Errorf("connector name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connectors[name]; exists {
		return fmt.Errorf("connector already registered: %s", name)
	}
	r.connectors[name] = c
	r.logger.Info("connector registered", zap.String("connector", name))
	return nil
}

// Get retrieves a Connector by name.
func (r *ConnectorRegistry) Get(name string) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.connectors[name]
	return c, ok
}

// GetAll returns all registered Connectors.
func (r *ConnectorRegistry) GetAll() []Connector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Connector, 0, len(r.connectors))
	for _, c := range r.connectors {
		out = append(out, c)
	}
	return out
}

// List returns the names of all registered Connectors.
func (r *ConnectorRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.connectors))
	for name := range r.connectors {
		names = append(names, name)
	}
	return names
}

// HealthCheck calls Health on every registered Connector and returns the results.
func (r *ConnectorRegistry) HealthCheck(ctx context.Context) map[string]error {
	r.mu.RLock()
	snapshot := make(map[string]Connector, len(r.connectors))
	for name, c := range r.connectors {
		snapshot[name] = c
	}
	r.mu.RUnlock()

	results := make(map[string]error, len(snapshot))
	for name, c := range snapshot {
		results[name] = c.Health(ctx)
	}
	return results
}

// DispatchConfig holds tuning parameters for the Dispatcher.
type DispatchConfig struct {
	// WorkerCount is the number of concurrent dispatch goroutines.
	// Default: 4.
	WorkerCount int

	// QueueCapacity is the maximum number of batches buffered in memory
	// before backpressure propagates to the ingest layer. Default: 1000.
	QueueCapacity int

	// SendTimeout is the per-Send call deadline. Default: 30s.
	SendTimeout time.Duration

	// ShutdownTimeout is the drain timeout on Close. Default: 30s.
	ShutdownTimeout time.Duration
}

// DefaultDispatchConfig returns sensible defaults.
func DefaultDispatchConfig() *DispatchConfig {
	return &DispatchConfig{
		WorkerCount:     4,
		QueueCapacity:   1000,
		SendTimeout:     30 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

// DispatchJob is a unit of work queued by the agent collector layer.
type DispatchJob struct {
	Batch   *SignalBatch
	Targets []string // connector names to deliver to
}

// Dispatcher fans batches out to the target Connectors via a fixed worker pool.
// Delivery failures are surfaced to the caller (and counted in dispatcher stats);
// retry/backoff is handled by the WAL buffer's drain loop, not here.
type Dispatcher struct {
	config   *DispatchConfig
	registry *ConnectorRegistry
	queue    chan *DispatchJob
	logger   *zap.Logger
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once

	// Lock-free counters for observability
	accepted  uint64
	delivered uint64
	failed    uint64
	dropped   uint64
}

// NewDispatcher creates and starts a Dispatcher.
func NewDispatcher(cfg *DispatchConfig, registry *ConnectorRegistry, logger *zap.Logger) (*Dispatcher, error) {
	if registry == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}
	if cfg == nil {
		cfg = DefaultDispatchConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		config:   cfg,
		registry: registry,
		queue:    make(chan *DispatchJob, cfg.QueueCapacity),
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
	}

	for i := 0; i < cfg.WorkerCount; i++ {
		d.wg.Add(1)
		go d.worker(i)
	}
	return d, nil
}

// Enqueue adds a batch delivery job to the queue.
// Returns error if the queue is full or the Dispatcher is shutting down.
// On successful enqueue, increments the accepted counter.
func (d *Dispatcher) Enqueue(job *DispatchJob) error {
	select {
	case d.queue <- job:
		atomic.AddUint64(&d.accepted, 1)
		return nil
	case <-d.ctx.Done():
		return fmt.Errorf("dispatcher is shutting down")
	default:
		return fmt.Errorf("dispatch queue full (capacity %d)", d.config.QueueCapacity)
	}
}

func (d *Dispatcher) worker(_ int) {
	defer d.wg.Done()
	for {
		select {
		case job := <-d.queue:
			d.process(job)
		case <-d.ctx.Done():
			// drain
			for {
				select {
				case job := <-d.queue:
					d.process(job)
				default:
					return
				}
			}
		}
	}
}

func (d *Dispatcher) process(job *DispatchJob) {
	if job == nil || job.Batch == nil {
		return
	}
	for _, name := range job.Targets {
		c, ok := d.registry.Get(name)
		if !ok {
			d.logger.Warn("connector not found", zap.String("connector", name))
			continue
		}
		ctx, cancel := context.WithTimeout(d.ctx, d.config.SendTimeout)
		_, err := c.Send(ctx, job.Batch)
		cancel()
		if err != nil {
			d.logger.Error("connector send failed",
				zap.String("connector", name),
				zap.String("batch_id", job.Batch.BatchID),
				zap.Error(err))
			atomic.AddUint64(&d.failed, 1)
		} else {
			atomic.AddUint64(&d.delivered, 1)
		}
	}
}

// Stats returns a snapshot of the Dispatcher's lock-free counters.
// Keys: "accepted", "delivered", "failed", "dropped".
func (d *Dispatcher) Stats() map[string]uint64 {
	return map[string]uint64{
		"accepted":  atomic.LoadUint64(&d.accepted),
		"delivered": atomic.LoadUint64(&d.delivered),
		"failed":    atomic.LoadUint64(&d.failed),
		"dropped":   atomic.LoadUint64(&d.dropped),
	}
}

// Close drains the queue and shuts down workers.
func (d *Dispatcher) Close() error {
	var err error
	d.once.Do(func() {
		d.cancel()
		done := make(chan struct{})
		go func() { d.wg.Wait(); close(done) }()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), d.config.ShutdownTimeout)
		defer cancel()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			err = fmt.Errorf("dispatcher shutdown timed out")
		}
	})
	return err
}
