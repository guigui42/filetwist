package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/guigui42/filetwist/internal/config"
	"github.com/guigui42/filetwist/internal/media"
)

func lookupFrom(values map[string]string) config.LookupFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestLoadRequiresNoEnvironment(t *testing.T) {
	settings, err := config.Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.DataDir != config.DefaultDataDir {
		t.Errorf("DataDir = %q; want %q", settings.DataDir, config.DefaultDataDir)
	}
	if settings.JobTTL != 24*time.Hour {
		t.Errorf("JobTTL = %s; want 24h", settings.JobTTL)
	}
	if settings.MaxConcurrentProcesses < 1 || settings.MaxConcurrentProcesses > 4 {
		t.Errorf("MaxConcurrentProcesses = %d; want a small positive default",
			settings.MaxConcurrentProcesses)
	}
	if settings.MaxFilesPerJob != config.DefaultMaxFilesPerJob {
		t.Errorf("MaxFilesPerJob = %d; want %d", settings.MaxFilesPerJob, config.DefaultMaxFilesPerJob)
	}
	if settings.MaxUploadSize != config.DefaultMaxUploadSize {
		t.Errorf("MaxUploadSize = %d; want %d", settings.MaxUploadSize, config.DefaultMaxUploadSize)
	}
	if settings.MinFreeSpace != config.DefaultMinFreeSpace {
		t.Errorf("MinFreeSpace = %d; want %d", settings.MinFreeSpace, config.DefaultMinFreeSpace)
	}
	if settings.CommandTimeout != config.DefaultCommandTimeout {
		t.Errorf("CommandTimeout = %s; want %s", settings.CommandTimeout, config.DefaultCommandTimeout)
	}
	if settings.Diagnostics != config.DiagnosticsLocal {
		t.Errorf("Diagnostics = %q; want local", settings.Diagnostics)
	}
	if settings.WebRoot != "" {
		t.Errorf("WebRoot = %q; want empty", settings.WebRoot)
	}
	if settings.Acceleration.Mode != media.AccelerationAuto {
		t.Errorf("Acceleration.Mode = %q; want auto", settings.Acceleration.Mode)
	}
	if settings.ReadStallTimeout != config.DefaultReadStallTimeout {
		t.Errorf("ReadStallTimeout = %s; want %s",
			settings.ReadStallTimeout, config.DefaultReadStallTimeout)
	}
	if settings.WriteStallTimeout != config.DefaultWriteStallTimeout {
		t.Errorf("WriteStallTimeout = %s; want %s",
			settings.WriteStallTimeout, config.DefaultWriteStallTimeout)
	}
	if settings.MinUploadRate != config.DefaultMinUploadRate {
		t.Errorf("MinUploadRate = %d; want %d", settings.MinUploadRate, config.DefaultMinUploadRate)
	}
	if len(settings.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v; want no trusted peer by default", settings.TrustedProxies)
	}
	if settings.HealthcheckURL != "" {
		t.Errorf("HealthcheckURL = %q; want empty", settings.HealthcheckURL)
	}
	url, err := settings.HealthURL()
	if err != nil {
		t.Fatalf("HealthURL: %v", err)
	}
	if url != "http://127.0.0.1:8080/healthz" {
		t.Errorf("HealthURL = %q", url)
	}
}

