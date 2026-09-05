# Opt-in Windows test candidates

This developer channel tests changes before merging them into `main`. It does not
change the default marketplace, installed Plugin, Production API, or released pins.
It is not a claim that Windows GUI acceptance has passed.

## Build and test

Use the exact committed SHA on `codex/win-002-test-channel`. Ordinary branch pushes
have no automatic CI; keep iterative development separate from an open delivery PR.
When a coherent candidate is ready, manually dispatch the existing Helper workflow:

```sh
gh workflow run helper-build-spike.yml \
  --ref codex/win-002-test-channel \
  -f run_profile=windows-test-build-only \
  -f candidate_sha=FULL_40_CHARACTER_SHA
```

The named profile builds a candidate and runs native Windows and relevant macOS
checks. It has no signing secrets or Release write permission. It is development
evidence, not a substitute for required PR checks or final real-device acceptance.

For a local build, use a clean committed checkout, fetch tags first, and run:

```sh
python3 tools/windows_test_candidate.py --candidate-sha FULL_40_CHARACTER_SHA
```

The output is `dist/windows-test/`. Existing output is rejected; use a fresh output
directory for another attempt. The builder verifies artifact provenance, hashes,
versions, and descriptor consistency and generates a deterministic marketplace ZIP
from committed public Plugin files only. The ZIP contains generated test pins and a
SHA-specific Plugin cachebuster. The source Plugin remains unchanged.

The fixed test versions live in `tools/release_profiles.py`. Each published candidate
needs a new version/tag, never an overwritten Release. Update the version/key binding
and corresponding tests when advancing the test version. All three architecture assets
are built to preserve the descriptor contract; the candidate's human test scope is
Windows x64, not a new macOS release or expanded platform support.

## Protected signing and distribution

The `signed-windows-test-only` profile additionally requires
`windows_test_confirmation=SIGN WINDOWS TEST FULL_40_CHARACTER_SHA` and protected
environment approval for that exact workflow SHA. It signs only after its build and
platform checks pass. The signing job checks out a fixed trusted signer commit, has no
candidate source execution or build cache, and receives the secret only for signing.

The Production environment remains approval-protected. A repository owner must
explicitly approve adding the exact test branch to its branch policy; never allow
arbitrary branches or remove reviewers. Remove that one branch policy to close the
channel. Removing permission does not revoke previously issued signed assets.

Neither profile creates a tag or GitHub Release. After a separately authorized signing
run, verify the candidate SHA, ZIP hash, six binary hashes, detached signature and exact
asset list. Publish the matching Helper/Bootstrap assets and descriptor/signature as a
new immutable **prerelease**, with `latest=false`, targeting the exact candidate SHA.
The default marketplace pins and server recommended/minimum versions stay unchanged.

**Do not install the ZIP before those matching signed assets are published.** The
normal Plugin → fixed-source Bootstrap → verified Helper installation chain is retained;
an unsigned binary or a manually replaced recovery engine is not an install test.

The tester receives the candidate identity, checksum, exact install instructions,
recovery instructions and a short result checklist. Installing this candidate uses the
normal Codex Skin runtime storage; it is not a second isolated Codex profile. Start with
status/version and one Apply/Restore round, then same-mode and cross-mode switching.
Human confirmation remains required before an app restart or Restore, and any uncertain
result stops the test. No system protection, private credentials or chat history need
to be disabled, shared or copied.

Once the candidate works on the real Windows device, promote a coherent increment to
a delivery PR and run the applicable complete release checks. Keep failed test artifacts
distinct from the approved final release; test-channel success is not Production release.
