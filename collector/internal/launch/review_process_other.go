//go:build !unix

package launch

import "os/exec"

func configureReviewProcess(*exec.Cmd) {}
