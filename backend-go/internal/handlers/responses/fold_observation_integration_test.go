package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// Exercise the real provider conversion, SendRequest and observed transport.
func TestFoldContinuationObservationIntegration(t *testing.T) {
	for _, mode := range []string{"summary", "failed", "all"} {
		for _, failure := range []string{"http500", "send_error"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				var requestsMu sync.Mutex
				requests := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestsMu.Lock()
					requests++
					requestsMu.Unlock()
					_, _ = io.Copy(io.Discard, r.Body)
					if failure == "send_error" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.WriteHeader(500)
					_, _ = io.WriteString(w, "PRIVATE-UPSTREAM-ERROR")
				}))
				defer srv.Close()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				body := []byte(`{"model":"test","input":[],"stream":true}`)
				c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(body)))
				recorded := 0
				o := observability.NewObserver(mode, observability.Context{}, func(s observability.Summary) { recorded++ })
				c.Request = observability.Bind(c.Request, o)
				firstReq, _ := http.NewRequest("POST", srv.URL, strings.NewReader(string(body)))
				o.Request(firstReq)
				o.Response(200)
				reasoning := map[string]interface{}{"id": "reason", "type": "reasoning", "encrypted_content": "PRIVATE-ENCRYPTED", "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": "thinking"}}, "status": "completed", "role": "assistant", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "thinking"}}}
				stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "delta": "answer"}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp"}}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": reasoning}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.done", "output_index": 0, "item": reasoning}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp", "output": []interface{}{reasoning}, "usage": map[string]interface{}{"input_tokens": 1, "output_tokens": 518, "output_tokens_details": map[string]interface{}{"reasoning_tokens": 516}}, "status": "completed"}})
				first := responsesFoldTestResp(stream)
				_, err := handleFoldedResponsesStreamSuccess(c, first, &providers.ResponsesProvider{}, &config.UpstreamConfig{BaseURL: srv.URL, ServiceType: "responses"}, "test-key", &config.EnvConfig{PayloadCaptureMode: mode}, nil, &types.ResponsesRequest{}, body)
				if err != nil {
					t.Fatal(err)
				}
				o.Finish("success", false, c.Writer.Written(), recorder.Body.Len())
				s := o.Snapshot()
				requestsMu.Lock()
				gotRequests := requests
				requestsMu.Unlock()
				if gotRequests != 1 || s.TransportTries != 1 || recorded != 1 {
					t.Fatalf("missing real continuation or failed recording: requests=%d recorded=%d summary=%+v", gotRequests, recorded, s)
				}
				if s.Outcome == "success" || !s.SyntheticFailure || s.EventCounts["response.completed"] != 1 || s.EventCounts["response.incomplete"] != 0 || s.Terminal != "response.incomplete" {
					t.Fatalf("synthetic result corrupted raw observations: %+v", s)
				}
				if s.ContentLength != int64(len(body)) || s.GetBodyReplayBytes != len(body) {
					t.Fatalf("continuation overwrote first request metadata: %+v", s)
				}
				if failure == "http500" && (s.UpstreamStatus != 500 || s.FirstUpstreamStatus != 200) {
					t.Fatalf("lost HTTP failure: %+v", s)
				}
				if strings.Count(recorder.Body.String(), `"type":"response.incomplete"`) != 1 {
					t.Fatalf("missing client incomplete: %s", recorder.Body.String())
				}
				data, _ := observability.MarshalSummary(s)
				if strings.Contains(string(data), "PRIVATE") {
					t.Fatalf("payload leak: %s", data)
				}
				_ = common.PayloadObserver(c)
			})
		}
	}
}
