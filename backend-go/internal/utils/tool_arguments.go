package utils

import (
	"encoding/json"
	"strings"
)

// IsMalformedNamedToolArguments preserves the shared named-tool argument rules.
func IsMalformedNamedToolArguments(name string, args interface{}) bool {
	if args == nil {
		return ToolRequiresArguments(name)
	}

	switch v := args.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || trimmed == "{}" {
			return ToolRequiresArguments(name)
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return true
		}
		return parsedJSONEmptyObject(parsed) && ToolRequiresArguments(name)
	case map[string]interface{}:
		return len(v) == 0 && ToolRequiresArguments(name)
	case []interface{}:
		return len(v) == 0 && ToolRequiresArguments(name)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return true
		}
		return IsMalformedNamedToolArguments(name, string(b))
	}
}

func ToolRequiresArguments(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "edit", "write", "bash", "grep", "glob", "webfetch", "websearch", "agent", "taskcreate", "taskupdate", "taskget", "taskoutput", "taskstop", "notebookedit", "askuserquestion", "sendmessage", "skill", "croncreate", "crondelete", "enterworktree", "exitworktree", "mcp__serena__read_memory", "mcp__serena__write_memory", "mcp__serena__find_symbol", "mcp__serena__replace_content":
		return true
	default:
		return false
	}
}

func parsedJSONEmptyObject(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	return ok && len(m) == 0
}
