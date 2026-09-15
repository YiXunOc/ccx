package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

// ProbeSystemHeaderLevel 探测最优过滤层级
func ProbeSystemHeaderLevel(
	ctx context.Context,
	channelUID string,
	keyHash string,
	model string,
	baseURL string,
	apiKey string,
	cache *config.SystemHeaderFilterCache,
) (SystemHeaderFilterLevel, error) {
	// 检查缓存
	if entry := cache.Get(channelUID, keyHash, model); entry != nil {
		return SystemHeaderFilterLevel(entry.Level), nil
	}

	// 构造测试请求（最小化 token 消耗）
	testReq := buildMinimalTestRequest()

	// 从低层级到高层级探测
	for level := SystemHeaderFilterLevel(0); level <= LevelFirstBlock; level++ {
		filteredReq, _ := FilterSystemHeader(testReq, level)
		err := sendTestRequest(ctx, baseURL, apiKey, filteredReq, model)
		if err == nil {
			// 探测成功，记录到缓存
			cache.Set(channelUID, keyHash, model, int(level))
			return level, nil
		}

		// 如果错误不是 system header 相关，直接返回
		if !isSystemHeaderError(err) {
			cache.RecordFailure(channelUID, keyHash, model, err.Error())
			return level, fmt.Errorf("请求失败: %w", err)
		}
	}

	// 所有层级都失败
	cache.RecordFailure(channelUID, keyHash, model, "无法兼容任何过滤层级")
	return -1, fmt.Errorf("无法兼容任何过滤层级")
}

// buildMinimalTestRequest 构造最小测试请求
func buildMinimalTestRequest() interface{} {
	return map[string]interface{}{
		"system": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "x-anthropic-billing-header: cc_version=2.1.216.046; cc_entrypoint=cli;",
			},
			map[string]interface{}{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
			map[string]interface{}{
				"type": "text",
				"text": "You are a helpful assistant.",
			},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hi",
			},
		},
		"max_tokens": 16,
	}
}

// sendTestRequest 发送测试请求
func sendTestRequest(ctx context.Context, baseURL string, apiKey string, req interface{}, model string) error {
	// 构造完整请求
	fullReq := map[string]interface{}{
		"model":      model,
		"max_tokens": 16,
	}

	// 合并 system 和 messages
	if reqMap, ok := req.(map[string]interface{}); ok {
		if system, exists := reqMap["system"]; exists {
			fullReq["system"] = system
		}
		if messages, exists := reqMap["messages"]; exists {
			fullReq["messages"] = messages
		}
	}

	bodyBytes, err := json.Marshal(fullReq)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	// 构造 HTTP 请求
	url := strings.TrimRight(baseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	// 发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	// 检查响应状态
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// 读取错误响应
	var errResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return fmt.Errorf("HTTP %d: 无法解析响应", resp.StatusCode)
	}

	// 构造错误信息
	errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if msg, ok := errResp["error"].(map[string]interface{}); ok {
		if message, ok := msg["message"].(string); ok {
			errMsg = message
		}
	}

	return fmt.Errorf("%s", errMsg)
}

// isSystemHeaderError 判断错误是否与 system header 相关
func isSystemHeaderError(err error) bool {
	errStr := strings.ToLower(err.Error())
	// 检查是否与 system header 相关的错误
	keywords := []string{
		"system",
		"billing",
		"cch",
		"header",
		"instruction",
		"prompt",
	}
	for _, keyword := range keywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}
	return false
}
