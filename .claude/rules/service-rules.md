# Service Rules (PART 24, 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

Covers: Privilege Escalation & Service (24), Service Support (25).

## CRITICAL - NEVER DO
- Never prompt for escalation if the user cannot actually escalate (not in
  sudoers/wheel/admin) — show an informative error instead.
- Never write service files (systemd/OpenRC/rc.d/launchd) that assume the
  binary itself performs escalation — the service manager runs it
  privileged; the binary only detects and drops.
- Never reuse a UID/GID from the reserved/well-known list, and never let
  UID != GID for the `{internal_name}` system user.
- Never skip the destructive-action confirmation prompt on `--service
  --uninstall` before deleting data dirs and the system user.
- Never claim cashp can run its server fully unprivileged as a general
  rule — that contradicts IDEA.md's documented exception.

## CRITICAL - ALWAYS DO
- Server binary self-detects root/admin vs regular user at startup and
  handles user/group creation, dir setup, and permissions itself — the
  `--service --install` flag only installs/starts the service unit.
- Unix service default: start elevated for privileged port binding, then
  drop to the `{internal_name}` system user — UNLESS IDEA.md explicitly
  requires permanent root, which cashp does.
- cashp is the documented permanent-root exception (IDEA.md "Security
  decisions & exceptions"): the server needs sustained root/elevated
  privileges to manage VMs (libvirt/KVM), containers
  (Docker/Incus/Podman), mail, DNS, and firewall — this is
  non-negotiable and must NOT be "fixed" by adding a privilege-drop step.
  Mitigate via strict internal RBAC and per-tenant isolation, not by
  dropping process privileges.
- Any service file/docs for cashp must explicitly state permanent root
  and explain why privilege drop is not possible (per PART 25's
  exception clause).
- Windows path: run as Virtual Service Account
  (`NT SERVICE\{internal_name}`) — already minimal-privilege, no drop
  needed there regardless of the Unix exception.
- Support all init systems: systemd, OpenRC, SysVinit, runit (Linux);
  rc.d (BSD); launchd (macOS); Windows Service.

## KEY DECISIONS (pre-answered)

| Question | Answer | Spec Reference |
|---|---|---|
| Does cashp's server drop privileges after binding, like the generic template? | No — permanent root is required and documented as an exception | IDEA.md "Security decisions & exceptions"; AI.md PART 25 "Service Templates" exception clause |
| Who creates the `{internal_name}` system user? | The server binary itself, during normal startup, not the `--service --install` flag | AI.md PART 24 "Service Installation Logic" |
| What UID/GID range for the service account? | 200-899, UID must equal GID, excluding reserved/well-known IDs | AI.md PART 24 "System User Requirements" |
| Does the binary prompt for sudo if the user can't escalate? | No — shows an informative error instead | AI.md PART 24 "Overview" |
| Does `--service --disable` delete data/config/user? | No — only stops + disables autostart; everything else remains | AI.md PART 24 "Service Disable Logic" |
| Does `--service --uninstall` delete the binary? | No — deletes data/config/user, prints manual `rm` instructions for the binary | AI.md PART 24 "Service Uninstall Logic" |

## TERMINOLOGY

| Term | Meaning |
|---|---|
| Escalation | Process of a non-root user gaining root/admin (sudo, su, pkexec, doas, UAC, runas) |
| Privilege drop | Process lowering its own privileges after performing a privileged action (e.g. binding <1024) |
| Permanent root exception | IDEA.md-documented case (cashp) where privilege drop never happens because the workload needs sustained elevated access |
| System user | Non-login service account (`{internal_name}`), UID==GID, 200-899 range |
| VSA | Windows Virtual Service Account, auto-managed minimal-privilege identity |
| Service (escalated) mode | Binary run by root/admin via a service manager, any port allowed |
| User mode | Binary run by the calling user, restricted to ports >1024, no drop needed |

---
For complete details, see AI.md PART 24, 25
