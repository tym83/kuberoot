// Package nodeapi serves the node.kuberoot.dev API: the node's own operating
// system, exposed as Kubernetes resources and reachable with plain kubectl.
package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
)

// resourceVersion fingerprints an object's content, so it changes exactly when the object does.
func resourceVersion(parts ...any) string {
	h := fnv.New64a()
	for _, p := range parts {
		b, _ := json.Marshal(p)
		_, _ = h.Write(b)
	}
	return fmt.Sprintf("%d", h.Sum64()>>1)
}

// pollWatch turns a list function into a watch: it emits MODIFIED, ADDED and
// DELETED events as polled snapshots differ. Node state has no change feed.
func pollWatch(ctx context.Context, list func(context.Context) (map[string]runtime.Object, error)) watch.Interface {
	events := make(chan watch.Event)
	w := watch.NewProxyWatcher(events)
	go func() {
		defer close(events)
		prev, _ := list(ctx)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.StopChan():
				return
			case <-ticker.C:
			}
			cur, err := list(ctx)
			if err != nil {
				continue
			}
			for name, obj := range cur {
				old, ok := prev[name]
				switch {
				case !ok:
					events <- watch.Event{Type: watch.Added, Object: obj}
				case versionOf(old) != versionOf(obj):
					events <- watch.Event{Type: watch.Modified, Object: obj}
				}
			}
			for name, obj := range prev {
				if _, ok := cur[name]; !ok {
					events <- watch.Event{Type: watch.Deleted, Object: obj}
				}
			}
			prev = cur
		}
	}()
	return w
}

func versionOf(obj runtime.Object) string {
	if m, ok := obj.(metav1.Object); ok {
		return m.GetResourceVersion()
	}
	return ""
}

func age(t time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func bytesHuman(b int64) string {
	const mi = 1 << 20
	if b < mi {
		return fmt.Sprintf("%dKi", b>>10)
	}
	return fmt.Sprintf("%dMi", b/mi)
}

// table builds the server-side Table kubectl prints for get.
func table(columns []metav1.TableColumnDefinition, objs []runtime.Object, cells func(runtime.Object) []any) *metav1.Table {
	t := &metav1.Table{ColumnDefinitions: columns}
	for _, o := range objs {
		t.Rows = append(t.Rows, metav1.TableRow{Cells: cells(o), Object: runtime.RawExtension{Object: o}})
	}
	return t
}

func col(name, format string) metav1.TableColumnDefinition {
	return metav1.TableColumnDefinition{Name: name, Type: "string", Format: format}
}
