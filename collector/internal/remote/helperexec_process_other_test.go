//go:build !unix

package remote

const processGroupTestSupported = false

func processExited(int) bool { return false }
