package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

type sixFixBody struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *sixFixBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestSixFixScannerClosesActualBody(t *testing.T) {
	for _, ending := range []string{"terminal", "eof", "error", "timeout"} {
		t.Run(ending, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "delta": "hello"})
			if ending == "terminal" {
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp_test", "output": []interface{}{}}})
			}
			body := &sixFixBody{closed: make(chan struct{})}
			var tail io.Reader = strings.NewReader("")
			if ending == "terminal" || ending == "timeout" {
				tail = &sixFixBlockingReader{closed: body.closed}
			}
			if ending == "error" {
				tail = foldStreamErrorReader{}
			}
			if ending == "timeout" {
				stream = ""
			}
			body.Reader = io.MultiReader(strings.NewReader(stream), tail)
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}
			_, _ = handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{FirstContentTimeoutMs: 50})
			select {
			case <-body.closed:
			default:
				t.Fatal("actual response body not closed on handler exit")
			}
		})
	}
}
func TestSixFixScannerCancellation(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	body := &sixFixBody{closed: make(chan struct{})}
	body.Reader = &sixFixBlockingReader{closed: body.closed}
	_, err := handleStreamSuccess(c, &http.Response{Body: body}, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{}, nil, common.StreamPreflightTimeouts{})
	if !errors.Is(err, context.Canceled) || c.Writer.Written() {
		t.Fatalf("cancel did not leave zero-byte exit: %v", err)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("canceled body not closed")
	}
}

func sixFixUsageFailureStream(tool bool) string {
	msg := map[string]interface{}{"type": "message", "id": "msg_kept", "role": "assistant", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "kept answer"}}}
	output := []interface{}{msg}
	if tool {
		output = append(output, map[string]interface{}{"type": "function_call", "id": "fc_bad", "call_id": "call_bad", "name": "read", "arguments": "{}"})
	}
	return formatResponsesFoldSSE(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp_kept"}}) + formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp_kept", "output": output, "usage": map[string]interface{}{"input_tokens": 40, "output_tokens": 9, "total_tokens": 49, "input_tokens_details": map[string]interface{}{"cached_tokens": 2}, "output_tokens_details": map[string]interface{}{"reasoning_tokens": 3}}}})
}

func sixFixAssertRecoveryWire(t *testing.T, wire string, index int) map[string]interface{} {
	t.Helper()
	var terminal map[string]interface{}
	incomplete, added, done, text, lastSeq := 0, false, false, "", -1
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var e map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		seq := intFromInterfaceDefault(e["sequence_number"])
		if seq <= lastSeq {
			t.Fatalf("sequence order changed: %s", wire)
		}
		lastSeq = seq
		switch e["type"] {
		case "response.completed", "response.function_call_arguments.done":
			t.Fatalf("false completion: %v", e)
		case "response.incomplete":
			incomplete++
			r := mapFromInterface(e["response"])
			terminal = r
			if r["id"] != "resp_kept" || mapFromInterface(r["incomplete_details"])["reason"] != "upstream_error" {
				t.Fatalf("terminal identity/reason: %v", e)
			}
		case "response.output_item.added", "response.output_item.done":
			if mapFromInterface(e["item"])["id"] == "msg_kept" {
				if intFromInterfaceDefault(e["output_index"]) != index {
					t.Fatalf("message index: %v", e)
				}
				if e["type"] == "response.output_item.added" {
					if added {
						t.Fatal("duplicate added")
					}
					added = true
				} else {
					if !added || done || text != "kept answer" {
						t.Fatalf("done order: %v", e)
					}
					done = true
				}
			}
		case "response.output_text.delta":
			if !added || done || e["item_id"] != "msg_kept" || intFromInterfaceDefault(e["output_index"]) != index {
				t.Fatalf("delta identity/order: %v", e)
			}
			text += stringFromInterface(e["delta"])
		}
	}
	if incomplete != 1 || !done || text != "kept answer" {
		t.Fatalf("lost delivery/terminal: %s", wire)
	}
	return terminal
}

