# Security Policy

## Reporting a Vulnerability

If you find a security vulnerability in the Kumiko satellite, **please do not open a public
issue**. Report it privately so we can ship a fixed release first.

1. [GitHub Private Vulnerability Reporting](https://github.com/CosmicDriftGameStudio/kumiko-satellite/security/advisories/new),
   tracked in the Security tab.
2. Email **security@cosmicdriftgamestudio.com** with a description and reproduction steps.

You can expect an acknowledgement within **3 working days**, a public advisory together with a
fixed release, and credit in the advisory unless you prefer to stay anonymous.

## Supported Versions

Only the [latest release](https://github.com/CosmicDriftGameStudio/kumiko-satellite/releases/latest)
receives security fixes.

## Scope

In scope:

- The satellite binary: pairing (device flow), token storage, folder guard, uploads.
- The local status page on `127.0.0.1`: session secret, login nonce, cookie, `Host`/`Origin`
  checks, the window and the folder dialog.
- Autostart entries (`schtasks`, LaunchAgent, XDG autostart).

The server side of the protocol (`connectors` feature) is in scope of
[kumiko-framework](https://github.com/CosmicDriftGameStudio/kumiko-framework/security/policy).

Out of scope: attacks that need code execution as the same OS user who runs the satellite, and
the remaining cookie sharing between ports described in the README security model.

## Coordinated Disclosure

Public details land in an advisory only after a fixed release is published, with at least a
7-day grace period for customers to update.
