package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolModelPreferencesCopiesAndMerge(t *testing.T) {
	p := ProtocolModelPreferences{"chat": {"model"}}
	clone := p.Clone()
	clone["chat"][0] = "changed"
	if p["chat"][0] != "model" {
		t.Fatal("clone aliases original")
	}
	if _, err := mergeProtocolModelPreferences(p, ProtocolModelPreferences{"responses": {"model"}}); err == nil {
		t.Fatal("convergence conflict accepted")
	}
	merged, err := mergeProtocolModelPreferences(p, ProtocolModelPreferences{"responses": {"other"}})
	if err != nil || merged.ProtocolForModel("other") != "responses" {
		t.Fatalf("merge lost binding: %v", err)
	}
	if err := (ProtocolModelPreferences{"chat": {"model", "model"}}).Validate(); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestProtocolPreferencesRebuildPreservesBindings(t *testing.T) {
	cfg := Config{Upstream: []UpstreamConfig{mkPhys("acct", "msg", "one", "n", "claude", "active")}, ChatUpstream: []UpstreamConfig{mkPhys("acct", "chat", "two", "n", "openai", "active")}, LogicalChannels: []LogicalChannel{
		{LogicalChannelUID: "one", AccountUID: "acct", ProtocolModelPreferences: ProtocolModelPreferences{"chat": {"a"}}},
		{LogicalChannelUID: "two", AccountUID: "acct", ProtocolModelPreferences: ProtocolModelPreferences{"responses": {"b"}}},
	}}
	RebuildLogicalChannels(&cfg)
	if len(cfg.LogicalChannels) != 1 || cfg.LogicalChannels[0].ProtocolModelPreferences.ProtocolForModel("b") != "responses" {
		t.Fatal("rebuild lost binding")
	}
	cfg.ResponsesUpstream = append(cfg.ResponsesUpstream, mkPhys("acct", "resp", "", "n", "responses", "active"))
	RebuildLogicalChannels(&cfg)
	if cfg.LogicalChannels[0].ProtocolModelPreferences.ProtocolForModel("a") != "chat" {
		t.Fatal("new route lost binding")
	}
}

func TestProtocolPreferencesCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(path, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	lc, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{Name: "example", BaseURLs: []string{"https://binding.example"}, Kind: LogicalChannelKindLLM, Protocols: []CreateLogicalChannelProtocol{{Kind: "chat", ServiceType: "openai", APIKeys: []string{"test-key"}}}, ProtocolModelPreferences: ProtocolModelPreferences{"chat": {"model"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: lc.LogicalChannelUID, Common: &UpdateLogicalChannelCommon{}}); err != nil {
		t.Fatal(err)
	}
	if cm.GetLogicalChannel(lc.LogicalChannelUID).ProtocolModelPreferences.ProtocolForModel("model") != "chat" {
		t.Fatal("omission cleared binding")
	}
	snap := cm.GetConfig()
	snap.LogicalChannels[0].ProtocolModelPreferences["chat"][0] = "changed"
	if cm.GetLogicalChannel(lc.LogicalChannelUID).ProtocolModelPreferences.ProtocolForModel("model") != "chat" {
		t.Fatal("snapshot aliases binding")
	}
	empty := ProtocolModelPreferences{}
	if _, err = cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: lc.LogicalChannelUID, Common: &UpdateLogicalChannelCommon{ProtocolModelPreferences: &empty}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err = json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.ProtocolForLogicalModel(lc.LogicalChannelUID, "model") != "" {
		t.Fatal("clear did not persist")
	}
}

func TestProtocolPreferencesDeletedAccountMember(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			cfg := normalizedBindingConflictFixture()
			a, b := cfg.Upstream[0], cfg.ChatUpstream[0]
			a.AccountUID = "account-a"
			b.AccountUID = "account-b"
			b.ServiceType = a.ServiceType
			cfg.Upstream = []UpstreamConfig{a, b}
			cfg.ChatUpstream = nil
			if !conflict {
				cfg.LogicalChannels[1].ProtocolModelPreferences = ProtocolModelPreferences{"responses": {"other"}}
			}
			if conflict {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.json")
				raw, _ := json.Marshal(cfg)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				active := &ConfigManager{configFile: path, backupDir: filepath.Join(dir, "backups"), config: Config{LogicalChannels: []LogicalChannel{{LogicalChannelUID: "previous"}}}}
				before, _ := json.Marshal(active.config)
				if err := active.loadConfig(); err == nil {
					t.Fatal("load accepted deleted-member conflict")
				}
				after, _ := json.Marshal(active.config)
				disk, err := os.ReadFile(path)
				if err != nil || string(disk) != string(raw) || string(before) != string(after) {
					t.Fatal("failed load changed memory or disk")
				}
			}
			cm := &ConfigManager{config: cfg}
			before, _ := json.Marshal(cm.config)
			changed, err := cm.mergeManagedProviderAccountsChecked()
			if conflict {
				after, _ := json.Marshal(cm.config)
				if err == nil || string(before) != string(after) {
					t.Fatal("conflicting deleted member was accepted or mutated")
				}
				return
			}
			if err != nil || !changed {
				t.Fatalf("merge failed: %v", err)
			}
			if err = RebuildLogicalChannels(&cm.config); err != nil {
				t.Fatal(err)
			}
			if len(cm.config.Upstream) != 1 || len(cm.config.LogicalChannels) != 1 {
				t.Fatal("expected existing account convergence")
			}
			p := cm.config.LogicalChannels[0].ProtocolModelPreferences
			if p.ProtocolForModel("model") != "chat" || p.ProtocolForModel("other") != "responses" {
				t.Fatal("deleted member lost bindings")
			}
		})
	}
}

