package responses

import (
	"bytes"
	"errors"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeFoldPayloadObservationFailedZeroClientBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"PRIVATE"}`))
	o := observability.NewObserver("summary", observability.NewContext("corr", "route-trace", "attempt", "ch", "responses", "responses", "test", ""), nil)
	c.Request = observability.Bind(c.Request, o)
	raw := "data: {\"type\":\"response.metadata\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"upstream_error\",\"message\":\"PRIVATE\"}}}\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(raw)), Request: c.Request}
	_, err := handleFoldedResponsesStreamSuccess(c, resp, &providers.ResponsesProvider{}, &config.UpstreamConfig{ServiceType: "responses"}, "test-key", &config.EnvConfig{PayloadCaptureMode: "summary"}, nil, &types.ResponsesRequest{}, []byte(`{"model":"test","input":"PRIVATE","stream":true}`))
	if !errors.Is(err, common.ErrEmptyStreamResponse) {
		t.Fatalf("err=%v", err)
	}
	if recorder.Body.Len() != 0 || c.Writer.Written() {
		t.Fatal("failed preflight wrote client bytes")
	}
	s := o.Snapshot()
	if s.EventCounts["response.failed"] != 1 || s.EventCounts["response.metadata"] != 1 || s.EmitterEvents == 0 || s.Terminal != "response.failed" {
		t.Fatalf("missing raw/emitter evidence: %+v", s)
	}
	data, _ := observability.MarshalSummary(s)
	if bytes.Contains(data, []byte("PRIVATE")) {
		t.Fatalf("payload leak: %s", data)
	}
}
