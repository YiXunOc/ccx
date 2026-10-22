package config

import (
	"path/filepath"
	"testing"
)

func TestConvergeMappingDoesNotCrossStaleLogicalUIDAcrossSites(t *testing.T) {
	cfg := Config{
		Upstream:     []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "stale", BaseURL: "https://one.example.com", AccountUID: "acct-one", ModelMapping: map[string]string{"A": "B"}}},
		ChatUpstream: []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "stale", BaseURL: "https://two.example.com", AccountUID: "acct-two", ModelMapping: map[string]string{"A": "C"}}},
	}
	convergeSharedChannelFields(&cfg)
	if cfg.ChatUpstream[0].ManualModel("A") != "C" {
		t.Fatalf("stale UID contaminated another site: %v", cfg.ChatUpstream[0].ModelMapping)
	}
}

func TestModelMappingUpdateDoesNotTouchOtherLogicalGroup(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream: []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", BaseURL: "https://one.example.com", ServiceType: "claude"}},
		ChatUpstream: []UpstreamConfig{
			{ChannelUID: "c", LogicalChannelUID: "lc", BaseURL: "https://one.example.com", ServiceType: "openai"},
			{ChannelUID: "other", LogicalChannelUID: "other-lc", BaseURL: "https://two.example.com", ServiceType: "openai", ModelMapping: map[string]string{"A": "Other"}},
		},
	}}
	if _, err := UpdateUpstreamByKind(cm, ChannelLocation{Kind: ChannelKindMessages, Index: 0}, UpstreamUpdate{ModelMapping: map[string]string{"A": "B"}}); err != nil {
		t.Fatal(err)
	}
	if cm.config.ChatUpstream[0].ManualModel("A") != "B" || cm.config.ChatUpstream[1].ManualModel("A") != "Other" {
		t.Fatalf("mapping crossed groups: %+v", cm.config.ChatUpstream)
	}
}

func TestLegacySiblingMappingSurvivesEmptyPrimaryOnLoad(t *testing.T) {
	cfg := Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", BaseURL: "https://example.com", ServiceType: "claude"}},
		ChatUpstream:    []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", BaseURL: "https://example.com", ServiceType: "openai", ModelMapping: map[string]string{"A": "B"}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}}, Settings: &LogicalChannelSettings{}}},
	}
	if !convergeSharedChannelFields(&cfg) {
		t.Fatal("expected old sibling mapping to converge")
	}
	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("expected old Settings to gain mapping")
	}
	if cfg.Upstream[0].ManualModel("A") != "B" || cfg.ChatUpstream[0].ManualModel("A") != "B" {
		t.Fatalf("mapping lost: messages=%v chat=%v", cfg.Upstream[0].ModelMapping, cfg.ChatUpstream[0].ModelMapping)
	}
}

