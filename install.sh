#!/usr/bin/env bash
# kubeanvil installer (ADR 0005).
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/liosmarf/kubeanvil/<tag>/install.sh)"
#
# On a Proxmox VE host it offers to create a least-privilege API user and token for
# Terraform and a small Debian container (the bastion) with kubeanvil, OpenTofu, Ansible,
# kubectl, helm and SOPS, then tells you how to start the wizard inside it. Anywhere else it
# installs the kubeanvil binary and a checkout of the repository. It asks before every change
# to the host, and verifies every download against a SHA-256 checksum.
set -euo pipefail

KUBEANVIL_VERSION=${KUBEANVIL_VERSION:-v1.0.0}
REPO=liosmarf/kubeanvil

# Tools for the bastion, pinned with their published checksums (linux/amd64).
TOFU_VERSION=1.13.2
TOFU_SHA256=ac05d9952662c7db28dd57e93f0a68018837bfdf9342384ec0987977c16a9645
HELM_VERSION=v4.3.1
HELM_SHA256=0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
KUBECTL_VERSION=v1.36.6
KUBECTL_SHA256=29818b6729dc9262b692307470d0558d027725e738be5dd47c760261868a2634
SOPS_VERSION=v3.13.3
SOPS_SHA256=e5bec3346a873ae91d871550f3e698c1aad962aff462a080e40f25fde17fef6b

bold=$'\e[1m' dim=$'\e[2m' reset=$'\e[0m'
say() { printf '%s\n' "$*"; }
step() { printf '\n%s━━ %s%s\n' "$bold" "$*" "$reset"; }
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }

# ask "Question" default(y|n): reads from the terminal, also when the script comes from a pipe.
ask() {
  local reply hint="[y/N]"
  [[ $2 == y ]] && hint="[Y/n]"
  read -r -p "$1 $hint " reply </dev/tty || reply=""
  reply=${reply:-$2}
  [[ $reply == [yY]* ]]
}

# Downloads go to one temporary directory, removed however the script ends.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# fetch URL DEST SHA256: download and refuse anything that does not match the checksum.
fetch() {
  curl -fsSL --retry 3 -o "$2" "$1"
  local sum
  sum=$( { sha256sum "$2" 2>/dev/null || shasum -a 256 "$2"; } | cut -d' ' -f1) # shasum on older macOS
  [[ $sum == "$3" ]] || die "checksum mismatch for $1"
}

# release_sha256 ASSET: the checksum GoReleaser published for an asset of this release.
release_sha256() {
  curl -fsSL "https://github.com/$REPO/releases/download/$KUBEANVIL_VERSION/checksums.txt" |
    awk -v f="$1" '$2 == f {print $1}' || true
}

# --- anywhere but Proxmox -------------------------------------------------------------------

install_cli() {
  local os arch asset sum bin
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) die "unsupported CPU: $(uname -m)" ;;
  esac
  asset="kubeanvil_${KUBEANVIL_VERSION#v}_${os}_${arch}.tar.gz"
  sum=$(release_sha256 "$asset")
  [[ -n $sum ]] || die "no $asset in release $KUBEANVIL_VERSION"
  fetch "https://github.com/$REPO/releases/download/$KUBEANVIL_VERSION/$asset" "$work/$asset" "$sum"
  tar -xzf "$work/$asset" -C "$work" kubeanvil

  bin=$HOME/.local/bin
  [[ $EUID -eq 0 ]] && bin=/usr/local/bin
  mkdir -p "$bin"
  install -m 0755 "$work/kubeanvil" "$bin/kubeanvil"
  say "Installed $bin/kubeanvil $KUBEANVIL_VERSION."

  if command -v git >/dev/null && [[ ! -e kubeanvil ]]; then
    git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$KUBEANVIL_VERSION" "https://github.com/$REPO.git" kubeanvil
    say "Checked out the repository in ./kubeanvil (Terraform and Ansible run from it)."
  fi
  say "
