// Package config loads and strictly validates the filetwist web service
// environment. Every setting has a working default so the service starts with
// no environment variables at all, and every supplied value is validated.
package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/guigui42/filetwist/internal/media"
)

// Environment variable names read by Load.
const (
	// DataDirEnv selects the persistent job root directory.
	DataDirEnv = "DATA_DIR"
	// JobTTLEnv selects how long a job survives before cleanup removes it.
	JobTTLEnv = "JOB_TTL"
	// MaxConcurrentProcessesEnv bounds simultaneous converter processes.
	MaxConcurrentProcessesEnv = "MAX_CONCURRENT_PROCESSES"
	// MaxFilesPerJobEnv bounds the number of uploaded files in one job.
	MaxFilesPerJobEnv = "MAX_FILES_PER_JOB"
	// MaxUploadSizeEnv bounds the aggregate uploaded bytes in one job.
	MaxUploadSizeEnv = "MAX_UPLOAD_SIZE"
	// MinFreeSpaceEnv is the free-space floor enforced before accepting bytes.
	MinFreeSpaceEnv = "MIN_FREE_SPACE"
	// CommandTimeoutEnv bounds one converter process.
	CommandTimeoutEnv = "COMMAND_TIMEOUT"
	// ListenAddressEnv selects the HTTP listen address.
	ListenAddressEnv = "LISTEN_ADDRESS"
	// WebRootEnv mounts the application under a URL subpath.
	WebRootEnv = "WEBROOT"
	// DiagnosticsEnv selects the diagnostics endpoint exposure policy.
	DiagnosticsEnv = "DIAGNOSTICS"
	// CleanupIntervalEnv selects how often expired jobs are swept.
	CleanupIntervalEnv = "CLEANUP_INTERVAL"
	// ReadStallTimeoutEnv bounds how long a request body may deliver nothing.
	ReadStallTimeoutEnv = "READ_STALL_TIMEOUT"
	// WriteStallTimeoutEnv bounds how long a response may accept nothing.
	WriteStallTimeoutEnv = "WRITE_STALL_TIMEOUT"
	// MinUploadRateEnv is the average bytes-per-second floor for a request
	// body once it has been open longer than READ_STALL_TIMEOUT.
	MinUploadRateEnv = "MIN_UPLOAD_RATE"
	// TrustedProxiesEnv lists the peers whose forwarded client-address headers
	// are honored.
	TrustedProxiesEnv = "TRUSTED_PROXIES"
	// HealthcheckURLEnv overrides the URL the healthcheck subcommand probes.
	HealthcheckURLEnv = "HEALTHCHECK_URL"
)

// Defaults applied when an environment variable is unset.
const (
	// DefaultDataDir is the container job root.
	DefaultDataDir = "/data"
	// DefaultJobTTL is the retention window for a job.
	DefaultJobTTL = 24 * time.Hour
	// DefaultMaxFilesPerJob is the per-job file-count ceiling.
	DefaultMaxFilesPerJob = 25
	// DefaultMaxUploadSize is the aggregate per-job byte ceiling.
	DefaultMaxUploadSize int64 = 4 << 30
	// DefaultMinFreeSpace is the free-space floor for the data directory.
	DefaultMinFreeSpace int64 = 2 << 30
	// DefaultCommandTimeout bounds one converter process.
	DefaultCommandTimeout = 10 * time.Minute
	// DefaultListenAddress is the HTTP listen address.
	DefaultListenAddress = ":8080"
	// DefaultCleanupInterval is the expired-job sweep period.
	DefaultCleanupInterval = 5 * time.Minute
	// DefaultReadStallTimeout is the idle window granted to a request body.
	// The window restarts every time the body delivers bytes, so a slow but
	// progressing upload is never cut while a stalled one is.
	DefaultReadStallTimeout = 60 * time.Second
	// DefaultWriteStallTimeout is the idle window granted to a response that
	// has started writing. It restarts on every accepted write, so a long
	// streamed download survives while a client that stops reading does not.
	DefaultWriteStallTimeout = 60 * time.Second
	// DefaultMinUploadRate is the average bytes-per-second floor a request
	// body must sustain once it outlives the read stall window.
	DefaultMinUploadRate int64 = 4 << 10
)

