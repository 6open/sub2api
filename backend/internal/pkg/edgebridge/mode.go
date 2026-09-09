package edgebridge

import "os"

func ControlOnly() bool { return os.Getenv("LKLB_EDGE_CONTROL_ONLY") == "1" }