Next:
  cd kubeanvil
  kubeanvil init     # a few questions → cluster.yaml
  kubeanvil apply    # needs OpenTofu (or Terraform) and Ansible"
}

# --- on Proxmox -------------------------------------------------------------------------------

# The bridge that carries the default route, and the host's address on it.
host_network() {
  bridge=$(ip -4 route show default | awk '{for (i = 1; i < NF; i++) if ($i == "dev") print $(i + 1); exit}')
  host_ip=$(ip -4 -o addr show dev "$bridge" | awk '{split($4, a, "/"); print a[1]; exit}')
  [[ -n $bridge && -n $host_ip ]] || die "could not find the bridge with the default route"
}

# The first active storage that accepts a content type, local before shared.
storage_for() {
  pvesh get "/nodes/$node/storage" --content "$1" --enabled 1 --output-format json |
    python3 -c 'import json, sys
s = sorted(json.load(sys.stdin), key=lambda x: (x.get("shared", 0), -x.get("avail", 0)))
print(s[0]["storage"] if s else "")'
}

api_token() {
  step "Proxmox API token for Terraform"
  say "kubeanvil needs an API token to create VMs. This creates the role Kubeanvil (VM, disk and
network privileges only), the user terraform@pve and a token for it."
  ask "Create them now?" y || { say "Skipped: kubeanvil apply will ask for a token."; return; }

  local privs=(Datastore.Allocate Datastore.AllocateSpace Datastore.AllocateTemplate Datastore.Audit
    Sys.Audit Sys.Console Sys.Modify SDN.Use VM.Allocate VM.Audit VM.Clone VM.Config.CDROM
    VM.Config.Cloudinit VM.Config.CPU VM.Config.Disk VM.Config.HWType VM.Config.Memory
    VM.Config.Network VM.Config.Options VM.Console VM.PowerMgmt VM.GuestAgent.Audit)
  if pveum role list --output-format json | grep -q '"roleid":"Kubeanvil"'; then
    pveum role modify Kubeanvil -privs "${privs[*]}"
  else
    pveum role add Kubeanvil -privs "${privs[*]}"
  fi
  pveum user list --output-format json | grep -q '"userid":"terraform@pve"' ||
    pveum user add terraform@pve --comment "kubeanvil / OpenTofu"
  pveum aclmod / -user terraform@pve -role Kubeanvil

  local name
  name=kubeanvil-$(date +%Y%m%d%H%M)
  token="terraform@pve!$name=$(pveum user token add terraform@pve "$name" --privsep 0 --output-format json |
    python3 -c 'import json, sys; print(json.load(sys.stdin)["value"])')"
  say "Created token terraform@pve!$name. It is stored only inside the bastion."
}