func TestProtocolPreferencesWhitespaceAccountConflict(t *testing.T) {
	cfg := normalizedBindingConflictFixture()
	cfg.Upstream[0].AccountUID = " acct "
	cfg.ChatUpstream[0].AccountUID = "acct"
	before, _ := json.Marshal(cfg)
	if err := RebuildLogicalChannels(&cfg); err == nil {
		t.Fatal("whitespace conflict accepted")
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("failed rebuild mutated config")
	}
}

func TestLogicalProtocolUpdateSaveFailureRestoresSnapshot(t *testing.T) {
	cfg := normalizedBindingConflictFixture()
	cfg.ChatUpstream = nil
	cfg.LogicalChannels = cfg.LogicalChannels[:1]
	cfg.Upstream[0].AccountUID = "acct"
	if err := RebuildLogicalChannels(&cfg); err != nil {
		t.Fatal(err)
	}
	cm := &ConfigManager{config: cfg, configFile: filepath.Join(t.TempDir(), "missing", "config.json")}
	before, _ := json.Marshal(cm.config)
	enabled := false
	_, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{LogicalChannelUID: cfg.LogicalChannels[0].LogicalChannelUID, Protocols: []UpdateLogicalChannelProtocol{{Kind: "messages", Enabled: &enabled, Priority: 17}}})
	if err == nil {
		t.Fatal("expected save failure")
	}
	after, _ := json.Marshal(cm.config)
	if string(before) != string(after) {
		t.Fatal("failed save changed protocols or configuration")
	}
}

