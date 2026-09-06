package runner

type boundedBuffer struct {
	bytes     []byte
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{
		bytes: make([]byte, 0, min(limit, 4096)),
		limit: limit,
	}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.bytes)
	if remaining > 0 {
		copied := min(remaining, len(p))
		b.bytes = append(b.bytes, p[:copied]...)
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) output() Output {
	bytes := make([]byte, len(b.bytes))
	copy(bytes, b.bytes)
	return Output{
		Bytes:     bytes,
		Limit:     b.limit,
		Truncated: b.truncated,
	}
}