// Diagnostics selects who may read the diagnostics endpoint.
type Diagnostics string

const (
	// DiagnosticsLocal serves diagnostics only to loopback clients. It is the
	// default.
	DiagnosticsLocal Diagnostics = "local"
	// DiagnosticsDisabled removes the diagnostics route entirely.
	DiagnosticsDisabled Diagnostics = "disabled"
	// DiagnosticsEnabled serves diagnostics to every client.
	DiagnosticsEnabled Diagnostics = "enabled"
)

// Config is the validated web service configuration.
type Config struct {
	// DataDir is the absolute persistent job root.
	DataDir string
	// JobTTL is the job retention window.
	JobTTL time.Duration
	// MaxConcurrentProcesses bounds simultaneous converter processes.
	MaxConcurrentProcesses int
	// MaxFilesPerJob bounds uploaded files in one job.
	MaxFilesPerJob int
	// MaxUploadSize bounds aggregate uploaded bytes in one job.
	MaxUploadSize int64
	// MinFreeSpace is the data-directory free-space floor.
	MinFreeSpace int64
	// CommandTimeout bounds one converter process.
	CommandTimeout time.Duration
	// ListenAddress is the HTTP listen address.
	ListenAddress string
	// WebRoot is the normalized URL subpath prefix, empty or "/prefix".
	WebRoot string
	// Diagnostics is the diagnostics exposure policy.
	Diagnostics Diagnostics
	// CleanupInterval is the expired-job sweep period.
	CleanupInterval time.Duration
	// ReadStallTimeout is the idle window a request body may spend without
	// delivering bytes. Zero disables the read deadline.
	ReadStallTimeout time.Duration
	// WriteStallTimeout is the idle window a started response may spend
	// without accepting bytes. Zero disables the write deadline.
	WriteStallTimeout time.Duration
	// MinUploadRate is the average bytes-per-second floor enforced on a
	// request body that outlives ReadStallTimeout. Zero disables the floor.
	MinUploadRate int64
	// TrustedProxies lists the peer networks whose forwarded client-address
	// headers are honored. Empty means no forwarded header is ever trusted.
	TrustedProxies []netip.Prefix
	// HealthcheckURL overrides the URL the healthcheck subcommand probes.
	// Empty means it is derived from ListenAddress.
	HealthcheckURL string
	// Acceleration is the validated video acceleration policy.
	Acceleration media.AccelerationConfig
}

// LookupFunc reads one environment variable.
type LookupFunc func(string) (string, bool)

// DefaultMaxConcurrentProcesses returns the process ceiling used when
// MAX_CONCURRENT_PROCESSES is unset. Media conversion is CPU bound, so the
// default stays small and never exceeds four.
func DefaultMaxConcurrentProcesses() int {
	cpus := runtime.NumCPU()
	switch {
	case cpus <= 1:
		return 1
	case cpus >= 4:
		return 4
	default:
		return cpus
	}
}

