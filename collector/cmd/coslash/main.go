// Command coslash serves the coSlash frontend and API from one loopback origin.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/directedhandoff"
	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/inventory"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
	"github.com/centauri-ai/coslash/collector/internal/vendors/pi"
	"github.com/centauri-ai/coslash/collector/internal/web"
)

// version is injected by the release build; see collector/Makefile.
var version = "dev"

const defaultPort = 8787

type options struct {
	port        int
	noOpen      bool
	showVersion bool
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("coslash", flag.ContinueOnError)
	var opts options
	flags.IntVar(
		&opts.port,
		"port",
		defaultPort,
		"port to serve on, loopback only; 0 picks any free port",
	)
	flags.BoolVar(&opts.noOpen, "no-open", false, "do not open a browser on startup")
	flags.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	// 0 asks the kernel for a free port; the bound one is logged at startup.
	if opts.port < 0 || opts.port > 65535 {
		return options{}, fmt.Errorf("--port must be between 0 and 65535, got %d", opts.port)
	}
	if extra := flags.Args(); len(extra) > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", extra[0])
	}
	return opts, nil
}

func main() {
	arguments := os.Args[1:]
	var startupIntent *hubclient.LaunchIntent
	protocolRequest := len(arguments) > 0 && arguments[0] == "--protocol-url"
	if protocolRequest {
		if len(arguments) != 2 {
			fmt.Fprintln(os.Stderr, "coSlash activation could not be started; return to Hub and retry.")
			return
		}
		var alreadyRunning bool
		startupIntent, alreadyRunning = handleProtocolActivation(arguments[1])
		if alreadyRunning {
			return
		}
		arguments = []string{"--no-open"}
	}
	if !protocolRequest && len(os.Args) > 1 {
		switch os.Args[1] {
		case "ssh-auth":
			if len(os.Args) != 3 {
				log.Fatal("coslash ssh-auth requires one attempt ID")
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			if err := remote.RunAuthAttempt(ctx, os.Args[2]); err != nil {
				if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, remote.ErrAuthAttemptCancelled) {
					fmt.Fprintln(os.Stderr, "SSH authentication cancelled; return to coSlash.")
				} else {
					fmt.Fprintln(os.Stderr, "SSH authentication did not complete; return to coSlash and try again.")
				}
				return
			}
			fmt.Println("SSH authentication ready; return to coSlash.")
			return
		case "sessions", "handoff", "send", "review", "doctor", "mcp":
			os.Exit(runCLI(os.Stdout, os.Stderr, os.Args[1:]))
		}
	}

	opts, err := parseOptions(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("coslash: %v", err)
	}
	if opts.showVersion {
		fmt.Println(version)
		return
	}
	runtimeLock, err := acquireRuntimeLock()
	if err != nil {
		if startupIntent != nil && errors.Is(err, errRuntimeAlreadyRunning) && forwardWhenReady(startupIntent) {
			return
		}
		log.Fatalf("coslash: %v", err)
	}
	defer runtimeLock.Close()
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if err := registerProtocolHandler(); err != nil {
			log.Printf("Hub app launch handoff is unavailable; return to Hub and use the install/open recovery steps")
		}
	}

	settingsStore := settings.Open()
	var fingerprints *syncv4.Fingerprints
	if hubclient.ScaleImportEnabled() {
		if fingerprints, err = syncv4.OpenFingerprints(""); err != nil {
			log.Printf("parse cache unavailable: %v; parsing every pass", err)
		} else {
			vendors.SetParseCache(fingerprints)
		}
	}
	if err := pi.EnsureExtension(); err != nil {
		log.Printf("install Pi coSlash extension: %v", err)
	}
	if err := opencode.EnsurePlugin(); err != nil {
		log.Printf("install OpenCode coSlash plugin: %v", err)
	}
	settingsState := settingsStore.State()
	var runner synthesis.Runner
	if !settingsState.Valid {
		log.Printf("settings: %s", settingsState.Error)
	} else if settingsState.Persisted {
		runner, _ = synthesis.NewRunner(settingsState.Config.Synthesis)
	}
	mgr := synthesis.NewManager(runner)
	reviewManager := review.NewManager(launch.Review)
	directedStore, err := newDirectedHandoffStore()
	if err != nil {
		log.Fatalf("directed handoff store: %v", err)
	}
	if err := directedStore.RecoverReviews(); err != nil {
		log.Fatalf("recover directed reviews: %v", err)
	}
	if err := synthesis.EnsureDirs(); err != nil {
		log.Printf("initialize synthesis cache: %v", err)
		mgr.SetRunner(nil)
	} else if err := synthesis.MigrateLegacyCache(collector.NewLegacySessionExistsResolver()); err != nil {
		log.Printf("migrate synthesis cache: %v", err)
	}
	if err := synthesis.CleanupScratch(); err != nil {
		log.Printf("sweep synthesis scratch directories: %v", err)
	}
	go mgr.Run(context.Background(), func() ([]*session.Session, error) {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return collector.List(context.Background(), today.UnixMilli())
	})
	go cleanupHandoffs(settingsStore)
	remoteManager, err := newProductionRemoteManager()
	if err != nil {
		log.Fatalf("remote manager: %v", err)
	}
	if settingsState.Valid {
		if err := remoteManager.ApplySettings(settingsState.Config.Remote); err != nil {
			log.Printf("remote settings: %v", err)
		}
	}
	discoveryContext, stopDiscovery := context.WithCancel(context.Background())
	defer stopDiscovery()
	go runDirectedHandoffDiscovery(discoveryContext, directedStore, remoteManager)

	// Bind before opening the browser, so a port conflict is an error the user
	// reads rather than a browser tab pointed at nothing.
	listener, err := listen(opts.port)
	if err != nil {
		log.Fatalf("coslash: %v", err)
	}
	token, err := newToken()
	if err != nil {
		log.Fatalf("coslash: generate API token: %v", err)
	}
	if err := writeToken(token); err != nil {
		log.Fatalf("coslash: write API token: %v", err)
	}
	baseURL := "http://" + listener.Addr().String()
	if err := writeRuntime(baseURL); err != nil {
		log.Fatalf("coslash: write runtime descriptor: %v", err)
	}
	defer func() {
		if err := removeRuntime(baseURL, token); err != nil {
			log.Printf("remove runtime discovery: %v", err)
		}
	}()
	accessURL := baseURL + "/#t=" + token
	log.Printf("listening on %s", baseURL)
	log.Printf("open %s", accessURL)
	if !opts.noOpen {
		if err := openBrowser(accessURL); err != nil {
			log.Printf("could not open a browser (%v); use the URL above", err)
		}
	}
	guard := httpsec.Guard{Addr: listener.Addr().String(), Token: token}
	hub, err := hubClientFromEnvironment(version)
	if err != nil {
		log.Printf("Hub integration disabled: %v", err)
	}
	var queue *syncv4.Queue
	if hub != nil && os.Getenv("COSLASH_V4_SYNC_ENABLED") == "1" {
		queue, err = syncv4.Open("")
		if err != nil {
			log.Fatalf("coslash: initialize v4 sync queue: %v", err)
		}
	}
	onboardings := newOnboardingManager(version)
	onboardings.SetV4SyncActive(queue != nil)
	server := newServer(guard, mgr, reviewManager, settingsStore, remoteManager, hub,
		serverServices{queue: queue, directedStore: directedStore, onboardings: onboardings})
	onboardings.StartCheckIns(hub)
	if startupIntent != nil {
		switch startupIntent.Action {
		case "pair":
			if err := onboardings.StartPairing(startupIntent.HubURL.String(), startupIntent.AttemptID, startupIntent.LaunchToken); err != nil {
				fmt.Fprintln(os.Stderr, "coSlash could not start Hub pairing; return to Hub and retry.")
			}
		case "check-in":
			go func() { _ = onboardings.RetryCheckIn(startupIntent.HubURL.String()) }()
		}
	}
	wake := make(chan struct{}, 1)
	inventoryTracker := &inventory.Tracker{}
	if fingerprints != nil && queue != nil {
		go runInventory(discoveryContext, fingerprints, queue, wake, inventoryTracker, func(ctx context.Context) bool {
			credential, err := hub.Credentials.Load(ctx)
			return err == nil && credential != ""
		})
	}
	if queue != nil {
		syncContext, stopSync := context.WithCancel(context.Background())
		server.RegisterOnShutdown(stopSync)
		runner := &syncv4.Runner{
			Version: version,
			Queue:   queue, Backup: hub.Backup, Hub: hub,
			Discover:          func(ctx context.Context) ([]*session.Session, error) { return collector.List(ctx, 0) },
			InventoryProgress: func() (int64, bool) { return inventoryTracker.FilesSoFar(), inventoryTracker.Running() },
			Conditions:        syncv4.LocalConditions,
			LocalPause:        func() bool { return settingsStore.State().Config.SyncPaused },
			Command:           v4CommandRunner(queue, settingsStore, remoteManager),
		}
		if fingerprints != nil {
			runner.DiscoverBatches = func(ctx context.Context, visit func(syncv4.DiscoveryBatch) error) error {
				var resume *inventory.Cursor
				if saved, ok := fingerprints.LoadDiscoveryCursor(); ok {
					resume = inventory.DecodeCursor(saved)
				}
				for batch, err := range inventory.Discover(ctx, inventory.DiscoverOptions{Resume: resume}) {
					if err != nil {
						return err
					}
					if err := visit(syncv4.DiscoveryBatch{Sessions: batch.Sessions, ContentBytes: batch.ContentBytes}); err != nil {
						return err
					}
					encoded, err := json.Marshal(batch.Cursor)
					if err != nil {
						return err
					}
					if err := fingerprints.SaveDiscoveryCursor(encoded); err != nil {
						return err
					}
				}
				return nil
			}
		}
		go func() {
			runV4SyncLoop(syncContext, runner, queue, hub.V4Wait, wake)
		}()
	}
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	runtimeReady, err := acquireRuntimeReadiness()
	if err != nil {
		log.Fatalf("coslash: acquire runtime readiness: %v", err)
	}
	defer runtimeReady.Close()
	serveErr := server.Serve(listener)
	stopDiscovery()
	directedStore.Shutdown()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("coslash: %v", serveErr)
	}
}

