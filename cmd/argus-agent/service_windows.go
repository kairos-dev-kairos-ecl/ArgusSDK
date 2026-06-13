//go:build windows

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent"
)

const (
	serviceName        = "argus-agent"
	serviceDisplayName = "Argus SDK Agent"
	serviceDescription = "Collects LLM and Shadow-AI signals and forwards them to configured destinations."
	defaultWinConfig   = `C:\ProgramData\argus-agent\agent.yaml`
)

// runAgentLifecycle runs as a Windows service when launched by the Service
// Control Manager, and as a normal console process otherwise (e.g. when run
// directly from a terminal for debugging).
func runAgentLifecycle(a *agent.Agent) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("determine windows service context: %w", err)
	}
	if !isService {
		return a.Run()
	}
	return svc.Run(serviceName, &argusService{agent: a})
}

// argusService adapts the agent lifecycle to the Windows Service Control Manager.
type argusService struct{ agent *agent.Agent }

// Execute is the SCM entry point. It starts the agent, reports Running, then
// handles Stop/Shutdown by gracefully stopping the agent.
func (s *argusService) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}
	if err := s.agent.Start(context.Background()); err != nil {
		// Non-zero exit code signals a service-specific error to the SCM.
		return false, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: accepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			_ = s.agent.Stop()
			return false, 0
		default:
			// Ignore other control requests.
		}
	}
	return false, 0
}

// init registers the `service` command tree (Windows only). On Unix the
// equivalent role is filled by the systemd unit / launchd plist shipped in the
// OS packages.
func init() {
	svcCmd := &cobra.Command{
		Use:   "service",
		Short: "Install and control the argus-agent Windows service",
	}
	svcCmd.AddCommand(
		&cobra.Command{Use: "install", Short: "Register the service with the SCM", RunE: serviceInstall},
		&cobra.Command{Use: "uninstall", Short: "Remove the service from the SCM", RunE: serviceUninstall},
		&cobra.Command{Use: "start", Short: "Start the service", RunE: serviceStart},
		&cobra.Command{Use: "stop", Short: "Stop the service", RunE: serviceStop},
	)
	rootCmd.AddCommand(svcCmd)
}

func serviceInstall(_ *cobra.Command, _ []string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	cfg := cfgFile
	if cfg == "" {
		cfg = defaultWinConfig
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(serviceName); err == nil {
		existing.Close()
		return fmt.Errorf("service %q already exists", serviceName)
	}

	s, err := m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName:  serviceDisplayName,
		Description:  serviceDescription,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "--config", cfg)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	fmt.Printf("installed service %q (config: %s)\n", serviceName, cfg)
	return nil
}

func serviceUninstall(_ *cobra.Command, _ []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	fmt.Printf("uninstalled service %q\n", serviceName)
	return nil
}

func serviceStart(_ *cobra.Command, _ []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	fmt.Printf("started service %q\n", serviceName)
	return nil
}

func serviceStop(_ *cobra.Command, _ []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed", serviceName)
	}
	defer s.Close()

	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	fmt.Printf("stopped service %q\n", serviceName)
	return nil
}
