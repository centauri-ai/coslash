package remote

import (
	"os"

	"github.com/centauri-ai/coslash/collector/internal/windowsprivate"
)

func readMetadataSequenceContent(path string) ([]byte, error) {
	return readPrivateWindowsFile(path, "metadata sequence")
}

func protectMetadataSequenceDirectory(path string) error {
	return protectPrivateWindowsDirectory(path, "metadata sequence")
}

func protectMetadataSequenceFile(path string, file *os.File) error {
	return protectPrivateWindowsFile(path, file, "metadata sequence")
}

func readPrivateWindowsFile(path, subject string) ([]byte, error) {
	return windowsprivate.ReadFile(path, subject, subject)
}

func openPrivateWindowsFile(path, subject string) (*os.File, error) {
	return windowsprivate.OpenFile(path, subject, subject)
}

func protectPrivateWindowsDirectory(path, subject string) error {
	return windowsprivate.ProtectDirectory(path, subject)
}

func protectPrivateWindowsFile(path string, file *os.File, subject string) error {
	return windowsprivate.ProtectFile(path, file, subject)
}
