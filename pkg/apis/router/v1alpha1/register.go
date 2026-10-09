package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion of the router API.
var GroupVersion = schema.GroupVersion{Group: "router.kuberoot.dev", Version: "v1alpha1"}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&Interface{}, &InterfaceList{},
		&Route{}, &RouteList{},
		&NATRule{}, &NATRuleList{},
		&FirewallZone{}, &FirewallZoneList{},
		&FirewallRule{}, &FirewallRuleList{},
		&DHCPServer{}, &DHCPServerList{},
		&BGPRouter{}, &BGPRouterList{},
		&BGPPeer{}, &BGPPeerList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
