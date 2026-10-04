package v1alpha1

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
	// +listType=atomic
	Nameservers []string `json:"nameservers,omitempty"`
	// Sysctls are kernel parameters applied on top of the distro defaults.
	Sysctls map[string]string `json:"sysctls,omitempty"`
}

type OSConfigStatus struct {
	Distro        string `json:"distro,omitempty"`
	Version       string `json:"version,omitempty"`
	KernelVersion string `json:"kernelVersion,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	// +listType=atomic
	Addresses   []string    `json:"addresses,omitempty"`
	BootTime    metav1.Time `json:"bootTime,omitempty"`
	MemoryTotal int64       `json:"memoryTotal,omitempty"`
	MemoryFree  int64       `json:"memoryFree,omitempty"`
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

// Disk is a block device of the node.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
type Disk struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status DiskStatus `json:"status,omitempty"`
}

type DiskStatus struct {
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	Model     string `json:"model,omitempty"`
	Removable bool   `json:"removable,omitempty"`
	// Role is what kuberoot uses the disk for: BootMedia, System or empty when free.
	Role string `json:"role,omitempty"`
	// +listType=atomic
	Partitions []DiskPartition `json:"partitions,omitempty"`
}

type DiskPartition struct {
	Name      string `json:"name"`
	Label     string `json:"label,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DiskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Disk `json:"items"`
}

// Installation writes kuberoot from the boot media onto a disk of this node.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced
type Installation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstallationSpec   `json:"spec,omitempty"`
	Status InstallationStatus `json:"status,omitempty"`
}

type InstallationSpec struct {
	// Disk is the name of the target disk; everything on it is erased.
	Disk string `json:"disk"`
	// Reboot into the installed system when done.
	Reboot bool `json:"reboot,omitempty"`
}

type InstallationStatus struct {
	Phase       string       `json:"phase,omitempty"`
	Message     string       `json:"message,omitempty"`
	Progress    int32        `json:"progress"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type InstallationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Installation `json:"items"`
}
