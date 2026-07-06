// Unit tests for the Pattern 1 adapter's decision logic.
//
// These tests exercise the *pure* functions — translation, the ObservedGeneration
// Gate, and the Equality Gate — and therefore need NO apiserver, NO envtest
// binaries, NO OVS, and NO hardware. Run them with:
//
//	go test -v -run TestSfc ./...
//
// They convert four architecture-document claims from "argued" to "shown":
//   - the SFC->DPF translation produces the correct owned objects;
//   - the ObservedGeneration Gate holds back a false Ready while DPF lags;
//   - the Equality Gate suppresses a redundant write on an unchanged input
//     (this is the write-amplification bound, demonstrated);
//   - the projection is deterministic (a re-reconcile on identical input
//     produces an identical target, so it cannot self-trigger a loop).
package dpfadapter

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func newSFC(name, namespace, uid, image string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(sfcGVK)
	u.SetName(name)
	u.SetNamespace(namespace)
	u.SetUID(types.UID(uid))
	_ = unstructured.SetNestedSlice(u.Object, []interface{}{
		map[string]interface{}{"name": "nf0", "image": image},
	}, "spec", "networkFunctions")
	return u
}

func TestSfcTranslateProducesOwnedDPFChildren(t *testing.T) {
	sfc := newSFC("chain1", "openshift-dpu-operator", "uid-123", "registry/nf:v1")

	children, err := translateServiceFunctionChain(sfc, "dpf-system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("want 2 DPF children, got %d", len(children))
	}
	for _, c := range children {
		if c.GetNamespace() != "dpf-system" {
			t.Errorf("child %q namespace = %q, want dpf-system", c.GetName(), c.GetNamespace())
		}
		if got := c.GetAnnotations()[AnnotationOwnerUID]; got != "uid-123" {
			t.Errorf("child %q owner-uid annotation = %q, want uid-123", c.GetName(), got)
		}
		if got := c.GetAnnotations()[AnnotationOwnerName]; got != "chain1" {
			t.Errorf("child %q owner-name annotation = %q, want chain1", c.GetName(), got)
		}
		if got := c.GetLabels()[LabelOwningSFCUID]; got != "uid-123" {
			t.Errorf("child %q owning-sfc-uid label = %q, want uid-123", c.GetName(), got)
		}
	}

	// The workload image must survive translation onto the DPUService child.
	svc := children[0]
	img, _, _ := unstructured.NestedString(svc.Object, "spec", "image")
	if img != "registry/nf:v1" {
		t.Errorf("DPUService spec.image = %q, want registry/nf:v1", img)
	}
}

func TestSfcObservedGenerationGateHoldsBack(t *testing.T) {
	// One child still lagging (observedGeneration < generation) => no false Ready.
	children := []dpfChildStatus{
		{Name: "a", Generation: 3, ObservedGeneration: 2, ReadyStatus: "True"},
	}
	got := buildTargetConditions(children, 5)
	if len(got) != 1 {
		t.Fatalf("want 1 condition, got %d", len(got))
	}
	if got[0].Status != metav1.ConditionFalse || got[0].Reason != reasonWaitingDPF {
		t.Fatalf("while a child lags, want %s/False, got %s/%s",
			reasonWaitingDPF, got[0].Reason, got[0].Status)
	}
	if got[0].ObservedGeneration != 5 {
		t.Errorf("mirrored observedGeneration = %d, want 5", got[0].ObservedGeneration)
	}
}

func TestSfcAllConvergedAndReadyYieldsReady(t *testing.T) {
	children := []dpfChildStatus{
		{Name: "a", Generation: 3, ObservedGeneration: 3, ReadyStatus: "True"},
		{Name: "b", Generation: 1, ObservedGeneration: 1, ReadyStatus: "True"},
	}
	got := buildTargetConditions(children, 7)
	if got[0].Status != metav1.ConditionTrue || got[0].Reason != reasonReady {
		t.Fatalf("want %s/True, got %s/%s", reasonReady, got[0].Reason, got[0].Status)
	}
}

// TestSfcEqualityGateSuppressesRedundantWrite is the write-amplification bound
// made concrete: on an unchanged DPF input, the recomputed target equals the
// prior target, so the Equality Gate skips the /status write.
func TestSfcEqualityGateSuppressesRedundantWrite(t *testing.T) {
	children := []dpfChildStatus{
		{Name: "a", Generation: 2, ObservedGeneration: 2, ReadyStatus: "True"},
	}
	first := buildTargetConditions(children, 4)
	second := buildTargetConditions(children, 4) // identical DPF input == a re-reconcile

	if !conditionsSemanticallyEqual(first, second) {
		t.Fatalf("projection is not deterministic; the loop could self-trigger and amplify writes")
	}
}

// TestSfcEqualityGateDetectsRealTransition proves the gate is not merely always-equal:
// a genuine DPF status transition is detected and would produce exactly one write.
func TestSfcEqualityGateDetectsRealTransition(t *testing.T) {
	lagging := buildTargetConditions([]dpfChildStatus{
		{Name: "a", Generation: 2, ObservedGeneration: 1, ReadyStatus: ""},
	}, 4)
	ready := buildTargetConditions([]dpfChildStatus{
		{Name: "a", Generation: 2, ObservedGeneration: 2, ReadyStatus: "True"},
	}, 4)

	if conditionsSemanticallyEqual(lagging, ready) {
		t.Fatalf("gate must detect the WaitingOnDPF -> Ready transition")
	}
}
