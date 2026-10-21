# Common node contract (ADR 0003): every provider module returns these outputs.

output "name" {
  description = "Hostname."
  value       = proxmox_virtual_environment_vm.this.name
}

output "ip" {
  description = "IPv4 address without prefix, for the Ansible inventory."
  value       = split("/", var.ip_cidr)[0]
}

output "role" {
  description = "server or agent."
  value       = var.role
}

output "zone" {
  description = "Failure domain; becomes the topology.kubernetes.io/zone label."
  value       = var.node_name
}

output "vm_id" {
  description = "Proxmox VMID (provider-specific, informational)."
  value       = proxmox_virtual_environment_vm.this.vm_id
}
