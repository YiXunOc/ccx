package chat

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func chatStreamUsageRequested(c *gin.Context) bool {
	if c == nil {
		return false
	}
	body, ok := c.Get("requestBodyBytes")
	if !ok {
		return false
	}
	var request struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	bytes, ok := body.([]byte)
	return ok && json.Unmarshal(bytes, &request) == nil && request.StreamOptions.IncludeUsage
}

func usageNumber(raw map[string]interface{}, field string) int {
	if value, ok := raw[field].(float64); ok && value > 0 {
		return int(value)
	}
	return 0
}

func cacheReadFrom(raw map[string]interface{}, detailField string) int {
	for _, field := range []string{"cache_read_input_tokens", "prompt_cache_hit_tokens", "cached_tokens"} {
		if value, ok := raw[field].(float64); ok {
			return int(value)
		}
	}
	if details, ok := raw[detailField].(map[string]interface{}); ok {
		if value, ok := details["cached_tokens"].(float64); ok {
			return int(value)
		}
	}
	if detailField != "input_tokens_details" {
		if details, ok := raw["input_tokens_details"].(map[string]interface{}); ok {
			return usageNumber(details, "cached_tokens")
		}
	}
	return 0
}

// Chat/OpenAI and Responses input counters include cache reads. Preserve the total
// so the metrics layer can subtract reads exactly once.
func chatUsageFromOpenAI(raw map[string]interface{}) *types.Usage {
	input := usageNumber(raw, "prompt_tokens")
	if input == 0 {
		input = usageNumber(raw, "input_tokens")
	}
	return &types.Usage{InputTokens: input, PromptTokensTotal: input, OutputTokens: usageNumber(raw, "completion_tokens"), CacheReadInputTokens: cacheReadFrom(raw, "prompt_tokens_details")}
}

func chatUsageFromResponses(raw map[string]interface{}) *types.Usage {
	input := usageNumber(raw, "input_tokens")
	usage := &types.Usage{InputTokens: input, OutputTokens: usageNumber(raw, "output_tokens"), CacheReadInputTokens: cacheReadFrom(raw, "input_tokens_details"), CacheCreationInputTokens: usageNumber(raw, "cache_creation_input_tokens")}
	if details, ok := raw["input_tokens_details"].(map[string]interface{}); ok {
		if _, present := details["cached_tokens"]; present {
			usage.PromptTokensTotal = input
			return usage
		}
	}
	if _, split := raw["cache_read_input_tokens"]; !split {
		usage.PromptTokensTotal = input
	}
	return usage
}

// Anthropic Messages input_tokens excludes separately reported cache traffic.
func chatUsageFromClaude(raw map[string]interface{}) *types.Usage {
	return &types.Usage{InputTokens: usageNumber(raw, "input_tokens"), OutputTokens: usageNumber(raw, "output_tokens"), CacheReadInputTokens: usageNumber(raw, "cache_read_input_tokens"), CacheCreationInputTokens: usageNumber(raw, "cache_creation_input_tokens"), CacheCreation5mInputTokens: usageNumber(raw, "cache_creation_5m_input_tokens"), CacheCreation1hInputTokens: usageNumber(raw, "cache_creation_1h_input_tokens")}
}

func chatUsageFromGemini(raw map[string]interface{}) *types.Usage {
	input := usageNumber(raw, "promptTokenCount")
	return &types.Usage{InputTokens: input, PromptTokensTotal: input, OutputTokens: usageNumber(raw, "candidatesTokenCount"), CacheReadInputTokens: usageNumber(raw, "cachedContentTokenCount")}
}

func chatWireUsage(usage *types.Usage) map[string]interface{} {
	input := usage.PromptTokensTotal
	if input == 0 {
		input = usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
		if usage.CacheCreationInputTokens == 0 {
			input += usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens
		}
	}
	result := map[string]interface{}{"prompt_tokens": input, "completion_tokens": usage.OutputTokens, "total_tokens": input + usage.OutputTokens}
	if usage.CacheReadInputTokens > 0 {
		result["prompt_tokens_details"] = map[string]interface{}{"cached_tokens": usage.CacheReadInputTokens}
	}
	return result
}
