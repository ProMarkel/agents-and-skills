# Codex ImageGen Mac 技能包

本包包含独立的 macOS 技能 `codex-image-gen-mac`，内置 Apple Silicon 与 Intel Mac 可执行文件，不依赖另一个已安装技能。

## 安装

1. 解压 ZIP，将完整的 `codex-image-gen-mac` 文件夹复制到 Codex 技能目录：
   - 默认：`~/.codex/skills/codex-image-gen-mac/`
   - 如果设置了 `CODEX_HOME`：`$CODEX_HOME/skills/codex-image-gen-mac/`
2. 保留该文件夹中的 `SKILL.md`、`agents`、`bin` 和 `references`。`bin` 下为 `codex-image-gen-mac-darwin-arm64` / `codex-image-gen-mac-darwin-amd64`；`src` 为可选的 Go 源码，用于自行复现构建，不影响使用。
3. 按 `codex-image-gen-mac/references/macos-persistence.md` 配置钥匙串和 `~/.zprofile`。
4. 在 Codex 中使用 `$codex-image-gen-mac` 调用技能。

钥匙串服务名为 `codex-image-gen`。API 密钥应保存在 macOS 钥匙串中，`.zprofile` 只读取密钥并设置 `CODEX_API_URL`；不要把密钥写入文件、命令参数或分享包。包内没有真实密钥、用户 API 地址、生成历史或恢复状态。

## 平台

支持 macOS Apple Silicon（arm64）和 Intel（x86_64）。两个原生 CLI 已随技能打包，正常使用不需要 Python、Node 或 Go。此分享包不含 Windows 或 Linux 可执行文件。

默认输出目录为 `output/codex-image-gen-mac/`，私有状态位于其下的 `.state/`。需要改用其他位置时传 `--out-dir`。

