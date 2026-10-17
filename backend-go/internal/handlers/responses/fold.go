package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/observability"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	responsesFoldStep             = 518
	responsesFoldMinN             = 1
	responsesFoldMaxN             = 6
	responsesFoldMaxContinue      = 3
	responsesFoldMarkerText       = "Continue thinking..."
	responsesFoldEncryptedInclude = "reasoning.encrypted_content"
)

type responsesFoldRoundOpener func(body map[string]interface{}) (*http.Response, []byte, error)
type responsesFoldEmitter func(event map[string]interface{}) error

type responsesFoldResult struct {
	AgentUsage  map[string]interface{}
	BilledUsage map[string]interface{}
}

const responsesRetainedByteLimit = 32 * 1024 * 1024

// Counts retained serialized events and snapshots, not Go heap overhead.
type responsesRetentionBudget struct {
	observer *observability.Observer
	used     int
	exceeded bool
}

func (b *responsesRetentionBudget) reserve(n int) error {
	if b == nil {
		return nil
	}
	if n > responsesRetainedByteLimit-b.used {
		b.exceeded = true
		return fmt.Errorf("%w: retained Responses data exceeds 32MiB", common.ErrInvalidResponseBody)
	}
	b.used += n
	return nil
}
func responsesRetainedSize(v interface{}) int { data, _ := json.Marshal(v); return len(data) }
func (b *responsesRetentionBudget) hold(v interface{}) error {
	return b.reserve(responsesRetainedSize(v))
}
func (b *responsesRetentionBudget) release(n int) {
	if b != nil {
		b.used -= n
	}
}

type responsesFoldBufferedOutput struct {
	budget     *responsesRetentionBudget
	retained   int
	item       map[string]interface{}
	events     []map[string]interface{}
	index      interface{}
	live       bool
	emitted    int
	downstream int
}

func (e *responsesFoldBufferedOutput) keepEvent(event map[string]interface{}) error {
	n := responsesRetainedSize(event)
	if err := e.budget.reserve(n); err != nil {
		return err
	}
	e.retained += n
	return nil
}
func (e *responsesFoldBufferedOutput) setItem(item map[string]interface{}) error {
	old := 0
	if e.item != nil {
		old = responsesRetainedSize(e.item)
	}
	n := responsesRetainedSize(item)
	if err := e.budget.reserve(n - old); err != nil {
		return err
	}
	e.retained += n - old
	e.item = item
	return nil
}

func handleFoldedResponsesStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	provider *providers.ResponsesProvider,
	upstream *config.UpstreamConfig,
	apiKey string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
) (*types.Usage, error) {
	if provider == nil || upstream == nil || apiKey == "" {
		return handleStreamSuccess(c, resp, "responses", envCfg, sessionManager, time.Now(), originalReq, originalRequestJSON, common.StreamPreflightTimeouts{})
	}

	if err := utils.DecompressResponseBodyIfNeeded(resp); err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	var baseBody map[string]interface{}
	if err := json.Unmarshal(originalRequestJSON, &baseBody); err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	emitter := newResponsesFoldHTTPEmitter(c, resp, sessionManager, originalReq)
	emitter.budget.observer = common.PayloadObserver(c)
	defer emitter.clearPreflight()
	var codexCtx *converters.CodexToolContext
	if hoisted, ok := c.Get("codex_additional_tools_hoisted"); ok && hoisted == true {
		if ctxVal, ok := c.Get("codex_tool_context"); ok {
			if typed, ok := ctxVal.(converters.CodexToolContext); ok {
				codexCtx = &typed
			}
		}
	}
	openRound := func(body map[string]interface{}) (*http.Response, []byte, error) {
		bodyBytes, err := utils.MarshalJSONNoEscape(body)
		if err != nil {
			return nil, nil, err
		}
		req, _, err := provider.ConvertBodyToProviderRequest(c, upstream, apiKey, bodyBytes, c.Request.URL.Path)
		if err != nil {
			return nil, nil, err
		}
		req = common.WithRequestLogContext(req, c)
		roundResp, err := common.SendRequest(req, upstream, envCfg, true, "Responses")
		if err != nil {
			return nil, nil, err
		}
		if roundResp.StatusCode < 200 || roundResp.StatusCode >= 300 {
			respBody, _ := io.ReadAll(roundResp.Body)
			_ = roundResp.Body.Close()
			respBody = utils.DecompressGzipIfNeeded(roundResp, respBody)
			return nil, nil, fmt.Errorf("continuation upstream HTTP %d: %s", roundResp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		if err := utils.DecompressResponseBodyIfNeeded(roundResp); err != nil {
			_ = roundResp.Body.Close()
			return nil, nil, err
		}
		return roundResp, bodyBytes, nil
	}

	result, err := runResponsesFoldWithBudget(baseBody, resp, openRound, emitter.emit, codexCtx, emitter.budget)
	if err != nil {
		return nil, err
	}
	if !emitter.committed {
		return nil, common.ErrEmptyStreamResponse
	}
	emitter.finish()
	return responsesFoldMetricsUsage(result.BilledUsage), nil
}

func runResponsesFold(
	baseBody map[string]interface{},
	firstResp *http.Response,
	openRound responsesFoldRoundOpener,
	emit responsesFoldEmitter,
	codexCtx *converters.CodexToolContext,
) (responsesFoldResult, error) {
	return runResponsesFoldWithBudget(baseBody, firstResp, openRound, emit, codexCtx, &responsesRetentionBudget{})
}

func runResponsesFoldWithBudget(baseBody map[string]interface{}, firstResp *http.Response, openRound responsesFoldRoundOpener, emit responsesFoldEmitter, codexCtx *converters.CodexToolContext, budget *responsesRetentionBudget) (result responsesFoldResult, resultErr error) {
	origInput := responsesFoldInputItems(baseBody["input"])
	seq := 0
	dsOutputIndex := 0
	var baseResponse map[string]interface{}
	var finalOutput []interface{}
	var replayTail []interface{}
	summedUsage := make(map[string]interface{})
	var firstUsage map[string]interface{}
	var roundsInfo []map[string]interface{}

	stamp := func(event map[string]interface{}) map[string]interface{} {
		event = cloneStringInterfaceMap(event)
		event["sequence_number"] = seq
		seq++
		return event
	}

	currentResp := firstResp
	defer func() {
		_ = currentResp.Body.Close()
		if resultErr != nil && errors.Is(resultErr, common.ErrInvalidResponseBody) {
			incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, nil, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
			if err := emit(stamp(incomplete)); err == nil {
				result = responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}
				resultErr = nil
			}
		}
	}()
	for roundNo := 1; ; roundNo++ {
		iterator := newResponsesSSEIterator(currentResp.Body)
		iterator.budget = budget
		outputIndexToDownstream := make(map[string]int)
		outputKind := make(map[string]string)
		bufferedByIndex := make(map[string]*responsesFoldBufferedOutput)
		var bufferedOrder []*responsesFoldBufferedOutput
		var roundReasoning []interface{}
		var terminal map[string]interface{}
		var usage map[string]interface{}

		for {
			frame, ok, err := iterator.next()
			if err != nil || !ok {
				budget.observer.ReadEnd(err)
			}
			if ok {
				if frame.done {
					budget.observer.Event("done")
				} else {
					kind, _ := frame.event["type"].(string)
					budget.observer.Event(kind)
				}
			}
			if err != nil {
				// Synthetic incomplete is a CCX outcome, not an upstream SSE event.
				if budget.observer != nil {
					budget.observer.MarkSyntheticTerminal()
				}
				_ = currentResp.Body.Close()
				if budget.exceeded {
					return result, err
				}
				incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
				return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
			}
			if !ok {
				break
			}
			if frame.done {
				continue
			}

			// OpenRouter 等上游的 reasoning 非标准事件/条目归一化（按形状触发，标准流零改动）
			converters.NormalizeOpenRouterReasoningEvent(frame.event)

			eventType, _ := frame.event["type"].(string)
			switch eventType {
			case "response.created", "response.in_progress":
				if roundNo == 1 {
					if eventType == "response.created" {
						if baseResponse != nil {
							budget.release(responsesRetainedSize(baseResponse))
						}
						baseResponse = mapFromInterface(frame.event["response"])
						if err := budget.hold(baseResponse); err != nil {
							return result, err
						}
					}
					if err := emit(stamp(frame.event)); err != nil {
						_ = currentResp.Body.Close()
						return responsesFoldResult{BilledUsage: summedUsage}, err
					}
				}
				continue
			case "response.completed", "response.failed", "response.incomplete":
				if err := budget.hold(frame.event); err != nil {
					return result, err
				}
				terminal = frame.event
				usage = responsesFoldUsageFromTerminal(frame.event)
				goto roundDone
			}

			outputIndex, hasOutputIndex := frame.event["output_index"]
			outputIndexKey := responsesFoldOutputIndexKey(outputIndex)
			if eventType == "response.output_item.added" {
				item := mapFromInterface(frame.event["item"])
				if item["type"] == "reasoning" && bufferedByIndex[outputIndexKey] == nil {
					entry := &responsesFoldBufferedOutput{budget: budget, index: outputIndex, live: true, downstream: dsOutputIndex, emitted: 1}
					if err := entry.setItem(item); err != nil {
						return result, err
					}
					if err := entry.keepEvent(frame.event); err != nil {
						return result, err
					}
					entry.events = []map[string]interface{}{cloneStringInterfaceMap(frame.event)}
					bufferedByIndex[outputIndexKey] = entry
					bufferedOrder = append(bufferedOrder, entry)
					outputKind[outputIndexKey] = "reasoning"
					outputIndexToDownstream[outputIndexKey] = dsOutputIndex
					frame.event["output_index"] = dsOutputIndex
					dsOutputIndex++
					if err := emit(stamp(frame.event)); err != nil {
						_ = currentResp.Body.Close()
						return responsesFoldResult{BilledUsage: summedUsage}, err
					}
				} else {
					outputKind[outputIndexKey] = "buffered"
					entry := bufferedByIndex[outputIndexKey]
					if entry == nil {
						entry = &responsesFoldBufferedOutput{budget: budget, index: outputIndex}
						bufferedByIndex[outputIndexKey] = entry
						bufferedOrder = append(bufferedOrder, entry)
					}
					if err := entry.setItem(item); err != nil {
						return result, err
					}
					if err := entry.keepEvent(frame.event); err != nil {
						return result, err
					}
					entry.events = append(entry.events, frame.event)

				}
				continue
			}

			switch outputKind[outputIndexKey] {
			case "reasoning":
				entry := bufferedByIndex[outputIndexKey]
				if err := entry.keepEvent(frame.event); err != nil {
					return result, err
				}
				entry.events = append(entry.events, cloneStringInterfaceMap(frame.event))
				entry.emitted++
				if downstreamIndex, ok := outputIndexToDownstream[outputIndexKey]; ok && hasOutputIndex {
					frame.event["output_index"] = downstreamIndex
				}
				if eventType == "response.output_item.done" {
					item := mapFromInterface(frame.event["item"])
					if err := entry.setItem(item); err != nil {
						return result, err
					}
				}
				if err := emit(stamp(frame.event)); err != nil {
					_ = currentResp.Body.Close()
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			case "buffered":
				entry := bufferedByIndex[outputIndexKey]
				if entry == nil {
					continue
				}
				if err := entry.keepEvent(frame.event); err != nil {
					return result, err
				}
				entry.events = append(entry.events, frame.event)
				if eventType == "response.output_item.done" {
					if err := entry.setItem(mapFromInterface(frame.event["item"])); err != nil {
						return result, err
					}
				}
			default:
				if hasOutputIndex {
					entry := bufferedByIndex[outputIndexKey]
					if entry == nil {
						entry = &responsesFoldBufferedOutput{budget: budget, index: outputIndex}
						bufferedByIndex[outputIndexKey] = entry
						bufferedOrder = append(bufferedOrder, entry)
					}
					if err := entry.keepEvent(frame.event); err != nil {
						return result, err
					}
					entry.events = append(entry.events, frame.event)
					if eventType == "response.output_item.done" {
						if err := entry.setItem(mapFromInterface(frame.event["item"])); err != nil {
							return result, err
						}
					}
					continue
				}
				if err := emit(stamp(frame.event)); err != nil {
					_ = currentResp.Body.Close()
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			}
		}

	roundDone:
		_ = currentResp.Body.Close()
		responsesFoldSumUsage(summedUsage, usage)
		if roundNo == 1 {
			firstUsage = usage
		}

		var reconcileErr error
		bufferedOrder, reconcileErr = reconcileResponsesFoldOutput(bufferedOrder, terminal, budget)
		if reconcileErr != nil {
			if budget.exceeded {
				return result, reconcileErr
			}
			incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
			return responsesFoldResult{BilledUsage: summedUsage}, emit(stamp(incomplete))
		}
		for _, entry := range bufferedOrder {
			if entry.item["type"] != "reasoning" {
				continue
			}
			if !entry.live {
				entry.downstream = dsOutputIndex
				dsOutputIndex++
			}
			for _, event := range entry.events[entry.emitted:] {
				event["output_index"] = entry.downstream
				if err := emit(stamp(event)); err != nil {
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			}
			if err := budget.hold(entry.item); err != nil {
				return result, err
			}
			roundReasoning = append(roundReasoning, entry.item)
			if err := budget.hold(entry.item); err != nil {
				return result, err
			}
			finalOutput = append(finalOutput, entry.item)
		}
		reasoningTokens, hasReasoningTokens := responsesFoldReasoningTokens(usage)
		n, hasTier := responsesFoldTierN(reasoningTokens, hasReasoningTokens)
		roundInfo := map[string]interface{}{
			"round":            roundNo,
			"reasoning_tokens": nil,
			"n":                nil,
		}
		if hasReasoningTokens {
			roundInfo["reasoning_tokens"] = reasoningTokens
		}
		if hasTier {
			roundInfo["n"] = n
		}
		roundsInfo = append(roundsInfo, roundInfo)

		hasEncrypted := false
		if len(roundReasoning) > 0 {
			lastReasoning := mapFromInterface(roundReasoning[len(roundReasoning)-1])
			hasEncrypted = strings.TrimSpace(stringFromInterface(lastReasoning["encrypted_content"])) != ""
		}
		doContinue := terminal != nil && terminal["type"] == "response.completed" && hasTier && responsesFoldInContinueWindow(n) && hasEncrypted && roundNo <= responsesFoldMaxContinue

		stoppedReason := ""
		if !doContinue && hasTier {
			switch {
			case !hasEncrypted:
				stoppedReason = "no_encrypted_content"
			case roundNo > responsesFoldMaxContinue:
				stoppedReason = "max_continue"
			default:
				stoppedReason = "tier_out_of_window"
			}
		}

		if doContinue {
			for _, item := range roundReasoning {
				if err := budget.hold(item); err != nil {
					return result, err
				}
			}
			if err := budget.hold(responsesFoldCommentaryNudge()); err != nil {
				return result, err
			}
			for _, entry := range bufferedOrder {
				budget.release(entry.retained)
			}
			for _, item := range roundReasoning {
				budget.release(responsesRetainedSize(item))
			}
			if terminal != nil {
				budget.release(responsesRetainedSize(terminal))
			}
			bufferedOrder = nil
			bufferedByIndex = nil
			terminal = nil
			replayTail = append(replayTail, roundReasoning...)
			replayTail = append(replayTail, responsesFoldCommentaryNudge())
			nextBody := responsesFoldNextRoundBody(baseBody, append(append([]interface{}{}, origInput...), replayTail...))
			nextResp, _, err := openRound(nextBody)
			if err != nil {
				// Synthetic incomplete is a CCX outcome, not an upstream SSE event.
				if budget.observer != nil {
					budget.observer.MarkSyntheticTerminal()
				}
				incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
				return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
			}
			currentResp = nextResp
			continue
		}

		if terminal == nil {
			incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_eof", "upstream_eof")
			return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
		}

		for _, entry := range bufferedOrder {
			if entry.item["type"] == "reasoning" {
				continue
			}
			if err := remapFoldCodexEntryBudgeted(codexCtx, entry); err != nil {
				return result, err
			}
			for _, event := range entry.events {
				if _, ok := event["output_index"]; ok {
					event["output_index"] = dsOutputIndex
				}
				if err := emit(stamp(event)); err != nil {
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			}
			dsOutputIndex++
			if err := budget.hold(entry.item); err != nil {
				return result, err
			}
			finalOutput = append(finalOutput, entry.item)
		}

		agentUsage := responsesFoldAgentUsage(firstUsage, summedUsage, usage, true)
		terminalEvent := responsesFoldTerminalEvent(terminal, baseResponse, finalOutput, agentUsage, roundsInfo, summedUsage, stoppedReason, "")
		if err := emit(stamp(terminalEvent)); err != nil {
			return responsesFoldResult{AgentUsage: agentUsage, BilledUsage: summedUsage}, err
		}
		return responsesFoldResult{AgentUsage: agentUsage, BilledUsage: summedUsage}, nil
	}
}

// remapFoldCodexEntry 把上游扁平 function_call 输出还原为 codex 期望的形态
// （additional_tools 提升的逆变换）：custom 代理（functions__exec）→
// custom_tool_call 事件序列；namespace function → 原始 name + namespace 字段。
// fold 按 output_index 整项缓冲，轮末冲刷前 remap 完整 item，无需增量 delta 转换。
// reconcileResponsesFoldOutput runs before continuation selection so encrypted reasoning
// and terminal-only items participate in the same round as incremental output.
func reconcileResponsesFoldOutput(entries []*responsesFoldBufferedOutput, terminal map[string]interface{}, budgets ...*responsesRetentionBudget) ([]*responsesFoldBufferedOutput, error) {
	var budget *responsesRetentionBudget
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	byIndex := make(map[string]*responsesFoldBufferedOutput)
	byID := make(map[string]*responsesFoldBufferedOutput)
	for _, entry := range entries {
		item, err := responsesFoldEntryIdentity(entry)
		if err != nil {
			return nil, err
		}
		if err := entry.setItem(item); err != nil {
			return nil, err
		}
		key := responsesFoldOutputIndexKey(entry.index)
		if prior := byIndex[key]; prior != nil && prior != entry {
			return nil, fmt.Errorf("duplicate output index")
		}
		byIndex[key] = entry
		if id := stringFromInterface(item["id"]); id != "" {
			if prior := byID[id]; prior != nil && prior != entry {
				return nil, fmt.Errorf("duplicate output id")
			}
			byID[id] = entry
		}
	}
	snapshot, _ := mapFromInterface(terminal["response"])["output"].([]interface{})
	snapshotIDs := make(map[string]bool)
	matches := make([]*responsesFoldBufferedOutput, len(snapshot))
	claimed := make(map[*responsesFoldBufferedOutput]bool)
	unmatchedSnapshots := len(snapshot)
	// Snapshot offsets are compact; upstream output_index values may be sparse.
	// Reserve every stable-ID match before considering any positional fallback.
	for i, raw := range snapshot {
		id := stringFromInterface(mapFromInterface(raw)["id"])
		if id == "" {
			continue
		}
		if snapshotIDs[id] {
			return nil, fmt.Errorf("duplicate snapshot id")
		}
		snapshotIDs[id] = true
		if entry := byID[id]; entry != nil {
			matches[i] = entry
			claimed[entry] = true
			unmatchedSnapshots--
		}
	}
	unmatchedEntries := len(entries) - len(claimed)
	for _, entry := range entries {
		if !claimed[entry] && stringFromInterface(entry.item["id"]) == "" && unmatchedSnapshots > 1 {
			return nil, fmt.Errorf("ambiguous output without stable id")
		}
	}
	for i, raw := range snapshot {
		item := mapFromInterface(raw)
		match := matches[i]
		if match == nil {
			if stringFromInterface(item["id"]) == "" && unmatchedEntries > 1 {
				return nil, fmt.Errorf("ambiguous snapshot without stable id")
			}
			if candidate := byIndex[strconv.Itoa(i)]; candidate != nil && !claimed[candidate] {
				match = candidate
			}
		}
		if match == nil {
			match = &responsesFoldBufferedOutput{budget: budget, index: i}
			entries = append(entries, match)
		}
		claimed[match] = true
		merged := cloneStringInterfaceMap(match.item)
		for _, key := range []string{"id", "type", "call_id", "name", "namespace"} {
			a, b := stringFromInterface(merged[key]), stringFromInterface(item[key])
			if a != "" && b != "" && a != b {
				return nil, fmt.Errorf("conflicting output identity %s", key)
			}
		}
		for _, event := range match.events {
			if event["type"] != "response.output_item.done" {
				continue
			}
			prior := mapFromInterface(event["item"])
			for _, key := range []string{"content", "summary", "arguments", "input", "encrypted_content"} {
				a, aok := prior[key]
				b, bok := item[key]
				if !aok || !bok {
					continue
				}
				aj, _ := json.Marshal(a)
				bj, _ := json.Marshal(b)
				if !bytes.Equal(aj, bj) {
					return nil, fmt.Errorf("conflicting completed item %s", key)
				}
			}
		}
		for key, value := range item {
			merged[key] = value
		}
		if err := match.setItem(merged); err != nil {
			return nil, err
		}
		matches[i] = match
	}
	// Live reasoning keeps its committed prefix; fold always emits remaining
	// reasoning before buffered visible output. Reject a snapshot that contradicts it.
	ordered := make([]*responsesFoldBufferedOutput, 0, len(entries))
	for _, entry := range entries {
		if entry.live {
			ordered = append(ordered, entry)
		}
	}
	var pending []*responsesFoldBufferedOutput
	for _, match := range matches {
		if !match.live {
			pending = append(pending, match)
		}
	}
	for _, entry := range entries {
		if !entry.live && !claimed[entry] {
			pending = append(pending, entry)
		}
	}
	for _, reasoning := range []bool{true, false} {
		for _, entry := range pending {
			if (entry.item["type"] == "reasoning") == reasoning {
				ordered = append(ordered, entry)
			}
		}
	}
	snapshotPosition := make(map[*responsesFoldBufferedOutput]int)
	for i, match := range matches {
		snapshotPosition[match] = i
	}
	lastPosition := -1
	for _, entry := range ordered {
		if position, ok := snapshotPosition[entry]; ok {
			if position <= lastPosition {
				return nil, fmt.Errorf("snapshot contradicts committed/fold output order")
			}
			lastPosition = position
		}
	}
	entries = ordered
	for _, e := range entries {
		if err := completeResponsesFoldEntry(e); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// responsesFoldEntryIdentity validates the original lifecycle before any snapshot merge.
func responsesFoldEntryIdentity(e *responsesFoldBufferedOutput) (map[string]interface{}, error) {
	identity := make(map[string]interface{})
	check := func(item map[string]interface{}) error {
		for _, key := range []string{"id", "type", "call_id", "name", "namespace"} {
			value := stringFromInterface(item[key])
			if value == "" {
				continue
			}
			if old := stringFromInterface(identity[key]); old != "" && old != value {
				return fmt.Errorf("conflicting lifecycle identity %s", key)
			}
			identity[key] = value
		}
		return nil
	}
	for _, event := range e.events {
		if index, ok := event["output_index"]; ok && responsesFoldOutputIndexKey(index) != responsesFoldOutputIndexKey(e.index) {
			return nil, fmt.Errorf("conflicting lifecycle index")
		}
		typ := stringFromInterface(event["type"])
		if typ == "response.output_item.added" || typ == "response.output_item.done" {
			if err := check(mapFromInterface(event["item"])); err != nil {
				return nil, err
			}
		}
		item := map[string]interface{}{"id": event["item_id"], "call_id": event["call_id"], "name": event["name"], "namespace": event["namespace"]}
		switch {
		case strings.HasPrefix(typ, "response.output_text."):
			item["type"] = "message"
		case strings.HasPrefix(typ, "response.function_call_arguments."):
			item["type"] = "function_call"
		case strings.HasPrefix(typ, "response.custom_tool_call_input."):
			item["type"] = "custom_tool_call"
		case strings.HasPrefix(typ, "response.reasoning"):
			item["type"] = "reasoning"
		}
		if err := check(item); err != nil {
			return nil, err
		}
	}
	if err := check(e.item); err != nil {
		return nil, err
	}
	item := cloneStringInterfaceMap(e.item)
	for key, value := range identity {
		item[key] = value
	}
	return item, nil
}

func validateResponsesFoldLifecycle(events []map[string]interface{}, item map[string]interface{}) error {
	type fieldState struct{ partAdded, fieldDone, partDone bool }
	fields := make(map[int]*fieldState)
	message := item["type"] == "message"
	stem := ""
	switch item["type"] {
	case "message":
		stem = "response.output_text"
		parts, _ := item["content"].([]interface{})
		for i, raw := range parts {
			if mapFromInterface(raw)["type"] == "output_text" {
				fields[i] = &fieldState{}
			}
		}
	case "function_call":
		stem = "response.function_call_arguments"
		fields[-1] = &fieldState{}
	case "custom_tool_call":
		stem = "response.custom_tool_call_input"
		fields[-1] = &fieldState{}
	}
	added, done := false, false
	for _, event := range events {
		typ := stringFromInterface(event["type"])
		if typ == "response.output_item.added" {
			if added || done {
				return fmt.Errorf("duplicate/late item added")
			}
			added = true
			continue
		}
		if !added || done {
			return fmt.Errorf("event outside item lifecycle")
		}
		if typ == "response.output_item.done" {
			for _, state := range fields {
				if !state.fieldDone || (message && !state.partDone) {
					return fmt.Errorf("item done before field/part done")
				}
			}
			done = true
			continue
		}
		index := -1
		if message {
			index = intFromInterfaceDefault(event["content_index"])
		}
		state := fields[index]
		if state == nil {
			if typ == stem+".delta" || typ == stem+".done" {
				return fmt.Errorf("field event has no matching output part")
			}
			if message && (typ == "response.content_part.added" || typ == "response.content_part.done") {
				parts, _ := item["content"].([]interface{})
				if index < 0 || index >= len(parts) {
					return fmt.Errorf("part event has no matching output part")
				}
			}
			continue
		}
		switch typ {
		case "response.content_part.added":
			if !message || state.partAdded || state.fieldDone || mapFromInterface(event["part"])["type"] != "output_text" {
				return fmt.Errorf("duplicate/late part added")
			}
			state.partAdded = true
		case stem + ".delta", stem + ".done":
			if state.fieldDone || state.partDone || (message && !state.partAdded) {
				return fmt.Errorf("field event outside lifecycle")
			}
			if typ == stem+".done" {
				state.fieldDone = true
			}
		case "response.content_part.done":
			if !message || !state.partAdded || !state.fieldDone || state.partDone {
				return fmt.Errorf("part done outside lifecycle")
			}
			state.partDone = true
		}
	}
	if !done {
		return fmt.Errorf("missing item done")
	}
	return nil
}

func completeResponsesFoldEntry(e *responsesFoldBufferedOutput) error {
	if stringFromInterface(e.item["type"]) == "" {
		return fmt.Errorf("orphan output has no item")
	}
	added, done := false, false
	for _, event := range e.events {
		added = added || event["type"] == "response.output_item.added"
		done = done || event["type"] == "response.output_item.done"
	}
	event := func(kind string) map[string]interface{} {
		return map[string]interface{}{"type": kind, "output_index": e.index, "item_id": e.item["id"]}
	}
	var prefix []map[string]interface{}
	if added && !e.live {
		var ordered []map[string]interface{}
		for _, v := range e.events {
			if v["type"] == "response.output_item.added" {
				ordered = append(ordered, v)
			}
		}
		if len(ordered) != 1 {
			return fmt.Errorf("duplicate output item added")
		}
		for _, v := range e.events {
			if v["type"] != "response.output_item.added" {
				ordered = append(ordered, v)
			}
		}
		e.events = ordered
	}
	if !added {
		a := event("response.output_item.added")
		item := cloneStringInterfaceMap(e.item)
		item["status"] = "in_progress"
		delete(item, "arguments")
		delete(item, "input")
		if item["type"] == "message" {
			item["content"] = []interface{}{}
		}
		a["item"] = item
		if err := e.keepEvent(a); err != nil {
			return err
		}
		prefix = append(prefix, a)
	}
	// Only the unobserved suffix is synthesized; conflicting snapshots never concatenate.
	supplement := func(kind, field, full string, contentIndex int) error {
		seen := ""
		closed := false
		partClosed := false
		for _, v := range e.events {
			if contentIndex >= 0 && intFromInterfaceDefault(v["content_index"]) != contentIndex {
				continue
			}
			if v["type"] == kind+".delta" {
				if closed || partClosed {
					return fmt.Errorf("delta follows field/part done")
				}
				seen += stringFromInterface(v["delta"])
			}
			if contentIndex >= 0 && v["type"] == "response.content_part.done" {
				partClosed = true
				part := mapFromInterface(v["part"])
				if part["type"] != "output_text" || stringFromInterface(part["text"]) != full {
					return fmt.Errorf("conflicting completed part")
				}
			}
			if v["type"] == kind+".done" {
				if closed || partClosed {
					return fmt.Errorf("field done follows close")
				}
				closed = true
				if stringFromInterface(v[field]) != full {
					return fmt.Errorf("conflicting output done")
				}
			}
		}
		if !strings.HasPrefix(full, seen) {
			return fmt.Errorf("conflicting output delta")
		}
		var supplement []map[string]interface{}
		if len(full) > len(seen) {
			v := event(kind + ".delta")
			v["delta"] = full[len(seen):]
			if contentIndex >= 0 {
				v["content_index"] = contentIndex
			}
			supplement = append(supplement, v)
		}
		if !closed {
			v := event(kind + ".done")
			v[field] = full
			if contentIndex >= 0 {
				v["content_index"] = contentIndex
			}
			supplement = append(supplement, v)
		}
		if len(supplement) > 0 {
			for _, v := range supplement {
				if err := e.keepEvent(v); err != nil {
					return err
				}
			}
			insertAt := len(e.events)
			for i, v := range e.events {
				matchingPart := contentIndex >= 0 && intFromInterfaceDefault(v["content_index"]) == contentIndex
				if v["type"] == "response.output_item.done" || (v["type"] == kind+".done" && (contentIndex < 0 || matchingPart)) || (matchingPart && v["type"] == "response.content_part.done") {
					insertAt = i
					break
				}
			}
			if e.live && insertAt < e.emitted {
				return fmt.Errorf("snapshot extends delivered lifecycle")
			}
			suffix := append([]map[string]interface{}{}, e.events[insertAt:]...)
			e.events = append(append(e.events[:insertAt], supplement...), suffix...)
		}
		return nil
	}
	switch e.item["type"] {
	case "message":
		parts, _ := e.item["content"].([]interface{})
		for i, raw := range parts {
			part := mapFromInterface(raw)
			if part["type"] != "output_text" {
				continue
			}
			if err := supplement("response.output_text", "text", stringFromInterface(part["text"]), i); err != nil {
				return err
			}
		}
	case "function_call":
		if err := supplement("response.function_call_arguments", "arguments", stringFromInterface(e.item["arguments"]), -1); err != nil {
			return err
		}
	case "custom_tool_call":
		if err := supplement("response.custom_tool_call_input", "input", stringFromInterface(e.item["input"]), -1); err != nil {
			return err
		}
	}
	// Live reasoning already has a delivered prefix; only its unobserved suffix may change.
	if e.live && done {
		return validateResponsesFoldLifecycle(e.events, e.item)
	}
	events := append(prefix, e.events...)
	if !done {
		v := event("response.output_item.done")
		v["item"] = e.item
		if err := e.keepEvent(v); err != nil {
			return err
		}
		events = append(events, v)
	} else {
		for _, v := range events {
			if v["type"] == "response.output_item.done" {
				v["item"] = e.item
			}
		}
	}
	if e.item["type"] == "message" {
		parts, _ := e.item["content"].([]interface{})
		for i, raw := range parts {
			part := mapFromInterface(raw)
			if part["type"] != "output_text" {
				continue
			}
			hasAdded, hasDone := false, false
			for _, v := range events {
				if intFromInterfaceDefault(v["content_index"]) == i {
					hasAdded = hasAdded || v["type"] == "response.content_part.added"
					hasDone = hasDone || v["type"] == "response.content_part.done"
				}
			}
			var normalized []map[string]interface{}
			for _, v := range events {
				matching := intFromInterfaceDefault(v["content_index"]) == i
				if !hasAdded && matching && (v["type"] == "response.output_text.delta" || v["type"] == "response.output_text.done") {
					a := event("response.content_part.added")
					p := cloneStringInterfaceMap(part)
					p["text"] = ""
					a["part"] = p
					a["content_index"] = i
					if err := e.keepEvent(a); err != nil {
						return err
					}
					normalized = append(normalized, a)
					hasAdded = true
				}
				normalized = append(normalized, v)
				if !hasDone && matching && v["type"] == "response.output_text.done" {
					d := event("response.content_part.done")
					d["part"] = part
					d["content_index"] = i
					if err := e.keepEvent(d); err != nil {
						return err
					}
					normalized = append(normalized, d)
					hasDone = true
				}
			}
			events = normalized
		}
	}
	if err := validateResponsesFoldLifecycle(events, e.item); err != nil {
		return err
	}
	e.events = events
	return nil
}

func remapFoldCodexEntryBudgeted(ctx *converters.CodexToolContext, entry *responsesFoldBufferedOutput) error {
	if ctx == nil {
		return nil
	}
	// Remapping rules are unchanged; account the replacement before retaining it.
	var copyEntry responsesFoldBufferedOutput
	raw, _ := json.Marshal(entry.item)
	_ = json.Unmarshal(raw, &copyEntry.item)
	raw, _ = json.Marshal(entry.events)
	_ = json.Unmarshal(raw, &copyEntry.events)
	remapFoldCodexEntry(ctx, &copyEntry)
	n := responsesRetainedSize(copyEntry.item)
	for _, event := range copyEntry.events {
		n += responsesRetainedSize(event)
	}
	if err := entry.budget.reserve(n - entry.retained); err != nil {
		return err
	}
	entry.retained = n
	entry.item = copyEntry.item
	entry.events = copyEntry.events
	return nil
}

func remapFoldCodexEntry(ctx *converters.CodexToolContext, entry *responsesFoldBufferedOutput) {
	if ctx == nil || entry == nil || entry.item == nil {
		return
	}
	itemType, _ := entry.item["type"].(string)
	if itemType != "function_call" {
		return
	}
	name, _ := entry.item["name"].(string)
	callID, _ := entry.item["call_id"].(string)

	// custom 代理 → custom_tool_call 序列（与 chat_to_responses 转换器路径同形态）
	if ctx.IsCustomToolProxy(name) {
		args, _ := entry.item["arguments"].(string)
		customInput := converters.ReconstructCustomToolCallInput(*ctx, name, args)
		originalName := ctx.OriginalCustomToolName(name)
		itemID, _ := entry.item["id"].(string)
		if itemID == "" {
			itemID = "ctc_" + callID
		}
		remappedItem := map[string]interface{}{
			"id":      itemID,
			"type":    "custom_tool_call",
			"status":  "completed",
			"call_id": callID,
			"name":    originalName,
			"input":   customInput,
		}
		inputEvents := func(outputIndex interface{}) []map[string]interface{} {
			return []map[string]interface{}{
				{
					"type":         "response.custom_tool_call_input.delta",
					"item_id":      itemID,
					"call_id":      callID,
					"output_index": outputIndex,
					"delta":        customInput,
				},
				{
					"type":         "response.custom_tool_call_input.done",
					"item_id":      itemID,
					"call_id":      callID,
					"output_index": outputIndex,
					"input":        customInput,
				},
			}
		}
		events := make([]map[string]interface{}, 0, len(entry.events)+2)
		inputEmitted := false
		for _, event := range entry.events {
			eventType, _ := event["type"].(string)
			switch eventType {
			case "response.output_item.added":
				if added := mapFromInterface(event["item"]); added != nil {
					added["type"] = "custom_tool_call"
					added["name"] = originalName
					delete(added, "arguments")
					added["input"] = ""
					event["item"] = added
				}
				events = append(events, event)
			case "response.function_call_arguments.delta":
				// 聚合后一次性下发，丢弃增量
				continue
			case "response.function_call_arguments.done":
				events = append(events, inputEvents(event["output_index"])...)
				inputEmitted = true
			case "response.output_item.done":
				if !inputEmitted {
					events = append(events, inputEvents(event["output_index"])...)
					inputEmitted = true
				}
				event["item"] = remappedItem
				events = append(events, event)
			default:
				events = append(events, event)
			}
		}
		entry.events = events
		entry.item = remappedItem
		return
	}

	// namespace function → 还原原始 name + namespace 字段
	displayName, namespace := ctx.OpenAINameForFunctionTool(name)
	if namespace == "" {
		return
	}
	for _, event := range entry.events {
		eventType, _ := event["type"].(string)
		if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
			continue
		}
		if item := mapFromInterface(event["item"]); item != nil {
			item["name"] = displayName
			item["namespace"] = namespace
			event["item"] = item
		}
	}
	entry.item["name"] = displayName
	entry.item["namespace"] = namespace
}

type responsesFoldHTTPEmitter struct {
	c                  *gin.Context
	resp               *http.Response
	sessionManager     *session.SessionManager
	originalReq        *types.ResponsesRequest
	outputCollector    *streamOutputCollector
	reasoningCollector *thinkingcache.ResponsesStreamCollector
	pseudoScanner      *common.PseudoToolCallMarkerScanner
	budget             *responsesRetentionBudget
	preflightBytes     int
	toolNames          map[string]string
	toolTracker        *common.StreamToolCallTracker
	preflightEvents    []string
	preflightText      bytes.Buffer
	committed          bool
	seenEvent          bool
	seenCompleted      bool
	seenUsageOnly      bool
	seenUnknown        bool
	unknownEventType   string
}

func newResponsesFoldHTTPEmitter(
	c *gin.Context,
	resp *http.Response,
	sessionManager *session.SessionManager,
	originalReq *types.ResponsesRequest,
) *responsesFoldHTTPEmitter {
	return &responsesFoldHTTPEmitter{
		budget:             &responsesRetentionBudget{},
		toolNames:          make(map[string]string),
		toolTracker:        common.NewStreamToolCallTracker(),
		c:                  c,
		resp:               resp,
		sessionManager:     sessionManager,
		originalReq:        originalReq,
		outputCollector:    newStreamOutputCollector(),
		reasoningCollector: thinkingcache.NewResponsesStreamCollector(),
		pseudoScanner:      &common.PseudoToolCallMarkerScanner{},
	}
}

// observeStreamContent 流式观察器接线：fold 路径此前完全未接观察器，导致正向
// 白名单学习（真实工具调用）与负反馈（伪标记文本）对 codex 主流路径全盲。
// 全部客户端可见事件（含轮末冲刷的 buffered 输出）都会流经 emit，在此统一观测。
func (e *responsesFoldHTTPEmitter) observeStreamContent(event map[string]interface{}) {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		if delta, _ := event["delta"].(string); delta != "" && e.pseudoScanner.Feed(delta) {
			common.MarkPseudoToolCallMarker(e.c)
		}
	case "response.output_item.done":
		item := mapFromInterface(event["item"])
		switch item["type"] {
		case "function_call", "custom_tool_call":
			common.MarkStreamToolCallActivity(e.c)
		case "message":
			// 非 delta 形态上游（整体返回 message）的文本也要参与伪标记观测
			if contents, ok := item["content"].([]interface{}); ok {
				for _, content := range contents {
					part := mapFromInterface(content)
					if part["type"] != "output_text" {
						continue
					}
					if text, _ := part["text"].(string); text != "" && e.pseudoScanner.Feed(text) {
						common.MarkPseudoToolCallMarker(e.c)
					}
				}
			}
		}
	}
}

func (e *responsesFoldHTTPEmitter) clearPreflight() {
	e.preflightEvents = nil
	e.preflightText.Reset()
	e.budget.release(e.preflightBytes)
	e.preflightBytes = 0
}

func (e *responsesFoldHTTPEmitter) emit(event map[string]interface{}) (emitErr error) {
	common.ObservePayloadEmitter(e.c)
	defer func() {
		if emitErr != nil {
			e.clearPreflight()
		}
	}()
	e.observeStreamContent(event)
	eventString := formatResponsesFoldSSE(event)
	if !validResponsesToolCompletion(eventString, e.toolNames) {
		return fmt.Errorf("%w: malformed tool completion", common.ErrInvalidResponseBody)
	}
	if malformed, _ := e.toolTracker.ProcessResponsesEvent(responsesContentInspectionEvent(eventString)); malformed {
		return fmt.Errorf("%w: malformed tool completion", common.ErrInvalidResponseBody)
	}
	if e.committed {
		return e.writeEvent(eventString)
	}

	eventType, _ := event["type"].(string)
	if upstreamErr, ok := detectResponsesStreamError(eventString, eventType); ok {
		if r, m := detectResponsesErrorBlacklist(upstreamErr); r != "" {
			return &common.ErrBlacklistKey{Reason: r, Message: m, RecoverAt: utils.ExtractQuotaRecoverAt(m)}
		}
		diagnostic := formatResponsesErrorDiagnostic(upstreamErr)
		return fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, diagnostic)
	}

	if e.budget.exceeded {
		return fmt.Errorf("%w: retained Responses data exceeds 32MiB", common.ErrInvalidResponseBody)
	}
	if err := e.budget.reserve(len(eventString)); err != nil {
		e.preflightEvents = nil
		e.preflightText.Reset()
		return err
	}
	e.preflightBytes += len(eventString)
	e.preflightEvents = append(e.preflightEvents, eventString)
	e.seenEvent = true
	e.seenCompleted = e.seenCompleted || eventType == "response.completed"
	e.seenUsageOnly = e.seenUsageOnly || isResponsesUsageOnlyEvent(eventString)
	if t, ok := firstUnknownResponsesEventType(eventString); ok {
		e.seenUnknown = true
		if e.unknownEventType == "" {
			e.unknownEventType = t
		}
	}
	oldTextBytes := e.preflightText.Len()
	extractResponsesDeliverableTextFromEvent(eventString, &e.preflightText)
	textDelta := e.preflightText.Len() - oldTextBytes
	if err := e.budget.reserve(textDelta); err != nil {
		return err
	}
	e.preflightBytes += textDelta

	hasContent := common.HasResponsesDeliverableContent(responsesContentInspectionEvent(eventString)) || !common.IsEffectivelyEmptyStreamText(strings.TrimSpace(e.preflightText.String()))
	if hasContent && !e.toolTracker.HasPendingToolCall() {
		return e.commit()
	}
	if isResponsesFoldTerminalType(eventType) {
		if isCompactionV2UsageOnlyStream(e.originalReq != nil && hasCompactionTrigger(e.originalReq.Input), e.seenCompleted, e.seenUsageOnly) {
			return e.commit()
		}
		diagnostic := buildResponsesPreflightDiagnostic(e.seenEvent, e.seenCompleted, e.seenUsageOnly, e.seenUnknown, e.unknownEventType, e.preflightText.String())
		return fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, diagnostic)
	}
	return nil
}

func (e *responsesFoldHTTPEmitter) commit() error {
	if e.committed {
		return nil
	}
	// 竞速提交裁决：fold 路径是 native responses 流的主出口，写 Header 前必须
	// 仲裁——赢家 claim 桥接分支缓冲，败者返回 ErrRacingSuperseded 零字节退出
	// （ForStream 版带伪工具标记软校验，同 43fc0967 给转换器路径补的闸门）。
	// 这是当时漏掉的第四条路径：竞速武装后 fold 的全部写出只进分支缓冲、无人
	// Commit，客户端拿到空 200（2026-09-12 晚间 native responses 流量全空实测）。
	if !common.RacingClaimClientCommitForStream(e.c, e.preflightText.String()) {
		return common.ErrRacingSuperseded
	}
	utils.ForwardResponseHeaders(e.resp.Header, e.c.Writer)
	e.c.Header("Content-Type", "text/event-stream")
	e.c.Header("Cache-Control", "no-cache")
	e.c.Header("Connection", "keep-alive")
	e.c.Header("X-Accel-Buffering", "no")
	e.c.Status(e.resp.StatusCode)
	e.committed = true
	for _, event := range e.preflightEvents {
		if err := e.writeEvent(event); err != nil {
			return err
		}
	}
	e.preflightEvents = nil
	e.budget.release(e.preflightBytes)
	e.preflightBytes = 0
	e.preflightText.Reset()
	return nil
}

func (e *responsesFoldHTTPEmitter) writeEvent(event string) error {
	e.outputCollector.processEvent(event)
	e.reasoningCollector.ProcessEvent(event)
	if _, err := e.c.Writer.Write([]byte(event)); err != nil {
		return err
	}
	if flusher, ok := e.c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (e *responsesFoldHTTPEmitter) finish() {
	streamSessionID := writeStreamSession(e.sessionManager, e.originalReq, e.outputCollector)
	if streamSessionID != "" {
		e.reasoningCollector.Store(streamSessionID)
	}
}

type responsesSSEFrame struct {
	event map[string]interface{}
	done  bool
}

type responsesSSEIterator struct {
	scanner   *bufio.Scanner
	budget    *responsesRetentionBudget
	retained  int
	eventName string
	dataLines []string
}

func newResponsesSSEIterator(reader io.Reader) *responsesSSEIterator {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), utils.ResponsesSSEScannerMaxBufferSize)
	return &responsesSSEIterator{scanner: scanner}
}

func (it *responsesSSEIterator) next() (responsesSSEFrame, bool, error) {
	for it.scanner.Scan() {
		line := normalizeResponsesSSEFieldLine(strings.TrimRight(it.scanner.Text(), "\r"))
		if line == "" {
			if frame, ok, err := it.flush(); ok || err != nil {
				return frame, ok, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			it.eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if err := it.budget.reserve(len(data)); err != nil {
				return responsesSSEFrame{}, false, err
			}
			it.retained += len(data)
			it.dataLines = append(it.dataLines, data)
		}
	}
	if err := it.scanner.Err(); err != nil {
		return responsesSSEFrame{}, false, err
	}
	return it.flush()
}

func (it *responsesSSEIterator) flush() (responsesSSEFrame, bool, error) {
	defer func() { it.budget.release(it.retained); it.retained = 0 }()
	if len(it.dataLines) == 0 {
		it.eventName = ""
		return responsesSSEFrame{}, false, nil
	}
	data := strings.Join(it.dataLines, "\n")
	it.dataLines = nil
	eventName := it.eventName
	it.eventName = ""
	if data == "[DONE]" {
		return responsesSSEFrame{done: true}, true, nil
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return responsesSSEFrame{}, false, err
	}
	if _, ok := event["type"].(string); !ok && eventName != "" {
		event["type"] = eventName
	}
	return responsesSSEFrame{event: event}, true, nil
}

func responsesFoldInputItems(input interface{}) []interface{} {
	switch v := input.(type) {
	case []interface{}:
		return append([]interface{}{}, v...)
	case string:
		return []interface{}{map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []interface{}{map[string]interface{}{
				"type": "input_text",
				"text": v,
			}},
		}}
	default:
		if input == nil {
			return nil
		}
		return []interface{}{input}
	}
}

func responsesFoldNextRoundBody(base map[string]interface{}, inputItems []interface{}) map[string]interface{} {
	body := cloneStringInterfaceMap(base)
	delete(body, "transformer_metadata")
	body["stream"] = true
	body["input"] = inputItems
	include := responsesFoldIncludeValues(body["include"])
	if !stringSliceContains(include, responsesFoldEncryptedInclude) {
		include = append(include, responsesFoldEncryptedInclude)
	}
	body["include"] = include
	delete(body, "previous_response_id")
	return body
}

func responsesFoldIncludeValues(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := stringFromInterface(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string{}, v...)
	case string:
		return []string{v}
	default:
		return nil
	}
}

func responsesFoldCommentaryNudge() map[string]interface{} {
	return map[string]interface{}{
		"type": "message",
		"role": "assistant",
		"content": []interface{}{map[string]interface{}{
			"type": "output_text",
			"text": responsesFoldMarkerText,
		}},
		"phase": "commentary",
	}
}

func responsesFoldReasoningTokens(usage map[string]interface{}) (int, bool) {
	if usage == nil {
		return 0, false
	}
	details := mapFromInterface(usage["output_tokens_details"])
	return intFromInterface(details["reasoning_tokens"])
}

func responsesFoldTierN(tokens int, ok bool) (int, bool) {
	if !ok || tokens < responsesFoldStep-2 || (tokens+2)%responsesFoldStep != 0 {
		return 0, false
	}
	return (tokens + 2) / responsesFoldStep, true
}

func responsesFoldInContinueWindow(n int) bool {
	return n >= responsesFoldMinN && (responsesFoldMaxN == 0 || n <= responsesFoldMaxN)
}

func responsesFoldSumUsage(acc map[string]interface{}, usage map[string]interface{}) {
	if usage == nil {
		return
	}
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if value, ok := intFromInterface(usage[key]); ok {
			acc[key] = intFromInterfaceDefault(acc[key]) + value
		}
	}
	if cached, ok := intFromInterface(mapFromInterface(usage["input_tokens_details"])["cached_tokens"]); ok {
		details := mapFromInterface(acc["input_tokens_details"])
		details["cached_tokens"] = intFromInterfaceDefault(details["cached_tokens"]) + cached
		acc["input_tokens_details"] = details
	}
	if reasoning, ok := responsesFoldReasoningTokens(usage); ok {
		details := mapFromInterface(acc["output_tokens_details"])
		details["reasoning_tokens"] = intFromInterfaceDefault(details["reasoning_tokens"]) + reasoning
		acc["output_tokens_details"] = details
	}
}

