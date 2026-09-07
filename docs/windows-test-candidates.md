# Opt-in Windows test candidates

This developer channel tests changes before merging them into `main`. It does not
change the default marketplace, installed Plugin, Production API, or released pins.
It is not a claim that Windows GUI acceptance has passed.

## In-place appearance candidate (unreleased)

Windows now attempts the same verified native dark/light controls used on macOS
when an existing controlled connection is available. It does not bypass process
identity or listener ownership, enable debugging in an ordinary running instance,
add a background service, or change first-connection consent.

In-place Restore supports the original system/light/dark choice, including an
implicitly absent setting. Codex changes its own live mode first; the Helper then
restores the exact saved configuration and retains recovery until the official
renderer and native mode are verified. Changed legacy code-theme settings or an
unavailable UI contract require the existing consented restart path. Uncertain
post-change results fail closed and retain recovery. macOS Restore is unchanged.

If Restore preparation succeeds but a later step fails, cleanup runs before
disconnecting. Before any skin removal, it restores the prior native mode on
the same trusted renderer. Once removal may have started, it attempts bounded
official cleanup instead of blindly re-pinning the old mode. Lost identity or
renderer continuity blocks further writes. The operation still reports failure
and retains the original recovery backup; this cleanup never restarts Codex.

Local coordinator tests use a simulated UI boundary and real temporary config/
recovery files. They are not Windows GUI evidence. After a separately approved
signed candidate is available, acceptance must cover both cross-mode directions,
same-mode skin replacement, original system/light/dark Restore, exact settings
and font preservation, and failure recovery. Verify process identity/start time
and renderer continuity as well as the visible result; a window staying open
alone does not prove the same renderer survived. If first connection requires a
controlled launch, approve it separately and do not count it as an in-place test.

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
`windows_test_confirmation=SIGN WINDOWS TEST FULL_40_CHARACTER_SHA`, `build_run_id`
from the successful unsigned run, and protected environment approval for that exact
workflow SHA. It verifies that run's repository, workflow, branch, SHA, completed checks
and immutable artifact ID, then reuses the artifact without rebuilding or retesting.
Failed, skipped, incomplete, expired, foreign or different-SHA evidence is rejected.
Use a new manual run instead of a rerun attempt. The signing job checks out a fixed trusted signer commit, has no
candidate source execution or build cache, and receives the secret only for signing.

The Production environment remains approval-protected. A repository owner must
explicitly approve adding the exact test branch to its branch policy; never allow
arbitrary branches or remove reviewers. Remove that one branch policy to close the
channel. Removing permission does not revoke previously issued signed assets.

The test Helper `.17.windows.1` sorts after the released `.17` but before the next
official `.18`; Bootstrap similarly uses `.16.windows.1`. Reinstalling the old official
Plugin does not downgrade an already installed newer Helper. Appearance Restore restores
Codex appearance, not the Helper version. A later official upgrade must sort higher.

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
