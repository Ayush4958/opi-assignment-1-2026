//go:build bf3

// BF-3 hardware e2e — runs only when BF3_LAB=1 against a real BlueField-3 lab cluster.
// See testdata/hardware/bf3-lane.yaml and scripts/e2e-bf3-hardware.sh.
package nvidiadpf

import (
	"os"
	"testing"
)

func TestBF3LabGate(t *testing.T) {
	if os.Getenv("BF3_LAB") != "1" {
		t.Skip("BF3_LAB not set; hardware lane is contract-gated in unit tests")
	}
	if os.Getenv("KUBECONFIG") == "" {
		t.Fatal("KUBECONFIG required for BF-3 hardware lane")
	}
	t.Log("BF-3 lab gate open: run lane phases via scripts/e2e-bf3-hardware.sh orchestration")
}

func TestBF3HardwareSFCGoldenOnLab(t *testing.T) {
	if os.Getenv("BF3_LAB") != "1" {
		t.Skip("BF3_LAB not set")
	}
	// Reuses Kind/e2e golden apply against lab cluster API — proves same contract on real apiserver.
	TestE2EKindSFCGoldenApply(t)
}
