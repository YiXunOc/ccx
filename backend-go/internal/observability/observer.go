package observability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

type observerKey struct{}
type clientBytesKey struct{}
type convertedBytesKey struct{}
type correlationKey struct{}

func WithCorrelation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}
func Correlation(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}
func NewCorrelationID() string { return newID("corr") }
func WithClientBytes(ctx context.Context, n int) context.Context {
	if _, ok := ctx.Value(clientBytesKey{}).(int); ok {
		return ctx
	}
	return context.WithValue(ctx, clientBytesKey{}, n)
}
func ClientBytes(ctx context.Context) int {
	if n, ok := ctx.Value(clientBytesKey{}).(int); ok {
		return n
	}
	return -1
}
func WithConvertedBytes(req *http.Request, body []byte) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(context.WithValue(req.Context(), convertedBytesKey{}, len(body)))
}
func ConvertedBytes(ctx context.Context) int {
	if n, ok := ctx.Value(convertedBytesKey{}).(int); ok {
		return n
	}
	return -1
}

// Observer retains bounded counters only. Callbacks receive immutable snapshots.
// Transport IDs describe explicit RoundTrip calls, excluding net/http internal retries.
type Observer struct {
	mu              sync.Mutex
	mode            string
	summary         Summary
	emit            func(Summary)
	finished        bool
	requestObserved bool
}

