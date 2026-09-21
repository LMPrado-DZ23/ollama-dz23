//go:build !windows

package mcpbridge

import "os/exec"

func prepareCommand(cmd *exec.Cmd) {}
