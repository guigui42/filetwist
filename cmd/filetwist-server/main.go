package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/conversion"
	"github.com/guigui42/filetwist/internal/jobs"
	"github.com/guigui42/filetwist/internal/jobs/storage"
	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
	"github.com/guigui42/filetwist/internal/web"
)

const (
	// shutdownGrace bounds HTTP shutdown before in-flight work is canceled.
	shutdownGrace = 10 * time.Second
	// drainGrace bounds the conversion worker drain during shutdown.
	drainGrace = 15 * time.Second
	// headerTimeout bounds reading one request header block.
	headerTimeout = 15 * time.Second
	// idleTimeout bounds an idle keep-alive connection.
	idleTimeout = 120 * time.Second
	// healthTimeout bounds one healthcheck probe.
	healthTimeout = 2 * time.Second
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "serve":
		return runServer(ctx, args, stdout, stderr)
	case "healthcheck":
		return runHealthcheck(ctx, args, stdout, stderr, http.DefaultClient)
	case "version":
		_, err := fmt.Fprintf(stdout, "filetwist-server %s\n", version)
		if err != nil {
			return 1
		}
		return 0
	case "help", "-h", "--help":
		writeServerUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "filetwist-server: unknown command %q\n", command)
		writeServerUsage(stderr)
		return 2
	}
}

func runServer(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("filetwist-server serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", "", "HTTP listen address override")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}

	settings, err := config.Load(os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
		return 1
	}
	if *listenAddress != "" {
		settings.ListenAddress = *listenAddress
	}
	if err := checkRuntimeDependencies(); err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: runtime check failed: %v\n", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	application, err := newApplication(settings, logger)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
		return 1
	}

	interrupted, err := application.manager.RecoverInterrupted()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: job recovery failed: %v\n", err)
		return 1
	}
	if len(interrupted) > 0 {
		logger.Info("interrupted jobs marked", slog.Int("count", len(interrupted)))
	}

	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	defer stopCleanup()
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		application.manager.RunCleanup(cleanupCtx, settings.CleanupInterval)
	}()

	server := &http.Server{
		Addr:              settings.ListenAddress,
		Handler:           application.handler,
		ReadHeaderTimeout: headerTimeout,
		IdleTimeout:       idleTimeout,
		// ReadTimeout and WriteTimeout stay unset on purpose. Both are fixed
		// whole-request bounds, so either one would reject a legitimate
		// multi-gigabyte upload on a slow link and cut a long streamed
		// download. The handler applies progress-aware read and write
		// deadlines instead, bounded by READ_STALL_TIMEOUT,
		// WRITE_STALL_TIMEOUT, and MIN_UPLOAD_RATE.
	}
	_, errCh, err := startHTTPServer(
		server,
		stdout,
		fmt.Sprintf(
			"filetwist-server %s listening on %s (data %s, %d concurrent processes)\n",
			version,
			settings.ListenAddress,
			settings.DataDir,
			settings.MaxConcurrentProcesses,
		),
	)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
		stopCleanup()
		<-cleanupDone
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainGrace)
		defer cancelDrain()
		if shutdownErr := application.manager.Shutdown(drainCtx); shutdownErr != nil {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", shutdownErr)
		}
		return 1
	}

	exitCode := 0
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
			exitCode = 1
		}
	case <-ctx.Done():
		application.manager.StopIntake()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		if err := shutdownHTTPServer(shutdownCtx, server); err != nil {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: shutdown failed: %v\n", err)
			exitCode = 1
		}
		cancel()
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
			exitCode = 1
		}
	}

	stopCleanup()
	<-cleanupDone
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainGrace)
	defer cancelDrain()
	if err := application.manager.Shutdown(drainCtx); err != nil {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
		exitCode = 1
	}
	return exitCode
}

func startHTTPServer(
	server *http.Server,
	stdout io.Writer,
	banner string,
) (net.Listener, <-chan error, error) {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, nil, err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	_, _ = io.WriteString(stdout, banner)
	return listener, errCh, nil
}

func shutdownHTTPServer(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		// Canceling a request context cannot unblock a stalled body read.
		// Close its transport before waiting for accepted uploads to drain.
		return errors.Join(err, server.Close())
	}
	return nil
}