func TestLoadAcceptsExplicitValues(t *testing.T) {
	settings, err := config.Load(lookupFrom(map[string]string{
		config.DataDirEnv:                "/srv/filetwist/data/",
		config.JobTTLEnv:                 "90m",
		config.MaxConcurrentProcessesEnv: "3",
		config.MaxFilesPerJobEnv:         "7",
		config.MaxUploadSizeEnv:          "512MiB",
		config.MinFreeSpaceEnv:           "1GB",
		config.CommandTimeoutEnv:         "45s",
		config.CleanupIntervalEnv:        "2m",
		config.ListenAddressEnv:          "127.0.0.1:9090",
		config.WebRootEnv:                "convert/",
		config.DiagnosticsEnv:            "Disabled",
		media.AccelerationEnv:            "cpu",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.DataDir != "/srv/filetwist/data" {
		t.Errorf("DataDir = %q", settings.DataDir)
	}
	if settings.JobTTL != 90*time.Minute {
		t.Errorf("JobTTL = %s", settings.JobTTL)
	}
	if settings.MaxConcurrentProcesses != 3 || settings.MaxFilesPerJob != 7 {
		t.Errorf("limits = %d, %d", settings.MaxConcurrentProcesses, settings.MaxFilesPerJob)
	}
	if settings.MaxUploadSize != 512<<20 {
		t.Errorf("MaxUploadSize = %d", settings.MaxUploadSize)
	}
	if settings.MinFreeSpace != 1_000_000_000 {
		t.Errorf("MinFreeSpace = %d", settings.MinFreeSpace)
	}
	if settings.CommandTimeout != 45*time.Second || settings.CleanupInterval != 2*time.Minute {
		t.Errorf("timings = %s, %s", settings.CommandTimeout, settings.CleanupInterval)
	}
	if settings.ListenAddress != "127.0.0.1:9090" {
		t.Errorf("ListenAddress = %q", settings.ListenAddress)
	}
	if settings.WebRoot != "/convert" {
		t.Errorf("WebRoot = %q; want /convert", settings.WebRoot)
	}
	if settings.Diagnostics != config.DiagnosticsDisabled {
		t.Errorf("Diagnostics = %q", settings.Diagnostics)
	}
	if settings.Acceleration.Mode != media.AccelerationCPU {
		t.Errorf("Acceleration.Mode = %q", settings.Acceleration.Mode)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "empty data dir", values: map[string]string{config.DataDirEnv: ""}},
		{name: "relative data dir", values: map[string]string{config.DataDirEnv: "data"}},
		{name: "root data dir", values: map[string]string{config.DataDirEnv: "/"}},
		{name: "empty ttl", values: map[string]string{config.JobTTLEnv: ""}},
		{name: "malformed ttl", values: map[string]string{config.JobTTLEnv: "24"}},
		{name: "negative ttl", values: map[string]string{config.JobTTLEnv: "-1h"}},
		{name: "zero processes", values: map[string]string{config.MaxConcurrentProcessesEnv: "0"}},
		{name: "huge processes", values: map[string]string{config.MaxConcurrentProcessesEnv: "5000"}},
		{name: "word processes", values: map[string]string{config.MaxConcurrentProcessesEnv: "many"}},
		{name: "zero files", values: map[string]string{config.MaxFilesPerJobEnv: "0"}},
		{name: "empty upload size", values: map[string]string{config.MaxUploadSizeEnv: ""}},
		{name: "negative upload size", values: map[string]string{config.MaxUploadSizeEnv: "-1"}},
		{name: "NaN upload size", values: map[string]string{config.MaxUploadSizeEnv: "NaN"}},
		{name: "NaN minimum rate", values: map[string]string{config.MinUploadRateEnv: "NaN"}},
		{name: "fractional byte limit", values: map[string]string{config.MaxUploadSizeEnv: "0.5B"}},
		{name: "unit only", values: map[string]string{config.MaxUploadSizeEnv: "MiB"}},
		{name: "bad free space", values: map[string]string{config.MinFreeSpaceEnv: "lots"}},
		{name: "zero command timeout", values: map[string]string{config.CommandTimeoutEnv: "0s"}},
		{name: "portless listen", values: map[string]string{config.ListenAddressEnv: "localhost"}},
		{name: "traversal webroot", values: map[string]string{config.WebRootEnv: "/a/../../b"}},
		{name: "punctuated webroot", values: map[string]string{config.WebRootEnv: "/a b"}},
		{name: "bad diagnostics", values: map[string]string{config.DiagnosticsEnv: "public"}},
		{name: "bad acceleration", values: map[string]string{media.AccelerationEnv: "cuda"}},
		{name: "named listen port", values: map[string]string{config.ListenAddressEnv: ":http"}},
		{name: "negative listen port", values: map[string]string{config.ListenAddressEnv: ":-1"}},
		{name: "overflow listen port", values: map[string]string{config.ListenAddressEnv: ":65536"}},
		{name: "ephemeral listen port", values: map[string]string{config.ListenAddressEnv: ":0"}},
		{name: "empty read stall", values: map[string]string{config.ReadStallTimeoutEnv: ""}},
		{name: "negative read stall", values: map[string]string{config.ReadStallTimeoutEnv: "-5s"}},
		{name: "word write stall", values: map[string]string{config.WriteStallTimeoutEnv: "soon"}},
		{name: "empty min rate", values: map[string]string{config.MinUploadRateEnv: ""}},
		{name: "word min rate", values: map[string]string{config.MinUploadRateEnv: "slow"}},
		{name: "empty proxies", values: map[string]string{config.TrustedProxiesEnv: ""}},
		{name: "host proxy", values: map[string]string{config.TrustedProxiesEnv: "proxy.internal"}},
		{name: "bad proxy cidr", values: map[string]string{config.TrustedProxiesEnv: "10.0.0.0/64"}},
		{name: "empty health url", values: map[string]string{config.HealthcheckURLEnv: ""}},
		{name: "relative health url", values: map[string]string{config.HealthcheckURLEnv: "/healthz"}},
		{name: "ftp health url", values: map[string]string{
			config.HealthcheckURLEnv: "ftp://127.0.0.1/healthz",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.Load(lookupFrom(tt.values)); err == nil {
				t.Fatalf("Load(%v) = nil; want rejection", tt.values)
			}
		})
	}
}

func TestLoadRejectsNilLookup(t *testing.T) {
	if _, err := config.Load(nil); err == nil {
		t.Fatal("Load(nil) = nil; want rejection")
	}
}

func TestParseBytes(t *testing.T) {
	tests := map[string]int64{
		"512":     512,
		"512B":    512,
		"1k":      1024,
		"2KiB":    2048,
		"1MB":     1_000_000,
		"1MiB":    1 << 20,
		"1.5GiB":  1610612736,
		" 4 GiB ": 4 << 30,
	}
	for input, want := range tests {
		got, err := config.ParseBytes(input)
		if err != nil {
			t.Errorf("ParseBytes(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ParseBytes(%q) = %d; want %d", input, got, want)
		}
	}
	for _, input := range []string{"", "0", "-5", "abc", "GiB", "1XB", "NaN", "NaNKiB", "+Inf", "0.5B"} {
		if _, err := config.ParseBytes(input); err == nil {
			t.Errorf("ParseBytes(%q) = nil; want rejection", input)
		}
	}
}

func TestNormalizeWebRoot(t *testing.T) {
	tests := map[string]string{
		"":         "",
		"/":        "",
		"convert":  "/convert",
		"/convert": "/convert",
		"/a/b/":    "/a/b",
	}
	for input, want := range tests {
		got, err := config.NormalizeWebRoot(input)
		if err != nil {
			t.Errorf("NormalizeWebRoot(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeWebRoot(%q) = %q; want %q", input, got, want)
		}
	}
	if _, err := config.NormalizeWebRoot("/a?b"); err == nil {
		t.Error("NormalizeWebRoot accepted a query character")
	}
	for _, input := range []string{"//example.com", `/\example.com`} {
		if _, err := config.NormalizeWebRoot(input); err == nil {
			t.Errorf("NormalizeWebRoot(%q) accepted an absolute redirect prefix", input)
		}
	}
}

func TestLoadErrorsNameTheVariable(t *testing.T) {
	_, err := config.Load(lookupFrom(map[string]string{config.MaxUploadSizeEnv: "huge"}))
	if err == nil {
		t.Fatal("Load = nil; want rejection")
	}
	if !strings.Contains(err.Error(), config.MaxUploadSizeEnv) {
		t.Fatalf("error = %q; want it to name %s", err, config.MaxUploadSizeEnv)
	}
}

func TestLoadStallAndProxySettings(t *testing.T) {
	settings, err := config.Load(lookupFrom(map[string]string{
		config.ReadStallTimeoutEnv:  "20s",
		config.WriteStallTimeoutEnv: "off",
		config.MinUploadRateEnv:     "64KiB",
		config.TrustedProxiesEnv:    "loopback, 10.1.0.0/16 192.0.2.9",
		config.HealthcheckURLEnv:    "http://127.0.0.1:9443/convert/healthz",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.ReadStallTimeout != 20*time.Second {
		t.Errorf("ReadStallTimeout = %s", settings.ReadStallTimeout)
	}
	if settings.WriteStallTimeout != 0 {
		t.Errorf("WriteStallTimeout = %s; want disabled", settings.WriteStallTimeout)
	}
	if settings.MinUploadRate != 64<<10 {
		t.Errorf("MinUploadRate = %d", settings.MinUploadRate)
	}
	want := []string{"127.0.0.0/8", "::1/128", "10.1.0.0/16", "192.0.2.9/32"}
	if len(settings.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v; want %v", settings.TrustedProxies, want)
	}
	for index, prefix := range settings.TrustedProxies {
		if prefix.String() != want[index] {
			t.Errorf("TrustedProxies[%d] = %s; want %s", index, prefix, want[index])
		}
	}
	url, err := settings.HealthURL()
	if err != nil {
		t.Fatalf("HealthURL: %v", err)
	}
	if url != "http://127.0.0.1:9443/convert/healthz" {
		t.Errorf("HealthURL = %q; want the explicit override", url)
	}
}

func TestMinUploadRateCanBeDisabled(t *testing.T) {
	for _, value := range []string{"off", "0", "none"} {
		settings, err := config.Load(lookupFrom(map[string]string{config.MinUploadRateEnv: value}))
		if err != nil {
			t.Fatalf("Load(%q): %v", value, err)
		}
		if settings.MinUploadRate != 0 {
			t.Errorf("MinUploadRate(%q) = %d; want 0", value, settings.MinUploadRate)
		}
	}
}

func TestHealthURLFollowsListenAddress(t *testing.T) {
	tests := []struct {
		listen string
		want   string
	}{
		{listen: ":8080", want: "http://127.0.0.1:8080/healthz"},
		{listen: ":9443", want: "http://127.0.0.1:9443/healthz"},
		{listen: "0.0.0.0:9443", want: "http://127.0.0.1:9443/healthz"},
		{listen: "127.0.0.1:9090", want: "http://127.0.0.1:9090/healthz"},
		{listen: "192.0.2.10:8080", want: "http://192.0.2.10:8080/healthz"},
		{listen: "[::]:8080", want: "http://[::1]:8080/healthz"},
		{listen: "[::1]:8081", want: "http://[::1]:8081/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			settings, err := config.Load(lookupFrom(map[string]string{
				config.ListenAddressEnv: tt.listen,
			}))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			url, err := settings.HealthURL()
			if err != nil {
				t.Fatalf("HealthURL: %v", err)
			}
			if url != tt.want {
				t.Errorf("HealthURL = %q; want %q", url, tt.want)
			}
		})
	}
}
