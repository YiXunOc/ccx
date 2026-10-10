package config

import (
	"strings"
	"time"
)

// MinBalanceCheckInterval 上游余额探测间隔硬下限，任何更小的配置值一律 clamp 到该值。
// 余额探测会对上游发真实 HTTP 请求（每 key 每轮 1-3 发轻量 GET），
// 与保活验证共用同一频率纪律。
const MinBalanceCheckInterval = 30 * time.Minute

// 上游余额探测的 provider 常量（决定探测器调用的上游端点与响应解析方式）。
const (
	// BalanceProviderAuto 自动识别：按 newapi_token → sub2api → newapi_billing 顺序
	// 逐个尝试，按响应 schema 特征判定；识别成功后记忆，后续轮次优先复用。
	BalanceProviderAuto = "auto"
	// BalanceProviderNewapiToken new-api 系 /api/usage/token/ 端点。
	// 一发请求直取 key 级 remaining（原始 quota 点），精度最高。
	BalanceProviderNewapiToken = "newapi_token"
	// BalanceProviderSub2API sub2api /v1/usage 端点。
	// key 级/订阅/钱包三模式，响应自带 unit:"USD"。
	BalanceProviderSub2API = "sub2api"
	// BalanceProviderNewapiBilling OpenAI 兼容 billing 端点对
	//（/v1/dashboard/billing/subscription + /usage），one-api 系通用兜底。
	// 需两发请求做减法；受上游站点展示单位/统计口径影响，scope 不保证 key 级。
	BalanceProviderNewapiBilling = "newapi_billing"
)

// DefaultBalanceQuotaPerUnit new-api 系 quota 点→USD 换算比率的默认值
// （new-api 默认 QuotaPerUnit=500000，即 500000 点 = $1）。
const DefaultBalanceQuotaPerUnit = 500000.0

// ParseBalanceProvider 归一化余额探测 provider 字符串；空串/未知值回退 auto。
func ParseBalanceProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case BalanceProviderNewapiToken:
		return BalanceProviderNewapiToken
	case BalanceProviderSub2API:
		return BalanceProviderSub2API
	case BalanceProviderNewapiBilling:
		return BalanceProviderNewapiBilling
	default:
		return BalanceProviderAuto
	}
}

// GlobalBalanceCheckConfig 上游余额探测全局配置（所有字段可选，零值/nil 使用默认值）。
type GlobalBalanceCheckConfig struct {
	// Enabled 全局开关（nil=按渠道托管类型分档默认：new_api 托管渠道默认开，其余默认关）
	Enabled *bool `json:"enabled,omitempty"`
	// DefaultIntervalMinutes 全局默认探测间隔（分钟，0=6 小时）
	DefaultIntervalMinutes int `json:"defaultIntervalMinutes,omitempty"`
	// MaxConcurrency 渠道间最大并发探测数（0=2）
	MaxConcurrency int `json:"maxConcurrency,omitempty"`
	// TimeoutMs 单次探测请求超时（毫秒，0=15000）
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
}

// ChannelBalanceCheckConfig 渠道级余额探测配置（所有字段可选，优先于全局配置与托管默认）。
type ChannelBalanceCheckConfig struct {
	// Enabled 渠道级开关（nil=继承全局/托管默认，可单独覆盖开/关）
	Enabled *bool `json:"enabled,omitempty"`
	// Provider 上游余额端点类型（""=auto；取值见 ParseBalanceProvider）
	Provider string `json:"provider,omitempty"`
	// IntervalMinutes 渠道级探测间隔（分钟，0=继承全局默认）
	IntervalMinutes int `json:"intervalMinutes,omitempty"`
	// QuotaPerUnit new-api quota 点→USD 换算比率覆盖（0=默认 500000）
	QuotaPerUnit float64 `json:"quotaPerUnit,omitempty"`
}

// cloneChannelBalanceCheckConfig 深拷贝渠道级余额探测配置（指针字段独立分配）。
func cloneChannelBalanceCheckConfig(c *ChannelBalanceCheckConfig) *ChannelBalanceCheckConfig {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Enabled != nil {
		v := *c.Enabled
		cp.Enabled = &v
	}
	return &cp
}

// ResolvedBalancePolicy 合并后的最终余额探测策略，供调度器直接使用。
type ResolvedBalancePolicy struct {
	Enabled        bool          // 是否启用余额探测
	Interval       time.Duration // 探测间隔（已 clamp 到 >= MinBalanceCheckInterval）
	Provider       string        // 已归一化的 provider（auto/newapi_token/sub2api/newapi_billing）
	Timeout        time.Duration // 单次探测请求超时
	MaxConcurrency int           // 渠道间最大并发探测数
	QuotaPerUnit   float64       // quota 点→USD 换算比率
}

// ResolveBalancePolicy 解析指定渠道的最终余额探测策略。
// 覆盖优先级：渠道级字段 > 全局字段 > 托管类型分档默认。
// 托管默认：AutoManagedKind=="new_api" 的渠道默认开启 auto 探测
// （上游为 new-api 系部署，余额端点存在的概率极高）；其余渠道默认关闭。
func (c *Config) ResolveBalancePolicy(u *UpstreamConfig) ResolvedBalancePolicy {
	policy := ResolvedBalancePolicy{
		Enabled:        u != nil && strings.EqualFold(strings.TrimSpace(u.AutoManagedKind), "new_api"),
		Interval:       6 * time.Hour,
		Provider:       BalanceProviderAuto,
		Timeout:        15 * time.Second,
		MaxConcurrency: 2,
		QuotaPerUnit:   DefaultBalanceQuotaPerUnit,
	}

	if g := c.BalanceCheck; g != nil {
		if g.Enabled != nil {
			policy.Enabled = *g.Enabled
		}
		if g.DefaultIntervalMinutes > 0 {
			policy.Interval = time.Duration(g.DefaultIntervalMinutes) * time.Minute
		}
		if g.MaxConcurrency > 0 {
			policy.MaxConcurrency = g.MaxConcurrency
		}
		if g.TimeoutMs > 0 {
			policy.Timeout = time.Duration(g.TimeoutMs) * time.Millisecond
		}
	}

	if u != nil && u.BalanceCheck != nil {
		ch := u.BalanceCheck
		if ch.Enabled != nil {
			policy.Enabled = *ch.Enabled
		}
		if ch.Provider != "" {
			policy.Provider = ch.Provider
		}
		if ch.IntervalMinutes > 0 {
			policy.Interval = time.Duration(ch.IntervalMinutes) * time.Minute
		}
		if ch.QuotaPerUnit > 0 {
			policy.QuotaPerUnit = ch.QuotaPerUnit
		}
	}

	if policy.Interval < MinBalanceCheckInterval {
		policy.Interval = MinBalanceCheckInterval
	}
	policy.Provider = ParseBalanceProvider(policy.Provider)
	return policy
}
