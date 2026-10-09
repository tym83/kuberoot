// Package v1alpha1 is the vm.kuberoot.dev API: virtual machines of a
// hypervisor cluster. The cluster places each machine on a node, keeps its
// disk replicated on others, and starts it on another node when its node
// fails.
// +kubebuilder:object:generate=true
// +groupName=vm.kuberoot.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion of the virtual machines API.
var GroupVersion = schema.GroupVersion{Group: "vm.kuberoot.dev", Version: "v1alpha1"}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &VirtualMachine{}, &VirtualMachineList{})
		metav1.AddToGroupVersion(s, GroupVersion)
		return nil
	})
	AddToScheme = SchemeBuilder.AddToScheme
)

// VirtualMachine is a machine of the cluster, wherever it runs.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=vm
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Running,type=boolean,JSONPath=`.spec.running`
// +kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name=Node,type=string,JSONPath=`.status.node`
// +kubebuilder:printcolumn:name=Address,type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name=Replicas,type=string,JSONPath=`.status.replicaNodes`
type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSpec   `json:"spec"`
	Status VirtualMachineStatus `json:"status,omitempty"`
}

type VirtualMachineSpec struct {
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	CPUs int32 `json:"cpus,omitempty"`
	// +kubebuilder:default="512Mi"
	Memory resource.Quantity `json:"memory,omitempty"`
	Disk   Disk              `json:"disk"`
	// Running: the machine runs; false shuts it down and keeps its disk.
	// +kubebuilder:default=true
	Running *bool `json:"running,omitempty"`
	// Replicas of the disk, on as many nodes; the machine can run on any
	// of them, and moves to another when its node fails.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	Replicas int32 `json:"replicas,omitempty"`
	// Node to run on, among the replicas; any when unset.
	// +optional
	Node string `json:"node,omitempty"`
}

type Disk struct {
	// +kubebuilder:default="4Gi"
	Size resource.Quantity `json:"size,omitempty"`
	// Image written onto the disk when it is created: a URL of a qcow2 or
	// raw disk image that boots with UEFI.
	Image string `json:"image"`
}

type VirtualMachineStatus struct {
	// Phase: Pending, Starting, Running, Stopped, Moving or Failed.
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
	// Node the machine runs on, and the nodes holding its disk.
	Node         string   `json:"node,omitempty"`
	ReplicaNodes []string `json:"replicaNodes,omitempty"`
	// MAC of its network interface, and the address it took.
	MAC     string `json:"mac,omitempty"`
	Address string `json:"address,omitempty"`
	// Minor and Port of its replicated disk.
	Minor int32 `json:"minor,omitempty"`
	Port  int32 `json:"port,omitempty"`
	// Moves counts the times the machine was started on another node.
	Moves int32 `json:"moves,omitempty"`
}

// +kubebuilder:object:root=true
type VirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachine `json:"items"`
}
