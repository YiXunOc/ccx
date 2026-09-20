package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Temporary opt-in metadata only; never log arbitrary event values or payloads.
var toolTraceSequence atomic.Uint64

type toolTrace struct {
	id    string
	count int
}

func getToolTrace(c *gin.Context) *toolTrace {
	if os.Getenv("CCX_TOOL_TRACE") != "1" {
		return nil
	}
	if v, ok := c.Get("ccxToolTrace"); ok {
		return v.(*toolTrace)
	}
	d := &toolTrace{id: fmt.Sprintf("%x-%d", time.Now().UnixNano(), toolTraceSequence.Add(1))}
	c.Set("ccxToolTrace", d)
	return d
}

func (d *toolTrace) token(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(d.id + ":" + s))
	return fmt.Sprintf("%x", h[:8])
}

func (d *toolTrace) emit(fields map[string]interface{}) {
	if d.count >= 2000 {
		return
	}
	d.count++
	fields["trace"] = d.id
	fields["seq"] = d.count
	if d.count == 2000 {
		fields["truncated"] = true
	}
	b, _ := json.Marshal(fields)
	log.Printf("[Chat-ToolTrace] %s", b)
}

func traceToolRoute(c *gin.Context, protocol, model, host string) {
	if d := getToolTrace(c); d != nil {
		switch protocol {
		case "responses", "openai", "claude", "gemini":
		default:
			protocol = "other"
		}
		d.emit(map[string]interface{}{"stage": "route", "client": "chat", "upstream": protocol, "model_token": d.token(model), "host_token": d.token(host)})
	}
}

func traceToolUpstream(c *gin.Context, data, eventType string, crlf bool) {
	d := getToolTrace(c)
	if d == nil {
		return
	}
	root := gjson.Parse(data)
	typ := root.Get("type").String()
	switch typ {
	case "response.output_item.added", "response.output_item.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.completed":
	default:
		return
	}
	fields := map[string]interface{}{"stage": "upstream", "event": typ, "event_matches": eventType == typ, "crlf": crlf}
	if idx := root.Get("output_index"); idx.Exists() {
		fields["index"] = idx.Int()
	}
	item := root.Get("item")
	if !item.Exists() {
		item = root
	}
	traceToolFields(d, fields, item)
	fields["delta_bytes"] = len(root.Get("delta").String())
	d.emit(fields)
	if typ == "response.completed" {
		for i, item := range root.Get("response.output").Array() {
			if item.Get("type").String() != "function_call" {
				continue
			}
			f := map[string]interface{}{"stage": "upstream_final_item", "index": i}
			traceToolFields(d, f, item)
			d.emit(f)
		}
	}
}

func traceToolFields(d *toolTrace, f map[string]interface{}, item gjson.Result) {
	name := item.Get("name")
	f["name_present"] = name.Exists()
	f["name_nonempty"] = strings.TrimSpace(name.String()) != ""
	f["name_token"] = d.token(name.String())
	f["call_token"] = d.token(item.Get("call_id").String())
	id := item.Get("id").String()
	if id == "" {
		id = item.Get("item_id").String()
	}
	f["item_token"] = d.token(id)
	f["args_bytes"] = len(item.Get("arguments").String())
}

func traceToolDownstream(c *gin.Context, data []byte) {
	d := getToolTrace(c)
	if d == nil {
		return
	}
	root := gjson.ParseBytes(data)
	for _, choice := range root.Get("choices").Array() {
		for _, call := range choice.Get("delta.tool_calls").Array() {
			f := map[string]interface{}{"stage": "downstream", "choice": choice.Get("index").Int(), "index": call.Get("index").Int()}
			traceToolFields(d, f, call.Get("function"))
			f["call_token"] = d.token(call.Get("id").String())
			d.emit(f)
		}
		if finish := choice.Get("finish_reason").String(); finish != "" {
			switch finish {
			case "stop", "tool_calls", "length", "content_filter":
			default:
				finish = "other"
			}
			d.emit(map[string]interface{}{"stage": "downstream_finish", "reason": finish})
		}
	}
}
