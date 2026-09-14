package harness

import "testing"

// The probe withholds these to see whether an agent gates its own tool calls,
// so they must not also be in ACPArgs — passing one from both places would
// make the withholding a no-op and report the result as an agent trait.
func TestAutoApproveArgsAreNotAlsoPassedNormally(t *testing.T) {
	for name, h := range All {
		for _, auto := range h.ACPAutoApproveArgs {
			for _, a := range h.ACPArgs {
				if a == auto {
					t.Errorf("%s passes %q in both ACPArgs and ACPAutoApproveArgs", name, auto)
				}
			}
		}
	}
}
