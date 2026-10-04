package nodeapi

import (
	"context"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/tym83/kuberoot/pkg/apis/node"
	nodev1 "github.com/tym83/kuberoot/pkg/apis/node/v1alpha1"
	"github.com/tym83/kuberoot/pkg/membership"
	"github.com/tym83/kuberoot/pkg/pki"
	"github.com/tym83/kuberoot/pkg/supervisor"
)

const membershipName = "cluster"

// membershipStorage joins this node to a cluster, or leaves it, by switching
// the node's role in place.
type membershipStorage struct {
	kinit *supervisor.Client
}

var (
	_ rest.Creater         = &membershipStorage{}
	_ rest.Updater         = &membershipStorage{}
	_ rest.GracefulDeleter = &membershipStorage{}
)

func (s *membershipStorage) New() runtime.Object     { return &node.Membership{} }
func (s *membershipStorage) NewList() runtime.Object { return &node.MembershipList{} }
func (s *membershipStorage) Destroy()                {}
func (s *membershipStorage) NamespaceScoped() bool   { return false }
func (s *membershipStorage) GetSingularName() string { return "membership" }

func (s *membershipStorage) current() (*node.Membership, error) {
	spec, err := membership.Load()
	if err != nil || spec == nil {
		return nil, err
	}
	obj := &node.Membership{}
	v1 := &nodev1.Membership{Spec: *spec}
	if err := scheme.Convert(v1, obj, nil); err != nil {
		return nil, err
	}
	obj.Spec.NodeAPIKey = "" // never handed back out
	obj.ObjectMeta = metav1.ObjectMeta{Name: membershipName, UID: types.UID(string(nodeUID()) + "-membership"),
		ResourceVersion: resourceVersion(spec.Server, spec.Role)}
	obj.Status = node.MembershipStatus{Phase: "Member", Message: "member of " + spec.Server + " as " + spec.Role}
	return obj, nil
}

func (s *membershipStorage) Get(_ context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	obj, err := s.current()
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	if obj == nil || name != membershipName {
		return nil, apierrors.NewNotFound(node.Resource("memberships"), name)
	}
	return obj, nil
}

func (s *membershipStorage) List(context.Context, *metainternalversion.ListOptions) (runtime.Object, error) {
	list := &node.MembershipList{}
	if obj, err := s.current(); err == nil && obj != nil {
		list.Items = append(list.Items, *obj)
	}
	return list, nil
}

func (s *membershipStorage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	m := obj.(*node.Membership)
	if m.Name != membershipName {
		return nil, apierrors.NewBadRequest(`the membership must be named "cluster"`)
	}
	if existing, _ := s.current(); existing != nil {
		return nil, apierrors.NewAlreadyExists(node.Resource("memberships"), m.Name)
	}
	return s.apply(ctx, m)
}

