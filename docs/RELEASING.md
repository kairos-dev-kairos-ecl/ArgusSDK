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
| `checksums.txt` (+ GPG signature) | — |

The GitHub Release is created as a **draft** so you can review artifacts before
publishing.

## Signing & notarization

Signing activates automatically when the matching secrets are present; without
them the release still builds, just unsigned. Configure these as repository
**Actions secrets**:

| Secret | Purpose |
|---|---|
| `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`, `GPG_FINGERPRINT` | Detached GPG signature over `checksums.txt` |
| `WINDOWS_CERT_BASE64`, `WINDOWS_CERT_PASSWORD` | Authenticode signing of the Windows `.exe` (base64-encoded PKCS#12) |
| `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD` | Apple Developer ID certificate (base64-encoded PKCS#12) |
| `MACOS_NOTARY_ISSUER_ID`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_KEY` | App Store Connect API key for notarization |

Notes:
- Windows Authenticode uses `osslsigncode` and macOS notarization uses the
  GoReleaser built-in notary (both run on the Linux runner — no macOS runner
  required).
- To preview a build locally without any secrets:
  ```bash
  goreleaser release --snapshot --clean --skip=publish,sign,notarize
  goreleaser check     # validate the configuration
  ```

## Versioning

The version is injected at build time via `-ldflags "-X main.version=<tag>"`
and surfaced by `argus-agent --version`. Follow [SemVer](https://semver.org);
update `CHANGELOG.md` before tagging.
