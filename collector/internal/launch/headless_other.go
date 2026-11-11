//go:build !linux

package launch

func HeadlessMain(string, string) int { return 125 }
