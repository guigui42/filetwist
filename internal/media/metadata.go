package media

import "strings"

// SensitiveMetadata identifies the known sensitive tag categories exposed by
// ffprobe. It does not establish whether opaque metadata payloads are absent.
type SensitiveMetadata struct {
	GPS     bool
	EXIF    bool
	XMP     bool
	GainMap bool
}

// DetectSensitiveMetadata checks container and stream tag names using the
// native profiles' existing metadata observation policy.
func DetectSensitiveMetadata(input Probe) SensitiveMetadata {
	var metadata SensitiveMetadata
	inspect := func(tags map[string]string) {
		for key := range tags {
			key = strings.ToLower(key)
			metadata.GPS = metadata.GPS || strings.Contains(key, "gps") ||
				strings.Contains(key, "location") || strings.Contains(key, "iso6709")
			metadata.EXIF = metadata.EXIF || strings.Contains(key, "exif")
			metadata.XMP = metadata.XMP || strings.Contains(key, "xmp")
			metadata.GainMap = metadata.GainMap || strings.Contains(key, "gainmap") ||
				strings.Contains(key, "gain_map")
		}
	}
	inspect(input.Format.Tags)
	for _, stream := range input.Streams {
		inspect(stream.Tags)
	}
	return metadata
}
