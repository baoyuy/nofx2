package kernel

import "testing"

func TestUnstructuredResponseIsFailure(t *testing.T) {
	if _, err := extractDecisions("Market is uncertain, wait for confirmation."); err == nil {
		t.Fatal("unstructured AI reply counted as success")
	}
	if decisions, err := extractDecisions(`[{"symbol":"ALL","action":"wait","reasoning":"uncertain"}]`); err != nil || len(decisions) != 1 {
		t.Fatalf("valid wait rejected: %v", err)
	}
}
