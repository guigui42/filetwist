package corpus

// ManifestVersion is the only fixture manifest schema version supported by
// this package.
const ManifestVersion = "1.1"

// Manifest is the root of a compatibility fixture manifest.
type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	Fixtures      []Fixture `json:"fixtures"`
}

// Fixture describes one input and the compatibility result it must produce.
type Fixture struct {
	ID             string                `json:"id"`
	Title          string                `json:"title"`
	InputPath      string                `json:"input_path"`
	Representation FixtureRepresentation `json:"representation"`
	Source         Source                `json:"source"`
	Input          InputCharacteristics  `json:"input"`
	Operation      Operation             `json:"operation"`
	Expected       ExpectedResult        `json:"expected"`
	Tolerances     Tolerances            `json:"tolerances"`
	Traits         []string              `json:"traits"`
}

// FixtureRepresentation distinguishes executable media from synthetic
// ffprobe JSON used to exercise stream-selection policies that FFmpeg cannot
// encode into redistributable test media.
type FixtureRepresentation string

const (
	// RepresentationMedia identifies a real media file.
	RepresentationMedia FixtureRepresentation = "media"
	// RepresentationFFprobeJSON identifies a synthetic ffprobe JSON document.
	RepresentationFFprobeJSON FixtureRepresentation = "ffprobe_json"
)

// Source records the provenance and redistribution rules for a fixture.
type Source struct {
	Category       SourceCategory       `json:"category"`
	Privacy        PrivacyStatus        `json:"privacy"`
	Redistribution RedistributionStatus `json:"redistribution"`
	Generator      string               `json:"generator,omitempty"`
	License        string               `json:"license,omitempty"`
}

// SourceCategory identifies how a fixture was obtained.
type SourceCategory string

const (
	// SourceGenerated identifies a deterministically generated fixture.
	SourceGenerated SourceCategory = "generated"
	// SourcePrivateCapture identifies a private device capture that cannot be committed.
	SourcePrivateCapture SourceCategory = "private_capture"
	// SourcePublicDomain identifies a redistributable public-domain fixture.
	SourcePublicDomain SourceCategory = "public_domain"
	// SourcePermissive identifies a fixture covered by a permissive license.
	SourcePermissive SourceCategory = "permissive"
)

// PrivacyStatus describes the privacy review state of a fixture.
type PrivacyStatus string

const (
	// PrivacySynthetic means the fixture contains only generated content.
	PrivacySynthetic PrivacyStatus = "synthetic"
	// PrivacyScrubbed means the fixture was reviewed and stripped of personal data.
	PrivacyScrubbed PrivacyStatus = "scrubbed"
	// PrivacyPrivate means the fixture must remain in the gitignored private corpus.
	PrivacyPrivate PrivacyStatus = "private"
)

// RedistributionStatus describes whether a fixture may be committed or shared.
type RedistributionStatus string

const (
	// RedistributionAllowed permits committing and redistributing the fixture.
	RedistributionAllowed RedistributionStatus = "allowed"
	// RedistributionRestricted requires checking the recorded license before sharing.
	RedistributionRestricted RedistributionStatus = "restricted"
	// RedistributionProhibited keeps the fixture in the private corpus.
	RedistributionProhibited RedistributionStatus = "prohibited"
)

// InputCharacteristics records the declared properties of the source media.
type InputCharacteristics struct {
	MediaKind  MediaKind       `json:"media_kind"`
	Extension  string          `json:"extension"`
	MIMEType   string          `json:"mime_type"`
	Properties MediaProperties `json:"properties"`
}

// MediaKind is the broad kind of media in a fixture.
type MediaKind string

const (
	// MediaImage identifies still or animated image input.
	MediaImage MediaKind = "image"
	// MediaAudio identifies audio input.
	MediaAudio MediaKind = "audio"
	// MediaVideo identifies video input.
	MediaVideo MediaKind = "video"
)

// MediaProperties describes probeable media characteristics.
type MediaProperties struct {
	Container      string       `json:"container"`
	Streams        []Stream     `json:"streams"`
	Dimensions     *Dimensions  `json:"dimensions,omitempty"`
	DurationMillis *int64       `json:"duration_millis,omitempty"`
	Orientation    *Orientation `json:"orientation,omitempty"`
	Alpha          Presence     `json:"alpha"`
	Metadata       Metadata     `json:"metadata"`
}

