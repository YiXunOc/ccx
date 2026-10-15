package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func foldTestRunEvents(t *testing.T, events []map[string]interface{}) []map[string]interface{} {
	t.Helper()
	stream := ""
	for _, e := range events {
		stream += formatResponsesFoldSSE(e)
	}
	var out []map[string]interface{}
	_, err := runResponsesFold(map[string]interface{}{}, responsesFoldTestResp(stream), nil, func(e map[string]interface{}) error { out = append(out, cloneFoldTestMap(e)); return nil }, foldCodexTestContext())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFoldRepairClosingOrder(t *testing.T) {
	for _, kind := range []string{"message", "function_call", "custom_tool_call"} {
		for _, shape := range []string{"field_done_only", "partial_field_done", "partial_part_done"} {
			if kind != "message" && shape == "partial_part_done" {
				continue
			}
			t.Run(kind+"/"+shape, func(t *testing.T) {
				item := map[string]interface{}{"id": "item", "type": kind, "status": "completed"}
				field, stem := "arguments", "response.function_call_arguments"
				full := "hello"
				if kind == "message" {
					field, stem = "text", "response.output_text"
					item["content"] = []interface{}{map[string]interface{}{"type": "output_text", "text": full}}
				} else if kind == "custom_tool_call" {
					field, stem = "input", "response.custom_tool_call_input"
					item[field] = full
					item["call_id"] = "call"
					item["name"] = "plain"
				} else {
					item[field] = full
					item["call_id"] = "call"
					item["name"] = "plain"
				}
				events := []map[string]interface{}{}
				if strings.HasPrefix(shape, "partial") {
					events = append(events, map[string]interface{}{"type": stem + ".delta", "output_index": 7, "item_id": "item", "content_index": 0, "delta": "hel"})
				}
				if shape == "partial_part_done" {
					events = append(events, map[string]interface{}{"type": "response.content_part.done", "output_index": 7, "item_id": "item", "content_index": 0, "part": map[string]interface{}{"type": "output_text", "text": full}, "original_close": true})
				} else {
					events = append(events, map[string]interface{}{"type": stem + ".done", "output_index": 7, "item_id": "item", "content_index": 0, field: full, "original_close": true})
				}
				events = append(events, map[string]interface{}{"type": "response.output_item.done", "output_index": 7, "item": item}, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}}})
				out := foldTestRunEvents(t, events)
				added, partAdded, fieldDone, partDone, itemDone := false, false, false, false, false
				text := ""
				originalCloses := 0
				for _, e := range out {
					typ := stringFromInterface(e["type"])
					if e["original_close"] == true {
						originalCloses++
					}
					switch typ {
					case "response.output_item.added":
						if added || itemDone {
							t.Fatalf("duplicate/late item added: %#v", out)
						}
						added = true
					case "response.content_part.added":
						if !added || partAdded || fieldDone || itemDone {
							t.Fatalf("part added out of order: %#v", out)
						}
						partAdded = true
					case stem + ".delta":
						if !added || fieldDone || partDone || itemDone || (kind == "message" && !partAdded) {
							t.Fatalf("delta after close or before added: %#v", out)
						}
						text += stringFromInterface(e["delta"])
					case stem + ".done":
						if !added || fieldDone || partDone || itemDone || text != full || (kind == "message" && !partAdded) {
							t.Fatalf("field done before full delta: %#v", out)
						}
						fieldDone = true
					case "response.content_part.done":
						if !fieldDone || !partAdded || partDone || itemDone {
							t.Fatalf("part done before field done: %#v", out)
						}
						partDone = true
					case "response.output_item.done":
						if !fieldDone || itemDone || (kind == "message" && !partDone) {
							t.Fatalf("item done out of order: %#v", out)
						}
						itemDone = true
					}
				}
				if out[len(out)-1]["type"] != "response.completed" || text != full || !itemDone || originalCloses != 1 {
					t.Fatalf("lost content/closing event: %#v", out)
				}
			})
		}
	}
}

