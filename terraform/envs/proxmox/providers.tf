provider "proxmox" {
  endpoint  = var.proxmox.endpoint
  api_token = var.proxmox_api_token
  insecure  = var.proxmox.insecure_tls

  # Cloud-init snippets are uploaded over SSH; the API cannot write them.
  ssh {
    username    = var.proxmox.ssh_user
    agent       = var.proxmox_ssh_private_key == "" # fall back to the local SSH agent
    private_key = var.proxmox_ssh_private_key == "" ? null : var.proxmox_ssh_private_key
  }
}