func responsesFoldAgentUsage(first map[string]interface{}, summed map[string]interface{}, finalRound map[string]interface{}, flushedFinal bool) map[string]interface{} {
	inputTokens := intFromInterfaceDefault(first["input_tokens"])
	cached, hasCached := intFromInterface(mapFromInterface(first["input_tokens_details"])["cached_tokens"])
	reasoning := intFromInterfaceDefault(mapFromInterface(summed["output_tokens_details"])["reasoning_tokens"])
	finalPart := 0
	if flushedFinal && finalRound != nil {
		outputTokens := intFromInterfaceDefault(finalRound["output_tokens"])
		finalReasoning, _ := responsesFoldReasoningTokens(finalRound)
		if outputTokens > finalReasoning {
			finalPart = outputTokens - finalReasoning
		}
	}
	usage := map[string]interface{}{
		"input_tokens":          inputTokens,
		"output_tokens":         reasoning + finalPart,
		"total_tokens":          inputTokens + reasoning + finalPart,
		"output_tokens_details": map[string]interface{}{"reasoning_tokens": reasoning},
	}
	if hasCached {
		usage["input_tokens_details"] = map[string]interface{}{"cached_tokens": cached}
	}
	return usage
}

func responsesFoldTerminalEvent(
	upstreamTerminal map[string]interface{},
	baseResponse map[string]interface{},
	output []interface{},
	usage map[string]interface{},
	rounds []map[string]interface{},
	billed map[string]interface{},
	stoppedReason string,
	incompleteReason string,
) map[string]interface{} {
	upstreamResponse := map[string]interface{}{}
	if upstreamTerminal != nil {
		upstreamResponse = mapFromInterface(upstreamTerminal["response"])
	}
	response := cloneStringInterfaceMap(upstreamResponse)
	if len(baseResponse) > 0 {
		response = cloneStringInterfaceMap(baseResponse)
	}
	response["output"] = output
	response["usage"] = usage
	metadata := mapFromInterface(response["metadata"])
	metadata["proxy_rounds"] = rounds
	metadata["proxy_billed_usage"] = billed
	if stoppedReason != "" {
		metadata["proxy_stopped_reason"] = stoppedReason
	}
	response["metadata"] = metadata

	if incompleteReason != "" {
		response["status"] = "incomplete"
		response["incomplete_details"] = map[string]interface{}{"reason": incompleteReason}
		return map[string]interface{}{"type": "response.incomplete", "response": response}
	}

	status := stringFromInterface(upstreamResponse["status"])
	if status == "" {
		status = "completed"
	}
	response["status"] = status
	if details, ok := upstreamResponse["incomplete_details"]; ok {
		response["incomplete_details"] = details
	}
	eventType := "response.completed"
	if upstreamTerminal != nil {
		if t := stringFromInterface(upstreamTerminal["type"]); t != "" {
			eventType = t
		}
	}
	return map[string]interface{}{"type": eventType, "response": response}
}