func TestFoldRepairIdentityConflicts(t *testing.T) {
	base := map[string]interface{}{"id": "original", "type": "function_call", "call_id": "call", "name": "plain", "arguments": "{}"}
	cases := []string{"snapshot_same_index_other_id", "added_done_id", "added_done_type", "added_done_call_id", "added_done_name", "delta_item_id", "delta_call_id", "orphan_item_id", "duplicate_entry_id"}
	for _, name := range cases {
		for _, snapshot := range []bool{false, true} {
			if name == "snapshot_same_index_other_id" && !snapshot {
				continue
			}
			t.Run(name+map[bool]string{true: "/snapshot", false: "/no_snapshot"}[snapshot], func(t *testing.T) {
				added := cloneFoldTestMap(base)
				added["arguments"] = ""
				done := cloneFoldTestMap(base)
				snap := cloneFoldTestMap(base)
				events := []map[string]interface{}{{"type": "response.output_item.added", "output_index": 0, "item": added}}
				switch name {
				case "snapshot_same_index_other_id":
					snap["id"] = "replacement"
				case "added_done_id":
					done["id"] = "replacement"
					snap["id"] = "replacement"
				case "added_done_type":
					done["type"] = "custom_tool_call"
					snap["type"] = "custom_tool_call"
				case "added_done_call_id":
					done["call_id"] = "replacement"
					snap["call_id"] = "replacement"
				case "added_done_name":
					done["name"] = "replacement"
					snap["name"] = "replacement"
				case "delta_item_id":
					events = append(events, map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "replacement", "delta": "{}"})
				case "delta_call_id":
					events = append(events, map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "original", "call_id": "replacement", "delta": "{}"})
				case "orphan_item_id":
					events = []map[string]interface{}{{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "replacement", "delta": "{}"}}
				case "duplicate_entry_id":
					events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": 9, "item": cloneFoldTestMap(added)}, map[string]interface{}{"type": "response.output_item.done", "output_index": 9, "item": cloneFoldTestMap(done)})
				}
				events = append(events, map[string]interface{}{"type": "response.output_item.done", "output_index": 0, "item": done})
				output := []interface{}{}
				if snapshot {
					output = append(output, snap)
				}
				events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": output}})
				out := foldTestRunEvents(t, events)
				if out[len(out)-1]["type"] != "response.incomplete" || mapFromInterface(mapFromInterface(out[len(out)-1]["response"])["incomplete_details"])["reason"] != "upstream_error" {
					t.Fatalf("identity conflict accepted: %#v", out)
				}
				for _, e := range out {
					if e["type"] == "response.completed" {
						t.Fatalf("conflict succeeded: %#v", out)
					}
				}
			})
		}
	}
}

func TestFoldRepairRejectsUnsafeLifecycle(t *testing.T) {
	for _, shape := range []string{"late_part_added", "item_done_before_part_done", "delta_after_part_done", "duplicate_part_done", "conflicting_part", "unknown_part_index"} {
		t.Run(shape, func(t *testing.T) {
			part := map[string]interface{}{"type": "output_text", "text": "hello"}
			item := map[string]interface{}{"id": "msg", "type": "message", "content": []interface{}{part}}
			ev := func(kind string) map[string]interface{} {
				return map[string]interface{}{"type": kind, "output_index": 7, "item_id": "msg", "content_index": 0}
			}
			added := ev("response.output_item.added")
			added["item"] = map[string]interface{}{"id": "msg", "type": "message", "content": []interface{}{}}
			pa := ev("response.content_part.added")
			pa["part"] = map[string]interface{}{"type": "output_text", "text": ""}
			delta := ev("response.output_text.delta")
			delta["delta"] = "hello"
			fd := ev("response.output_text.done")
			fd["text"] = "hello"
			pd := ev("response.content_part.done")
			pd["part"] = cloneFoldTestMap(part)
			done := ev("response.output_item.done")
			done["item"] = item
			events := []map[string]interface{}{added, pa, delta, fd, pd, done}
			switch shape {
			case "late_part_added":
				events = []map[string]interface{}{added, delta, pa, fd, pd, done}
			case "item_done_before_part_done":
				events = []map[string]interface{}{added, pa, delta, fd, done, pd}
			case "delta_after_part_done":
				late := ev("response.output_text.delta")
				late["delta"] = "!"
				events = []map[string]interface{}{added, pa, delta, fd, pd, late, done}
			case "duplicate_part_done":
				events = []map[string]interface{}{added, pa, delta, fd, pd, cloneFoldTestMap(pd), done}
			case "conflicting_part":
				pd["part"] = map[string]interface{}{"type": "output_text", "text": "different"}
			case "unknown_part_index":
				extra := ev("response.output_text.delta")
				extra["content_index"] = 2
				extra["delta"] = "orphan"
				events = []map[string]interface{}{added, pa, delta, fd, pd, extra, done}
			}
			events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}}})
			out := foldTestRunEvents(t, events)
			if out[len(out)-1]["type"] != "response.incomplete" {
				t.Fatalf("unsafe lifecycle succeeded: %#v", out)
			}
		})
	}
}

