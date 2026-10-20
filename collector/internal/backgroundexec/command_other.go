//go:build !windows

package backgroundexec

import "os/exec"

var CommandContext = exec.CommandContext
