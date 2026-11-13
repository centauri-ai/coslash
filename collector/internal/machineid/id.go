package machineid

import "strings"

// Normalize returns the platform id in the form Hub stores, or empty when the
// host cannot provide one. Pairing still works without it.
func Normalize(raw string) string {
	raw = strings.ToLower(strings.Trim(strings.TrimSpace(raw), "{}"))
	if len(raw) < 8 || len(raw) > 128 {
		return ""
	}
	for _, r := range raw {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return raw
}
