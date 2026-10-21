# The `nodes` output is the provider-independent contract (ADR 0003): kubeanvil turns it into
# the Ansible inventory, so Ansible never needs to know which provider built the machines.

output "nodes" {
  description = "Map of hostname => { ip, role, zone }."
  value = {
    for name, n in module.node : name => {
      ip   = n.ip
      role = n.role
      zone = n.zone
    }
  }
}
