package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/edgenode"

// Edge nodes keep request-critical workers but never run maintenance jobs
// against the shared production database. Normal deployments are unchanged.
func startProvidedService(s interface{ Start() }) {
	if edgenode.Enabled() {
		switch s.(type) {
		case *TimingWheelService, *DeferredService, *OpsSystemLogSink, *AuditLogService, *OpsIngressRejectAggregator:
		default:
			return
		}
	}
	s.Start()
}