// runInventory takes the stat-only inventory at startup and every sync
// interval, records it for check-in, prunes cache entries whose source is
// gone, and wakes the sync loop once so the first inventory checks in within
// seconds of pairing.
func runInventory(ctx context.Context, fingerprints *syncv4.Fingerprints, queue *syncv4.Queue, wake chan<- struct{}, tracker *inventory.Tracker, paired func(context.Context) bool) {
	const interval = 5 * time.Minute
	first := true
	for {
		if !paired(ctx) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}
		snapshot, err := inventory.Scan(ctx, inventory.Options{Tracker: tracker})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("inventory: %v", err)
		} else {
			report := snapshot.Inventory()
			log.Printf("inventory: %d files, %d bytes in %d ms", report.Files, report.Bytes, report.DurationMs)
			if queue != nil {
				if err := queue.SetInventory(report); err != nil {
					log.Printf("inventory: record for check-in: %v", err)
				}
			}
			if removed, err := fingerprints.Prune(inventory.KeepCached(ctx, snapshot)); err != nil {
				log.Printf("parse cache prune: %v", err)
			} else if removed > 0 {
				log.Printf("parse cache: pruned %d entries", removed)
			}
			if first {
				first = false
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func newProductionRemoteManager() (*remote.Manager, error) {
	return remote.NewProductionManager()
}

type serverServices struct {
	queue         *syncv4.Queue
	directedStore *directedhandoff.Store
	onboardings   *onboardingManager
}

func newServer(
	guard httpsec.Guard,
	mgr *synthesis.Manager,
	reviewManager *review.Manager,
	settingsStore *settings.Store,
	remoteManager *remote.Manager,
	hub *hubclient.Client,
	services ...serverServices,
) *http.Server {
	var service serverServices
	if len(services) > 0 {
		service = services[0]
	}
	if service.onboardings == nil {
		service.onboardings = newOnboardingManager(version)
	}
	server := &http.Server{
		Handler:           guard.Wrap(routesWithOnboarding(mgr, reviewManager, settingsStore, remoteManager, hub, service.onboardings, service)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	server.RegisterOnShutdown(remoteManager.Shutdown)
	server.RegisterOnShutdown(reviewManager.Shutdown)
	server.RegisterOnShutdown(service.onboardings.Close)
	if service.directedStore != nil {
		server.RegisterOnShutdown(service.directedStore.Shutdown)
	}
	return server
}

func routes(
	mgr *synthesis.Manager,
	reviewManager *review.Manager,
	settingsStore *settings.Store,
	remoteManager *remote.Manager,
	hub *hubclient.Client,
	services ...serverServices,
) *http.ServeMux {
	return routesWithOnboarding(mgr, reviewManager, settingsStore, remoteManager, hub, newOnboardingManager(version), services...)
}

func routesWithOnboarding(
	mgr *synthesis.Manager,
	reviewManager *review.Manager,
	settingsStore *settings.Store,
	remoteManager *remote.Manager,
	hub *hubclient.Client,
	onboardings *onboardingManager,
	services ...serverServices,
) *http.ServeMux {
	var service serverServices
	if len(services) > 0 {
		service = services[0]
	}
	mux := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("GET /api/hub/v4-update", func(w http.ResponseWriter, _ *http.Request) {
		if service.queue == nil {
			writeJSON(w, syncv4.UpdatePrompt{})
			return
		}
		writeJSON(w, service.queue.UpdatePrompt())
	})
	getCanonicalSession := func(agent, id string) (*session.Session, error) {
		return canonicalSession(agent, id, mgr, collector.GetSessionForPreviewByAgent)
	}
	api.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("id") {
			handleExactSession(w, r, collector.GetSessionForPreviewByAgent)
			return
		}
		handleList(w, r, mgr, reviewManager, remoteManager)
	})
	api.HandleFunc("GET /api/session-detail", func(w http.ResponseWriter, r *http.Request) {
		handleSessionDetail(w, r, collector.GetSessionDetail, remoteManager)
	})
	api.HandleFunc("GET /api/synthesis", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteSource(w, r) {
			return
		}
		query := r.URL.Query()
		handleSynthesis(w, query.Get("agent"), query.Get("id"), mgr)
	})
	api.HandleFunc("GET /api/diff", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("session") {
			handleExactDiff(w, r, collector.GetSessionChanges, remoteManager)
			return
		}
		if rejectRemoteSource(w, r) {
			return
		}
		handleDiff(w, r, collector.GetSessionFacts)
	})
	api.HandleFunc("GET /api/share-preview", func(w http.ResponseWriter, r *http.Request) {
		handleSharePreview(w, r, collector.GetSessionForPreview, remoteManager, version)
	})
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeSettings(r.Context(), w, settingsStore.State())
	})
	api.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		handleSaveSettings(w, r, settingsStore, mgr, remoteManager)
	})
	api.HandleFunc("POST /api/launch", func(w http.ResponseWriter, r *http.Request) {
		handleLaunch(w, r, settingsStore, remoteManager)
	})
	api.HandleFunc("POST /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("source") != localSourceID {
			handleRemoteReview(w, r, settingsStore,
				func(source, agent, id string) (*session.Session, string, error) {
					return remoteManager.LaunchSession(source, agent, id, launch.NewSession)
				},
				launch.RemoteReviewerOptions, reviewManager.Start)
			return
		}
		getSession := func(agent, id string) (*session.Session, error) {
			found, err := collector.GetSessionForPreviewByAgent(agent, id, 0)
			if found != nil {
				found.Synthesis = contextSynthesis(mgr, found)
			}
			return found, err
		}
		handleReview(w, r, settingsStore, getSession, func(reviewer string) bool {
			return launch.ReviewCLIAvailable(r.Context(), reviewer)
		}, reviewManager.Start)
	})
	api.HandleFunc("GET /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		handleReviewStatus(w, r, reviewManager)
	})
	api.HandleFunc("GET /api/reviews/options", func(w http.ResponseWriter, r *http.Request) {
		handleRemoteReviewOptions(w, r, func(source string) (string, bool) {
			state := settingsStore.State()
			if !state.Valid || state.Config.Remote == nil || !state.Config.Remote.Enabled || state.Config.Remote.ID != source {
				return "", false
			}
			health := remoteManager.DiagnosticsHealth()
			return state.Config.Remote.SSHAlias, health.SourceID == source &&
				(health.State == remote.StateOK || health.State == remote.StateLimited)
		}, launch.RemoteReviewerOptions)
	})
	api.HandleFunc("GET /api/handoff", func(w http.ResponseWriter, r *http.Request) {
		handleHandoff(w, r, getCanonicalSession)
	})
	directedStore := service.directedStore
	api.HandleFunc("GET /api/directed-handoffs/targets", func(w http.ResponseWriter, r *http.Request) {
		handleDirectedHandoffTargets(w, r, settingsStore)
	})
	api.HandleFunc("GET /api/directed-handoffs", func(w http.ResponseWriter, _ *http.Request) {
		if directedStore == nil {
			writeJSON(w, struct {
				Handoffs []directedhandoff.Record `json:"handoffs"`
			}{[]directedhandoff.Record{}})
			return
		}
		writeJSON(w, struct {
			Handoffs []directedhandoff.Record `json:"handoffs"`
		}{directedStore.List()})
	})
	api.HandleFunc("POST /api/directed-handoffs", func(w http.ResponseWriter, r *http.Request) {
		handleDirectedHandoffStart(w, r, directedStore, settingsStore, remoteManager, mgr)
	})
	api.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		handleSend(w, r, settingsStore, getCanonicalSession, launch.ReviewerAvailable, launch.TerminalWithPrompt)
	})
	api.HandleFunc("POST /api/remote/test", func(w http.ResponseWriter, r *http.Request) {
		handleRemoteTest(w, r, remoteManager)
	})
	api.HandleFunc("POST /api/remote/retry", func(w http.ResponseWriter, r *http.Request) {
		handleRemoteRetry(w, r, remoteManager)
	})
	api.HandleFunc("GET /api/remote/status", func(w http.ResponseWriter, r *http.Request) {
		handleRemoteStatus(w, r, remoteManager)
	})
	api.HandleFunc("POST /api/remote/helper/setup", func(w http.ResponseWriter, r *http.Request) {
		handleRemoteHelperSetup(w, r, remoteManager)
	})
	api.HandleFunc("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, diagnostics.CollectWithRemote(r.Context(), version, false, remoteHealthFact(remoteManager)))
	})
	var backupManager *sessionbackupproducer.Manager
	if hub != nil {
		backupManager = sessionbackupproducer.New(sessionbackupproducer.Options{
			CollectorVersion: version, Remote: remoteManager, Synthesis: mgr,
		})
	}
	registerHubRoutes(api, hub, remoteManager, backupManager, onboardings)
	mux.Handle("/api", api)
	mux.Handle("/api/", api)

	frontend, err := web.Handler()
	if err != nil {
		// A `make build` binary has no staged assets; keep its API usable for
		// `npm run dev` instead of failing to start.
		log.Printf("coslash: frontend unavailable: %v", err)
		frontend = unavailable()
	}
	mux.Handle("/", frontend)
	return mux
}

