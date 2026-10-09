package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const releaseBase = "https://github.com/centauri-ai/coslash/releases/download/"
const maxUpdateAsset = 300 << 20

func updateTarget() (string, error) {
	channel := detectedInstallChannel()
	if !automaticUpdatesSupported(runtime.GOOS, channel, branchBuildMetadata == "true") {
		return "", errors.New("automatic updates require a supported script installation")
	}
	target, err := os.Executable()
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("update target must be a regular executable")
	}
	if runtime.GOOS != "windows" && filepath.Base(target) != "coslash" || runtime.GOOS == "windows" && !strings.EqualFold(filepath.Base(target), "coslash.exe") {
		return "", errors.New("update target has an unexpected name")
	}
	return target, nil
}

func automaticUpdatesSupported(goos, channel string, branchBuild bool) bool {
	return !branchBuild && ((goos == "darwin" || goos == "linux") && channel == "script" || goos == "windows" && channel == "windows-script")
}

func downloadRelease(ctx context.Context, name string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, name, nil)
	if err != nil {
		return nil, err
	}
	client := releaseClient()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximum {
		return nil, errors.New("release asset is unavailable or too large")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(content)) > maximum {
		return nil, errors.New("release asset could not be read within the size limit")
	}
	return content, nil
}

func releaseClient() *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || len(via) > 5 {
			return errors.New("release redirect is invalid")
		}
		switch request.URL.Hostname() {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		default:
			return errors.New("release redirected to an unexpected host")
		}
	}
	return client
}

const updateLockName = "update.lock"

