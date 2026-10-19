package common

import (
	"bytes"
	"errors"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObservedSSEBodyReadAndTerminal(t *testing.T) {
	o := observability.NewObserver("summary", observability.Context{}, nil)
	body := ObserveUpstreamSSEBody(io.NopCloser(strings.NewReader("event: error\ndata: {\"type\":\"error\",\"message\":\"PRIVATE\"}\n\n")), o)
	if _, err := io.Copy(io.Discard, body); err != nil {
		t.Fatal(err)
	}
	s := o.Snapshot()
	if s.Terminal != "error" || s.EventCounts["error"] != 1 || !s.EOF {
		t.Fatalf("missing upstream evidence: %+v", s)
	}
}

func TestDownstreamTerminalObservationIndependentOfUpstream(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	o := observability.NewObserver("summary", observability.Context{}, nil)
	c.Writer = ObserveDownstreamSSEWriter(c.Writer, o)
	_, _ = c.Writer.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	s := o.Snapshot()
	if s.Terminal != "" || s.EventCounts["response.completed"] != 0 || s.DownstreamTerminal != "done" || s.DownstreamEventCounts["response.completed"] != 1 || w.Body.Len() == 0 {
		t.Fatalf("upstream counts duplicated or downstream missing: %+v", s)
	}
}

func TestStreamAttemptTerminalObservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Set("ccx.request_correlation_id", "corr-123")
	c.Set("ccx.stream_observation_attempt", "attempt-123")
	c.Set("ccx.stream_observation_channel", "channel-123")
	o := observability.NewObserver("summary", observability.Context{}, nil)
	c.Request = observability.Bind(c.Request, o)
	o.Data(`{"type":"response.failed","response":{"error":{"message":"PRIVATE"}}}`)
	o.ReadEnd(io.ErrUnexpectedEOF)
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	finishPayloadAttempt(c, o, ErrUpstreamStreamFailure, 200, 0)
	got := out.String()
	for _, want := range []string{"[StreamAttemptOutcome]", "correlation=corr-123", "attempt=attempt-123", "channel=channel-123", "upstreamTerminal=response.failed", "readError=unexpected_eof", "downstreamCommitted=false", "outcome=upstream_terminal_failure"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "PRIVATE") {
		t.Fatalf("raw payload leaked: %s", got)
	}
}

func TestPayloadSummaryHonestBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader("client-original"))
	_, err := ReadRequestBody(c, 1024)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", "http://local.test", strings.NewReader(strings.Repeat("x", 1024*1024)))
	req.ContentLength = 17
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	RecordPayloadAttemptSummary(c, &config.EnvConfig{PayloadCaptureMode: "summary"}, observability.NewContext("corr", "rt", "attempt", "ch", "chat", "chat", "", ""), []byte("compressed"), req)
	text := out.String()
	for _, want := range []string{"clientBodyBytes=15", "outboundBodyBytes=-1", "getBodyReplayBytes=1048576"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s: %s", want, text)
		}
	}
	body, _ := io.ReadAll(req.Body)
	if len(body) != 1024*1024 {
		t.Fatal("real request body consumed")
	}
}

func TestPayloadSummaryReplayReadFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	req, _ := http.NewRequest("POST", "http://local.test", nil)
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("PRIVATE prompt") }
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	RecordPayloadAttemptSummary(c, &config.EnvConfig{PayloadCaptureMode: "summary"}, observability.NewContext("corr", "rt", "a", "ch", "chat", "chat", "", ""), nil, req)
	if !strings.Contains(out.String(), "replayError=get_body_error") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("unsafe/missing replay error: %s", out.String())
	}
}