// Load reads and strictly validates the configuration through lookup. An unset
// variable uses its default; a supplied variable that is empty or malformed is
// an error.
func Load(lookup LookupFunc) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("config: environment lookup must not be nil")
	}

	config := Config{
		DataDir:                DefaultDataDir,
		JobTTL:                 DefaultJobTTL,
		MaxConcurrentProcesses: DefaultMaxConcurrentProcesses(),
		MaxFilesPerJob:         DefaultMaxFilesPerJob,
		MaxUploadSize:          DefaultMaxUploadSize,
		MinFreeSpace:           DefaultMinFreeSpace,
		CommandTimeout:         DefaultCommandTimeout,
		ListenAddress:          DefaultListenAddress,
		Diagnostics:            DiagnosticsLocal,
		CleanupInterval:        DefaultCleanupInterval,
		ReadStallTimeout:       DefaultReadStallTimeout,
		WriteStallTimeout:      DefaultWriteStallTimeout,
		MinUploadRate:          DefaultMinUploadRate,
	}

	var err error
	if config.DataDir, err = loadDataDir(lookup); err != nil {
		return Config{}, err
	}
	if config.JobTTL, err = loadDuration(lookup, JobTTLEnv, config.JobTTL); err != nil {
		return Config{}, err
	}
	if config.CommandTimeout, err = loadDuration(
		lookup,
		CommandTimeoutEnv,
		config.CommandTimeout,
	); err != nil {
		return Config{}, err
	}
	if config.CleanupInterval, err = loadDuration(
		lookup,
		CleanupIntervalEnv,
		config.CleanupInterval,
	); err != nil {
		return Config{}, err
	}
	if config.MaxConcurrentProcesses, err = loadCount(
		lookup,
		MaxConcurrentProcessesEnv,
		config.MaxConcurrentProcesses,
		1024,
	); err != nil {
		return Config{}, err
	}
	if config.MaxFilesPerJob, err = loadCount(
		lookup,
		MaxFilesPerJobEnv,
		config.MaxFilesPerJob,
		10000,
	); err != nil {
		return Config{}, err
	}
	if config.MaxUploadSize, err = loadBytes(lookup, MaxUploadSizeEnv, config.MaxUploadSize); err != nil {
		return Config{}, err
	}
	if config.MinFreeSpace, err = loadBytes(lookup, MinFreeSpaceEnv, config.MinFreeSpace); err != nil {
		return Config{}, err
	}
	if config.ListenAddress, err = loadListenAddress(lookup, config.ListenAddress); err != nil {
		return Config{}, err
	}
	if config.WebRoot, err = loadWebRoot(lookup); err != nil {
		return Config{}, err
	}
	if config.Diagnostics, err = loadDiagnostics(lookup, config.Diagnostics); err != nil {
		return Config{}, err
	}
	if config.ReadStallTimeout, err = loadStallTimeout(
		lookup,
		ReadStallTimeoutEnv,
		config.ReadStallTimeout,
	); err != nil {
		return Config{}, err
	}
	if config.WriteStallTimeout, err = loadStallTimeout(
		lookup,
		WriteStallTimeoutEnv,
		config.WriteStallTimeout,
	); err != nil {
		return Config{}, err
	}
	if config.MinUploadRate, err = loadRate(lookup, MinUploadRateEnv, config.MinUploadRate); err != nil {
		return Config{}, err
	}
	if config.TrustedProxies, err = loadTrustedProxies(lookup); err != nil {
		return Config{}, err
	}
	if config.HealthcheckURL, err = loadHealthcheckURL(lookup); err != nil {
		return Config{}, err
	}
	if config.Acceleration, err = media.LoadAccelerationConfig(lookup); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return config, nil
}

// loadStallTimeout reads an idle-window duration. The literal "off" disables
// the deadline; every other value must be a positive Go duration.
func loadStallTimeout(
	lookup LookupFunc,
	name string,
	fallback time.Duration,
) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	if isDisabled(value) {
		return 0, nil
	}
	return loadDuration(lookup, name, fallback)
}

// loadRate reads a bytes-per-second floor. The literal "off" or "0" disables
// the floor.
func loadRate(lookup LookupFunc, name string, fallback int64) (int64, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	if isDisabled(value) {
		return 0, nil
	}
	return loadBytes(lookup, name, fallback)
}

// isDisabled reports whether a value explicitly turns a setting off. An empty
// value is never disabling, so a blank variable stays a configuration error.
func isDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "none", "0":
		return true
	default:
		return false
	}
}

