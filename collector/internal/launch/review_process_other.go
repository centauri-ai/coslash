//go:build !unix && !windows

package launch

import "os/exec"

func configureReviewProcess(*exec.Cmd) {}