bastion() {
  step "Bastion container"
  local ctid template_store rootfs_store template
  ctid=$(pvesh get /cluster/nextid)
  template_store=$(storage_for vztmpl)
  rootfs_store=$(storage_for rootdir)
  [[ -n $template_store && -n $rootfs_store ]] || die "no storage for container templates or root disks"
  say "A Debian container, ID $ctid, 2 vCPU, 2 GiB RAM, 8 GiB on $rootfs_store, DHCP on $bridge.
It holds the tools, your cluster.yaml, Terraform state and the kubeconfig. Its SSH key is
added to root's authorized_keys on this host so the wizard and Terraform can reach it."
  ask "Create it?" y || { say "Nothing created."; exit 0; }

  pveam update >/dev/null
  template=$(pveam available --section system | awk '$2 ~ /^debian-13-standard_.*_amd64/ {print $2}' | sort -V | tail -1)
  [[ -n $template ]] || die "no Debian 13 template offered by pveam"
  pveam list "$template_store" | grep -q "$template" || pveam download "$template_store" "$template"

  pct create "$ctid" "$template_store:vztmpl/$template" \
    --hostname kubeanvil --cores 2 --memory 2048 --swap 512 \
    --rootfs "$rootfs_store:8" --net0 "name=eth0,bridge=$bridge,ip=dhcp" \
    --unprivileged 1 --features nesting=1 --onboot 1 --start 1 \
    --description "kubeanvil bastion: OpenTofu, Ansible, kubectl, helm, SOPS and kubeanvil."
  say "Waiting for the network…"
  for _ in $(seq 30); do pct exec "$ctid" -- getent hosts github.com >/dev/null 2>&1 && break; sleep 2; done

  step "Tools"
  fetch "https://github.com/opentofu/opentofu/releases/download/v$TOFU_VERSION/tofu_${TOFU_VERSION}_linux_amd64.tar.gz" "$work/tofu.tgz" "$TOFU_SHA256"
  fetch "https://get.helm.sh/helm-$HELM_VERSION-linux-amd64.tar.gz" "$work/helm.tgz" "$HELM_SHA256"
  fetch "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl" "$work/kubectl" "$KUBECTL_SHA256"
  fetch "https://github.com/getsops/sops/releases/download/$SOPS_VERSION/sops-$SOPS_VERSION.linux.amd64" "$work/sops" "$SOPS_SHA256"
  local asset="kubeanvil_${KUBEANVIL_VERSION#v}_linux_amd64.tar.gz" sum
  sum=$(release_sha256 "$asset")
  [[ -n $sum ]] || die "no $asset in release $KUBEANVIL_VERSION"
  fetch "https://github.com/$REPO/releases/download/$KUBEANVIL_VERSION/$asset" "$work/kubeanvil.tgz" "$sum"
  tar -xzf "$work/tofu.tgz" -C "$work" tofu
  tar -xzf "$work/helm.tgz" -C "$work" --strip-components 1 linux-amd64/helm
  tar -xzf "$work/kubeanvil.tgz" -C "$work" kubeanvil
  for tool in tofu helm kubectl sops kubeanvil; do
    pct push "$ctid" "$work/$tool" "/usr/local/bin/$tool" --perms 0755
  done

  pct exec "$ctid" -- bash -euo pipefail -c "
    export DEBIAN_FRONTEND=noninteractive
    apt-get -qq update
    apt-get -qq install -y --no-install-recommends ansible-core age git openssh-client ca-certificates curl python3 >/dev/null
    git -c advice.detachedHead=false clone --quiet --depth 1 --branch '$KUBEANVIL_VERSION' 'https://github.com/$REPO.git' /root/kubeanvil
    install -d -m 0700 /root/.ssh
    ssh-keygen -q -t ed25519 -N '' -C 'kubeanvil bastion' -f /root/.ssh/id_ed25519
    install -d -m 0700 /root/.config/kubeanvil
    echo 'export KUBEANVIL_PROXMOX_HOST=$host_ip' > /root/.config/kubeanvil/env
    echo '[ -f ~/.config/kubeanvil/env ] && . ~/.config/kubeanvil/env' >> /root/.bashrc"
  if [[ -n ${token:-} ]]; then
    printf "export PROXMOX_VE_API_TOKEN='%s'\n" "$token" |
      pct exec "$ctid" -- bash -c 'umask 077; cat >> /root/.config/kubeanvil/env'
  fi

  pct exec "$ctid" -- cat /root/.ssh/id_ed25519.pub >>/root/.ssh/authorized_keys
  say "Bastion $ctid is ready."
  say "
Next, on this host:
  pct enter $ctid
  cd kubeanvil && kubeanvil init     ${dim}# Proxmox host: $host_ip${reset}
  kubeanvil apply"
}

main() {
  say "${bold}kubeanvil $KUBEANVIL_VERSION${reset} — Kubernetes on your hypervisor from one cluster.yaml"
  if ! command -v pveversion >/dev/null; then
    install_cli
    return
  fi
  [[ $EUID -eq 0 ]] || die "run as root on the Proxmox host"
  [[ $(uname -m) == x86_64 ]] || die "the bastion is built for amd64 hosts"
  node=$(hostname)
  host_network
  say "Proxmox VE $(pveversion | cut -d/ -f2) on $node ($host_ip, $bridge)."
  api_token
  bastion
}

main "$@"
