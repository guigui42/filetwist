package storage

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxFileNameLength bounds a stored file name including its extension.
const MaxFileNameLength = 100

// SafeFileName reduces a browser-supplied file name to a conservative,
// path-free name. It strips directories, normalizes separators and control
// characters, collapses runs of unsafe characters, and bounds the length. It
// returns fallback when nothing usable remains.
func SafeFileName(name, fallback string) string {
	cleaned := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, name)
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	if index := strings.LastIndex(cleaned, "/"); index >= 0 {
		cleaned = cleaned[index+1:]
	}
	if !utf8.ValidString(cleaned) {
		cleaned = ""
	}

	// Leading dots never introduce an extension, so a dotfile keeps its name
	// as the stem instead of becoming an extension-only file.
	trimmed := strings.TrimLeft(cleaned, ".")
	rawExtension := filepath.Ext(trimmed)
	extension := sanitizeToken(rawExtension)
	stem := sanitizeToken(strings.TrimSuffix(trimmed, rawExtension))
	if stem == "" {
		stem = fallback
	}
	if extension != "" {
		extension = "." + extension
		if len(extension) > 16 {
			extension = extension[:16]
		}
	}
	limit := MaxFileNameLength - len(extension)
	if limit < 1 {
		limit = 1
	}
	if len(stem) > limit {
		stem = stem[:limit]
	}
	stem = strings.Trim(stem, "-.")
	if stem == "" {
		stem = fallback
	}
	return stem + extension
}

func sanitizeToken(value string) string {
	var builder strings.Builder
	separator := false
	for _, character := range strings.TrimPrefix(value, ".") {
		switch {
		case unicode.IsLetter(character) && character < unicode.MaxASCII,
			unicode.IsDigit(character) && character < unicode.MaxASCII:
			builder.WriteRune(unicode.ToLower(character))
			separator = false
		case character == '.' || character == '-' || character == '_':
			if builder.Len() > 0 && !separator {
				builder.WriteRune(character)
				separator = true
			}
		default:
			if builder.Len() > 0 && !separator {
				builder.WriteByte('-')
				separator = true
			}
		}
	}
	return strings.Trim(builder.String(), "-._")
}

// UniqueFileName appends a numeric suffix until name is unused in taken. The
// returned name is also recorded in taken.
func UniqueFileName(taken map[string]bool, name string) string {
	if taken == nil {
		return name
	}
	if !taken[name] {
		taken[name] = true
		return name
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for counter := 2; ; counter++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, counter, extension)
		if !taken[candidate] {
			taken[candidate] = true
			return candidate
		}
	}
}

// ContentType returns a conservative served content type for a stored output
// name. Unknown extensions fall back to application/octet-stream so browsers
// never sniff user content into an active type.
func ContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".heic", ".heif":
		return "image/heic"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".m4a":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".zip":
		return "application/zip"
	default:
		if detected := mime.TypeByExtension(filepath.Ext(name)); detected != "" &&
			!strings.HasPrefix(detected, "text/html") &&
			!strings.Contains(detected, "javascript") &&
			!strings.Contains(detected, "xml") {
			return detected
		}
		return "application/octet-stream"
	}
}
