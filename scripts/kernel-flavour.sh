# Sourced by the kernel and module builds: the kernel a distribution boots.
# A distribution with distros/<name>/kernel.config has a flavour of its own,
# named after it, built in a directory of its own; the others share the
# base kernel.
KFLAVOUR=""
if [ -n "${KUBEROOT_DISTRO:-}" ] && [ -f "$ROOT/distros/$KUBEROOT_DISTRO/kernel.config" ]; then
  KFLAVOUR=$KUBEROOT_DISTRO
fi

# kernel_build_dir <work> <kernel arch>: where the flavour's objects live.
kernel_build_dir() { echo "$1/build-$2${KFLAVOUR:+-$KFLAVOUR}"; }
