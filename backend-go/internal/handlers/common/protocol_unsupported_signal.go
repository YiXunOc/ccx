package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// 协议端点不支持信号识别（学习闭环的写入侧识别器）。
//
// 背景：跨模型替代按协议全量放行后，ModelResolver 可能把请求映射到某渠道在
// 「请求协议」下从未验证过的模型（画像协议维与端点协议发现是两套数据源，
// 存在此侧有模型画像、彼侧 protocolModels 清单不含该模型的窗口）。上游会以
// 400 明确拒绝（如 seekai：model "glm-5.3-flash" is not supported on
// /v1/responses; use /v1/chat/completions instead，code=model_not_supported_on_endpoint）。
// 该错误一次即可确证「渠道×key×模型×执行协议」组合不可用，学习进 CompatCache
// （no_protocol_support:<protocol>），供 ModelResolver 后续替代映射时直接剔除，
// 避免每个请求都重新踩一次坑。
//
// 防误判：只认无歧义强信号——错误 code 点名 model_not_supported_on_endpoint，
// 或文案同时点到模型名、"is not supported on"短语与协议端点路径。通用
// invalid_request / 模型不存在（not_found）不学：前者归因不明，后者由画像
// 层的模型清单维护，不属于协议能力结论。

// protocolEndpointUnsupportedPatterns 上游文案点名「模型不支持该协议端点」的特征
// （小写匹配）。要求出现端点路径片段（/v1/xxx 或 /v1beta/xxx），把「模型不存在」
// 与「模型存在但不在此端点服务」区分开。
var protocolEndpointUnsupportedPatterns = []*regexp.Regexp{
	regexp.MustCompile("model .{1,128}? is not supported on (/v1[a-z0-9/._-]*)"),
	regexp.MustCompile("(/v1[a-z0-9/._-]*) (?:endpoint )?(?:does not support|doesn't support|not support) model"),
}

// protocolEndpointUnsupportedCode 上游错误 code 的强信号值（见 seekai 实测）。
const protocolEndpointUnsupportedCode = "model_not_supported_on_endpoint"

// ProtocolEndpointUnsupportedSignal 一条识别出的协议端点不支持信号。
type ProtocolEndpointUnsupportedSignal struct {
	Evidence string // 命中的错误文案/code 摘要，写入记忆便于事后追溯
}

// ProtocolEndpointUnsupportedFromError 从上游错误响应中识别「模型不支持当前协议
// 端点」信号。仅处理 400：429/5xx/超时属容量问题，422 参数校验不含端点语义。
func ProtocolEndpointUnsupportedFromError(statusCode int, bodyBytes []byte) *ProtocolEndpointUnsupportedSignal {
	if statusCode != http.StatusBadRequest || len(bodyBytes) == 0 {
		return nil
	}

	var errResp map[string]interface{}
	if json.Unmarshal(bodyBytes, &errResp) != nil {
		return nil
	}
	if code := errorObjectFieldString(errResp, "code"); strings.EqualFold(code, protocolEndpointUnsupportedCode) {
		return &ProtocolEndpointUnsupportedSignal{Evidence: "error code: " + code}
	}
	for _, msg := range extractErrorMessageFields(errResp) {
		if matchesAnyPattern(strings.ToLower(msg), protocolEndpointUnsupportedPatterns) {
			return &ProtocolEndpointUnsupportedSignal{Evidence: msg}
		}
	}
	return nil
}

// errorObjectFieldString 提取顶层或 error 对象内的字符串字段（如 code）。
func errorObjectFieldString(errResp map[string]interface{}, field string) string {
	if v, ok := errResp[field].(string); ok && v != "" {
		return v
	}
	if errObj, ok := errResp["error"].(map[string]interface{}); ok {
		if v, ok := errObj[field].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ProtocolUnsupportedLearningTrait 把执行协议编码为 CompatCache trait 键。
// protocol 传调度层 ChannelKind 字符串（responses/chat/messages/gemini）。
func ProtocolUnsupportedLearningTrait(protocol string) config.CompatTrait {
	return config.ProtocolUnsupportedTrait(protocol)
}
