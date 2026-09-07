package web

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/jobs"
)

// ToolChecker reports whether a converter executable is present. It exists so
// diagnostics and tests never shell out unexpectedly.
type ToolChecker func(name string) bool

// Options configures an App.
type Options struct {
	// Manager runs job intake, conversion, and cleanup.
	Manager *jobs.Manager
	// Config supplies limits, retention, web root, and diagnostics policy.
	Config config.Config
	// Logger receives request-level warnings.
	Logger *slog.Logger
	// Now returns the current time. Zero selects time.Now.
	Now func() time.Time
	// LookupTool reports converter availability for diagnostics. Zero selects
	// an exec.LookPath-backed checker.
	LookupTool ToolChecker
}

// App is the HTTP application. Use Handler to obtain the configured router.
type App struct {
	manager     *jobs.Manager
	templates   *template.Template
	base        string
	retention   time.Duration
	diagnostics config.Diagnostics
	trusted     []netip.Prefix
	deadlines   deadlinePolicy
	minFree     int64
	accelMode   string
	accelDevice string
	logger      *slog.Logger
	now         func() time.Time
	lookupTool  ToolChecker
}

// NewApp validates options, parses the embedded templates, and returns a ready
// application.
func NewApp(options Options) (*App, error) {
	if options.Manager == nil {
		return nil, errors.New("web: job manager must not be nil")
	}
	base, err := config.NormalizeWebRoot(options.Config.WebRoot)
	if err != nil {
		return nil, err
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	lookupTool := options.LookupTool
	if lookupTool == nil {
		lookupTool = defaultToolChecker
	}
	app := &App{
		manager:     options.Manager,
		base:        base,
		retention:   options.Config.JobTTL,
		diagnostics: options.Config.Diagnostics,
		trusted:     options.Config.TrustedProxies,
		deadlines: deadlinePolicy{
			ReadStall:  options.Config.ReadStallTimeout,
			WriteStall: options.Config.WriteStallTimeout,
			MinRate:    options.Config.MinUploadRate,
		},
		minFree:     options.Config.MinFreeSpace,
		accelMode:   string(options.Config.Acceleration.Mode),
		accelDevice: options.Config.Acceleration.Device,
		logger:      logger,
		now:         nowFunc,
		lookupTool:  lookupTool,
	}
	templates, err := parseTemplates(template.FuncMap{
		"url":    joinURL,
		"asset":  assetURL,
		"plural": plural,
	})
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	app.templates = templates
	return app, nil
}

// Handler returns the router for the whole application, including the WEBROOT
// subpath prefix when one is configured.
func (app *App) Handler() http.Handler {
	originProtection := http.NewCrossOriginProtection()
	originProtection.SetDenyHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		app.renderNotice(writer, http.StatusForbidden, "error",
			"Cross-origin requests are not allowed. Open the application directly and try again.")
	}))
	inner := http.NewServeMux()
	inner.HandleFunc("GET /{$}", app.handleIndex)
	inner.HandleFunc("GET /help", app.handleHelp)
	inner.HandleFunc("POST /jobs", app.handleUpload)
	inner.HandleFunc("GET /jobs/{id}", app.handleJobPage)
	inner.HandleFunc("GET /jobs/{id}/status", app.handleJobStatus)
	inner.HandleFunc("POST /jobs/{id}/start", app.handleStart)
	inner.HandleFunc("POST /jobs/{id}/cancel", app.handleCancel)
	inner.HandleFunc("POST /jobs/{id}/delete", app.handleDelete)
	inner.HandleFunc("POST /jobs/{id}/files/{fileID}/remove", app.handleRemoveFile)
	inner.HandleFunc("GET /jobs/{id}/files/{fileID}", app.handleFileDownload)
	inner.HandleFunc("GET /jobs/{id}/download", app.handleArchiveDownload)
	inner.HandleFunc("GET /healthz", app.handleHealth)
	inner.HandleFunc("HEAD /healthz", app.handleHealth)
	if app.diagnostics != config.DiagnosticsDisabled {
		inner.HandleFunc("GET /diagnostics", app.handleDiagnostics)
	}

	assets, err := StaticFS()
	if err != nil {
		app.logger.Error("static assets are unavailable", slog.String("error", err.Error()))
	} else {
		fileServer := http.FileServer(http.FS(assets))
		inner.Handle("GET /static/", http.StripPrefix("/static/", staticHeaders(fileServer)))
	}

	if app.base == "" {
		return app.deadlines.wrap(originProtection.Handler(inner))
	}
	outer := http.NewServeMux()
	outer.HandleFunc("GET /healthz", app.handleHealth)
	outer.HandleFunc("HEAD /healthz", app.handleHealth)
	outer.Handle(app.base+"/", http.StripPrefix(app.base, inner))
	outer.HandleFunc(app.base, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, app.base+"/", http.StatusMovedPermanently)
	})
	return app.deadlines.wrap(originProtection.Handler(outer))
}

func staticHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(writer, request)
	})
}

// joinURL prefixes an application path with the configured web root.
func joinURL(base, target string) string {
	if target == "" || target[0] == '\\' ||
		(strings.HasPrefix(target, "/") && len(target) > 1 &&
			(target[1] == '/' || target[1] == '\\')) {
		target = "/"
	} else if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	if base == "" {
		return target
	}
	return base + target
}

// assetURL builds a static asset URL under the configured web root.
func assetURL(base, name string) string {
	return joinURL(base, path.Join("/static", name))
}

func defaultToolChecker(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (app *App) htmlHeaders(writer http.ResponseWriter) {
	header := writer.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"form-action 'self'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	}, "; "))
}

// attachmentHeaders configures a response that always downloads user content
// as an opaque attachment.
func attachmentHeaders(writer http.ResponseWriter, contentType, filename string) {
	header := writer.Header()
	header.Set("Content-Type", contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Content-Disposition", contentDisposition(filename))
}

func (app *App) renderPage(writer http.ResponseWriter, status int, data pageData) {
	app.htmlHeaders(writer)
	writer.WriteHeader(status)
	if err := app.templates.ExecuteTemplate(writer, "layout", data); err != nil {
		app.logger.Error("page render failed", slog.String("error", err.Error()))
	}
}

func (app *App) renderFragment(writer http.ResponseWriter, status int, name string, data any) {
	app.htmlHeaders(writer)
	writer.WriteHeader(status)
	if err := app.templates.ExecuteTemplate(writer, name, data); err != nil {
		app.logger.Error("fragment render failed", slog.String("error", err.Error()))
	}
}

func (app *App) renderNotice(writer http.ResponseWriter, status int, class, message string) {
	app.renderFragment(writer, status, "notice", noticeData{Class: class, Message: message})
}

// diagnosticsAllowed reports whether this request may read the diagnostics
// endpoint under the configured policy.
//
// DIAGNOSTICS=local means "the client is on this host", not "the connection is
// loopback". A reverse proxy on the same host makes every remote client arrive
// from 127.0.0.1, so a loopback peer that also carries forwarding headers is
// refused unless the peer is a configured trusted proxy whose forwarded chain
// can be believed.
func (app *App) diagnosticsAllowed(request *http.Request) bool {
	switch app.diagnostics {
	case config.DiagnosticsEnabled:
		return true
	case config.DiagnosticsDisabled:
		return false
	default:
		address, resolved := clientAddress(request, app.trusted)
		return resolved && address.IsLoopback()
	}
}
