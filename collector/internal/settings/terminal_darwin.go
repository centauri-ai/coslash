package settings

const (
	TerminalApple = "terminal"
	TerminalITerm = "iterm2"
)

func defaultTerminal() string {
	return TerminalApple
}

func TerminalOptions() []TerminalOption {
	return []TerminalOption{
		{ID: TerminalApple, Label: "Apple Terminal"},
		{ID: TerminalITerm, Label: "iTerm2"},
	}
}

func migrateTerminal(terminal string) string { return terminal }
