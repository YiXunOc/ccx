package responses

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestResponsesStreamFailureObservation(t *testing.T) {
	cases := []struct {
		name, terminal, wantPhase, wantType, wantReason string
		wantLog                                         bool
	}{
		{"upstream failed after commit", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"PRIVATE_MESSAGE"},"output":[]}}`, "postcommit", "response.failed", "server_error", true},
		{"upstream incomplete after commit", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`, "postcommit", "response.incomplete", "max_output_tokens", true},
		{"successful completion", `{"type":"response.completed","response":{"status":"completed","output":[]}}`, "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set("ccx.request_correlation_id", "corr-test-123")
			c.Set("ccx.stream_observation_attempt", "attempt-123")
			c.Set("ccx.stream_observation_channel", "ch_123")
			common.SetRequestLogContext(c, "session-private-987c", 24)
			stream := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"VISIBLE_SECRET\"}\n\n" + "event: " + strings.Split(tc.terminal, `"`)[3] + "\ndata: " + tc.terminal + "\n\n"
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
			var logs strings.Builder
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{})
			if err != nil {
				t.Fatal(err)
			}
			body := writer.Body.String()
			if !strings.Contains(body, "VISIBLE_SECRET") || (!strings.Contains(body, tc.terminal) && (tc.wantLog || !strings.Contains(body, `"type":"response.completed"`))) {
				t.Fatalf("stream changed: %q", body)
			}
			got := logs.String()
			if !tc.wantLog {
				if strings.Contains(got, "Responses-StreamOutcome") {
					t.Fatalf("success logged: %s", got)
				}
				return
			}
			for _, part := range []string{"[Responses-StreamOutcome]", "correlation=corr-test-123", "attempt=attempt-123", "channel=ch_123", "phase=" + tc.wantPhase, "terminal=" + tc.wantType, tc.wantReason, "errorPresent=", "incompletePresent=", "headerCommitted=true", "write=writer_accepted", "response.output_text.delta:1"} {
				if !strings.Contains(got, part) {
					t.Fatalf("missing %q from %s", part, got)
				}
			}
			for _, secret := range []string{"PRIVATE_MESSAGE", "VISIBLE_SECRET", "session-private-987c"} {
				if strings.Contains(got, secret) {
					t.Fatalf("leaked %q in %s", secret, got)
				}
			}
		})
	}
}

func TestResponsesStreamFailureObservationPreflightEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("ccx.request_correlation_id", "corr-preflight")
	common.SetRequestLogContext(c, "session-private-987c", 24)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.metadata\ndata: {\"type\":\"response.metadata\"}\n\n"))}
	var logs strings.Builder
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{})
	if !errors.Is(err, common.ErrEmptyStreamResponse) || w.Body.Len() != 0 {
		t.Fatalf("preflight changed: err=%v bytes=%d", err, w.Body.Len())
	}
	if !strings.Contains(logs.String(), "correlation=corr-preflight attempt=- channel=- phase=preflight headerCommitted=false terminal=empty eventCounts=response.metadata:1") {
		t.Fatalf("missing preflight observation: %s", logs.String())
	}
}

func TestResponsesStreamFailureObservationMissingTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("ccx.request_correlation_id", "corr-eof")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"VISIBLE_SECRET\"}\n\n"))}
	var logs strings.Builder
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{})
	if err != nil || !strings.Contains(w.Body.String(), "response.incomplete") {
		t.Fatalf("incomplete delivery changed: err=%v body=%q", err, w.Body.String())
	}
	if !strings.Contains(logs.String(), "correlation=corr-eof attempt=- channel=- phase=postcommit headerCommitted=true terminal=missing reason=upstream_eof eventCounts=response.output_text.delta:1") || strings.Contains(logs.String(), "VISIBLE_SECRET") {
		t.Fatalf("missing or unsafe observation: %s", logs.String())
	}
}

