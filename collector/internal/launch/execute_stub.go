//go:build !linux

package launch

func runAgentProcess(name string, args []string) error {
	return ErrUnsupportedOnHost
}
