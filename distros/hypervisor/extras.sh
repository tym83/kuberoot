# Sourced by build-rootfs.sh: cloud-hypervisor, the virtual machine monitor,
# and its UEFI firmware.
case $ARCH in amd64) chArch="";; arm64) chArch="-aarch64";; esac
fetch "https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/$CLOUD_HYPERVISOR_VERSION/cloud-hypervisor-static$chArch" "cloud-hypervisor-$CLOUD_HYPERVISOR_VERSION$chArch"
install -m 0755 "$CACHE/cloud-hypervisor-$CLOUD_HYPERVISOR_VERSION$chArch" $r/usr/bin/cloud-hypervisor
fetch "https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/$CLOUD_HYPERVISOR_VERSION/ch-remote-static$chArch" "ch-remote-$CLOUD_HYPERVISOR_VERSION$chArch"
install -m 0755 "$CACHE/ch-remote-$CLOUD_HYPERVISOR_VERSION$chArch" $r/usr/bin/ch-remote
# The firmware is CLOUDHV.fd on x86_64 and CLOUDHV_EFI.fd on aarch64.
case $ARCH in amd64) fw=CLOUDHV.fd;; arm64) fw=CLOUDHV_EFI.fd;; esac
fetch "https://github.com/cloud-hypervisor/edk2/releases/download/$CLOUDHV_FIRMWARE_VERSION/$fw" "$fw-$CLOUDHV_FIRMWARE_VERSION"
install -D -m 0644 "$CACHE/$fw-$CLOUDHV_FIRMWARE_VERSION" $r/usr/share/kuberoot/CLOUDHV.fd
