package media_test

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/guigui42/filetwist/internal/corpus"
	"github.com/guigui42/filetwist/internal/media"
)

func TestBuildPlanSelectsAccelerationPath(t *testing.T) {
	h264Probe := media.Probe{
		Streams: []media.Stream{{
			Index:       3,
			CodecName:   "h264",
			CodecType:   "video",
			Width:       1280,
			Height:      720,
			PixelFormat: "yuv420p",
		}},
		Format: media.Format{DurationText: "1.000000"},
	}
	rotatedHEVC := readProbeFixture(t, "iphone-spatial.json")
	ready := media.VAAPIReadiness{
		Device:                  media.DefaultVAAPIDevice,
		Ready:                   true,
		FunctionalProbeComplete: true,
		HardwareDecodeCodecs: map[string]bool{
			"h264": true,
			"hevc": true,
		},
	}

	tests := []struct {
		name         string
		mode         media.AccelerationMode
		probe        media.Probe
		readiness    *media.VAAPIReadiness
		wantPath     media.ExecutionPath
		wantFallback media.FallbackReason
		wantFilter   string
		wantHWDecode bool
		wantSDRColor bool
		wantError    media.ErrorCode
	}{
		{
			name:       "CPU explicitly disabled",
			mode:       media.AccelerationCPU,
			probe:      h264Probe,
			readiness:  &ready,
			wantPath:   media.ExecutionPathCPU,
			wantFilter: "scale=1280:720",
		},
		{
			name:         "automatic CPU when probe unavailable",
			mode:         media.AccelerationAuto,
			probe:        h264Probe,
			wantPath:     media.ExecutionPathCPU,
			wantFallback: media.FallbackVAAPIProbeUnavailable,
			wantFilter:   "scale=1280:720",
		},
		{
			name:      "required VA-API rejects unavailable probe",
			mode:      media.AccelerationVAAPI,
			probe:     h264Probe,
			wantError: media.ErrorAccelerationUnavailable,
		},
		{
			name:         "hardware decode for reported H.264 support",
			mode:         media.AccelerationAuto,
			probe:        h264Probe,
			readiness:    &ready,
			wantPath:     media.ExecutionPathVAAPIHardwareDecode,
			wantFilter:   "scale_vaapi=w=1280:h=720:format=nv12",
			wantHWDecode: true,
		},
		{
			name:       "software decode for rotated input",
			mode:       media.AccelerationAuto,
			probe:      rotatedHEVC,
			readiness:  &ready,
			wantPath:   media.ExecutionPathVAAPISoftwareDecode,
			wantFilter: "scale=1080:1920,format=nv12,hwupload",
		},
		{
			name: "software tone mapping for HDR input",
			mode: media.AccelerationAuto,
			probe: media.Probe{
				Streams: []media.Stream{{
					Index:          0,
					CodecName:      "hevc",
					CodecType:      "video",
					Width:          1920,
					Height:         1080,
					PixelFormat:    "yuv420p10le",
					ColorSpace:     "bt2020nc",
					ColorTransfer:  "arib-std-b67",
					ColorPrimaries: "bt2020",
				}},
				Format: media.Format{DurationText: "1.000000"},
			},
			readiness:    &ready,
			wantPath:     media.ExecutionPathVAAPISoftwareDecode,
			wantSDRColor: true,
			wantFilter: "zscale=t=linear:npl=100,format=gbrpf32le," +
				"tonemap=hable:desat=0," +
				"zscale=p=bt709:t=bt709:m=bt709:r=tv," +
				"scale=1920:1080,format=nv12,hwupload",
		},
		{
			name: "software decode for unprobed pixel format",
			mode: media.AccelerationAuto,
			probe: media.Probe{
				Streams: []media.Stream{{
					Index:       4,
					CodecName:   "h264",
					CodecType:   "video",
					Width:       17,
					Height:      15,
					PixelFormat: "yuv444p",
				}},
				Format: media.Format{DurationText: "0.500000"},
			},
			readiness:  &ready,
			wantPath:   media.ExecutionPathVAAPISoftwareDecode,
			wantFilter: "scale=16:14,format=nv12,hwupload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := media.BuildPlan(media.PlanRequest{
				Operation:  corpus.OperationCompatibleVideo,
				InputPath:  "input.mov",
				OutputPath: "output.mp4",
				Input:      tt.probe,
				Acceleration: media.AccelerationConfig{
					Mode:   tt.mode,
					Device: media.DefaultVAAPIDevice,
				},
				VAAPI: tt.readiness,
			})
			if tt.wantError != "" {
				var planErr *media.PlanError
				if !errors.As(err, &planErr) || planErr.Code != tt.wantError {
					t.Fatalf("BuildPlan() error = %v; want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if plan.ExecutionPath != tt.wantPath {
				t.Errorf("execution path = %q; want %q", plan.ExecutionPath, tt.wantPath)
			}
			if plan.FallbackReason != tt.wantFallback {
				t.Errorf("fallback reason = %q; want %q", plan.FallbackReason, tt.wantFallback)
			}
			if !containsPair(plan.Args, "-vf", tt.wantFilter) {
				t.Errorf("args = %v; want filter %q", plan.Args, tt.wantFilter)
			}
			if containsPair(plan.Args, "-hwaccel", "vaapi") != tt.wantHWDecode {
				t.Errorf("hardware decode args = %v; want %t", plan.Args, tt.wantHWDecode)
			}
			if containsPair(plan.Args, "-color_trc", "bt709") != tt.wantSDRColor {
				t.Errorf("SDR color metadata args = %v; want %t", plan.Args, tt.wantSDRColor)
			}
			if tt.wantPath != media.ExecutionPathCPU {
				if !containsPair(plan.Args, "-c:v", "h264_vaapi") {
					t.Errorf("args = %v; want h264_vaapi encoder", plan.Args)
				}
				if !containsPair(plan.Args, "-protocol_whitelist", "file,pipe") {
					t.Errorf("args = %v; want local protocol allowlist", plan.Args)
				}
				if !containsPair(plan.Args, "-map", "0:"+streamIndexText(media.SelectStreams(tt.probe).Video)) {
					t.Errorf("args = %v; want explicit video map", plan.Args)
				}
			}
		})
	}
}

