# Releasing

Releases are fully automated by [GoReleaser](https://goreleaser.com) via the
`release` GitHub Actions workflow. Pushing a semver tag builds, signs, and
publishes everything.

```bash
git tag v1.0.0
git push origin v1.0.0
```

## What gets produced

| Artifact | Platforms |
|---|---|
| Static binaries | linux, windows, darwin × amd64, arm64 |
| `.deb` / `.rpm` packages (systemd unit + default config + service user) | linux |
| Archives (`.tar.gz` / `.zip`) incl. service units and install scripts | all |
| Multi-arch container image | `ghcr.io/kairos-dev-kairos-ecl/argus-agent` |
| `checksums.txt` (+ cosign `.sig`/`.pem`) | — |

The GitHub Release is created as a **draft** so you can review artifacts before
publishing.

## Trust stack (free — no certificates, no secrets)

The release is verifiable out of the box using Sigstore and GitHub OIDC. There
is **nothing to configure** — these run on the workflow's own identity.

- **cosign keyless signatures** over `checksums.txt` and the container image.
  Verify a download:
  ```bash
  cosign verify-blob \
    --certificate checksums.txt.pem \
    --signature checksums.txt.sig \
    --certificate-identity-regexp 'https://github.com/kairos-dev-kairos-ecl/ArgusSDK/.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    checksums.txt
  ```
- **SLSA build provenance** for every artifact:
  ```bash
  gh attestation verify argus-agent_1.0.0_linux_amd64.tar.gz \
    --repo kairos-dev-kairos-ecl/ArgusSDK
  ```

Provenance cryptographically ties each binary to this exact source commit and
build workflow — stronger integrity assurance than a paid code-signing cert,
which only suppresses OS GUI warnings and says nothing about the contents.

Local validation without any of this:
```bash
goreleaser check                                              # validate config
goreleaser release --snapshot --clean --skip=publish,sign,docker
```

## Homebrew tap (free, optional)

`brew`-installed CLI binaries are not Gatekeeper-quarantined, so a tap is the
zero-cost way to ship to macOS without notarization. The formula is generated
on every release but not pushed until you enable it:

1. Create the repo `kairos-dev-kairos-ecl/homebrew-tap`.
2. Add a PAT secret `HOMEBREW_TAP_GITHUB_TOKEN` (repo scope).
3. In `.goreleaser.yaml`, set the `brews` block's `skip_upload` to `"false"` and
   add `token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"` under `repository`.

Then `brew install kairos-dev-kairos-ecl/tap/argus-agent` works.

## Optional: paid OS code signing (later)

Only needed to remove SmartScreen/Gatekeeper warnings for double-click
installs — not required for a functional, verifiable release.

- **Windows Authenticode** — apply to the [SignPath Foundation](https://signpath.org)
  free OSS program, or use a cloud HSM (Azure Trusted Signing, DigiCert
  KeyLocker, SSL.com eSigner). Add a signing step to the release workflow per the
  provider's action/CLI.
- **macOS Developer ID + notarization** — requires an Apple Developer membership
  ($99/yr); re-add a GoReleaser `notarize.macos` block fed by an exported `.p12`
  and an App Store Connect API key.

## Versioning

The version is injected at build time via `-ldflags "-X main.version=<tag>"`
and surfaced by `argus-agent --version`. Follow [SemVer](https://semver.org);
update `CHANGELOG.md` before tagging.
