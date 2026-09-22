//go:build windows

package winprocess

import (
	"os"
	"testing"
)

func TestCurrentUserExecutableAcceptsCurrentProcess(t *testing.T) {
	path, err := CurrentUserExecutable(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("current process executable is empty")
	}
}
