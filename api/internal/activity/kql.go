package activity

import (
	"fmt"
	"strings"
	"time"
)

// Columns shared by the activity queries. Attribute names follow the OpenTelemetry
// GenAI conventions, with the agent gateway's agw.* extras when present.
const genAIProjection = `
| extend P = Properties
| extend op = tostring(P["gen_ai.operation.name"])
| extend agent = coalesce(tostring(P["gen_ai.agent.id"]), tostring(P["gen_ai.agent.name"]), tostring(P["microsoft.gen_ai.main_agent.id"]))
| extend conv = coalesce(tostring(P["gen_ai.conversation.id"]), tostring(P["microsoft.gen_ai.main_agent.conversation_id"]), tostring(P["agw.run_id"]))
| project TimeGenerated, Id, ParentId, OperationId, Name, DurationMs, Success, AppRoleName, op, agent, conv,
    tool = tostring(P["gen_ai.tool.name"]),
    model = coalesce(tostring(P["gen_ai.response.model"]), tostring(P["gen_ai.request.model"])),
    inTok = toint(P["gen_ai.usage.input_tokens"]),
    outTok = toint(P["gen_ai.usage.output_tokens"]),
    turn = tostring(P["agw.turn"]),
    toolsReq = tostring(P["agw.tools_requested"]),
    outcome = coalesce(tostring(P["agw.tool.outcome"]), tostring(P["secsdk.outcome"])),
    err = tostring(P["error.type"]),
    stop = coalesce(tostring(P["agw.stop_reason"]), tostring(P["gen_ai.response.finish_reasons"])),
    usr = coalesce(tostring(P["agw.user.name"]), tostring(P["enduser.id"])),
    input = substring(tostring(P["gen_ai.input.messages"]), 0, 3000),
    output = substring(tostring(P["gen_ai.output.messages"]), 0, 3000)`

// ActivityQuery returns GenAI spans newer than since (zero = whole timespan).
func ActivityQuery(since time.Time, limit int) string {
	where := ""
	if !since.IsZero() {
		where = fmt.Sprintf("\n| where TimeGenerated > datetime(%s)", since.UTC().Format(time.RFC3339Nano))
	}
	return "AppDependencies" + where + `
| where isnotempty(tostring(Properties["gen_ai.operation.name"]))` + genAIProjection + fmt.Sprintf(`
| order by TimeGenerated desc
| take %d`, limit)
}

// ConversationQuery returns every span and log line in the operations (traces)
// that carry the given conversation id, including untagged child spans.
func ConversationQuery(conversationID string) string {
	id := kqlString(conversationID)
	return fmt.Sprintf(`let ops = AppDependencies
| where tostring(Properties["gen_ai.conversation.id"]) == %[1]s
    or tostring(Properties["microsoft.gen_ai.main_agent.conversation_id"]) == %[1]s
    or tostring(Properties["agw.run_id"]) == %[1]s
| distinct OperationId;
union
  (AppDependencies | where OperationId in (ops) | extend Kind = iff(DependencyType == "InProc", "internal", "http"), Message = ""),
  (AppRequests | where OperationId in (ops) | extend Kind = "request", Message = ""),
  (AppTraces | where OperationId in (ops) | extend Kind = "log", Name = "log", Id = "", DurationMs = 0.0, Success = true)
| project TimeGenerated, Id, ParentId, OperationId, Name, DurationMs, Success, AppRoleName, Kind, Message, Properties, SeverityLevel
| order by TimeGenerated asc
| take 2000`, id)
}

func kqlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
