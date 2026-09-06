package imageconv

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guigui42/filetwist/internal/runner"
)

// TransparencyProbe is an independently testable libvips plan for detecting
// whether an alpha band contains any non-opaque pixels.
type TransparencyProbe struct {
	Commands []runner.Command
	MaxAlpha float64
}

// BuildTransparencyProbe constructs a direct alpha minimum probe.
func BuildTransparencyProbe(vipsPath, inputPath, workDir string, info Info) (TransparencyProbe, error) {
	if vipsPath == "" || inputPath == "" || workDir == "" || !info.HasAlpha || info.Bands < 2 {
		return TransparencyProbe{}, imageError(
			CodeInvalidRequest,
			"build transparency probe",
			errors.New("vips path, input, work directory, and an alpha band are required"),
		)
	}
	alphaPath := filepath.Join(workDir, "alpha.v")
	commands := []runner.Command{{
		Path: vipsPath,
		Args: []string{"extract_band", inputPath, alphaPath, strconv.Itoa(info.Bands - 1)},
	}}
	minimumInput := alphaPath
	switch info.BandFormat {
	case "uchar":
	case "ushort":
		alpha8Path := filepath.Join(workDir, "alpha-8.v")
		commands = append(commands, runner.Command{
			Path: vipsPath,
			Args: []string{"cast", alphaPath, alpha8Path, "uchar", "--shift"},
		})
		minimumInput = alpha8Path
	default:
		return TransparencyProbe{}, imageError(
			CodeAlphaUnsupported,
			"build transparency probe",
			fmt.Errorf("unsupported WebP alpha band format %q", info.BandFormat),
		)
	}
	commands = append(commands, runner.Command{
		Path: vipsPath,
		Args: []string{"min", minimumInput},
	})
	return TransparencyProbe{Commands: commands, MaxAlpha: 255}, nil
}

// ParseTransparency reports whether a vips min result found non-opaque alpha.
func ParseTransparency(output []byte, maxAlpha float64) (bool, error) {
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return false, imageError(CodeProbeFailed, "parse transparency probe", errors.New("alpha minimum is empty"))
	}
	minimum, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(minimum) || math.IsInf(minimum, 0) {
		return false, imageError(CodeProbeFailed, "parse transparency probe", fmt.Errorf("invalid alpha minimum %q", fields[0]))
	}
	return minimum < maxAlpha, nil
}
