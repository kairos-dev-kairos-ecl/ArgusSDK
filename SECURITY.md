# Security Policy

## Supported versions

ArgusSDK follows semantic versioning. Security fixes are applied to the latest
minor release line.

| Version | Supported |
|---------|-----------|
| 1.0.x   | ✅        |

## Reporting a vulnerability

Please report security vulnerabilities **privately** — do not open a public
GitHub issue.

Use GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository, or email the maintainers if a contact address is published
in the repository profile.

When reporting, please include:

- A description of the issue and its impact.
- Steps to reproduce (a minimal proof of concept if possible).
- The affected version or commit.

We aim to acknowledge reports within a few business days and will keep you
informed as we work on a fix. Please give us a reasonable opportunity to release
a patch before any public disclosure.

## Security posture

ArgusSDK is designed to be safe to deploy on production hosts and enterprise
endpoints:

- **Transport:** TLS 1.3 is mandatory in remote mode; there is no downgrade path.
- **Secrets at rest:** Instance identity and credentials are stored in an
  AES-256-GCM encrypted state file (`agent-state.json`), never in plaintext.
  The master key is supplied via `ARGUS_MASTER_KEY` and never persisted.
- **Least privilege:** The container image runs as a non-root user. The EUC
  collector observes only connection metadata for configured AI endpoints — no
  process enumeration, file monitoring, full packet capture, or payload
  inspection.
- **Bounded hot-reload:** SIGHUP re-applies only the EUC watch list and log
  level. Transport, authentication, and output topology cannot be changed
  without a restart, so a malformed config edit can never silently re-route
  signal delivery.
