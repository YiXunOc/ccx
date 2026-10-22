package common

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestManualMappedTargetRejectedByUpstreamRetriesNextKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		seen = append(seen, body.Model)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Test-Key") == "sk-a" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request_error","message":"Unsupported model B"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	cm, sch, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name: "manual-rejected", ChannelUID: "ch-manual-rejected", BaseURL: server.URL, APIKeys: []string{"sk-a", "sk-b"}, Status: "active", ServiceType: "openai", ModelMapping: map[string]string{"A": "B"},
	})
	defer cleanup()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	original := []byte(`{"model":"A","messages":[]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(original))
	cfg := cm.GetConfig()
	up := &cfg.Upstream[0]
	handled, key, _, _, _, lastErr := TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cm, sch, scheduler.ChannelKindMessages, "Messages", mm, up,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}}, original, nil, false,
		func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
			return cm.GetNextAPIKey(up, failed, "Messages")
		},
		func(c *gin.Context, copy *config.UpstreamConfig, key string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, copy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
			if err == nil {
				req.Header.Set("X-Test-Key", key)
			}
			return req, err
		}, func(string) {}, nil, nil,
		func(c *gin.Context, resp *http.Response, copy *config.UpstreamConfig, key string, body []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		}, "A", "", 0, sch.GetChannelLogStore(scheduler.ChannelKindMessages))
	if !handled || key != "sk-b" || lastErr != nil || len(seen) != 2 || seen[0] != "B" || seen[1] != "B" {
		t.Fatalf("handled=%v key=%s last=%v seen=%v", handled, key, lastErr, seen)
	}
}

func TestManualMappedModelNotFoundAllowsFailoverPastInvalidRequest(t *testing.T) {
	body := []byte(`{"error":{"code":"invalid_request_error","message":"Unsupported model B"}}`)
	if !manualTargetRejected(http.StatusBadRequest, body) {
		t.Fatal("mapped B rejection must be eligible for failover")
	}
	if manualTargetRejected(http.StatusBadRequest, []byte(`{"error":{"code":"invalid_request_error","message":"messages must not be empty"}}`)) {
		t.Fatal("unrelated invalid request must remain terminal")
	}
}

func TestManualMappingFailoverSkipsKnownUnsupportedKeyAndNoAutoOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requests <- payload.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	cm, sch, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name: "manual-map-test", ChannelUID: "ch-manual-test", BaseURL: server.URL, APIKeys: []string{"sk-a", "sk-b"}, Status: "active", ServiceType: "openai", ModelMapping: map[string]string{"A": "B", "B": "C"}, SupportedModels: []string{"A"},
	})
	defer cleanup()
	old := manualModelAvailable
	manualModelAvailable = func(_, _, key, target string) bool { return key != "sk-a" && target == "B" }
	defer func() { manualModelAvailable = old }()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	original := []byte(`{"model":"A","messages":[]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(original))
	cfg := cm.GetConfig()
	up := &cfg.Upstream[0]
	policy := &autopilot.EndpointAttemptPolicy{ResolvedTargetForBinding: func(_, _, _ string) (*autopilot.ResolvedRouteTarget, string) {
		return &autopilot.ResolvedRouteTarget{Model: "C"}, ""
	}}
	handled, key, _, failErr, _, lastErr := TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cm, sch, scheduler.ChannelKindMessages, "Messages", mm, up, []warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}}, original, nil, false,
		func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
			return cm.GetNextAPIKey(up, failed, "Messages")
		},
		func(c *gin.Context, copy *config.UpstreamConfig, key string) (*http.Request, error) {
			if got := string(GetEffectiveRequestBody(c, nil)); got != `{"model":"B","messages":[]}` {
				t.Errorf("provider conversion received %s, want mapped B", got)
			}
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, copy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
		},
		func(string) {}, nil, nil, func(c *gin.Context, resp *http.Response, copy *config.UpstreamConfig, key string, body []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"A", "", 0, sch.GetChannelLogStore(scheduler.ChannelKindMessages), WithEndpointAttemptPolicy(policy))
	if !handled || key != "sk-b" || failErr != nil || lastErr != nil {
		t.Fatalf("handled=%v key=%q fail=%v err=%v", handled, key, failErr, lastErr)
	}
	if got := <-requests; got != "B" {
		t.Fatalf("upstream received %q, want B", got)
	}
	select {
	case extra := <-requests:
		t.Fatalf("unexpected extra request: %q", extra)
	default:
	}
	if got := c.GetString("mappingSource"); got != "manual_mapping" {
		t.Fatalf("source=%q", got)
	}

	// A known-unsupported B on every key must not fall back to A or chain to C.
	manualModelAvailable = func(_, _, _, _ string) bool { return false }
	blockedRecorder := httptest.NewRecorder()
	blocked, _ := gin.CreateTestContext(blockedRecorder)
	blocked.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(original))
	handled, _, _, _, _, _ = TryUpstreamWithAllKeys(blocked, config.NewEnvConfig(), cm, sch, scheduler.ChannelKindMessages, "Messages", mm, up,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}}, original, nil, false,
		func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
			return cm.GetNextAPIKey(up, failed, "Messages")
		},
		func(c *gin.Context, copy *config.UpstreamConfig, key string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, copy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
		}, func(string) {}, nil, nil,
		func(c *gin.Context, resp *http.Response, copy *config.UpstreamConfig, key string, body []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		}, "A", "", 0, sch.GetChannelLogStore(scheduler.ChannelKindMessages), WithEndpointAttemptPolicy(policy))
	if handled {
		t.Fatal("known-unsupported B was forwarded")
	}
	select {
	case extra := <-requests:
		t.Fatalf("known-unsupported target leaked upstream model %q", extra)
	default:
	}
}
