# ---------------------------------------------------------------------------------------------
# Common node contract (ADR 0003): every provider module accepts these inputs.
# ---------------------------------------------------------------------------------------------

variable "name" {
  description = "VM name and hostname, e.g. lab-cp-01."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$", var.name))
    error_message = "name must be a lowercase DNS label."
  }
}

variable "role" {
  description = "server (control plane + etcd) or agent (worker)."
  type        = string

  validation {
    condition     = contains(["server", "agent"], var.role)
    error_message = "role must be \"server\" or \"agent\"."
  }
}

variable "ip_cidr" {
  description = "Static IPv4 address with prefix length, e.g. 192.168.50.91/24."
  type        = string

  validation {
    condition     = can(cidrhost(var.ip_cidr, 0))
    error_message = "ip_cidr must look like 192.168.50.91/24."
  }
}

variable "cpu" {
  description = "Number of vCPUs."
  type        = number
}

variable "memory_mib" {
  description = "Memory in MiB. No ballooning: Kubernetes needs predictable memory."
  type        = number
}

variable "os_disk_gib" {
  description = "OS disk size in GiB (the cloud image is grown to this size)."
  type        = number
}

variable "data_disk_gib" {
  description = "Extra raw disk for Longhorn in GiB; 0 means no data disk."
  type        = number
  default     = 0
}

variable "admin_user" {
  description = "Admin user created by cloud-init (passwordless sudo, SSH keys only)."
  type        = string
}

variable "ssh_public_keys" {
  description = "Public keys authorised for admin_user."
  type        = list(string)
}

variable "cluster_name" {
  description = "Cluster name, used for tags and descriptions."
  type        = string
}

variable "gateway" {
  description = "Default IPv4 gateway."
  type        = string
}

variable "dns_servers" {
  description = "DNS resolvers."
  type        = list(string)
}

variable "search_domain" {
  description = "DNS search domain; the node FQDN is <name>.<search_domain>."
  type        = string
}

variable "timezone" {
  description = "Time zone configured by cloud-init."
  type        = string
  default     = "UTC"
}

# ---------------------------------------------------------------------------------------------
# Proxmox-specific inputs
# ---------------------------------------------------------------------------------------------

variable "node_name" {
  description = "Proxmox node that hosts the VM. Also reported as the failure zone."
  type        = string
}

variable "vm_id" {
  description = "Proxmox VMID."
  type        = number
}

variable "disk_datastore" {
  description = "Datastore for the OS, data, EFI and cloud-init disks (e.g. local-lvm)."
  type        = string
}

variable "snippets_datastore" {
  description = "Datastore with the \"snippets\" content type, for cloud-init files."
  type        = string
  default     = "local"
}

variable "bridge" {
  description = "Linux bridge for the VM network interface."
  type        = string
  default     = "vmbr0"
}

variable "image_file_id" {
  description = "ID of the Ubuntu cloud image already downloaded to the node (content type import)."
  type        = string
}

variable "extra_tags" {
  description = "Additional Proxmox tags."
  type        = list(string)
  default     = []
}
