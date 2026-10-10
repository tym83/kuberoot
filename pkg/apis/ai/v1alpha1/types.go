// Package v1alpha1 is the ai.kuberoot.dev API: language models a cluster
// serves. The cluster runs each model on as many nodes as it has replicas,
// moves a new version onto one node first and onto the others only once it
// answers there, and goes back to the version before when it does not.
// +kubebuilder:object:generate=true
// +groupName=ai.kuberoot.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion of the models API.
var GroupVersion = schema.GroupVersion{Group: "ai.kuberoot.dev", Version: "v1alpha1"}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &Model{}, &ModelList{}, &Remedy{}, &RemedyList{}, &Agent{}, &AgentList{})
		metav1.AddToGroupVersion(s, GroupVersion)
		return nil
	})
	AddToScheme = SchemeBuilder.AddToScheme
)

// Model is a language model the cluster serves, under its name, at the
// cluster's OpenAI-compatible endpoint.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Ready,type=string,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name=Serving,type=string,JSONPath=`.status.current.sha256`
// +kubebuilder:printcolumn:name=Engine,type=string,JSONPath=`.status.current.engine.sha256`,priority=1
// +kubebuilder:printcolumn:name=Message,type=string,JSONPath=`.status.message`
type Model struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelSpec   `json:"spec"`
	Status ModelStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.gpus) || self.gpus == 0 || has(self.source.engine)",message="a model on GPUs needs an engine built for CUDA"
type ModelSpec struct {
	// Source is the version to serve: its weights.
	Source Source `json:"source"`
	// Replicas: the nodes serving the model.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`
	// ContextSize in tokens, shared by the Parallel requests a replica
	// serves at once.
	// +kubebuilder:default=4096
	ContextSize int32 `json:"contextSize,omitempty"`
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Parallel int32 `json:"parallel,omitempty"`
	// Threads for generation on each replica; all the node's CPUs when 0.
	// +optional
	Threads int32 `json:"threads,omitempty"`
	// GPUs each replica runs on: NVIDIA GPUs of its node, which then holds
	// all of the model; none runs it on the CPUs. Replicas go only to nodes
	// with as many GPUs free, and the source needs an engine built for CUDA.
	// +optional
	// +kubebuilder:validation:Minimum=0
	GPUs int32 `json:"gpus,omitempty"`
}

// Source is a version of a model: a GGUF file and its SHA-256, and the
// program that serves it.
type Source struct {
	URL string `json:"url"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	SHA256 string `json:"sha256"`
	// Engine serves the weights in place of the llama-server built into the
	// image: one built for CUDA, which a model on GPUs needs, or a newer one.
	// It is fetched like the weights, by the nodes that run it.
	// +optional
	Engine *Engine `json:"engine,omitempty"`
}

// Engine is a model server program: a URL and its SHA-256.
type Engine struct {
	URL string `json:"url"`
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	SHA256 string `json:"sha256"`
}

type ModelStatus struct {
	// Phase: Pending, Deploying, Ready, RollingOut, RolledBack or Degraded.
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
	// Ready replicas, out of the replicas asked for: "2/3".
	Ready string `json:"ready,omitempty"`
	// Current is the version every replica serves; Previous the one before
	// it, which spec.source set back to rolls the model back.
	Current  *Source `json:"current,omitempty"`
	Previous *Source `json:"previous,omitempty"`
	// Canary is the node a new version is tried on first.
	Canary string `json:"canary,omitempty"`
	// RolloutStartedAt: when the new version went to its first node.
	RolloutStartedAt *metav1.Time `json:"rolloutStartedAt,omitempty"`
	// FailedVersion is a version that did not answer on its first node, as
	// its weights' SHA-256 and its engine's after a +; it is not tried again
	// until spec.source changes.
	FailedVersion string `json:"failedVersion,omitempty"`
	// Port the replicas listen on, on their nodes.
	Port     int32     `json:"port,omitempty"`
	Replicas []Replica `json:"replicas,omitempty"`
}

// Replica is the model on one node.
type Replica struct {
	Node    string `json:"node"`
	Address string `json:"address,omitempty"`
	// Version the replica runs, as FailedVersion names it.
	Version string `json:"version,omitempty"`
	// Phase: Downloading, Loading, Ready, Failed or Starting.
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
type ModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Model `json:"items"`
}
