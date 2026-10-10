// Package agent is the operator agent of an ai cluster: it looks for
// problems, asks a language model what to do about each, and proposes the
// answer as a Remedy, one of a few actions a person or a policy approves.
// It changes nothing itself.
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Finding is a problem the agent saw, with what it could act on.
type Finding struct {
	// Subject names what is wrong: node/<node>, service/<node>/<service>,
	// model/<model> or modelserver/<node>/<model>.
	Subject string
	Node    string
	// Target: the service or the model.
	Target string
	// Text tells the problem, log lines included, for the model to read.
	Text string
}

var (
	modelsGVR   = schema.GroupVersionResource{Group: "ai.kuberoot.dev", Version: "v1alpha1", Resource: "models"}
	servicesGVR = schema.GroupVersionResource{Group: "node.kuberoot.dev", Version: "v1alpha1", Resource: "nodeservices"}
	serversGVR  = schema.GroupVersionResource{Group: "node.kuberoot.dev", Version: "v1alpha1", Resource: "modelservers"}
)

// LogLines of a failing service or server shown to the model.
const LogLines = 15

// Collector gathers findings through the cluster's API and the node APIs
// aggregated into it.
type Collector struct {
	Dynamic dynamic.Interface
	Kube    kubernetes.Interface
	// Raw reads the logs of node services and model servers.
	Raw rest.Interface
}

// Collect lists the cluster's problems: nodes not ready, services of nodes
// restarting, models short of ready replicas and the servers that failed.
func (c *Collector) Collect(ctx context.Context) ([]Finding, error) {
	var out []Finding
	nodes, err := c.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	ready := map[string]bool{}
	for _, n := range nodes.Items {
		for _, cond := range n.Status.Conditions {
			if cond.Type != corev1.NodeReady {
				continue
			}
			ready[n.Name] = cond.Status == corev1.ConditionTrue
			if !ready[n.Name] {
				out = append(out, Finding{Subject: "node/" + n.Name, Node: n.Name,
					Text: fmt.Sprintf("Node %s is not ready since %s (%s: %s). Its node API may not answer.",
						n.Name, cond.LastTransitionTime.UTC().Format(time.RFC3339), cond.Reason, cond.Message)})
			}
		}
	}
	if services, err := c.Dynamic.Resource(servicesGVR).List(ctx, metav1.ListOptions{}); err == nil {
		for _, u := range services.Items {
			node, svc, ok := strings.Cut(u.GetName(), ".")
			if !ok || !ready[node] {
				continue
			}
			state, _, _ := unstructured.NestedString(u.Object, "status", "state")
			if state != "Restarting" {
				continue
			}
			restarts, _, _ := unstructured.NestedInt64(u.Object, "status", "restarts")
			exit, _, _ := unstructured.NestedString(u.Object, "status", "lastExit")
			text := fmt.Sprintf("Service %s on node %s keeps restarting: %d restarts, last exit: %s.", svc, node, restarts, exit)
			out = append(out, Finding{Subject: "service/" + node + "/" + svc, Node: node, Target: svc,
				Text: text + c.logTail(ctx, "nodeservices", u.GetName())})
		}
	}
	models, err := c.Dynamic.Resource(modelsGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out, nil
	}
	servers := map[string]*unstructured.Unstructured{}
	if list, err := c.Dynamic.Resource(serversGVR).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range list.Items {
			servers[list.Items[i].GetName()] = &list.Items[i]
		}
	}
	for _, m := range models.Items {
		phase, _, _ := unstructured.NestedString(m.Object, "status", "phase")
		msg, _, _ := unstructured.NestedString(m.Object, "status", "message")
		readyCount, _, _ := unstructured.NestedString(m.Object, "status", "ready")
		replicas, _, _ := unstructured.NestedSlice(m.Object, "status", "replicas")
		var failed []string
		for _, r := range replicas {
			rm, _ := r.(map[string]any)
			node, _ := rm["node"].(string)
			if p, _ := rm["phase"].(string); p == "Failed" && ready[node] {
				failed = append(failed, node)
				name := node + "." + m.GetName()
				text := fmt.Sprintf("The server of model %s on node %s failed.", m.GetName(), node)
				if s := servers[name]; s != nil {
					if smsg, _, _ := unstructured.NestedString(s.Object, "status", "message"); smsg != "" {
						text += " It reports: " + smsg + "."
					}
				}
				out = append(out, Finding{Subject: "modelserver/" + node + "/" + m.GetName(), Node: node, Target: m.GetName(),
					Text: text + c.logTail(ctx, "modelservers", name)})
			}
		}
		if phase == "Degraded" || phase == "RolledBack" || phase == "Pending" {
			text := fmt.Sprintf("Model %s is %s with %s replicas ready.", m.GetName(), phase, readyCount)
			if msg != "" {
				text += " " + msg + "."
			}
			if prev, ok, _ := unstructured.NestedString(m.Object, "status", "previous", "sha256"); ok && prev != "" {
				text += " A version before the current one exists, so it can be rolled back."
			}
			out = append(out, Finding{Subject: "model/" + m.GetName(), Target: m.GetName(), Text: text})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out, nil
}

// logTail is the last lines a service or server wrote, as part of a finding.
func (c *Collector) logTail(ctx context.Context, resource, name string) string {
	if c.Raw == nil {
		return ""
	}
	raw, err := c.Raw.Get().AbsPath("/apis/node.kuberoot.dev/v1alpha1", resource, name, "log").Do(ctx).Raw()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > LogLines {
		lines = lines[len(lines)-LogLines:]
	}
	return "\nLast log lines:\n" + strings.Join(lines, "\n")
}
