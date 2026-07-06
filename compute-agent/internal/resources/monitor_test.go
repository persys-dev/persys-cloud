package resources

import "testing"

func TestDefaultThresholds(t *testing.T) {
	th := DefaultThresholds()
	if th.MemoryThreshold <= 0 || th.CPUThreshold <= 0 || th.DiskThreshold <= 0 {
		t.Fatal("expected positive default thresholds")
	}
}
