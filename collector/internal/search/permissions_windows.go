package search

import "github.com/centauri-ai/coslash/collector/internal/windowsprivate"

func protectDirectory(path string) error { return windowsprivate.ProtectDirectory(path, "search") }
