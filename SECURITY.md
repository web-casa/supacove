# Security Policy

## Supported versions

SupaCove is pre-1.0. Only the latest release tag (and, during beta, the tip
of `main`) receives security fixes. There is no long-term-support branch.

## Reporting a vulnerability

**Do not open a public issue.** Email the maintainer address listed on the
repository profile, or use the platform's private vulnerability reporting
feature where available. Include:

- the affected commit or release tag;
- a minimal reproduction or a concrete attack narrative;
- whether you believe the issue is reachable in the default configuration
  (single authenticated admin, BYOS storage).

You should receive an acknowledgement within 7 days. If the report is
accepted, we will coordinate a fix and a disclosure timeline with you;
credit is given unless you ask otherwise.

## Threat model summary (what a report should argue against)

- One authenticated administrator per instance; the console and API are
  session+CSRF protected and default-deny.
- Backups are age-encrypted; the instance stores the public recipient only.
  The age identity is provided explicitly for restore verification
  (`SB_VERIFY_ENABLED`) and is a documented trust decision (ADR-004).
- Outbound requests (webhooks, heartbeat, object storage) are dial-restricted
  against link-local/cloud-metadata addresses (internal/netguard).
- Error surfaces (logs, API responses, task history, metrics) must never
  carry connection credentials; redaction is tested by canary suites.

Out of scope by design: attacks requiring write access to `SB_DATA_DIR`
(the data directory is the trust boundary), and multi-tenant/admin-isolation
scenarios (single-admin model).
