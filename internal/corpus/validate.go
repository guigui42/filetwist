package corpus

import (
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"

	"github.com/guigui42/filetwist/internal/profiles"
)

var tokenPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// ValidationError identifies one invalid field in a fixture manifest.
type ValidationError struct {
	Field   string
	Problem string
}

// Error formats a validation error with its field path.
func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Problem)
}

// ValidationErrors contains every validation error found in one contract.
type ValidationErrors []ValidationError

// Error formats all validation errors.
func (errs ValidationErrors) Error() string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}

// Validate checks the complete fixture manifest contract.
func (manifest Manifest) Validate() error {
	var errs ValidationErrors

	if manifest.SchemaVersion != ManifestVersion {
		errs.add("schema_version", fmt.Sprintf("must be %q", ManifestVersion))
	}
	if len(manifest.Fixtures) == 0 {
		errs.add("fixtures", "must contain at least one fixture")
	}

	ids := make(map[string]struct{}, len(manifest.Fixtures))
	for index, fixture := range manifest.Fixtures {
		field := fmt.Sprintf("fixtures[%d]", index)
		fixture.validate(field, &errs)

		if _, exists := ids[fixture.ID]; exists {
			errs.add(field+".id", "must be unique")
		}
		ids[fixture.ID] = struct{}{}
	}

	return errs.asError()
}

func (fixture Fixture) validate(field string, errs *ValidationErrors) {
	validateToken(field+".id", fixture.ID, errs)
	if strings.TrimSpace(fixture.Title) == "" {
		errs.add(field+".title", "must not be empty")
	}
	validateFixturePath(field+".input_path", fixture.InputPath, errs)
	switch fixture.Representation {
	case RepresentationMedia:
		if strings.HasSuffix(fixture.InputPath, ".ffprobe.json") {
			errs.add(field+".input_path", "media representation cannot use an ffprobe JSON path")
		}
	case RepresentationFFprobeJSON:
		if fixture.Source.Category != SourceGenerated {
			errs.add(field+".representation", "ffprobe JSON fixtures must be generated")
		}
		if !strings.HasSuffix(fixture.InputPath, ".ffprobe.json") {
			errs.add(field+".input_path", "ffprobe JSON representation must use an .ffprobe.json path")
		}
	default:
		errs.add(field+".representation", "must be media or ffprobe_json")
	}
	fixture.Source.validate(field+".source", errs)
	fixture.Input.validate(field+".input", errs)

	spec, supported := profiles.Lookup(fixture.Operation)
	if !supported {
		errs.add(field+".operation", "is not a supported named operation")
	} else if !spec.Accepts(fixture.Input.MediaKind) {
		errs.add(field+".operation", "is not valid for the input media kind")
	}
	fixture.Expected.validate(field+".expected", spec.OutputKind, errs)
	fixture.Tolerances.validate(field+".tolerances", errs)
	validateTraits(field+".traits", fixture.Traits, errs)
}

func (source Source) validate(field string, errs *ValidationErrors) {
	switch source.Category {
	case SourceGenerated, SourcePrivateCapture, SourcePublicDomain, SourcePermissive:
	default:
		errs.add(field+".category", "is not supported")
	}
	switch source.Privacy {
	case PrivacySynthetic, PrivacyScrubbed, PrivacyPrivate:
	default:
		errs.add(field+".privacy", "is not supported")
	}
	switch source.Redistribution {
	case RedistributionAllowed, RedistributionRestricted, RedistributionProhibited:
	default:
		errs.add(field+".redistribution", "is not supported")
	}

	switch source.Category {
	case SourceGenerated:
		if strings.TrimSpace(source.Generator) == "" {
			errs.add(field+".generator", "is required for generated fixtures")
		}
		if source.Privacy != PrivacySynthetic {
			errs.add(field+".privacy", "generated fixtures must be synthetic")
		}
		if source.Redistribution != RedistributionAllowed {
			errs.add(field+".redistribution", "generated fixtures must allow redistribution")
		}
	case SourcePrivateCapture:
		if source.Privacy != PrivacyPrivate {
			errs.add(field+".privacy", "private captures must be private")
		}
		if source.Redistribution != RedistributionProhibited {
			errs.add(field+".redistribution", "private captures must prohibit redistribution")
		}
	case SourcePermissive:
		if strings.TrimSpace(source.License) == "" {
			errs.add(field+".license", "is required for permissively licensed fixtures")
		}
	}
}

