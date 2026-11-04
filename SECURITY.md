# Security policy

## Supported versions

Tapyr has not reached 1.0: security fixes go into the latest minor release (and `main`). Upgrade to the latest release before reporting, if you can.

## Reporting a vulnerability

**Do not open a public issue.** Report it privately through GitHub: on the repository page, **Security**, then **Report a vulnerability** ([private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)). Only the maintainers see the report. If you cannot use it, write to [conduct@tapyr.eu](mailto:conduct@tapyr.eu) with "security" in the subject, without the details of the vulnerability: a maintainer answers with a way to share them privately.

Please include:

- the version (`tapyr version`) and how Tapyr runs (binary, image, configuration features involved);
- what an attacker can do, and the steps or a configuration to reproduce it, against the simulator if possible (`tapyr sim`);
- no real credential, token or data of yours: if a secret leaked, rotate it first, then describe where it appeared.

The maintainers aim to acknowledge a report within 7 days and to agree on a disclosure date with you. A fix is released with a GitHub security advisory and a `Security` entry in [`CHANGELOG.md`](CHANGELOG.md); you are credited unless you prefer otherwise.

## Scope

Tapyr holds credentials of vendor APIs and calls those APIs on behalf of its users. Examples of what we treat as vulnerabilities:

- a secret or token appearing in logs, errors, the status API, the web UI, the debug endpoint, metrics, recordings (`tapyr record`) or the state store unencrypted when `TAPYR_STATE_KEY` is set;
- a way around the basic auth of the status API and the UI, or a write action reachable through them;
- requests sent to a host the configuration did not intend (for example through pagination links or redirects), or a write operation sent to a provider API;
- a way to make Tapyr exceed the limits declared for a provider on purpose (quota exhaustion of the account it serves);
- vulnerabilities of the release artifacts: images, binaries, signatures.

Not in scope: the simulator (`tapyr sim`) and the demo compose (`examples/compose`), which are development tools with fake credentials and no authentication, and must not be exposed to untrusted networks; findings that need a malicious configuration file or provider package (configuration is trusted, like code).

## Verifying releases

Release checksums and images are signed with cosign in keyless mode by the release workflow; [`docs/install.md`](docs/install.md#verify-the-signatures) shows how to verify them.
