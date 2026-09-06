package media_test

import (
	"testing"

	"github.com/guigui42/filetwist/internal/media"
)

func TestLoadAccelerationConfig(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		want        media.AccelerationConfig
		wantError   bool
	}{
		{
			name: "defaults",
			want: media.AccelerationConfig{
				Mode:   media.AccelerationAuto,
				Device: media.DefaultVAAPIDevice,
			},
		},
		{
			name:        "explicit CPU",
			environment: map[string]string{media.AccelerationEnv: "cpu"},
			want: media.AccelerationConfig{
				Mode:   media.AccelerationCPU,
				Device: media.DefaultVAAPIDevice,
			},
		},
		{
			name: "explicit VA-API device",
			environment: map[string]string{
				media.AccelerationEnv: "vaapi",
				media.VAAPIDeviceEnv:  "/dev/dri/renderD129",
			},
			want: media.AccelerationConfig{
				Mode:   media.AccelerationVAAPI,
				Device: "/dev/dri/renderD129",
			},
		},
		{
			name:        "empty mode",
			environment: map[string]string{media.AccelerationEnv: ""},
			wantError:   true,
		},
		{
			name:        "unknown mode",
			environment: map[string]string{media.AccelerationEnv: "gpu"},
			wantError:   true,
		},
		{
			name:        "mode whitespace",
			environment: map[string]string{media.AccelerationEnv: " auto"},
			wantError:   true,
		},
		{
			name:        "empty device",
			environment: map[string]string{media.VAAPIDeviceEnv: ""},
			wantError:   true,
		},
		{
			name:        "relative device",
			environment: map[string]string{media.VAAPIDeviceEnv: "dev/dri/renderD128"},
			wantError:   true,
		},
		{
			name:        "unclean device",
			environment: map[string]string{media.VAAPIDeviceEnv: "/dev/dri/../dri/renderD128"},
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := media.LoadAccelerationConfig(func(key string) (string, bool) {
				value, ok := tt.environment[key]
				return value, ok
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("LoadAccelerationConfig() error = %v; wantError %t", err, tt.wantError)
			}
			if config != tt.want {
				t.Errorf("LoadAccelerationConfig() = %+v; want %+v", config, tt.want)
			}
		})
	}
}
