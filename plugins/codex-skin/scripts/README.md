# Scripts

The platform wrappers invoke only the signed Helper copy installed outside the replaceable Plugin cache under Codex Skin's fixed recovery engine path. They do not parse `current.json`, select arbitrary binaries, call the network, or require Node.

If the signed Helper has not been installed by the release bootstrap, the wrappers fail closed. The code-stage branch does not authorize an unsigned local Helper as a user installation.