func responsesFoldUsageFromTerminal(event map[string]interface{}) map[string]interface{} {
	if response := mapFromInterface(event["response"]); len(response) > 0 {
		if usage := mapFromInterface(response["usage"]); len(usage) > 0 {
			return usage
		}
	}
	return mapFromInterface(event["usage"])
}

func responseUsageMap(event map[string]interface{}) map[string]interface{} {
	return responsesFoldUsageFromTerminal(event)
}

func responsesFoldOutputIndexKey(value interface{}) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.Itoa(int(v))
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func formatResponsesFoldSSE(event map[string]interface{}) string {
	eventType := stringFromInterface(event["type"])
	if eventType == "" {
		eventType = "message"
	}
	data, _ := utils.MarshalJSONNoEscape(event)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
}

func responsesFoldMetricsUsage(usage map[string]interface{}) *types.Usage {
	if usage == nil {
		return nil
	}
	responsesUsage := types.ResponsesUsage{
		InputTokens:  intFromInterfaceDefault(usage["input_tokens"]),
		OutputTokens: intFromInterfaceDefault(usage["output_tokens"]),
		TotalTokens:  intFromInterfaceDefault(usage["total_tokens"]),
	}
	if cached, ok := intFromInterface(mapFromInterface(usage["input_tokens_details"])["cached_tokens"]); ok {
		responsesUsage.InputTokensDetails = &types.InputTokensDetails{CachedTokens: cached}
	}
	if reasoning, ok := intFromInterface(mapFromInterface(usage["output_tokens_details"])["reasoning_tokens"]); ok {
		responsesUsage.OutputTokensDetails = &types.OutputTokensDetails{ReasoningTokens: reasoning}
	}
	return metricsUsageFromResponsesUsage(responsesUsage, responsesUsage.InputTokens)
}

func isResponsesFoldTerminalType(eventType string) bool {
	switch eventType {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func mapFromInterface(value interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func cloneStringInterfaceMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func stringFromInterface(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func intFromInterface(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func intFromInterfaceDefault(value interface{}) int {
	i, _ := intFromInterface(value)
	return i
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
