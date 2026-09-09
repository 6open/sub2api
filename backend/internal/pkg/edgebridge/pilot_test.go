package edgebridge

import "testing"

func TestPilotIdentityAllowlist(t *testing.T) {
	for _, id := range []int64{1, 157} {
		if !PilotUserAllowed(id) {
			t.Errorf("expected allowed user %d", id)
		}
	}
	for _, id := range []int64{-1, 0, 2, 156, 158} {
		if PilotUserAllowed(id) {
			t.Errorf("unexpected allowed user %d", id)
		}
	}
}