func TestSixFixFoldRecoveredUsage(t *testing.T) {
	for _, failure := range []string{"budget", "tool"} {
		for _, prior := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prior=%v", failure, prior), func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				stream := sixFixUsageFailureStream(failure == "tool")
				first := stream
				index, input, output, total, reasoning, agentInput := 0, 40, 9, 49, 3, 40
				if prior {
					first = strings.ReplaceAll(responsesFoldReasoningRound("prior", responsesFoldStep-2, "hidden", true), "resp_1", "resp_kept")
					index, input, output, total, reasoning, agentInput = 1, 140, 545, 685, 519, 100
				}
				emitter := newResponsesFoldHTTPEmitter(c, responsesFoldTestResp(""), nil, &types.ResponsesRequest{})
				opens := 0
				opener := func(map[string]interface{}) (*http.Response, []byte, error) {
					opens++
					return responsesFoldTestResp(stream), nil, nil
				}
				emit := func(e map[string]interface{}) error {
					if err := emitter.emit(e); err != nil {
						t.Logf("emitter rejected %v: %v", e["type"], err)
						return err
					}
					if failure == "budget" && e["type"] == "response.output_item.done" && mapFromInterface(e["item"])["id"] == "msg_kept" {
						// Exhaust capacity after legal delivery and terminal usage collection,
						// before retaining the same item's final snapshot.
						return emitter.budget.reserve(responsesRetainedByteLimit - emitter.budget.used)
					}
					return nil
				}
				result, err := runResponsesFoldWithBudget(map[string]interface{}{}, responsesFoldTestResp(first), opener, emit, nil, emitter.budget)
				if err != nil {
					t.Fatal(err)
				}
				if result.BilledUsage == nil {
					t.Fatal("result.BilledUsage=nil after recovered postcommit failure")
				}
				b := result.BilledUsage
				if intFromInterfaceDefault(b["input_tokens"]) != input || intFromInterfaceDefault(b["output_tokens"]) != output || intFromInterfaceDefault(b["total_tokens"]) != total || intFromInterfaceDefault(mapFromInterface(b["input_tokens_details"])["cached_tokens"]) != 2 || intFromInterfaceDefault(mapFromInterface(b["output_tokens_details"])["reasoning_tokens"]) != reasoning {
					t.Fatalf("lost/double billed usage: %v", b)
				}
				if result.AgentUsage == nil || intFromInterfaceDefault(result.AgentUsage["input_tokens"]) != agentInput || intFromInterfaceDefault(result.AgentUsage["output_tokens"]) != reasoning {
					t.Fatalf("lost/changed agent usage: %v", result.AgentUsage)
				}
				if opens != map[bool]int{false: 0, true: 1}[prior] {
					t.Fatalf("unexpected opens %d", opens)
				}
				terminal := sixFixAssertRecoveryWire(t, w.Body.String(), index)
				if !reflect.DeepEqual(terminal["usage"], cloneFoldTestMap(result.AgentUsage)) || !reflect.DeepEqual(mapFromInterface(terminal["metadata"])["proxy_billed_usage"], cloneFoldTestMap(result.BilledUsage)) {
					t.Fatalf("terminal and returned usage disagree: %v %+v", terminal, result)
				}
				if intFromInterfaceDefault(result.AgentUsage["total_tokens"]) != agentInput+reasoning {
					t.Fatalf("agent total changed: %v", result.AgentUsage)
				}
			})
		}
	}
}

func TestSixFixFoldRecoveredWrapperUsage(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(fmt.Sprint(prior), func(t *testing.T) {
			stream := sixFixUsageFailureStream(true)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}))
			defer server.Close()
			first := stream
			input, output, index := 40, 9, 0
			if prior {
				first = strings.ReplaceAll(responsesFoldReasoningRound("prior", responsesFoldStep-2, "hidden", true), "resp_1", "resp_kept")
				input, output, index = 140, 545, 1
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			usage, err := handleFoldedResponsesStreamSuccess(c, responsesFoldTestResp(first), &providers.ResponsesProvider{}, &config.UpstreamConfig{BaseURL: server.URL}, "test-only-key", &config.EnvConfig{}, nil, &types.ResponsesRequest{Model: "gpt-5", Stream: true}, []byte("{}"))
			want := &types.Usage{InputTokens: input, OutputTokens: output, CacheReadInputTokens: 2, PromptTokensTotal: input}
			if err != nil || !reflect.DeepEqual(usage, want) {
				t.Fatalf("wrapper usage got=%+v want=%+v err=%v", usage, want, err)
			}
			if requests != map[bool]int{false: 0, true: 1}[prior] {
				t.Fatalf("unexpected HTTP retries %d", requests)
			}
			sixFixAssertRecoveryWire(t, w.Body.String(), index)
		})
	}
}

