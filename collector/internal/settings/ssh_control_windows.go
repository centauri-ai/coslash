package settings

import "path/filepath"

func SSHControlPath() (string, error) {
	return filepath.Join(Home(), "ssh", "cm-%C"), nil
}
