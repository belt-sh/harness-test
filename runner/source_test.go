package runner

import (
	"os"
	"testing"
)

// readSource reads a file from this package, for guards that assert how a
// result is recorded rather than what it says.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
