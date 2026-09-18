// Command coslash serves the coSlash frontend and API from one loopback origin.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/remote"
	"github.com/centauri-ai/coslash/collector/internal/review"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "sessions", "handoff", "send", "review", "doctor":
			os.Exit(runCLI(os.Stdout, os.Stderr, os.Args[1:]))
		}
	}

	opts, err := parseOptions(os.Args[1:])
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
		log.Fatalf("coslash: %v", err)
	}
	defer runtimeLock.Close()

	settingsStore := settings.Open()
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
	sessionIDs := sync.OnceValues(collector.SessionIDIndex)
	if err := synthesis.EnsureDirs(); err != nil {
		log.Printf("initialize synthesis cache: %v", err)
		mgr.SetRunner(nil)
	} else if err := synthesis.MigrateLegacyCache(func(agent, id string) (bool, error) {
		index, err := sessionIDs()
		return index[agent][id], err
	}); err != nil {
		log.Printf("migrate synthesis cache: %v", err)
	}
	if err := synthesis.CleanupScratch(); err != nil {
		log.Printf("sweep synthesis scratch directories: %v", err)
	}
	go mgr.Run(context.Background(), func() ([]*session.Session, error) {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return collector.List(today.UnixMilli())
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
	server := newServer(guard, mgr, reviewManager, settingsStore, remoteManager, hub)
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
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("coslash: %v", err)
	}
}

func newProductionRemoteManager() (*remote.Manager, error) {
	return remote.NewProductionManager()
}

func newServer(
	guard httpsec.Guard,
	mgr *synthesis.Manager,
	reviewManager *review.Manager,
	settingsStore *settings.Store,
	remoteManager *remote.Manager,
	hub *hubclient.Client,
) *http.Server {
	server := &http.Server{
		Handler:           guard.Wrap(routes(mgr, reviewManager, settingsStore, remoteManager, hub)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	server.RegisterOnShutdown(remoteManager.Shutdown)
	server.RegisterOnShutdown(reviewManager.Shutdown)
	return server
}

func routes(
	mgr *synthesis.Manager,
	reviewManager *review.Manager,
	settingsStore *settings.Store,
	remoteManager *remote.Manager,
	hub *hubclient.Client,
) *http.ServeMux {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	getCanonicalSession := func(agent, id string) (*session.Session, error) {
		return canonicalSession(agent, id, mgr, collector.GetSessionForPreviewByAgent)
	}
	api.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
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
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, _ *http.Request) {
		writeSettings(w, settingsStore.State())
	})
	api.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		handleSaveSettings(w, r, settingsStore, mgr, remoteManager)
	})
	api.HandleFunc("POST /api/launch", func(w http.ResponseWriter, r *http.Request) {
		handleLaunch(w, r, settingsStore, remoteManager)
	})
	api.HandleFunc("POST /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		getSession := func(agent, id string) (*session.Session, error) {
			found, err := collector.GetSessionForPreviewByAgent(agent, id, 0)
			if found != nil {
				found.Synthesis = mgr.LookupLatest(found.Agent, found.ID)
			}
			return found, err
		}
		handleReview(w, r, settingsStore, getSession, launch.ReviewerAvailable, reviewManager.Start)
	})
	api.HandleFunc("GET /api/handoff", func(w http.ResponseWriter, r *http.Request) {
		handleHandoff(w, r, getCanonicalSession)
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
	registerHubRoutes(api, hub, remoteManager)
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
	path := filepath.Join(home, "token")
	temporary, err := os.CreateTemp(home, ".token-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := protectTokenFile(temporaryPath, temporary); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
