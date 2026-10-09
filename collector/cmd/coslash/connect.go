package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const (
	connectExitOK                = 0
	connectExitArguments         = 2
	connectExitInvalid           = 3
	connectExitUnreachable       = 4
	connectExitDeclined          = 5
	connectExitUnsupported       = 6
	connectExitCredentialStore   = 7
	connectExitRetryable         = 8
	connectExitCredentialRevoked = 9
)

var connectSignalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

type connectJob struct {
	state     string
	updatedAt time.Time
}

const (
	connectJobClaimed                = "claimed"
	connectJobRetrying               = "retrying"
	connectJobCheckingIn             = "checking_in"
	connectJobConnected              = "connected"
	connectJobDeclined               = "declined"
	connectJobExpired                = "expired"
	connectJobCredentialStoreFailed  = "credential_store_failed"
	connectJobCredentialRevoked      = "credential_revoked"
	connectJobCheckInRetryable       = "check_in_retryable"
	connectFirstCheckInRetryWindow   = 30 * time.Second
	connectFirstCheckInRetryInterval = 2 * time.Second
	maxConnectCredentialLoadFailures = 3
)

type syncHooks interface {
	Ensure(*hubclient.Client) error
	RequestPass(string)
}

type syncHookFuncs struct {
	ensure func(*hubclient.Client) error
	pass   func(string)
}

func (hooks syncHookFuncs) Ensure(client *hubclient.Client) error {
	if hooks.ensure != nil {
		return hooks.ensure(client)
	}
	return nil
}

func (hooks syncHookFuncs) RequestPass(reason string) {
	if hooks.pass != nil {
		hooks.pass(reason)
	}
}

type connectOptions struct {
	code string
	hub  string
	json bool
}

func parseConnectArgs(args []string) (connectOptions, bool, error) {
	var opts connectOptions
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return opts, true, nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--hub":
			i++
			if i == len(args) || opts.hub != "" {
				return opts, false, errors.New("--hub requires one origin")
			}
			opts.hub = args[i]
		case strings.HasPrefix(arg, "--hub="):
			if opts.hub != "" || len(arg) == len("--hub=") {
				return opts, false, errors.New("--hub requires one origin")
			}
			opts.hub = strings.TrimPrefix(arg, "--hub=")
		case arg == "--json":
			opts.json = true
		case strings.HasPrefix(arg, "-"):
			return opts, false, fmt.Errorf("unknown option %q", arg)
		case opts.code == "":
			opts.code = arg
		default:
			return opts, false, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	if opts.code == "" || opts.hub == "" {
		return opts, false, errors.New("usage: coslash connect CODE --hub ORIGIN [--json]")
	}
	if !hubclient.ValidConnectCode(opts.code) {
		return opts, false, errors.New("invalid connect code")
	}
	if _, err := hubOrigin(opts.hub); err != nil {
		return opts, false, errors.New("invalid Hub origin")
	}
	return opts, false, nil
}

func runConnectCLI(stdout, stderr io.Writer, args []string) int {
	opts, help, err := parseConnectArgs(args)
	if help {
		fmt.Fprintln(stdout, "usage: coslash connect CODE --hub ORIGIN [--json]")
		return connectExitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return connectExitArguments
	}
	ctx, stop := connectSignalContext()
	defer stop()
	client, alreadyRunning, err := ensureBackgroundLocal()
	if err != nil {
		return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
	}
	if !alreadyRunning && !opts.json {
		fmt.Fprintln(stdout, "coSlash Local is running in the background.")
	}
	body, _ := json.Marshal(map[string]string{"hubUrl": opts.hub, "connectCode": strings.ToUpper(opts.code)})
	data, err := client.request(http.MethodPost, "/api/hub/onboarding/connect", bytes.NewReader(body))
	if err != nil {
		return connectErrorFromLocal(stdout, stderr, opts, err)
	}
	var started struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if json.Unmarshal(data, &started) != nil || started.ID == "" || started.State != "claimed" {
		return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
	}
	if !opts.json {
		fmt.Fprintln(stdout, "✓ Found your Hub setup. Return to Hub and click \"Connect this computer\".")
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			if opts.json {
				writeConnectJSON(stdout, "waiting", connectExitOK)
			} else {
				fmt.Fprintln(stdout, "coSlash Local keeps waiting in the background. Finish in Hub.")
			}
			return connectExitOK
		}
		data, err = client.request(http.MethodGet, "/api/hub/onboarding/connect/"+started.ID, nil)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
		}
		var status struct {
			State string `json:"state"`
		}
		if json.Unmarshal(data, &status) != nil {
			return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
		}
		switch status.State {
		case connectJobConnected:
			if opts.json {
				reportBackgroundPersistence(io.Discard)
				writeConnectJSON(stdout, status.State, connectExitOK)
			} else {
				fmt.Fprintln(stdout, "✓ Connected. Your recent sessions are syncing to My space.")
				reportBackgroundPersistence(stdout)
			}
			return connectExitOK
		case connectJobDeclined:
			return printConnectFailure(stdout, stderr, opts, connectExitDeclined, "")
		case connectJobExpired:
			return printConnectFailure(stdout, stderr, opts, connectExitInvalid, "")
		case connectJobCredentialStoreFailed:
			return printConnectFailure(stdout, stderr, opts, connectExitCredentialStore, "")
		case connectJobCredentialRevoked:
			return printConnectFailure(stdout, stderr, opts, connectExitCredentialRevoked, "")
		case connectJobCheckInRetryable:
			return printConnectFailure(stdout, stderr, opts, connectExitRetryable, "")
		case connectJobClaimed, connectJobRetrying, connectJobCheckingIn:
		default:
			return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
}

