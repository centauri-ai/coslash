package launch

import (
	"os/exec"

	"github.com/centauri-ai/coslash/collector/internal/backgroundexec"
)

func configureReviewProcess(command *exec.Cmd) {
	backgroundexec.HideConsole(command)
}
