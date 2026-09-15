// Package alpha 提供 Codex 记忆层（history/notes）数据面端点的透传处理器。
//
// Codex CLI 的 token budget 记忆系统通过 namespace 工具（history.*/notes.*）
// 访问服务端历史与笔记，后端请求直接发往模型 provider 的 base_url：
// POST {base}/alpha/history/v2/{list_windows,list_items,read_item,search_contents}
// POST {base}/alpha/notes/v2/{list_files_by_prefix,read_file,search_contents,append_to_file,write_file}
// Codex 指向 CCX 时这些请求打到 /v1/alpha/*，本包将其透传到当前 Responses 渠道池。
//
// 关键语义：
//   - 记忆数据按上游账号隔离 → 同 session_id 的请求粘同一渠道（Trace 亲和），
//     首次请求按上下文桶回退扫描推理请求写入的亲和（跨端点粘性，尽力而为）；
//   - 指标恒记失败使用独立 identity（serviceType 标签 "Alpha"），且不写
//     RecordSuccess/RecordFailure/MarkKeyAsFailed——中转站普遍不支持 alpha 端点，
//     失败并入推理指标会把渠道打成不健康，污染推理路由；
//   - 上游 404 等错误如实透传，Codex 客户端自行降级为无记忆模式。
package alpha

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// alphaMetricsServiceType 独立指标标签：与推理指标（Responses）隔离，
// alpha 端点在中转站上的普遍缺失不得影响推理健康度。
const alphaMetricsServiceType = "Alpha"

// Handler 构建 /v1/alpha/* 透传处理器
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			return
		}

		sessionID := extractAlphaSessionID(bodyBytes)
		operation := extractAlphaOperation(c.Request.URL.Path)
		common.SetRequestLogContext(c, sessionID, 0)
		common.RequestLogf(c, "[Alpha] %s 请求 (session: %s)", operation, sessionID)

		// 粘性优先级：X-Channel 显式 pin > 推理亲和桶扫描（跨端点尽力而为）
		// > SelectChannel 自身的无桶亲和（alpha 写、alpha 读，自粘）
		pinChannel := c.GetHeader("X-Channel")
		if pinChannel == "" && sessionID != "" {
			pinChannel = channelScheduler.PreferredChannelNameForUserSweep(sessionID, scheduler.ChannelKindResponses)
		}

		if channelScheduler.IsMultiChannelMode(scheduler.ChannelKindResponses) {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, sessionID, operation, pinChannel)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, operation)
		}
	})
}

// handleSingleChannel 单渠道模式（带 key 轮转）
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	operation string,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentResponsesUpstreamWithIndex()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "未配置任何 Responses 渠道"})
		return
	}
	if len(upstream.APIKeys) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "当前渠道未配置 API 密钥"})
		return
	}

	channelLogStore := channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses)
	failedKeys := make(map[string]bool)
	var lastStatus int
	var lastBody []byte
	for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
		apiKey, err := cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		if err != nil {
			break
		}
		attemptStart := time.Now()
		handled, status, body := tryAlphaWithKey(c, upstream, apiKey, bodyBytes, envCfg, operation)
		metricsKey := metrics.GenerateMetricsIdentityKey(upstream.BaseURL, apiKey, alphaMetricsServiceType)
		common.RecordChannelLog(channelLogStore, metricsKey, channelIndex, "", operation,
			status, time.Since(attemptStart).Milliseconds(), handled, apiKey, upstream.BaseURL,
			"", "Responses", attempt > 0, upstream.Name)
		if handled {
			return
		}
		failedKeys[apiKey] = true
		lastStatus, lastBody = status, body
	}

	// 候选耗尽：回写最后一次上游响应（如 404=上游未实现 alpha），
	// 让客户端按真实状态降级，而非误导性的 503
	if lastBody != nil {
		c.Data(lastStatus, "application/json", lastBody)
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "All upstream channels are currently unavailable"})
}

// handleMultiChannel 多渠道模式（渠道故障转移 + session 粘性）
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	sessionID string,
	operation string,
	pinChannel string,
) {
	failedChannels := make(map[int]bool)
	maxAttempts := channelScheduler.GetActiveChannelCount(scheduler.ChannelKindResponses)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	channelLogStore := channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses)
	var lastUpstreamStatus int
	var lastUpstreamBody []byte

	for attempt := 0; attempt < maxAttempts; attempt++ {
		selection, err := channelScheduler.SelectChannel(
			c.Request.Context(), sessionID, failedChannels,
			scheduler.ChannelKindResponses, "", c.Param("routePrefix"), pinChannel)
		if err != nil {
			break
		}

		upstream := selection.Upstream
		channelIndex := selection.ChannelIndex
		if len(upstream.APIKeys) == 0 {
			failedChannels[channelIndex] = true
			continue
		}

		success, successKey, lastStatus, lastBody := tryAlphaChannelWithAllKeys(
			c, upstream, channelIndex, operation, cfgManager, channelLogStore, bodyBytes, envCfg)
		if success {
			// 只有真正成功才写 Trace 亲和：同 session 的后续记忆请求粘住该渠道
			if successKey != "" && sessionID != "" {
				channelScheduler.SetTraceAffinity(sessionID, channelIndex, scheduler.ChannelKindResponses)
			}
			return
		}

		failedChannels[channelIndex] = true
		// pin 的渠道失败后放开，让后续尝试走正常调度
		pinChannel = ""
		if lastBody != nil {
			lastUpstreamStatus, lastUpstreamBody = lastStatus, lastBody
		}
	}

	if lastUpstreamBody != nil {
		c.Data(lastUpstreamStatus, "application/json", lastUpstreamBody)
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "All upstream channels are currently unavailable"})
}

