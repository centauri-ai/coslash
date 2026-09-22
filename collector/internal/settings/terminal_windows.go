package settings

func defaultTerminal() string {
	return TerminalWindows
}

func TerminalOptions() []TerminalOption {
	return []TerminalOption{{ID: TerminalWindows, Label: "Windows Terminal"}}
}

func migrateTerminal(terminal string) string {
	if terminal == "terminal" {
		return TerminalWindows
	}
	return terminal
}
