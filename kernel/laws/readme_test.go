package laws_test

import (
	"os"
	"regexp"
	"testing"
)

// TestKernelReadmeStatesWhatIsNotBuilt guards criterion 8 of the closing
// story: whoever reads the README of the kernel learns that the timer
// scheduler process does not exist yet, and that the privileged road of the
// staff into a space belongs to phase B4. The statements are the ones B3 and
// B4 must not rely on by mistake.
func TestKernelReadmeStatesWhatIsNotBuilt(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatalf("read the README of the kernel: %v", err)
	}
	for name, pattern := range map[string]*regexp.Regexp{
		"the scheduler does not exist": regexp.MustCompile(`(?i)timer scheduler process does not exist`),
		"staff access is phase B4":     regexp.MustCompile(`(?i)privileged road of NexusThrive staff[^.]*phase B4`),
	} {
		if !pattern.Match(raw) {
			t.Errorf("kernel/README.md does not say: %s", name)
		}
	}
}
