terraform {
  required_version = ">= 1.6.0" # Terraform >= 1.6 or OpenTofu >= 1.6

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.116.0" # patch updates only; Renovate proposes minor bumps
    }
  }

  # No backend block: state is local by default. `kubeanvil generate` writes a
  # backend_override.tf (S3-compatible, with locking) when spec.backup.s3 is configured.
}
