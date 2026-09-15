package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// 竞速闸门回归测试：流式 preflight 通过后必须经 RacingClaimClientCommit 裁决。
// 漏掉裁决时（2026-09-12 codex 全量重连事故的根因），分支 writer 缓冲的响应
// 永远不会 Commit 到真实客户端 writer，客户端拿到空 200 后只能断线重连。

func newRacingClaimTestStream(t *testing.T) (*http.Response, *types.ResponsesRequest) {
	t.Helper()
	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}
	go func() {
		defer errutil.IgnoreDeferred(writer.Close)
		writeSSE := func(s string) { _, _ = io.WriteString(writer, s) }
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"PONG\"}\n\n")
		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_racing_001\",\"status\":\"completed\",\"output\":[]},\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}\n\n")
	}()
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: "hello", Stream: true}
	return resp, originalReq
}

// 败者：闸门已被其他分支 claim，本分支必须以 ErrRacingSuperseded 收尾且零字节写出。
func TestHandleStreamSuccess_RacingLostBranchReturnsSuperseded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	gate := racing.NewGate()
	if !gate.ClaimBy(7) {
		t.Fatal("预置闸门 claim 失败")
	}
	c.Set(racing.ContextKeyGate, gate)
	c.Set(racing.ContextKeyRole, racing.RoleShadow)

	resp, originalReq := newRacingClaimTestStream(t)

	_, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		session.NewSessionManager(time.Hour, 100, 100000),
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	)
	if !strings.Contains(errN(err), racing.ErrRacingSuperseded.Error()) {
		t.Fatalf("期望 ErrRacingSuperseded，实际 err = %v", err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("败者分支不得写出任何字节，实际写出 %d 字节", w.Body.Len())
	}
}

// 赢家：闸门空闲，本分支 claim 成功后内容必须真正到达客户端 writer。
func TestHandleStreamSuccess_RacingWinnerBridgesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	gate := racing.NewGate()
	c.Set(racing.ContextKeyGate, gate)
	c.Set(racing.ContextKeyRole, racing.RolePrimary)

	resp, originalReq := newRacingClaimTestStream(t)

	usage, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		session.NewSessionManager(time.Hour, 100, 100000),
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	)
	if err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}
	_ = usage
	if !strings.Contains(w.Body.String(), "PONG") {
		t.Fatalf("赢家分支的响应体必须到达客户端，实际 body = %q", w.Body.String())
	}
}

// 质量闸门：带工具请求 + 伪工具调用标记文本（tool_choice=auto 下模型把
// 工具调用写成 Qwen/DSML 标记文本）时，即使闸门空闲也不得 claim——
// 分支按 ErrRacingSuperseded 让位，主分支/其他影子继续服务。
func TestHandleStreamSuccess_RacingQualityGateBlocksPseudoToolMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	requestBody := `{"model":"gpt-5","stream":true,"tools":[{"type":"function","name":"exec"}],"input":"hi"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	c.Set("requestBodyBytes", []byte(requestBody))

	gate := racing.NewGate()
	c.Set(racing.ContextKeyGate, gate)
	c.Set(racing.ContextKeyRole, racing.RoleShadow)

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}
	go func() {
		defer errutil.IgnoreDeferred(writer.Close)
		writeSSE := func(s string) { _, _ = io.WriteString(writer, s) }
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"我来执行。<tool_call>{\\\"name\\\":\\\"exec\\\"}\"}\n\n")
		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_pseudo_001\",\"status\":\"completed\",\"output\":[]}}\n\n")
	}()

	originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: "hi", Stream: true}
	_, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		session.NewSessionManager(time.Hour, 100, 100000),
		time.Now(),
		originalReq,
		[]byte(requestBody),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	)
	if !strings.Contains(errN(err), racing.ErrRacingSuperseded.Error()) {
		t.Fatalf("伪标记分支应以 ErrRacingSuperseded 让位，实际 err = %v", err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("让位分支不得写出任何字节，实际写出 %d 字节", w.Body.Len())
	}
}

// 观察窗：竞速影子 + 带工具请求的伪标记在第 3+ delta 才出现（实测 qwen 系
// 形态——首段干净文本、标记紧跟其后）时，延长观察的 preflight 必须捕获并
// 让位；主分支（RolePrimary）不受观察窗影响，保持原有放行节奏正常透传。
func TestHandleStreamSuccess_PseudoMarkerLateDeltaCaughtForShadowOnly(t *testing.T) {
	run := func(t *testing.T, role string) (string, error) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		requestBody := `{"model":"gpt-5","stream":true,"tools":[{"type":"function","name":"exec"}],"input":"hi"}`
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
		c.Set("requestBodyBytes", []byte(requestBody))
		c.Set(racing.ContextKeyGate, racing.NewGate())
		c.Set(racing.ContextKeyRole, role)

		reader, writer := io.Pipe()
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       reader,
		}
		go func() {
			defer errutil.IgnoreDeferred(writer.Close)
			writeSSE := func(s string) { _, _ = io.WriteString(writer, s) }
			writeSSE("event: response.output_item.added\n")
			writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
			// delta 1-2：干净文本（旧窗口在第 2 个有效内容即放行，捕获不到后续）
			writeSSE("event: response.output_text.delta\n")
			writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"git log --oneline -1\"}\n\n")
			writeSSE("event: response.output_text.delta\n")
			writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"\\n\"}\n\n")
			// delta 3：伪标记（实测漏网形态）
			writeSSE("event: response.output_text.delta\n")
			writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"</parameter></function></tool_call>\"}\n\n")
			writeSSE("event: response.completed\n")
			writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":4,\"response\":{\"id\":\"resp_late_001\",\"status\":\"completed\",\"output\":[]}}\n\n")
		}()

		originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: "hi", Stream: true}
		_, err := handleStreamSuccess(
			c, resp, "responses",
			&config.EnvConfig{LogLevel: "info"},
			session.NewSessionManager(time.Hour, 100, 100000),
			time.Now(),
			originalReq,
			[]byte(requestBody),
			common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
		)
		return w.Body.String(), err
	}

	t.Run("影子分支让位", func(t *testing.T) {
		_, err := run(t, racing.RoleShadow)
		if err == nil || !strings.Contains(err.Error(), racing.ErrRacingSuperseded.Error()) {
			t.Fatalf("影子分支伪标记应让位，实际 err = %v", err)
		}
	})
	t.Run("主分支正常透传不受观察窗影响", func(t *testing.T) {
		body, err := run(t, racing.RolePrimary)
		if err != nil {
			t.Fatalf("主分支不应受观察窗影响，实际 err = %v", err)
		}
		if !strings.Contains(body, "git log") {
			t.Fatalf("主分支应正常透传内容，body = %q", body)
		}
	})
}

func errN(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
