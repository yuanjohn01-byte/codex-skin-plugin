# macOS Helper signing and notarization feasibility

Status: internal S3 feasibility evidence, not a public signing claim. Last checked: 2026-07-20.

## What the spike proves

The Public workflow builds the arm64 and x64 Mach-O Helpers, copies them into an isolated temporary directory, applies an ad-hoc signature, and requires both of these checks:

- `codesign --verify --strict --verbose=2` accepts the unmodified copy.
- The same verification rejects a copy changed by one byte after signing.

The workflow records only counts, tool versions, filenames, hashes, and boolean results. It never prints identity names or uploads a certificate, private key, keychain, notarization credential, signed binary, or signing profile. Its JSON result always says `formalDistributionReady: false` and `notaryUploadAttempted: false`.

Ad-hoc signing proves only that the build and bootstrap can preserve and verify a Mach-O code-signature envelope. It does not identify Codex Skin, establish Apple trust, create a secure timestamp, enable hardened runtime, create a notarization ticket, or establish Gatekeeper acceptance for public users.

## Local evidence and current limitation

On 2026-07-20 the development Mac had Command Line Tools `26.2.0.0.1.1764812424`, `notarytool 1.1.0 (39)`, `stapler`, and zero valid code-signing identities; the Developer ID Application identity count was zero. No notarization upload was attempted.

The exact ad-hoc output for a run is generated under ignored `dist/signing/`. CI retains the non-secret summary for 14 days. `spctl` is recorded but is not treated as an authoritative public-distribution result: an ad-hoc binary is expected to be rejected, while a locally accepted result can also depend on machine policy or quarantine context.

## Required production flow

Formal distribution remains blocked until all of the following are implemented and evidenced on the exact release bytes:

1. Obtain a valid `Developer ID Application` certificate under the project Apple Developer team. Keep the private key in a protected signing service or ephemeral CI keychain; never commit or upload it as a general build artifact.
2. Sign each final Helper with a secure timestamp and hardened runtime, for example `codesign --force --options runtime --timestamp --sign <protected-identity> <helper>`.
3. Verify the signed bytes with `codesign --verify --strict --verbose=2` and record the post-signing SHA-256 in the signed release descriptor.
4. Submit a supported archive/container with `xcrun notarytool submit ... --wait` using protected App Store Connect or keychain-profile credentials. Preserve the submission ID and accepted result as release evidence.
5. Test Gatekeeper on clean supported macOS systems using the exact download channel and quarantine behavior.

Apple's `stapler` supports app bundles, UDIF disk images, and signed flat installer packages, not a standalone Mach-O executable or ZIP. Therefore the current raw-binary GitHub Release design cannot claim an offline stapled ticket. Before M4/M6 release, choose and test one of these explicit paths:

- distribute a notarized/stapled supported container and update descriptor/bootstrap handling; or
- retain raw assets only after the exact online Gatekeeper/notarization behavior is proven and the offline limitation is accepted as a documented release decision.

This S3 task does not make that product/release decision and does not close `M6-SEC-002`.

## Authoritative references

- [Apple: Create Developer ID certificates](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/)
- [Apple: Notarizing macOS software before distribution](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)
- [Apple: Customizing the notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)
