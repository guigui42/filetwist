package imageconv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/guigui42/filetwist/internal/probe"
	"github.com/guigui42/filetwist/internal/runner"
)

// ProbeFile asks libvips to select a loader from the input content and returns
// normalized compatibility properties.
func ProbeFile(ctx context.Context, run probe.RunFunc, headerPath, inputPath string) (Info, error) {
	if ctx == nil {
		return Info{}, imageError(CodeInvalidRequest, "probe input", errors.New("context must not be nil"))
	}
	if run == nil || headerPath == "" || inputPath == "" {
		return Info{}, imageError(CodeInvalidRequest, "probe input", errors.New("run function and paths are required"))
	}

	result, err := run(ctx, runner.Command{
		Path: headerPath,
		Args: []string{"-a", inputPath},
	})
	if err != nil {
		return Info{}, imageError(CodeProbeFailed, "probe input", err)
	}
	if result.Stdout.Truncated {
		return Info{}, imageError(CodeProbeFailed, "probe input", errors.New("header output was truncated"))
	}

	info, err := ParseHeader(result.Stdout.Bytes)
	if err != nil {
		return Info{}, imageError(CodeUnsupportedInput, "parse input header", err)
	}
	return info, nil
}

// ParseHeader parses bounded vipsheader -a output.
func ParseHeader(output []byte) (Info, error) {
	fields := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		fields[key] = value
	}
	if err := scanner.Err(); err != nil {
		return Info{}, fmt.Errorf("scan header: %w", err)
	}

	width, err := positiveHeaderInt(fields, "width")
	if err != nil {
		return Info{}, err
	}
	height, err := positiveHeaderInt(fields, "height")
	if err != nil {
		return Info{}, err
	}
	bands, err := positiveHeaderInt(fields, "bands")
	if err != nil {
		return Info{}, err
	}

	loader := normalizeLoader(fields["vips-loader"])
	format, mimeType, err := detectFormat(loader, fields)
	if err != nil {
		return Info{}, err
	}

	orientation := 1
	if value := fields["orientation"]; value != "" {
		orientation, err = firstInteger(value)
		if err != nil || orientation < 1 || orientation > 8 {
			return Info{}, errors.New("invalid orientation")
		}
	}

	pages := 1
	if value := fields["n-pages"]; value != "" {
		pages, err = firstInteger(value)
		if err != nil || pages < 1 {
			return Info{}, errors.New("invalid page count")
		}
	} else if value := fields["page-height"]; value != "" {
		pageHeight, pageErr := firstInteger(value)
		if pageErr != nil || pageHeight <= 0 || height%pageHeight != 0 {
			return Info{}, errors.New("invalid page height")
		}
		pages = height / pageHeight
	}

	metadata := detectMetadata(fields)
	cicp, err := parseCICP(fields)
	if err != nil {
		return Info{}, err
	}
	bandFormat := strings.ToLower(fields["format"])
	coding := strings.ToLower(fields["coding"])
	interpretation := strings.ToLower(fields["interpretation"])
	hasAlpha := inferAlpha(bands, interpretation, coding)
	if value := fields["has-alpha"]; value != "" {
		hasAlpha, err = strconv.ParseBool(strings.ToLower(value))
		if err != nil {
			return Info{}, errors.New("invalid has-alpha value")
		}
	}

	return Info{
		Format:         format,
		MIMEType:       mimeType,
		Loader:         loader,
		Width:          width,
		Height:         height,
		Bands:          bands,
		BandFormat:     bandFormat,
		Coding:         coding,
		Interpretation: interpretation,
		Orientation:    orientation,
		HDR:            cicp.Transfer == 16 || cicp.Transfer == 18,
		HasAlpha:       hasAlpha,
		Pages:          pages,
		CICP:           cicp,
		Metadata:       metadata,
	}, nil
}

func parseCICP(fields map[string]string) (CICP, error) {
	const unknown = -1
	cicp := CICP{
		Primaries: unknown,
		Transfer:  unknown,
		Matrix:    unknown,
		FullRange: unknown,
	}
	keys := []struct {
		name   string
		target *int
	}{
		{name: "cicp-colour-primaries", target: &cicp.Primaries},
		{name: "cicp-transfer-characteristics", target: &cicp.Transfer},
		{name: "cicp-matrix-coefficients", target: &cicp.Matrix},
		{name: "cicp-full-range-flag", target: &cicp.FullRange},
	}
	for _, key := range keys {
		raw, ok := fields[key.name]
		if !ok {
			continue
		}
		value, err := firstInteger(raw)
		if err != nil {
			return CICP{}, fmt.Errorf("invalid %s", key.name)
		}
		*key.target = value
		cicp.Present = true
	}
	if !cicp.Present {
		return CICP{}, nil
	}
	return cicp, nil
}

func positiveHeaderInt(fields map[string]string, key string) (int, error) {
	value, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	parsed, err := firstInteger(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return parsed, nil
}

func firstInteger(value string) (int, error) {
	token := strings.Fields(value)
	if len(token) == 0 {
		return 0, errors.New("integer value is empty")
	}
	return strconv.Atoi(token[0])
}

func normalizeLoader(loader string) string {
	loader = strings.ToLower(strings.TrimSpace(loader))
	loader = strings.TrimSuffix(loader, "_source")
	loader = strings.TrimSuffix(loader, "_buffer")
	return loader
}

func detectFormat(loader string, fields map[string]string) (Format, string, error) {
	switch loader {
	case "jpegload", "uhdrload":
		return FormatJPEG, "image/jpeg", nil
	case "pngload":
		return FormatPNG, "image/png", nil
	case "webpload":
		return FormatWebP, "image/webp", nil
	case "heifload":
		compression := strings.ToLower(fields["heif-compression"] + " " + fields["compression"])
		if strings.Contains(compression, "av1") || strings.Contains(compression, "avif") {
			return FormatAVIF, "image/avif", nil
		}
		return FormatHEIF, "image/heif", nil
	case "tiffload":
		return FormatTIFF, "image/tiff", nil
	case "gifload", "nsgifload":
		return FormatGIF, "image/gif", nil
	case "magickload":
		if strings.EqualFold(strings.TrimSpace(fields["magick-format"]), "BMP") {
			return FormatBMP, "image/bmp", nil
		}
	}
	return "", "", fmt.Errorf("unsupported libvips loader %q", loader)
}

func detectMetadata(fields map[string]string) Metadata {
	var metadata Metadata
	for key := range fields {
		switch {
		case key == "icc-profile-data":
			metadata.ICC = true
		case key == "xmp-data":
			metadata.XMP = true
		case key == "gainmap-data":
			metadata.GainMap = true
		case key == "exif-data" || strings.HasPrefix(key, "exif-"):
			metadata.EXIF = true
		}

		if strings.HasPrefix(key, "exif-") && strings.Contains(key, "gps") {
			metadata.GPS = true
		}
		if strings.HasPrefix(key, "exif-") &&
			(strings.Contains(key, "datetimeoriginal") || strings.Contains(key, "datetimedigitized")) {
			metadata.CaptureDate = true
		}
	}
	return metadata
}

func inferAlpha(bands int, interpretation, coding string) bool {
	if coding == "labq" {
		return false
	}
	colorBands := 0
	switch interpretation {
	case "b-w", "grey16":
		colorBands = 1
	case "cmyk":
		colorBands = 4
	case "srgb", "rgb", "rgb16", "scrgb", "hsv", "lab", "lch", "xyz", "yxy":
		colorBands = 3
	}
	return colorBands > 0 && bands == colorBands+1
}
