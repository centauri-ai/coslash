package launch

func remoteSSHArgs(alias, command string) []string {
	return []string{"ssh", "-tt", alias, command}
}
