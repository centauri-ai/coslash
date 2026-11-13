//go:build darwin

package machineid

import (
	"os/exec"
	"strings"
)

func Current() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			return ""
		}
		return Normalize(strings.Trim(strings.TrimSpace(value), `"`))
	}
	return ""
}
