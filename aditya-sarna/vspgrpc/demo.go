package vspgrpc

import (
	"context"
	"fmt"
	"time"

	nvidia "github.com/adityasarna/opi-nvidia-vsp-skeleton"
)

// DemoNode is the default node name for local daemon demos.
const DemoNode = "node-1"

// NewDemoVSP wires an in-memory DPF client with demo-friendly timeouts.
func NewDemoVSP(node string, dpf nvidia.DPFClient, vfCount int) *nvidia.NvidiaVSP {
	cfg := nvidia.DefaultVSPConfig(node)
	cfg.NFReadyTimeout = 3 * time.Second
	cfg.VFActuationTimeout = 2 * time.Second
	cfg.PollInterval = 10 * time.Millisecond
	return nvidia.NewNvidiaVSP(cfg, dpf, demoVFAlloc{}, demoVFs{count: vfCount})
}

type demoVFAlloc struct{}

func (demoVFAlloc) AllocatedVFs() int32 { return 0 }

type demoVFs struct{ count int }

func (d demoVFs) ListVFs() ([]string, error) {
	out := make([]string, d.count)
	for i := range out {
		out[i] = fmt.Sprintf("0000:03:00.%d", i)
	}
	return out, nil
}

// SeedDemoCluster marks a DPU Ready for Init/GetDevices demos.
func SeedDemoCluster(ctx context.Context, dpf nvidia.DPFClient, node string) error {
	return dpf.Apply(ctx, nvidia.DPFObject{
		GVK:        nvidia.GVKDPU,
		Namespace:  nvidia.ManagedNamespace,
		Name:       "dpu-" + node,
		Spec:       map[string]any{"nodeName": node},
		Conditions: []nvidia.Condition{{Type: "Ready", Status: true}},
	})
}

// SeedNFReady marks derived chain Ready so CreateNetworkFunction succeeds quickly.
func SeedNFReady(ctx context.Context, dpf nvidia.DPFClient, node string, req nvidia.NFRequest) error {
	plan := nvidia.TranslateNFRequest(node, req)
	for _, obj := range plan.Objects {
		obj.Conditions = []nvidia.Condition{{Type: "Ready", Status: true}}
		if err := dpf.Apply(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}