func TestCreateLogicalChannelWithSharedModelMapping(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name: "mapped-site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://mapped.example.com"},
		ModelMapping: map[string]string{"haiku-hindsight": "glm-5.3-flash"},
		Protocols:    []CreateLogicalChannelProtocol{{Kind: "messages", ServiceType: "claude", APIKeys: []string{"key"}}, {Kind: "chat", ServiceType: "openai", APIKeys: []string{"key"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if logical.Settings == nil || logical.Settings.ModelMapping["haiku-hindsight"] != "glm-5.3-flash" || len(cfg.Upstream) != 1 || len(cfg.ChatUpstream) != 1 || cfg.Upstream[0].ManualModel("haiku-hindsight") != "glm-5.3-flash" || cfg.ChatUpstream[0].ManualModel("haiku-hindsight") != "glm-5.3-flash" {
		t.Fatalf("mapping not created on all routes: logical=%+v", logical)
	}
}

func TestNewProtocolInheritsLogicalModelMapping(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", BaseURL: "https://example.com", ServiceType: "claude", ModelMapping: map[string]string{"A": "B"}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", Name: "group", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://example.com"}, Settings: &LogicalChannelSettings{ModelMapping: map[string]string{"A": "B"}}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}}}},
	}}
	_, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: "lc", Protocols: []UpdateLogicalChannelProtocol{{Kind: "chat", ServiceType: "openai", BaseURL: "https://example.com", APIKeys: []string{"key"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cm.config.ChatUpstream) != 1 || cm.config.ChatUpstream[0].ManualModel("A") != "B" {
		t.Fatalf("new route did not inherit shared mapping: %+v", cm.config.ChatUpstream)
	}
}

func TestUpdateLogicalChannelCommonModelMappingProjectsToAllProtocols(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:     []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", ModelMapping: map[string]string{"A": "B"}}},
		ChatUpstream: []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc"}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", Name: "test", Protocols: []LogicalChannelProtocol{
			{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"},
		}}},
	}}
	mapping := map[string]string{"haiku-hindsight": "glm-5.3-flash"}
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: "lc", Common: &UpdateLogicalChannelCommon{ModelMapping: mapping}}); err != nil {
		t.Fatal(err)
	}
	if cm.config.Upstream[0].ManualModel("haiku-hindsight") != "glm-5.3-flash" || cm.config.ChatUpstream[0].ManualModel("haiku-hindsight") != "glm-5.3-flash" {
		t.Fatalf("not projected: %+v %+v", cm.config.Upstream[0].ModelMapping, cm.config.ChatUpstream[0].ModelMapping)
	}
}

func TestSyncLogicalChannelSettingsPreservesSharedMappingAfterReload(t *testing.T) {
	cfg := Config{
		Upstream:     []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", ModelMapping: map[string]string{"haiku-hindsight": "glm-5.3-flash"}}},
		ChatUpstream: []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc"}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", Protocols: []LogicalChannelProtocol{
			{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"},
		}, Settings: &LogicalChannelSettings{}}},
	}
	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("expected mapping projection")
	}
	if got := cfg.ChatUpstream[0].ManualModel("haiku-hindsight"); got != "glm-5.3-flash" {
		t.Fatalf("chat mapping = %q", got)
	}
	refreshLogicalChannelSettingsFromRoutes(&cfg)
	if got := cfg.LogicalChannels[0].Settings.ModelMapping["haiku-hindsight"]; got != "glm-5.3-flash" {
		t.Fatalf("settings mapping = %q", got)
	}
}

func TestModelMappingSharedAcrossLogicalRoutes(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:          []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", ModelMapping: map[string]string{"A": "B"}}},
		ChatUpstream:      []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc"}},
		ResponsesUpstream: []UpstreamConfig{{ChannelUID: "r", LogicalChannelUID: "lc", ModelMapping: map[string]string{"old": "target"}}},
		GeminiUpstream:    []UpstreamConfig{{ChannelUID: "g", LogicalChannelUID: "lc"}},
	}}
	mapping := map[string]string{"haiku-hindsight": "glm-5.3-flash"}
	if _, err := UpdateUpstreamByKind(cm, ChannelLocation{Kind: ChannelKindMessages, Index: 0}, UpstreamUpdate{ModelMapping: mapping}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []UpstreamConfig{cm.config.Upstream[0], cm.config.ChatUpstream[0], cm.config.ResponsesUpstream[0], cm.config.GeminiUpstream[0]} {
		if route.ManualModel("haiku-hindsight") != "glm-5.3-flash" || route.ManualModel("old") != "" {
			t.Fatalf("sibling not synchronized: %+v", route.ModelMapping)
		}
	}
	if _, err := UpdateUpstreamByKind(cm, ChannelLocation{Kind: ChannelKindMessages, Index: 0}, UpstreamUpdate{ModelMapping: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []UpstreamConfig{cm.config.Upstream[0], cm.config.ChatUpstream[0], cm.config.ResponsesUpstream[0], cm.config.GeminiUpstream[0]} {
		if len(route.ModelMapping) != 0 {
			t.Fatalf("clear not synchronized: %+v", route.ModelMapping)
		}
	}
}

func TestSharedChannelUpdateProjectsToSiblingRoutes(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-chat",
			LogicalChannelUID: "lc-shared",
			APIKeys:           []string{"sk-1"},
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-responses",
			LogicalChannelUID: "lc-shared",
			APIKeys:           []string{"sk-1"},
			RoutePrefix:       "responses",
		}},
	}}

	proxy := "http://127.0.0.1:7890"
	maxMultiplier := 1.5
	routePrefix := "new-chat-prefix"
	_, err := UpdateUpstreamByKind(cm, ChannelLocation{Kind: ChannelKindChat, Index: 0}, UpstreamUpdate{
		ProxyURL:           &proxy,
		MaxGroupMultiplier: &maxMultiplier,
		RoutePrefix:        &routePrefix,
	})
	if err != nil {
		t.Fatalf("UpdateUpstreamByKind() error = %v", err)
	}

	sibling := cm.config.ResponsesUpstream[0]
	if sibling.ProxyURL != proxy {
		t.Fatalf("expected shared proxy projected to sibling, got %q", sibling.ProxyURL)
	}
	if sibling.MaxGroupMultiplier == nil || *sibling.MaxGroupMultiplier != maxMultiplier {
		t.Fatalf("expected shared max multiplier projected to sibling, got %+v", sibling.MaxGroupMultiplier)
	}
	if sibling.RoutePrefix != "responses" {
		t.Fatalf("protocol RoutePrefix must remain route-local, got %q", sibling.RoutePrefix)
	}
}

