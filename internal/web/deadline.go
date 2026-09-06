package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// deadlinePolicy bounds how long one request may stall without transferring
// bytes. It replaces a fixed whole-request timeout, which would reject a
// legitimate multi-gigabyte upload on a slow link.
//
// Both deadlines are rearmed immediately before every read or write, rather
// than once per window, because a call that is already blocked when its
// deadline expires is cut even though the transfer as a whole is still moving.
// The write deadline is armed only once the response starts, so nothing bounds
// the interval between the last body byte and the first response byte, where
// intake probing legitimately runs.
type deadlinePolicy struct {
	// ReadStall is the idle window granted to a request body.
	ReadStall time.Duration
	// WriteStall is the idle window granted to a started response.
	WriteStall time.Duration
	// MinRate is the average bytes-per-second floor a request body must
	// sustain once it has been open longer than ReadStall. Zero disables it.
	MinRate int64
	// Now returns the current time. Zero selects time.Now.
	Now func() time.Time
}

// stallKey addresses the per-request stall report in the request context.
type stallKey struct{}

// stallReport records that this request body was terminated for lack of
// progress. mime/multipart parses part headers through a bufio.Reader that
// drops a read error whenever it already holds a partial line, so the
// terminating error cannot be recovered from what the handler receives. The
// wrapper records the fact directly instead.
type stallReport struct {
	stalled atomic.Bool
}

// requestStalled reports whether the body of this request was cut for lack of
// progress rather than for being malformed.
func requestStalled(request *http.Request) bool {
	report, ok := request.Context().Value(stallKey{}).(*stallReport)
	return ok && report.stalled.Load()
}

// isTimeout reports whether err is a transport deadline expiry.
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

// stallError reports a body that failed the idle or throughput bound. It
// reports itself as a timeout so callers classify it exactly like the
// operating system deadline error it stands in for.
type stallError struct {
	message string
}

func (err stallError) Error() string   { return "web: " + err.message }
func (err stallError) Timeout() bool   { return true }
func (err stallError) Temporary() bool { return false }

func (policy deadlinePolicy) now() time.Time {
	if policy.Now != nil {
		return policy.Now()
	}
	return time.Now()
}

// wrap installs the request body and response writer deadline tracking around
// next. It is a no-op when both windows are disabled.
func (policy deadlinePolicy) wrap(next http.Handler) http.Handler {
	if policy.ReadStall <= 0 && policy.WriteStall <= 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		controller := http.NewResponseController(writer)
		// A response deadline inherited from an earlier request on this
		// connection would fire against a handler that has not written yet,
		// and intake probing runs before the first response byte.
		_ = controller.SetWriteDeadline(time.Time{})

		if policy.ReadStall > 0 && request.Body != nil && request.Body != http.NoBody {
			report := &stallReport{}
			request.Body = &deadlineBody{
				body:       request.Body,
				controller: controller,
				policy:     policy,
				started:    policy.now(),
				report:     report,
			}
			request = request.WithContext(
				context.WithValue(request.Context(), stallKey{}, report),
			)
		}
		if policy.WriteStall <= 0 {
			next.ServeHTTP(writer, request)
			return
		}
		tracked := &deadlineWriter{
			ResponseWriter: writer,
			controller:     controller,
			policy:         policy,
		}
		defer tracked.finish()
		next.ServeHTTP(tracked, request)
	})
}

// deadlineBody enforces the read idle window and the throughput floor on one
// request body.
type deadlineBody struct {
	body        io.ReadCloser
	controller  *http.ResponseController
	report      *stallReport
	policy      deadlinePolicy
	started     time.Time
	unsupported bool
	read        int64
}

func (reader *deadlineBody) Read(buffer []byte) (int, error) {
	reader.touch()
	count, err := reader.body.Read(buffer)
	reader.read += int64(count)
	if err != nil {
		if isTimeout(err) {
			reader.report.stalled.Store(true)
		}
		return count, err
	}
	if stalled := reader.checkRate(); stalled != nil {
		reader.report.stalled.Store(true)
		return count, stalled
	}
	return count, nil
}

func (reader *deadlineBody) Close() error {
	return reader.body.Close()
}

// touch grants the next read its own full idle window. The deadline is
// rewritten before every call rather than once per window, because a call that
// is already blocked when the deadline expires is cut even though the transfer
// as a whole is still making progress.
func (reader *deadlineBody) touch() {
	if reader.unsupported {
		return
	}
	deadline := reader.policy.now().Add(reader.policy.ReadStall)
	if err := reader.controller.SetReadDeadline(deadline); err != nil {
		// HTTP/2 and test response recorders do not support deadlines. The
		// request stays bounded by its context and by the byte ceiling.
		reader.unsupported = true
	}
}

// checkRate rejects a body that stays under the throughput floor after it has
// outlived the idle window. It closes the gap the idle window leaves open: a
// client that sends one byte just before every deadline never stalls, yet it
// still holds a connection and an upload reservation indefinitely.
func (reader *deadlineBody) checkRate() error {
	if reader.policy.MinRate <= 0 {
		return nil
	}
	elapsed := reader.policy.now().Sub(reader.started)
	if elapsed <= reader.policy.ReadStall {
		return nil
	}
	if float64(reader.read) >= elapsed.Seconds()*float64(reader.policy.MinRate) {
		return nil
	}
	return stallError{message: "request body fell below the minimum upload rate"}
}

// deadlineWriter arms the response write deadline on the first write and
// extends it while the response keeps making progress.
type deadlineWriter struct {
	http.ResponseWriter
	controller  *http.ResponseController
	policy      deadlinePolicy
	unsupported bool
}

// Unwrap exposes the original writer to http.ResponseController and to the
// net/http internals that identify their own response value.
func (writer *deadlineWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *deadlineWriter) WriteHeader(status int) {
	writer.touch()
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *deadlineWriter) Write(payload []byte) (int, error) {
	writer.touch()
	return writer.ResponseWriter.Write(payload)
}

// touch grants the next write its own full idle window, for the same reason
// the read side does: a large body is delivered as many partial writes, and
// each one has to be judged on its own progress.
func (writer *deadlineWriter) touch() {
	if writer.unsupported {
		return
	}
	deadline := writer.policy.now().Add(writer.policy.WriteStall)
	if err := writer.controller.SetWriteDeadline(deadline); err != nil {
		writer.unsupported = true
	}
}

// finish bounds the buffered flush net/http performs after the handler
// returns. The deadline is always rewritten from the current time so a reused
// keep-alive connection never starts its next response already expired.
func (writer *deadlineWriter) finish() {
	if writer.unsupported {
		return
	}
	_ = writer.controller.SetWriteDeadline(writer.policy.now().Add(writer.policy.WriteStall))
}

// baseWriter unwraps middleware writers so net/http sees its own response
// value. http.MaxBytesReader signals an oversized body to the server through
// an unexported interface that no wrapper can satisfy.
func baseWriter(writer http.ResponseWriter) http.ResponseWriter {
	for {
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return writer
		}
		writer = unwrapper.Unwrap()
	}
}
