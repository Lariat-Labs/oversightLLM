// Package activity turns OpenTelemetry GenAI spans (as stored by Application Insights)
// into agent runs, tool usage, an overview, and per-conversation chains of steps.
// Everything here is pure: rows in, structs out.
package activity

import "time"

// Span is one normalised telemetry record.
type Span struct {
	Time           time.Time         `json:"time"`
	ID             string            `json:"id"`
	ParentID       string            `json:"parentId,omitempty"`
	OperationID    string            `json:"operationId"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"` // agent | llm | tool | internal | http | request | log
	Operation      string            `json:"operation,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	ConversationID string            `json:"conversationId,omitempty"`
	Tool           string            `json:"tool,omitempty"`
	Model          string            `json:"model,omitempty"`
	DurationMs     float64           `json:"durationMs"`
	Success        bool              `json:"success"`
	InputTokens    int               `json:"inputTokens,omitempty"`
	OutputTokens   int               `json:"outputTokens,omitempty"`
	Turn           int               `json:"turn"` // -1 when unknown
	ToolsRequested []string          `json:"toolsRequested,omitempty"`
	Outcome        string            `json:"outcome,omitempty"`
	Error          string            `json:"error,omitempty"`
	StopReason     string            `json:"stopReason,omitempty"`
	User           string            `json:"user,omitempty"`
	Role           string            `json:"role,omitempty"`
	Input          string            `json:"input,omitempty"`
	Output         string            `json:"output,omitempty"`
	SystemPrompt   string            `json:"systemPrompt,omitempty"`
	ToolDefs       []string          `json:"toolDefs,omitempty"`
	Message        string            `json:"message,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Totals struct {
	Runs          int `json:"runs"`
	Running       int `json:"running"`
	FailedRuns    int `json:"failedRuns"`
	Conversations int `json:"conversations"`
	LLMCalls      int `json:"llmCalls"`
	ToolCalls     int `json:"toolCalls"`
	ToolErrors    int `json:"toolErrors"`
	InputTokens   int `json:"inputTokens"`
	OutputTokens  int `json:"outputTokens"`
	ActiveAgents  int `json:"activeAgents"`
}

type AgentStats struct {
	ID            string      `json:"id"`
	Runs          int         `json:"runs"`
	Running       int         `json:"running"`
	FailedRuns    int         `json:"failedRuns"`
	ToolCalls     int         `json:"toolCalls"`
	ToolErrors    int         `json:"toolErrors"`
	LLMCalls      int         `json:"llmCalls"`
	InputTokens   int         `json:"inputTokens"`
	OutputTokens  int         `json:"outputTokens"`
	AvgDurationMs float64     `json:"avgDurationMs"`
	P95DurationMs float64     `json:"p95DurationMs"`
	LastSeen      time.Time   `json:"lastSeen"`
	Timeline      []Bucket    `json:"timeline"`
	TopTools      []NameCount `json:"topTools"`
	Users         []string    `json:"users"`
}

type ToolStats struct {
	Name     string      `json:"name"`
	Calls    int         `json:"calls"`
	Errors   int         `json:"errors"`
	AvgMs    float64     `json:"avgMs"`
	Agents   []string    `json:"agents"`
	Outcomes []NameCount `json:"outcomes"`
}

// Task is one agent run (one conversation turn from the user's point of view).
type Task struct {
	ConversationID string    `json:"conversationId"`
	Agent          string    `json:"agent"`
	Started        time.Time `json:"started"`
	LastActivity   time.Time `json:"lastActivity"`
	DurationMs     float64   `json:"durationMs"`
	Status         string    `json:"status"` // running | completed | failed
	Input          string    `json:"input"`
	OutputPreview  string    `json:"outputPreview"`
	ToolSequence   []string  `json:"toolSequence"`
	ToolErrors     int       `json:"toolErrors"`
	Turns          int       `json:"turns"`
	Tokens         int       `json:"tokens"`
	User           string    `json:"user,omitempty"`
	StopReason     string    `json:"stopReason,omitempty"`
	Model          string    `json:"model,omitempty"`
}

type Bucket struct {
	Start     time.Time `json:"start"`
	Runs      int       `json:"runs"`
	ToolCalls int       `json:"toolCalls"`
	Errors    int       `json:"errors"`
	Tokens    int       `json:"tokens"`
}

type FeedItem struct {
	Time           time.Time `json:"time"`
	Agent          string    `json:"agent"`
	ConversationID string    `json:"conversationId"`
	Kind           string    `json:"kind"`
	Text           string    `json:"text"`
	OK             bool      `json:"ok"`
}

type Overview struct {
	GeneratedAt time.Time    `json:"generatedAt"`
	Window      string       `json:"window"`
	Totals      Totals       `json:"totals"`
	Agents      []AgentStats `json:"agents"`
	Tools       []ToolStats  `json:"tools"`
	Tasks       []Task       `json:"tasks"`
	Timeline    []Bucket     `json:"timeline"`
	Feed        []FeedItem   `json:"feed"`
}

// Step is one link in a conversation's chain.
type Step struct {
	ID         string            `json:"id"`
	ParentID   string            `json:"parentId,omitempty"`
	Time       time.Time         `json:"time"`
	Kind       string            `json:"kind"` // user | llm | tool | subcall | http | answer | log
	Title      string            `json:"title"`
	Detail     string            `json:"detail,omitempty"`
	DurationMs float64           `json:"durationMs"`
	OK         bool              `json:"ok"`
	Turn       int               `json:"turn"`
	Depth      int               `json:"depth"`
	Tokens     int               `json:"tokens,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Conversation struct {
	ID           string    `json:"id"`
	Agent        string    `json:"agent"`
	User         string    `json:"user,omitempty"`
	Model        string    `json:"model,omitempty"`
	Started      time.Time `json:"started"`
	Ended        time.Time `json:"ended"`
	DurationMs   float64   `json:"durationMs"`
	Status       string    `json:"status"`
	StopReason   string    `json:"stopReason,omitempty"`
	Input        string    `json:"input"`
	Output       string    `json:"output"`
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	ToolDefs     []string  `json:"toolDefs,omitempty"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	ToolCalls    int       `json:"toolCalls"`
	ToolErrors   int       `json:"toolErrors"`
	Runs         int       `json:"runs"`
	Steps        []Step    `json:"steps"`
}
