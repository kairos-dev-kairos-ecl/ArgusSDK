package connector

import (
	"context"
	"fmt"
	"os"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// ConnectorConfig holds the configuration for a single connector instance.
// It is parsed from the YAML connectors file and passed to each connector's
// factory function via the Settings map.
type ConnectorConfig struct {
	// Enabled controls whether this connector is active. Disabled connectors
	// are not registered and do not participate in dispatch.
	Enabled bool `yaml:"enabled"`

	// Type identifies the connector implementation.
	// Valid values: "kafka" | "splunk_hec" | "elastic" | "syslog" | "argusxdr"
	Type string `yaml:"type"`

	// Settings carries connector-specific configuration decoded from YAML.
	// Each connector's New() factory is responsible for decoding Settings into
	// its own typed Config struct (e.g. via mapstructure or yaml.Unmarshal).
	Settings map[string]interface{} `yaml:"settings"`
}

// ConnectorsFileConfig is the top-level structure of the connectors YAML file.
type ConnectorsFileConfig struct {
	Connectors []ConnectorConfig `yaml:"connectors"`
}

// LoadConnectorsConfig reads and parses the YAML file at path.
// Returns an error if the file cannot be read or the YAML is malformed.
// Malformed YAML does not apply a partial configuration; the caller receives
// an error and the previous config remains in effect (see Watcher).
func LoadConnectorsConfig(path string) (*ConnectorsFileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read connectors config %s: %w", path, err)
	}
	var cfg ConnectorsFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse connectors config %s: %w", path, err)
	}
	return &cfg, nil
}

// Watcher monitors a connectors YAML file for changes and invokes onChange
// with the newly parsed config whenever the file is written or replaced.
// It uses fsnotify for cross-platform file-system event notification.
//
// Threat model: Watcher validates the YAML before calling onChange; a
// malformed file write does not invoke onChange and therefore cannot install
// a partial or zero-value connector configuration (T-02-03).
type Watcher struct {
	path     string
	onChange func(*ConnectorsFileConfig)
	watcher  *fsnotify.Watcher
}

// NewWatcher creates a Watcher for the file at path. onChange is called on
// every successful config reload. The Watcher does not start watching until
// Start is called.
func NewWatcher(path string, onChange func(*ConnectorsFileConfig)) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}
	if err := fw.Add(path); err != nil {
		fw.Close()
		return nil, fmt.Errorf("failed to watch %s: %w", path, err)
	}
	return &Watcher{
		path:     path,
		onChange: onChange,
		watcher:  fw,
	}, nil
}

// Start begins watching for file changes. It blocks until ctx is cancelled.
// Call this in a goroutine.
func (w *Watcher) Start(ctx context.Context) {
	defer w.watcher.Close()
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				cfg, err := LoadConnectorsConfig(w.path)
				if err != nil {
					// Malformed YAML: log and keep previous config (T-02-03).
					fmt.Fprintf(os.Stderr, "connector watcher: failed to reload %s: %v\n", w.path, err)
					continue
				}
				w.onChange(cfg)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "connector watcher: fsnotify error: %v\n", err)
		case <-ctx.Done():
			return
		}
	}
}

// Close stops the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}