// loadTrustedProxies parses the peer networks whose forwarded client-address
// headers are honored. The default is empty, so no forwarded header is
// trusted until an operator names the proxy.
func loadTrustedProxies(lookup LookupFunc) ([]netip.Prefix, error) {
	value, ok := lookup(TrustedProxiesEnv)
	if !ok {
		return nil, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("config: %s must not be empty", TrustedProxiesEnv)
	}
	if isDisabled(trimmed) {
		return nil, nil
	}

	var prefixes []netip.Prefix
	for _, field := range strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if strings.EqualFold(field, "loopback") {
			prefixes = append(
				prefixes,
				netip.MustParsePrefix("127.0.0.0/8"),
				netip.MustParsePrefix("::1/128"),
			)
			continue
		}
		parsed, err := parseProxyPrefix(field)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, parsed)
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("config: %s must list at least one address or CIDR", TrustedProxiesEnv)
	}
	return prefixes, nil
}

func parseProxyPrefix(field string) (netip.Prefix, error) {
	if strings.Contains(field, "/") {
		prefix, err := netip.ParsePrefix(field)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf(
				"config: %s entry %q is not an address or CIDR: %w",
				TrustedProxiesEnv, field, err,
			)
		}
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(field)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf(
			"config: %s entry %q is not an address or CIDR: %w",
			TrustedProxiesEnv, field, err,
		)
	}
	return netip.PrefixFrom(address.Unmap(), address.Unmap().BitLen()), nil
}

// loadHealthcheckURL validates an explicit health endpoint override.
func loadHealthcheckURL(lookup LookupFunc) (string, error) {
	value, ok := lookup(HealthcheckURLEnv)
	if !ok {
		return "", nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("config: %s must not be empty", HealthcheckURLEnv)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("config: %s is not a URL: %w", HealthcheckURLEnv, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("config: %s must use http or https", HealthcheckURLEnv)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("config: %s must include a host", HealthcheckURLEnv)
	}
	return trimmed, nil
}

// HealthURL returns the URL the healthcheck subcommand probes. An explicit
// HEALTHCHECK_URL wins; otherwise the URL is derived from the same listen
// address the server binds, so a non-default port stays consistent.
func (config Config) HealthURL() (string, error) {
	if config.HealthcheckURL != "" {
		return config.HealthcheckURL, nil
	}
	host, port, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return "", fmt.Errorf("config: %s is not host:port: %w", ListenAddressEnv, err)
	}
	if port == "" {
		return "", fmt.Errorf("config: %s must include a port such as :8080", ListenAddressEnv)
	}
	return "http://" + net.JoinHostPort(healthHost(host), port) + "/healthz", nil
}

// healthHost maps a bind host onto an address the local healthcheck can dial.
// A wildcard bind is probed over loopback; any other host is probed as bound.
func healthHost(host string) string {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if trimmed == "" {
		return "127.0.0.1"
	}
	address, err := netip.ParseAddr(trimmed)
	if err != nil {
		return trimmed
	}
	if address.IsUnspecified() {
		if address.Is4() {
			return "127.0.0.1"
		}
		return "::1"
	}
	return trimmed
}

func loadDataDir(lookup LookupFunc) (string, error) {
	value, ok := lookup(DataDirEnv)
	if !ok {
		return DefaultDataDir, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("config: %s must not be empty", DataDirEnv)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("config: %s must be an absolute path", DataDirEnv)
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == string(filepath.Separator) {
		return "", fmt.Errorf("config: %s must not be the filesystem root", DataDirEnv)
	}
	return cleaned, nil
}

func loadDuration(lookup LookupFunc, name string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("config: %s must not be empty", name)
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a Go duration such as 24h: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("config: %s must be positive", name)
	}
	return parsed, nil
}

