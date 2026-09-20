package server

import "testing"

// The four wire formats spell the argument schema differently, and a probe
// that cannot read it cannot fill a query in. Gemini nests its schema one
// level deeper than the rest, which is the case that broke first.
func TestToolSchemaIsReadFromEveryWireFormat(t *testing.T) {
	cases := map[string][]byte{
		"openai":    []byte(`{"tools":[{"type":"function","function":{"name":"ToolSearch","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}}]}`),
		"anthropic": []byte(`{"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]}`),
		"responses": []byte(`{"tools":[{"type":"function","name":"ToolSearch","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}]}`),
		"gemini":    []byte(`{"tools":[{"functionDeclarations":[{"name":"ToolSearch","parametersJsonSchema":{"type":"object","properties":{"query":{"type":"string"}}}}]}]}`),
	}
	for name, body := range cases {
		decls := toolDeclsIn(body)
		var found *ToolDeclaration
		for i := range decls {
			if decls[i].Name == "ToolSearch" {
				found = &decls[i]
			}
		}
		if found == nil {
			t.Errorf("%s: ToolSearch not declared", name)
			continue
		}
		if !found.StringArgument("query") {
			t.Errorf("%s: query not readable as a string argument (schema %v)", name, found.Schema)
		}
	}
}

// A tool named in the conversation history is not a declaration — the same
// rule toolNamesIn follows, and the reason it walks only the tools sections.
func TestHistoryIsNotADeclaration(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"Ghost"}}]}],"tools":[{"function":{"name":"Real"}}]}`)
	for _, d := range toolDeclsIn(body) {
		if d.Name == "Ghost" {
			t.Error("a tool call in history was read as a declaration")
		}
	}
}

// Required arguments come first, so the fallback picks one the tool will
// accept rather than an optional it may ignore.
func TestRequiredArgumentsAreOrderedFirst(t *testing.T) {
	d := ToolDeclaration{Schema: map[string]any{
		"properties": map[string]any{"alpha": map[string]any{"type": "string"}, "zeta": map[string]any{"type": "string"}},
		"required":   []any{"zeta"},
	}}
	if got := d.ArgumentNames(); len(got) != 2 || got[0] != "zeta" {
		t.Errorf("want zeta first, got %v", got)
	}
}
