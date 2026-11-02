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

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/google/uuid"
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
const updateConfirmationLockName = "update-confirm.lock"

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
	if !prompt.Available || !hubclient.NewerClientVersion(normalizedVersion(), prompt.Version) {
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

func launchUpdateHelper(staged, attemptID string) error {
	target, err := updateTarget()
	if err != nil {
		return err
	}
	if err := verifyUpdateVersions(target, staged); err != nil {
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
	args := []string{"update-apply", target, staged, readyPath}
	if attemptID != "" {
		args = append(args, attemptID)
	}
	if err := startDetachedUpdateProcess(helperPath, args...); err != nil {
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
	if len(arguments) != 3 && len(arguments) != 4 {
		return
	}
	target, staged, readyPath := arguments[0], arguments[1], arguments[2]
	attemptID := ""
	if len(arguments) == 4 {
		attemptID = arguments[3]
		if uuid.Validate(attemptID) != nil {
			return
		}
	}
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
	if attemptID != "" {
		confirmation, err := os.OpenFile(filepath.Join(settings.Home(), updateConfirmationLockName), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			_ = os.WriteFile(readyPath, []byte("error"), 0o600)
			return
		}
		if err := lockRuntimeFileExclusive(confirmation, true); err != nil {
			_ = confirmation.Close()
			_ = os.WriteFile(readyPath, []byte("error"), 0o600)
			return
		}
		defer confirmation.Close()
	}
	if err := verifyUpdateVersions(target, staged); err != nil {
		_ = os.WriteFile(readyPath, []byte("error"), 0o600)
		return
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		return
	}
	for deadline := time.Now().Add(time.Minute); runtimeOwnerActive() && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
	}
	if runtimeOwnerActive() {
		writeUpdateFailure(attemptID)
		return
	}
	if err := verifyUpdateVersions(target, staged); err != nil {
		log.Printf("refuse coSlash Local replacement: %v", err)
		writeUpdateFailure(attemptID)
		if !backgroundLoginLoaded() {
			_ = updateLock.Close()
			lockHeld = false
			if err := startDetachedUpdateProcess(target, "--background"); err != nil {
				log.Printf("restart coSlash Local after refused replacement: %v", err)
			}
		}
		return
	}
	backup := target + ".previous"
	if err := replaceUpdateTarget(target, staged, backup); err != nil {
		log.Printf("stage coSlash Local replacement: %v", err)
		writeUpdateFailure(attemptID)
		if !backgroundLoginLoaded() {
			_ = updateLock.Close()
			lockHeld = false
			_ = startDetachedUpdateProcess(target, "--background")
		}
		return
	}
	if err := updateLock.Close(); err != nil {
		log.Printf("release coSlash Local update handoff: %v", err)
		writeUpdateFailure(attemptID)
		return
	}
	lockHeld = false
	if !backgroundLoginLoaded() {
		if err := startDetachedUpdateProcess(target, "--background"); err != nil {
			writeUpdateFailure(attemptID)
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
			if attemptID != "" {
				if err := writeUpdateOutcome(attemptID, "update_ready"); err != nil {
					log.Printf("record update readiness: %v", err)
					return
				}
			}
			_ = os.Remove(backup)
			return
		}
	}
	if runtimeOwnerActive() {
		if attemptID != "" {
			if err := writeUpdateOutcome(attemptID, "update_ready"); err != nil {
				log.Printf("record update readiness: %v", err)
				return
			}
		}
		_ = os.Remove(backup)
		return
	}
	if err := restoreUpdateTarget(target, backup); err != nil {
		writeUpdateFailure(attemptID)
		log.Printf("restore previous coSlash Local after readiness timeout: %v", err)
		return
	}
	writeUpdateFailure(attemptID)
	if !backgroundLoginLoaded() {
		_ = startDetachedUpdateProcess(target, "--background")
	}
}

func automaticUpdateNeeded(enabled bool, prompt syncv4.UpdatePrompt, runningVersion string) bool {
	return enabled && prompt.Available && hubclient.NewerClientVersion(runningVersion, prompt.Version)
}

func verifyUpdateVersions(target, staged string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readVersion := func(executable string) (string, error) {
		return readExecutableVersion(ctx, executable)
	}
	return verifyUpdateVersionsWith(target, staged, readVersion)
}

func readExecutableVersion(ctx context.Context, executable string) (string, error) {
	command := exec.CommandContext(ctx, executable, "--version")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", errors.New("cannot verify executable version")
	}
	if err := command.Start(); err != nil {
		return "", errors.New("cannot verify executable version")
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, 257))
	if len(output) > 256 {
		_ = command.Process.Kill()
	}
	if err := command.Wait(); err != nil || readErr != nil || len(output) > 256 {
		return "", errors.New("cannot verify executable version")
	}
	return strings.TrimPrefix(strings.TrimSpace(string(output)), "v"), nil
}

