package config

import (
	"math"
	"testing"
	"time"
)

func TestEvaluateAPIKeyMultiplierEligibility(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	one := 1.0
	two := 2.0
	zero := 0.0
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	tests := []struct {
		name       string
		cfg        APIKeyConfig
		channelMax *float64
		want       MultiplierEligibility
	}{
		{name: "legacy config", cfg: APIKeyConfig{}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "multiplier without channel gate", cfg: APIKeyConfig{GroupMultiplier: &two, MultiplierSource: "manual"}, channelMax: nil, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "nan multiplier", cfg: APIKeyConfig{GroupMultiplier: ptrFloat64(math.NaN()), MultiplierSource: "manual"}, channelMax: &one, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMultiplier}},
		{name: "nan channel max", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "manual"}, channelMax: ptrFloat64(math.NaN()), want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier}},
		{name: "negative channel max", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "manual"}, channelMax: ptrFloat64(-1), want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier}},
		{name: "manual source within limit", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "manual"}, channelMax: &two, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "provider source ignores expiry", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "provider", MultiplierExpiresAt: &past}, channelMax: &two, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "over channel limit", cfg: APIKeyConfig{GroupMultiplier: &two, MultiplierSource: "manual"}, channelMax: &one, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonOverGroupLimit}},
		{name: "equal to channel limit", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "manual"}, channelMax: &one, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "fresh new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, channelMax: &two, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK, Status: "fresh"}},
		{name: "stale new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "stale", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: "stale"}},
		{name: "expired fresh new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &past}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: "fresh"}},
		{name: "missing ownership new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: "fresh"}},
		{name: "sync error new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "sync_error", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonSyncError, Status: "sync_error"}},
		{name: "relink new api", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "relink_required", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: "relink_required"}},
		{name: "unknown source", cfg: APIKeyConfig{GroupMultiplier: &one, MultiplierSource: "mystery"}, channelMax: &two, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonUnknownSource}},
		{name: "zero multiplier opportunistic", cfg: APIKeyConfig{GroupMultiplier: &zero, MultiplierSource: "manual", ConsumptionPolicy: KeyConsumptionOpportunistic}, channelMax: &one, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "zero multiplier without gate", cfg: APIKeyConfig{GroupMultiplier: &zero, MultiplierSource: "manual", ConsumptionPolicy: KeyConsumptionNormal}, channelMax: nil, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "legacy key level max ignored", cfg: APIKeyConfig{GroupMultiplier: &two, MaxGroupMultiplier: &one, MultiplierSource: "manual"}, channelMax: nil, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAPIKeyMultiplierEligibility(tt.cfg, tt.channelMax, now)
			if got.Eligible != tt.want.Eligible || got.Reason != tt.want.Reason || got.Status != tt.want.Status {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetNextAPIKeySkipsKeysByUnifiedMultiplierEligibility(t *testing.T) {
	safeRatio, unsafeRatio, limit := 1.0, 2.0, 1.0
	cm := &ConfigManager{}
	future := time.Now().Add(time.Hour)
	upstream := &UpstreamConfig{
		Name:    "newapi",
		APIKeys: []string{"unsafe", "fresh", "legacy", "stale"},
		// 渠道级上限是唯一真源：unsafe 的倍率 2 超过渠道上限 1，自动退出调度。
		MaxGroupMultiplier: &limit,
		APIKeyConfigs: []APIKeyConfig{
			{Key: "unsafe", GroupMultiplier: &unsafeRatio, MultiplierSource: "manual"},
			{Key: "fresh", GroupMultiplier: &safeRatio, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future},
			{Key: "legacy"},
			{Key: "stale", GroupMultiplier: &safeRatio, MultiplierSource: "new_api", MultiplierSyncStatus: "stale", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 2, MultiplierExpiresAt: &future},
		},
	}

	key, err := cm.GetNextAPIKey(upstream, nil, "Responses")
	if err != nil || key != "fresh" {
		t.Fatalf("GetNextAPIKey() = %q, %v; want fresh key", key, err)
	}

	key, err = cm.GetNextAPIKey(upstream, map[string]bool{"fresh": true}, "Responses")
	if err != nil || key != "legacy" {
		t.Fatalf("GetNextAPIKey() = %q, %v; want legacy key", key, err)
	}
}

func TestGetAdminAPIKeySkipsDisabledKeyByUnifiedMultiplierEligibility(t *testing.T) {
	unsafeRatio, limit := 2.0, 1.0
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name:               "newapi",
		MaxGroupMultiplier: &limit,
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "unsafe",
			Config: &APIKeyConfig{
				Key:              "unsafe",
				GroupMultiplier:  &unsafeRatio,
				MultiplierSource: "manual",
			},
		}},
	}

	if _, _, err := cm.GetAdminAPIKey(upstream, nil, "Responses"); err == nil {
		t.Fatal("GetAdminAPIKey() must not borrow an over-limit key")
	}
}

func ptrFloat64(value float64) *float64 {
	return &value
}
