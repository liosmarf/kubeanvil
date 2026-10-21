# One Kubernetes node on Proxmox VE: cloud-init snippets + a VM whose OS disk is imported from
# an Ubuntu cloud image and, for workers, a raw data disk for Longhorn.

locals {
  fqdn = "${var.name}.${var.search_domain}"

  # Minimal cloud-init: just enough for Ansible to log in. Hardening, packages and Kubernetes
  # are Ansible's job, so the image stays generic.
  user_data = <<-EOT
    #cloud-config
    hostname: ${var.name}
    fqdn: ${local.fqdn}
    manage_etc_hosts: true
    timezone: ${var.timezone}
    users:
      - name: ${var.admin_user}
        groups: [sudo]
        shell: /bin/bash
        sudo: "ALL=(ALL) NOPASSWD:ALL"
        lock_passwd: true
        ssh_authorized_keys:
    %{for k in var.ssh_public_keys~}
          - ${k}
    %{endfor~}
    ssh_pwauth: false
    disable_root: true
    package_update: true
    packages: [qemu-guest-agent]
    runcmd:
      - systemctl enable --now qemu-guest-agent
  EOT

  # Own network-config (netplan v2). Proxmox's generated one renames the NIC to eth0 with
  # "set-name", which races with the initial DHCP on first boot and can leave the static IP
  # unapplied. Matching by driver without renaming avoids the race.
  network_config = <<-EOT
    version: 2
    ethernets:
      primary:
        match:
          driver: virtio_net
        dhcp4: false
        dhcp6: false
        addresses: [${var.ip_cidr}]
        routes:
          - to: default
            via: ${var.gateway}
        nameservers:
          addresses: [${join(", ", var.dns_servers)}]
          search: [${var.search_domain}]
  EOT
}

resource "proxmox_virtual_environment_file" "user_data" {
  content_type = "snippets"
  datastore_id = var.snippets_datastore
  node_name    = var.node_name

  source_raw {
    data      = local.user_data
    file_name = "${var.name}-user-data.yaml"
  }
}

resource "proxmox_virtual_environment_file" "network_config" {
  content_type = "snippets"
  datastore_id = var.snippets_datastore
  node_name    = var.node_name

  source_raw {
    data      = local.network_config
    file_name = "${var.name}-network-config.yaml"
  }
}

resource "proxmox_virtual_environment_vm" "this" {
  name        = var.name
  vm_id       = var.vm_id
  node_name   = var.node_name
  description = "${var.cluster_name} Kubernetes ${var.role}. Managed by Terraform (k8s-platform-iac); do not edit by hand."
  tags        = sort(distinct(concat(["k8s", var.cluster_name, var.role], var.extra_tags)))
  on_boot     = true
  started     = true

  machine       = "q35"
  bios          = "ovmf"
  scsi_hardware = "virtio-scsi-single"

  stop_on_destroy                      = true
  purge_on_destroy                     = true
  delete_unreferenced_disks_on_destroy = true

  cpu {
    type  = "host"
    cores = var.cpu
  }

  memory {
    dedicated = var.memory_mib
  }

  efi_disk {
    datastore_id      = var.disk_datastore
    type              = "4m"
    pre_enrolled_keys = false
  }

  # OS disk, imported from the cloud image and grown to the requested size
  disk {
    datastore_id = var.disk_datastore
    interface    = "scsi0"
    import_from  = var.image_file_id
    size         = var.os_disk_gib
    iothread     = true
    discard      = "on"
    ssd          = true
  }

  # Data disk for Longhorn, only when requested. Ansible finds it by its stable ID
  # (/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_drive-scsi1), never by /dev/sdX.
  dynamic "disk" {
    for_each = var.data_disk_gib > 0 ? [1] : []
    content {
      datastore_id = var.disk_datastore
      interface    = "scsi1"
      size         = var.data_disk_gib
      iothread     = true
      discard      = "on"
      ssd          = true
      file_format  = "raw"
    }
  }

  network_device {
    bridge = var.bridge
    model  = "virtio"
  }

  agent {
    enabled = true
    timeout = "5m"
  }

  operating_system {
    type = "l26"
  }

  # Ubuntu cloud images expect a serial console
  serial_device {
    device = "socket"
  }

  initialization {
    datastore_id = var.disk_datastore
    interface    = "ide2"

    ip_config {
      ipv4 {
        address = var.ip_cidr
        gateway = var.gateway
      }
    }

    dns {
      domain  = var.search_domain
      servers = var.dns_servers
    }

    user_data_file_id    = proxmox_virtual_environment_file.user_data.id
    network_data_file_id = proxmox_virtual_environment_file.network_config.id
  }

  lifecycle {
    ignore_changes = [
      # A newer base image must not recreate existing nodes; it applies to new nodes only.
      disk[0].import_from,
    ]
  }
}