func (input InputCharacteristics) validate(field string, errs *ValidationErrors) {
	if !validMediaKind(input.MediaKind) {
		errs.add(field+".media_kind", "must be image, audio, or video")
	}
	if input.Extension == "" || input.Extension != strings.ToLower(input.Extension) ||
		!strings.HasPrefix(input.Extension, ".") || path.Ext("fixture"+input.Extension) != input.Extension {
		errs.add(field+".extension", "must be one lowercase file extension including the leading dot")
	}
	if input.MIMEType == "" {
		errs.add(field+".mime_type", "must not be empty")
	} else if mediaType, _, err := mime.ParseMediaType(input.MIMEType); err != nil || mediaType != input.MIMEType {
		errs.add(field+".mime_type", "must be a normalized MIME media type")
	}
	input.Properties.validate(field+".properties", input.MediaKind, errs)
}

func (properties MediaProperties) validate(field string, mediaKind MediaKind, errs *ValidationErrors) {
	validateToken(field+".container", properties.Container, errs)
	if len(properties.Streams) == 0 {
		errs.add(field+".streams", "must contain at least one stream")
	}
	for index, stream := range properties.Streams {
		stream.validate(fmt.Sprintf("%s.streams[%d]", field, index), errs)
	}
	if validMediaKind(mediaKind) && !hasPrimaryStream(properties.Streams, mediaKind) {
		errs.add(field+".streams", fmt.Sprintf("must contain a %s stream", mediaKind))
	}

	if mediaKind == MediaImage || mediaKind == MediaVideo {
		if properties.Dimensions == nil {
			errs.add(field+".dimensions", "is required for visual media")
		}
		if properties.Orientation == nil {
			errs.add(field+".orientation", "is required for visual media")
		}
	}
	if properties.Dimensions != nil {
		if properties.Dimensions.Width <= 0 {
			errs.add(field+".dimensions.width", "must be greater than zero")
		}
		if properties.Dimensions.Height <= 0 {
			errs.add(field+".dimensions.height", "must be greater than zero")
		}
	}
	if properties.DurationMillis != nil && *properties.DurationMillis <= 0 {
		errs.add(field+".duration_millis", "must be greater than zero")
	}
	if mediaKind == MediaAudio || mediaKind == MediaVideo {
		if properties.DurationMillis == nil {
			errs.add(field+".duration_millis", "is required for timed media")
		}
	}
	if properties.Orientation != nil {
		switch properties.Orientation.RotationDegrees {
		case 0, 90, 180, 270:
		default:
			errs.add(field+".orientation.rotation_degrees", "must be 0, 90, 180, or 270")
		}
	}
	validatePresence(field+".alpha", properties.Alpha, errs)
	properties.Metadata.validate(field+".metadata", errs)
}

func (stream Stream) validate(field string, errs *ValidationErrors) {
	switch stream.Kind {
	case StreamImage, StreamAudio, StreamVideo, StreamSubtitle, StreamData:
	default:
		errs.add(field+".kind", "is not a supported stream kind")
	}
	validateToken(field+".codec", stream.Codec, errs)
	if stream.Count <= 0 {
		errs.add(field+".count", "must be greater than zero")
	}
	if stream.Channels != nil && *stream.Channels <= 0 {
		errs.add(field+".channels", "must be greater than zero")
	}
	if stream.SampleRate != nil && *stream.SampleRate <= 0 {
		errs.add(field+".sample_rate", "must be greater than zero")
	}
	if stream.PixelFormat != "" {
		validateToken(field+".pixel_format", stream.PixelFormat, errs)
	}
}

