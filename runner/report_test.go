package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inference-sh/agentprotocol/harness"
)

// The regression gate compares check ids across runs, so an id may only be
// built from fixed text and from names that are the same on every run: a file
// name, a hook event, a seed variant. Anything measured (a codename, a session
// id, a count, an error) would make every run a new set of checks. This reads
// every call site and rejects an id argument built from anything else.
func TestCheckIDsAreBuiltFromFixedNames(t *testing.T) {
	allowed := map[string]bool{
		"name": true, "variant": true, "envVar": true, "label": true, "prefix": true,
		"when": true, "correlates": true, "d.Name": true, "f.kind": true,
		"r.harness.APIKeyEnvVar": true,
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	calls := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "r" {
				return true
			}
			switch sel.Sel.Name {
			case "pass", "fail", "skip", "finding":
			default:
				return true
			}
			calls++
			if bad := unstableIDPart(call.Args[0], allowed); bad != "" {
				t.Errorf("%s: check id uses %s, which can differ between runs", fset.Position(call.Pos()), bad)
			}
			return true
		})
	}
	if calls < 100 {
		t.Fatalf("found %d check call sites; the scan has stopped seeing them", calls)
	}
}

func unstableIDPart(e ast.Expr, allowed map[string]bool) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		return ""
	case *ast.BinaryExpr:
		if s := unstableIDPart(v.X, allowed); s != "" {
			return s
		}
		return unstableIDPart(v.Y, allowed)
	case *ast.CallExpr:
		if f, ok := v.Fun.(*ast.Ident); ok && (f.Name == "slug" || f.Name == "probeID" || f.Name == "hookID") {
			for _, a := range v.Args {
				if s := unstableIDPart(a, allowed); s != "" {
					return s
				}
			}
			return ""
		}
	}
	src := exprString(e)
	if allowed[src] {
		return ""
	}
	return src
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	}
	return "an expression"
}

func TestRecordFilesChecksUnderTheSection(t *testing.T) {
	r := &TestRunner{harness: harness.Harness{Name: "test"}}
	r.pass("binary", "claude installed")
	r.section = "acp"
	r.finding("seed.hand-built:not-reached", "seed (hand-built): loaded SEED-1234 and ...")
	r.pass("inflight.reraise:during-the-load", "first")
	r.pass("inflight.reraise:after-the-load-returned", "second")

	got := r.result.Checks
	want := []struct{ id, key string }{
		{"setup/binary", "pass"},
		{"acp/seed.hand-built", "finding:not-reached"},
		{"acp/inflight.reraise", "pass:during-the-load"},
		{"acp/inflight.reraise#2", "pass:after-the-load-returned"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Key() != w.key {
			t.Errorf("check %d: got %s=%s, want %s=%s", i, got[i].ID, got[i].Key(), w.id, w.key)
		}
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"during the load":                        "during-the-load",
		"the same toolCallId as the parked call": "the-same-toolcallid-as-the-parked-call",
		"belt ":                                  "belt",
		"CLAUDE.md":                              "claude.md",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
