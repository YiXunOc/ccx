package autopilot

import "strings"

// ModelRoutingIntent 描述下游请求是否允许由其他模型承接。
// 自适应入口按协议划分：messages 与 responses 是 Autopilot 托管的逻辑协议，
// 任意请求模型都允许跨模型替代；其余协议（chat/gemini/images/vectors）
// 作为执行侧保持精确模型语义。
type ModelRoutingIntent string

const (
	ModelRoutingIntentExactOnly         ModelRoutingIntent = "exact_only"
	ModelRoutingIntentClaudeAdaptive    ModelRoutingIntent = "claude_adaptive"
	ModelRoutingIntentResponsesAdaptive ModelRoutingIntent = "responses_adaptive"
)

// AllowsSubstitution 表示该请求允许在满足能力下界后跨模型替代。
func (i ModelRoutingIntent) AllowsSubstitution() bool {
	return i == ModelRoutingIntentClaudeAdaptive || i == ModelRoutingIntentResponsesAdaptive
}

// ClassifyModelRoutingIntent 按请求协议识别路由意图。
// 自适应与否只由协议入口决定，与请求的模型名无关：精确/等价命中始终优先短路，
// 跨模型替代只发生在渠道画像不含请求模型时。
func ClassifyModelRoutingIntent(channelKind string) ModelRoutingIntent {
	switch strings.ToLower(strings.TrimSpace(channelKind)) {
	case "messages":
		return ModelRoutingIntentClaudeAdaptive
	case "responses":
		return ModelRoutingIntentResponsesAdaptive
	default:
		return ModelRoutingIntentExactOnly
	}
}

func normalizeRoutingModelID(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func findExactModelProfile(profiles []ModelProfile, requestModel string) (ModelProfile, bool) {
	normalized := normalizeRoutingModelID(requestModel)
	for _, profile := range profiles {
		if normalizeRoutingModelID(profile.ModelID) == normalized {
			return profile, true
		}
	}
	return ModelProfile{}, false
}

// findEquivalentModelProfile 只接受供应商文档明确声明的兼容模型别名。
// 它仍属于 exact-only 语义，不允许扩展为同模型族内的任意替代。
func findEquivalentModelProfile(profiles []ModelProfile, requestModel string) (ModelProfile, bool) {
	normalized := normalizeRoutingModelID(requestModel)
	canonical := canonicalCompatibilityModelID(normalized)
	for _, profile := range profiles {
		candidate := normalizeRoutingModelID(profile.ModelID)
		if candidate == normalized {
			continue
		}
		if canonicalCompatibilityModelID(candidate) == canonical {
			return profile, true
		}
	}
	return ModelProfile{}, false
}

// canonicalCompatibilityModelID 收敛供应商官方文档中的兼容别名。
// 未列出的模型保持原 ID，确保 exact-only 的安全默认值不变。
func canonicalCompatibilityModelID(model string) string {
	normalized := normalizeRoutingModelID(model)
	if strings.HasPrefix(normalized, "claude-opus-4.8") {
		return strings.Replace(normalized, "claude-opus-4.8", "claude-opus-4-8", 1)
	}
	switch normalized {
	case "deepseek-chat":
		return "deepseek-v4-flash"
	case "deepseek-reasoner":
		return "deepseek-v4-pro"
	default:
		return normalized
	}
}
