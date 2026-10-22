package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLogicalGroupModelPolicyAcrossProtocolsAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	cm.config.Upstream = []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", BaseURL: "https://site.example.com", ServiceType: "claude", Status: "active", APIKeys: []string{"key-a", "key-b"}}}
	cm.config.ChatUpstream = []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site-chat", BaseURL: "https://site.example.com", ServiceType: "openai", Status: "active", APIKeys: []string{"key-a", "key-b"}}}
	cm.config.ResponsesUpstream = []UpstreamConfig{{ChannelUID: "r", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site-responses", BaseURL: "https://site.example.com", ServiceType: "responses", Status: "active", APIKeys: []string{"key-a", "key-b"}}}
	cm.config.LogicalChannels = []LogicalChannel{{LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}, {Kind: "responses", ChannelUID: "r"}}, Settings: &LogicalChannelSettings{}}}
	if err := cm.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	// A physical-route API call must become one logical policy, not one protocol's policy.
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if len(cfg.LogicalChannels) != 1 || cfg.LogicalChannels[0].Settings == nil || len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 1 {
		t.Fatalf("logical policy not saved: %+v", cfg.LogicalChannels)
	}
	for _, up := range []UpstreamConfig{cfg.Upstream[0], cfg.ChatUpstream[0], cfg.ResponsesUpstream[0]} {
		if !up.IsAPIKeyGroupModelDisabled("key-a", "gpt-6-sol") || up.IsAPIKeyGroupModelDisabled("key-b", "gpt-6-sol") {
			t.Fatalf("wrong projected policy on %s", up.ChannelUID)
		}
	}
	if _, _, err := cm.DisableGroupModel("Chat", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	if len(cm.GetConfig().LogicalChannels[0].Settings.DisabledGroupModels) != 1 {
		t.Fatal("duplicate sibling write")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		ChannelsV3 []struct {
			Settings *LogicalChannelSettings `json:"settings"`
		} `json:"channelsV3"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.ChannelsV3) != 1 || disk.ChannelsV3[0].Settings == nil || len(disk.ChannelsV3[0].Settings.DisabledGroupModels) != 1 {
		t.Fatal("logical policy missing from authoritative disk")
	}
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if !reloaded.GetConfig().ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-a", "gpt-6-sol") {
		t.Fatal("policy lost on reload")
	}
	if _, _, err := reloaded.RestoreGroupModel("Chat", 0, "key-a", "", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg = reloaded.GetConfig()
	if len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 0 {
		t.Fatal("logical restore failed")
	}
	for _, up := range []UpstreamConfig{cfg.Upstream[0], cfg.ChatUpstream[0], cfg.ResponsesUpstream[0]} {
		if up.IsAPIKeyGroupModelDisabled("key-a", "gpt-6-sol") {
			t.Fatalf("restore left policy on %s", up.ChannelUID)
		}
	}
}

func TestLogicalGroupModelPolicyNewProtocolInherits(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"},
		Protocols: []CreateLogicalChannelProtocol{{Kind: "messages", ServiceType: "claude", APIKeys: []string{"key"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	_, err = cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: logical.LogicalChannelUID, Protocols: []UpdateLogicalChannelProtocol{{Kind: "chat", ServiceType: "openai", BaseURL: "https://site.example.com", APIKeys: []string{"key"}}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if len(cfg.ChatUpstream) != 1 || !cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key", "gpt-6-sol") {
		t.Fatal("new protocol missing logical policy")
	}
}

func TestLogicalGroupModelPolicyRejectsFailedPersistenceWithoutStateChange(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "missing", "config.json"), config: Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", BaseURL: "https://site.example.com", ServiceType: "claude", APIKeys: []string{"key"}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}}, Settings: &LogicalChannelSettings{}}},
	}}
	before := cm.GetConfig()
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key", "gpt-*"); err == nil {
		t.Fatal("expected disk failure")
	}
	if !reflect.DeepEqual(cm.GetConfig(), before) {
		t.Fatal("failed save changed live state")
	}
}

func TestLogicalGroupModelPolicyIgnoresLegacyRouteOnlyRecords(t *testing.T) {
	cfg := Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", APIKeys: []string{"key"}, DisabledGroupModels: []DisabledGroupModelInfo{{Key: "key", Model: "gpt-*"}}}},
		ChatUpstream:    []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", APIKeys: []string{"key"}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}}, Settings: &LogicalChannelSettings{}}},
	}
	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("legacy physical policy not cleared")
	}
	if len(cfg.Upstream[0].DisabledGroupModels) != 0 || len(cfg.ChatUpstream[0].DisabledGroupModels) != 0 || len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 0 {
		t.Fatal("legacy route-only policy was promoted")
	}
}

func TestLogicalGroupModelPolicySameKeyDifferentProtocolGroups(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", BaseURL: "https://site.example.com", ServiceType: "claude", APIKeys: []string{"key-a", "key-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "key-a", QuotaGroup: "group-m"}, {Key: "key-b", QuotaGroup: "group-m"}}}},
		ChatUpstream:    []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site-chat", BaseURL: "https://site.example.com", ServiceType: "openai", APIKeys: []string{"key-a", "key-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "key-a", QuotaGroup: "group-c"}, {Key: "key-b", QuotaGroup: "other"}}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}}, Settings: &LogicalChannelSettings{}}},
	}}
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if !cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-a", "gpt-6-sol") {
		t.Fatalf("same key bypassed on other protocol group: logical=%+v chat=%+v", cfg.LogicalChannels[0].Settings.DisabledGroupModels, cfg.ChatUpstream[0].DisabledGroupModels)
	}
	if !cfg.Upstream[0].IsAPIKeyGroupModelDisabled("key-b", "gpt-6-sol") {
		t.Fatal("same quota group must remain excluded")
	}
	if cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-b", "gpt-6-sol") {
		t.Fatal("different group on chat was disabled")
	}
	if _, _, err := cm.RestoreGroupModel("Chat", 0, "", "group-m", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg = cm.GetConfig()
	if len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 0 || cfg.Upstream[0].IsAPIKeyGroupModelDisabled("key-b", "gpt-6-sol") {
		t.Fatal("cross-protocol restore left group ban")
	}
}

func TestLogicalGroupModelPolicyUngroupedKeyAcrossProtocols(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", BaseURL: "https://site.example.com", ServiceType: "claude", APIKeys: []string{"key-a"}}},
		ChatUpstream:    []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site-chat", BaseURL: "https://site.example.com", ServiceType: "openai", APIKeys: []string{"key-a", "key-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "key-a", QuotaGroup: "group-c"}, {Key: "key-b", QuotaGroup: "other"}}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}}, Settings: &LogicalChannelSettings{}}},
	}}
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if !cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-a", "gpt-6-sol") {
		t.Fatal("ungrouped key bypassed on grouped sibling")
	}
	if cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-b", "gpt-6-sol") {
		t.Fatal("unrelated key excluded")
	}
}

func TestLogicalGroupModelPolicyDistinctGroupsAndRestore(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		Upstream:        []UpstreamConfig{{ChannelUID: "m", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", BaseURL: "https://site.example.com", ServiceType: "claude", APIKeys: []string{"key-a", "key-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "key-a", QuotaGroup: "X"}, {Key: "key-b", QuotaGroup: "X"}}}},
		ChatUpstream:    []UpstreamConfig{{ChannelUID: "c", LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site-chat", BaseURL: "https://site.example.com", ServiceType: "openai", APIKeys: []string{"key-a", "key-c"}, APIKeyConfigs: []APIKeyConfig{{Key: "key-a", QuotaGroup: "Y"}, {Key: "key-c", QuotaGroup: "Y"}}}},
		LogicalChannels: []LogicalChannel{{LogicalChannelUID: "lc", AccountUID: "acct-shared", Name: "site", Kind: LogicalChannelKindLLM, BaseURLs: []string{"https://site.example.com"}, Protocols: []LogicalChannelProtocol{{Kind: "messages", ChannelUID: "m"}, {Kind: "chat", ChannelUID: "c"}}, Settings: &LogicalChannelSettings{}}},
	}}
	if _, _, err := cm.DisableGroupModel("Messages", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cm.DisableGroupModel("Chat", 0, "key-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	if len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 2 || !cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-c", "gpt-6-sol") {
		t.Fatal("second group not stored")
	}
	if _, _, err := cm.RestoreGroupModel("Messages", 0, "", "X", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	cfg = cm.GetConfig()
	if len(cfg.LogicalChannels[0].Settings.DisabledGroupModels) != 1 || cfg.LogicalChannels[0].Settings.DisabledGroupModels[0].QuotaGroup != "Y" || !cfg.ChatUpstream[0].IsAPIKeyGroupModelDisabled("key-c", "gpt-6-sol") {
		t.Fatal("restore X erased Y")
	}
}

func TestPhysicalGroupedRecordDoesNotFollowKeyIntoAnotherGroup(t *testing.T) {
	up := UpstreamConfig{APIKeys: []string{"key-a"}, DisabledGroupModels: []DisabledGroupModelInfo{{Key: "key-a", QuotaGroup: "old", Model: "gpt-*"}}}
	if up.IsGroupModelDisabled("key-a", "new", "gpt-6-sol") {
		t.Fatal("legacy group rule followed key")
	}
}
