package common

import (
	"context"
	"errors"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// EnsureRequestCorrelation preserves the logical ID through retries and route changes.
func EnsureRequestCorrelation(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	id := c.GetString("ccx.request_correlation_id")
	if id == "" {
		id = observability.Correlation(c.Request.Context())
	}
	if id == "" {
		id = observability.NewCorrelationID()
	}
	c.Set("ccx.request_correlation_id", id)
	c.Request = c.Request.WithContext(observability.WithCorrelation(c.Request.Context(), id))
	return id
}
func startPayloadAttempt(c *gin.Context, envCfg *config.EnvConfig, ctx observability.Context, req *http.Request) (*http.Request, *observability.Observer) {
	if envCfg == nil || req == nil {
		return req, nil
	}
	observer := observability.NewObserver(envCfg.PayloadCaptureMode, ctx, func(summary observability.Summary) {
		data, err := observability.MarshalSummary(summary)
		if err == nil {
			RequestLogf(c, "[PayloadSummary] %s", data)
		}
	})
	if observer != nil && c != nil && c.Request != nil {
		c.Request = observability.Bind(c.Request, observer)
		req = observability.Bind(req, observer)
	}
	return observability.Bind(req, observer), observer
}

type payloadObserverContextKey struct{}

func PayloadObserver(c *gin.Context) *observability.Observer {
	if c != nil && c.Request != nil {
		return observability.FromContext(c.Request.Context())
	}
	return nil
}
func ObservePayloadSSELine(c *gin.Context, line string) {
	if strings.HasPrefix(line, "data:") {
		PayloadObserver(c).Data(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
}

// ObserveDownstreamSSEWriter records client-facing SSE without touching upstream event counts.
func ObserveDownstreamSSEWriter(w gin.ResponseWriter, observer *observability.Observer) gin.ResponseWriter {
	if observer == nil {
		return w
	}
	return &downstreamWriter{ResponseWriter: w, observer: observer}
}

type downstreamWriter struct {
	gin.ResponseWriter
	observer *observability.Observer
	pending  string
}

func (w *downstreamWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *downstreamWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.pending += string(p[:n])
	for {
		i := strings.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(w.pending[:i], "\r")
		w.pending = w.pending[i+1:]
		if strings.HasPrefix(line, "data:") {
			w.observer.DownstreamData(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(w.pending) > 64*1024 {
		w.pending = ""
	}
	return n, err
}

// ObserveUpstreamSSEBody records upstream frames and read termination without
// changing bytes handed to the existing protocol parser.
func ObserveUpstreamSSEBody(body io.ReadCloser, observer *observability.Observer) io.ReadCloser {
	if observer == nil || body == nil {
		return body
	}
	return &observedSSEBody{ReadCloser: body, observer: observer}
}

type observedSSEBody struct {
	io.ReadCloser
	observer *observability.Observer
	pending  string
}

func (r *observedSSEBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.pending += string(p[:n])
		for {
			index := strings.IndexByte(r.pending, '\n')
			if index < 0 {
				break
			}
			line := strings.TrimRight(r.pending[:index], "\r")
			r.pending = r.pending[index+1:]
			if strings.HasPrefix(line, "data:") {
				r.observer.Data(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if len(r.pending) > 64*1024 {
			r.pending = ""
		}
	}
	if err != nil {
		r.observer.ReadEnd(err)
	}
	return n, err
}

var streamObservationToken = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func streamObservationID(c *gin.Context, key string) string {
	if c != nil {
		if value, ok := c.Get(key); ok {
			if id, ok := value.(string); ok && streamObservationToken.MatchString(id) {
				return id
			}
		}
	}
	return "-"
}

func ObservePayloadEmitter(c *gin.Context) { PayloadObserver(c).Emitter() }
func finishPayloadAttempt(c *gin.Context, o *observability.Observer, err error, status int, before int) {
	outcome := "success"
	if err != nil {
		outcome = "handler_error"
		if errors.Is(err, context.Canceled) {
			outcome = "cancelled"
		} else if errors.Is(err, ErrUpstreamStreamFailure) {
			outcome = "upstream_terminal_failure"
		} else if errors.Is(err, ErrStreamReadFailure) {
			outcome = "upstream_read_error"
		} else if errors.Is(err, ErrEmptyStreamResponse) {
			outcome = "empty_stream"
		}
	} else if status < 200 || status >= 300 {
		outcome = "upstream_http_error"
	}
	written := c.Writer.Size()
	if written < 0 {
		written = 0
	}
	if before > 0 {
		written -= before
	}
	o.Finish(outcome, err != nil || status < 200 || status >= 300, c.Writer.Written(), written)
	if status >= 200 && status < 300 && (err != nil || o.Snapshot().Terminal == "response.failed" || o.Snapshot().Terminal == "response.incomplete" || o.Snapshot().Terminal == "error") {
		s := o.Snapshot()
		terminal := s.Terminal
		if terminal == "" {
			terminal = "-"
		}
		readError := s.ReadError
		if readError == "" {
			readError = "-"
		}
		RequestLogf(c, "[StreamAttemptOutcome] correlation=%s attempt=%s channel=%s upstreamTerminal=%s readError=%s downstreamCommitted=%t clientWriteBytes=%d outcome=%s",
			streamObservationID(c, "ccx.request_correlation_id"), streamObservationID(c, "ccx.stream_observation_attempt"),
			streamObservationID(c, "ccx.stream_observation_channel"), terminal, readError, c.Writer.Written(), written, s.Outcome)
	}
}

// RecordPayloadAttemptSummary supports diagnostic callers without consuming live bodies.
// Production uses startPayloadAttempt and emits only after the attempt terminates.
func RecordPayloadAttemptSummary(c *gin.Context, envCfg *config.EnvConfig, ctx observability.Context, _ []byte, req *http.Request) {
	req, o := startPayloadAttempt(c, envCfg, ctx, req)
	if o == nil {
		return
	}
	// Ingress length is preserved independently of the rewritten attempt body.
	req = req.WithContext(observability.WithClientBytes(req.Context(), observability.ClientBytes(c.Request.Context())))
	o.Request(req)
	if !observability.ShouldRecord(envCfg.PayloadCaptureMode, false) {
		return
	}
	s := o.Snapshot()
	RequestLogf(c, "[PayloadSummary] clientBodyBytes=%d outboundBodyBytes=%d contentLength=%d getBodyReplayBytes=%d replayError=%s", s.ClientBodyBytes, s.OutboundBodyBytes, s.ContentLength, s.GetBodyReplayBytes, s.ReplayError)
}