type sixFixHandshakeReader struct {
	entered chan struct{}
	exited  chan struct{}
	closed  <-chan struct{}
}

func (r *sixFixHandshakeReader) Read([]byte) (int, error) {
	close(r.entered)
	<-r.closed
	close(r.exited)
	return 0, io.ErrClosedPipe
}

func TestSixFixScannerCancellationAfterReadEntered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		body := &sixFixBody{closed: make(chan struct{})}
		reader := &sixFixHandshakeReader{entered: make(chan struct{}), exited: make(chan struct{}), closed: body.closed}
		body.Reader = reader
		handlerDone := make(chan error, 1)
		go func() {
			_, err := handleStreamSuccess(c, &http.Response{Body: body}, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{}, nil, common.StreamPreflightTimeouts{})
			handlerDone <- err
		}()
		<-reader.entered
		synctest.Wait()
		select {
		case <-reader.exited:
			t.Fatal("Read exited before cancellation")
		case <-handlerDone:
			t.Fatal("handler returned before cancellation")
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case <-reader.exited:
		default:
			t.Fatal("Close did not release the entered Read")
		}
		select {
		case err := <-handlerDone:
			if !errors.Is(err, context.Canceled) || c.Writer.Written() {
				t.Fatalf("cancel did not leave zero-byte exit: %v", err)
			}
		default:
			t.Fatal("handler did not return after cancellation")
		}
		// The bubble cannot return with a surviving scanner goroutine, even if
		// the reader and handler have both returned.
	})
}

type sixFixGatedWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *sixFixGatedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.release
	})
	return w.ResponseRecorder.Write(p)
}

type sixFixEOFReader struct {
	*strings.Reader
	eof bool
}

func (r *sixFixEOFReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.eof = true
	}
	return n, err
}

func TestSixFixScannerBlockedSendExits(t *testing.T) {
	for _, ending := range []string{"line", "eof"} {
		t.Run(ending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := &sixFixGatedWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				terminal := `data: {"type":"response.completed","response":{"id":"resp_blocked_send","output":[{"type":"message","id":"msg_blocked_send","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}` + "\n"
				// One blank line fills lineChan while the handler writes the terminal.
				// More lines block the line send; otherwise EOF blocks the final send.
				tail := "\n"
				if ending == "line" {
					tail += ": one\n: two\n"
				}
				reader := &sixFixEOFReader{Reader: strings.NewReader(terminal + tail)}
				body := &sixFixBody{Reader: reader, closed: make(chan struct{})}
				handlerDone := make(chan struct{})
				go func() {
					defer close(handlerDone)
					_, _ = handleStreamSuccess(c, &http.Response{Body: body}, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{}, nil, common.StreamPreflightTimeouts{})
				}()
				<-w.entered
				synctest.Wait()
				if reader.eof != (ending == "eof") {
					t.Fatalf("scanner did not reach expected %s send boundary: EOF=%v", ending, reader.eof)
				}
				select {
				case <-handlerDone:
					t.Fatal("handler returned while its terminal write was blocked")
				default:
				}
				close(w.release)
				synctest.Wait()
				select {
				case <-handlerDone:
				default:
					t.Fatal("handler did not join scanner blocked on send")
				}
				select {
				case <-body.closed:
				default:
					t.Fatal("upstream body was not closed")
				}
				// synctest.Test also waits for the real scanner goroutine to exit.
			})
		})
	}
}

func TestSixFixRetentionBudgetBoundary(t *testing.T) {
	budget := &responsesRetentionBudget{}
	if err := budget.reserve(responsesRetainedByteLimit); err != nil {
		t.Fatal("exact limit must be permitted", err)
	}
	if err := budget.reserve(0); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(1); !errors.Is(err, common.ErrInvalidResponseBody) {
		t.Fatal("next byte must fail", err)
	}
	if budget.used != responsesRetainedByteLimit {
		t.Fatal("failed reservation changed retained count")
	}
	budget.release(10)
	if err := budget.reserve(10); err != nil {
		t.Fatal("released capacity not reusable", err)
	}
}

