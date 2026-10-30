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

func setBoundAliasResolver(s *ChannelScheduler) {
	s.SetModelSupportResolverProvider(func(_ context.Context, _ ChannelKind, _ *config.UpstreamConfig, model string) (bool, string, string, string) {
		if model == "alias" {
			return true, "actual", "test_resolver", ""
		}
		return true, model, "test_resolver", ""
	})
}

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
			ProtocolModelPreferences: config.ProtocolModelPreferences{"chat": {"actual"}, "responses": {"actual"}},
		}},
	}
}

func TestChatRequestSelectsBoundMessagesSibling(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name: "high-priority-messages", ChannelUID: "messages-high", LogicalChannelUID: "logical-chat-messages",
			AccountUID: "account-chat-messages", BaseURL: "https://messages.example", APIKeys: []string{"key"}, Status: "active",
			Priority: 1,
		}},
		ChatUpstream: []config.UpstreamConfig{{
			Name: "low-priority-chat", ChannelUID: "chat-low", LogicalChannelUID: "logical-chat-messages",
			AccountUID: "account-chat-messages", BaseURL: "https://chat.example", APIKeys: []string{"key"}, Status: "active",
			Priority: 100,
		}},
		LogicalChannels: []config.LogicalChannel{{
			LogicalChannelUID: "logical-chat-messages", AccountUID: "account-chat-messages",
			BaseURLs:                 []string{"https://messages.example"},
			ProtocolModelPreferences: config.ProtocolModelPreferences{"messages": {"actual"}},
		}},
	}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	setBoundAliasResolver(s)
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindChat, Model: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route.Kind != string(ChannelKindMessages) || result.Upstream.ChannelUID != "messages-high" {
		t.Fatalf("selected route = %#v, want high-priority messages sibling", result)
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
			setBoundAliasResolver(s)
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
			setBoundAliasResolver(s)
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
				setBoundAliasResolver(s)
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
			setBoundAliasResolver(s)
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

func TestBoundSingleEndpointDoesNotRequireClientRoute(t *testing.T) {
	for _, target := range []ChannelKind{ChannelKindMessages, ChannelKindResponses} {
		t.Run(string(target), func(t *testing.T) {
			cfg := boundAliasConfig()
			u := cfg.Upstream[0]
			u.ServiceType = "claude"
			cfg.Upstream, cfg.ChatUpstream = nil, nil
			if target == ChannelKindMessages {
				cfg.Upstream = []config.UpstreamConfig{u}
			} else {
				u.ServiceType = "responses"
				cfg.ResponsesUpstream = []config.UpstreamConfig{u}
			}
			cfg.LogicalChannels[0].ProtocolModelPreferences = config.ProtocolModelPreferences{string(target): {"actual"}}
			scheduler, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			setBoundAliasResolver(scheduler)
			result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindChat, Model: "alias"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Route.Kind != string(target) || result.ExecutionModel != "actual" {
				t.Fatalf("wrong selection: %#v", result)
			}
		})
	}
}

func TestBoundCandidatesPreserveExistingFilters(t *testing.T) {
	for _, mode := range []string{"eligible", "unbound", "disabled", "no-key", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			cfg := boundAliasConfig()
			cfg.Upstream = nil
			cfg.LogicalChannels[0].ProtocolModelPreferences = config.ProtocolModelPreferences{"chat": {"actual"}}
			if mode == "unbound" {
				cfg.LogicalChannels[0].ProtocolModelPreferences = nil
			}
			if mode == "disabled" {
				cfg.ChatUpstream[0].Status = "disabled"
			}
			if mode == "no-key" {
				cfg.ChatUpstream[0].APIKeys = nil
			}
			scheduler, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			scheduler.SetModelSupportResolverProvider(func(_ context.Context, _ ChannelKind, _ *config.UpstreamConfig, _ string) (bool, string, string, string) {
				return mode != "unsupported", "actual", "test", ""
			})
			snapshot := scheduler.configManager.GetConfig()
			got := scheduler.addBoundProtocolCandidates(context.Background(), &snapshot, ChannelKindMessages, "alias", nil, nil)
			want := 0
			if mode == "eligible" {
				want = 1
			}
			if len(got) != want {
				t.Fatalf("candidates=%#v, want count %d", got, want)
			}
		})
	}
}

func TestProtocolPreferencesRequestActualIntersection(t *testing.T) {
	for _, tt := range []struct {
		name        string
		preferences config.ProtocolModelPreferences
		want        bool
	}{
		{"both automatic", nil, true},
		{"request automatic", config.ProtocolModelPreferences{"chat": {"actual"}}, true},
		{"actual automatic", config.ProtocolModelPreferences{"chat": {"alias"}}, true},
		{"overlap", config.ProtocolModelPreferences{"chat": {"alias", "actual"}, "responses": {"alias"}, "messages": {"actual"}}, true},
		{"disjoint", config.ProtocolModelPreferences{"chat": {"alias"}, "responses": {"actual"}}, false},
		{"actual denies", config.ProtocolModelPreferences{"chat": {"alias"}, "messages": {"actual"}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := boundAliasConfig()
			cfg.LogicalChannels[0].ProtocolModelPreferences = tt.preferences
			for i := 0; i < 100; i++ {
				if got := protocolPreferenceAllows(&cfg, ChannelRouteRef{Kind: "chat", ChannelUID: "target"}, "alias", "actual"); got != tt.want {
					t.Fatalf("allows=%v want %v", got, tt.want)
				}
			}
		})
	}
}

func TestMultiBoundCandidatesPriorityAndFailover(t *testing.T) {
	cfg := boundAliasConfig()
	response := *cfg.ChatUpstream[0].Clone()
	response.ChannelUID = "response"
	response.ServiceType = "responses"
	response.Priority = 1
	cfg.ChatUpstream[0].Priority = 9
	cfg.ResponsesUpstream = []config.UpstreamConfig{response}
	cfg.LogicalChannels[0].ProtocolModelPreferences = config.ProtocolModelPreferences{"chat": {"actual"}, "responses": {"actual"}}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	setBoundAliasResolver(scheduler)
	for i := 0; i < 20; i++ {
		first, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias"})
		if err != nil {
			t.Fatal(err)
		}
		if first.Route.Kind != "responses" || first.CandidateCount != 2 {
			t.Fatalf("priority/multi candidates lost: %#v", first)
		}
		next, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindMessages, Model: "alias", FailedRoutes: map[ChannelRouteKey]bool{first.Route.Key(): true}})
		if err != nil {
			t.Fatal(err)
		}
		if next.Route.Kind != "chat" {
			t.Fatalf("failover lost other bound protocol: %#v", next)
		}
	}
}
