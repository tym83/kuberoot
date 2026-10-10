package aictl

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLabelGPUNodes(t *testing.T) {
	kube := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "g"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "was", Labels: map[string]string{GPULabel: "true"}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cpu"}},
	)
	c := &Controller{Kube: kube}
	c.labelGPUNodes(context.Background(), []Node{{Name: "g", GPUs: 2}, {Name: "was"}, {Name: "cpu"}})
	for name, want := range map[string]bool{"g": true, "was": false, "cpu": false} {
		n, _ := kube.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
		if _, has := n.Labels[GPULabel]; has != want {
			t.Errorf("%s labelled %v, want %v", name, has, want)
		}
	}
}
