# The module declares which provider it needs, not how to connect: the caller passes a
# configured provider, so the same module works against any Proxmox endpoint.
terraform {
  required_version = ">= 1.6.0"

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = ">= 0.116.0, < 1.0.0"
    }
  }
}
