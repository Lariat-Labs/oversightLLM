package activity

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Row = map[string]any

// SpanFromActivityRow parses a row produced by ActivityQuery.
func SpanFromActivityRow(r Row) Span {
	s := baseSpan(r)
	s.Operation = str(r["op"])
	s.Agent = str(r["agent"])
	s.ConversationID = str(r["conv"])
	s.Tool = str(r["tool"])
	s.Model = str(r["model"])
	s.InputTokens = toInt(r["inTok"])
	s.OutputTokens = toInt(r["outTok"])
	s.Turn = turn(r["turn"])
	s.ToolsRequested = splitList(str(r["toolsReq"]))
	s.Outcome = str(r["outcome"])
	s.Error = str(r["err"])
	s.StopReason = str(r["stop"])
	s.User = str(r["usr"])
	s.Input = MessagesText(str(r["input"]))
	s.Output = MessagesText(str(r["output"]))
	s.Kind = kindFor(s.Operation, "internal")
	return s
}

// SpanFromDetailRow parses a row produced by ConversationQuery (full Properties).
func SpanFromDetailRow(r Row) Span {
	s := baseSpan(r)
	s.Kind = str(r["Kind"])
	s.Message = str(r["Message"])
	p := properties(r["Properties"])
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := p[k]; v != "" {
				return v
			}
		}
		return ""
	}
	s.Operation = get("gen_ai.operation.name")
	s.Agent = get("gen_ai.agent.id", "gen_ai.agent.name", "microsoft.gen_ai.main_agent.id")
	s.ConversationID = get("gen_ai.conversation.id", "microsoft.gen_ai.main_agent.conversation_id", "agw.run_id")
	s.Tool = get("gen_ai.tool.name")
	s.Model = get("gen_ai.response.model", "gen_ai.request.model")
	s.InputTokens = toInt(p["gen_ai.usage.input_tokens"])
	s.OutputTokens = toInt(p["gen_ai.usage.output_tokens"])
	s.Turn = turn(p["agw.turn"])
	s.ToolsRequested = splitList(p["agw.tools_requested"])
	s.Outcome = get("agw.tool.outcome", "secsdk.outcome")
	s.Error = get("error.type")
	s.StopReason = get("agw.stop_reason", "gen_ai.response.finish_reasons")
	s.User = get("agw.user.name", "enduser.id")
	s.Input = MessagesText(p["gen_ai.input.messages"])
	s.Output = MessagesText(p["gen_ai.output.messages"])
	s.SystemPrompt = MessagesText(p["gen_ai.system_instructions"])
	s.ToolDefs = toolNames(p["gen_ai.tool.definitions"])
	if s.Operation != "" {
		s.Kind = kindFor(s.Operation, s.Kind)
	}
	// Keep the remaining small attributes for the UI, minus bulky or internal ones.
	s.Attributes = map[string]string{}
	for k, v := range p {
		if strings.HasPrefix(k, "_MS.") || len(v) > 400 {
			continue
		}
		switch k {
		case "gen_ai.input.messages", "gen_ai.output.messages", "gen_ai.system_instructions", "gen_ai.tool.definitions":
			continue
		}
		s.Attributes[k] = v
	}
	if lvl := str(r["SeverityLevel"]); lvl != "" && s.Kind == "log" {
		s.Attributes["severity"] = lvl
	}
	return s
}

func baseSpan(r Row) Span {
	s := Span{
		ID:          str(r["Id"]),
		ParentID:    str(r["ParentId"]),
		OperationID: str(r["OperationId"]),
		Name:        str(r["Name"]),
		DurationMs:  toFloat(r["DurationMs"]),
		Success:     toBool(r["Success"], true),
		Role:        str(r["AppRoleName"]),
		Turn:        -1,
	}
	s.Time, _ = time.Parse(time.RFC3339Nano, str(r["TimeGenerated"]))
	return s
}

func kindFor(op, fallback string) string {
	switch op {
	case "invoke_agent", "create_agent":
		return "agent"
	case "chat", "text_completion", "generate_content":
		return "llm"
	case "execute_tool":
		return "tool"
	}
	return fallback
}

// Failed reports whether a span represents a failure.
func (s Span) Failed() bool {
	if !s.Success || s.Error != "" {
		return true
	}
	return s.Outcome != "" && s.Outcome != "ok" && s.Outcome != "not_found"
}

// MessagesText extracts readable text from GenAI message JSON. It accepts the
// current "parts" format, the older {"role","content"} format, plain strings and
// truncated JSON (falls back to the raw text).
func MessagesText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		var one map[string]any
		if json.Unmarshal([]byte(raw), &one) == nil {
			msgs = []map[string]any{one}
		} else if txt := salvageContent(raw); txt != "" {
			return txt
		} else {
			return raw
		}
	}
	var out []string
	for _, m := range msgs {
		if parts, ok := m["parts"].([]any); ok {
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				switch str(pm["type"]) {
				case "text", "":
					if t := str(pm["content"]); t != "" {
						out = append(out, t)
					}
				case "tool_call":
					out = append(out, fmt.Sprintf("[tool call %s %s]", str(pm["name"]), compactJSON(pm["arguments"])))
				case "tool_call_response":
					out = append(out, fmt.Sprintf("[tool result %s]", truncate(compactJSON(pm["response"]), 300)))
				}
			}
			continue
		}
		switch c := m["content"].(type) {
		case string:
			out = append(out, c)
		case nil:
		default:
			out = append(out, compactJSON(c))
		}
	}
	return strings.Join(out, "\n")
}

func toolNames(raw string) []string {
	var defs []map[string]any
	if json.Unmarshal([]byte(raw), &defs) != nil {
		return nil
	}
	var names []string
	for _, d := range defs {
		name := str(d["name"])
		if fn, ok := d["function"].(map[string]any); ok && name == "" {
			name = str(fn["name"])
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func properties(v any) map[string]string {
	out := map[string]string{}
	var m map[string]any
	switch t := v.(type) {
	case map[string]any:
		m = t
	case string:
		if json.Unmarshal([]byte(t), &m) != nil {
			return out
		}
	}
	for k, val := range m {
		out[k] = str(val)
	}
	return out
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if t == "None" || t == "null" {
			return ""
		}
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return compactJSON(t)
	}
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func toInt(v any) int { return int(toFloat(v)) }

func toBool(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		if b, err := strconv.ParseBool(strings.ToLower(t)); err == nil {
			return b
		}
	}
	return def
}

func turn(v any) int {
	if str(v) == "" {
		return -1
	}
	return toInt(v)
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func compactJSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// salvageContent pulls "content" strings out of message JSON that was cut off
// mid-way (the activity query truncates long messages).
func salvageContent(raw string) string {
	var out []string
	rest := raw
	for {
		i := strings.Index(rest, `"content":"`)
		if i < 0 {
			break
		}
		rest = rest[i+len(`"content":"`):]
		end, esc := -1, false
		for j, r := range rest {
			if esc {
				esc = false
				continue
			}
			if r == '\\' {
				esc = true
			} else if r == '"' {
				end = j
				break
			}
		}
		chunk := rest
		if end >= 0 {
			chunk = rest[:end]
		}
		chunk = strings.TrimSuffix(chunk, `\\`)
		var txt string
		if json.Unmarshal([]byte(`"`+chunk+`"`), &txt) != nil {
			txt = chunk
		}
		out = append(out, txt)
		if end < 0 {
			break
		}
		rest = rest[end:]
	}
	return strings.Join(out, "\n")
}
