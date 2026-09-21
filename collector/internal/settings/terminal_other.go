//go:build !darwin && !windows

package settings

const terminalOther = "terminal"

func defaultTerminal() string {
	return terminalOther
}

func TerminalOptions() []TerminalOption {
	return []TerminalOption{{ID: terminalOther, Label: "Terminal"}}
}