func printConnectFailure(stdout, stderr io.Writer, opts connectOptions, code int, detail string) int {
	if opts.json {
		state := map[int]string{connectExitInvalid: "connect_code_invalid", connectExitUnreachable: "unreachable",
			connectExitDeclined: "declined", connectExitUnsupported: "unsupported",
			connectExitCredentialStore: connectJobCredentialStoreFailed, connectExitRetryable: connectJobCheckInRetryable,
			connectExitCredentialRevoked: connectJobCredentialRevoked}[code]
		writeConnectJSON(stdout, state, code)
		return code
	}
	var message string
	switch code {
	case connectExitInvalid:
		message = "This connect code is invalid or expired. Get a new command in Hub."
	case connectExitUnreachable:
		message = "Couldn't reach " + opts.hub + ". Check your connection and run the command again."
	case connectExitDeclined:
		message = "This setup was declined in Hub. Nothing was connected."
	case connectExitCredentialStore:
		message = "Local could not access the secure credential store. Resolve the system credential store issue and retry."
	case connectExitRetryable:
		message = "Pairing was saved, but the first Hub check-in has not completed. Local will keep retrying; check connection status later."
	case connectExitCredentialRevoked:
		message = "Hub revoked this pairing. Start a new connect command in Hub to pair again."
	case connectExitUnsupported:
		message = "This Hub doesn't support connect codes yet. Use Devices → Add device in Hub."
	default:
		message = "coSlash Local could not start; return to Hub and retry."
	}
	if detail != "" {
		fmt.Fprintln(stderr, detail)
	}
	fmt.Fprintln(stderr, message)
	return code
}

func connectErrorFromLocal(stdout, stderr io.Writer, opts connectOptions, err error) int {
	switch strings.TrimSpace(err.Error()) {
	case "connect_code_invalid":
		return printConnectFailure(stdout, stderr, opts, connectExitInvalid, "")
	case "connect_unsupported":
		return printConnectFailure(stdout, stderr, opts, connectExitUnsupported, "")
	default:
		return printConnectFailure(stdout, stderr, opts, connectExitUnreachable, "")
	}
}

func writeConnectJSON(writer io.Writer, state string, exitCode int) {
	_ = json.NewEncoder(writer).Encode(map[string]any{"state": state, "exitCode": exitCode})
}

func ensureBackgroundLocal() (*localAPIClient, bool, error) {
	return ensureBackgroundLocalWith(startBackgroundProcess)
}