func acquireUpdateLock() (*os.File, error) {
	home := settings.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(home, updateLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockRuntimeFileExclusive(file, true); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func updateInProgress() bool {
	return exclusiveRuntimeLockHeld(updateLockName)
}

func waitForRuntimeLock(ctx context.Context) (*os.File, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	executable, _ := os.Executable()
	runningFile, _ := os.Stat(executable)
	sawUpdate := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		updating := updateInProgress()
		if updating {
			sawUpdate = true
		}
		if !updating {
			if sawUpdate && runningFile != nil {
				if currentFile, err := os.Stat(executable); err == nil && !os.SameFile(runningFile, currentFile) {
					if err := reexecBackground(executable); err != nil {
						return nil, fmt.Errorf("restart updated coSlash Local: %w", err)
					}
					return nil, errors.New("restart updated coSlash Local returned")
				}
			}
			file, err := acquireRuntimeLock()
			if err == nil {
				return file, nil
			}
			if !errors.Is(err, errRuntimeAlreadyRunning) {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func downloadReleaseFile(ctx context.Context, name string, maximum int64) (*os.File, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, name, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := releaseClient().Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > maximum {
		return nil, "", errors.New("release asset is unavailable or too large")
	}
	file, err := os.CreateTemp("", "coslash-release-*")
	if err != nil {
		return nil, "", err
	}
	ok := false
	defer func() {
		if !ok {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maximum+1))
	if err != nil || count > maximum {
		return nil, "", errors.New("release asset could not be read within the size limit")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	ok = true
	return file, hex.EncodeToString(hash.Sum(nil)), nil
}

func updateAssetFor(goos, goarch, release string) (string, string, error) {
	switch {
	case (goos == "darwin" || goos == "linux") && (goarch == "arm64" || goarch == "amd64"):
		return fmt.Sprintf("coslash_%s_%s_%s.tar.gz", release, goos, goarch), "checksums.txt", nil
	case goos == "windows" && goarch == "amd64":
		return "coslash-windows-amd64.exe", "checksums-windows.txt", nil
	}
	return "", "", errors.New("unsupported update platform")
}

func prepareAutomaticUpdate(ctx context.Context, prompt syncv4.UpdatePrompt) (string, error) {
	target, err := updateTarget()
	if err != nil {
		return "", err
	}
	if !prompt.Available || !checkInVersionPattern.MatchString(prompt.Version) || len(prompt.Version) > 128 {
		return "", errors.New("invalid recommended update version")
	}
	release := "v" + prompt.Version
	asset, checksums, err := updateAssetFor(runtime.GOOS, runtime.GOARCH, release)
	if err != nil {
		return "", err
	}
	base := releaseBase + release + "/"
	checksumURL := base + checksums
	assetURL := base + asset
	manifest, err := downloadRelease(ctx, checksumURL, 64<<10)
	if err != nil {
		return "", err
	}
	expected := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset && len(fields[0]) == 64 {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return "", errors.New("release checksum is missing")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return "", errors.New("release checksum is invalid")
	}
	archive, digest, err := downloadReleaseFile(ctx, assetURL, maxUpdateAsset)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()
	if digest != expected {
		return "", errors.New("release checksum does not match")
	}
	dir := filepath.Dir(target)
	pattern := ".coslash-update-*"
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	staged, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		_ = staged.Close()
		if !ok {
			_ = os.Remove(staged.Name())
		}
	}()
	if runtime.GOOS == "windows" {
		if _, err = io.Copy(staged, archive); err != nil {
			return "", err
		}
	} else {
		reader, err := gzip.NewReader(archive)
		if err != nil {
			return "", err
		}
		defer reader.Close()
		tarReader := tar.NewReader(reader)
		want := strings.TrimSuffix(asset, ".tar.gz") + "/coslash"
		found := false
		for {
			header, nextErr := tarReader.Next()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return "", nextErr
			}
			if header.Name != want {
				continue
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || header.Size <= 0 || header.Size > maxUpdateAsset {
				return "", errors.New("release executable is invalid")
			}
			if _, err = io.CopyN(staged, tarReader, header.Size); err != nil {
				return "", err
			}
			found = true
			break
		}
		if !found {
			return "", errors.New("release executable is missing")
		}
	}
	if err := staged.Chmod(0o755); err != nil {
		return "", err
	}
	if err := staged.Sync(); err != nil {
		return "", err
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	output, err := exec.CommandContext(ctx, staged.Name(), "--version").Output()
	if err != nil || strings.TrimPrefix(strings.TrimSpace(string(output)), "v") != prompt.Version {
		return "", errors.New("release executable version does not match")
	}
	ok = true
	return staged.Name(), nil
}

func launchUpdateHelper(staged string) error {
	target, err := updateTarget()
	if err != nil {
		return err
	}
	current, err := os.Open(target)
	if err != nil {
		return err
	}
	defer current.Close()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	helper, err := os.CreateTemp("", "coslash-update-helper-*"+suffix)
	if err != nil {
		return err
	}
	helperPath := helper.Name()
	started := false
	defer func() {
		if !started {
			_ = helper.Close()
			_ = os.Remove(helperPath)
		}
	}()
	if _, err := io.Copy(helper, current); err != nil {
		return err
	}
	if err := helper.Chmod(0o700); err != nil {
		return err
	}
	if err := helper.Close(); err != nil {
		return err
	}
	readyPath := staged + ".ready"
	_ = os.Remove(readyPath)
	if err := startDetachedUpdateProcess(helperPath, "update-apply", target, staged, readyPath); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := os.ReadFile(readyPath)
		if err == nil {
			_ = os.Remove(readyPath)
			if strings.TrimSpace(string(state)) != "ready" {
				return errors.New("update helper could not claim the update handoff")
			}
			started = true
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("update helper did not claim the update handoff")
}

func runUpdateApply(arguments []string) {
	if len(arguments) != 3 {
		return
	}
	target, staged, readyPath := arguments[0], arguments[1], arguments[2]
	if !filepath.IsAbs(target) || !filepath.IsAbs(staged) || !filepath.IsAbs(readyPath) ||
		filepath.Dir(target) != filepath.Dir(staged) || filepath.Dir(staged) != filepath.Dir(readyPath) ||
		!strings.HasPrefix(filepath.Base(staged), ".coslash-update-") || filepath.Base(readyPath) != filepath.Base(staged)+".ready" {
		return
	}
	helper, _ := os.Executable()
	defer func() {
		if err := scheduleUpdateHelperCleanup(helper); err != nil {
			log.Printf("remove coSlash Local update helper: %v", err)
		}
	}()
	defer os.Remove(staged)
	updateLock, err := acquireUpdateLock()
	if err != nil {
		_ = os.WriteFile(readyPath, []byte("error"), 0o600)
		return
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = updateLock.Close()
		}
	}()
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		return
	}
	for deadline := time.Now().Add(time.Minute); runtimeOwnerActive() && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
	}
	if runtimeOwnerActive() {
		return
	}
	backup := target + ".previous"
	if err := replaceUpdateTarget(target, staged, backup); err != nil {
		log.Printf("stage coSlash Local replacement: %v", err)
		return
	}
	if err := updateLock.Close(); err != nil {
		log.Printf("release coSlash Local update handoff: %v", err)
		return
	}
	lockHeld = false
	if !backgroundLoginLoaded() {
		if err := startDetachedUpdateProcess(target, "--background"); err != nil {
			if restoreErr := restoreUpdateTarget(target, backup); restoreErr != nil {
				log.Printf("restore previous coSlash Local after launch failure: %v", restoreErr)
				return
			}
			_ = startDetachedUpdateProcess(target, "--background")
			return
		}
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if runtimeOwnerActive() {
			_ = os.Remove(backup)
			return
		}
	}
	if err := restoreUpdateTarget(target, backup); err != nil {
		log.Printf("restore previous coSlash Local after readiness timeout: %v", err)
		return
	}
	if !backgroundLoginLoaded() {
		_ = startDetachedUpdateProcess(target, "--background")
	}
}

func automaticUpdateNeeded(enabled bool, prompt syncv4.UpdatePrompt, runningVersion string) bool {
	return enabled && prompt.Available && prompt.Version != "" && prompt.Version != runningVersion
}

func watchAutomaticUpdates(queue *syncv4.Queue, onPrepared func(string) error) {
	if _, err := updateTarget(); err != nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	failedVersion := ""
	var retryAfter time.Time
	for {
		_, config, _ := queue.Policy()
		prompt := queue.UpdatePrompt()
		if automaticUpdateNeeded(config.AutoUpdate, prompt, normalizedVersion()) && (prompt.Version != failedVersion || time.Now().After(retryAfter)) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			staged, err := prepareAutomaticUpdate(ctx, prompt)
			cancel()
			if err == nil {
				_, latestConfig, _ := queue.Policy()
				latestPrompt := queue.UpdatePrompt()
				if latestConfig.AutoUpdate && latestPrompt.Available && latestPrompt.Version == prompt.Version {
					if err := onPrepared(staged); err == nil {
						return
					} else {
						log.Printf("start coSlash Local update: %v", err)
					}
				}
				_ = os.Remove(staged)
				failedVersion, retryAfter = prompt.Version, time.Now().Add(15*time.Minute)
				continue
			}
			log.Printf("prepare coSlash Local update: %v", err)
			failedVersion, retryAfter = prompt.Version, time.Now().Add(15*time.Minute)
		}
		<-ticker.C
	}
}
