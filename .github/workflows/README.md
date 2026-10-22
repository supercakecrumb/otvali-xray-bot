# CI and tagged image publication

`Docker Image CI / build` runs for pull requests, every pushed branch and `v*`
tags. It builds the existing Dockerfile's `build` stage: Go1.27.2 runs
`CGO_ENABLED=0 go test -mod=vendor ./...` and the existing binary build. No
runtime image, toolchain, Go dependency or test command is changed by this
workflow. Tests that need a separately supplied test database still require
that integration environment; a green default build does not prove those ran.

Only a pushed tag can publish, after `build` passes. The release job validates
the exact event's `GITHUB_REF_NAME` as Docker-compatible `vX.Y.Z` or a SemVer
prerelease, and checks out the event commit SHA. It does not discover a nearby
tag with `git describe`. Leading-zero numeric versions/prerelease identifiers,
build metadata (`+...`), non-version names and oversized Docker tags fail
before registry login. Action versions are pinned to official release commits.

Pins verified against upstream releases on 2026-10-22:
[checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1),
[login-action v4.6.0](https://github.com/docker/login-action/releases/tag/v4.6.0),
and [build-push-action v7.4.0](https://github.com/docker/build-push-action/releases/tag/v7.4.0).
All three declare `using: node24`; their exact commit SHAs are in the workflow.

Deploy using the exact version tag and record the resulting image digest.
The existing `latest` alias is retained for compatibility; it is mutable and
is not a deployment pin. Registry jobs are serialized without cancellation;
GitHub does not guarantee their semantic-version order. Do not move or reuse
released version tags. GHCR tags themselves remain mutable: this workflow
makes event selection deterministic, not registry-enforced immutability.

Default token permission is `contents: read`; only the publishing job receives
`packages: write`. Checkouts do not persist credentials. Superseded branch/PR
checks can be cancelled, while tag runs finish. No production deployment,
secret provisioning or Telegram notification is performed here.

## Operator checks remain separate

An image-build success does not establish live VPN behavior. The public API
`/health` checks PostgreSQL independently of Remnawave, and TCP monitors do
not validate credentials, split routing or UDP443. Use the existing owner SSH
operator path and private scalar receipts for scoped diagnostics; keep public
admin ACLs unchanged and avoid credential/log output. Preserve unknown probe
outcomes, reconcile owned processes/files before retrying, and retain remote
scalar results so an SSH timeout does not erase completed evidence. No new
operator script, account, token, scheduled job or alert is introduced here.
