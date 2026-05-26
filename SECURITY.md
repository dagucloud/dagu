# Security Policy

## Supported Versions

Dagu security fixes are provided for the current development branch and the latest stable release series.

| Version | Supported |
| --- | --- |
| `main` | Yes |
| Latest stable release series | Yes |
| Older releases | No |

If you report an issue against an older release, we may ask you to verify it on the latest stable release before triage.

## Reporting a Vulnerability

Please do not open a public GitHub issue for suspected security vulnerabilities.

Report vulnerabilities privately to `contact@dagu.sh` with a subject such as `Security report: Dagu`.

If GitHub private vulnerability reporting is available for this repository, you may use that flow instead. When in doubt, email first and we can coordinate from there.

If you are unsure whether something is security-sensitive, err on the side of reporting it privately.

## What To Include

Please include as much of the following as you can:

- Affected version, commit SHA, and installation method (`binary`, `Docker`, `Helm`, `source`, etc.)
- Deployment context (`localhost`, private network, internet-exposed, reverse proxy, distributed worker setup)
- Authentication mode and relevant configuration with secrets redacted
- Reproduction steps or a proof of concept
- Expected impact and any likely attack preconditions
- Whether the issue affects default settings or requires specific features to be enabled
- Any logs, screenshots, traces, or patches that help confirm the issue

## Response Expectations

We aim to:

- Acknowledge new reports within 5 business days
- Share follow-up status updates at least every 7 calendar days while the issue is active
- Coordinate fix timing and public disclosure with the reporter when possible

## Disclosure

Please avoid public disclosure until a fix or mitigation is available and coordinated disclosure timing has been agreed.
