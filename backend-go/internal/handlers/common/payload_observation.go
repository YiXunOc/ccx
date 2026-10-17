package common

import (
	"context"
	"errors"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/gin-gonic/gin"
	"net/http"
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
func ObservePayloadEmitter(c *gin.Context) { PayloadObserver(c).Emitter() }
func finishPayloadAttempt(c *gin.Context, o *observability.Observer, err error, status int, before int) {
	outcome := "success"
	if err != nil {
		outcome = "handler_error"
		if errors.Is(err, context.Canceled) {
			outcome = "cancelled"
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
