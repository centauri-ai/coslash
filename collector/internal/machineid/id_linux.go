//go:build linux

package machineid

import "os"

func Current() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if id := Normalize(string(raw)); id != "" {
			return id
		}
	}
	return ""
}
