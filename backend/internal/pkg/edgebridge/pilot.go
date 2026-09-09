package edgebridge

// PilotUserAllowed is an identity allowlist, never a billing exemption.
func PilotUserAllowed(id int64) bool { return id == 1 || id == 157 }
