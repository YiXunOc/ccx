package responses

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestHandleStreamSuccess_WhitespaceAroundContentPreserved(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "eof", true: "completed"}[completed], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			stream := ""
			for _, delta := range []string{"  ", "hello", " \t"} {
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "output_index": 0, "delta": delta})
			}
			if completed {
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"status": "completed", "output": []interface{}{}}})
			}
			resp := responsesFoldTestResp(stream)
			_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000})
			if err != nil || !c.Writer.Written() {
				t.Fatalf("valid content not committed: %v", err)
			}
			iterator := newResponsesSSEIterator(io.NopCloser(strings.NewReader(w.Body.String())))
			text := ""
			count := 0
			for {
				frame, ok, err := iterator.next()
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				if frame.event["type"] == "response.output_text.delta" {
					text += stringFromInterface(frame.event["delta"])
					count++
				}
			}
			if text != "  hello \t" || count != 3 {
				t.Fatalf("whitespace modified or repeated: %q count=%d", text, count)
			}
			if !completed && (strings.Contains(w.Body.String(), "response.completed") || !strings.Contains(w.Body.String(), "response.incomplete")) {
				t.Fatal("EOF fabricated completion")
			}
		})
	}
}

func TestHandleStreamSuccess_EmptyEndRemainsUncommitted(t *testing.T) {
	for _, readError := range []bool{false, true} {
		t.Run(map[bool]string{false: "eof", true: "read_error"}[readError], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp", "output": []interface{}{}}}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "output_index": 0, "delta": "  "})
			var reader io.Reader = strings.NewReader(stream)
			if readError {
				reader = io.MultiReader(reader, foldStreamErrorReader{})
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}
			_, err := handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{})
			want := common.ErrEmptyStreamResponse
			if readError {
				want = common.ErrStreamReadFailure
			}
			if !errors.Is(err, want) || w.Body.Len() != 0 || c.Writer.Written() {
				t.Fatalf("empty stream committed: err=%v body=%q", err, w.Body.String())
			}
		})
	}
}

func TestHandleStreamSuccess_ConfirmedChatFinishStillCompletes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw := []byte(`{"model":"gpt-5","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
	stream := `data: {"id":"chat","choices":[{"index":0,"delta":{"content":"hello"}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
	_, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{})
	if err != nil || strings.Count(w.Body.String(), `"type":"response.completed"`) != 1 || strings.Contains(w.Body.String(), "response.incomplete") {
		t.Fatalf("confirmed finish rejected: err=%v body=%s", err, w.Body.String())
	}
}

func TestHandleStreamSuccess_AbnormalEndDoesNotComplete(t *testing.T) {
	for _, ending := range []string{"eof", "read_error", "response.failed", "response.incomplete"} {
		t.Run(ending, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			request := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(request)))
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "output_index": 0, "delta": "visible"})
			if strings.HasPrefix(ending, "response.") {
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": ending, "response": map[string]interface{}{"status": strings.TrimPrefix(ending, "response."), "usage": map[string]interface{}{"input_tokens": 12, "output_tokens": 3}, "error": map[string]interface{}{"message": "upstream stopped"}}})
			}
			var reader io.Reader = strings.NewReader(stream)
			if ending == "read_error" {
				reader = io.MultiReader(reader, foldStreamErrorReader{})
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}
			_, _ = handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, request, common.StreamPreflightTimeouts{})
			body := w.Body.String()
			if !strings.HasSuffix(body, "\n\n") {
				t.Fatalf("terminal SSE frame not closed: %q", body)
			}
			if strings.Contains(body, "response.completed") || strings.Contains(body, "[DONE]") {
				t.Fatalf("abnormal stream fabricated success: %s", body)
			}
			want := ending
			if ending == "eof" || ending == "read_error" {
				want = "response.incomplete"
			}
			if strings.Count(body, `"type":"`+want+`"`) != 1 {
				t.Fatalf("want one %s: %s", want, body)
			}
		})
	}
}

type foldStreamErrorReader struct{}

func (foldStreamErrorReader) Read([]byte) (int, error) { return 0, errors.New("upstream read failure") }