var errLegacyInstalledChanged = errors.New("installed executable changed during legacy update")

// Retiring the helper's only backup prevents a later legacy rollback. If the
// rollback won the rename race, the second version check detects it instead.
func settleLegacyInstallation() (string, error) {
	if !automaticUpdatesSupported(runtime.GOOS, detectedInstallChannel(), branchBuildMetadata == "true") {
		return "", errors.New("legacy settlement requires a supported script installation")
	}
	target, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		target = strings.TrimSuffix(target, " (deleted)")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" && filepath.Base(target) != "coslash" || runtime.GOOS == "windows" && !strings.EqualFold(filepath.Base(target), "coslash.exe") {
		return "", errors.New("legacy update target has an unexpected name")
	}
	lock, err := acquireUpdateLock()
	if err != nil {
		return "", err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = settleLegacyTarget(target, normalizedVersion(), func(name string) (string, error) { return readExecutableVersion(ctx, name) })
	return target, err
}

func settleLegacyTarget(target, running string, readVersion func(string) (string, error)) error {
	check := func() error {
		info, err := os.Lstat(target)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("installed executable unavailable")
		}
		version, err := readVersion(target)
		if err != nil {
			return err
		}
		if version != running {
			return errLegacyInstalledChanged
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	backup := target + ".previous"
	if info, statErr := os.Lstat(backup); statErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("legacy update backup is not a regular file")
		}
		if err := retireLegacyBackup(backup, check); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return check()
}

func verifyUpdateVersionsWith(target, staged string, readVersion func(string) (string, error)) error {
	current, err := readVersion(target)
	if err != nil {
		return err
	}
	next, err := readVersion(staged)
	if err != nil || !hubclient.NewerClientVersion(current, next) {
		return errors.New("update must be newer than the installed executable")
	}
	return nil
}

func writeUpdateFailure(id string) {
	if id == "" {
		return
	}
	if err := writeUpdateOutcome(id, "update_failed"); err != nil {
		log.Printf("record update failure: %v", err)
	}
}

func writeUpdateOutcome(id, outcome string) error {
	if uuid.Validate(id) != nil || (outcome != "update_failed" && outcome != "update_ready") {
		return errors.New("invalid update outcome")
	}
	dir := filepath.Join(settings.Home(), "update-results")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".result-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, writeErr := file.WriteString(outcome)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, id))
}

func consumeUpdateFailure(queue *syncv4.Queue) bool {
	update := queue.PendingUpdate()
	if update == nil || uuid.Validate(update.ID) != nil {
		return false
	}
	name := filepath.Join(settings.Home(), "update-results", update.ID)
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(name)
	if err != nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(file, 64))
	_ = file.Close()
	if err != nil || string(body) != "update_failed" {
		return false
	}
	if err := queue.SetUpdateStage(update.ID, "failed", "update_failed"); err != nil {
		return false
	}
	_ = os.Remove(name)
	return true
}

