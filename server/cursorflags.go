package server

import (
	"encoding/json"
	"fmt"
)

// Cursor's CLI never lists its built-in tools. The backend picks them from the
// mode, the model and a set of capability fields the client sends: flags on
// AgentRunRequest and on the RequestContext it hands back, plus the MCP tool
// definitions it carries. Those fields are the client's half of the
// negotiation, and a mock sees all of them without a key. Field numbers are
// from the agent.v1 schema in the CLI bundle (2026.09.23-86fc751).

// cursorFlag is one negotiation field. Kind says how an absent field reads:
// "bool" is proto3 non-optional (absent = false), "optbool" and "string" are
// optional (absent = unset), "count" is a repeated message, "set" a message.
type cursorFlag struct {
	Name string
	Path []int
	Kind string
}

// cursorRunRequestFlags are read from AgentRunRequest.
var cursorRunRequestFlags = []cursorFlag{
	{"harness", []int{13}, "string"},
	{"client_supports_inline_images", []int{19}, "optbool"},
	{"can_create_cloud_subagents", []int{21}, "optbool"},
	{"suppress_subagent_progress_update_tool", []int{22}, "optbool"},
	{"client_supports_send_to_user", []int{23}, "optbool"},
	{"computer_use_coordinate_mode", []int{24}, "string"},
	{"client_supports_prompt_context_usage_rpc", []int{27}, "optbool"},
	{"client_supports_routed_model_update", []int{28}, "optbool"},
	{"system_prompt_spec", []int{29}, "set"},
	{"client_supports_preview_card", []int{31}, "optbool"},
	{"mcp_tools", []int{4, 1}, "count"}, // McpTools { 1 mcp_tools repeated }
}

// cursorContextFlags are read from RequestContext.
var cursorContextFlags = []cursorFlag{
	{"tools", []int{7}, "count"}, // McpToolDefinition: MCP tools only
	{"web_search_enabled", []int{17}, "optbool"},
	{"custom_subagents", []int{22}, "count"},
	{"web_fetch_enabled", []int{24}, "optbool"},
	{"hooks_config", []int{28}, "set"},
	{"agent_skills", []int{29}, "count"},
	{"supports_mcp_auth", []int{32}, "optbool"},
	{"read_lints_enabled", []int{35}, "optbool"},
	{"search_conversations_enabled", []int{50}, "optbool"},
	{"send_message_enabled", []int{51}, "optbool"},
	{"env.sandbox_enabled", []int{4, 5}, "bool"},
	{"env.sandbox_supported", []int{4, 14}, "optbool"},
	{"env.secret_redaction_enabled", []int{4, 18}, "optbool"},
	{"env.computer_use_supported", []int{4, 19}, "optbool"},
}

// cursorFlagValues reads flags out of msg. The last element of a flag's path
// is the field itself; the elements before it are messages to descend into.
func cursorFlagValues(msg []byte, prefix string, flags []cursorFlag) map[string]string {
	out := map[string]string{}
	for _, f := range flags {
		parent, ok := pbPath(msg, f.Path[:len(f.Path)-1]...)
		var hits []pbField
		if ok {
			fields, _ := pbDecode(parent)
			for _, x := range fields {
				if x.No == f.Path[len(f.Path)-1] {
					hits = append(hits, x)
				}
			}
		}
		v := "unset"
		switch {
		case f.Kind == "count":
			v = fmt.Sprint(len(hits))
		case f.Kind == "bool" && len(hits) == 0:
			v = "false"
		case len(hits) == 0:
		case f.Kind == "set":
			v = "set"
		case f.Kind == "string":
			v = string(hits[len(hits)-1].Data)
			if v == "" {
				v = "empty"
			}
		default:
			v = fmt.Sprint(hits[len(hits)-1].Num != 0)
		}
		out[prefix+f.Name] = v
	}
	return out
}

// cursorClientFlags reads the negotiation fields an AgentClientMessage
// carries, nil when it carries none. The request context comes either with
// the run request (user_message_action.request_context) or as the reply to
// request_context_args (exec_client_message.request_context_result.success).
func cursorClientFlags(msg []byte) map[string]string {
	out := map[string]string{}
	if run, ok := pbPath(msg, 1); ok {
		for k, v := range cursorFlagValues(run, "run_request.", cursorRunRequestFlags) {
			out[k] = v
		}
		if ctx, ok := pbPath(run, 2, 1, 2); ok {
			for k, v := range cursorFlagValues(ctx, "request_context.", cursorContextFlags) {
				out[k] = v
			}
		}
	}
	if ctx, ok := pbPath(msg, 2, 10, 1, 1); ok {
		for k, v := range cursorFlagValues(ctx, "request_context.", cursorContextFlags) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CursorClientFlags is the client's half of Cursor's tool negotiation as
// recorded since the last ClearLog: every run_request and request_context
// field in cursorRunRequestFlags and cursorContextFlags, first value seen.
// Empty when no cursor client talked to the mock.
func (s *MockServer) CursorClientFlags() map[string]string {
	out := map[string]string{}
	for _, e := range s.Log() {
		var entry struct {
			Flags map[string]string `json:"client_flags"`
		}
		if json.Unmarshal(e.Body, &entry) != nil {
			continue
		}
		for k, v := range entry.Flags {
			if _, seen := out[k]; !seen {
				out[k] = v
			}
		}
	}
	return out
}