func TestFoldRepairSparseIndexes(t *testing.T) {
	a := map[string]interface{}{"id": "a", "type": "function_call", "call_id": "ca", "name": "plain", "arguments": "{}"}
	b := cloneFoldTestMap(a)
	b["id"] = "b"
	b["call_id"] = "cb"
	out := foldTestRunEvents(t, []map[string]interface{}{{"type": "response.output_item.done", "output_index": 7, "item": a}, {"type": "response.output_item.done", "output_index": 11, "item": b}, {"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{a, b}}}})
	if out[len(out)-1]["type"] != "response.completed" {
		t.Fatalf("valid sparse items rejected: %#v", out)
	}
	output := mapFromInterface(out[len(out)-1]["response"])["output"].([]interface{})
	if len(output) != 2 || mapFromInterface(output[0])["id"] != "a" || mapFromInterface(output[1])["id"] != "b" {
		t.Fatalf("sparse output lost/reordered: %#v", output)
	}
	for i, e := range out {
		if intFromInterfaceDefault(e["sequence_number"]) != i {
			t.Fatal("sequence gap")
		}
		if idx, ok := e["output_index"]; ok && intFromInterfaceDefault(idx) > 1 {
			t.Fatalf("not remapped: %#v", e)
		}
	}
}

func TestFoldCompactSnapshotSparseIdentity(t *testing.T) {
	cases := []struct {
		name    string
		indexes []int
		total   int
	}{
		{"dense", []int{0, 1}, 2}, {"overlap_1_7", []int{1, 7}, 2}, {"overlap_0_2_7", []int{0, 2, 7}, 3}, {"overlap_1_2_7", []int{1, 2, 7}, 3}, {"nonoverlap_7_11", []int{7, 11}, 2}, {"terminal_tail_overlaps_claimed_1", []int{1}, 2}, {"terminal_tail_overlaps_claimed_2", []int{0, 2}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]interface{}, tc.total)
			events := []map[string]interface{}{}
			for i := range items {
				id := string(rune('a' + i))
				items[i] = map[string]interface{}{"id": id, "type": "function_call", "call_id": "call_" + id, "name": "plain_" + id, "arguments": "{}"}
			}
			for i, idx := range tc.indexes {
				item := cloneFoldTestMap(mapFromInterface(items[i]))
				item["arguments"] = ""
				events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": idx, "item": item}, map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": idx, "item_id": item["id"], "delta": "{"})
			}
			events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": items}})
			out := foldTestRunEvents(t, events)
			last := out[len(out)-1]
			if last["type"] != "response.completed" {
				t.Fatalf("valid compact snapshot rejected for %v: %#v", tc.indexes, last)
			}
			output := mapFromInterface(last["response"])["output"].([]interface{})
			if len(output) != tc.total {
				t.Fatalf("output count=%d", len(output))
			}
			added, done := map[string]int{}, map[string]int{}
			args := map[string]string{}
			order := []string{}
			for seq, e := range out {
				if intFromInterfaceDefault(e["sequence_number"]) != seq {
					t.Fatalf("sequence gap: %#v", e)
				}
				switch e["type"] {
				case "response.output_item.added":
					item := mapFromInterface(e["item"])
					id := stringFromInterface(item["id"])
					added[id]++
					order = append(order, id)
					idx := intFromInterfaceDefault(e["output_index"])
					if item["call_id"] != "call_"+id || item["name"] != "plain_"+id || idx != len(order)-1 {
						t.Fatalf("added identity/order changed: %#v", e)
					}
				case "response.function_call_arguments.delta":
					id := stringFromInterface(e["item_id"])
					if added[id] != 1 || done[id] != 0 {
						t.Fatalf("delta outside lifecycle: %#v", e)
					}
					args[id] += stringFromInterface(e["delta"])
				case "response.output_item.done":
					item := mapFromInterface(e["item"])
					id := stringFromInterface(item["id"])
					done[id]++
					if added[id] != 1 || args[id] != "{}" || item["arguments"] != "{}" || item["call_id"] != "call_"+id || item["name"] != "plain_"+id {
						t.Fatalf("done changed/replayed identity/args: %#v", e)
					}
				}
			}
			for i, raw := range output {
				id := string(rune('a' + i))
				item := mapFromInterface(raw)
				if item["id"] != id || item["call_id"] != "call_"+id || item["name"] != "plain_"+id || item["arguments"] != "{}" || added[id] != 1 || done[id] != 1 || order[i] != id {
					t.Fatalf("output lost/reordered/duplicated: %#v events=%#v", output, out)
				}
			}
		})
	}
}

func TestFoldCompactSnapshotAmbiguityAndDuplicateID(t *testing.T) {
	for _, kind := range []string{"missing_upstream_id", "missing_snapshot_id", "duplicate_snapshot_id", "duplicate_upstream_id"} {
		t.Run(kind, func(t *testing.T) {
			a := map[string]interface{}{"id": "a", "type": "function_call", "name": "plain", "arguments": "{}"}
			b := cloneFoldTestMap(a)
			b["id"] = "b"
			events := []map[string]interface{}{}
			switch kind {
			case "missing_upstream_id":
				events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{"type": "function_call", "name": "plain", "arguments": ""}})
			case "missing_snapshot_id":
				for i, item := range []map[string]interface{}{a, b} {
					events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": i, "item": cloneFoldTestMap(item)})
				}
				a = cloneFoldTestMap(a)
				b = cloneFoldTestMap(b)
				delete(a, "id")
				delete(b, "id")
			case "duplicate_snapshot_id":
				b["id"] = "a"
			case "duplicate_upstream_id":
				for _, idx := range []int{1, 7} {
					events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": idx, "item": cloneFoldTestMap(a)})
				}
			}
			events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{a, b}}})
			out := foldTestRunEvents(t, events)
			if out[len(out)-1]["type"] != "response.incomplete" {
				t.Fatalf("ambiguous/duplicate identity guessed: %#v", out)
			}
			for _, e := range out {
				if e["type"] == "response.completed" || e["type"] == "response.output_item.added" {
					t.Fatalf("ambiguous tool delivered: %#v", out)
				}
			}
		})
	}
}

