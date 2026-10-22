package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestGroupModelPatternRawFallback(t *testing.T) {
	for _, policy := range []*autopilot.EndpointAttemptPolicy{nil, {}} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("policy=%v/all=%v", policy != nil, all), func(t *testing.T) {
				up := &config.UpstreamConfig{APIKeys: []string{"a", "b"}, DisabledGroupModels: []config.DisabledGroupModelInfo{{Key: "a", Model: " GPT-* "}}}
				if all {
					up.DisabledGroupModels = append(up.DisabledGroupModels, config.DisabledGroupModelInfo{Key: "b", Model: "*"})
				}
				if keypool.HasEffectiveConfig(up) {
					t.Fatal("test must exercise raw APIKeys path")
				}
				fallback := func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
					for _, k := range up.APIKeys {
						if !failed[k] {
							return k, nil
						}
					}
					return "", fmt.Errorf("no keys")
				}
				_, key, err := selectAttemptAPIKeyFiltered(nil, scheduler.ChannelKindMessages, 0, up, "", nil, nil, "gpt-5", fallback, policy, "Messages", nil, nil, nil, "")
				if all {
					if err == nil || key != "" {
						t.Fatal("all excluded keys escaped fallback")
					}
				} else if err != nil || key != "b" {
					t.Fatalf("key=%q err=%v", key, err)
				}
			})
		}
	}
}

func TestGroupModelPatternMappedCrossProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, cross := range []bool{false, true} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("cross=%v/all=%v", cross, all), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var body struct{ Model string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Model != "glm-5.2" {
						t.Errorf("actual model=%q", body.Model)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				}))
				defer server.Close()
				rules := []config.DisabledGroupModelInfo{{Key: "sk-a", Model: " GLM-* "}}
				if all {
					rules = append(rules, config.DisabledGroupModelInfo{Key: "sk-b", Model: "*5.2"})
				}
				cm, cs, mm, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{Name: "manual-pattern", ChannelUID: "ch-manual-pattern", BaseURL: server.URL, APIKeys: []string{"sk-a", "sk-b"}, Status: "active", ServiceType: "openai", AutoManaged: true})
				defer cleanup()
				for _, rule := range rules {
					if _, _, err := cm.DisableGroupModel("Messages", 0, rule.Key, rule.Model); err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				body := []byte(`{"model":"claude-opus-4-8","messages":[]}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				cfg := cm.GetConfig()
				up := &cfg.Upstream[0]
				policy := &autopilot.EndpointAttemptPolicy{ResolvedTargetForBinding: func(string, string, string) (*autopilot.ResolvedRouteTarget, string) {
					return &autopilot.ResolvedRouteTarget{Model: "glm-5.2"}, ""
				}}
				opts := []TryUpstreamOption{WithEndpointAttemptPolicy(policy)}
				if cross {
					opts = append(opts, WithExecutionRoute(scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindChat), Index: 0, ChannelUID: up.ChannelUID}))
				}
				handled, key, _, failErr, _, lastErr := TryUpstreamWithAllKeys(c, config.NewEnvConfig(), cm, cs, scheduler.ChannelKindMessages, "Messages", mm, up, []warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}}, body, nil, false,
					func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
						return cm.GetNextAPIKey(up, failed, "Messages")
					},
					func(c *gin.Context, copy *config.UpstreamConfig, key string) (*http.Request, error) {
						return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, copy.BaseURL, bytes.NewReader(GetEffectiveRequestBody(c, nil)))
					},
					func(string) {}, nil, nil, func(c *gin.Context, r *http.Response, up *config.UpstreamConfig, key string, b []byte) (*types.Usage, error) {
						_ = r.Body.Close()
						return nil, nil
					}, "claude-opus-4-8", "", 0, cs.GetChannelLogStore(scheduler.ChannelKindMessages), opts...)
				if all {
					if calls.Load() != 0 || handled || key != "" {
						t.Fatalf("all banned: calls=%d handled=%v key=%q", calls.Load(), handled, key)
					}
				} else if !handled || key != "sk-b" || calls.Load() != 1 || failErr != nil || lastErr != nil {
					t.Fatalf("calls=%d handled=%v key=%q fail=%v last=%v", calls.Load(), handled, key, failErr, lastErr)
				}
			})
		}
	}
}
