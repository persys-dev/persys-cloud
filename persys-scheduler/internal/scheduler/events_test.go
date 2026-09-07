package scheduler

import "testing"

func TestKnownSchedulerEventTypesIncludesClusterTroubleshootingSignals(t *testing.T) {
	expected := []string{
		"NodeJoined",
		"NodeLost",
		"NodeLeft",
		"WorkloadScheduled",
		"WorkloadFailed",
		"DriftDetected",
		"RetryTriggered",
		"Rescheduled",
		"Relocated",
	}

	for _, eventType := range expected {
		if !isKnownSchedulerEventType(eventType) {
			t.Fatalf("event type %q should be recognized as a scheduler event", eventType)
		}
	}

	if isKnownSchedulerEventType("RandomNoise") {
		t.Fatal("unknown event types should not be accepted into the scheduler catalog")
	}
}