func TestHandleStreamSuccess_SessionWritebackPreservesReasoningEncryptedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}

	go func() {
		defer errutil.IgnoreDeferred(writer.Close)
		writeSSE := func(s string) { _, _ = io.WriteString(writer, s) }

		// reasoning output_item (passthrough Responses 流格式)
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\"}}\n\n")

		writeSSE("event: response.reasoning_summary_text.delta\n")
		writeSSE("data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"text\":\"Let me think...\"}\n\n")

		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_abc123\",\"status\":\"completed\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"Let me think...\"}],\"encrypted_content\":\"ENCRYPTED_BLOB_XYZ\"}}\n\n")

		// message output_item
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")

		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"content_index\":0,\"delta\":\"Hello!\"}\n\n")

		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello!\"}]}}\n\n")

		// completed
		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_test_001\",\"status\":\"completed\",\"output\":[]},\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n")
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	usage, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		sessionManager,
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	)
	if err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}
	_ = usage

	// 验证 session 已写入（通过 responseID 映射查找）
	sess, err := sessionManager.GetSessionByResponseID("resp_test_001")
	if err != nil {
		t.Fatalf("GetSessionByResponseID(resp_test_001) err = %v", err)
	}

	var foundReasoning bool
	var foundMessage bool
	var encContent string
	for _, item := range sess.Messages {
		switch {
		case item.Type == "reasoning":
			foundReasoning = true
			encContent = item.EncryptedContent
		case item.Type == "message" && item.Role == "assistant":
			foundMessage = true
		}
	}
	if !foundReasoning {
		t.Fatalf("session 应包含 reasoning item，当前 messages: %+v", sess.Messages)
	}
	if encContent != "ENCRYPTED_BLOB_XYZ" {
		t.Fatalf("reasoning encrypted_content 应被完整保留，期望 %q，实际 %q", "ENCRYPTED_BLOB_XYZ", encContent)
	}
	if !foundMessage {
		t.Fatalf("session 应包含 assistant message，当前 messages: %+v", sess.Messages)
	}
}

func TestHandleStreamSuccess_PostCommitStallSynthesizesIncompleteEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}

	go func() {
		defer errutil.IgnoreDeferred(writer.Close)
		writeSSE := func(s string) { _, _ = io.WriteString(writer, s) }

		// 发送一段内容以通过预检
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial\"}]}}\n\n")

		// 然后上游沉默（触发 post-commit stall）
		// 保持连接打开，不发送更多数据；stall 由 idle watchdog 触发
		time.Sleep(5 * time.Second)
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	_, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // 不关心 session 回写（此测试聚焦 stall 终端事件）
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{
			FirstContentTimeoutMs: 5000,
			InactivityTimeoutMs:   500, // 短超时以快速触发 stall
		},
	)

	// handleStreamSuccess 应返回 ErrStreamPostCommitStalled
	if !errors.Is(err, common.ErrStreamPostCommitStalled) {
		t.Fatalf("期望 ErrStreamPostCommitStalled，实际 err = %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "response.incomplete") {
		t.Fatalf("post-commit stall 应合成 response.incomplete 事件，实际输出:\n%s", body)
	}
	if !strings.Contains(body, "stream_stalled") {
		t.Fatalf("response.incomplete 应包含 incomplete_details.reason=stream_stalled，实际输出:\n%s", body)
	}
}

func TestHandleStreamSuccess_StoresReasoningEncryptedContentToThinkingCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	thinkingcache.ResetForTest()
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

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
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\"}}\n\n")
		writeSSE("event: response.reasoning_summary_text.delta\n")
		writeSSE("data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"text\":\"thinking\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_cache_test\",\"encrypted_content\":\"CACHED_ENCRYPTED_BLOB\"}}\n\n")

		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"content_index\":0,\"delta\":\"answer\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}}\n\n")

		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_cache_test\",\"status\":\"completed\",\"output\":[]},\"usage\":{\"input_tokens\":5,\"output_tokens\":5}}\n\n")
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	if _, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		sessionManager,
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	); err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}

	// 通过 responseID 找到 session，验证 reasoning encrypted_content 已存入 thinkingcache
	sess, err := sessionManager.GetSessionByResponseID("resp_cache_test")
	if err != nil {
		t.Fatalf("GetSessionByResponseID() err = %v", err)
	}

	got, ok := thinkingcache.LookupResponsesReasoning(sess.ID, "rs_cache_test")
	if !ok {
		t.Fatal("期望 thinkingcache 命中 rs_cache_test 的 encrypted_content")
	}
	if got != "CACHED_ENCRYPTED_BLOB" {
		t.Fatalf("thinkingcache 中的 encrypted_content = %q, want CACHED_ENCRYPTED_BLOB", got)
	}
}

