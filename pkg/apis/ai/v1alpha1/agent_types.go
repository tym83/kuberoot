package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Actions a Remedy may take: all there is for the operator agent to do.
const (
	// RestartService restarts a service of a node.
	ActionRestartService = "RestartService"
	// RestartModelServer restarts a model's server on a node.
	ActionRestartModelServer = "RestartModelServer"
	// RollbackModel sets a model's source back to the version before.
	ActionRollbackModel = "RollbackModel"
	// RebootNode reboots a node.
	ActionRebootNode = "RebootNode"
	// Escalate changes nothing: it tells the people what the agent found
	// and could not fix with the actions it has.
	ActionEscalate = "Escalate"
)

// Remedy is one action the operator agent proposes: what to do, to what,
// and why. The agent can only propose; the action runs once a person
// approves it (spec.approved), or at once when the Agent's policy lets this
// action run unattended. Remedies are the record of everything the agent
// did and wanted to do.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Action,type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name=Node,type=string,JSONPath=`.spec.node`
// +kubebuilder:printcolumn:name=Target,type=string,JSONPath=`.spec.target`
// +kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name=Reason,type=string,JSONPath=`.spec.reason`
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`
type Remedy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemedySpec   `json:"spec"`
	Status RemedyStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf || (oldSelf.approved == false && self.approved == true && self.action == oldSelf.action && self.node == oldSelf.node && self.target == oldSelf.target)",message="a remedy changes only by being approved"
type RemedySpec struct {
	// +kubebuilder:validation:Enum=RestartService;RestartModelServer;RollbackModel;RebootNode;Escalate
	Action string `json:"action"`
	// Node the action is on, for the actions on a node.
	// +optional
	Node string `json:"node,omitempty"`
	// Target: the service of RestartService, the model of
	// RestartModelServer and RollbackModel.
	// +optional
	Target string `json:"target,omitempty"`
	// Reason is the agent's account of the problem and of the choice.
	// +kubebuilder:validation:MaxLength=2000
	Reason string `json:"reason"`
	// Evidence the agent saw: the findings it was shown.
	// +optional
	Evidence []string `json:"evidence,omitempty"`
	// Approved runs the action. Only a person sets it: the agent may not.
	// +kubebuilder:default=false
	Approved bool `json:"approved"`
}

type RemedyStatus struct {
	// Phase: Proposed, Running, Done, Failed, Refused or Expired.
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
	// ApprovedBy: "person", or "policy" when the Agent's policy ran it.
	ApprovedBy string       `json:"approvedBy,omitempty"`
	DoneAt     *metav1.Time `json:"doneAt,omitempty"`
}

// +kubebuilder:object:root=true
type RemedyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Remedy `json:"items"`
}

// Agent is the operator agent of the cluster: the model it thinks with and
// the policy its remedies run under. There is one, named default; without
// it the agent watches nothing.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Model,type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name=Findings,type=integer,JSONPath=`.status.findings`
// +kubebuilder:printcolumn:name=Checked,type=date,JSONPath=`.status.lastCheck`
// +kubebuilder:printcolumn:name=Message,type=string,JSONPath=`.status.message`
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec"`
	Status AgentStatus `json:"status,omitempty"`
}

type AgentSpec struct {
	// Model the agent asks, by name, at Endpoint.
	Model string `json:"model"`
	// Endpoint of an OpenAI-compatible API; the cluster's own when unset.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// APIKeySecret names a Secret in kube-system, named kuberoot-agent,
	// whose key api-key holds the endpoint's key; none is sent when false.
	// +optional
	APIKeySecret bool `json:"apiKeySecret,omitempty"`
	// AutoApprove: actions that run without a person approving them.
	// +optional
	// +kubebuilder:validation:items:Enum=RestartService;RestartModelServer;RollbackModel;RebootNode
	AutoApprove []string `json:"autoApprove,omitempty"`
	// MaxPerHour bounds the actions run in any hour, approved or not.
	// +kubebuilder:default=4
	// +kubebuilder:validation:Minimum=0
	MaxPerHour int32 `json:"maxPerHour,omitempty"`
	// Paused: the agent watches and proposes nothing.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

type AgentStatus struct {
	LastCheck *metav1.Time `json:"lastCheck,omitempty"`
	// Findings: problems seen on the last check.
	Findings int32  `json:"findings,omitempty"`
	Message  string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}