func TestFoldCompactSnapshotTerminalOrder(t *testing.T) {
	for _, upstreamOrder := range []string{"ac", "ca"} {
		t.Run(upstreamOrder, func(t *testing.T) {
			items := []interface{}{}
			for _, id := range []string{"a", "b", "c"} {
				items = append(items, map[string]interface{}{"id": id, "type": "function_call", "call_id": "call_" + id, "name": "plain_" + id, "arguments": "{}"})
			}
			events := []map[string]interface{}{}
			for _, r := range upstreamOrder {
				id := string(r)
				idx := map[string]int{"a": 1, "c": 7}[id]
				item := cloneFoldTestMap(mapFromInterface(items[map[string]int{"a": 0, "c": 2}[id]]))
				item["arguments"] = ""
				events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": idx, "item": item}, map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": idx, "item_id": id, "delta": "{}"})
			}
			events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": items}})
			out := foldTestRunEvents(t, events)
			if out[len(out)-1]["type"] != "response.completed" {
				t.Fatalf("valid partial snapshot rejected: %#v", out)
			}
			addedOrder := []string{}
			deltas := map[string]string{}
			for _, e := range out {
				if e["type"] == "response.output_item.added" {
					addedOrder = append(addedOrder, stringFromInterface(mapFromInterface(e["item"])["id"]))
				}
				if e["type"] == "response.function_call_arguments.delta" {
					deltas[stringFromInterface(e["item_id"])] += stringFromInterface(e["delta"])
				}
			}
			output := mapFromInterface(out[len(out)-1]["response"])["output"].([]interface{})
			for i, raw := range output {
				id := string(rune('a' + i))
				if mapFromInterface(raw)["id"] != id || addedOrder[i] != id || deltas[id] != "{}" {
					t.Fatalf("snapshot order/identity/delta changed: output=%#v order=%v deltas=%v", output, addedOrder, deltas)
				}
			}
		})
	}
}

