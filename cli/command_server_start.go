package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"contrib.go.opencensus.io/exporter/prometheus"
	"github.com/pkg/errors"
	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/kopia/kopia/internal/server"
	"github.com/kopia/kopia/repo"
)

var (
	serverStartCommand         = serverCommands.Command("start", "Start Kopia server").Default()
	serverStartHTMLPath        = serverStartCommand.Flag("html", "Server the provided HTML at the root URL").ExistingDir()
	serverStartUI              = serverStartCommand.Flag("ui", "Start the server with HTML UI (EXPERIMENTAL)").Bool()
	serverStartRefreshInterval = serverStartCommand.Flag("refresh-interval", "Frequency for refreshing repository status").Default("10s").Duration()

	serverStartRandomPassword = serverStartCommand.Flag("random-password", "Generate random password and print to stderr").Hidden().Bool()
	serverStartAutoShutdown   = serverStartCommand.Flag("auto-shutdown", "Auto shutdown the server if API requests not received within given time").Hidden().Duration()
)

func init() {
	setupConnectOptions(serverStartCommand)
	serverStartCommand.Action(optionalRepositoryAction(runServer))
}

func runServer(ctx context.Context, rep *repo.Repository) error {
	srv, err := server.New(ctx, rep, server.Options{
		ConfigFile:      repositoryConfigFileName(),
		ConnectOptions:  connectOptions(),
		RefreshInterval: *serverStartRefreshInterval,
	})
	if err != nil {
		return errors.Wrap(err, "unable to initialize server")
	}

	if err = srv.SetRepository(ctx, rep); err != nil {
		return errors.Wrap(err, "error connecting to repository")
	}

	mux := http.NewServeMux()

	mux.Handle("/api/", srv.APIHandlers())

	if *serverStartHTMLPath != "" {
		fileServer := http.FileServer(http.Dir(*serverStartHTMLPath))
		mux.Handle("/", fileServer)
	} else if *serverStartUI {
		mux.Handle("/", serveIndexFileForKnownUIRoutes(http.FileServer(server.AssetFile())))
	}

	httpServer := &http.Server{Addr: stripProtocol(*serverAddress)}
	srv.OnShutdown = httpServer.Shutdown

	onCtrlC(func() {
		log(ctx).Infof("Shutting down...")

		if err = httpServer.Shutdown(ctx); err != nil {
			log(ctx).Warningf("unable to shut down: %v", err)
		}
	})

	mux = requireCredentials(mux)

	// init prometheus after adding interceptors that require credentials, so that this
	// handler can be called without auth
	if err = initPrometheus(mux); err != nil {
		return errors.Wrap(err, "error initializing Prometheus")
	}

	var handler http.Handler = mux

	if as := *serverStartAutoShutdown; as > 0 {
		log(ctx).Infof("starting a watchdog to stop the server if there's no activity for %v", as)
		handler = startServerWatchdog(handler, as, func() {
			if serr := httpServer.Shutdown(ctx); err != nil {
				log(ctx).Warningf("unable to stop the server: %v", serr)
			}
		})
	}

	httpServer.Handler = handler

	err = startServerWithOptionalTLS(ctx, httpServer)
	if err != http.ErrServerClosed {
		return err
	}

	return srv.SetRepository(ctx, nil)
}

func initPrometheus(mux *http.ServeMux) error {
	reg := prom.NewRegistry()
	if err := reg.Register(prom.NewProcessCollector(prom.ProcessCollectorOpts{})); err != nil {
		return errors.Wrap(err, "error registering process collector")
	}

	if err := reg.Register(prom.NewGoCollector()); err != nil {
		return errors.Wrap(err, "error registering go collector")
	}

	pe, err := prometheus.NewExporter(prometheus.Options{
		Registry: reg,
	})

	if err != nil {
		return errors.Wrap(err, "unable to initialize prometheus exporter")
	}

	mux.Handle("/metrics", pe)

	return nil
}

func stripProtocol(addr string) string {
	return strings.TrimPrefix(strings.TrimPrefix(addr, "https://"), "http://")
}

func isKnownUIRoute(path string) bool {
	return strings.HasPrefix(path, "/snapshots/") ||
		strings.HasPrefix(path, "/policies") ||
		strings.HasPrefix(path, "/repo")
}

func serveIndexFileForKnownUIRoutes(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isKnownUIRoute(r.URL.Path) {
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = "/"
			r = r2
		}
		h.ServeHTTP(w, r)
	})
}

func requireCredentials(handler http.Handler) *http.ServeMux {
	if *serverPassword != "" {
		handler = requireAuth{handler, *serverUsername, *serverPassword}
	}

	if *serverStartRandomPassword {
		// generate very long random one-time password
		b := make([]byte, 32)
		io.ReadFull(rand.Reader, b) //nolint:errcheck

		randomPassword := hex.EncodeToString(b)

		// print it to the stderr bypassing any log file so that the user or calling process can connect
		fmt.Fprintln(os.Stderr, "SERVER PASSWORD:", randomPassword)

		handler = requireAuth{handler, *serverUsername, randomPassword}
	}

	mux := http.NewServeMux()
	mux.Handle("/", handler)

	return mux
}

type requireAuth struct {
	inner            http.Handler
	expectedUsername string
	expectedPassword string
}

func (a requireAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Kopia"`)
		http.Error(w, "Missing credentials.\n", http.StatusUnauthorized)

		return
	}

	valid := subtle.ConstantTimeCompare([]byte(user), []byte(a.expectedUsername)) *
		subtle.ConstantTimeCompare([]byte(pass), []byte(a.expectedPassword))

	if valid != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Kopia"`)
		http.Error(w, "Access denied.\n", http.StatusUnauthorized)

		return
	}

	a.inner.ServeHTTP(w, r)
}