func remoteHealthFact(manager *remote.Manager) *diagnostics.RemoteHealth {
	health := manager.DiagnosticsHealth()
	if health.SourceID == "" {
		return nil
	}
	var reason *string
	if health.Reason != nil {
		value := string(*health.Reason)
		reason = &value
	}
	var helperReason *string
	if health.Helper != nil && health.Helper.Reason != nil {
		value := string(*health.Helper.Reason)
		helperReason = &value
	}
	remoteHealth := &diagnostics.RemoteHealth{
		SourceID: health.SourceID, Label: health.Label, State: string(health.State),
		Complete: health.Complete, Reason: reason, LastSuccessAtMs: health.LastSuccessAtMs,
		CoverageSinceMs: health.CoverageSinceMs, RoundTripMs: health.RoundTripMs,
		Error:     health.Error,
		Transport: string(health.Transport), RequestBytes: health.Metrics.RequestBytes,
		ResponseBytes: health.Metrics.ResponseBytes, Records: health.Metrics.Records,
		HelperReason: helperReason,
	}
	if health.Helper != nil {
		remoteHealth.HelperState = string(health.Helper.State)
		remoteHealth.HelperVersion = health.Helper.Version
		remoteHealth.HelperCompatible = health.Helper.Compatible
		remoteHealth.HelperFallback = health.Helper.Fallback
	}
	return remoteHealth
}

func listen(port int) (net.Listener, error) {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf(
				"port %d is already in use; quit the other process or pass --port",
				port,
			)
		}
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	return listener, nil
}

func unavailable() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "frontend unavailable", http.StatusServiceUnavailable)
	})
}

func newToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func writeToken(token string) error {
	home := settings.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if err := protectTokenDirectory(home); err != nil {
		return err
	}
	return writeTokenFile(home, token)
}