// A new target must not report completion until the replacement helper has
// observed runtime readiness and committed to keeping the replacement.
func waitForUpdateHandoff(ctx context.Context, queue *syncv4.Queue, current string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		update := queue.PendingUpdate()
		if update == nil || update.LegacySettlement || update.Stage == "failed" || update.TargetVersion != current || update.HandoffReady {
			return nil
		}
		if consumeUpdateFailure(queue) {
			return nil
		}
		name := filepath.Join(settings.Home(), "update-results", update.ID)
		info, err := os.Lstat(name)
		if err == nil && info.Mode().IsRegular() {
			file, err := os.Open(name)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(file, 64))
				_ = file.Close()
				if readErr == nil && string(body) == "update_ready" {
					if err := queue.ConfirmUpdateHandoff(update.ID); err != nil {
						return err
					}
					_ = os.Remove(name)
					return nil
				}
			}
		}
		lock, lockErr := os.OpenFile(filepath.Join(settings.Home(), updateConfirmationLockName), os.O_RDWR, 0)
		if errors.Is(lockErr, os.ErrNotExist) {
			return queue.ConfirmUpdateHandoff(update.ID)
		}
		if lockErr == nil {
			if lockRuntimeFileExclusive(lock, true) == nil {
				err := queue.ConfirmUpdateHandoff(update.ID)
				_ = lock.Close()
				return err
			}
			_ = lock.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func watchAutomaticUpdates(queue *syncv4.Queue, requested <-chan struct{}, onPrepared func(string, string) error, onLegacyRollback func(string), reportUpdate func()) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	failedVersion := ""
	var retryAfter time.Time
	var nextAutomatic time.Time
	var nextLegacySettlement time.Time
	firstCheck := true
	for {
		explicit := false
		if firstCheck {
			firstCheck = false
		} else {
			select {
			case <-requested:
				explicit = true
			case <-ticker.C:
			}
		}
		if consumeUpdateFailure(queue) {
			reportUpdate()
		}
		if update := queue.PendingUpdate(); update != nil {
			if update.LegacySettlement {
				if update.HandoffReady || time.Now().Before(nextLegacySettlement) {
					continue
				}
				target, err := settleLegacyInstallation()
				if err == nil {
					_ = queue.ConfirmUpdateHandoff(update.ID)
					continue
				}
				if errors.Is(err, errLegacyInstalledChanged) {
					_ = queue.SetUpdateStage(update.ID, "failed", "update_failed")
					reportUpdate()
					onLegacyRollback(target)
					return
				}
				nextLegacySettlement = time.Now().Add(30 * time.Second)
				continue
			}
			if update.Stage == "failed" || !hubclient.NewerClientVersion(normalizedVersion(), update.TargetVersion) {
				continue
			}
			if _, err := updateTarget(); err != nil {
				_ = queue.SetUpdateStage(update.ID, "failed", "update_unavailable")
				reportUpdate()
				continue
			}
			if update.Stage == "picked_up" {
				if err := queue.SetUpdateStage(update.ID, "preparing", ""); err != nil {
					continue
				}
				reportUpdate()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			staged, err := prepareAutomaticUpdate(ctx, syncv4.UpdatePrompt{Available: true, Version: update.TargetVersion})
			cancel()
			if err == nil {
				if err = queue.SetUpdateStage(update.ID, "finalizing", ""); err == nil {
					err = onPrepared(staged, update.ID)
				}
				if err == nil {
					return
				}
				_ = os.Remove(staged)
			}
			_ = queue.SetUpdateStage(update.ID, "failed", "update_failed")
			reportUpdate()
			continue
		}
		if !explicit && time.Now().Before(nextAutomatic) {
			continue
		}
		nextAutomatic = time.Now().Add(time.Minute)
		if _, err := updateTarget(); err != nil {
			continue
		}
		_, config, _ := queue.Policy()
		prompt := queue.UpdatePrompt()
		requestedVersionAvailable := explicit && prompt.Available && hubclient.NewerClientVersion(normalizedVersion(), prompt.Version)
		shouldUpdate := requestedVersionAvailable || automaticUpdateNeeded(config.AutoUpdate, prompt, normalizedVersion())
		if shouldUpdate && (explicit || prompt.Version != failedVersion || time.Now().After(retryAfter)) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			staged, err := prepareAutomaticUpdate(ctx, prompt)
			cancel()
			if err == nil {
				_, latestConfig, _ := queue.Policy()
				latestPrompt := queue.UpdatePrompt()
				if (explicit || latestConfig.AutoUpdate) && latestPrompt.Available && latestPrompt.Version == prompt.Version && hubclient.NewerClientVersion(normalizedVersion(), latestPrompt.Version) {
					if err := onPrepared(staged, ""); err == nil {
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
	}
}
