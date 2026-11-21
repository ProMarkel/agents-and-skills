# Codex ImageGen · 本地技能包

调用名：`$codex-image-gen`。这是可独立使用的通用版，原有技能可保留并存。

## 在其他设备安装

1. 解压压缩包，将其中完整的 `codex-image-gen` 文件夹放到 Codex 的技能目录：
   - macOS：`~/.codex/skills/codex-image-gen/`
   - Windows：`%USERPROFILE%\.codex\skills\codex-image-gen\`
   - 如果自行设置了 `CODEX_HOME`，使用该目录下的 `skills/codex-image-gen/`。
2. 保持 `SKILL.md` 位于该文件夹第一层，保留 `bin`、`references`、`agents` 和 `src` 子目录。
3. 在新设备本地配置 `CODEX_API_URL` 和 `CODEX_API_KEY`，使 Codex 进程可见。已有配置可沿用；包内没有真实密钥、账户配置或历史任务。此通用版不包含 mac 专用技能新增的 Keychain 持久化或 `.zprofile` 注入方案，也不依赖 `codex-image-gen-mac`。
4. 在 Codex 中使用：`$codex-image-gen 生成一张蓝色玻璃瓶的图片`。如果当前会话尚未出现新技能，可新开会话再试。

支持 macOS Apple Silicon / Intel，以及 Windows ARM64 / x64。包内含四种对应原生可执行文件；正常使用不需要 Python、Node 或 Go。Linux 未包含预编译程序。

macOS 如解压后缺少执行权限，可执行：

```sh
chmod +x ~/.codex/skills/codex-image-gen/bin/codex-image-gen-darwin-*
```

## 离线检查配置

Apple Silicon macOS：

```sh
~/.codex/skills/codex-image-gen/bin/codex-image-gen-darwin-arm64 doctor
```

Intel macOS 将文件名中的 `arm64` 改成 `amd64`。

Windows x64 PowerShell：

```powershell
& "$env:USERPROFILE/.codex/skills/codex-image-gen/bin/codex-image-gen-windows-amd64.exe" doctor
```

Windows ARM64 将文件名中的 `amd64` 改成 `arm64`。

`doctor` 不联网，只检查配置可见性、配置来源和输出目录可写性，不验证额度或模型是否实际可用。在 macOS 上，缺失的进程配置还会尝试从 GUI 登录会话内存读取，不输出密钥。

缺少 API 地址时，由用户选择补充配置，或为本次调用明确选择预设 `https://apinebula.com`（`--use-default-url`）。不会静默切换地址。

## 模型选择规则

- 固定优先级：`gpt-image-2.5` → `gpt-image-2.5-sunburst` → `gpt-image-2.5-flare` → `gpt-image-2`。
- 与 `/models` 返回列表取交集；没有的模型标记为 `unsupported`，不发送生成请求。
- 其他图片模型排在后面，保持接口返回顺序；聊天和视频模型不作图片回退。
- 有成功记录时，在符合列表的成功模型中按上述顺序选择；没有成功记录时，才用本次实际生成按顺序尝试，成功即停止。
- 普通任务复用 5 分钟内的模型列表；`models` 命令强制刷新。查询失败不会产生“不支持”的新标记，也不会提交生成。
- 接口列出不等于已验证可用。只有用户明确要求检查可用性时才运行 `models --check-availability`；它会真实生成测试图，可能收费。
- 显式指定但接口未列出的模型会直接报告不支持，不擅自换成其他模型。
- 仅对安全可重试的请求重试，最多 3 次，间隔 5 / 10 / 15 秒；累计等待不超过 30 秒，请求耗时和视频正常轮询另计。可能已经受理的生成请求不会盲目重复提交。

模型列表和任务状态保存在当前项目的 `output/codex-image-gen/.state/`；也可通过 `CODEX_IMAGE_STATE_DIR` 指定共同的可写目录。缓存按 API 和密钥指纹区分，生成/编辑的成功记录分开保存。该私有状态不在分享包中。

## 文件和验证

- `codex-image-gen/`：完整可安装技能，含源码和测试。
- `manifest.json`：技能文件的 SHA-256 与权限信息。
- `VALIDATION.zh-CN.md`：本次验证范围与平台限制。

完整参数和恢复操作见技能内的 `references/runtime.md`。本包未修改默认 `imagegen` 或其他技能。
