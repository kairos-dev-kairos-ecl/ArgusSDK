//go:build !windows

package main

import (
	"go.uber.org/zap"

	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent"
)

// runAgentLifecycle runs the agent in the foreground. On Unix-like systems the
// process is supervised by systemd/launchd, which deliver SIGTERM for shutdown
// and SIGHUP for reload — both handled by Agent.Run. The logger is unused here
// (the agent logs internally); it is accepted to match the Windows signature.
func runAgentLifecycle(a *agent.Agent, _ *zap.Logger) error {
	return a.Run()
}