func TestResponsesFailureObservationDoesNotLogRawUntrustedFields(t *testing.T) {
	event := `event: response.failed
data: {"type":"response.failed","response":{"status":"failed","error":{"code":"secret_key=ABC","message":"PRIVATE_MESSAGE"},"incomplete_details":{"reason":"PRIVATE_REASON"}}}

`
	got := responsesFailureEventSummary(event)
	for _, secret := range []string{"ABC", "PRIVATE_MESSAGE", "PRIVATE_REASON"} {
		if strings.Contains(got, secret) {
			t.Fatalf("leaked %q in %s", secret, got)
		}
	}
	for _, raw := range []string{
		`{"type":"response.failed","response":{"status":{"unexpected":true},"error":{"code":{"unexpected":true}}}}`,
		`{"type":"response.incomplete","response":{"incomplete_details":{"reason":["SECRET"]}}}`,
	} {
		summary := responsesFailureEventSummary("data: " + raw + "\n\n")
		if !strings.Contains(summary, "other") || strings.Contains(summary, "SECRET") {
			t.Fatalf("unexpected malformed field summary: %s", summary)
		}
	}
}

func TestResponsesObservationUsesSSENameWhenDataTypeMissing(t *testing.T) {
	event := "event: response.failed\ndata: {\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"SECRET\"}}}\n\n"
	if got := responsesFailureEventSummary(event); !strings.Contains(got, "terminal=response.failed") || strings.Contains(got, "SECRET") {
		t.Fatalf("failed event name missed: %s", got)
	}
	counts := make(map[string]int)
	responsesObservationCountEvent(event, counts)
	if counts["response.failed"] != 1 {
		t.Fatalf("failed event count missed: %v", counts)
	}
}

func TestResponsesStreamFailureObservationNoRawPreflightSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stream := "event: response.metadata\ndata: {\"type\":\"response.metadata\",\"secret\":\"PRIVATE_PREVIEW\"}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
	var logs strings.Builder
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{})
	if !errors.Is(err, common.ErrEmptyStreamResponse) || w.Body.Len() != 0 {
		t.Fatalf("preflight behavior changed: err=%v bytes=%d", err, w.Body.Len())
	}
	if strings.Contains(logs.String(), "PRIVATE_PREVIEW") || strings.Contains(logs.String(), "Responses-EmptyResponse-Raw") {
		t.Fatalf("raw preflight SSE leaked: %s", logs.String())
	}
}

func TestResponsesStreamObservationPostCommitTimeoutLoggedOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reader, writer := io.Pipe()
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
	var logs strings.Builder
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	var sent sync.WaitGroup
	sent.Add(1)
	go func() {
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"VISIBLE_SECRET\"}\n\n")
		sent.Done()
		defer writer.Close()
		<-time.After(120 * time.Millisecond)
	}()
	_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{InactivityTimeoutMs: 30, FirstContentTimeoutMs: 1000})
	sent.Wait()
	if !errors.Is(err, common.ErrStreamPostCommitStalled) || !strings.Contains(w.Body.String(), "response.incomplete") {
		t.Fatalf("timeout behavior changed: err=%v body=%q", err, w.Body.String())
	}
	if got := strings.Count(logs.String(), "[Responses-StreamOutcome]"); got != 1 || !strings.Contains(logs.String(), "terminal=timeout reason=stream_stalled") || strings.Contains(logs.String(), "response.incomplete:1") {
		t.Fatalf("timeout observations duplicated or synthesized event counted: %s", logs.String())
	}
}

func TestResponsesStreamObservationRecoveredChatIsNotFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
	var logs strings.Builder
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	_, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{LogLevel: "info"}, nil, time.Now(), &types.ResponsesRequest{Model: "test-model", Stream: true}, []byte(`{"model":"test-model","stream":true}`), common.StreamPreflightTimeouts{})
	if err != nil || !strings.Contains(w.Body.String(), "response.completed") || strings.Contains(logs.String(), "[Responses-StreamOutcome]") {
		t.Fatalf("recovered chat marked failed: err=%v log=%s", err, logs.String())
	}
}
