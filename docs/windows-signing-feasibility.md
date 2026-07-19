# Windows Helper signing and SmartScreen feasibility

Status: internal S3 feasibility evidence, not a public signing or reputation claim. Last checked: 2026-07-20.

## What the spike proves

The Windows workflow creates a one-day, non-exportable, self-signed RSA code-signing certificate in the GitHub runner's `CurrentUser` store. It temporarily peer-trusts only the public certificate in `CurrentUser\TrustedPeople`, then:

1. signs the native Windows x64 Helper with SignTool `/fd SHA256`;
2. verifies it with the Authenticode `/pa` policy while that temporary local trust exists;
3. executes signed `version --json` and `doctor --json` with Node, Python, and Go removed from `PATH`;
4. changes one byte inside the signed PE image and requires verification to fail; and
5. removes both temporary certificate-store entries in `finally` and verifies cleanup.

The workflow never exports a PFX or private key and never uploads a certificate, thumbprint, subject, signed executable, or certificate-store snapshot. Its only artifact is a short-lived JSON summary containing tool versions, algorithms, hashes, booleans, and explicit limitations.

This proves the repository's Authenticode command, signed-binary execution, verification, tamper-rejection, and cleanup plumbing. It does not prove public trust, stable publisher identity, RFC 3161 timestamping, download reputation, SmartScreen UI behavior, or commercial release readiness.

## SmartScreen boundary

SmartScreen reputation is a cloud and ecosystem result influenced by the downloaded file, signing reputation, prevalence, history, and delivery context. A self-signed certificate is not publicly trusted and cannot establish publisher reputation. A CA-issued or EV identity may improve the trust story, but certificate type alone must not be treated as an automatic positive SmartScreen result.

The CI result therefore always records:

- `formalDistributionReady: false`;
- `smartScreenTested: false`;
- `smartScreenReputationEstablished: false`; and
- `timestampApplied: false`.

SmartScreen UI testing requires the exact final bytes to arrive through the final download channel on clean supported Windows systems. It cannot be replaced by SignTool output or an in-place CI executable.

## Required production flow

Formal distribution remains blocked until all of the following are complete:

1. Choose a publicly trusted code-signing provider or managed service and keep its private key in required hardware/cloud key protection. Do not store PFX material in the repository or ordinary CI artifacts.
2. Sign the exact release bytes with SHA-256 and an RFC 3161 timestamp, using the SignTool shape `/fd SHA256 /tr <approved-https-tsa> /td SHA256` plus the protected identity selector.
3. Verify the exact signed asset with `signtool verify /pa /all /v`, re-run the Helper JSON contracts, and put the post-signing SHA-256 into the signed release descriptor.
4. Test clean download/install/upgrade/rollback on the supported Windows Store Codex environment and record the actual SmartScreen result without promising reputation in advance.
5. Preserve a certificate rotation/revocation plan and last-known-good signed Helper so that signing outages cannot remove offline restore.

This S3 task records the feasible command path and current credential/reputation limitation. It does not select a commercial provider and does not close `M6-SEC-002`.

## Authoritative references

- [Microsoft: SignTool](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool)
- [Microsoft Defender SmartScreen overview](https://learn.microsoft.com/en-us/windows/security/operating-system-security/virus-and-threat-protection/microsoft-defender-smartscreen/)
- [Microsoft Defender SmartScreen FAQ](https://learn.microsoft.com/en-us/windows/security/operating-system-security/virus-and-threat-protection/microsoft-defender-smartscreen/faq)
