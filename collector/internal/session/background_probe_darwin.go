package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

var volumeCaseSensitivity sync.Map
var canonicalHomeDirectories sync.Map

// Background enrichment must not cause macOS to request access to a project
// folder merely because an old transcript mentions it.
func backgroundFilesystemProbeAllowed(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	path = filepath.Clean(path)
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	home, ok := canonicalHomeDirectory(home)
	if !ok {
		return false
	}
	return resolvedBackgroundProbePathAllowed(path, home, volumeIsCaseSensitive("/"), volumeIsCaseSensitive(home))
}

func resolvedBackgroundProbePathAllowed(path, home string, rootCaseSensitive, homeCaseSensitive bool) bool {
	var seen map[string]struct{}
	for links := 0; links <= 40; links++ {
		if !filepath.IsAbs(path) {
			return false
		}
		path = filepath.Clean(path)
		if backgroundProbePathRestricted(path, home, rootCaseSensitive, homeCaseSensitive) {
			return false
		}

		current := string(filepath.Separator)
		components := strings.Split(strings.Trim(path, string(filepath.Separator)), string(filepath.Separator))
		resolvedLink := false
		for index, component := range components {
			if component == "" {
				continue
			}
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				return false
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}

			target, err := os.Readlink(current)
			if err != nil {
				return false
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			if suffix := filepath.Join(components[index+1:]...); suffix != "." {
				target = filepath.Join(target, suffix)
			}
			path = filepath.Clean(target)
			if seen == nil {
				seen = make(map[string]struct{})
			}
			if _, ok := seen[path]; ok {
				return false
			}
			seen[path] = struct{}{}
			resolvedLink = true
			break
		}
		if !resolvedLink {
			return true
		}
	}
	return false
}

func canonicalHomeDirectory(home string) (string, bool) {
	if cached, ok := canonicalHomeDirectories.Load(home); ok {
		return cached.(string), true
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", false
	}
	cached, _ := canonicalHomeDirectories.LoadOrStore(home, resolved)
	return cached.(string), true
}

func backgroundProbePathRestricted(path, home string, rootCaseSensitive, homeCaseSensitive bool) bool {
	if withinPath(path, "/Volumes", rootCaseSensitive) || withinPath(path, "/Network", rootCaseSensitive) {
		return true
	}
	for _, relative := range []string{
		"Desktop", "Documents", "Downloads",
		filepath.Join("Library", "Mobile Documents"),
		filepath.Join("Library", "CloudStorage"),
	} {
		if withinPath(path, filepath.Join(home, relative), homeCaseSensitive) {
			return true
		}
	}
	return false
}

func withinPath(path, root string, caseSensitive bool) bool {
	if caseSensitive {
		return path == root || len(path) > len(root) && path[len(root)] == filepath.Separator && path[:len(root)] == root
	}
	return strings.EqualFold(path, root) ||
		len(path) > len(root) && path[len(root)] == filepath.Separator && strings.EqualFold(path[:len(root)], root)
}

func volumeIsCaseSensitive(path string) bool {
	if cached, ok := volumeCaseSensitivity.Load(path); ok {
		return cached.(bool)
	}

	sensitive, ok := readVolumeCaseSensitivity(path)
	if !ok {
		// Unknown volume behavior is treated as case-insensitive to avoid probing a protected path.
		sensitive = false
	}
	cached, _ := volumeCaseSensitivity.LoadOrStore(path, sensitive)
	return cached.(bool)
}

func readVolumeCaseSensitivity(path string) (bool, bool) {
	pathPointer, err := syscall.BytePtrFromString(path)
	if err != nil {
		return false, false
	}
	attributes := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_CAPABILITIES,
	}
	var result struct {
		length       uint32
		capabilities [4]uint32
		valid        [4]uint32
	}
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathPointer)),
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&result)),
		uintptr(unsafe.Sizeof(result)),
		0,
		0,
	)
	runtime.KeepAlive(pathPointer)
	runtime.KeepAlive(&attributes)
	runtime.KeepAlive(&result)
	if errno != 0 || result.length < uint32(unsafe.Sizeof(result)) {
		return false, false
	}
	const caseSensitiveCapability = uint32(0x00000100)
	if result.valid[0]&caseSensitiveCapability == 0 {
		return false, false
	}
	return result.capabilities[0]&caseSensitiveCapability != 0, true
}
