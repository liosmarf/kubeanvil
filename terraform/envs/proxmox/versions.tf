terraform {
  required_version = ">= 1.6.0" # Terraform >= 1.6 or OpenTofu >= 1.6

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.116.0" # patch updates only; Renovate proposes minor bumps
    }
  }

  # Local state; `kubeanvil apply` points it at clusters/<name>/terraform.tfstate at init
  # time, so every cluster keeps its own state next to its other files.
  backend "local" {}
}