func (s *membershipStorage) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo,
	_ rest.ValidateObjectFunc, _ rest.ValidateObjectUpdateFunc, _ bool, _ *metav1.UpdateOptions) (runtime.Object, bool, error) {
	current, _ := s.current()
	var base runtime.Object = &node.Membership{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if current != nil {
		base = current
	}
	updated, err := objInfo.UpdatedObject(ctx, base)
	if err != nil {
		return nil, false, err
	}
	obj, err := s.apply(ctx, updated.(*node.Membership))
	return obj, current == nil, err
}

func (s *membershipStorage) apply(ctx context.Context, m *node.Membership) (runtime.Object, error) {
	if m.Spec.Role != membership.RoleWorker {
		return nil, apierrors.NewBadRequest("spec.role must be Worker")
	}
	if m.Spec.Server == "" || m.Spec.ClusterCA == "" || m.Spec.BootstrapToken == "" || m.Spec.NodeAPICert == "" || m.Spec.NodeAPIKey == "" {
		return nil, apierrors.NewBadRequest("server, clusterCA, bootstrapToken, nodeAPICert and nodeAPIKey are required; apply the membership a JoinTicket produced")
	}
	v1 := &nodev1.Membership{}
	if err := scheme.Convert(m, v1, nil); err != nil {
		return nil, err
	}
	if err := membership.Save(v1.Spec); err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	if err := s.kinit.Reconfigure(ctx); err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	out, _ := s.current()
	out.Status = node.MembershipStatus{Phase: "Joining", Message: "switching to the worker role"}
	return out, nil
}

func (s *membershipStorage) Delete(ctx context.Context, name string, _ rest.ValidateObjectFunc, _ *metav1.DeleteOptions) (runtime.Object, bool, error) {
	current, err := s.Get(ctx, name, nil)
	if err != nil {
		return nil, false, err
	}
	if err := membership.Remove(); err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	if err := s.kinit.Reconfigure(ctx); err != nil {
		return nil, false, apierrors.NewInternalError(err)
	}
	return current, true, nil
}

func (s *membershipStorage) ConvertToTable(_ context.Context, obj runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	var items []runtime.Object
	switch o := obj.(type) {
	case *node.Membership:
		items = []runtime.Object{o}
	case *node.MembershipList:
		for i := range o.Items {
			items = append(items, &o.Items[i])
		}
	}
	return table([]metav1.TableColumnDefinition{col("Name", "name"), col("Role", ""), col("Server", ""), col("Phase", "")},
		items, func(o runtime.Object) []any {
			m := o.(*node.Membership)
			return []any{m.Name, m.Spec.Role, m.Spec.Server, m.Status.Phase}
		}), nil
}

// joinTicketStorage issues join tickets on the control plane node.
type joinTicketStorage struct {
	cluster   kubernetes.Interface
	ca        *pki.Authority
	caPEM     string
	frontPEM  string
	advertise string
	mu        sync.Mutex
	items     map[string]*node.JoinTicket
}

func (s *joinTicketStorage) New() runtime.Object     { return &node.JoinTicket{} }
func (s *joinTicketStorage) NewList() runtime.Object { return &node.JoinTicketList{} }
func (s *joinTicketStorage) Destroy()                {}
func (s *joinTicketStorage) NamespaceScoped() bool   { return false }
func (s *joinTicketStorage) GetSingularName() string { return "jointicket" }

func (s *joinTicketStorage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	t := obj.(*node.JoinTicket).DeepCopy()
	if t.Name == "" {
		return nil, apierrors.NewBadRequest("metadata.name is required")
	}
	ttl := time.Hour
	if t.Spec.TTL != nil {
		ttl = t.Spec.TTL.Duration
	}
	token, err := s.bootstrapToken(ctx, ttl, t.Name)
	if err != nil {
		return nil, apierrors.NewInternalError(fmt.Errorf("bootstrap token: %w", err))
	}
	certPEM, keyPEM, err := s.ca.Issue(pki.Spec{
		CommonName: "kuberoot-node.kube-system.svc",
		DNSNames:   []string{"kuberoot-node.kube-system.svc", "kuberoot-node.kube-system.svc.cluster.local"},
		Server:     true,
	})
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	m := nodev1.Membership{
		TypeMeta:   metav1.TypeMeta{APIVersion: nodev1.SchemeGroupVersion.String(), Kind: "Membership"},
		ObjectMeta: metav1.ObjectMeta{Name: membershipName},
		Spec: nodev1.MembershipSpec{
			Role:           membership.RoleWorker,
			Server:         "https://" + net.JoinHostPort(s.advertise, "6443"),
			ClusterCA:      s.caPEM,
			BootstrapToken: token,
			FrontProxyCA:   s.frontPEM,
			NodeAPICert:    string(certPEM),
			NodeAPIKey:     string(keyPEM),
		},
	}
	manifest, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	expires := metav1.NewTime(time.Now().Add(ttl).Truncate(time.Second))
	t.UID, t.CreationTimestamp = uuid.NewUUID(), metav1.Now()
	t.Status = node.JoinTicketStatus{Expires: &expires, Membership: string(manifest)}
	t.ResourceVersion = resourceVersion(t.Status.Expires)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]*node.JoinTicket{}
	}
	s.items[t.Name] = t
	return t.DeepCopy(), nil
}

// bootstrapToken creates a standard Kubernetes bootstrap token, which the joining
// kubelet uses once to request its own client certificate.
func (s *joinTicketStorage) bootstrapToken(ctx context.Context, ttl time.Duration, ticket string) (string, error) {
	id, secret := randomString(6), randomString(16)
	_, err := s.cluster.CoreV1().Secrets("kube-system").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-token-" + id, Namespace: "kube-system"},
		Type:       corev1.SecretTypeBootstrapToken,
		StringData: map[string]string{
			"description":                    "kuberoot join ticket " + ticket,
			"token-id":                       id,
			"token-secret":                   secret,
			"expiration":                     time.Now().Add(ttl).UTC().Format(time.RFC3339),
			"usage-bootstrap-authentication": "true",
			"auth-extra-groups":              "system:bootstrappers:kuberoot",
		},
	}, metav1.CreateOptions{})
	return id + "." + secret, err
}

func randomString(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	for range n {
		i, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String()
}

func (s *joinTicketStorage) Get(_ context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.items[name]; ok {
		return t.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(node.Resource("jointickets"), name)
}

func (s *joinTicketStorage) List(context.Context, *metainternalversion.ListOptions) (runtime.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := &node.JoinTicketList{}
	for _, t := range s.items {
		list.Items = append(list.Items, *t.DeepCopy())
	}
	return list, nil
}

func (s *joinTicketStorage) ConvertToTable(_ context.Context, obj runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	var items []runtime.Object
	switch o := obj.(type) {
	case *node.JoinTicket:
		items = []runtime.Object{o}
	case *node.JoinTicketList:
		for i := range o.Items {
			items = append(items, &o.Items[i])
		}
	}
	return table([]metav1.TableColumnDefinition{col("Name", "name"), col("Expires", "")},
		items, func(o runtime.Object) []any {
			t := o.(*node.JoinTicket)
			return []any{t.Name, t.Status.Expires.Format(time.RFC3339)}
		}), nil
}

func readPEM(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if b, _ := pem.Decode(raw); b == nil {
		return "", fmt.Errorf("%s is not PEM", path)
	}
	return string(raw), nil
}

func newJoinTickets(cluster kubernetes.Interface, o Options) (*joinTicketStorage, error) {
	ca, err := pki.LoadAuthority(o.ClusterCAFile, o.ClusterCAKeyFile)
	if err != nil {
		return nil, err
	}
	caPEM, err := readPEM(o.ClusterCAFile)
	if err != nil {
		return nil, err
	}
	frontPEM, _ := readPEM(o.RequestHeaderCA)
	return &joinTicketStorage{cluster: cluster, ca: ca, caPEM: caPEM, frontPEM: frontPEM, advertise: o.Advertise}, nil
}
