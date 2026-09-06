package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// HasFastStart reports whether a top-level MP4 moov atom appears before mdat.
func HasFastStart(reader io.ReaderAt, size int64) (bool, error) {
	if reader == nil {
		return false, errors.New("media: MP4 reader must not be nil")
	}
	if size < 0 {
		return false, errors.New("media: MP4 size must not be negative")
	}

	var (
		offset  int64
		sawMoov bool
	)
	header := make([]byte, 16)
	for offset < size {
		if size-offset < 8 {
			return false, errors.New("media: truncated MP4 box header")
		}
		if _, err := reader.ReadAt(header[:8], offset); err != nil {
			return false, fmt.Errorf("media: read MP4 box header: %w", err)
		}

		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 1:
			if size-offset < 16 {
				return false, errors.New("media: truncated extended MP4 box header")
			}
			if _, err := reader.ReadAt(header[8:16], offset+8); err != nil {
				return false, fmt.Errorf("media: read extended MP4 box header: %w", err)
			}
			boxSize = int64(binary.BigEndian.Uint64(header[8:16]))
			headerSize = 16
		case 0:
			boxSize = size - offset
		}
		if boxSize < headerSize || boxSize > size-offset {
			return false, errors.New("media: invalid MP4 box size")
		}

		switch string(header[4:8]) {
		case "moov":
			sawMoov = true
		case "mdat":
			return sawMoov, nil
		}
		offset += boxSize
	}
	return false, nil
}