func TestSixFixFoldSharedBudgetAndAbnormalExit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			emitter := newResponsesFoldHTTPEmitter(c, responsesFoldTestResp(""), nil, &types.ResponsesRequest{})
			if committed {
				if err := emitter.emit(map[string]interface{}{"type": "response.output_text.delta", "delta": "keep this"}); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate other snapshots of this same request already retaining capacity.
			if err := emitter.budget.reserve(responsesRetainedByteLimit - 512); err != nil {
				t.Fatal(err)
			}
			event := map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": map[string]interface{}{"id": "rs", "type": "reasoning", "blob": strings.Repeat("x", 100)}}
			body := &sixFixBody{Reader: strings.NewReader(formatResponsesFoldSSE(event)), closed: make(chan struct{})}
			_, err := runResponsesFoldWithBudget(map[string]interface{}{}, &http.Response{Body: body}, nil, emitter.emit, nil, emitter.budget)
			select {
			case <-body.closed:
			default:
				t.Fatal("overflow did not close upstream")
			}
			if !emitter.budget.exceeded {
				t.Fatal("round+emitter did not share the retention budget")
			}
			wire := w.Body.String()
			if committed {
				if err != nil || !strings.Contains(wire, "keep this") || strings.Count(wire, `"type":"response.incomplete"`) != 1 || !strings.Contains(wire, "upstream_error") || strings.Contains(wire, "response.completed") {
					t.Fatalf("postcommit overflow: err=%v wire=%s", err, wire)
				}
			} else if !errors.Is(err, common.ErrInvalidResponseBody) || c.Writer.Written() || w.Body.Len() != 0 {
				t.Fatalf("precommit overflow must be zero bytes: %v %s", err, wire)
			}
		})
	}
}

func TestSixFixPreflightCumulativeBudget(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	stream := strings.Repeat(":"+strings.Repeat("x", 1024*1024)+"\n", 33)
	body := &sixFixBody{Reader: strings.NewReader(stream), closed: make(chan struct{})}
	_, err := handleStreamSuccess(c, &http.Response{Body: body}, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{}, nil, common.StreamPreflightTimeouts{})
	if !errors.Is(err, common.ErrInvalidResponseBody) || c.Writer.Written() || w.Body.Len() != 0 {
		t.Fatalf("cumulative preflight overflow accepted: %v", err)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("overflow body not closed")
	}
}

func TestSixFixNativeToolDoneBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw := []byte(`{"model":"gpt-5","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
	stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "delta": "hello"}) +
		formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": "read"}}) +
		formatResponsesFoldSSE(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": "{"}) +
		formatResponsesFoldSSE(map[string]interface{}{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": "{"}) +
		formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.done", "output_index": 1, "item": map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": "read", "arguments": "{"}}) +
		formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp", "output": []interface{}{}}})
	_, err := handleStreamSuccess(c, responsesFoldTestResp(stream), "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{})
	wire := w.Body.String()
	if err != nil || !strings.Contains(wire, "hello") || strings.Contains(wire, "response.function_call_arguments.done") || strings.Contains(wire, "response.output_item.done") || strings.Contains(wire, "response.completed") || strings.Count(wire, `"type":"response.incomplete"`) != 1 {
		t.Fatalf("invalid native tool completion escaped: err=%v wire=%s", err, wire)
	}
}

func TestSixFixFoldToolDoneBoundary(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	emitter := newResponsesFoldHTTPEmitter(c, responsesFoldTestResp(""), nil, &types.ResponsesRequest{})
	if err := emitter.emit(map[string]interface{}{"type": "response.output_text.delta", "delta": "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.emit(map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{"type": "function_call", "id": "fc", "name": "read", "call_id": "call"}}); err != nil {
		t.Fatal(err)
	}
	err := emitter.emit(map[string]interface{}{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": "{}"})
	if err == nil || strings.Contains(w.Body.String(), "response.function_call_arguments.done") {
		t.Fatalf("Fold emitted malformed completed tool: %v %s", err, w.Body.String())
	}
}

func TestSixFixFoldReasoningDoesNotCommit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	emitter := newResponsesFoldHTTPEmitter(c, responsesFoldTestResp(""), nil, &types.ResponsesRequest{})
	for _, event := range []map[string]interface{}{
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]interface{}{"type": "reasoning", "id": "rs"}},
		{"type": "response.reasoning_summary_text.delta", "output_index": 0, "delta": "thinking"},
	} {
		if err := emitter.emit(event); err != nil {
			t.Fatal(err)
		}
	}
	if c.Writer.Written() || emitter.committed || w.Body.Len() != 0 {
		t.Fatal("reasoning committed without deliverable content")
	}
	if err := emitter.emit(map[string]interface{}{"type": "response.output_text.delta", "output_index": 1, "delta": "answer"}); err != nil {
		t.Fatal(err)
	}
	if !emitter.committed || !strings.Contains(w.Body.String(), "thinking") || !strings.Contains(w.Body.String(), "answer") {
		t.Fatal("buffered reasoning not released with valid content")
	}
}

func TestSixFixFoldBudgetRejectsBeforeCommit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	emitter := newResponsesFoldHTTPEmitter(c, responsesFoldTestResp(""), nil, &types.ResponsesRequest{})
	err := emitter.emit(map[string]interface{}{"type": "response.unknown", "blob": strings.Repeat("x", 32*1024*1024)})
	if !errors.Is(err, common.ErrInvalidResponseBody) || c.Writer.Written() || w.Body.Len() != 0 {
		t.Fatalf("over-budget retention accepted or committed: %v", err)
	}
}

func TestSixFixSyntheticIDResumesSession(t *testing.T) {
	for _, ending := range []string{"eof", "error", "stall"} {
		t.Run(ending, func(t *testing.T) {
			manager := session.NewSessionManager(time.Hour, 100, 100000)
			w := &sixFixTerminalRecorder{httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.done", "item": map[string]interface{}{"id": "msg", "type": "message", "role": "assistant", "content": "hello"}})
			body := &sixFixBody{closed: make(chan struct{})}
			var tail io.Reader = strings.NewReader("")
			if ending == "error" {
				tail = foldStreamErrorReader{}
			}
			if ending == "stall" {
				tail = &sixFixBlockingReader{closed: body.closed}
			}
			body.Reader = io.MultiReader(strings.NewReader(stream), tail)
			_, _ = handleStreamSuccess(c, &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, "responses", &config.EnvConfig{}, manager, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true, Input: "question"}, raw, common.StreamPreflightTimeouts{InactivityTimeoutMs: 30})
			iterator := newResponsesSSEIterator(io.NopCloser(strings.NewReader(w.Body.String())))
			id := ""
			for {
				frame, ok, err := iterator.next()
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				if frame.event["type"] == "response.incomplete" {
					r, _ := frame.event["response"].(map[string]interface{})
					id = stringFromInterface(r["id"])
				}
			}
			if id == "" {
				t.Fatal("no client terminal ID")
			}
			sess, err := manager.GetSessionByResponseID(id)
			if err != nil || sess == nil {
				t.Fatalf("client ID cannot resume saved session: id=%s err=%v", id, err)
			}
			resumed, err := manager.GetOrCreateSession(id)
			if err != nil || resumed.ID != sess.ID {
				t.Fatalf("ID did not resume same session: %v", err)
			}
		})
	}
}

func TestSixFixIncompleteTerminalContent(t *testing.T) {
	for _, kind := range []string{"text", "tool", "refusal", "reasoning", "empty"} {
		t.Run(kind, func(t *testing.T) {
			item := map[string]interface{}{"id": "item", "type": "reasoning", "encrypted_content": "secret"}
			switch kind {
			case "text":
				item = map[string]interface{}{"type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "visible"}}}
			case "refusal":
				item = map[string]interface{}{"type": "message", "content": []interface{}{map[string]interface{}{"type": "refusal", "refusal": "cannot comply"}}}
			case "tool":
				item = map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": "run", "arguments": "{}"}
			}
			output := []interface{}{item}
			if kind == "empty" {
				output = nil
			}
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.incomplete", "response": map[string]interface{}{"id": "resp", "status": "incomplete", "output": output}})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			raw := []byte(`{"model":"gpt-5","stream":true}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw)))
			_, err := handleStreamSuccess(c, responsesFoldTestResp(stream), "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, raw, common.StreamPreflightTimeouts{})
			valid := kind != "reasoning" && kind != "empty"
			if valid && (err != nil || !c.Writer.Written() || strings.Count(w.Body.String(), `"type":"response.incomplete"`) != 1) {
				t.Fatalf("valid incomplete dropped: err=%v body=%s", err, w.Body.String())
			}
			if !valid && (err == nil || c.Writer.Written() || w.Body.Len() != 0) {
				t.Fatalf("empty incomplete committed: %v", err)
			}
		})
	}
}

