//go:build !windows

package main

import "github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/agent"

// runAgentLifecycle runs the agent in the foreground. On Unix-like systems the
// process is supervised by systemd/launchd, which deliver SIGTERM for shutdown
// and SIGHUP for reload — both handled by Agent.Run.
func runAgentLifecycle(a *agent.Agent) error {
	return a.Run()
}
