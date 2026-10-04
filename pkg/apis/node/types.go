package node

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// OSConfig is the operating system of one node: its desired settings and what
// the node reports about itself. There is exactly one, named after the node.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
type OSConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OSConfigSpec   `json:"spec,omitempty"`
	Status OSConfigStatus `json:"status,omitempty"`
}

type OSConfigSpec struct {
	// Nameservers replace the DNS servers handed out by DHCP.
	Nameservers []string `json:"nameservers,omitempty"`
	// Sysctls are kernel parameters applied on top of the distro defaults.
	Sysctls map[string]string `json:"sysctls,omitempty"`
}

type OSConfigStatus struct {
	Distro        string      `json:"distro,omitempty"`
	Version       string      `json:"version,omitempty"`
	KernelVersion string      `json:"kernelVersion,omitempty"`
	Architecture  string      `json:"architecture,omitempty"`
	Hostname      string      `json:"hostname,omitempty"`
	Addresses     []string    `json:"addresses,omitempty"`
	BootTime      metav1.Time `json:"bootTime,omitempty"`
	MemoryTotal   int64       `json:"memoryTotal,omitempty"`
	MemoryFree    int64       `json:"memoryFree,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OSConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []OSConfig `json:"items"`
}

// NodeService is one of the processes the node init supervises.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
type NodeService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeServiceSpec   `json:"spec,omitempty"`
	Status NodeServiceStatus `json:"status,omitempty"`
}

type NodeServiceSpec struct {
	// RestartedAt restarts the service whenever it changes, like kubectl rollout restart.
	RestartedAt *metav1.Time `json:"restartedAt,omitempty"`
}

type NodeServiceStatus struct {
	State     string      `json:"state,omitempty"`
	PID       int32       `json:"pid,omitempty"`
	Restarts  int32       `json:"restarts"`
	StartedAt metav1.Time `json:"startedAt,omitempty"`
	LastExit  string      `json:"lastExit,omitempty"`
	// MemoryBytes and CPUUsageSeconds come from the service cgroup.
	MemoryBytes     int64 `json:"memoryBytes,omitempty"`
	CPUUsageSeconds int64 `json:"cpuUsageSeconds,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NodeService `json:"items"`
}

// Kubeconfig is a credential for the Kubernetes cluster this node serves or belongs to.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
type Kubeconfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Server is the API server URL written into the kubeconfig.
	Server string `json:"server,omitempty"`
	// Kubeconfig is the complete file, ready to save.
	Kubeconfig string `json:"kubeconfig,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type KubeconfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Kubeconfig `json:"items"`
}


