package deploy

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestRuntimeUsesOnlySourceBuiltFFmpeg(t *testing.T) {
	data, err := os.ReadFile("Dockerfile.runtime")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	start := strings.LastIndex(dockerfile, "\nFROM ")
	if start < 0 {
		t.Fatal("runtime stage not found")
	}
	runtime := dockerfile[start:]
	for _, required := range []string{
		"AS ffmpeg-build",
		"ARG FFMPEG_VERSION=7:7.1.5-0+deb13u1",
		"ARG FFMPEG_DSC_SHA256=9ed2ed34cbe7f056eeebbe9045c5e2d15e41b5b053fe7c8ba6979a0b6fb081ce",
		"COPY --from=ffmpeg-build /opt/ffmpeg /opt/ffmpeg",
		"scripts/build-ffmpeg.sh",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("missing source-build requirement %q", required)
		}
	}
	for _, forbidden := range []string{"ffmpeg", "libavcodec61", "libavdevice61", "libavfilter10", "libavformat61", "libavutil59", "libpostproc58", "libswresample5", "libswscale8", "libzvbi0t64", "libcdio19t64"} {
		t.Run(forbidden, func(t *testing.T) {
			packageLine := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(forbidden) + `(?:\s|=)`)
			if packageLine.MatchString(runtime) {
				t.Errorf("runtime must not install Debian %s alongside the corrected FFmpeg build", forbidden)
			}
		})
	}
	env := strings.Index(runtime, `ENV PATH="/opt/ffmpeg/bin:`)
	buildconf := strings.Index(runtime, "&& ffmpeg -hide_banner -buildconf")
	if env < 0 || buildconf < env {
		t.Fatal("the custom FFmpeg must be on PATH before recording its build configuration")
	}
	for _, required := range []string{
		`LD_LIBRARY_PATH="/opt/ffmpeg/lib:/opt/vips/lib"`,
		"/usr/share/doc/ffmpeg/copyright",
		"libva-drm2",
		"libzvbi",
		"libcdio",
	} {
		if !strings.Contains(runtime, required) {
			t.Errorf("runtime is missing loader, attribution, or forbidden-library check %q", required)
		}
	}
}

func TestFFmpegBuildPreservesProfilesAndCorrespondingSource(t *testing.T) {
	data, err := os.ReadFile("../scripts/build-ffmpeg.sh")
	if err != nil {
		t.Fatal(err)
	}
	build := string(data)
	for _, required := range []string{
		"apt-get source --download-only",
		"sha256sum --check --strict",
		"dpkg-source --extract",
		"--disable-autodetect",
		"--disable-libzvbi",
		"--disable-libcdio",
		"--enable-shared",
		"--disable-static",
		"--enable-gpl",
		"--enable-libx264",
		"--enable-libmp3lame",
		"--enable-libdav1d",
		"--enable-libzimg",
		"--enable-vaapi",
		"--enable-libdrm",
		"--enable-ffmpeg",
		"--enable-ffprobe",
		"make -j",
		"/opt/ffmpeg/share/licenses/ffmpeg",
		"SHA256SUMS",
		"COPYING.",
		"LICENSE.md",
		"debian/copyright",
		"SOURCE",
	} {
		t.Run(required, func(t *testing.T) {
			if !strings.Contains(build, required) {
				t.Errorf("missing FFmpeg build requirement %q", required)
			}
		})
	}
	for _, forbidden := range []string{"--enable-nonfree", "--enable-libzvbi", "--enable-libcdio", "--disable-decoders", "--disable-filters", "--disable-demuxers"} {
		if strings.Contains(build, forbidden) {
			t.Errorf("FFmpeg build contains incompatible or profile-restricting option %q", forbidden)
		}
	}
}

func TestFFmpegBuildRejectsUnboundedParallelism(t *testing.T) {
	for _, jobs := range []string{"0", "-1", "17", "all", "1 2"} {
		t.Run(jobs, func(t *testing.T) {
			command := exec.Command("sh", "../scripts/build-ffmpeg.sh")
			command.Env = append(os.Environ(),
				"FFMPEG_VERSION=7:7.1.5-0+deb13u1",
				"FFMPEG_DSC_SHA256=unused",
				"DEBIAN_SNAPSHOT=20260905T000000Z",
				"FFMPEG_BUILD_JOBS="+jobs,
			)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "FFMPEG_BUILD_JOBS must be between 1 and 16") {
				t.Fatalf("build with %q jobs: error = %v, output = %s", jobs, err, output)
			}
		})
	}
}

func TestRuntimeDependencyCacheIgnoresReleaseMetadata(t *testing.T) {
	data, err := os.ReadFile("Dockerfile.runtime")
	if err != nil {
		t.Fatal(err)
	}

	dockerfile := string(data)
	start := strings.LastIndex(dockerfile, "\nFROM ")
	if start < 0 {
		t.Fatal("runtime stage not found")
	}
	runtime := dockerfile[start:]
	dependencies := strings.Index(runtime, "apt-get install")
	if dependencies < 0 {
		t.Fatal("runtime dependency installation not found")
	}
	// RUN instructions inherit every ARG in scope, even if not used explicitly.
	for _, name := range []string{"VERSION", "REVISION"} {
		t.Run(name, func(t *testing.T) {
			arg := strings.Index(runtime, "\nARG "+name+"=")
			if arg < 0 {
				t.Fatalf("runtime %s argument not found", name)
			}
			if arg < dependencies {
				t.Fatalf("%s must be declared after dependency installation to preserve its cache", name)
			}
		})
	}
}

func TestApplicationBuildOnlyCompilesGoOverPinnedRuntime(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(data)
	pin := regexp.MustCompile(`(?m)^ARG MEDIA_RUNTIME_IMAGE=ghcr.io/guigui42/filetwist@sha256:[0-9a-f]{64}$`)
	if !pin.MatchString(recipe) || strings.Count(recipe, "\nFROM ") != 2 {
		t.Fatal("application build must contain only a Go builder and a digest-pinned runtime stage")
	}
	for _, forbidden := range []string{"apt-get", "meson", "build-ffmpeg", "ffmpeg-build", "vips-build", "Dockerfile.runtime AS"} {
		if strings.Contains(recipe, forbidden) {
			t.Errorf("application build must not compile or install native dependencies: %s", forbidden)
		}
	}
	for _, required := range []string{
		"FROM ${MEDIA_RUNTIME_IMAGE}", "go build -trimpath",
		"COPY --from=go-build /out/filetwist ", "COPY --from=go-build /out/filetwist-server ",
		"USER 10001:10001", "io.github.filetwist.media-runtime=",
	} {
		if !strings.Contains(recipe, required) {
			t.Errorf("application recipe missing %s", required)
		}
	}
	preserve := strings.Index(recipe, "cp /usr/local/share/licenses/filetwist/Dockerfile")
	overwrite := strings.Index(recipe, "COPY --chmod=0644 deploy/Dockerfile ")
	if preserve < 0 || preserve > overwrite {
		t.Fatal("bootstrap native recipe must be preserved before replacing the application recipe")
	}
}
