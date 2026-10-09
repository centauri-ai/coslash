//go:build linux

package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSystemdUnitQuotesExecutableAndCarriesDataLocations(t *testing.T) {
	env := map[string]string{
		"COSLASH_HOME": "/home/dev/.coslash-dev/50% $off",
		"PATH":         "/home/dev/.local/bin:/usr/bin",
		"CODEX_HOME":   "/ignored",
	}
	unit, err := systemdUnit(`/home/dev/my "bin"/coslash`, func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{
		`ExecStart="/home/dev/my \"bin\"/coslash" --background`,
		`Environment="COSLASH_HOME=/home/dev/.coslash-dev/50%% $$off"`,
		`Environment="PATH=/home/dev/.local/bin:/usr/bin"`,
		"Restart=always\n", "KillMode=process\n", "WantedBy=default.target\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("unit is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "CODEX_HOME") {
		t.Errorf("unit carries an unlisted variable:\n%s", text)
	}
	if _, err := systemdUnit("/bin/cos\nlash", func(string) string { return "" }); err == nil {
		t.Fatal("systemdUnit accepted a newline in the executable path")
	}
}

func TestCrontabEntryIsIdempotentAndPreservesOtherJobs(t *testing.T) {
	existing := "MAILTO=dev\n0 * * * * /usr/bin/backup\n@reboot '/old/coslash' --background >/dev/null 2>&1 # coSlash Local\n"
	updated, changed, err := crontabWithEntry(existing, "/home/dev/.local/bin/coslash", "")
	if err != nil || !changed {
		t.Fatalf("crontabWithEntry = %v, %v", changed, err)
	}
	want := "MAILTO=dev\n0 * * * * /usr/bin/backup\n@reboot '/home/dev/.local/bin/coslash' --background >/dev/null 2>&1 # coSlash Local\n"
	if updated != want {
		t.Fatalf("crontab =\n%s\nwant\n%s", updated, want)
	}
	if again, changed, err := crontabWithEntry(updated, "/home/dev/.local/bin/coslash", ""); err != nil || changed || again != updated {
		t.Fatalf("second crontabWithEntry changed the crontab: %v, %v", changed, err)
	}
	withHome, _, err := crontabWithEntry("", "/opt/it's/coslash", "/data/coslash")
	if err != nil || withHome != "@reboot COSLASH_HOME='/data/coslash' '/opt/it'\\''s/coslash' --background >/dev/null 2>&1 # coSlash Local\n" {
		t.Fatalf("crontab with COSLASH_HOME = %q, %v", withHome, err)
	}
	if _, _, err := crontabWithEntry("", "/opt/100%/coslash", ""); err == nil {
		t.Fatal("crontabWithEntry accepted a percent sign, which cron treats as a newline")
	}
}

func TestBackgroundLoginLoadedTreatsPendingRestartAsSupervised(t *testing.T) {
	previousCommand, previousLookPath := backgroundCommand, backgroundLookPath
	t.Cleanup(func() { backgroundCommand, backgroundLookPath = previousCommand, previousLookPath })
	backgroundLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	for state, want := range map[string]bool{"active": true, "activating": true, "inactive": false, "failed": false} {
		backgroundCommand = func(_ string, args ...string) ([]byte, error) {
			if strings.Join(args, " ") == "--user is-system-running" {
				return []byte("degraded\n"), errors.New("exit status 1")
			}
			return []byte(state + "\n"), nil
		}
		if got := backgroundLoginLoaded(); got != want {
			t.Errorf("backgroundLoginLoaded with ActiveState=%s = %t, want %t", state, got, want)
		}
	}
	backgroundCommand = func(string, ...string) ([]byte, error) {
		return []byte("offline\n"), errors.New("exit status 1")
	}
	if backgroundLoginLoaded() {
		t.Error("backgroundLoginLoaded without a running user manager = true")
	}
}

func TestBackgroundPersistenceNeedsLingerForSystemd(t *testing.T) {
	previousCommand, previousLookPath, previousWrite := backgroundCommand, backgroundLookPath, writeCrontab
	t.Cleanup(func() {
		backgroundCommand, backgroundLookPath, writeCrontab = previousCommand, previousLookPath, previousWrite
	})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	backgroundLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	for _, allowLinger := range []bool{false, true} {
		linger, calls, crontab := "no", []string{}, ""
		backgroundCommand = func(name string, args ...string) ([]byte, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			switch {
			case call == "systemctl --user is-system-running":
				return []byte("running\n"), nil
			case strings.HasPrefix(call, "loginctl show-user"):
				return []byte(linger + "\n"), nil
			case call == "loginctl enable-linger":
				if !allowLinger {
					return []byte("Could not enable linger: Access denied\n"), errors.New("exit status 1")
				}
				linger = "yes"
			case call == "crontab -l":
				return []byte("no crontab for dev\n"), errors.New("exit status 1")
			}
			return nil, nil
		}
		writeCrontab = func(contents string) error { crontab = contents; return nil }
		mode, err := ensureBackgroundPersistence()
		enabled := strings.Contains(strings.Join(calls, "\n"), "systemctl --user enable "+systemdUnitName)
		if allowLinger && (err != nil || mode != "systemd" || !enabled || crontab != "") {
			t.Errorf("with lingering: mode=%q err=%v enabled=%t crontab=%q", mode, err, enabled, crontab)
		}
		if !allowLinger && (err != nil || mode != "cron" || enabled || !strings.HasSuffix(crontab, cronMarker+"\n")) {
			t.Errorf("without lingering: mode=%q err=%v enabled=%t crontab=%q", mode, err, enabled, crontab)
		}
		if !allowLinger && startSupervisedBackground() {
			t.Error("started a user unit that would stop at logout without lingering")
		}
	}
}
