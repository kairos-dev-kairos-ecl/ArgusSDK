// Command argus-agent is the ArgusSDK signal collection and forwarding agent.
// It collects signals from local instrumentation libraries (Python/TypeScript)
// and routes them to configured output destinations (ArgusXDR, Kafka, Splunk, etc.).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent"
)

var cfgFile string

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "argus-agent",
	Short: "Argus SDK signal collection and forwarding agent",
	Long: `argus-agent collects signals from LLM instrumentation libraries and
routes them to configured output destinations (ArgusXDR, Kafka, Splunk, etc.).`,
	RunE: runAgent,
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./agent.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level: debug|info|warn|error")
	rootCmd.PersistentFlags().String("log-format", "json", "log format: json|console")
	_ = viper.BindPFlag("logging.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindPFlag("logging.format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("agent")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/argus-agent/")
	}

	// ARGUS_SDK_* environment variables override YAML values.
	viper.SetEnvPrefix("ARGUS_SDK")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		// Config file is optional at startup; agent will fail later if
		// required fields (group_id, outputs) are missing.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "config read error: %v\n", err)
		}
	}
}

func runAgent(_ *cobra.Command, _ []string) error {
	logger, err := buildLogger()
	if err != nil {
		return fmt.Errorf("logger init: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	var cfg agent.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("config unmarshal: %w", err)
	}

	a, err := agent.New(&cfg, logger)
	if err != nil {
		return fmt.Errorf("agent init: %w", err)
	}

	return a.Run()
}

func buildLogger() (*zap.Logger, error) {
	level := viper.GetString("logging.level")
	format := viper.GetString("logging.format")

	var zapCfg zap.Config
	if format == "console" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}

	switch level {
	case "debug":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapCfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	return zapCfg.Build()
}
