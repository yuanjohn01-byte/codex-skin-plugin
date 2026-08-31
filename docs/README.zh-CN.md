<div align="center">

[English](../README.md) · [简体中文](README.zh-CN.md)

# Codex Skin

**通过 Codex 插件，直接在 Codex 里应用桌面皮肤。**

在官网选中喜欢的皮肤，复制六位数字 ID，再回到普通 Codex 任务里让它应用即可。插件本身免费、开源。

[官方网站](https://codexskin.ai/zh) · [浏览皮肤](https://codexskin.ai/zh/themes) · [工作原理](https://codexskin.ai/zh/how-it-works)

</div>

> [!NOTE]
> Codex Skin 目前通过 GitHub prerelease 分发，并非稳定版本。当前目标平台为 macOS（Apple 芯片和 Intel）与 Windows x64；Windows 上最后一轮真实应用验证仍在进行。Codex Skin 是独立的第三方项目，与 OpenAI 没有关联，也未获得 OpenAI 背书。

![在 Codex Desktop 中切换并应用两款 Codex Skin 皮肤](https://codexskin.ai/assets/product/plugin-applied-switch.webp)

<p align="center"><sub>在真实 Codex 中从皮肤 100017 切换到 100007。截图不含任何工作区内容。</sub></p>

## 看看实际应用效果

目前目录里有 23 款已发布皮肤，其中 6 款免费、17 款为 Pro。下面是三款免费皮肤应用到 Codex Desktop 后的样子。

| [Ember Dune · 100002](https://codexskin.ai/themes/100002) | [Midnight Canopy · 100003](https://codexskin.ai/themes/100003) | [Polar Archive · 100004](https://codexskin.ai/themes/100004) |
| --- | --- | --- |
| <img src="https://codexskin.ai/previews/themes/100002/1.0.0/presentation/1/applied-full-e3d7e3fd6ec62325438c765c657d9db0cff05d51cc0cdeeff1b5dc6a90ef3e2f.webp" alt="Ember Dune 应用到 Codex Desktop 后的效果" width="520"> | <img src="https://codexskin.ai/previews/themes/100003/1.0.0/presentation/1/applied-full-451b388beb30ea68f189f0d722edcee310506239593fea4d3d045411fc828310.webp" alt="Midnight Canopy 应用到 Codex Desktop 后的效果" width="520"> | <img src="https://codexskin.ai/previews/themes/100004/1.0.0/presentation/1/applied-full-2bd88ff2cbb1d20539d39461dd1e6eb7bbe10cd81d8da1a8bb6b8346fa2a4901.webp" alt="Polar Archive 应用到 Codex Desktop 后的效果" width="520"> |

[查看全部皮肤 →](https://codexskin.ai/zh/themes)

## 为什么做成 Codex 插件？

平时在哪里使用 Codex，就在哪里控制皮肤。你不需要另外开着一套皮肤工作室、托盘应用或后台守护进程。每次收到请求，插件只会临时启动 Helper，处理完这一次操作、核对结果后就退出。

这和 [Codex Dream Skin](https://github.com/Fei-Away/Codex-Dream-Skin) 的独立应用方式不同。Codex Skin 从一个普通 Codex 任务开始，插件就是操作入口。其中少量渲染兼容代码改编自该项目采用 MIT 许可证的实现，具体归属写在 [NOTICE](../NOTICE) 中；它的插画、皮肤、安装器和产品标识均未被复用。

## 使用流程

1. 打开[皮肤目录](https://codexskin.ai/zh/themes)，复制准确的六位数字 ID。
2. 首次使用时安装一次插件。
3. 新建一个普通 Codex 任务，让它应用这个 ID。
4. 如果浏览器打开设备授权页，确认授权。随后 Codex Skin 会下载并校验皮肤，应用后再检查画面是否正确。

插件本身免费，免费皮肤也不需要付款。

## 安装

在终端里运行：

```bash
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

最后一条命令应该只显示一个已安装的 `codex-skin@codex-skin`，并且其中 `installed: true`、`enabled: true`。

接着彻底退出 Codex，重新打开并新建一个任务，然后输入：

```text
运行 $codex-skin-version
```

版本检查应确认当前安装的插件、已签名的 Helper 和固定 API 地址 `https://codexskin.ai`。

## 应用、切换、查看状态和恢复

直接在 Codex 任务里这样说即可：

```text
应用 Codex Skin 皮肤 100002
切换到 Codex Skin 皮肤 100005
查看我的 Codex Skin 状态
恢复 Codex 官方外观
```

应用或切换通常需要 20–60 秒。执行期间不要点击、输入、切换页面或关闭 Codex。如果确实需要重启，插件会先明确询问，得到确认后才会继续。

皮肤按会话生效，并不是永久修改。关闭 Codex、重启电脑，或者应用内部重新载入界面，都可能让效果结束。下次回来时，在新任务里重新应用一次即可。

## 恢复原来的外观

最简单的方式是让插件处理：

```text
恢复 Codex 官方外观
```

恢复时不需要皮肤 ID、有效订阅或网络连接。安装器还会在可替换的插件缓存之外保留一份恢复命令：

- macOS：`~/Library/Application Support/CodexSkin/recovery/restore.command`
- Windows：`%LOCALAPPDATA%\CodexSkin\recovery\restore.cmd`

![Codex 已恢复为原来的官方外观](https://codexskin.ai/assets/product/restore-official.png)

## 安全边界和当前适用范围

- 皮肤包只包含结构化数据和本地图片，不能携带任意 CSS、JavaScript、Shell、PowerShell、选择器或远程执行地址。
- Helper 会核对官方 Codex 进程，只通过本机回环地址控制浏览器。每次修改都要经过校验；无法确认成功时会回滚，也不会把这次操作当成完成。
- Codex Skin 不修改官方应用包。插件与 Helper 不会读取或上传提示词、对话、项目文件、源代码、令牌、Cookie、截图或本机绝对路径。
- 它只改变桌面应用里的 Codex 视图，不会改变 Chat、Work、网页版、Codex CLI 或 IDE 扩展。

为了提供服务，系统会保留有限的账户、设备、皮肤交付和应用结果记录。详情见[隐私政策](https://codexskin.ai/zh/privacy)。

请不要为了安装 Codex Skin 而关闭 Gatekeeper、SmartScreen、杀毒软件或其他系统保护。如果系统拦截发布文件，请停止操作并反馈原始错误。

## 升级与排查

刷新 Marketplace 快照，重新安装同一个插件 ID，再核对结果：

```bash
codex plugin marketplace upgrade codex-skin
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

彻底退出 Codex，重新打开，在新任务里运行 `$codex-skin-version`。

如果添加或升级失败，请先保留原始报错，只收集下面这些可以分享的诊断信息：

```bash
codex --version
codex plugin marketplace list
codex plugin list --json
```

不要编辑 Codex 配置，也不要删除 Marketplace 或插件缓存目录。

如果只是 `codex-skin` 的 Marketplace 快照缺失或过期，可以用下面这组可逆命令刷新：

```bash
codex plugin marketplace remove codex-skin
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

如果升级失败，但原来安装的插件还能工作，请保留现有安装。把失败命令、原始错误和脱敏后的诊断信息提交到 [GitHub Issues](https://github.com/yuanjohn01-byte/codex-skin-plugin/issues)。不要分享令牌、Cookie、提示词、源代码、账户数据、截图或本机绝对路径。

## 参与开发

可安装的插件位于 `plugins/codex-skin/`。这个仓库还包含可独立运行的 Go Helper、Bootstrap 与恢复代码、生成后的公开合约、合成测试数据和发布检查。它不包含私有网站源码、客户数据、未发布皮肤包、源美术文件或签名私钥。

提交改动前可以运行：

```bash
go test ./...
go vet ./...
python3 tools/validate_public_repo.py
python3 tools/test_public_repository.py
python3 tools/test_release_descriptor.py
```

通过自动检查并不表示版本已经发布。正式发布前，仍需针对 `main` 上的最终内容完成合并后的 macOS 与 Windows 双平台检查。

## 许可证与第三方说明

Codex Skin Plugin 使用 [MIT License](../LICENSE)。第三方归属说明见 [NOTICE](../NOTICE)。
