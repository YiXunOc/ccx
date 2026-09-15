package common

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/gin-gonic/gin"
)

// 竞速质量闸门测试：伪工具调用标记检测 + 软裁决边界。

func TestDetectPseudoToolCallMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"空串", "", false},
		{"干净文本", "我来执行这个命令。git log 的输出如下：a6c93a54", false},
		{"Qwen tool_call", `我来执行。<tool_call>
{"name": "exec"}`, true},
		{"Qwen tool_calls 复数", "<tool_calls>\nfoo", true},
		{"DeepSeek 官方标记", "分析中\n<｜tool▁calls▁begin｜>function", true},
		{"DSML 前缀", "<｜DSML｜tool_calls>\ninvoke", true},
		{"DSML ToolCode（实测形态）", "<｜DSML｜ToolCode>", true},
		{"Qwen function 变体（实测形态）", "</function>\n<parameter=timeout>10</parameter>\n</tool_call>", true},
		{"仅闭标记段（实测漏网形态）", "git log --oneline -1\n</parameter></function></tool_call>", true},
		{"DeepSeek tool_return（实测形态）", "<tool_return>\n<return>......", true},
		{"大小写不敏感", "<TOOL_CALL>", true},
		{"正文讨论 tool 一词不算", "tool call 是模型调工具的机制", false},
		{"HTML 标签不算", "<div>hello</div>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectPseudoToolCallMarker(tt.text); got != tt.want {
				t.Fatalf("DetectPseudoToolCallMarker(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func newQualityGateTestContext(t *testing.T, withGate bool, requestBody string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(requestBody))
	if requestBody != "" {
		c.Set("requestBodyBytes", []byte(requestBody))
	}
	if withGate {
		c.Set(racing.ContextKeyGate, racing.NewGate())
		c.Set(racing.ContextKeyRole, racing.RoleShadow)
	}
	return c, w
}

const toolsRequestBody = `{"model":"gpt-6-astra","stream":true,"tools":[{"type":"function","function":{"name":"exec"}}],"input":"run git log"}`

func TestRacingClaimClientCommitForStream(t *testing.T) {
	t.Run("无闸门直连放行（伪标记也不拦）", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, false, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "<tool_call>boom") {
			t.Fatal("无闸门路径必须零开销放行，行为与直连一致")
		}
	})
	t.Run("带工具+伪标记让出提交权", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if RacingClaimClientCommitForStream(c, "我来执行。<tool_call>{\"name\":\"exec\"}") {
			t.Fatal("伪标记分支应让出提交权")
		}
	})
	t.Run("带工具+干净文本正常 claim", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "PONG") {
			t.Fatal("干净文本应正常 claim")
		}
	})
	t.Run("无工具请求伪标记不拦", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, `{"model":"gpt-x","stream":true,"input":"写一段 DSML 教程"}`)
		if !RacingClaimClientCommitForStream(c, "<｜DSML｜tool_calls> 示例") {
			t.Fatal("不带 tools 的请求不做伪标记校验")
		}
	})
	t.Run("空缓冲不拦", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "") {
			t.Fatal("空缓冲应正常 claim")
		}
	})
}
