package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// fold 路径的竞速闸门回归测试：native responses 流经 responsesFoldHTTPEmitter
// 写出，commit 前必须经 RacingClaimClientCommitForStream 裁决。漏掉裁决时
// （2026-09-12 晚间 native responses 流量全空实测——竞速武装后 fold 的全部
// 写出只进分支缓冲、无人 Commit，客户端拿到空 200），43fc0967 只修了转换器
// 路径，这是当时漏掉的第四条出口。

func newFoldRacingTestEmitter(t *testing.T, gate *racing.Gate, role string) (*responsesFoldHTTPEmitter, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))
	c.Set(racing.ContextKeyGate, gate)
	c.Set(racing.ContextKeyRole, role)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: "hi", Stream: true}
	emitter := newResponsesFoldHTTPEmitter(c, resp, session.NewSessionManager(time.Hour, 100, 100000), originalReq)
	return emitter, w
}

// 败者：闸门已被其他分支 claim，fold 出口的 commit 必须以 ErrRacingSuperseded
// 收尾且零字节写出。
func TestFoldEmitterRacingLostBranchReturnsSuperseded(t *testing.T) {
	gate := racing.NewGate()
	if !gate.ClaimBy(7) {
		t.Fatal("预置闸门 claim 失败")
	}
	emitter, w := newFoldRacingTestEmitter(t, gate, racing.RoleShadow)

	err := emitter.emit(map[string]interface{}{
		"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "PONG",
	})
	if err == nil || !strings.Contains(err.Error(), racing.ErrRacingSuperseded.Error()) {
		t.Fatalf("期望 ErrRacingSuperseded，实际 err = %v", err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("败者分支不得写出任何字节，实际写出 %d 字节", w.Body.Len())
	}
}

// 赢家：闸门空闲，fold 出口 claim 成功后内容必须真正到达客户端 writer。
func TestFoldEmitterRacingWinnerBridgesBody(t *testing.T) {
	emitter, w := newFoldRacingTestEmitter(t, racing.NewGate(), racing.RolePrimary)

	if err := emitter.emit(map[string]interface{}{
		"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "PONG",
	}); err != nil {
		t.Fatalf("emit() err = %v", err)
	}
	if !strings.Contains(w.Body.String(), "PONG") {
		t.Fatalf("赢家分支的响应体必须到达客户端，实际 body = %q", w.Body.String())
	}
}

// 质量闸门：带工具请求 + 伪工具调用标记文本时，即使闸门空闲也不得 claim——
// fold 出口同样受伪标记软校验约束（与转换器路径口径一致）。
func TestFoldEmitterRacingQualityGateBlocksPseudoToolMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	requestBody := `{"model":"gpt-5","stream":true,"tools":[{"type":"function","name":"exec"}],"input":"hi"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	c.Set("requestBodyBytes", []byte(requestBody))
	c.Set(racing.ContextKeyGate, racing.NewGate())
	c.Set(racing.ContextKeyRole, racing.RoleShadow)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: "hi", Stream: true}
	emitter := newResponsesFoldHTTPEmitter(c, resp, session.NewSessionManager(time.Hour, 100, 100000), originalReq)

	err := emitter.emit(map[string]interface{}{
		"type": "response.output_text.delta", "output_index": 0, "content_index": 0,
		"delta": "我来执行。<tool_call>{\"name\":\"exec\"}",
	})
	if err == nil || !strings.Contains(err.Error(), racing.ErrRacingSuperseded.Error()) {
		t.Fatalf("伪标记分支应以 ErrRacingSuperseded 让位，实际 err = %v", err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("让位分支不得写出任何字节，实际写出 %d 字节", w.Body.Len())
	}
}