// application bundles the HTTP handler with the job manager that backs it.
type application struct {
	handler http.Handler
	manager *jobs.Manager
}

// newApplication builds the conversion service, the job manager, and the web
// handler from validated configuration.
func newApplication(settings config.Config, logger *slog.Logger) (*application, error) {
	processRunner, err := runner.New(runner.Config{
		Timeout:     settings.CommandTimeout,
		StdoutLimit: 256 * 1024,
		StderrLimit: 256 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("process runner: %w", err)
	}
	prober, err := probe.New(processRunner.Run)
	if err != nil {
		return nil, fmt.Errorf("prober: %w", err)
	}
	service, err := conversion.NewLocal(conversion.LocalConfig{
		Run:            processRunner.Run,
		Prober:         prober,
		Acceleration:   settings.Acceleration,
		CommandTimeout: settings.CommandTimeout,
		ProbeTimeout:   30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("conversion service: %w", err)
	}

	store, err := storage.NewStore(settings.DataDir)
	if err != nil {
		return nil, fmt.Errorf("job storage: %w", err)
	}
	manager, err := jobs.NewManager(jobs.Options{
		Store:                  store,
		Converter:              service,
		MaxConcurrentProcesses: settings.MaxConcurrentProcesses,
		MaxFilesPerJob:         settings.MaxFilesPerJob,
		MaxUploadSize:          settings.MaxUploadSize,
		MinFreeSpace:           settings.MinFreeSpace,
		JobTTL:                 settings.JobTTL,
		CommandTimeout:         settings.CommandTimeout,
		Logger:                 logger,
	})
	if err != nil {
		return nil, fmt.Errorf("job manager: %w", err)
	}
	app, err := web.NewApp(web.Options{
		Manager: manager,
		Config:  settings,
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("web application: %w", err)
	}
	return &application{handler: app.Handler(), manager: manager}, nil
}

func runHealthcheck(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	client *http.Client,
) int {
	flags := flag.NewFlagSet("filetwist-server healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	healthURL := flags.String("url", "", "health endpoint URL, default derived from LISTEN_ADDRESS")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}

	target := *healthURL
	if target == "" {
		// The healthcheck reads the same configuration the server binds, so a
		// non-default LISTEN_ADDRESS or an explicit HEALTHCHECK_URL is
		// followed without repeating the port in the container definition.
		settings, err := config.Load(os.LookupEnv)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
			return 1
		}
		if target, err = settings.HealthURL(); err != nil {
			_, _ = fmt.Fprintf(stderr, "filetwist-server: %v\n", err)
			return 1
		}
	}

	requestCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filetwist-server: invalid health URL")
		return 2
	}
	response, err := client.Do(request)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filetwist-server: health request failed")
		return 1
	}
	if err := response.Body.Close(); err != nil {
		_, _ = fmt.Fprintln(stderr, "filetwist-server: health response could not be closed")
		return 1
	}
	if response.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "filetwist-server: unhealthy HTTP status %d\n", response.StatusCode)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "healthy")
	return 0
}

func checkRuntimeDependencies() error {
	for _, command := range []string{"filetwist", "ffmpeg", "ffprobe", "vips", "vipsheader"} {
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
	}
	return nil
}

func writeServerUsage(writer io.Writer) {
	lines := []string{
		"usage: filetwist-server [serve|healthcheck|version]",
		"  serve [--listen ADDRESS]",
		"  healthcheck [--url URL]",
		"",
		"Configuration is read from the environment; every value has a default.",
		"  DATA_DIR MAX_CONCURRENT_PROCESSES MAX_FILES_PER_JOB MAX_UPLOAD_SIZE",
		"  MIN_FREE_SPACE JOB_TTL COMMAND_TIMEOUT CLEANUP_INTERVAL",
		"  LISTEN_ADDRESS WEBROOT DIAGNOSTICS ACCELERATION VAAPI_DEVICE",
		"  READ_STALL_TIMEOUT WRITE_STALL_TIMEOUT MIN_UPLOAD_RATE",
		"  TRUSTED_PROXIES HEALTHCHECK_URL",
	}
	_, _ = fmt.Fprintln(writer, strings.Join(lines, "\n"))
}
