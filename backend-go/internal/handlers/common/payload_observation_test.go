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