// Stream describes an expected stream kind, codec, and multiplicity.
type Stream struct {
	Kind        StreamKind `json:"kind"`
	Codec       string     `json:"codec"`
	Count       int        `json:"count"`
	Channels    *int       `json:"channels,omitempty"`
	SampleRate  *int       `json:"sample_rate,omitempty"`
	PixelFormat string     `json:"pixel_format,omitempty"`
}

// StreamKind identifies a media stream.
type StreamKind string

const (
	// StreamImage identifies an image stream.
	StreamImage StreamKind = "image"
	// StreamAudio identifies an audio stream.
	StreamAudio StreamKind = "audio"
	// StreamVideo identifies a video stream.
	StreamVideo StreamKind = "video"
	// StreamSubtitle identifies a subtitle stream.
	StreamSubtitle StreamKind = "subtitle"
	// StreamData identifies a data or attachment stream.
	StreamData StreamKind = "data"
)

// Dimensions records pixel width and height.
type Dimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Orientation records the expected displayed rotation and normalization state.
type Orientation struct {
	RotationDegrees int  `json:"rotation_degrees"`
	Mirrored        bool `json:"mirrored"`
	PixelNormalized bool `json:"pixel_normalized"`
}

// Presence expresses whether a property must exist, must not exist, or is not
// part of the compatibility assertion.
type Presence string

const (
	// PresenceRequired requires the property to be present.
	PresenceRequired Presence = "required"
	// PresenceForbidden requires the property to be absent.
	PresenceForbidden Presence = "forbidden"
	// PresenceIgnored excludes the property from compatibility evaluation.
	PresenceIgnored Presence = "ignored"
)

// Metadata records expectations for compatibility-relevant metadata.
type Metadata struct {
	GPS          Presence `json:"gps"`
	ColorProfile Presence `json:"color_profile"`
	EXIF         Presence `json:"exif"`
	XMP          Presence `json:"xmp"`
	GainMap      Presence `json:"gain_map"`
}

// Operation is one named prototype conversion operation.
type Operation string

const (
	// OperationCompatiblePhoto creates a broadly compatible photo.
	OperationCompatiblePhoto Operation = "compatible_photo"
	// OperationSmallerPhoto creates a smaller lossy photo.
	OperationSmallerPhoto Operation = "smaller_photo"
	// OperationLosslessImage creates a lossless image.
	OperationLosslessImage Operation = "lossless_image"
	// OperationCompatibleVideo creates a broadly compatible video.
	OperationCompatibleVideo Operation = "compatible_video"
	// OperationSmallerVideo creates a smaller video.
	OperationSmallerVideo Operation = "smaller_video"
	// OperationExtractAudio extracts audio from media.
	OperationExtractAudio Operation = "extract_audio"
	// OperationCompatibleAudio creates broadly compatible audio.
	OperationCompatibleAudio Operation = "compatible_audio"
	// OperationLosslessAudio creates lossless audio.
	OperationLosslessAudio Operation = "lossless_audio"
)

// ExpectedResult declares whether conversion must succeed or be clearly rejected.
type ExpectedResult struct {
	Outcome       ExpectedOutcome  `json:"outcome"`
	RejectionCode string           `json:"rejection_code,omitempty"`
	Properties    *MediaProperties `json:"properties,omitempty"`
}

// ExpectedOutcome is the acceptable high-level fixture outcome.
type ExpectedOutcome string

const (
	// ExpectedSuccess requires a validated compatible output.
	ExpectedSuccess ExpectedOutcome = "success"
	// ExpectedRejection requires a clear rejection without an invalid output.
	ExpectedRejection ExpectedOutcome = "rejection"
)

// Tolerances defines allowed differences between expected and observed output.
// FrameRateMilliFPS and BitratePercent are reserved and must be zero until
// corresponding observed properties and comparisons are supported.
type Tolerances struct {
	DurationMillis    int64   `json:"duration_millis"`
	DimensionPixels   int     `json:"dimension_pixels"`
	FrameRateMilliFPS int     `json:"frame_rate_millifps"`
	BitratePercent    float64 `json:"bitrate_percent"`
}
