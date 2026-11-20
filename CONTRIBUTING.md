# Contributing to the Kumiko satellite

Thanks for your interest. Please open an issue before a larger change, so you don't spend time
on something we are already building or have ruled out.

## Setup

    git clone https://github.com/CosmicDriftGameStudio/kumiko-satellite.git
    cd kumiko-satellite
    go vet ./... && go test ./...

CI runs the tests on Linux and Windows (including a smoke test of the real window) and builds the
macOS binaries on a Mac runner.

## Conventions

- **Dependencies:** the Go standard library plus the two pinned window modules listed in the
  README. A new module needs a strong reason; the code should stay auditable in an afternoon.
- **One small package per topic** under `internal/`.
- **Tests are not optional.** Behaviour changes ship with a test; platform code (Windows paths,
  autostart, folder guard) gets a test that runs on that platform in CI.
- **Security model:** a change to pairing, token storage, the local page or the folder guard
  updates the security section of the README in the same PR.
- **Commit messages:** conventional prefixes (`feat:`, `fix:`, `ci:`, `docs:`, `refactor:`),
  subject under 70 characters, body explains the *why*.

## License

By contributing, you agree your contributions are licensed under the [MIT License](./LICENSE).
Sign off each commit (`git commit -s`) to certify the
[Developer Certificate of Origin](https://developercertificate.org/).

## Reporting security issues

Please don't open a public issue for a vulnerability; see [SECURITY.md](.github/SECURITY.md).