func TestSixFixSessionTerminalReconciliation(t *testing.T) {
	col := newStreamOutputCollector()
	emit := func(event map[string]interface{}) { col.processEvent(formatResponsesFoldSSE(event)) }
	emit(map[string]interface{}{"type": "response.output_item.done", "item": map[string]interface{}{"id": "rs", "type": "reasoning", "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": "original"}}}})
	emit(map[string]interface{}{"type": "response.output_item.done", "item": map[string]interface{}{"id": "rs", "type": "reasoning"}})
	emit(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp", "output": []interface{}{
		map[string]interface{}{"id": "msg", "type": "message", "role": "assistant", "content": "new"},
		map[string]interface{}{"id": "rs", "type": "reasoning", "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": "conflict"}}, "encrypted_content": "cipher"},
		map[string]interface{}{"type": "message", "content": "anonymous"},
	}}})
	if len(col.items) != 3 || col.items[0].ID != "rs" || col.items[1].ID != "msg" {
		t.Fatalf("lost, duplicated or reordered items: %+v", col.items)
	}
	if col.items[0].EncryptedContent != "cipher" || !strings.Contains(fmt.Sprint(col.items[0].Summary), "original") {
		t.Fatalf("terminal failed to fill only missing fields: %+v", col.items[0])
	}
}

func TestSixFixNativeToolCompletionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		valid      bool
	}{
		{"read", "", false}, {"read", "{}", false}, {"read", "{", false},
		{"run", "", true}, {"read", `{"path":"x"}`, true},
		{"read", "null", true}, {"read", "[]", true},
	} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/committed=%t", tc.name, tc.args, committed), func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				stream := ""
				if committed {
					stream = formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_text.delta", "delta": "keep legal text"})
				}
				item := map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": tc.name, "arguments": tc.args}
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": tc.name}})
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": tc.args})
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.function_call_arguments.done", "output_index": 1, "arguments": tc.args})
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.done", "output_index": 1, "item": item})
				stream += formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp", "output": []interface{}{item}}})
				_, err := handleStreamSuccess(c, responsesFoldTestResp(stream), "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5", Stream: true}, nil, common.StreamPreflightTimeouts{})
				wire := w.Body.String()
				if tc.valid {
					if err != nil || !strings.Contains(wire, "response.function_call_arguments.done") || !strings.Contains(wire, "response.completed") {
						t.Fatalf("legal tool rejected: %v %s", err, wire)
					}
					return
				}
				if strings.Contains(wire, "response.function_call_arguments.done") || strings.Contains(wire, "response.output_item.done") || strings.Contains(wire, "response.completed") {
					t.Fatalf("invalid completion escaped: %v %s", err, wire)
				}
				if !committed {
					if err == nil || c.Writer.Written() || len(wire) != 0 {
						t.Fatalf("invalid precommit must fail with zero bytes: %v %s", err, wire)
					}
				} else if err != nil || !strings.Contains(wire, "keep legal text") || strings.Count(wire, `"type":"response.incomplete"`) != 1 || !strings.Contains(wire, "upstream_error") {
					t.Fatalf("invalid postcommit must preserve text and terminate abnormally: %v %s", err, wire)
				}
			})
		}
	}
}

func TestSixFixIncompleteSnapshotBudget(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	text := strings.Repeat("x", 17*1024*1024)
	stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.incomplete", "response": map[string]interface{}{"id": "resp", "output": []interface{}{map[string]interface{}{"type": "message", "content": text}}}})
	_, err := handleStreamSuccess(c, responsesFoldTestResp(stream), "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{}, nil, common.StreamPreflightTimeouts{})
	if !errors.Is(err, common.ErrInvalidResponseBody) || c.Writer.Written() || w.Body.Len() != 0 {
		t.Fatalf("incomplete snapshot and retained text bypassed shared budget: err=%v bytes=%d", err, w.Body.Len())
	}
}

// Cross a clock tick at the wire boundary so two independent UnixNano IDs
// cannot accidentally compare equal on Windows' coarse wall clock.
type sixFixTerminalRecorder struct{ *httptest.ResponseRecorder }

func (w *sixFixTerminalRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if strings.Contains(string(p), "response.incomplete") {
		<-time.After(30 * time.Millisecond)
	}
	return n, err
}

type sixFixBlockingReader struct{ closed <-chan struct{} }

func (b *sixFixBlockingReader) Read([]byte) (int, error) { <-b.closed; return 0, io.ErrClosedPipe }
