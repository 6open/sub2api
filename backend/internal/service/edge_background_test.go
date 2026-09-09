//go:build unit

package service

import "testing"

type maintenanceProbe struct{ starts int }

func (p *maintenanceProbe) Start() { p.starts++ }

func TestEdgeNodeDoesNotStartMaintenance(t *testing.T) {
	p := &maintenanceProbe{}
	t.Setenv("LKLB_EDGE_NODE", "1")
	startProvidedService(p)
	if p.starts != 0 {
		t.Fatal("edge node started maintenance")
	}
	t.Setenv("LKLB_EDGE_NODE", "")
	startProvidedService(p)
	if p.starts != 1 {
		t.Fatal("standard startup changed")
	}
}
