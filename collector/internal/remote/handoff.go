package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/centauri-ai/coslash/collector/internal/launch"
)

const handoffNameBytes = 16

func StageHandoff(ctx context.Context, alias string, contents []byte) (string, error) {
	return stageHandoff(ctx, alias, contents, OpenOptions{})
}

func RemoveHandoff(ctx context.Context, alias, name string) error {
	return removeHandoff(ctx, alias, name, OpenOptions{})
}

func stageHandoff(ctx context.Context, alias string, contents []byte, options OpenOptions) (string, error) {
	nameBytes := make([]byte, handoffNameBytes)
	if _, err := rand.Read(nameBytes); err != nil {
		return "", fmt.Errorf("create remote handoff name: %w", err)
	}
	name := hex.EncodeToString(nameBytes)
	command := stageHandoffCommand(name, len(contents), int(launch.HandoffMaxAge.Seconds()))
	args, err := handoffSSHArgs(alias, command, int(options.Limits.withDefaults().ConnectTimeout.Seconds()))
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, options.Limits.withDefaults().Deadline)
	defer cancel()
	process, err := startHelper(runCtx, alias, args, cancel, options)
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), options.Limits.withDefaults().Deadline)
		defer cleanupCancel()
		_ = removeHandoff(cleanupCtx, alias, name, options)
	}()
	writeErr := <-process.writeStdin(contents)
	exitCode, waitErr := process.finish(writeErr != nil)
	if writeErr != nil {
		return "", fmt.Errorf("transfer remote handoff: %w", writeErr)
	}
	if process.stderr.overflow {
		return "", ErrStderrLimit
	}
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	if waitErr != nil || exitCode != 0 {
		if waitErr == nil {
			waitErr = fmt.Errorf("SSH exited with status %d", exitCode)
		}
		return "", wrapSSHError(fmt.Errorf("transfer remote handoff: %w", waitErr), process.stderr.String())
	}
	complete = true
	return name, nil
}

func stageHandoffCommand(name string, size, maxAgeSeconds int) string {
	return `umask 077; dir="$HOME"/'.coslash/handoffs'; handoff="$dir"/'` + name +
		`'; mkdir -p "$dir" && chmod 700 "$dir" || exit 1; ` +
		`trap 'rm -f "$handoff"' EXIT HUP INT TERM; ` +
		`cat > "$handoff" && [ "$(wc -c < "$handoff")" -eq ` + strconv.Itoa(size) +
		` ] && chmod 600 "$handoff" || exit 1; ` +
		`nohup sh -c 'sleep "$1"; rm -f "$2"' sh ` + strconv.Itoa(maxAgeSeconds) +
		` "$handoff" </dev/null >/dev/null 2>&1 & ` +
		`trap - EXIT HUP INT TERM`
}

func removeHandoff(ctx context.Context, alias, name string, options OpenOptions) error {
	if len(name) != handoffNameBytes*2 {
		return fmt.Errorf("invalid remote handoff name")
	}
	if _, err := hex.DecodeString(name); err != nil {
		return fmt.Errorf("invalid remote handoff name")
	}
	command := `rm -f "$HOME"/'.coslash/handoffs/` + name + `'`
	args, err := handoffSSHArgs(alias, command, int(options.Limits.withDefaults().ConnectTimeout.Seconds()))
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, options.Limits.withDefaults().Deadline)
	defer cancel()
	return runSSHCommand(runCtx, options, args)
}

func handoffSSHArgs(alias, command string, connectTimeoutSeconds int) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
		alias,
		command,
	}, nil
}
