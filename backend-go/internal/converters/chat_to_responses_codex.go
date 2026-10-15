package converters

import "github.com/tidwall/gjson"

func buildCodexToolContextFromRequest(originalRequestRawJSON []byte) CodexToolContext {
	if originalRequestRawJSON == nil {
		return CodexToolContext{}
	}

	req := gjson.ParseBytes(originalRequestRawJSON)

	// Check codex_tool_compat_enabled flag from TransformerMetadata.
	if enabled := req.Get("transformer_metadata.codex_tool_compat_enabled"); !enabled.Exists() || !enabled.Bool() {
		return CodexToolContext{}
	}
	if tools := req.Get("tools"); tools.Exists() && tools.IsArray() {
		rawTools := make([]interface{}, 0, len(tools.Array()))
		for _, t := range tools.Array() {
			rawTools = append(rawTools, t.Value())
		}
		return BuildCodexToolContextFromRaw(rawTools)
	}

	return CodexToolContext{}
}

func (st *chatToResponsesState) ensureCodexToolContext(originalRequestRawJSON []byte) {
	if st.CodexCtxInitialized {
		return
	}
	// Extract codexToolCompat flag from raw JSON (injected by handler).
	if originalRequestRawJSON != nil {
		req := gjson.ParseBytes(originalRequestRawJSON)
		if enabled := req.Get("transformer_metadata.codex_tool_compat_enabled"); enabled.Exists() {
			st.CodexToolCompatEnabled = enabled.Bool()
		}
	}
	if st.CodexToolCompatEnabled {
		st.CodexCtx = buildCodexToolContextFromRequest(originalRequestRawJSON)
	}
	st.CodexCtxInitialized = true
}

func (st *chatToResponsesState) customToolOutputIndex(idx int) int {
	if st.FuncOutputIndexes == nil {
		st.FuncOutputIndexes = make(map[int]int)
	}
	if index, ok := st.FuncOutputIndexes[idx]; ok {
		return index
	}
	index := st.allocateOutputIndex()
	st.FuncOutputIndexes[idx] = index
	return index
}
