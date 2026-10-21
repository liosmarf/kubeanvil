# Non-secret inputs are rendered by `kubeanvil generate` into terraform.tfvars.json from
# cluster.yaml. Secrets come from the environment (TF_VAR_*), never from files in Git.

variable "cluster_name" {
  description = "Cluster name (metadata.name in cluster.yaml)."
  type        = string
}

variable "proxmox" {
  description = "Proxmox connection and placement settings."
  type = object({
    endpoint           = string
    insecure_tls       = optional(bool, false)
    ssh_user           = optional(string, "root")
    node               = string
    disk_datastore     = string
    image_datastore    = optional(string, "local")
    snippets_datastore = optional(string, "local")
    bridge             = optional(string, "vmbr0")
  })
}

variable "image" {
  description = "Ubuntu cloud image. Use releases/ URLs; daily builds are pruned after ~3 months."
  type = object({
    url    = string
    sha256 = string
  })

  validation {
    condition     = can(regex("^[0-9a-f]{64}$", var.image.sha256))
    error_message = "image.sha256 must be a 64-character hex SHA-256 checksum."
  }
}

variable "network" {
  description = "Node network settings."
  type = object({
    gateway       = string
    dns_servers   = list(string)
    search_domain = string
  })
}

variable "access" {
  description = "Admin access injected through cloud-init."
  type = object({
    admin_user      = string
    ssh_public_keys = list(string)
    timezone        = optional(string, "UTC")
  })
}

variable "nodes" {
  description = "One entry per node, keyed by hostname (rendered by kubeanvil from spec.nodes)."
  type = map(object({
    role          = string
    vm_id         = number
    ip_cidr       = string
    cpu           = number
    memory_mib    = number
    os_disk_gib   = number
    data_disk_gib = optional(number, 0)
  }))

  validation {
    condition     = length([for n in values(var.nodes) : n if n.role == "server"]) % 2 == 1
    error_message = "Use an odd number of control planes (1 or 3) so etcd keeps quorum."
  }
}

# --- Secrets (environment only) ---------------------------------------------------------------

variable "proxmox_api_token" {
  description = "API token, format user@realm!tokenid=secret. Set TF_VAR_proxmox_api_token."
  type        = string
  sensitive   = true
}

variable "proxmox_ssh_private_key" {
  description = "Private key for snippet uploads. Empty = use the local ssh-agent. Set TF_VAR_proxmox_ssh_private_key."
  type        = string
  sensitive   = true
  default     = ""
}