func TestConvergeSharedChannelFieldsUsesPrimaryRoute(t *testing.T) {
	maxMultiplier := 1.25
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			LogicalChannelUID:  "lc-converge",
			ProxyURL:           "http://primary-proxy",
			MaxGroupMultiplier: &maxMultiplier,
			RoutePrefix:        "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			LogicalChannelUID: "lc-converge",
			ProxyURL:          "http://stale-proxy",
			RoutePrefix:       "responses",
		}},
	}

	if !convergeSharedChannelFields(&cfg) {
		t.Fatal("expected divergent shared fields to converge")
	}
	sibling := cfg.ResponsesUpstream[0]
	if sibling.ProxyURL != "http://primary-proxy" {
		t.Fatalf("expected primary proxy to win, got %q", sibling.ProxyURL)
	}
	if sibling.MaxGroupMultiplier == nil || *sibling.MaxGroupMultiplier != 1.25 {
		t.Fatalf("expected primary max multiplier to win, got %+v", sibling.MaxGroupMultiplier)
	}
	if sibling.RoutePrefix != "responses" {
		t.Fatalf("protocol route prefix must remain unchanged, got %q", sibling.RoutePrefix)
	}
}

func TestConvergeSharedChannelFieldsClearsNilOptionalFieldsAndIsIdempotent(t *testing.T) {
	enabled := false
	costMultiplier := 1.5
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			LogicalChannelUID: "lc-nil-converge",
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			LogicalChannelUID: "lc-nil-converge",
			CostMultiplier:    &costMultiplier,
			Racing:            &ChannelRacingConfig{Enabled: &enabled},
			RoutePrefix:       "responses",
		}},
	}

	if !convergeSharedChannelFields(&cfg) {
		t.Fatal("expected nil shared fields to converge")
	}
	sibling := cfg.ResponsesUpstream[0]
	if sibling.CostMultiplier != nil || sibling.Racing != nil {
		t.Fatalf("nil shared fields were not cleared: cost=%v racing=%v", sibling.CostMultiplier, sibling.Racing)
	}
	if convergeSharedChannelFields(&cfg) {
		t.Fatal("second convergence must be a no-op")
	}
}

