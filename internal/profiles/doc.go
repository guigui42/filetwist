// Package profiles owns Filetwist's stable conversion profile vocabulary and
// declarative output facts.
//
// MediaKind is colocated here so corpus contracts, conversion orchestration,
// and the profile registry share a dependency-light taxonomy without an import
// cycle. Media detection and probing remain the responsibility of the image and
// media engine packages.
package profiles
