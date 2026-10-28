# Download the cloud image once per Proxmox node, then create one VM per entry in var.nodes.

resource "proxmox_download_file" "ubuntu" {
  node_name    = var.proxmox.node
  datastore_id = var.proxmox.image_datastore
  content_type = "import"
  url          = var.image.url
  # Ubuntu's .img cloud images are qcow2; Proxmox only accepts disk extensions for "import".
  file_name           = "${var.cluster_name}-${replace(basename(var.image.url), "/\\.img$/", ".qcow2")}"
  checksum            = var.image.sha256
  checksum_algorithm  = "sha256"
  overwrite_unmanaged = true
}

module "node" {
  source   = "../../modules/proxmox-vm"
  for_each = var.nodes

  # Common node contract (ADR 0003)
  name            = each.key
  role            = each.value.role
  ip_cidr         = each.value.ip_cidr
  cpu             = each.value.cpu
  memory_mib      = each.value.memory_mib
  os_disk_gib     = each.value.os_disk_gib
  data_disk_gib   = each.value.data_disk_gib
  admin_user      = var.access.admin_user
  ssh_public_keys = var.access.ssh_public_keys
  timezone        = var.access.timezone
  cluster_name    = var.cluster_name
  gateway         = var.network.gateway
  dns_servers     = var.network.dns_servers
  search_domain   = var.network.search_domain

  # Proxmox-specific
  node_name          = var.proxmox.node
  vm_id              = each.value.vm_id
  disk_datastore     = var.proxmox.disk_datastore
  snippets_datastore = var.proxmox.snippets_datastore
  bridge             = var.proxmox.bridge
  image_file_id      = proxmox_download_file.ubuntu.id
}
