// Package intents turns typed intents into Kubernetes primitives. People
// declare an App; the translator owns the Deployment and Service it lowers to,
// and puts them back whenever they drift. An Override is the audited way out:
// for a limited time one person may change the primitives of one namespace by
// hand, as themselves, while the App's translation is paused.
package intents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsapply "k8s.io/client-go/applyconfigurations/apps/v1"
	coreapply "k8s.io/client-go/applyconfigurations/core/v1"
	metaapply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// FieldManager owns every field the translator writes.
	FieldManager = "kuberoot-intents"
)

var (
	appsGVR      = schema.GroupVersionResource{Group: "intents.kuberoot.dev", Version: "v1alpha1", Resource: "apps"}
	overridesGVR = schema.GroupVersionResource{Group: "intents.kuberoot.dev", Version: "v1alpha1", Resource: "overrides"}
)

type Translator struct {
	Dynamic dynamic.Interface
	Client  kubernetes.Interface
	Now     func() time.Time
}

// Run reconciles everything every interval: intents are level-triggered, so a
// full pass over the current state is the whole algorithm.
func (t *Translator) Run(ctx context.Context, interval time.Duration) {
	for ctx.Err() == nil {
		if err := t.Reconcile(ctx); err != nil {
			klog.Errorf("reconcile: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
	}
}

func (t *Translator) Reconcile(ctx context.Context) error {
	paused, err := t.reconcileOverrides(ctx)
	if err != nil {
		return err
	}
	apps, err := t.Dynamic.Resource(appsGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range apps.Items {
		app := &apps.Items[i]
		key := app.GetNamespace() + "/" + app.GetName()
		if who, ok := paused[key]; ok {
			t.setAppStatus(ctx, app, "Overridden", "translation paused: "+who+" holds an Override", -1)
			continue
		}
		if err := t.lower(ctx, app); err != nil {
			t.setAppStatus(ctx, app, "Failed", err.Error(), -1)
			continue
		}
	}
	return nil
}

// lower writes the Deployment and Service an App stands for. Server-side apply
// with force takes back any field someone changed by hand.
func (t *Translator) lower(ctx context.Context, app *unstructured.Unstructured) error {
	spec, _, _ := unstructured.NestedMap(app.Object, "spec")
	image, _ := spec["image"].(string)
	replicas, _, _ := unstructured.NestedInt64(app.Object, "spec", "replicas")
	port, hasPort, _ := unstructured.NestedInt64(app.Object, "spec", "port")
	expose, _, _ := unstructured.NestedBool(app.Object, "spec", "expose")
	memory, _, _ := unstructured.NestedString(app.Object, "spec", "memory")
	env, _, _ := unstructured.NestedStringMap(app.Object, "spec", "env")

	ns, name := app.GetNamespace(), app.GetName()
	labels := map[string]string{"app.kubernetes.io/name": name, "app.kubernetes.io/managed-by": FieldManager}
	owner := metaapply.OwnerReference().WithAPIVersion("intents.kuberoot.dev/v1alpha1").WithKind("App").
		WithName(name).WithUID(app.GetUID()).WithController(true).WithBlockOwnerDeletion(true)

	container := coreapply.Container().WithName("app").WithImage(image)
	if hasPort {
		container.WithPorts(coreapply.ContainerPort().WithContainerPort(int32(port)).WithName("http"))
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		container.WithEnv(coreapply.EnvVar().WithName(k).WithValue(env[k]))
	}
	if memory != "" {
		q, err := resource.ParseQuantity(memory)
		if err != nil {
			return fmt.Errorf("memory: %w", err)
		}
		container.WithResources(coreapply.ResourceRequirements().WithLimits(corev1.ResourceList{corev1.ResourceMemory: q}))
	}
	deploy := appsapply.Deployment(name, ns).WithLabels(labels).WithOwnerReferences(owner).
		WithSpec(appsapply.DeploymentSpec().WithReplicas(int32(replicas)).
			WithSelector(metaapply.LabelSelector().WithMatchLabels(map[string]string{"app.kubernetes.io/name": name})).
			WithTemplate(coreapply.PodTemplateSpec().WithLabels(labels).WithSpec(coreapply.PodSpec().WithContainers(container))))
	applied, err := t.Client.AppsV1().Deployments(ns).Apply(ctx, deploy, metav1.ApplyOptions{FieldManager: FieldManager, Force: true})
	if err != nil {
		return fmt.Errorf("deployment: %w", err)
	}
	if expose && hasPort {
		svc := coreapply.Service(name, ns).WithLabels(labels).WithOwnerReferences(owner).
			WithSpec(coreapply.ServiceSpec().WithSelector(map[string]string{"app.kubernetes.io/name": name}).
				WithPorts(coreapply.ServicePort().WithName("http").WithPort(int32(port)).WithTargetPort(intstr.FromString("http"))))
		if _, err := t.Client.CoreV1().Services(ns).Apply(ctx, svc, metav1.ApplyOptions{FieldManager: FieldManager, Force: true}); err != nil {
			return fmt.Errorf("service: %w", err)
		}
	}
	t.setAppStatus(ctx, app, state(applied), "", int64(applied.Status.ReadyReplicas))
	return nil
}

func state(d *appsv1.Deployment) string {
	if d.Spec.Replicas != nil && d.Status.ReadyReplicas >= *d.Spec.Replicas {
		return "Ready"
	}
	return "Progressing"
}

func (t *Translator) setAppStatus(ctx context.Context, app *unstructured.Unstructured, st, msg string, ready int64) {
	status := map[string]any{"state": st, "message": msg, "observedGeneration": app.GetGeneration()}
	if ready >= 0 {
		status["readyReplicas"] = ready
	}
	patch := map[string]any{"apiVersion": "intents.kuberoot.dev/v1alpha1", "kind": "App",
		"metadata": map[string]any{"name": app.GetName(), "namespace": app.GetNamespace()}, "status": status}
	u := &unstructured.Unstructured{Object: patch}
	if _, err := t.Dynamic.Resource(appsGVR).Namespace(app.GetNamespace()).ApplyStatus(ctx, app.GetName(), u,
		metav1.ApplyOptions{FieldManager: FieldManager, Force: true}); err != nil {
		klog.V(2).Infof("status of %s/%s: %v", app.GetNamespace(), app.GetName(), err)
	}
}

// SealLabel marks the namespaces whose primitives come from intents only.
const SealLabel = "kuberoot.dev/sealed"

// OverridesConfigMap lists, per sealed namespace, the users of live Overrides;
// the sealing admission policy reads it as its parameter.
const OverridesConfigMap = "kuberoot-overrides"

// reconcileOverrides publishes who holds a live Override in every sealed
// namespace and returns the apps on pause, keyed namespace/name, with who holds them.
func (t *Translator) reconcileOverrides(ctx context.Context) (map[string]string, error) {
	list, err := t.Dynamic.Resource(overridesGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	paused := map[string]string{}
	users := map[string]map[string]bool{} // namespace -> users with a live Override
	now := t.Now()
	for i := range list.Items {
		o := &list.Items[i]
		user, _, _ := unstructured.NestedString(o.Object, "spec", "user")
		app, _, _ := unstructured.NestedString(o.Object, "spec", "app")
		ttl, _, _ := unstructured.NestedString(o.Object, "spec", "ttl")
		d, err := time.ParseDuration(ttl)
		if err != nil {
			continue
		}
		expires := o.GetCreationTimestamp().Add(d)
		st := "Expired"
		if now.Before(expires) {
			st = "Active"
			paused[o.GetNamespace()+"/"+app] = user
			if users[o.GetNamespace()] == nil {
				users[o.GetNamespace()] = map[string]bool{}
			}
			users[o.GetNamespace()][user] = true
		}
		t.setOverrideStatus(ctx, o, st, expires)
	}
	sealed, err := t.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: SealLabel + "=true"})
	if err != nil {
		return nil, err
	}
	for _, ns := range sealed.Items {
		names := make([]string, 0, len(users[ns.Name]))
		for u := range users[ns.Name] {
			names = append(names, u)
		}
		sort.Strings(names)
		cm := coreapply.ConfigMap(OverridesConfigMap, ns.Name).
			WithLabels(map[string]string{"app.kubernetes.io/managed-by": FieldManager}).
			WithData(map[string]string{"users": strings.Join(names, "\n")})
		if _, err := t.Client.CoreV1().ConfigMaps(ns.Name).Apply(ctx, cm, metav1.ApplyOptions{FieldManager: FieldManager, Force: true}); err != nil {
			klog.Errorf("overrides of %s: %v", ns.Name, err)
		}
	}
	return paused, nil
}

func (t *Translator) setOverrideStatus(ctx context.Context, o *unstructured.Unstructured, st string, expires time.Time) {
	cur, _, _ := unstructured.NestedString(o.Object, "status", "state")
	if cur == st {
		return
	}
	patch := []byte(fmt.Sprintf(`{"status":{"state":%q,"expiresAt":%q}}`, st, expires.UTC().Format(time.RFC3339)))
	if _, err := t.Dynamic.Resource(overridesGVR).Namespace(o.GetNamespace()).Patch(ctx, o.GetName(), types.MergePatchType, patch,
		metav1.PatchOptions{FieldManager: FieldManager}, "status"); err != nil {
		klog.V(2).Infof("override status: %v", err)
	}
}
