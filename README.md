<div align="center">

[English](README.md) · [简体中文](docs/README.zh-CN.md)

# Codex Skin

**Apply Codex Desktop themes from inside Codex, through a plugin.**

Choose a theme on the Codex Skin website, copy its six-digit ID, and ask Codex to apply it from a normal task. The Plugin is free and open source.

[Website](https://codexskin.ai/) · [Browse themes](https://codexskin.ai/themes) · [How it works](https://codexskin.ai/how-it-works)

</div>

> [!NOTE]
> Codex Skin is currently distributed as a GitHub prerelease, not a stable release. It targets Codex Desktop on macOS (Apple silicon and Intel) and Windows x64. Final real-app validation on Windows is still in progress. Codex Skin is an independent third-party project and is not affiliated with or endorsed by OpenAI.

![Two Codex Skin themes applied inside Codex Desktop](https://codexskin.ai/assets/product/plugin-applied-switch.webp)

<p align="center"><sub>A real switch from theme 100017 to 100007 inside Codex. The screenshot contains no workspace content.</sub></p>

## See a few themes applied

There are currently 23 published themes: 6 Free and 17 Pro. Here are three Free themes running in Codex Desktop.

| [Ember Dune · 100002](https://codexskin.ai/themes/100002) | [Midnight Canopy · 100003](https://codexskin.ai/themes/100003) | [Polar Archive · 100004](https://codexskin.ai/themes/100004) |
| --- | --- | --- |
| <img src="https://codexskin.ai/previews/themes/100002/1.0.0/presentation/1/applied-full-e3d7e3fd6ec62325438c765c657d9db0cff05d51cc0cdeeff1b5dc6a90ef3e2f.webp" alt="Ember Dune applied in Codex Desktop" width="520"> | <img src="https://codexskin.ai/previews/themes/100003/1.0.0/presentation/1/applied-full-451b388beb30ea68f189f0d722edcee310506239593fea4d3d045411fc828310.webp" alt="Midnight Canopy applied in Codex Desktop" width="520"> | <img src="https://codexskin.ai/previews/themes/100004/1.0.0/presentation/1/applied-full-2bd88ff2cbb1d20539d39461dd1e6eb7bbe10cd81d8da1a8bb6b8346fa2a4901.webp" alt="Polar Archive applied in Codex Desktop" width="520"> |

[Browse the full gallery →](https://codexskin.ai/themes)

## Why a Codex plugin?

Codex is already where you work, so it is also where you control the theme. You do not need to keep a separate theme studio, tray app, or background daemon running. Each request starts a short-lived Helper. It handles that one job, checks the result, and exits.

This is a different workflow from [Codex Dream Skin](https://github.com/Fei-Away/Codex-Dream-Skin), which uses a standalone app. Codex Skin starts from a regular Codex task, with the Plugin as the control point. A small part of the renderer compatibility code is adapted from that project's MIT-licensed implementation; the exact attribution is in [NOTICE](NOTICE). We did not reuse its artwork, themes, installer, or product identity.

## How it works

1. Browse the [theme gallery](https://codexskin.ai/themes) and copy the exact six-digit theme ID.
2. Install the Plugin once.
3. In a normal Codex task, ask it to apply that ID.
4. If a browser approval page opens, approve the device request. Codex Skin then downloads, verifies, applies, and checks the visible result.

The Plugin itself is free. Free themes do not require payment.

## Install

Run these commands in a terminal:

```bash
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

The final command must show exactly one installed `codex-skin@codex-skin` entry with `installed: true` and `enabled: true`.

Completely quit Codex, reopen it, and start a new task. Then ask:

```text
Run $codex-skin-version
```

The version check should confirm the installed Plugin, the signed Helper, and the fixed API origin `https://codexskin.ai`.

## Apply, switch, check, and restore

Use ordinary language in a Codex task:

```text
Apply Codex Skin theme 100002
Switch to Codex Skin theme 100005
Show my Codex Skin status
Restore the official Codex appearance
```

Apply or switch usually takes 20–60 seconds. While it is running, do not click, type, navigate, or close Codex. If a restart is required, the Plugin asks for explicit confirmation before changing anything.

Themes are session-based, not permanent. Closing Codex, restarting the computer, or a later interface reload can end the visual effect. Apply the theme again in a new task when you return.

## Restore the original appearance

The easiest route is to ask the Plugin:

```text
Restore the official Codex appearance
```

Restore does not need a theme ID, an active subscription, or a network connection. The installer also keeps a recovery command outside the replaceable Plugin cache:

- macOS: `~/Library/Application Support/CodexSkin/recovery/restore.command`
- Windows: `%LOCALAPPDATA%\CodexSkin\recovery\restore.cmd`

![Codex restored to its original appearance](https://codexskin.ai/assets/product/restore-official.png)

## Safety and current scope

- Theme packages contain structured data and local images. They cannot include arbitrary CSS, JavaScript, Shell, PowerShell, selectors, or remote execution URLs.
- The Helper checks the official Codex process and uses loopback-only browser control. It verifies each change and rolls it back when it cannot confirm success.
- Codex Skin does not modify the official application bundle. The Plugin and Helper do not read or upload prompts, conversations, project files, source code, tokens, cookies, screenshots, or absolute local paths.
- It styles only the Codex view in the desktop app. It does not style Chat, Work, the web app, Codex CLI, or IDE extensions.

The service keeps limited account, device, theme-delivery, and application-result records needed to provide the product. See the [Privacy Policy](https://codexskin.ai/privacy) for details.

Do not disable Gatekeeper, SmartScreen, antivirus software, or other system protections to install Codex Skin. If your system blocks a release asset, stop and report the error.

## Upgrade and troubleshooting

Refresh the Marketplace snapshot, reinstall the same Plugin ID, and verify it:

```bash
codex plugin marketplace upgrade codex-skin
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

Completely quit Codex, reopen it, and run `$codex-skin-version` in a new task.

If add or upgrade fails, keep the original error and collect only these shareable diagnostics:

```bash
codex --version
codex plugin marketplace list
codex plugin list --json
```

Do not edit Codex configuration or delete Marketplace/Plugin cache directories.

If only the `codex-skin` Marketplace snapshot is missing or stale, use this reversible refresh:

```bash
codex plugin marketplace remove codex-skin
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

If upgrade fails but the installed Plugin still works, leave it installed. Open a [GitHub issue](https://github.com/yuanjohn01-byte/codex-skin-plugin/issues) with the command, original error, and redacted diagnostics. Do not share tokens, cookies, prompts, source code, account data, screenshots, or absolute local paths.

## Development

The installable Plugin lives in `plugins/codex-skin/`. This repository also contains the self-contained Go Helper, Bootstrap and recovery code, generated public contracts, synthetic fixtures, and release checks. It does not contain the private website, customer data, unreleased theme packages, source artwork, or signing keys.

Useful checks for a local contribution:

```bash
go test ./...
go vet ./...
python3 tools/validate_public_repo.py
python3 tools/test_public_repository.py
python3 tools/test_release_descriptor.py
```

Passing the automated checks does not publish a release. Before anything ships, we still run the required post-merge two-platform check against the exact version on `main`.

## License and notices

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party attributions are listed in [NOTICE](NOTICE).
