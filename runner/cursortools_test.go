package runner

import "testing"

// Oneof members are the entries typed "message"; the id fields and the
// optional span_context around them are not exec types.
func TestParseExecTypes(t *testing.T) {
	bundle := []byte(`x=["ExecServerMessage|1 id 13|15 exec_id 9|2 shell_args #0 message|14 shell_stream_args #0 message|7 read_args #4 message|19 span_context #38?|55 accept_hook_additional_contexts 8?",a,b]`)
	got, err := parseExecTypes(bundle)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"read_args", "shell_args", "shell_stream_args"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if _, err := parseExecTypes([]byte("no schema")); err == nil {
		t.Error("a bundle without the schema must be an error")
	}
}

// Every tool the snapshot lists has its parameters and a mapping entry, so
// the row never shows a tool without saying how it runs.
func TestCursorToolSnapshotComplete(t *testing.T) {
	s, err := loadCursorToolSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.CLIVersion == "" || s.Date == "" || s.Source == "" {
		t.Errorf("snapshot has no version, date or source: %+v", s)
	}
	for _, mode := range []string{"agent", "plan", "ask"} {
		if len(s.Modes[mode]) == 0 {
			t.Errorf("no tools for mode %s", mode)
		}
		for _, tool := range s.Modes[mode] {
			if _, ok := s.Params[tool]; !ok {
				t.Errorf("%s/%s has no params", mode, tool)
			}
			if e, ok := s.Exec[tool]; !ok || e.Evidence == "" {
				t.Errorf("%s/%s has no exec mapping with evidence", mode, tool)
			}
		}
	}
}
