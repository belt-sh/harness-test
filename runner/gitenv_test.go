package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inference-sh/agentprotocol/harness"
)

// The test repo must be built in its own directory even when the caller's
// environment names another repository, as a git hook's does.
func TestTestRepoIgnoresCallerGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	outer := t.TempDir()
	// This test runs under the pre-push hook too, whose GIT_DIR would turn
	// its own git init into a reinit of the repository being pushed.
	initCmd := exec.Command("git", "init", "-q", outer)
	initCmd.Env = withoutGitEnv(os.Environ())
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Setenv("GIT_DIR", filepath.Join(outer, ".git"))

	r := &TestRunner{harness: harness.Harness{NeedsGitRepo: true}, home: t.TempDir()}
	repo := r.workDir()

	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("test repo has no .git of its own: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(outer, ".git", "config"))
	if strings.Contains(string(cfg), "t@t") || strings.Contains(string(cfg), "bare = true") {
		t.Errorf("the caller's repository config was changed:\n%s", cfg)
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "HEAD")
	cmd.Dir = outer
	cmd.Env = withoutGitEnv(os.Environ())
	if out, err := cmd.Output(); err == nil {
		t.Errorf("the caller's repository gained a commit: %s", out)
	}
}
