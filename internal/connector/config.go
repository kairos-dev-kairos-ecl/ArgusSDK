package connector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
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
// F9 / T-03-13 mitigated: Watcher watches the PARENT DIRECTORY of the config
// file and filters events by the target filename. This ensures atomic-rename
// saves (write temp + rename over target, as used by vim and many editors) are
// correctly detected. The previous implementation watched the file inode
// directly; after a rename the inode watch silently died.
//
// Threat model: Watcher validates the YAML before calling onChange; a
// malformed file write does not invoke onChange and therefore cannot install
// a partial or zero-value connector configuration (T-02-03).
type Watcher struct {
	path     string
	target   string // base filename of the config file
	onChange func(*ConnectorsFileConfig)
	watcher  *fsnotify.Watcher
	logger   *zap.Logger
}

// NewWatcher creates a Watcher for the file at path. onChange is called on
// every successful config reload. logger may be nil (defaults to zap.NewNop()).
// The Watcher does not start watching until Start is called.
//
// F9: watches the PARENT DIRECTORY, not the file inode, so atomic-rename
// saves survive. Events are filtered by the target filename (filepath.Base).
func NewWatcher(path string, onChange func(*ConnectorsFileConfig), logger *zap.Logger) (*Watcher, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for %s: %w", path, err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	// F9: watch the PARENT DIRECTORY so that atomic-rename saves (write temp +
	// rename over target) are detected even after the original inode is replaced.
	dir := filepath.Dir(absPath)
	if err := fw.Add(dir); err != nil {
		fw.Close()
		return nil, fmt.Errorf("failed to watch directory %s: %w", dir, err)
	}

	return &Watcher{
		path:     path,
		target:   filepath.Base(absPath),
		onChange: onChange,
		watcher:  fw,
		logger:   logger,
	}, nil
}

// Start begins watching for file changes. It blocks until ctx is cancelled.
// Call this in a goroutine.
//
// F9: handles Write, Create, Rename, and Remove events. Rename/Remove may
// race the new file into place; a load error on those events is logged and
// the subsequent Create/Write of the replacement file triggers the successful
// reload.
func (w *Watcher) Start(ctx context.Context) {
	defer w.watcher.Close()
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// F9: filter by target filename — ignore sibling files in the dir.
			// filepath.Base handles both forward and back slashes on Windows.
			if filepath.Base(event.Name) != w.target {
				continue
			}
			// React to Write, Create, Rename, and Remove of the target.
			// On atomic-rename saves: the old file gets a Rename, then the
			// temp file gets a Create/Rename (depending on fsnotify version).
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
				event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				cfg, err := LoadConnectorsConfig(w.path)
				if err != nil {
					// Malformed YAML or race (Rename/Remove before new file appears):
					// log and keep previous config (T-02-03).
					w.logger.Warn("connector watcher: reload failed",
						zap.String("path", w.path),
						zap.Error(err))
					continue
				}
				w.onChange(cfg)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("connector watcher: fsnotify error", zap.Error(err))
		case <-ctx.Done():
			return
		}
	}
}

// Close stops the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}