// tryAlphaChannelWithAllKeys 尝试渠道的所有 key
func tryAlphaChannelWithAllKeys(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	channelIndex int,
	operation string,
	cfgManager *config.ConfigManager,
	channelLogStore *metrics.ChannelLogStore,
	bodyBytes []byte,
	envCfg *config.EnvConfig,
) (bool, string, int, []byte) {
	failedKeys := make(map[string]bool)
	var lastStatus int
	var lastBody []byte
	for attempt := 0; attempt < len(upstream.APIKeys); attempt++ {
		apiKey, err := cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		if err != nil {
			break
		}
		attemptStart := time.Now()
		handled, status, body := tryAlphaWithKey(c, upstream, apiKey, bodyBytes, envCfg, operation)
		metricsKey := metrics.GenerateMetricsIdentityKey(upstream.BaseURL, apiKey, alphaMetricsServiceType)
		common.RecordChannelLog(channelLogStore, metricsKey, channelIndex, "", operation,
			status, time.Since(attemptStart).Milliseconds(), handled, apiKey, upstream.BaseURL,
			"", "Responses", attempt > 0, upstream.Name)
		if handled {
			return true, apiKey, status, body
		}
		failedKeys[apiKey] = true
		lastStatus, lastBody = status, body
	}
	return false, "", lastStatus, lastBody
}

// tryAlphaWithKey 用单个 key 透传 alpha 请求。
// 返回 handled=true 表示请求已终态回写（成功，或非故障转移错误）；false 表示该
// key 故障应换下一个，此时返回未回写的最后上游响应供候选耗尽时兜底回写。
// 注意：刻意不写 RecordSuccess/RecordFailure/MarkKeyAsFailed——alpha 失败
// 不能污染推理路由的健康度与 key 池。
func tryAlphaWithKey(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	bodyBytes []byte,
	envCfg *config.EnvConfig,
	operation string) (bool, int, []byte) {
	targetURL := buildAlphaURL(upstream, c)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		common.RequestLogf(c, "[Alpha] 创建请求失败: %v", err)
		return false, 0, nil
	}

	// 克隆客户端头（x-openai-encrypted-tool-arguments 等非标准头自动透传），
	// 再替换认证与应用渠道自定义头
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	req.Header.Del("authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
	if len(req.Header.Get("Content-Type")) == 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	req = common.WithRequestLogContext(req, c)

	resp, err := common.SendRequest(req, upstream, envCfg, false, "Alpha")
	if err != nil {
		common.RequestLogf(c, "[Alpha] %s 请求失败: %v", operation, err)
		return false, 0, nil
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	respBody, _ := io.ReadAll(resp.Body)
	respBody = utils.DecompressGzipIfNeeded(resp, respBody)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		shouldFailover, _ := common.ShouldRetryWithNextKey(resp.StatusCode, respBody, "Responses")
		// 404 = 上游未实现 alpha 端点（中转站常态），也允许换渠道尝试
		if !shouldFailover && resp.StatusCode != http.StatusNotFound {
			utils.ForwardResponseHeaders(resp.Header, c.Writer)
			c.Data(resp.StatusCode, "application/json", respBody)
			return true, resp.StatusCode, respBody
		}
		common.RequestLogf(c, "[Alpha] %s 上游返回 %d，尝试下一候选", operation, resp.StatusCode)
		return false, resp.StatusCode, respBody
	}

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Data(resp.StatusCode, "application/json", respBody)
	return true, resp.StatusCode, respBody
}

// extractAlphaSessionID 从请求体提取 context.session_id（Codex 记忆层请求的粘性键）
func extractAlphaSessionID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var req struct {
		Context struct {
			SessionID string `json:"session_id"`
		} `json:"context"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Context.SessionID
}

// extractAlphaOperation 从路径提取操作名（history/notes），用于日志归因
func extractAlphaOperation(path string) string {
	idx := strings.Index(path, "/alpha/")
	if idx < 0 {
		return "alpha"
	}
	rest := path[idx+len("/alpha/"):]
	if slash := strings.Index(rest, "/"); slash > 0 {
		return rest[:slash]
	}
	if rest != "" {
		return rest
	}
	return "alpha"
}

var alphaBaseURLVersionPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

// buildAlphaURL 构建上游 alpha 端点 URL，拼接语义与 compact 端点一致（用裸 BaseURL，
// 不走 GetEffectiveBaseURL 的版本尾规范化）：BaseURL 含版本尾或以 # 结尾 → 直接拼
// 剥掉 /v1 前缀的进站路径；否则补 /v1（与 /v1/responses 的拼法对齐）。
func buildAlphaURL(upstream *config.UpstreamConfig, c *gin.Context) string {
	baseURL := strings.TrimSuffix(upstream.BaseURL, "/")
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}

	path := c.Request.URL.Path
	if routePrefix := c.Param("routePrefix"); routePrefix != "" {
		path = strings.TrimPrefix(path, "/"+routePrefix)
	}
	endpoint := strings.TrimPrefix(path, "/v1")

	if alphaBaseURLVersionPattern.MatchString(baseURL) || skipVersionPrefix {
		return baseURL + endpoint
	}
	return baseURL + "/v1" + endpoint
}
