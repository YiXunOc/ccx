package autopilot

import "testing"

func TestClassifyModelRoutingIntent(t *testing.T) {
	tests := []struct {
		name        string
		channelKind string
		want        ModelRoutingIntent
	}{
		{name: "Messages Claude alias", channelKind: "messages", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Messages third-party model", channelKind: "messages", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Messages unknown model", channelKind: "messages", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Messages kind is case-insensitive", channelKind: " Messages ", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Responses GPT legacy series", channelKind: "responses", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Responses GPT-6 series", channelKind: "responses", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Responses third-party model", channelKind: "responses", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Responses unknown model", channelKind: "responses", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Chat execution kind", channelKind: "chat", want: ModelRoutingIntentExactOnly},
		{name: "Gemini execution kind", channelKind: "gemini", want: ModelRoutingIntentExactOnly},
		{name: "Images execution kind", channelKind: "images", want: ModelRoutingIntentExactOnly},
		{name: "Vectors execution kind", channelKind: "vectors", want: ModelRoutingIntentExactOnly},
		{name: "Empty kind", channelKind: "", want: ModelRoutingIntentExactOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyModelRoutingIntent(tt.channelKind); got != tt.want {
				t.Fatalf("ClassifyModelRoutingIntent(%q) = %q, want %q",
					tt.channelKind, got, tt.want)
			}
		})
	}
}

func TestModelResolver_ExactOnlyRejectsCrossModelMapping(t *testing.T) {
	tests := []struct {
		name           string
		channelKind    string
		requestModel   string
		candidateModel string
		family         ModelFamily
	}{
		{name: "Claude on chat kind", channelKind: "chat", requestModel: "claude-sonnet-5", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "DeepSeek on gemini kind", channelKind: "gemini", requestModel: "deepseek-chat", candidateModel: "glm-5.2", family: ModelFamilyGLM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := routingPolicyProfile(tt.channelKind, tt.candidateModel, tt.family)
			resolver := newTestResolver(t, []ModelProfile{profile})

			target, resolved, reason := resolver.ResolveModel(
				tt.requestModel, "ch_test", tt.channelKind, "metrics_test", CapabilityFloor{})
			if resolved || target.Model != tt.requestModel || reason != "exact_model_required" {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want (%q, false, exact_model_required)",
					target.Model, resolved, reason, tt.requestModel)
			}

			target, found, reason := resolver.ResolveModelAnyEndpoint(
				tt.requestModel, "ch_test", tt.channelKind)
			if found || target.Model != tt.requestModel || reason != "exact_model_required" {
				t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want (%q, false, exact_model_required)",
					target.Model, found, reason, tt.requestModel)
			}
		})
	}
}

func TestModelResolver_ExactOnlyFindsSameNormalizedModel(t *testing.T) {
	exact := routingPolicyProfile("chat", "DeepSeek-Chat", ModelFamilyDeepSeek)
	alternative := routingPolicyProfile("chat", "deepseek-reasoner", ModelFamilyDeepSeek)
	alternative.ProbeLatencyMs = 1
	resolver := newTestResolver(t, []ModelProfile{alternative, exact})

	target, resolved, reason := resolver.ResolveModel(
		" deepseek-chat ", "ch_test", "chat", "metrics_test", CapabilityFloor{})
	if !resolved || target.Model != "DeepSeek-Chat" || reason != "found_exact_model_in_profile" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want exact DeepSeek model", target.Model, resolved, reason)
	}

	target, found, reason := resolver.ResolveModelAnyEndpoint("deepseek-chat", "ch_test", "chat")
	if !found || target.Model != "DeepSeek-Chat" || reason != "found_exact_model_in_profile" {
		t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want exact DeepSeek model", target.Model, found, reason)
	}
}

func TestModelResolver_ExactOnlyAcceptsDocumentedCompatibilityAlias(t *testing.T) {
	flash := routingPolicyProfile("chat", "deepseek-v4-flash", ModelFamilyDeepSeek)
	pro := routingPolicyProfile("chat", "deepseek-v4-pro", ModelFamilyDeepSeek)
	pro.ProbeLatencyMs = 1
	resolver := newTestResolver(t, []ModelProfile{pro, flash})

	target, resolved, reason := resolver.ResolveModel(
		"deepseek-chat", "ch_test", "chat", "metrics_test", CapabilityFloor{})
	if !resolved || target.Model != "deepseek-v4-flash" || reason != "found_equivalent_model_in_profile" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want documented DeepSeek compatibility alias", target.Model, resolved, reason)
	}

	target, found, reason := resolver.ResolveModelAnyEndpoint("deepseek-chat", "ch_test", "chat")
	if !found || target.Model != "deepseek-v4-flash" || reason != "found_equivalent_model_in_profile" {
		t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want documented DeepSeek compatibility alias", target.Model, found, reason)
	}
}

func TestModelResolver_AdaptiveEntrypointsAllowSubstitution(t *testing.T) {
	tests := []struct {
		name           string
		channelKind    string
		requestModel   string
		candidateModel string
		family         ModelFamily
	}{
		{name: "Claude alias", channelKind: "messages", requestModel: "opus", candidateModel: "mimo-v2.5-pro", family: ModelFamilyMiMo},
		{name: "Claude full model", channelKind: "messages", requestModel: "claude-sonnet-5", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "GPT 5.6", channelKind: "responses", requestModel: "gpt-5.6-sol", candidateModel: "mimo-v2.5-pro", family: ModelFamilyMiMo},
		{name: "GPT 6 astra", channelKind: "responses", requestModel: "gpt-6-astra", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "Codex auto review", channelKind: "responses", requestModel: "codex-auto-review", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "Third-party model on messages", channelKind: "messages", requestModel: "deepseek-chat", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "Third-party model on responses", channelKind: "responses", requestModel: "glm-5.2", candidateModel: "deepseek-chat", family: ModelFamilyDeepSeek},
		{name: "Unknown model on responses", channelKind: "responses", requestModel: "vendor-model-x", candidateModel: "mimo-v2.5-pro", family: ModelFamilyMiMo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := routingPolicyProfile(tt.channelKind, tt.candidateModel, tt.family)
			resolver := newTestResolver(t, []ModelProfile{profile})

			target, resolved, reason := resolver.ResolveModel(
				tt.requestModel, "ch_test", tt.channelKind, "metrics_test", CapabilityFloor{})
			if !resolved || target.Model != tt.candidateModel || reason == "" {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want adaptive mapping to %q",
					target.Model, resolved, reason, tt.candidateModel)
			}

			target, found, reason := resolver.ResolveModelAnyEndpoint(
				tt.requestModel, "ch_test", tt.channelKind)
			if !found || target.Model != tt.candidateModel || reason == "" {
				t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want adaptive mapping to %q",
					target.Model, found, reason, tt.candidateModel)
			}
		})
	}
}

func routingPolicyProfile(channelKind, modelID string, family ModelFamily) ModelProfile {
	profile := makeModelProfile(modelID, family, QualityTierHigh, 1_000_000,
		true, true, true, true, 50)
	profile.ChannelKind = channelKind
	return profile
}
