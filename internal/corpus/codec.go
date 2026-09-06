package corpus

import (
	"encoding/json"
	"fmt"
	"io"
)

// DecodeManifest decodes and validates one strict JSON fixture manifest.
func DecodeManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	if err := decodeStrict(reader, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// EncodeManifest validates and writes one fixture manifest as indented JSON.
func EncodeManifest(writer io.Writer, manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := encodeJSON(writer, manifest); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}

func decodeStrict[T any](reader io.Reader, value *T) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func encodeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
