# proxmox-vm

One Kubernetes node on Proxmox VE, implementing the common node contract
([ADR 0003](../../../docs/adr/0003-provider-node-contract.md)).

What it creates:

- Two cloud-init snippets: a minimal `user-data` (admin user, SSH keys, qemu-guest-agent) and a
  netplan `network-config` that matches the NIC by driver instead of renaming it.
- A q35/OVMF VM with the OS disk imported from an Ubuntu cloud image and, when
  `data_disk_gib > 0`, a raw data disk for Longhorn at a stable device ID.

Design notes:

- No memory ballooning: the kubelet sizes the node at start-up.
- `disk[0].import_from` is ignored after creation, so a newer base image only affects new nodes.
- Everything beyond "Ansible can log in" belongs to Ansible.

| Inputs (contract) | Inputs (Proxmox) | Outputs |
|---|---|---|
| `name`, `role`, `ip_cidr`, `cpu`, `memory_mib`, `os_disk_gib`, `data_disk_gib`, `admin_user`, `ssh_public_keys`, `cluster_name`, `gateway`, `dns_servers`, `search_domain`, `timezone` | `node_name`, `vm_id`, `disk_datastore`, `snippets_datastore`, `bridge`, `image_file_id`, `extra_tags` | `name`, `ip`, `role`, `zone`, `vm_id` |
