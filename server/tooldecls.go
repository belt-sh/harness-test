package server

import (
	"encoding/json"
	"sort"
)

// ToolDeclaration is one tool as the agent declared it to the model.
type ToolDeclaration struct {
	Name   string
	Schema map[string]any // the JSON Schema for its arguments, if it published one
}

// ToolDeclarations is every tool declared across the requests recorded since
// the last ClearLog, keyed by name.
//
// DeclaredTools answers "what could the model reach"; this answers "and what
// does it take", which is what a probe needs before it can call one of them.
func (s *MockServer) ToolDeclarations() map[string]ToolDeclaration {
	s.mu.Lock()
	entries := make([]LogEntry, len(s.log))
	copy(entries, s.log)
	s.mu.Unlock()

	out := map[string]ToolDeclaration{}
	for _, e := range entries {
		for _, d := range toolDeclsIn(e.Body) {
			// A later declaration wins only if it carries a schema the
			// earlier one lacked: agents re-send the same tool every turn,
			// and a deferred tool's first appearance is the stub.
			if prev, ok := out[d.Name]; ok && len(d.Schema) == 0 && len(prev.Schema) > 0 {
				continue
			}
			out[d.Name] = d
		}
	}
	return out
}

// ArgumentNames lists the tool's declared argument names, required ones first.
func (d ToolDeclaration) ArgumentNames() []string {
	props, _ := d.Schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	required := map[string]bool{}
	if req, ok := d.Schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	var names []string
	for n := range props {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	return names
}

// StringArgument reports whether the named argument is declared a string,
// which is all a probe can safely fill in without inventing a shape.
func (d ToolDeclaration) StringArgument(name string) bool {
	props, _ := d.Schema["properties"].(map[string]any)
	p, _ := props[name].(map[string]any)
	t, _ := p["type"].(string)
	return t == "string"
}

// toolDeclsIn collects every tool declared in a request, with its argument
// schema. It walks the same "tools"/"functionDeclarations" sections
// toolNamesIn does, for the same reason: a tool name in conversation history
// is not a declaration.
func toolDeclsIn(body []byte) []ToolDeclaration {
	var doc any
	if json.Unmarshal(body, &doc) != nil {
		return nil
	}
	var decls []ToolDeclaration
	var collect func(any)
	collect = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			// A tool is declared either flat ({"name":...}) or wrapped in the
			// OpenAI function envelope ({"function":{"name":...}}).
			if fn, ok := t["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok && n != "" {
					decls = append(decls, ToolDeclaration{Name: n, Schema: schemaIn(fn)})
				}
			}
			if n, ok := t["name"].(string); ok && n != "" {
				decls = append(decls, ToolDeclaration{Name: n, Schema: schemaIn(t)})
			}
			for _, sub := range t {
				collect(sub)
			}
		case []any:
			for _, sub := range t {
				collect(sub)
			}
		}
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, sub := range t {
				if k == "tools" || k == "functionDeclarations" {
					collect(sub)
					continue
				}
				walk(sub)
			}
		case []any:
			for _, sub := range t {
				walk(sub)
			}
		}
	}
	walk(doc)
	return decls
}

// schemaIn finds the argument schema on a tool declaration. The four wire
// formats spell it differently and one of them nests it.
func schemaIn(decl map[string]any) map[string]any {
	for _, key := range []string{"parameters", "input_schema", "inputSchema", "parametersJsonSchema"} {
		if s, ok := decl[key].(map[string]any); ok {
			if inner, ok := s["json"].(map[string]any); ok {
				return inner
			}
			return s
		}
	}
	return nil
}
