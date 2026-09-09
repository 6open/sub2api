package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"

func startProvidedService(s interface{ Start() }) {
	if edgebridge.ControlOnly() {
		switch s.(type) {
		case *TimingWheelService, *DeferredService, *OpsSystemLogSink, *OpsIngressRejectAggregator:
		default:
			return
		}
	}
	s.Start()
}
