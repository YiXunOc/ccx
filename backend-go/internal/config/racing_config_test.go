package config

import "testing"

func TestResolveRacingPolicy(t *testing.T) {
	tr := func(v bool) *bool { return &v }
	tests := []struct {
		name     string
		cfg      *Config
		upstream *UpstreamConfig
		want     bool
	}{
		{"配置与渠道均未设置时默认开启", nil, nil, true},
		{"全局与渠道均为 nil 时默认开启", &Config{}, &UpstreamConfig{}, true},
		{"全局显式开启", &Config{Racing: &GlobalRacingConfig{Enabled: tr(true)}}, nil, true},
		{"全局显式关闭", &Config{Racing: &GlobalRacingConfig{Enabled: tr(false)}}, nil, false},
		{"渠道 nil 继承全局关闭", &Config{Racing: &GlobalRacingConfig{Enabled: tr(false)}}, &UpstreamConfig{}, false},
		{"渠道显式关闭覆盖全局开启", &Config{Racing: &GlobalRacingConfig{Enabled: tr(true)}}, &UpstreamConfig{Racing: &ChannelRacingConfig{Enabled: tr(false)}}, false},
		{"渠道显式开启覆盖全局关闭", &Config{Racing: &GlobalRacingConfig{Enabled: tr(false)}}, &UpstreamConfig{Racing: &ChannelRacingConfig{Enabled: tr(true)}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveRacingPolicy(tt.upstream); got != tt.want {
				t.Fatalf("ResolveRacingPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}
