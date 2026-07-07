package node

import "testing"

func TestGenerateUniqueNodeID_EnvOverride(t *testing.T) {
	t.Setenv("PERSYS_NODE_ID", "node-from-env")
	id, err := GenerateUniqueNodeID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "node-from-env" {
		t.Fatalf("expected env node id, got %s", id)
	}
}