func TestSyncLogicalChannelSettingsProjectsAndIsIdempotent(t *testing.T) {
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-chat",
			LogicalChannelUID: "lc-sync",
			ProxyURL:          "http://settings-proxy",
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-responses",
			LogicalChannelUID: "lc-sync",
			ProxyURL:          "http://stale-proxy",
			RoutePrefix:       "responses",
		}},
		LogicalChannels: []LogicalChannel{{
			LogicalChannelUID: "lc-sync",
			Protocols: []LogicalChannelProtocol{
				{Kind: string(ChannelKindChat), ChannelUID: "ch-chat"},
				{Kind: string(ChannelKindResponses), ChannelUID: "ch-responses"},
			},
			Settings: &LogicalChannelSettings{ProxyURL: "http://settings-proxy"},
		}},
	}

	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("expected divergent sibling route to be projected from settings")
	}
	if sibling := cfg.ResponsesUpstream[0]; sibling.ProxyURL != "http://settings-proxy" {
		t.Fatalf("expected settings proxy to win, got %q", sibling.ProxyURL)
	}
	if syncLogicalChannelSettings(&cfg) {
		t.Fatal("second sync must be a no-op (idempotent)")
	}
}

func TestSyncLogicalChannelSettingsClearsNilOptionalFields(t *testing.T) {
	enabled := false
	costMultiplier := 1.5
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-chat-nil",
			LogicalChannelUID: "lc-sync-nil",
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-responses-nil",
			LogicalChannelUID: "lc-sync-nil",
			CostMultiplier:    &costMultiplier,
			Racing:            &ChannelRacingConfig{Enabled: &enabled},
			RoutePrefix:       "responses",
		}},
		LogicalChannels: []LogicalChannel{{
			LogicalChannelUID: "lc-sync-nil",
			Protocols: []LogicalChannelProtocol{
				{Kind: string(ChannelKindChat), ChannelUID: "ch-chat-nil"},
				{Kind: string(ChannelKindResponses), ChannelUID: "ch-responses-nil"},
			},
			Settings: &LogicalChannelSettings{},
		}},
	}

	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("expected nil Settings fields to clear sibling route fields")
	}
	sibling := cfg.ResponsesUpstream[0]
	if sibling.CostMultiplier != nil || sibling.Racing != nil {
		t.Fatalf("nil Settings fields were not projected: cost=%v racing=%v", sibling.CostMultiplier, sibling.Racing)
	}
	if syncLogicalChannelSettings(&cfg) {
		t.Fatal("second Settings sync must be a no-op")
	}
}

func TestRefreshLogicalChannelSettingsFromRoutes(t *testing.T) {
	maxMultiplier := 1.5
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:         "ch-chat",
			LogicalChannelUID:  "lc-refresh",
			ProxyURL:           "http://primary-proxy",
			MaxGroupMultiplier: &maxMultiplier,
		}},
		LogicalChannels: []LogicalChannel{{
			LogicalChannelUID: "lc-refresh",
			Protocols: []LogicalChannelProtocol{
				{Kind: string(ChannelKindChat), ChannelUID: "ch-chat"},
			},
		}},
	}

	refreshLogicalChannelSettingsFromRoutes(&cfg)
	lc := cfg.LogicalChannels[0]
	if lc.Settings == nil {
		t.Fatal("expected settings backfilled from primary route")
	}
	if lc.Settings.ProxyURL != "http://primary-proxy" {
		t.Fatalf("expected primary proxy mirrored, got %q", lc.Settings.ProxyURL)
	}
	if lc.Settings.MaxGroupMultiplier == nil || *lc.Settings.MaxGroupMultiplier != 1.5 {
		t.Fatalf("expected primary max multiplier mirrored, got %+v", lc.Settings.MaxGroupMultiplier)
	}

	cfg.ChatUpstream[0].ProxyURL = "http://updated-proxy"
	refreshLogicalChannelSettingsFromRoutes(&cfg)
	if cfg.LogicalChannels[0].Settings.ProxyURL != "http://updated-proxy" {
		t.Fatalf("expected stale settings overwritten by primary route, got %q", cfg.LogicalChannels[0].Settings.ProxyURL)
	}
}
