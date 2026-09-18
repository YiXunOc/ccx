package scheduler

import (
	"context"
	"fmt"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"testing"
	"time"
)

func boundAliasConfig() config.Config {
	const (
		accountUID = "bound-account"
		baseURL    = "https://binding.example"
	)
	u := config.UpstreamConfig{
		ChannelUID:        "native",
		LogicalChannelUID: "logical",
		AccountUID:        accountUID,
		Name:              "native",
		BaseURL:           baseURL,
		APIKeys:           []string{"test"},
		Status:            "active",
		ModelMapping:      map[string]string{"alias": "actual"},
	}
	v := *u.Clone()
	v.ChannelUID = "target"
	v.ServiceType = "openai"
	v.Name = "target"
	return config.Config{
		Upstream:     []config.UpstreamConfig{u},
		ChatUpstream: []config.UpstreamConfig{v},
		LogicalChannels: []config.LogicalChannel{{
			LogicalChannelUID:        "logical",
			AccountUID:               accountUID,
			BaseURLs:                 []string{baseURL},
			ProtocolModelPreferences: config.ProtocolModelPreferences{"chat": {"actual"}},
		}},
	}
}

func TestBoundProtocolCandidatesKeepClientProtocolIndependent(t *testing.T) {
	for _, tt := range []struct {
		name       string
		clientKind ChannelKind
		targetKind ChannelKind
	}{
		{name: "messages_to_chat", clientKind: ChannelKindMessages, targetKind: ChannelKindChat},
		{name: "messages_to_responses", clientKind: ChannelKindMessages, targetKind: ChannelKindResponses},
		{name: "chat_to_responses", clientKind: ChannelKindChat, targetKind: ChannelKindResponses},
		{name: "responses_to_chat", clientKind: ChannelKindResponses, targetKind: ChannelKindChat},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := boundAliasConfig()
			native := cfg.Upstream[0]
			target := cfg.ChatUpstream[0]
			target.ChannelUID = "target-" + string(tt.targetKind)
			cfg.LogicalChannels[0].ProtocolModelPreferences = config.ProtocolModelPreferences{string(tt.targetKind): {"actual"}}
			switch tt.clientKind {
			case ChannelKindChat:
				native.ChannelUID = "native-chat"
				cfg.ChatUpstream = []config.UpstreamConfig{native}
			case ChannelKindResponses:
				native.ChannelUID = "native-responses"
				cfg.ResponsesUpstream = []config.UpstreamConfig{native}
			}
			switch tt.targetKind {
			case ChannelKindChat:
				cfg.ChatUpstream = append(cfg.ChatUpstream[:0], target)
			case ChannelKindResponses:
				cfg.ResponsesUpstream = []config.UpstreamConfig{target}
			}

			s, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: tt.clientKind, Model: "alias"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Route.Kind != string(tt.targetKind) || result.ExecutionModel != "actual" {
				t.Fatalf("selected %#v, want %s target using actual model", result, tt.targetKind)
			}
		})
	}
}

func TestProtocolPreferencesSelectAlias(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			cfg := boundAliasConfig()
			if missing {
				cfg.ChatUpstream = nil
			}
			s, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias"})
			if missing {
				if err == nil {
					t.Fatalf("fell back: %#v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Route.Kind != "chat" || result.ExecutionModel != "actual" {
				t.Fatalf("wrong mapped route: %#v", result)
			}
		})
	}
}

