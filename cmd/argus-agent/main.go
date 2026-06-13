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
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent"
)

var cfgFile string

// version is the agent version. It is overridden at build time via
// -ldflags "-X main.version=<tag>" (see Dockerfile). Defaults to "dev".
var version = "dev"

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
	Version: version,
	RunE:    runAgent,
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
	logger, atomicLevel, err := buildLogger()
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

	a.SetReloadSources(cfgFile, atomicLevel)

	// runAgentLifecycle is platform-specific: on Windows it runs under the
	// Service Control Manager when launched as a service (and falls back to
	// console mode otherwise); elsewhere it runs in the foreground under
	// systemd/launchd supervision.
	return runAgentLifecycle(a, logger)
}

func buildLogger() (*zap.Logger, zap.AtomicLevel, error) {
	level := viper.GetString("logging.level")
	format := viper.GetString("logging.format")
	logFile := viper.GetString("logging.file")

	var atomicLevel zap.AtomicLevel
	switch level {
	case "debug":
		atomicLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		atomicLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		atomicLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		atomicLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Console/stdout core.
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	var consoleEnc zapcore.Encoder
	if format == "console" {
		consoleEnc = zapcore.NewConsoleEncoder(encCfg)
	} else {
		consoleEnc = zapcore.NewJSONEncoder(encCfg)
	}
	cores := []zapcore.Core{
		zapcore.NewCore(consoleEnc, zapcore.AddSync(os.Stdout), atomicLevel),
	}

	// Optional rotating file core. A Windows service (or any daemon) has no
	// console, so logging.file makes a deployed agent observable on disk. The
	// file is always JSON for machine parsing; lumberjack creates parent dirs
	// and rotates by size/age.
	if logFile != "" {
		fileWriter := zapcore.AddSync(&lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    50, // MB per file before rotation
			MaxBackups: 5,
			MaxAge:     30, // days
			Compress:   true,
		})
		cores = append(cores, zapcore.NewCore(
			zapcore.NewJSONEncoder(encCfg), fileWriter, atomicLevel,
		))
	}

	logger := zap.New(zapcore.NewTee(cores...), zap.AddCaller())
	return logger, atomicLevel, nil
}
