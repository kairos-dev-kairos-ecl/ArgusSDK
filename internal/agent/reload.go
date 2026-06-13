package agent

import (
	"fmt"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// reloadConfig re-reads the configuration and applies hot-reloadable settings:
// - EUC watch list (AIEndpoints + LocalInferencePorts)
// - Logging level
// Other configuration changes are ignored.
func (a *Agent) reloadConfig() error {
	if a.configPath == "" {
		a.logger.Info("config reload: no config path set, skipping")
		return nil
	}

	// Create a fresh Viper instance to read the config file without affecting
	// the global Viper state used elsewhere.
	viperReload := viper.New()
	viperReload.SetConfigFile(a.configPath)
	viperReload.SetEnvPrefix("ARGUS_SDK")
	viperReload.AutomaticEnv()

	if err := viperReload.ReadInConfig(); err != nil {
		return fmt.Errorf("reload: read config: %w", err)
	}

	// Update EUC watch list if eucCollector is available.
	if a.eucCollector != nil {
		aiEndpoints := viperReload.GetStringSlice("ingest.euc.ai_endpoints")
		localInferencePorts := viperReload.GetIntSlice("ingest.euc.local_inference_ports")
		a.eucCollector.UpdateWatchList(aiEndpoints, localInferencePorts)
		a.logger.Info("config reload: updated EUC watch list",
			zap.Strings("ai_endpoints", aiEndpoints),
			zap.Ints("local_inference_ports", localInferencePorts))
	}

	// Update logging level.
	level := viperReload.GetString("logging.level")
	switch level {
	case "debug":
		a.atomicLevel.SetLevel(zap.DebugLevel)
	case "warn":
		a.atomicLevel.SetLevel(zap.WarnLevel)
	case "error":
		a.atomicLevel.SetLevel(zap.ErrorLevel)
	default:
		a.atomicLevel.SetLevel(zap.InfoLevel)
	}
	a.logger.Info("config reload: updated logging level", zap.String("level", level))

	return nil
}
