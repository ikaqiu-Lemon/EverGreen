# Security Policy

## Supported versions

Security fixes are applied to the latest published release. Older snapshots and
prereleases may not receive backports.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability.

Use GitHub's private vulnerability reporting feature on the repository
`Security` tab. Include:

- the affected version and platform;
- a minimal reproduction;
- the expected and observed behavior;
- the security impact;
- any suggested mitigation.

Avoid including real vault contents, credentials, or other sensitive data.
Maintainers will coordinate disclosure and remediation through the private
report.

## Security model

Evergreen performs no telemetry, model calls, or network requests. It does
process local Markdown, Git repositories, and user-provided files, which must
still be treated as untrusted input. Run the CLI with the minimum filesystem
permissions required for the target vault.
