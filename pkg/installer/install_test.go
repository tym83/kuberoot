package installer

import "testing"

func TestCarryArgsKeepsSettingsAndDropsDevelopmentSwitches(t *testing.T) {
	cmdline := "kuberoot.root=PARTLABEL=KUBEROOT-MEDIA:/kuberoot/rootfs.squashfs kuberoot.mode=install quiet panic=10 " +
		"console=tty0 console=ttyS0 kuberoot.dev kuberoot.verbose kuberoot.repo-plain-http " +
		"kuberoot.nameserver=1.1.1.1 kuberoot.ip=eth1:10.0.0.5/24 kuberoot.pod-cidr=10.50.0.0/16 " +
		"kuberoot.distro=router kuberoot.repo=https://example.org/index.yaml kuberoot.repo-key=QUJD"
	want := "console=tty0 console=ttyS0 kuberoot.nameserver=1.1.1.1 kuberoot.ip=eth1:10.0.0.5/24 " +
		"kuberoot.pod-cidr=10.50.0.0/16 kuberoot.distro=router kuberoot.repo=https://example.org/index.yaml kuberoot.repo-key=QUJD"
	if got := carryArgs(cmdline); got != want {
		t.Errorf("carryArgs =\n  %s\nwant\n  %s", got, want)
	}
}
