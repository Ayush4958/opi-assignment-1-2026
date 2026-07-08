package nvidiadpf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBF3LaneSpec_Complete validates the documented BF-3 hardware lane (§13.1).
func TestBF3LaneSpec_Complete(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "hardware", "bf3-lane.yaml"))
	if err != nil {
		t.Fatalf("read bf3 lane spec: %v", err)
	}
	var doc struct {
		Spec struct {
			Hardware struct {
				Platform  string `yaml:"platform"`
				PCIVendor string `yaml:"pciVendor"`
			} `yaml:"hardware"`
			TopologyModes []string `yaml:"topologyModes"`
			Phases        []struct {
				ID          string `yaml:"id"`
				Description string `yaml:"description"`
			} `yaml:"phases"`
			CI struct {
				Workflow    string `yaml:"workflow"`
				HardwareJob string `yaml:"hardwareJob"`
			} `yaml:"ci"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Spec.Hardware.Platform != "BlueField-3" {
		t.Fatalf("platform = %q", doc.Spec.Hardware.Platform)
	}
	if doc.Spec.Hardware.PCIVendor != "15b3" {
		t.Fatalf("pciVendor = %q", doc.Spec.Hardware.PCIVendor)
	}
	wantModes := map[string]bool{"DPFNative": false, "OPIConverged": false}
	for _, m := range doc.Spec.TopologyModes {
		wantModes[m] = true
	}
	for m, ok := range wantModes {
		if !ok {
			t.Fatalf("missing topology mode %q", m)
		}
	}
	wantPhases := []string{"provision", "vf-actuation", "sfc-declarative", "traffic", "teardown"}
	if len(doc.Spec.Phases) != len(wantPhases) {
		t.Fatalf("phases = %d, want %d", len(doc.Spec.Phases), len(wantPhases))
	}
	for i, id := range wantPhases {
		if doc.Spec.Phases[i].ID != id {
			t.Fatalf("phase[%d] = %q, want %q", i, doc.Spec.Phases[i].ID, id)
		}
		if strings.TrimSpace(doc.Spec.Phases[i].Description) == "" {
			t.Fatalf("phase %q missing description", id)
		}
	}
	if doc.Spec.CI.Workflow == "" || doc.Spec.CI.HardwareJob == "" {
		t.Fatalf("CI workflow metadata incomplete")
	}
}
