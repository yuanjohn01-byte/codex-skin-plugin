## Task

- Project-plan task ID:
- Repo scope: `plugin` / `both`
- CI profile: `fast` / `standard` / `full`
- Changed repositories: `plugin` / `private + plugin`
- Paired Private PR/ref (`both` only; otherwise `N/A`):
- Handoff allowlist (`both` only; otherwise `N/A`):
- User-visible change:

## Frozen head

- Final candidate commit:
- [ ] Final head is frozen; review and CI refer to this exact commit

## Verification

- [ ] Manifest and public-boundary validation
- [ ] Secret/license/forbidden-path review
- [ ] Relevant unit/contract/integration checks
- [ ] macOS evidence when affected
- [ ] Windows evidence when affected
- [ ] Apply/verify/restore evidence when affected
- [ ] Version, CHANGELOG, contracts and docs updated
- Tests run:
- Not tested and why:
- Evidence location (PR/Actions/artifact; no evidence-only commit):

## Release safety

- [ ] Required Private API is already backward-compatible and deployed
- [ ] No private website/template source, credentials, user data or private themes
- [ ] Rollback/update path documented
- [ ] `plugin` work does not require a Private branch/twin
- [ ] Any `both` cross-repository check is separate from this baseline
