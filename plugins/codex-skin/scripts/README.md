# Scripts

For `theme apply`, the platform wrappers first obtain only the release-tagged Bootstrap launcher whose platform filename and SHA-256 are generated into `bootstrap-pins.sh` / `bootstrap-pins.ps1`. The launcher uses the embedded Helper release public key and fixed GitHub Release origin to verify the canonical descriptor, detached Ed25519 signature, platform, size, artifact SHA-256, downgrade rule, and fixed `version` / `doctor` self-tests before installing the Helper outside the replaceable Plugin cache.

After bootstrap, all commands invoke only the verified Helper copy under Codex Skin's fixed recovery engine path. `theme continue`, `status`, and `theme restore` never download a launcher or Helper; offline Restore remains independent of the Plugin cache. Missing, symlinked, or hash-mismatched launchers and Helpers fail closed. Users do not need Node, Python, or Go.