func TestFoldCompactSnapshotLiveReferences(t *testing.T) {
	for _, shape := range []string{"compatible", "message_before_live", "reasoning_before_live", "reasoning_after_message", "reverse_live"} {
		t.Run(shape, func(t *testing.T) {
			reason := map[string]interface{}{"id": "r", "type": "reasoning", "summary": []interface{}{map[string]interface{}{"type": "summary_text", "text": "think"}}}
			msg := map[string]interface{}{"id": "ma", "type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "A"}}}
			middle := map[string]interface{}{"id": "mb", "type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "B"}}}
			tool := map[string]interface{}{"id": "tc", "type": "function_call", "call_id": "call", "name": "plain", "arguments": "{}"}
			events := []map[string]interface{}{{"type": "response.output_item.added", "output_index": 1, "item": map[string]interface{}{"id": "r", "type": "reasoning", "summary": []interface{}{}}}, {"type": "response.reasoning_summary_text.delta", "output_index": 1, "item_id": "r", "summary_index": 0, "delta": "think"}, {"type": "response.output_item.done", "output_index": 1, "item": reason}, {"type": "response.output_item.added", "output_index": 7, "item": map[string]interface{}{"id": "ma", "type": "message", "content": []interface{}{}}}, {"type": "response.output_text.delta", "output_index": 7, "item_id": "ma", "content_index": 0, "delta": "A"}, {"type": "response.output_item.added", "output_index": 11, "item": map[string]interface{}{"id": "tc", "type": "function_call", "call_id": "call", "name": "plain", "arguments": ""}}, {"type": "response.function_call_arguments.delta", "output_index": 11, "item_id": "tc", "delta": "{"}}
			snapshot := []interface{}{reason, msg, middle, tool}
			expected := []string{"r", "ma", "mb", "tc"}
			extraReason := map[string]interface{}{"id": "r2", "type": "reasoning", "summary": []interface{}{}}
			switch shape {
			case "message_before_live":
				snapshot = []interface{}{msg, reason, middle, tool}
			case "reasoning_before_live":
				snapshot = []interface{}{extraReason, reason, msg, middle, tool}
			case "reasoning_after_message":
				snapshot = []interface{}{reason, msg, extraReason, middle, tool}
			case "reverse_live":
				events = append(events, map[string]interface{}{"type": "response.output_item.added", "output_index": 15, "item": extraReason}, map[string]interface{}{"type": "response.output_item.done", "output_index": 15, "item": extraReason})
				snapshot = []interface{}{extraReason, reason, msg, middle, tool}
			}
			events = append(events, map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": snapshot}})
			out := foldTestRunEvents(t, events)
			last := out[len(out)-1]
			want := "response.completed"
			if shape != "compatible" {
				want = "response.incomplete"
			}
			if last["type"] != want {
				t.Fatalf("committed order conflict silently accepted: shape=%s terminal=%#v", shape, last)
			}
			indices := map[string]int{}
			counts := map[string]int{}
			deltas := map[string]string{}
			for seq, e := range out {
				if intFromInterfaceDefault(e["sequence_number"]) != seq {
					t.Fatal("sequence gap")
				}
				typ := stringFromInterface(e["type"])
				idx := intFromInterfaceDefault(e["output_index"])
				if typ == "response.output_item.added" {
					id := stringFromInterface(mapFromInterface(e["item"])["id"])
					indices[id] = idx
					counts[id]++
				}
				if id := stringFromInterface(e["item_id"]); id != "" {
					if known, ok := indices[id]; !ok || known != idx {
						t.Fatalf("reference changed: %#v indices=%v", e, indices)
					}
				}
				if typ == "response.output_item.done" {
					id := stringFromInterface(mapFromInterface(e["item"])["id"])
					if indices[id] != idx {
						t.Fatalf("done reference changed: %#v", e)
					}
				}
				if strings.HasSuffix(typ, ".delta") {
					deltas[stringFromInterface(e["item_id"])] += stringFromInterface(e["delta"])
				}
			}
			if indices["r"] != 0 || counts["r"] != 1 || deltas["r"] != "think" {
				t.Fatalf("live reasoning changed/replayed: indices=%v counts=%v deltas=%v", indices, counts, deltas)
			}
			if shape == "compatible" {
				output := mapFromInterface(last["response"])["output"].([]interface{})
				if len(output) != 4 {
					t.Fatal("missing output")
				}
				for i, id := range expected {
					if mapFromInterface(output[i])["id"] != id || indices[id] != i || counts[id] != 1 {
						t.Fatalf("output/committed index mismatch: output=%#v indices=%v counts=%v", output, indices, counts)
					}
				}
				if deltas["ma"] != "A" || deltas["mb"] != "B" || deltas["tc"] != "{}" {
					t.Fatalf("delta missing/repeated: %v", deltas)
				}
			}
		})
	}
}

func TestFoldRejectsConflictingSnapshot(t *testing.T) {
	for _, shape := range []string{"delta", "done"} {
		t.Run(shape, func(t *testing.T) {
			item := map[string]interface{}{"type": "function_call", "id": "fc", "call_id": "call", "name": "tool", "arguments": "new"}
			first := map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 3, "item_id": "fc", "delta": "old"}
			if shape == "done" {
				prior := cloneFoldTestMap(item)
				prior["arguments"] = "old"
				first = map[string]interface{}{"type": "response.output_item.done", "output_index": 3, "item": prior}
			}
			out := foldTestRunEvents(t, []map[string]interface{}{first, {"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}}}})
			if out[len(out)-1]["type"] != "response.incomplete" {
				t.Fatalf("conflict silently accepted: %#v", out)
			}
		})
	}
}

func TestFoldOrphanBeforeAdded(t *testing.T) {
	for _, kind := range []string{"message", "reasoning"} {
		t.Run(kind, func(t *testing.T) {
			item := map[string]interface{}{"id": "item", "type": kind}
			deltaType := "response.output_text.delta"
			if kind == "message" {
				item["content"] = []interface{}{map[string]interface{}{"type": "output_text", "text": "hi"}}
			} else {
				deltaType = "response.reasoning_summary_text.delta"
				item["summary"] = []interface{}{map[string]interface{}{"type": "summary_text", "text": "hi"}}
			}
			out := foldTestRunEvents(t, []map[string]interface{}{{"type": deltaType, "output_index": 2, "item_id": "item", "delta": "hi"}, {"type": "response.output_item.added", "output_index": 2, "item": item}, {"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}}}})
			added := false
			text := ""
			count := 0
			for _, e := range out {
				if e["type"] == "response.output_item.added" {
					if added {
						t.Fatal("duplicate added")
					}
					added = true
				}
				if e["type"] == deltaType {
					if !added {
						t.Fatal("orphan escaped before added")
					}
					text += stringFromInterface(e["delta"])
				}
				if e["type"] == "response.output_item.done" {
					count++
				}
			}
			if text != "hi" || count != 1 {
				t.Fatalf("text=%q done=%d", text, count)
			}
		})
	}
}

func TestFoldTerminalOnlyTools(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"type": "function_call", "id": "custom", "call_id": "c1", "name": "functions__exec", "arguments": `{"input":"ls"}`},
		map[string]interface{}{"type": "function_call", "id": "namespace", "call_id": "c2", "name": "functions__wait", "arguments": "{}"},
		map[string]interface{}{"type": "custom_tool_call", "id": "direct", "call_id": "c3", "name": "direct", "input": "hello"},
		map[string]interface{}{"type": "compaction", "id": "compact", "encrypted_content": "opaque"},
	}
	out := foldTestRunEvents(t, []map[string]interface{}{{"type": "response.completed", "response": map[string]interface{}{"output": items, "usage": map[string]interface{}{"input_tokens": 10, "output_tokens": 4}}}})
	output, _ := mapFromInterface(out[len(out)-1]["response"])["output"].([]interface{})
	if len(output) != 4 {
		t.Fatalf("output=%#v", output)
	}
	if mapFromInterface(output[0])["type"] != "custom_tool_call" || mapFromInterface(output[0])["input"] != "ls" || mapFromInterface(output[1])["namespace"] != "functions" {
		t.Fatalf("Codex not restored: %#v", output)
	}
	for i, e := range out {
		if intFromInterfaceDefault(e["sequence_number"]) != i {
			t.Fatal("sequence gap")
		}
	}
}

func TestFoldSnapshotReasoningBeforeContinuation(t *testing.T) {
	for _, terminalType := range []string{"response.completed", "response.failed", "response.incomplete"} {
		t.Run(terminalType, func(t *testing.T) {
			reason := map[string]interface{}{"id": "rs", "type": "reasoning", "summary": []interface{}{}, "encrypted_content": "ENC"}
			msg := map[string]interface{}{"id": "hidden", "type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "HIDDEN"}}}
			terminal := map[string]interface{}{"type": terminalType, "response": map[string]interface{}{"output": []interface{}{reason, msg}, "usage": map[string]interface{}{"input_tokens": 100, "output_tokens": responsesFoldStep + 8, "output_tokens_details": map[string]interface{}{"reasoning_tokens": responsesFoldStep - 2}}}}
			stream := formatResponsesFoldSSE(map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": map[string]interface{}{"id": "rs", "type": "reasoning"}}) + formatResponsesFoldSSE(terminal)
			opens := 0
			var out []map[string]interface{}
			result, err := runResponsesFold(map[string]interface{}{}, responsesFoldTestResp(stream), func(body map[string]interface{}) (*http.Response, []byte, error) {
				opens++
				input := body["input"].([]interface{})
				if mapFromInterface(input[0])["encrypted_content"] != "ENC" {
					t.Fatal("lost encrypted reasoning")
				}
				final := cloneFoldTestMap(msg)
				final["id"] = "final"
				final["content"] = []interface{}{map[string]interface{}{"type": "output_text", "text": "VISIBLE"}}
				return responsesFoldTestResp(formatResponsesFoldSSE(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{final}, "usage": map[string]interface{}{"input_tokens": 200, "output_tokens": 7}}})), nil, nil
			}, func(e map[string]interface{}) error { out = append(out, cloneFoldTestMap(e)); return nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantOpens := 0
			if terminalType == "response.completed" {
				wantOpens = 1
			}
			if opens != wantOpens {
				t.Fatalf("opens=%d", opens)
			}
			if opens == 1 {
				for _, e := range out {
					if e["delta"] == "HIDDEN" {
						t.Fatal("intermediate message leaked")
					}
				}
				if intFromInterfaceDefault(result.BilledUsage["input_tokens"]) != 300 || intFromInterfaceDefault(result.AgentUsage["input_tokens"]) != 100 || intFromInterfaceDefault(result.AgentUsage["output_tokens"]) != responsesFoldStep+5 {
					t.Fatalf("usage=%#v", result)
				}
			}
		})
	}
}

func TestFoldTerminalMessageContentLifecycle(t *testing.T) {
	item := map[string]interface{}{"id": "msg", "type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "hello"}}}
	out := foldTestRunEvents(t, []map[string]interface{}{{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}}}})
	var kinds []string
	for _, e := range out {
		kinds = append(kinds, stringFromInterface(e["type"]))
	}
	want := []string{"response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("lifecycle=%v", kinds)
	}
}

func TestFoldTerminalReconciliation(t *testing.T) {
	item := map[string]interface{}{"id": "msg", "type": "message", "role": "assistant", "status": "completed", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "hello"}}}
	for _, shape := range []string{"terminal", "done", "added", "orphan", "overlap"} {
		t.Run(shape, func(t *testing.T) {
			var stream string
			add := func(e map[string]interface{}) { stream += formatResponsesFoldSSE(e) }
			if shape == "added" || shape == "overlap" {
				add(map[string]interface{}{"type": "response.output_item.added", "output_index": 7, "item": map[string]interface{}{"id": "msg", "type": "message", "role": "assistant", "content": []interface{}{}}})
			}
			if shape == "orphan" || shape == "overlap" {
				add(map[string]interface{}{"type": "response.output_text.delta", "output_index": 7, "item_id": "msg", "content_index": 0, "delta": "hel"})
			}
			if shape == "done" {
				add(map[string]interface{}{"type": "response.output_item.done", "output_index": 7, "item": item})
			}
			add(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{item}, "usage": map[string]interface{}{"input_tokens": 10, "output_tokens": 5}}})
			var events []map[string]interface{}
			_, err := runResponsesFold(map[string]interface{}{}, responsesFoldTestResp(stream), nil, func(e map[string]interface{}) error { events = append(events, cloneFoldTestMap(e)); return nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			output, _ := mapFromInterface(events[len(events)-1]["response"])["output"].([]interface{})
			if len(output) != 1 || mapFromInterface(output[0])["id"] != "msg" {
				t.Fatalf("lost terminal item: %#v", output)
			}
			added, done := 0, 0
			text := ""
			for i, e := range events {
				if intFromInterfaceDefault(e["sequence_number"]) != i {
					t.Fatal("non-contiguous sequence")
				}
				switch e["type"] {
				case "response.output_item.added":
					added++
				case "response.output_item.done":
					done++
				case "response.output_text.delta":
					if added != 1 || done != 0 {
						t.Fatal("delta outside item lifecycle")
					}
					text += stringFromInterface(e["delta"])
				}
			}
			if added != 1 || done != 1 || text != "hello" {
				t.Fatalf("lifecycle added=%d done=%d text=%q", added, done, text)
			}
		})
	}
}

func TestRunResponsesFoldDetectsContinuesAndFoldsRounds(t *testing.T) {
	baseBody := map[string]interface{}{
		"model":                "gpt-5.5",
		"stream":               true,
		"transformer_metadata": map[string]interface{}{"codex_tool_compat_enabled": false},
		"input": []interface{}{
			map[string]interface{}{"type": "message", "role": "user", "content": []interface{}{}},
		},
	}
	rounds := []string{
		responsesFoldReasoningRound("rs_1", responsesFoldStep-2, "TRUNCATED ANSWER", true),
		responsesFoldReasoningRound("rs_2", 2*responsesFoldStep-2, "STILL TRUNCATED", true),
		responsesFoldReasoningRound("rs_3", 404, "REAL ANSWER", true),
	}

	var openedBodies []map[string]interface{}
	openRound := func(body map[string]interface{}) (*http.Response, []byte, error) {
		openedBodies = append(openedBodies, cloneFoldTestMap(body))
		idx := len(openedBodies) - 1
		return responsesFoldTestResp(rounds[idx+1]), nil, nil
	}

	var emitted []map[string]interface{}
	_, err := runResponsesFold(baseBody, responsesFoldTestResp(rounds[0]), openRound, func(event map[string]interface{}) error {
		emitted = append(emitted, cloneFoldTestMap(event))
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}

	if len(openedBodies) != 2 {
		t.Fatalf("continuation opens = %d, want 2", len(openedBodies))
	}
	for i, body := range openedBodies {
		if _, ok := body["transformer_metadata"]; ok {
			t.Fatalf("continuation %d leaked transformer_metadata: %#v", i+1, body)
		}
	}
	if got := foldTestInputTypes(openedBodies[0]["input"]); !reflect.DeepEqual(got, []string{"message", "reasoning", "message"}) {
		t.Fatalf("round 2 input types = %v", got)
	}
	if got := foldTestInputTypes(openedBodies[1]["input"]); !reflect.DeepEqual(got, []string{"message", "reasoning", "message", "reasoning", "message"}) {
		t.Fatalf("round 3 input types = %v", got)
	}
	if !foldTestIncludeContains(openedBodies[0]["include"], responsesFoldEncryptedInclude) {
		t.Fatalf("continuation include missing %q: %#v", responsesFoldEncryptedInclude, openedBodies[0]["include"])
	}
	lastInput := openedBodies[0]["input"].([]interface{})[2].(map[string]interface{})
	if lastInput["phase"] != "commentary" {
		t.Fatalf("continuation nudge phase = %v, want commentary", lastInput["phase"])
	}

	var deltas []string
	var terminals []map[string]interface{}
	seqs := make([]int, 0, len(emitted))
	outputIndexSet := make(map[int]struct{})
	for _, event := range emitted {
		seqs = append(seqs, intFromInterfaceDefault(event["sequence_number"]))
		if event["type"] == "response.output_text.delta" {
			deltas = append(deltas, stringFromInterface(event["delta"]))
		}
		if isResponsesFoldTerminalType(stringFromInterface(event["type"])) {
			terminals = append(terminals, event)
		}
		if idx, ok := intFromInterface(event["output_index"]); ok {
			outputIndexSet[idx] = struct{}{}
		}
	}
	if !reflect.DeepEqual(deltas, []string{"REAL ANSWER"}) {
		t.Fatalf("forwarded deltas = %v, want only final answer", deltas)
	}
	if len(terminals) != 1 || emitted[len(emitted)-1]["type"] != terminals[0]["type"] {
		t.Fatalf("expected exactly one final terminal event, got %d", len(terminals))
	}
	for i, seq := range seqs {
		if seq != i {
			t.Fatalf("sequence %d = %d, want %d", i, seq, i)
		}
	}
	var outputIndexes []int
	for idx := range outputIndexSet {
		outputIndexes = append(outputIndexes, idx)
	}
	sort.Ints(outputIndexes)
	if !reflect.DeepEqual(outputIndexes, []int{0, 1, 2, 3}) {
		t.Fatalf("downstream output indexes = %v, want [0 1 2 3]", outputIndexes)
	}

	terminalResponse := terminals[0]["response"].(map[string]interface{})
	usage := terminalResponse["usage"].(map[string]interface{})
	reasoning := intFromInterfaceDefault(mapFromInterface(usage["output_tokens_details"])["reasoning_tokens"])
	wantReasoning := (responsesFoldStep - 2) + (2*responsesFoldStep - 2) + 404
	if reasoning != wantReasoning {
		t.Fatalf("agent reasoning usage = %d, want %d", reasoning, wantReasoning)
	}

	metadata := terminalResponse["metadata"].(map[string]interface{})
	billed := metadata["proxy_billed_usage"].(map[string]interface{})
	if got := intFromInterfaceDefault(billed["input_tokens"]); got != 300 {
		t.Fatalf("proxy_billed_usage.input_tokens = %d, want 300", got)
	}
	roundInfo := metadata["proxy_rounds"].([]interface{})
	if len(roundInfo) != 3 {
		t.Fatalf("proxy_rounds len = %d, want 3", len(roundInfo))
	}

	output := terminalResponse["output"].([]interface{})
	gotOutput := foldTestOutputTypesAndIDs(output)
	wantOutput := []string{"reasoning:rs_1", "reasoning:rs_2", "reasoning:rs_3", "message:msg_rs_3"}
	if !reflect.DeepEqual(gotOutput, wantOutput) {
		t.Fatalf("terminal output = %v, want %v", gotOutput, wantOutput)
	}
}

func TestRunResponsesFoldStopsWhenReasoningHasNoEncryptedContent(t *testing.T) {
	baseBody := map[string]interface{}{
		"model":  "gpt-5.5",
		"stream": true,
		"input":  []interface{}{map[string]interface{}{"type": "message", "role": "user"}},
	}

	var emitted []map[string]interface{}
	_, err := runResponsesFold(
		baseBody,
		responsesFoldTestResp(responsesFoldReasoningRound("rs_1", responsesFoldStep-2, "ANSWER", false)),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			t.Fatalf("openRound should not be called when encrypted_content is missing")
			return nil, nil, nil
		},
		func(event map[string]interface{}) error {
			emitted = append(emitted, cloneFoldTestMap(event))
			return nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}

	terminal := emitted[len(emitted)-1]
	response := terminal["response"].(map[string]interface{})
	metadata := response["metadata"].(map[string]interface{})
	if metadata["proxy_stopped_reason"] != "no_encrypted_content" {
		t.Fatalf("proxy_stopped_reason = %v, want no_encrypted_content", metadata["proxy_stopped_reason"])
	}
	var deltas []string
	for _, event := range emitted {
		if event["type"] == "response.output_text.delta" {
			deltas = append(deltas, stringFromInterface(event["delta"]))
		}
	}
	if !reflect.DeepEqual(deltas, []string{"ANSWER"}) {
		t.Fatalf("final deltas = %v, want current round answer", deltas)
	}
}

func responsesFoldReasoningRound(id string, reasoningTokens int, text string, encrypted bool) string {
	item := map[string]interface{}{"id": id, "type": "reasoning", "summary": []interface{}{}}
	doneItem := cloneFoldTestMap(item)
	if encrypted {
		doneItem["encrypted_content"] = "ENC_" + id
	}
	message := map[string]interface{}{"id": "msg_" + id, "type": "message", "role": "assistant"}
	events := []map[string]interface{}{
		{"type": "response.created", "response": map[string]interface{}{"id": "resp_1", "status": "in_progress", "output": []interface{}{}}},
		{"type": "response.in_progress", "response": map[string]interface{}{"id": "resp_1", "status": "in_progress"}},
		{"type": "response.output_item.added", "output_index": 0, "item": item},
		{"type": "response.output_item.done", "output_index": 0, "item": doneItem},
		{"type": "response.output_item.added", "output_index": 1, "item": message},
		{"type": "response.output_text.delta", "output_index": 1, "item_id": message["id"], "content_index": 0, "delta": text},
		{"type": "response.output_item.done", "output_index": 1, "item": map[string]interface{}{
			"id":      message["id"],
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "output_text", "text": text}},
		}},
		{"type": "response.completed", "response": map[string]interface{}{
			"id":     "resp_1",
			"status": "completed",
			"output": []interface{}{},
			"usage": map[string]interface{}{
				"input_tokens":  100,
				"output_tokens": reasoningTokens + 20,
				"total_tokens":  120 + reasoningTokens,
				"output_tokens_details": map[string]interface{}{
					"reasoning_tokens": reasoningTokens,
				},
			},
		}},
	}
	var out string
	for _, event := range events {
		out += formatResponsesFoldSSE(event)
	}
	return out
}

func responsesFoldTestResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func cloneFoldTestMap(src map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(src)
	var dst map[string]interface{}
	_ = json.Unmarshal(data, &dst)
	return dst
}

func foldTestInputTypes(raw interface{}) []string {
	items := raw.([]interface{})
	types := make([]string, 0, len(items))
	for _, item := range items {
		types = append(types, stringFromInterface(item.(map[string]interface{})["type"]))
	}
	return types
}

func foldTestIncludeContains(raw interface{}, want string) bool {
	items := raw.([]interface{})
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func foldTestOutputTypesAndIDs(output []interface{}) []string {
	result := make([]string, 0, len(output))
	for _, raw := range output {
		item := raw.(map[string]interface{})
		result = append(result, stringFromInterface(item["type"])+":"+stringFromInterface(item["id"]))
	}
	return result
}
