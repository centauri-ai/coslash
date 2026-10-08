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

	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

const releaseBase = "https://github.com/centauri-ai/coslash/releases/download/"
const maxUpdateAsset = 300 << 20

func updateTarget() (string, error) {
	channel := detectedInstallChannel()
	supported := runtime.GOOS == "darwin" && channel == "script" || runtime.GOOS == "windows" && channel == "windows-script"
	if !supported {
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
	if runtime.GOOS == "darwin" && filepath.Base(target) != "coslash" || runtime.GOOS == "windows" && !strings.EqualFold(filepath.Base(target), "coslash.exe") {
		return "", errors.New("update target has an unexpected name")
	}
	return target, nil
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

func prepareAutomaticUpdate(ctx context.Context, prompt syncv4.UpdatePrompt) (string, error) {
	target, err := updateTarget()
	if err != nil {
		return "", err
	}
	if !prompt.Available || !checkInVersionPattern.MatchString(prompt.Version) || len(prompt.Version) > 128 {
		return "", errors.New("invalid recommended update version")
	}
	release := "v" + prompt.Version
	asset, checksums := "", ""
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
			return "", errors.New("unsupported update architecture")
		}
		asset = fmt.Sprintf("coslash_%s_darwin_%s.tar.gz", release, runtime.GOARCH)
		checksums = "checksums.txt"
	case "windows":
		if runtime.GOARCH != "amd64" {
			return "", errors.New("unsupported update architecture")
		}
		asset = "coslash-windows-amd64.exe"
		checksums = "checksums-windows.txt"
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
	defer archive.Close()
	defer os.Remove(archive.Name())
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
	defer staged.Close()
	ok := false
	defer func() {
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
	defer helper.Close()
	if _, err := io.Copy(helper, current); err != nil {
		return err
	}
	if err := helper.Chmod(0o700); err != nil {
		return err
	}
	if err := helper.Close(); err != nil {
		return err
	}
	return startDetachedUpdateProcess(helper.Name(), "update-apply", target, staged)
}

func runUpdateApply(arguments []string) {
	if len(arguments) != 2 {
		return
	}
	target, staged := arguments[0], arguments[1]
	if !filepath.IsAbs(target) || !filepath.IsAbs(staged) || filepath.Dir(target) != filepath.Dir(staged) || !strings.HasPrefix(filepath.Base(staged), ".coslash-update-") {
		return
	}
	for deadline := time.Now().Add(time.Minute); runtimeOwnerActive() && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
	}
	if runtimeOwnerActive() {
		return
	}
	backup := target + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return
	}
	if err := startDetachedUpdateProcess(target, "--background"); err == nil {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			if runtimeOwnerActive() {
				_ = os.Remove(backup)
				return
			}
		}
	}
	_ = os.Remove(target)
	if os.Rename(backup, target) == nil {
		_ = startDetachedUpdateProcess(target, "--background")
	}
}

func watchAutomaticUpdates(queue *syncv4.Queue, prepared chan<- string, shutdown func()) {
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
		if config.AutoUpdate && prompt.Available && (prompt.Version != failedVersion || time.Now().After(retryAfter)) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			staged, err := prepareAutomaticUpdate(ctx, prompt)
			cancel()
			if err == nil {
				_, latestConfig, _ := queue.Policy()
				latestPrompt := queue.UpdatePrompt()
				if latestConfig.AutoUpdate && latestPrompt.Available && latestPrompt.Version == prompt.Version {
					prepared <- staged
					shutdown()
					return
				}
				_ = os.Remove(staged)
				continue
			}
			log.Printf("prepare coSlash Local update: %v", err)
			failedVersion, retryAfter = prompt.Version, time.Now().Add(15*time.Minute)
		}
		<-ticker.C
	}
}