func (metadata Metadata) validate(field string, errs *ValidationErrors) {
	validatePresence(field+".gps", metadata.GPS, errs)
	validatePresence(field+".color_profile", metadata.ColorProfile, errs)
	validatePresence(field+".exif", metadata.EXIF, errs)
	validatePresence(field+".xmp", metadata.XMP, errs)
	validatePresence(field+".gain_map", metadata.GainMap, errs)
}

func (expected ExpectedResult) validate(field string, mediaKind MediaKind, errs *ValidationErrors) {
	switch expected.Outcome {
	case ExpectedSuccess:
		if expected.RejectionCode != "" {
			errs.add(field+".rejection_code", "must be empty for successful output")
		}
		if expected.Properties == nil {
			errs.add(field+".properties", "is required for successful output")
		} else {
			expected.Properties.validate(field+".properties", mediaKind, errs)
		}
	case ExpectedRejection:
		validateToken(field+".rejection_code", expected.RejectionCode, errs)
		if expected.Properties != nil {
			errs.add(field+".properties", "must be omitted for rejected input")
		}
	default:
		errs.add(field+".outcome", "must be success or rejection")
	}
}

func (tolerances Tolerances) validate(field string, errs *ValidationErrors) {
	if tolerances.DurationMillis < 0 {
		errs.add(field+".duration_millis", "must not be negative")
	}
	if tolerances.DimensionPixels < 0 {
		errs.add(field+".dimension_pixels", "must not be negative")
	}
	if tolerances.FrameRateMilliFPS != 0 {
		errs.add(field+".frame_rate_millifps", "must be zero until frame rate can be observed and compared")
	}
	if tolerances.BitratePercent != 0 {
		errs.add(field+".bitrate_percent", "must be zero until bitrate can be observed and compared")
	}
}

func (errs *ValidationErrors) add(field, problem string) {
	*errs = append(*errs, ValidationError{Field: field, Problem: problem})
}

func (errs ValidationErrors) asError() error {
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateFixturePath(field, value string, errs *ValidationErrors) {
	if value == "" || strings.Contains(value, `\`) || strings.ContainsRune(value, 0) || path.IsAbs(value) ||
		path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		errs.add(field, "must be a clean relative slash-separated path")
	}
}

func validateToken(field, value string, errs *ValidationErrors) {
	if !tokenPattern.MatchString(value) {
		errs.add(field, "must be a lowercase token")
	}
}

func validatePresence(field string, value Presence, errs *ValidationErrors) {
	switch value {
	case PresenceRequired, PresenceForbidden, PresenceIgnored:
	default:
		errs.add(field, "must be required, forbidden, or ignored")
	}
}

func validateTraits(field string, traits []string, errs *ValidationErrors) {
	seen := make(map[string]struct{}, len(traits))
	for index, trait := range traits {
		traitField := fmt.Sprintf("%s[%d]", field, index)
		validateToken(traitField, trait, errs)
		if _, exists := seen[trait]; exists {
			errs.add(traitField, "must be unique")
		}
		seen[trait] = struct{}{}
	}
}

func validMediaKind(mediaKind MediaKind) bool {
	return mediaKind == MediaImage || mediaKind == MediaAudio || mediaKind == MediaVideo
}

func hasPrimaryStream(streams []Stream, mediaKind MediaKind) bool {
	var required StreamKind
	switch mediaKind {
	case MediaImage:
		required = StreamImage
	case MediaAudio:
		required = StreamAudio
	case MediaVideo:
		required = StreamVideo
	default:
		return false
	}

	for _, stream := range streams {
		if stream.Kind == required && stream.Count > 0 {
			return true
		}
	}
	return false
}
