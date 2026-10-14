package common

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildChannelView_TuningBenchFields(t *testing.T) {
	up := config.UpstreamConfig{
		Name:                        "test-channel",
		ServiceType:                 "openai",
		AuthHeader:                  "x-api-key",
		RateLimitRPM:                120,
		RateLimitBurst:              20,
		RateLimitMaxConcurrent:      8,
		RateLimitAutoFromHeaders:    boolPtr(true),
		RequestTimeoutMs:            60000,
		ResponseHeaderTimeoutMs:     90000,
		StreamFirstContentTimeoutMs: 30000,
		StreamInactivityTimeoutMs:   20000,
		StreamToolCallIdleTimeoutMs: 45000,
		HistoricalImageTurnLimit:    5,
		ConvertImageURLToB64JSON:    true,
	}

	view := BuildChannelView(up, 0)

	assertions := []struct {
		key      string
		expected interface{}
	}{
		{"rateLimitRpm", 120},
		{"authHeader", "x-api-key"},
		{"rateLimitBurst", 20},
		{"rateLimitMaxConcurrent", 8},
		{"rateLimitAutoFromHeaders", true},
		{"requestTimeoutMs", 60000},
		{"responseHeaderTimeoutMs", 90000},
		{"streamFirstContentTimeoutMs", 30000},
		{"streamInactivityTimeoutMs", 20000},
		{"streamToolCallIdleTimeoutMs", 45000},
		{"historicalImageTurnLimit", 5},
		{"convertImageUrlToB64Json", true},
	}

	for _, a := range assertions {
		got, ok := view[a.key]
		if !ok {
			t.Errorf("BuildChannelView missing key %q", a.key)
			continue
		}
		if got != a.expected {
			t.Errorf("BuildChannelView[%q] = %v (%T), want %v (%T)", a.key, got, got, a.expected, a.expected)
		}
	}
}

func TestBuildChannelView_RateLimitDefaults(t *testing.T) {
	// 当 RateLimitAutoFromHeaders 为 nil（未设置），默认值为 true（自动学习上游限速）
	up := config.UpstreamConfig{
		Name:        "test-channel",
		ServiceType: "openai",
	}

	view := BuildChannelView(up, 0)

	if v, ok := view["rateLimitAutoFromHeaders"]; !ok || v != true {
		t.Errorf("expected rateLimitAutoFromHeaders=true when nil (default enabled), got %v", v)
	}
	if v, ok := view["rateLimitRpm"]; !ok || v != 0 {
		t.Errorf("expected rateLimitRpm=0 when unset, got %v", v)
	}
}

func TestBuildChannelViewExposesBillingAndGroupMultiplierFields(t *testing.T) {
	ratio, limit := 0.5, 1.25
	up := config.UpstreamConfig{
		Name:               "ch",
		CostMultiplier:     &ratio,
		MaxGroupMultiplier: &limit,
	}
	view := BuildChannelView(up, 0)
	if view["costMultiplier"] != &ratio {
		t.Fatalf("渠道视图应回传 costMultiplier，got %v", view["costMultiplier"])
	}
	if view["maxGroupMultiplier"] != &limit {
		t.Fatalf("渠道视图应回传 maxGroupMultiplier，got %v", view["maxGroupMultiplier"])
	}
	for _, key := range []string{"channelPaymentCurrency", "channelPaymentAmount", "channelCreditCurrency", "channelCreditAmount"} {
		if _, ok := view[key]; !ok {
			t.Fatalf("渠道视图应包含计费字段 %s", key)
		}
	}
}

func TestBuildChannelViewExposesKeyModelRestrictionRecords(t *testing.T) {
	// Key/分组级模型限制记录需回读展示：漏登记会表现为"保存成功但重开记录消失"
	up := config.UpstreamConfig{
		Name: "ch",
		DisabledKeyModels: []config.DisabledKeyModelInfo{
			{Key: "sk-a", Model: "model-x", DisabledAt: "2026-09-29T00:00:00Z"},
		},
		DisabledGroupModels: []config.DisabledGroupModelInfo{
			{Key: "sk-a", Model: "model-x", DisabledAt: "2026-09-29T00:00:00Z"},
			{Key: "sk-b", Model: "model-x", DisabledAt: "2026-09-30T00:00:00Z"},
			{QuotaGroup: "coding", Model: "model-y", DisabledAt: "2026-09-29T00:00:00Z"},
		},
	}
	view := BuildChannelView(up, 0)
	for field, want := range map[string]any{
		"disabledKeyModels":   up.DisabledKeyModels,
		"disabledGroupModels": up.DisabledGroupModels,
	} {
		got, exists := view[field]
		if !exists || !reflect.DeepEqual(got, want) {
			t.Fatalf("view[%s] = %#v (exists=%v), want %#v", field, got, exists, want)
		}
		data, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded[field]) != string(wantJSON) {
			t.Fatalf("JSON[%s] = %s, want %s", field, decoded[field], wantJSON)
		}
	}
}

func TestBuildChannelView_ModelRestrictionEmptyContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		up   config.UpstreamConfig
		want string
	}{
		{name: "nil", want: "null"},
		{name: "empty", up: config.UpstreamConfig{
			DisabledKeyModels:   []config.DisabledKeyModelInfo{},
			DisabledGroupModels: []config.DisabledGroupModelInfo{},
		}, want: "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(BuildChannelView(tc.up, 0))
			if err != nil {
				t.Fatal(err)
			}
			var view map[string]json.RawMessage
			if err := json.Unmarshal(data, &view); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"disabledKeyModels", "disabledGroupModels"} {
				got, exists := view[field]
				if !exists || string(got) != tc.want {
					t.Fatalf("JSON[%s] = %s (exists=%v), want %s", field, got, exists, tc.want)
				}
			}
		})
	}
}