func NewObserver(mode string, ctx Context, emit func(Summary)) *Observer {
	if ParseCaptureMode(mode) == CaptureModeOff {
		return nil
	}
	return &Observer{mode: mode, summary: NewSummary(ctx), emit: emit}
}
func Bind(req *http.Request, o *Observer) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(context.WithValue(req.Context(), observerKey{}, o))
}
func FromContext(ctx context.Context) *Observer {
	o, _ := ctx.Value(observerKey{}).(*Observer)
	return o
}
func (o *Observer) Request(req *http.Request) {
	if o == nil || req == nil {
		return
	}
	replayBytes := -1
	replayError := ""
	o.mu.Lock()
	if o.requestObserved {
		o.mu.Unlock()
		return
	}
	o.requestObserved = true
	o.mu.Unlock()
	if req.GetBody != nil {
		r, err := req.GetBody()
		if err != nil || r == nil {
			replayError = "get_body_error"
		} else {
			n, err := io.Copy(io.Discard, r)
			closeErr := r.Close()
			replayBytes = int(n)
			if err != nil {
				replayError = "replay_read_error"
			} else if closeErr != nil {
				replayError = "replay_close_error"
			}
		}
	}
	o.mu.Lock()
	o.summary.ClientBodyBytes = ClientBytes(req.Context())
	o.summary.ConvertedBodyBytes = ConvertedBytes(req.Context())
	o.summary.ContentLength = req.ContentLength
	o.summary.GetBodyReplayBytes = replayBytes
	o.summary.ReplayError = replayError
	o.mu.Unlock()
}
func (o *Observer) Snapshot() Summary {
	if o == nil {
		return NewSummary(Context{})
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshot()
}
func (o *Observer) snapshot() Summary {
	s := o.summary
	s.EventCounts = make(map[string]int, len(o.summary.EventCounts))
	for k, v := range o.summary.EventCounts {
		s.EventCounts[k] = v
	}
	s.DownstreamEventCounts = make(map[string]int, len(o.summary.DownstreamEventCounts))
	for k, v := range o.summary.DownstreamEventCounts {
		s.DownstreamEventCounts[k] = v
	}
	return s
}
func (o *Observer) TransportStart() string {
	if o == nil {
		return ""
	}
	id := newID("tx")
	o.mu.Lock()
	o.summary.Context.TransportTryID = id
	o.summary.AssociationStatus = o.summary.Context.AssociationStatus()
	o.summary.TransportTries++
	o.mu.Unlock()
	return id
}
func (o *Observer) Response(status int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.summary.FirstUpstreamStatus == 0 {
		o.summary.FirstUpstreamStatus = status
	}
	o.summary.UpstreamStatus = status
	o.mu.Unlock()
}
func (o *Observer) ReadEnd(err error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err == nil || errors.Is(err, io.EOF) {
		o.summary.EOF = true
	} else {
		switch {
		case errors.Is(err, context.Canceled):
			o.summary.ReadError = "cancelled"
		case errors.Is(err, context.DeadlineExceeded):
			o.summary.ReadError = "timeout"
		case errors.Is(err, io.ErrUnexpectedEOF):
			o.summary.ReadError = "unexpected_eof"
		default:
			var netErr *net.OpError
			if errors.As(err, &netErr) {
				o.summary.ReadError = "network_error"
			} else {
				o.summary.ReadError = "read_error"
			}
		}
	}
}
func (o *Observer) Event(kind string) {
	if o == nil {
		return
	}
	switch kind {
	case "response.metadata", "response.created", "response.in_progress", "response.output_text.delta", "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.completed", "response.failed", "response.incomplete", "error", "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "chat.chunk", "done":
	default:
		kind = "other"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.summary.EventCounts[kind] < 10000 {
		o.summary.EventCounts[kind]++
	}
	switch kind {
	case "response.failed", "response.incomplete", "error":
		o.summary.Terminal = kind
	case "response.completed", "message_stop":
		if o.summary.Terminal != "response.failed" && o.summary.Terminal != "response.incomplete" && o.summary.Terminal != "error" {
			o.summary.Terminal = kind
		}
	case "done":
		if o.summary.Terminal == "" {
			o.summary.Terminal = kind
		}
	}
}

// Data observes an already-parsed data field. Values are never retained or emitted.
func (o *Observer) Data(data string) {
	if o == nil {
		return
	}
	data = strings.TrimSpace(data)
	if data == "[DONE]" {
		o.Event("done")
		return
	}
	var event struct {
		Type    string          `json:"type"`
		Object  string          `json:"object"`
		Error   json.RawMessage `json:"error"`
		Choices json.RawMessage `json:"choices"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		o.Event("other")
		return
	}
	kind := event.Type
	if kind == "" && len(event.Error) > 0 && string(event.Error) != "null" {
		kind = "error"
	}
	if kind == "" && (event.Object == "chat.completion.chunk" || len(event.Choices) > 0) {
		kind = "chat.chunk"
	}
	o.Event(kind)
}

// DownstreamData observes a client-facing SSE data field separately from upstream evidence.
func (o *Observer) DownstreamData(data string) {
	if o == nil {
		return
	}
	data = strings.TrimSpace(data)
	kind := "other"
	if data == "[DONE]" {
		kind = "done"
	} else {
		var v struct {
			Type    string          `json:"type"`
			Error   json.RawMessage `json:"error"`
			Choices json.RawMessage `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &v) == nil {
			if v.Type != "" {
				kind = v.Type
			} else if len(v.Error) > 0 && string(v.Error) != "null" {
				kind = "error"
			} else if len(v.Choices) > 0 {
				kind = "chat.chunk"
			}
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.summary.DownstreamEventCounts[kind] < 10000 {
		o.summary.DownstreamEventCounts[kind]++
	}
	switch kind {
	case "done", "message_stop", "response.completed", "response.failed", "response.incomplete", "error":
		o.summary.DownstreamTerminal = kind
	}
}

func (o *Observer) Emitter() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.summary.EmitterEvents++
	o.mu.Unlock()
}
func (o *Observer) MarkSyntheticTerminal() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	// A synthetic terminal is a local CCX outcome. Keep upstream event counts
	// and status unchanged while making Finish classify the attempt as failed.
	o.summary.Terminal = "response.incomplete"
	o.summary.SyntheticFailure = true
}

func (o *Observer) Finish(outcome string, failed, committed bool, written int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return
	}
	o.finished = true
	if outcome == "success" {
		switch o.summary.Terminal {
		case "response.failed", "response.incomplete", "error":
			outcome = "upstream_terminal_failure"
		}
		if o.summary.ReadError != "" {
			outcome = "upstream_read_error"
		}
	}
	o.summary.Outcome = outcome
	o.summary.HeaderCommitted = committed
	o.summary.ClientWriteBytes = written
	failed = failed || o.summary.ReadError != "" || o.summary.Terminal == "response.failed" || o.summary.Terminal == "response.incomplete" || o.summary.Terminal == "error"
	s := o.snapshot()
	emit := o.emit
	record := ShouldRecord(o.mode, failed)
	o.mu.Unlock()
	if record && emit != nil {
		func() { defer func() { _ = recover() }(); emit(s) }()
	}
}