func TestCPUAndVAAPIPlansShareCompatibilityProfile(t *testing.T) {
	input := readProbeFixture(t, "iphone-spatial.json")
	cpu, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationSmallerVideo,
		InputPath:  "input.mov",
		OutputPath: "cpu.mp4",
		Input:      input,
		Acceleration: media.AccelerationConfig{
			Mode:   media.AccelerationCPU,
			Device: media.DefaultVAAPIDevice,
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(CPU) error = %v", err)
	}
	vaapi, err := media.BuildPlan(media.PlanRequest{
		Operation:  corpus.OperationSmallerVideo,
		InputPath:  "input.mov",
		OutputPath: "vaapi.mp4",
		Input:      input,
		Acceleration: media.AccelerationConfig{
			Mode:   media.AccelerationVAAPI,
			Device: media.DefaultVAAPIDevice,
		},
		VAAPI: &media.VAAPIReadiness{
			Device:                  media.DefaultVAAPIDevice,
			Ready:                   true,
			FunctionalProbeComplete: true,
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan(VA-API) error = %v", err)
	}
	if !reflect.DeepEqual(cpu.Expected, vaapi.Expected) {
		t.Errorf("CPU profile = %+v; VA-API profile = %+v", cpu.Expected, vaapi.Expected)
	}
	if !reflect.DeepEqual(cpu.Warnings, vaapi.Warnings) {
		t.Errorf("CPU warnings = %+v; VA-API warnings = %+v", cpu.Warnings, vaapi.Warnings)
	}
}

func streamIndexText(stream *media.Stream) string {
	if stream == nil {
		return "-1"
	}
	return strconv.Itoa(stream.Index)
}
