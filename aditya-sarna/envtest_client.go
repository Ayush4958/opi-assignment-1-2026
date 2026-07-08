//go:build integration || e2e

package nvidiadpf

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EnvtestDPFClient implements DPFClient against a real Kubernetes API server
// (envtest or Kind). SSA apply uses FieldManager for drift correction (§9.1).
type EnvtestDPFClient struct {
	Client client.Client
}

func gvkToSchema(gvk GroupVersionKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind,
	}
}

func normalizeSpec(spec map[string]any) map[string]interface{} {
	if spec == nil {
		return nil
	}
	b, err := json.Marshal(spec)
	if err != nil {
		out := make(map[string]interface{}, len(spec))
		for k, v := range spec {
			out[k] = v
		}
		return out
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		out = make(map[string]interface{}, len(spec))
		for k, v := range spec {
			out[k] = v
		}
	}
	return out
}

func (c *EnvtestDPFClient) toUnstructured(obj DPFObject) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkToSchema(obj.GVK))
	u.SetNamespace(obj.Namespace)
	u.SetName(obj.Name)
	u.SetLabels(obj.Labels)
	u.Object["spec"] = normalizeSpec(obj.Spec)
	if len(obj.Conditions) > 0 {
		conds := make([]interface{}, 0, len(obj.Conditions))
		for _, cond := range obj.Conditions {
			status := "False"
			if cond.Status {
				status = "True"
			}
			conds = append(conds, map[string]interface{}{
				"type":    cond.Type,
				"status":  status,
				"reason":  cond.Reason,
				"message": cond.Message,
			})
		}
		_ = unstructured.SetNestedSlice(u.Object, conds, "status", "conditions")
	}
	return u
}

func (c *EnvtestDPFClient) fromUnstructured(u *unstructured.Unstructured) DPFObject {
	obj := DPFObject{
		GVK: GroupVersionKind{
			Group:   u.GroupVersionKind().Group,
			Version: u.GroupVersionKind().Version,
			Kind:    u.GroupVersionKind().Kind,
		},
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Labels:    u.GetLabels(),
	}
	if spec, ok, _ := unstructured.NestedMap(u.Object, "spec"); ok {
		obj.Spec = spec
	}
	rawConds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, rc := range rawConds {
		m, _ := rc.(map[string]interface{})
		if m == nil {
			continue
		}
		obj.Conditions = append(obj.Conditions, Condition{
			Type:    fmt.Sprint(m["type"]),
			Status:  fmt.Sprint(m["status"]) == "True",
			Reason:  fmt.Sprint(m["reason"]),
			Message: fmt.Sprint(m["message"]),
		})
	}
	return obj
}

func (c *EnvtestDPFClient) Get(ctx context.Context, gvk GroupVersionKind, namespace, name string) (DPFObject, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkToSchema(gvk))
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, u); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return DPFObject{}, fmt.Errorf("%w: %s/%s %s", ErrNotFound, namespace, name, gvk.Kind)
		}
		return DPFObject{}, err
	}
	return c.fromUnstructured(u), nil
}

func (c *EnvtestDPFClient) List(ctx context.Context, gvk GroupVersionKind, namespace string, selector map[string]string) ([]DPFObject, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkToSchema(gvk))
	list.SetKind(gvk.Kind + "List")
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if len(selector) > 0 {
		opts = append(opts, client.MatchingLabels(selector))
	}
	if err := c.Client.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	out := make([]DPFObject, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, c.fromUnstructured(&list.Items[i]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *EnvtestDPFClient) Apply(ctx context.Context, obj DPFObject) error {
	u := c.toUnstructured(obj)
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(u.GroupVersionKind())
	err := c.Client.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: u.GetName()}, existing)
	if client.IgnoreNotFound(err) != nil {
		return err
	}
	if err != nil {
		return c.Client.Create(ctx, u)
	}
	u.SetResourceVersion(existing.GetResourceVersion())
	return c.Client.Patch(ctx, u, client.Apply, client.ForceOwnership, client.FieldOwner(FieldManager))
}

func (c *EnvtestDPFClient) Delete(ctx context.Context, gvk GroupVersionKind, namespace, name string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkToSchema(gvk))
	u.SetNamespace(namespace)
	u.SetName(name)
	if err := c.Client.Delete(ctx, u); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return err
	}
	return nil
}

// SetReady marks a derived object Ready via the status subresource (simulates DPF convergence).
func (c *EnvtestDPFClient) SetReady(ctx context.Context, gvk GroupVersionKind, namespace, name string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkToSchema(gvk))
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, u); err != nil {
		return err
	}
	now := metav1.Now().Format("2006-01-02T15:04:05Z")
	_ = unstructured.SetNestedSlice(u.Object, []interface{}{
		map[string]interface{}{
			"type":               "Ready",
			"status":             "True",
			"reason":             "ByTest",
			"message":            "simulated DPF ready",
			"lastTransitionTime": now,
		},
	}, "status", "conditions")
	return c.Client.Status().Update(ctx, u)
}

// EnsureNamespace creates a namespace if absent.
func EnsureNamespace(ctx context.Context, cl client.Client, name string) error {
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	ns.SetName(name)
	if err := cl.Create(ctx, ns); err != nil {
		if client.IgnoreAlreadyExists(err) == nil {
			return nil
		}
		return err
	}
	return nil
}
