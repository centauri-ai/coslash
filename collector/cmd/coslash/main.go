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
	"path/filepath"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/diagnostics"
	"github.com/centauri-ai/coslash/collector/internal/httpsec"
	"github.com/centauri-ai/coslash/collector/internal/session"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/synthesis"
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
	flags.IntVar(&opts.port, "port", defaultPort, "port to serve on, loopback only; 0 picks any free port")
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
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(runDoctor(os.Stdout, os.Stderr, os.Args[2:]))
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

	settingsStore := settings.Open()
	settingsState := settingsStore.State()
	var runner synthesis.Runner
	if !settingsState.Valid {
		log.Printf("settings: %s", settingsState.Error)
	} else if settingsState.Persisted {
		runner, _ = synthesis.NewRunner(settingsState.Config.Synthesis)
	}
	mgr := synthesis.NewManager(runner)
	if err := synthesis.EnsureDirs(); err != nil {
		log.Printf("initialize synthesis cache: %v", err)
		mgr.SetRunner(nil)
	}
	go mgr.Run(context.Background(), func() ([]*session.Session, error) {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return collector.List(today.UnixMilli())
	})
	go cleanupHandoffs()

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
		log.Printf("write API token: %v", err)
	}
	baseURL := "http://" + listener.Addr().String()
	accessURL := baseURL + "/#t=" + token
	log.Printf("listening on %s", baseURL)
	log.Printf("open %s", accessURL)
	if !opts.noOpen {
		if err := openBrowser(accessURL); err != nil {
			log.Printf("could not open a browser (%v); use the URL above", err)
		}
	}
	guard := httpsec.Guard{Addr: listener.Addr().String(), Token: token}
	server := newServer(guard, mgr, settingsStore)
	if err := server.Serve(listener); err != nil {
		log.Fatalf("coslash: %v", err)
	}
}

func newServer(guard httpsec.Guard, mgr *synthesis.Manager, settingsStore *settings.Store) *http.Server {
	return &http.Server{
		Handler:           guard.Wrap(routes(mgr, settingsStore)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

func routes(mgr *synthesis.Manager, settingsStore *settings.Store) *http.ServeMux {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleList(w, r, mgr)
	})
	api.HandleFunc("GET /api/synthesis", func(w http.ResponseWriter, r *http.Request) {
		handleSynthesis(w, r.URL.Query().Get("id"), mgr)
	})
	api.HandleFunc("GET /api/diff", func(w http.ResponseWriter, r *http.Request) {
		handleDiff(w, r, collector.Get)
	})
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, _ *http.Request) {
		writeSettings(w, settingsStore.State())
	})
	api.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		handleSaveSettings(w, r, settingsStore, mgr)
	})
	api.HandleFunc("POST /api/launch", func(w http.ResponseWriter, r *http.Request) {
		handleLaunch(w, r, settingsStore)
	})
	api.HandleFunc("GET /api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, diagnostics.Collect(r.Context(), version, false))
	})
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

func listen(port int) (net.Listener, error) {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("port %d is already in use; quit the other process or pass --port", port)
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
	path := filepath.Join(home, "token")
	temporary, err := os.CreateTemp(home, ".token-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
