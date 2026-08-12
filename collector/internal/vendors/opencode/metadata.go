package opencode

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

var sessionIDPattern = regexp.MustCompile(`^ses_[0-9A-Za-z]+$`)

func loadMetadata() *vendors.SessionMetadata {
	metadata := emptyMetadata()
	output, err := exec.Command("ps", "-ww", "-axo", "command=").Output()
	if err != nil {
		return metadata
	}
	for id := range liveSessionIDs(string(output)) {
		metadata.Live[id] = "interactive"
	}
	return metadata
}

func liveSessionIDs(processes string) map[string]struct{} {
	live := map[string]struct{}{}
	for line := range strings.SplitSeq(processes, "\n") {
		args := strings.Fields(line)
		if len(args) == 0 || filepath.Base(args[0]) != "opencode" {
			continue
		}
		for index, argument := range args[1:] {
			var id string
			switch {
			case argument == "-s" || argument == "--session":
				if index+2 < len(args) {
					id = args[index+2]
				}
			case strings.HasPrefix(argument, "-s="):
				id = strings.TrimPrefix(argument, "-s=")
			case strings.HasPrefix(argument, "--session="):
				id = strings.TrimPrefix(argument, "--session=")
			}
			if sessionIDPattern.MatchString(id) {
				live[id] = struct{}{}
			}
		}
	}
	return live
}