func ensureBackgroundLocalWith(start func() error) (*localAPIClient, bool, error) {
	if runtimeOwnerActive() {
		client, err := newLocalAPIClient()
		return client, true, err
	}
	if err := start(); err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeOwnerActive() {
			client, err := newLocalAPIClient()
			if err == nil {
				return client, false, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, false, errors.New("background Local did not start")
}

func newConnectJobID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *onboardingManager) StartConnectCode(rawHubURL, code string) (string, error) {
	origin, err := hubOrigin(rawHubURL)
	if err != nil || !hubclient.ValidConnectCode(code) {
		return "", errors.New("invalid connect request")
	}
	jobID, err := newConnectJobID()
	if err != nil {
		return "", errors.New("could not prepare connect request")
	}
	if err := writeStoredHubURL(origin); err != nil {
		return "", errors.New("Hub address could not be saved")
	}
	hubURL, _ := url.Parse(origin)
	client, err := hubClientForURL(m.version, hubURL.String())
	if err != nil {
		return "", hubclient.ErrConnectUnsupported
	}
	claimContext, cancelClaim := context.WithTimeout(m.ctx, 30*time.Second)
	pairing, err := client.ClaimOnboardingCode(claimContext, code)
	cancelClaim()
	if err != nil {
		return "", err
	}
	m.setHubClient(client)
	key := "connect/" + jobID
	ctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	m.connectJobs[jobID] = connectJob{state: connectJobClaimed, updatedAt: time.Now()}
	m.active[key] = cancel
	m.wait.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wait.Done()
		defer m.finish(key)
		m.runConnectApproval(ctx, client, pairing, jobID)
	}()
	return jobID, nil
}

func (m *onboardingManager) runConnectApproval(ctx context.Context, client *hubclient.Client, pairing hubclient.PairingResult, jobID string) {
	interval := time.Duration(pairing.IntervalSeconds) * time.Second
	if interval < time.Second || interval > 30*time.Second {
		interval = 2 * time.Second
	}
	for time.Now().Before(pairing.ExpiresAt) {
		if ctx.Err() != nil {
			return
		}
		result, err := client.PollPairing(ctx, pairing.PairingID)
		if err != nil {
			m.setConnectJobState(jobID, connectJobRetrying)
		} else {
			switch result.State {
			case hubclient.PairingStatePaired:
				m.setConnectJobState(jobID, connectJobCheckingIn)
				m.confirmFirstCheckIn(ctx, client, jobID)
				return
			case hubclient.PairingStateCredentialStoreFailed:
				m.setConnectJobState(jobID, connectJobCredentialStoreFailed)
				return
			case hubclient.PairingStateDeclined:
				m.setConnectJobState(jobID, connectJobDeclined)
				return
			case hubclient.PairingStateExpired:
				m.setConnectJobState(jobID, connectJobExpired)
				return
			case hubclient.PairingStateRetrying:
				m.setConnectJobState(jobID, connectJobRetrying)
			case hubclient.PairingStatePending:
				m.setConnectJobState(jobID, connectJobClaimed)
			default:
				m.setConnectJobState(jobID, connectJobRetrying)
			}
		}
		if !sleepContext(ctx, interval) {
			return
		}
	}
	m.setConnectJobState(jobID, connectJobExpired)
}

func (m *onboardingManager) confirmFirstCheckIn(ctx context.Context, client *hubclient.Client, jobID string) {
	deadline := time.Now().Add(connectFirstCheckInRetryWindow)
	delay := connectFirstCheckInRetryInterval
	storeFailures := 0
	retryStateSet := false
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := client.CheckIn(ctx, m.version); err == nil {
			m.ensureSync(client)
			m.setConnectJobState(jobID, connectJobConnected)
			return
		} else if errors.Is(err, hubclient.ErrCredentialStoreUnavailable) {
			storeFailures++
			if storeFailures >= maxConnectCredentialLoadFailures {
				m.setConnectJobState(jobID, connectJobCredentialStoreFailed)
				return
			}
		} else {
			var problem hubclient.V4Problem
			if errors.As(err, &problem) && problem.Code == "device_revoked" {
				m.ensureSync(client)
				m.setConnectJobState(jobID, connectJobCredentialRevoked)
				return
			}
			storeFailures = 0
		}
		if !retryStateSet && !time.Now().Before(deadline) {
			m.setConnectJobState(jobID, connectJobCheckInRetryable)
			retryStateSet = true
		} else if !retryStateSet {
			m.setConnectJobState(jobID, connectJobCheckingIn)
		}
		if !sleepContext(ctx, delay) {
			return
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func (m *onboardingManager) setConnectJobState(jobID, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.connectJobs[jobID]
	if !ok {
		return
	}
	job.state, job.updatedAt = state, time.Now()
	m.connectJobs[jobID] = job
}

func (m *onboardingManager) ConnectJobState(jobID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.connectJobs[jobID]
	return job.state, ok
}