func TestV3FinalAccountNormalizationPersists(t *testing.T) {
	t.Setenv(channelAuthoritativeStrictEnv, "false")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(path, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	cm.Close()
	cfg := cm.GetConfig()
	u := mkPhys("stable-account", "route", "logical", "route", "claude", "active")
	u.BaseURL = "https://normalization.example"
	u.APIKeys = []string{"test-key"}
	u.AutoManagedKind = "new_api"
	u.APIKeyConfigs = []APIKeyConfig{{Key: "test-key", SourceSubscriptionUID: "subscription"}}
	cfg.Upstream = []UpstreamConfig{u}
	cfg.LogicalChannels = []LogicalChannel{{LogicalChannelUID: "logical"}}
	// Warm legacy migrations before constructing a dual-write mismatch.
	if err = cm.saveConfigLocked(cfg); err != nil {
		t.Fatal(err)
	}
	if err = cm.loadConfig(); err != nil {
		t.Fatal(err)
	}
	cfg = cm.GetConfig()
	cfg.ChannelsV3 = BuildAuthoritativeChannels(&cfg)
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	for i := range cfg.ChannelsV3 {
		for j := range cfg.ChannelsV3[i].Protocols {
			cfg.ChannelsV3[i].Protocols[j].Upstream.AccountUID = ""
		}
	}
	raw, _ := json.Marshal(cfg)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = cm.loadConfig(); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk Config
	if err = json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	projected := ApplyAuthoritativeChannelsAsStruct(disk.ChannelsV3)
	want := deriveNewApiAccountUID("subscription")
	if len(projected.Upstream) != 1 || projected.Upstream[0].AccountUID != want {
		t.Fatalf("final V3 normalization not persisted: %#v", projected.Upstream)
	}
}

func normalizedBindingConflictFixture() Config {
	a := mkPhys("", "msg", "one", "n", "claude", "active")
	b := mkPhys("", "chat", "two", "n", "openai", "active")
	for _, u := range []*UpstreamConfig{&a, &b} {
		u.AutoManaged = true
		u.AutoManagedKind = "new_api"
		u.BaseURL = "https://same.example"
		u.APIKeys = []string{"test-key"}
		u.APIKeyConfigs = []APIKeyConfig{{SourceSubscriptionUID: "same-subscription"}}
	}
	return Config{Upstream: []UpstreamConfig{a}, ChatUpstream: []UpstreamConfig{b}, LogicalChannels: []LogicalChannel{
		{LogicalChannelUID: "one", ProtocolModelPreferences: ProtocolModelPreferences{"chat": {"model"}}},
		{LogicalChannelUID: "two", ProtocolModelPreferences: ProtocolModelPreferences{"responses": {"model"}}},
	}}
}

func TestProtocolPreferencesNormalizationConflictIsAtomic(t *testing.T) {
	for _, op := range []string{"save", "reload", "first-load", "rebuild"} {
		t.Run(op, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			conflict := normalizedBindingConflictFixture()
			disk := []byte(`{"sentinel":"unchanged"}`)
			if op == "reload" || op == "first-load" {
				disk, _ = json.Marshal(conflict)
			}
			if err := os.WriteFile(path, disk, 0600); err != nil {
				t.Fatal(err)
			}
			cm := &ConfigManager{configFile: path, backupDir: filepath.Join(dir, "backups"), config: Config{LogicalChannels: []LogicalChannel{{LogicalChannelUID: "previous"}}}}
			before, _ := json.Marshal(cm.config)
			var err error
			switch op {
			case "save":
				err = cm.saveConfigLocked(conflict)
			case "reload":
				err = cm.loadConfig()
			case "first-load":
				var fresh *ConfigManager
				fresh, err = NewConfigManager(path, cm.backupDir)
				if fresh != nil {
					fresh.Close()
				}
			case "rebuild":
				normalizeNewApiAccountUIDsConfig(&conflict)
				snapshot, _ := json.Marshal(conflict)
				err = RebuildLogicalChannels(&conflict)
				after, _ := json.Marshal(conflict)
				if string(snapshot) != string(after) {
					t.Fatal("failed rebuild mutated input")
				}
			}
			if err == nil {
				t.Fatal("normalization conflict accepted")
			}
			after, _ := json.Marshal(cm.config)
			if string(before) != string(after) {
				t.Fatal("failed operation changed active config")
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(disk) {
				t.Fatal("failed operation changed disk")
			}
		})
	}
}

// Bindings belong to a logical channel, never to the global config.
func TestProtocolModelPreferencesLoadValidation(t *testing.T) {
	for _, tt := range []struct {
		name, raw string
		wantErr   bool
	}{
		{"legacy", `{}`, false},
		{"valid", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{"chat":["model-a"],"responses":["model-b"]}}]}`, false},
		{"conflict within channel", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{"chat":["model-a"],"responses":["model-a"]}}]}`, true},
		{"independent channels", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{"chat":["model-a"]}},{"logicalChannelUid":"lc-b","protocolModelPreferences":{"responses":["model-a"]}}]}`, false},
		{"unknown protocol", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{"unknown":["model-a"]}}]}`, true},
		{"blank model", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{"chat":[" "]}}]}`, true},
		{"empty restores automatic", `{"logicalChannels":[{"logicalChannelUid":"lc-a","protocolModelPreferences":{}}]}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0600); err != nil {
				t.Fatal(err)
			}
			cm, err := NewConfigManager(path, filepath.Join(dir, "backups"))
			if cm != nil {
				defer cm.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("load error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
