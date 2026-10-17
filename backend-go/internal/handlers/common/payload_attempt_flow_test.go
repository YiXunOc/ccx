package common

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type payloadLogBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *payloadLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}
func (b *payloadLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.text.String() }
func payloadRecords(text string) []observability.Summary {
	var result []observability.Summary
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, "[PayloadSummary] ")
		if i < 0 {
			continue
		}
		var s observability.Summary
		if json.Unmarshal([]byte(line[i+len("[PayloadSummary] "):]), &s) == nil {
			result = append(result, s)
		}
	}
	return result
}
func TestPayloadAttemptFailoverModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"off", "summary", "failed", "all"} {
		t.Run(mode, func(t *testing.T) {
			var received []string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				received = append(received, string(body))
				mu.Unlock()
				if r.URL.Path == "/bad" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":"PRIVATE"}`))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			cfgManager, sched, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{Name: "capture", ChannelUID: "ch-capture", BaseURL: server.URL, APIKeys: []string{"capture-key"}, Status: "active", ServiceType: "openai"})
			defer cleanup()
			var output payloadLogBuffer
			old := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(old)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"test","input":"PRIVATE"}`
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			if _, err := ReadRequestBody(c, 1024); err != nil {
				t.Fatal(err)
			}
			corr := EnsureRequestCorrelation(c)
			c.Set("ccx.autopilot_trace_uid", "route-trace")
			env := config.NewEnvConfig()
			env.PayloadCaptureMode = mode
			env.EnableRequestLogs = false
			env.EnableResponseLogs = false
			upstream := &cfgManager.GetConfig().Upstream[0]
			handled, _, _, _, _, err := TryUpstreamWithAllKeys(c, env, cfgManager, sched, scheduler.ChannelKindResponses, "Responses", mm, upstream, []warmup.URLLatencyResult{{URL: server.URL + "/bad"}, {URL: server.URL + "/good"}}, []byte(body), nil, false,
				func(u *config.UpstreamConfig, failed map[string]bool) (string, error) {
					return cfgManager.GetNextAPIKey(u, failed, "Responses")
				},
				func(c *gin.Context, u *config.UpstreamConfig, key string) (*http.Request, error) {
					req, e := http.NewRequestWithContext(c.Request.Context(), "POST", u.BaseURL, strings.NewReader(body))
					return observability.WithConvertedBytes(req, []byte(body)), e
				}, nil, nil, nil,
				func(c *gin.Context, r *http.Response, u *config.UpstreamConfig, key string, body []byte) (*types.Usage, error) {
					defer r.Body.Close()
					data, e := io.ReadAll(r.Body)
					c.Data(200, "application/json", data)
					return nil, e
				}, "test", "", 0, sched.GetChannelLogStore(scheduler.ChannelKindResponses))
			if err != nil || !handled {
				t.Fatalf("handled=%t err=%v", handled, err)
			}
			mu.Lock()
			got := append([]string(nil), received...)
			mu.Unlock()
			if len(got) != 2 || got[0] != body || got[1] != body {
				t.Fatalf("request changed: %q", got)
			}
			records := payloadRecords(output.String())
			want := 2
			if mode == "off" {
				want = 0
			} else if mode == "failed" {
				want = 1
			}
			if len(records) != want {
				t.Fatalf("records=%d want=%d logs=%s", len(records), want, output.String())
			}
			for _, s := range records {
				if s.Context.CorrelationID != corr || s.Context.AttemptID == "" || s.ClientBodyBytes != len(body) || s.OutboundBodyBytes != -1 || s.TransportTries != 1 || s.GetBodyReplayBytes != len(body) {
					t.Fatalf("invalid summary: %+v", s)
				}
			}
			if len(records) == 2 && (records[0].Context.AttemptID == records[1].Context.AttemptID || records[1].Outcome != "success") {
				t.Fatal("attempt identity/outcome lost")
			}
		})
	}
}
