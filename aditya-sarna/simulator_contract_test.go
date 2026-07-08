package nvidiadpf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	goldenYAMLPath    = "testdata/golden/sfc-web-chain.yaml"
	simulatorContract = "testdata/simulator/sfc-web-chain-contract.json"
	bundleManifest    = "config/nvidia/dpf-bundle.yaml"
)

type simulatorContractDoc struct {
	ScenarioID      string `json:"scenarioId"`
	ManagedNamespace string `json:"managedNamespace"`
	Input           struct {
		Namespace        string            `json:"namespace"`
		Name             string            `json:"name"`
		NetworkFunctions []NetworkFunction `json:"networkFunctions"`
		IPAMSubnet       string            `json:"ipamSubnet"`
		NodeSelector     map[string]string `json:"nodeSelector"`
	} `json:"input"`
	ExpectedObjects []struct {
		Kind            string            `json:"kind"`
		Name            string            `json:"name"`
		SpecAssertions  map[string]string `json:"specAssertions"`
	} `json:"expectedObjects"`
	OwnershipLabels []string `json:"ownershipLabels"`
}

func loadSimulatorContract(t *testing.T) simulatorContractDoc {
	t.Helper()
	raw, err := os.ReadFile(simulatorContract)
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var doc simulatorContractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	return doc
}

func parseGoldenDocuments(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(goldenYAMLPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var docs []map[string]any
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode golden doc: %v", err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
	return docs
}

func TestSimulatorContract_MatchesTranslateSFC(t *testing.T) {
	contract := loadSimulatorContract(t)
	spec := ServiceFunctionChainSpec{
		Namespace:        contract.Input.Namespace,
		Name:             contract.Input.Name,
		NetworkFunctions: contract.Input.NetworkFunctions,
		IPAMSubnet:       contract.Input.IPAMSubnet,
		NodeSelector:     contract.Input.NodeSelector,
	}
	plan := TranslateSFC(spec)

	byName := map[string]DPFObject{}
	for _, o := range plan.Objects {
		byName[o.GVK.Kind+"/"+o.Name] = o
	}
	for _, want := range contract.ExpectedObjects {
		got, ok := byName[want.Kind+"/"+want.Name]
		if !ok {
			t.Fatalf("TranslateSFC missing %s/%s", want.Kind, want.Name)
		}
		for k, v := range want.SpecAssertions {
			if fmtSpec(got.Spec[k]) != v {
				t.Fatalf("%s spec[%s] = %v, want %q", want.Name, k, got.Spec[k], v)
			}
		}
	}
}

func TestSimulatorContract_MatchesGoldenYAML(t *testing.T) {
	contract := loadSimulatorContract(t)
	docs := parseGoldenDocuments(t)

	if len(docs) != len(contract.ExpectedObjects) {
		t.Fatalf("golden doc count = %d, contract expects %d", len(docs), len(contract.ExpectedObjects))
	}

	wantByName := map[string]struct {
		kind string
		spec map[string]string
	}{}
	for _, e := range contract.ExpectedObjects {
		wantByName[e.Name] = struct {
			kind string
			spec map[string]string
		}{e.Kind, e.SpecAssertions}
	}

	for _, doc := range docs {
		meta, _ := doc["metadata"].(map[string]any)
		if meta == nil {
			t.Fatal("golden doc missing metadata")
		}
		name := fmtSpec(meta["name"])
		ns := fmtSpec(meta["namespace"])
		if ns != contract.ManagedNamespace {
			t.Fatalf("%s namespace = %q, want %q", name, ns, contract.ManagedNamespace)
		}
		kind := fmtSpec(doc["kind"])
		want, ok := wantByName[name]
		if !ok {
			t.Fatalf("unexpected golden object %s", name)
		}
		if kind != want.kind {
			t.Fatalf("%s kind = %q, want %q", name, kind, want.kind)
		}
		labels, _ := meta["labels"].(map[string]any)
		for _, lk := range contract.OwnershipLabels {
			if labels[lk] == nil || labels[lk] == "" {
				t.Fatalf("%s missing ownership label %s", name, lk)
			}
		}
		spec, _ := doc["spec"].(map[string]any)
		for k, v := range want.spec {
			if fmtSpec(spec[k]) != v {
				t.Fatalf("%s spec[%s] = %v, want %q", name, k, spec[k], v)
			}
		}
	}
}

func TestDPFBundle_NoPlaceholderDigests(t *testing.T) {
	raw, err := os.ReadFile(bundleManifest)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "REPLACE_AT_RELEASE") {
		t.Fatal("dpf-bundle.yaml still contains REPLACE_AT_RELEASE placeholders")
	}
	const shaPrefix = "@sha256:"
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, shaPrefix) {
			continue
		}
		idx := strings.Index(line, shaPrefix)
		digest := strings.TrimSpace(line[idx+len(shaPrefix):])
		if len(digest) != 64 {
			t.Fatalf("invalid digest length in line: %s", strings.TrimSpace(line))
		}
		for _, c := range digest {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("invalid digest char in: %s", digest)
			}
		}
	}
}

func TestDPFBundle_AlignedWithCompatibilityMatrix(t *testing.T) {
	bundleRaw, err := os.ReadFile(bundleManifest)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle struct {
		Spec struct {
			DPF struct {
				Release string `yaml:"release"`
			} `yaml:"dpf"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(bundleRaw, &bundle); err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	if bundle.Spec.DPF.Release != "v25.7.0" {
		t.Fatalf("bundle release = %q, expected pinned v25.7.0", bundle.Spec.DPF.Release)
	}
	// Contract + bundle paths must exist for CI gate wiring (§13.1).
	for _, p := range []string{goldenYAMLPath, simulatorContract, bundleManifest} {
		if _, err := os.Stat(filepath.Clean(p)); err != nil {
			t.Fatalf("missing proof artifact %s: %v", p, err)
		}
	}
}

func fmtSpec(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
