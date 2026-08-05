// Command coslash serves the coSlash frontend and API from one loopback origin.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/collector"
	"github.com/centauri-ai/coslash/collector/internal/session"
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
	flags.IntVar(&opts.port, "port", defaultPort, "port to serve on, loopback only")
	flags.BoolVar(&opts.noOpen, "no-open", false, "do not open a browser on startup")
	flags.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if opts.port < 1 || opts.port > 65535 {
		return options{}, fmt.Errorf("--port must be between 1 and 65535, got %d", opts.port)
	}
	if extra := flags.Args(); len(extra) > 0 {
		return options{}, fmt.Errorf("unexpected argument %q", extra[0])
	}
	return opts, nil
}

func main() {
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

	mgr := synthesis.NewManager(synthesis.NewCLIRunner())
	if err := synthesis.EnsureDirs(); err != nil {
		log.Printf("initialize synthesis cache: %v", err)
		mgr = synthesis.NewManager(nil)
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
	url := "http://" + listener.Addr().String()
	log.Printf("listening on %s", url)
	if !opts.noOpen {
		if err := openBrowser(url); err != nil {
			log.Printf("could not open a browser (%v); open %s yourself", err, url)
		}
	}
	if err := http.Serve(listener, sameOrigin(routes(mgr), opts.port)); err != nil {
		log.Fatalf("coslash: %v", err)
	}
}

func sameOrigin(next http.Handler, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch fetchSite := r.Header.Get("Sec-Fetch-Site"); fetchSite {
		case "same-origin", "none":
		case "":
			if !sameOriginFallback(r) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		default:
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}

		// The Vite proxy must rewrite Host so dev requests pass this DNS-rebinding guard.
		host, requestPort, err := net.SplitHostPort(r.Host)
		if err != nil || requestPort != fmt.Sprint(port) || (host != "127.0.0.1" && !strings.EqualFold(host, "localhost")) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginFallback protects clients that do not send Fetch Metadata. An
// entirely headerless request is allowed so command-line clients keep working.
func sameOriginFallback(r *http.Request) bool {
	source := r.Header.Get("Origin")
	isOrigin := source != ""
	if !isOrigin {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		return true
	}

	u, err := url.Parse(source)
	if err != nil || u.User != nil || !strings.EqualFold(u.Scheme, "http") || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	// Origin is serialized as a scheme and authority only. Referer may include
	// the page path, query, and fragment.
	return !isOrigin || (u.Path == "" && u.RawQuery == "" && u.Fragment == "")
}

func routes(mgr *synthesis.Manager) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleList(w, r, mgr)
	})
	mux.HandleFunc("GET /api/synthesis", func(w http.ResponseWriter, r *http.Request) {
		handleSynthesis(w, r.URL.Query().Get("id"), mgr)
	})
	mux.HandleFunc("POST /api/launch", handleLaunch)
	// An unrouted /api path is a 404, never the frontend document.
	mux.Handle("/api/", http.NotFoundHandler())

	frontend, err := web.Handler()
	if err != nil {
		// A `make build` binary has no staged assets; keep its API usable for
		// `npm run dev` instead of failing to start.
		log.Printf("coslash: %v", err)
		frontend = unavailable(err)
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

func unavailable(err error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	})
}