func TestProtocolPreferencesCrossProtocolRuntimeKind(t *testing.T) {
	for _, mode := range []string{"promotion", "affinity", "manual"} {
		for _, cool := range []ChannelKind{ChannelKindMessages, ChannelKindChat} {
			t.Run(mode+string(cool), func(t *testing.T) {
				cfg := boundAliasConfig()
				if mode == "promotion" {
					until := time.Now().Add(time.Hour)
					cfg.ChatUpstream[0].PromotionUntil = &until
				}
				s, cleanup := createTestScheduler(t, cfg)
				defer cleanup()
				if mode == "affinity" {
					first, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias", UserID: "user"})
					if err != nil {
						t.Fatal(err)
					}
					s.traceAffinity.SetPreferredRoute("messages:user", first.Route)
				}
				if mode == "manual" {
					s.overrideManager = conversation.NewOverrideManager(time.Hour)
					defer s.overrideManager.Stop()
					if err := s.overrideManager.SetOverride("conversation", "messages", "user", []conversation.ChannelEntry{{ChannelIndex: 0}}, time.Hour); err != nil {
						t.Fatal(err)
					}
				}
				s.MarkChannelCooldown(cool, 0, time.Minute)
				result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias", UserID: "user"})
				if cool == ChannelKindChat {
					if err == nil {
						t.Fatalf("selected cooled target: %#v", result)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				expected := "promotion_priority"
				if mode == "manual" {
					expected = "manual_override"
				}
				if mode == "affinity" {
					expected = "trace_affinity"
				}
				if result.Route.Kind != "chat" || result.Reason != expected {
					t.Fatalf("used request runtime kind: %#v", result)
				}
			})
		}
	}
}

func TestBoundProtocolDeferredFallbackUsesExecutionRuntime(t *testing.T) {
	for _, mode := range []string{"pressure", "quota"} {
		t.Run(mode, func(t *testing.T) {
			cfg := boundAliasConfig()
			if mode == "pressure" {
				cfg.ChatUpstream[0].RateLimitRPM = 4
				cfg.ChatUpstream[0].RateLimitWindowMinutes = 1
			}
			s, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			if mode == "pressure" {
				limiter := s.GetRateLimitManager().GetOrCreate("Chat", 0, ratelimit.Config{RPM: 4, WindowSeconds: 60})
				for i := 0; i < 2; i++ {
					release, err := limiter.Acquire(context.Background(), time.Millisecond, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					release()
				}
			} else {
				qm := quota.NewManager()
				limit, remaining := 10000.0, 500.0
				qm.UpdateChannelProviderAPI("target", "account", []quota.Value{{Dimension: quota.DimTokens, Limit: &limit, Remaining: &remaining}}, nil)
				s.SetQuotaManager(qm)
			}
			s.MarkChannelCooldown(ChannelKindMessages, 0, time.Minute)
			result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias"})
			if err != nil {
				t.Fatal(err)
			}
			want := "rate_limit_pressure"
			if mode == "quota" {
				want = "quota_saturated_fallback"
			}
			if result.Route.Kind != "chat" || result.Reason != want {
				t.Fatalf("wrong fallback branch: %#v", result)
			}
		})
	}
}

func TestManualOverridePreservesSameIndexAcrossProtocols(t *testing.T) {
	channels := []ChannelInfo{{Index: 0, Route: ChannelRouteRef{Kind: "chat", Index: 0}}, {Index: 0, Route: ChannelRouteRef{Kind: "messages", Index: 0}}}
	got := applyManualOverrideOrder(channels, []conversation.ChannelEntry{{ChannelIndex: 0}}, ChannelKindMessages)
	if len(got) != 2 || got[0].Route.Kind != "messages" || got[1].Route.Kind != "chat" {
		t.Fatalf("lost or confused routes: %#v", got)
	}
}

func TestProtocolPreferencesScopedCandidates(t *testing.T) {
	cfg := config.Config{
		LogicalChannels: []config.LogicalChannel{{LogicalChannelUID: "one", ProtocolModelPreferences: config.ProtocolModelPreferences{"chat": {"model"}}}, {LogicalChannelUID: "two"}},
		Upstream:        []config.UpstreamConfig{{ChannelUID: "native", LogicalChannelUID: "one"}, {ChannelUID: "other", LogicalChannelUID: "two"}},
		ChatUpstream:    []config.UpstreamConfig{{ChannelUID: "bound", LogicalChannelUID: "one"}},
	}
	for _, tt := range []struct {
		name, kind, uid, model, actual string
		want                           bool
	}{
		{"bound endpoint", "chat", "bound", "model", "", true},
		{"wrong endpoint", "messages", "native", "model", "", false},
		{"other upstream independent", "messages", "other", "model", "", true},
		{"unbound model", "messages", "native", "free", "", true},
		{"redirect cannot bypass", "messages", "native", "free", "model", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := protocolPreferenceAllows(&cfg, ChannelRouteRef{Kind: tt.kind, ChannelUID: tt.uid}, tt.model, tt.actual)
			if got != tt.want {
				t.Fatalf("allows=%v want %v", got, tt.want)
			}
		})
	}
	candidates := []ChannelInfo{{Route: ChannelRouteRef{Kind: "messages", ChannelUID: "native"}}}
	if got := filterProtocolPreferences(&cfg, candidates, ChannelKindMessages, "model"); len(got) != 0 {
		t.Fatal("unavailable bound protocol must not fall back")
	}
}
