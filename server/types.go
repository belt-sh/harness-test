package server

// Shared request fields — only the fields we need to extract.
type llmRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	Tools     []any  `json:"tools,omitempty"`
	Functions []any  `json:"functions,omitempty"`
}

func (r llmRequest) hasTools() bool { return len(r.Tools) > 0 || len(r.Functions) > 0 }

func (r llmRequest) modelOrDefault() string {
	if r.Model != "" {
		return r.Model
	}
	return "mock-model"
}

// OpenAI Chat Completions types

type ChatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

type ChatChoice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

type ChatMessage struct {
	Role      string     `json:"role,omitempty"`
	Content   any        `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Anthropic Messages types

type AnthropicMessage struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      *AntUsage      `json:"usage,omitempty"`
}

type ContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type AntUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// OpenAI Responses API types

type ResponseObject struct {
	ID     string         `json:"id"`
	Object string         `json:"object,omitempty"`
	Ts     int64          `json:"created"`
	TsAlt  int64          `json:"created_at"`
	Status string         `json:"status"`
	Output []ResponseItem `json:"output"`
	Usage  *Usage         `json:"usage,omitempty"`
}

type ResponseItem struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Role      string         `json:"role,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"`
	Status    string         `json:"status,omitempty"`
	Content   []ResponsePart `json:"content,omitempty"`
}

type ResponsePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Shared

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens"`
}

type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Data []ModelEntry `json:"data"`
}