func loadCount(lookup LookupFunc, name string, fallback, maximum int) (int, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("config: %s must not be empty", name)
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a positive integer: %w", name, err)
	}
	if parsed < 1 {
		return 0, fmt.Errorf("config: %s must be at least 1", name)
	}
	if parsed > maximum {
		return 0, fmt.Errorf("config: %s must not exceed %d", name, maximum)
	}
	return parsed, nil
}

func loadBytes(lookup LookupFunc, name string, fallback int64) (int64, error) {
	value, ok := lookup(name)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("config: %s must not be empty", name)
	}
	parsed, err := ParseBytes(trimmed)
	if err != nil {
		return 0, fmt.Errorf("config: %s is invalid: %w", name, err)
	}
	return parsed, nil
}

// ParseBytes converts a byte size such as "512", "512MB", "2GiB", or "1.5GB"
// into bytes. Suffixes are case insensitive; SI suffixes use powers of 1000 and
// IEC suffixes use powers of 1024.
func ParseBytes(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("size must not be empty")
	}

	units := []struct {
		suffix     string
		multiplier float64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40},
		{"kb", 1e3}, {"mb", 1e6}, {"gb", 1e9}, {"tb", 1e12},
		{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"t", 1 << 40},
		{"b", 1},
	}

	lowered := strings.ToLower(trimmed)
	multiplier := float64(1)
	number := lowered
	for _, unit := range units {
		if strings.HasSuffix(lowered, unit.suffix) {
			multiplier = unit.multiplier
			number = strings.TrimSpace(strings.TrimSuffix(lowered, unit.suffix))
			break
		}
	}
	if number == "" {
		return 0, errors.New("size must contain a number")
	}
	parsed, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q is not a number with an optional unit", value)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed <= 0 {
		return 0, errors.New("size must be positive")
	}
	total := parsed * multiplier
	if total < 1 {
		return 0, errors.New("size must be at least one byte")
	}
	if total > float64(1<<62) {
		return 0, errors.New("size is too large")
	}
	return int64(total), nil
}

func loadListenAddress(lookup LookupFunc, fallback string) (string, error) {
	value, ok := lookup(ListenAddressEnv)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("config: %s must not be empty", ListenAddressEnv)
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil || port == "" {
		return "", fmt.Errorf("config: %s must include a port such as :8080", ListenAddressEnv)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("config: %s must use a numeric port between 1 and 65535", ListenAddressEnv)
	}
	return trimmed, nil
}

func loadWebRoot(lookup LookupFunc) (string, error) {
	value, ok := lookup(WebRootEnv)
	if !ok {
		return "", nil
	}
	return NormalizeWebRoot(value)
}

// NormalizeWebRoot validates a URL subpath prefix and returns either an empty
// string or a clean "/prefix" value with no trailing slash.
func NormalizeWebRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "/" {
		return "", nil
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("config: %s must not contain parent directory segments", WebRootEnv)
		}
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "/" || cleaned == "." {
		return "", nil
	}
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("config: %s must not contain parent directory segments", WebRootEnv)
	}
	for _, character := range cleaned {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '/', character == '-', character == '_':
		default:
			return "", fmt.Errorf(
				"config: %s may only contain letters, digits, -, _, and /",
				WebRootEnv,
			)
		}
	}
	return cleaned, nil
}

func loadDiagnostics(lookup LookupFunc, fallback Diagnostics) (Diagnostics, error) {
	value, ok := lookup(DiagnosticsEnv)
	if !ok {
		return fallback, nil
	}
	switch Diagnostics(strings.ToLower(strings.TrimSpace(value))) {
	case DiagnosticsLocal:
		return DiagnosticsLocal, nil
	case DiagnosticsDisabled:
		return DiagnosticsDisabled, nil
	case DiagnosticsEnabled:
		return DiagnosticsEnabled, nil
	default:
		return "", fmt.Errorf(
			"config: %s must be local, disabled, or enabled",
			DiagnosticsEnv,
		)
	}
}
