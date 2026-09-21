package settings

func defaultTerminal() string {
	return TerminalWindows
}

func TerminalOptions() []TerminalOption {
	return []TerminalOption{{ID: TerminalWindows, Label: "Windows Terminal"}}
}
