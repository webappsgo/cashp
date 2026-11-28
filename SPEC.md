## Optional parts
- PART 34: REQUIRED
- PART 35: REQUIRED
- PART 36: REQUIRED

## Privilege model

- On Unix, the server process runs permanently as root under the AI.md PART 24
  project-specific exception. Its core purpose includes installing and
  controlling native host services, firewall/network configuration, container
  engines, and hypervisors; dropping privileges would prevent these required
  features from working. Tenant workloads remain isolated from the server
  process. On Windows, use the Virtual Service Account defined by AI.md.