func TestHandleStreamSuccess_ChatMalformedIdentity(t *testing.T) {
	for _, compat := range []bool{false, true} {
		for _, content := range []string{"", "visible"} {
			for _, buffered := range []bool{false, true} {
				for _, ending := range []string{"done", "finish_eof", "eof", "read_error"} {
					t.Run(fmt.Sprintf("compat=%t/text=%s/buffered=%t/%s", compat, content, buffered, ending), func(t *testing.T) {
						gin.SetMode(gin.TestMode)
						w := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(w)
						raw := []byte(fmt.Sprintf(`{"model":"m","stream":true,"transformer_metadata":{"codex_tool_compat_enabled":%t}}`, compat))
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
						stream := ""
						if content != "" {
							stream += `data: {"id":"chat","choices":[{"delta":{"content":"visible"}}]}` + "\n\n"
						}
						stream += `data: {"id":"chat","choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad","function":{"arguments":"{}"}}]}}]}` + "\n\n"
						stream += `data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}` + "\n\n"
						if ending == "done" || ending == "finish_eof" {
							stream += `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"
						}
						if ending == "done" {
							stream += "data: [DONE]\n\n"
						}
						var reader io.Reader = strings.NewReader(stream)
						if ending == "read_error" {
							reader = io.MultiReader(reader, foldStreamErrorReader{})
						}
						resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}
						timeout := 0
						if buffered {
							timeout = 10000
						}
						usage, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "m", Stream: true}, raw, common.StreamPreflightTimeouts{InactivityTimeoutMs: timeout})
						body := w.Body.String()
						if content == "" {
							want := common.ErrEmptyStreamResponse
							if ending == "read_error" {
								want = common.ErrStreamReadFailure
							}
							if !errors.Is(err, want) || c.Writer.Written() || body != "" {
								t.Fatalf("empty malformed stream committed: err=%v body=%s", err, body)
							}
							return
						}
						if err != nil || !c.Writer.Written() {
							t.Fatalf("valid buffered content discarded: err=%v body=%s", err, body)
						}
						if strings.Contains(body, "response.completed") || strings.Contains(body, "[DONE]") || strings.Contains(body, `"type":"function_call"`) || strings.Count(body, `"type":"response.incomplete"`) != 1 {
							t.Fatalf("malformed tool escaped or terminal invalid: %s", body)
						}
						iter := newResponsesSSEIterator(io.NopCloser(strings.NewReader(body)))
						text := ""
						for {
							frame, ok, e := iter.next()
							if e != nil {
								t.Fatal(e)
							}
							if !ok {
								break
							}
							if frame.event["type"] == "response.output_text.delta" {
								text += stringFromInterface(frame.event["delta"])
							}
						}
						if text != "visible" || usage == nil {
							t.Fatalf("valid content or usage lost: text=%q usage=%+v", text, usage)
						}
						if !strings.Contains(body, `"reason":"upstream_error"`) || !strings.Contains(body, `"input_tokens":12`) || !strings.Contains(body, `"output_tokens":3`) {
							t.Fatalf("abnormal reason/usage lost: %s", body)
						}
					})
				}
			}
		}
	}
}

func TestHandleStreamSuccess_ChatIncompleteUsageCache(t *testing.T) {
	for _, tc := range []struct {
		name, usage  string
		input, total int
	}{
		{"openai", `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":8}}`, 12, 15},
		{"claude", `{"input_tokens":4,"output_tokens":3,"cache_read_input_tokens":8,"cache_creation_5m_input_tokens":2,"cache_creation_1h_input_tokens":1}`, 15, 18},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"m","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			stream := `data: {"id":"chat","choices":[{"delta":{"content":"visible"}}]}` + "\n\n" + `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad","function":{"arguments":"{}"}}]}}]}` + "\n\n" + `data: {"choices":[],"usage":` + tc.usage + "}\n\ndata: [DONE]\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
			_, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "m", Stream: true}, raw, common.StreamPreflightTimeouts{})
			if err != nil {
				t.Fatal(err)
			}
			iter := newResponsesSSEIterator(io.NopCloser(strings.NewReader(w.Body.String())))
			found := false
			for {
				frame, ok, e := iter.next()
				if e != nil {
					t.Fatal(e)
				}
				if !ok {
					break
				}
				if frame.event["type"] != "response.incomplete" {
					continue
				}
				found = true
				response := frame.event["response"].(map[string]interface{})
				usage := response["usage"].(map[string]interface{})
				if jsonIntFromMap(usage, "input_tokens") != tc.input || jsonIntFromMap(usage, "total_tokens") != tc.total || jsonIntFromMap(usage, "total_tokens") != jsonIntFromMap(usage, "input_tokens")+jsonIntFromMap(usage, "output_tokens") {
					t.Fatalf("cache semantics lost: %+v", usage)
				}
				if tc.name == "openai" {
					details, ok := usage["input_tokens_details"].(map[string]interface{})
					if !ok || jsonIntFromMap(details, "cached_tokens") != 8 {
						t.Fatalf("cache detail lost: %+v", usage)
					}
				} else if jsonIntFromMap(usage, "cache_read_input_tokens") != 8 || jsonIntFromMap(usage, "cache_creation_5m_input_tokens") != 2 || jsonIntFromMap(usage, "cache_creation_1h_input_tokens") != 1 || usage["cache_ttl"] != "mixed" {
					t.Fatalf("cache TTL detail lost: %+v", usage)
				}
			}
			if !found {
				t.Fatal("missing incomplete")
			}
		})
	}
}

func TestHandleStreamSuccess_ChatMalformedAfterValidTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw := []byte(`{"model":"m","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
	stream := `data: {"id":"chat","choices":[{"delta":{"tool_calls":[{"index":0,"id":"good","function":{"name":"run","arguments":"{}"}}]}}]}` + "\n\n" + `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" + `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"bad","function":{"arguments":"{}"}}]}}]}` + "\n\ndata: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}
	_, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "m", Stream: true}, raw, common.StreamPreflightTimeouts{})
	body := w.Body.String()
	if err != nil || !c.Writer.Written() || strings.Contains(body, "response.completed") || strings.Contains(body, `"call_id":"bad"`) || strings.Count(body, `"type":"response.incomplete"`) != 1 || !strings.Contains(body, `"call_id":"good"`) {
		t.Fatalf("valid tool lost or invalid tool leaked: err=%v body=%s", err, body)
	}
}

func TestIncompleteUsageNormalizationBoundaries(t *testing.T) {
	missing := "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"output\":[]}}\n\n"
	if got := normalizeCompletedEventUsageToOpenAISemantics(missing); got != missing {
		t.Fatalf("missing usage fabricated: %s", got)
	}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw := []byte(`{"model":"m","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
	stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "delta": "visible"}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.incomplete", "response": map[string]interface{}{"status": "incomplete", "output": []interface{}{}, "usage": map[string]interface{}{"input_tokens": 12, "output_tokens": 3, "total_tokens": 15, "input_tokens_details": map[string]interface{}{"cached_tokens": 8}}}})
	_, err := handleStreamSuccess(c, responsesFoldTestResp(stream), "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "m", Stream: true}, raw, common.StreamPreflightTimeouts{})
	if err != nil || !strings.Contains(w.Body.String(), `"input_tokens":12`) || !strings.Contains(w.Body.String(), `"total_tokens":15`) {
		t.Fatalf("native usage changed: err=%v body=%s", err, w.Body.String())
	}
}
