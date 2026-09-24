package runner

import (
	"testing"
	"time"
)

func TestParseProbesResumeAfter(t *testing.T) {
	p, err := ParseProbes("resume=kill,resumeafter=65s")
	if err != nil || !p.ResumeKill || p.ResumeAfter != 65*time.Second {
		t.Fatalf("got %+v, %v", p, err)
	}
	for _, bad := range []string{"resumeafter=soon", "resumeafter=-1s"} {
		if _, err := ParseProbes(bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
