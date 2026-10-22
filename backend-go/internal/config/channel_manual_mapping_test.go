package config

import (
	"encoding/json"
	"testing"
)

func TestManualMappingRejectsInvalidRulesOnCreateAndUpdate(t *testing.T) {
	cm := setupTestConfigManager(t)
	for _, mapping := range []map[string]string{
		{"": "B"}, {" ": "B"}, {"A": ""}, {"A*": "B"},
		{"A": "B*"}, {"A": " B "}, {"A": "B\nC"},
	} {
		if err := cm.AddUpstream(UpstreamConfig{BaseURL: "https://example.test", APIKeys: []string{"test-key"}, ModelMapping: mapping}); err == nil {
			t.Fatalf("accepted invalid create mapping: %#v", mapping)
		}
	}
	if err := cm.AddUpstream(UpstreamConfig{BaseURL: "https://example.test", APIKeys: []string{"test-key"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{ModelMapping: map[string]string{"A": "B*"}}); err == nil {
		t.Fatal("accepted invalid update mapping")
	}
}

func TestChannelManualModelMappingUpdateCloneAndJSON(t *testing.T) {
	var up UpstreamConfig
	if err := json.Unmarshal([]byte(`{"modelMapping":{"Case-A":"Case-B","Case-B":"Case-C"}}`), &up); err != nil {
		t.Fatal(err)
	}
	if got := up.ManualModel("Case-A"); got != "Case-B" {
		t.Fatalf("manual target = %q", got)
	}
	if got := up.ManualModel("case-a"); got != "" {
		t.Fatalf("case insensitive mapping = %q", got)
	}
	clone := up.Clone()
	clone.ModelMapping["Case-A"] = "changed"
	if up.ModelMapping["Case-A"] != "Case-B" {
		t.Fatal("clone shares mapping")
	}
	if _, err := applyUpstreamUpdateFields(&up, UpstreamUpdate{ModelMapping: map[string]string{"new": "target"}}); err != nil {
		t.Fatal(err)
	}
	if up.ManualModel("Case-A") != "" || up.ManualModel("new") != "target" {
		t.Fatalf("replace failed: %+v", up.ModelMapping)
	}
	if _, err := applyUpstreamUpdateFields(&up, UpstreamUpdate{}); err != nil {
		t.Fatal(err)
	}
	if up.ManualModel("new") != "target" {
		t.Fatal("omitted update cleared mapping")
	}
	if _, err := applyUpstreamUpdateFields(&up, UpstreamUpdate{ModelMapping: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if len(up.ModelMapping) != 0 {
		t.Fatal("empty mapping did not clear")
	}
}
