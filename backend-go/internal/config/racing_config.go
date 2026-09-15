package config

import "log"

// GlobalRacingConfig 竞速（影子请求）全局配置。
// 极简面：仅总开关。影子数、触发阈值、候选成本过滤等行为参数由请求的
// CostPreference 经 internal/racing 策略表自动推导，不作为配置项暴露。
type GlobalRacingConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // 全局开关（nil=默认开启）
}

// ChannelRacingConfig 渠道级竞速参与配置。
// 关闭 = 该渠道既不作为主触发竞速，其候选也不会被选为影子目标。
type ChannelRacingConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // 渠道级开关（nil=继承全局）
}

// ResolveRacingPolicy 解析渠道最终是否参与竞速。
// 覆盖优先级：渠道级字段 > 全局字段 > 默认开启。
func (c *Config) ResolveRacingPolicy(u *UpstreamConfig) bool {
	enabled := true
	if c != nil && c.Racing != nil && c.Racing.Enabled != nil {
		enabled = *c.Racing.Enabled
	}
	if u != nil && u.Racing != nil && u.Racing.Enabled != nil {
		enabled = *u.Racing.Enabled
	}
	return enabled
}

// SetRacingEnabled 更新竞速全局开关并持久化（管理 API PUT /api/racing/config）。
func (cm *ConfigManager) SetRacingEnabled(enabled bool) error {
	cm.mu.Lock()
	if cm.config.Racing == nil {
		cm.config.Racing = &GlobalRacingConfig{}
	}
	cm.config.Racing.Enabled = &enabled
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}
	log.Printf("[Config-Racing] 竞速模式已更新: %v", enabled)
	cm.fireConfigChangeCallbacks()
	return nil
}

// GetRacingEnabled 读取竞速全局开关（nil 配置视为默认开启）。
func (cm *ConfigManager) GetRacingEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg := cm.config
	return cfg.ResolveRacingPolicy(nil)
}
