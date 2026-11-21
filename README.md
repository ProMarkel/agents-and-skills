# agents-and-skills

> A personal collection of Codex agents and skills I created or refined. Drop-in skill packages with bundled native CLIs — no Python, Node, or Go needed at runtime.

个人创建与优化的 Codex agents / skills 集合，用于公开分享。每个目录是一个自包含的发布包：技能定义、原生可执行文件、参考文档、Go 源码与验证记录。开箱即用，运行时不需要 Python / Node / Go。

## 技能一览

| 技能 | 调用名 | 平台 | CLI 文件名 | 凭证方式 | 发布 |
| --- | --- | --- | --- | --- | --- |
| Codex ImageGen | `$codex-image-gen` | macOS (arm64/amd64)、Windows (amd64/arm64) | `codex-image-gen-*` | 环境变量；macOS GUI 会话兼容 | 2026-11-21 |
| Codex ImageGen Mac | `$codex-image-gen-mac` | macOS (arm64/amd64) | `codex-image-gen-mac-*` | Keychain + `~/.zprofile`，zsh login shell | 2026-11-22 |

两个技能互不依赖，可同时安装共存。核心逻辑相同，差异在平台覆盖与凭证持久化方式；默认输出目录分别为 `output/codex-image-gen/` 与 `output/codex-image-gen-mac/`，均与技能名一致：

- 需要 Windows，或偏好临时环境变量配置 → 用通用版
- 只在 mac 上使用，希望密钥存入钥匙串免重复配置 → 用 Mac 版

两者都通过兼容的图片 API 网关工作：读取 `CODEX_API_URL` 与 `CODEX_API_KEY`，支持模型自动选择、批量生成和失败恢复。未配置 URL 时不会静默切换，需用户明确选择是否使用内置预设地址。

## 快速开始

1. 进入对应技能包，阅读 `INSTALL.zh-CN.md`。
2. 把包内**内层技能文件夹**（`codex-image-gen/` 或 `codex-image-gen-mac/`）复制到 Codex 技能目录：
   - macOS：`~/.codex/skills/<技能名>/`
   - Windows：`%USERPROFILE%\.codex\skills\<技能名>\`
   - 设置了 `CODEX_HOME` 时用 `$CODEX_HOME/skills/<技能名>/`

   > 注意：复制的是内层文件夹，不是外层带日期的发布包目录。
3. 在本机配置 `CODEX_API_URL` 和 `CODEX_API_KEY`（Mac 版按 `macos-persistence.md` 配置钥匙串）。
4. 新开会话后用 `$codex-image-gen 生成一张蓝色玻璃瓶的图片` 调用；若技能未出现，重开会话再试。
5. `doctor` 命令可离线检查配置可见性（不联网、不验证额度或模型可用性）。

详细说明见各包的 [通用版安装文档](codex-image-gen-20261121/INSTALL.zh-CN.md) 与 [Mac 版安装文档](codex-image-gen-mac-20261122/INSTALL.zh-CN.md)。

## 目录结构

```
agents-and-skills/
├── README.md
├── LICENSE                              # MIT
├── .gitattributes                       # 二进制/文本文件换行符规则
├── .gitignore                           # 排除运行时产物与本地凭证
├── codex-image-gen-20261121/            # 通用跨平台版发布包
│   ├── INSTALL.zh-CN.md                 # 安装说明
│   ├── VALIDATION.zh-CN.md              # 验证记录
│   ├── manifest.json                    # 文件清单 + SHA-256 + 权限
│   └── codex-image-gen/                 # ← 安装时复制这一层
│       ├── SKILL.md
│       ├── agents/openai.yaml
│       ├── bin/                         # 4 个原生 CLI
│       ├── references/                  # batch-format / runtime
│       └── src/                         # Go 源码与测试
└── codex-image-gen-mac-20261122/        # macOS 专享版发布包
    ├── INSTALL.zh-CN.md
    ├── VALIDATION.zh-CN.md
    ├── manifest.json
    └── codex-image-gen-mac/             # ← 安装时复制这一层
        ├── SKILL.md
        ├── agents/openai.yaml
        ├── bin/                         # 2 个 macOS CLI（codex-image-gen-mac-darwin-*）
        ├── references/                  # batch-format / runtime / macos-persistence
        └── src/                         # Go 源码与测试
```

后续新增技能时沿用同样约定：每个技能一个独立发布包目录，内含安装/验证文档、manifest 和内层技能文件夹；不改动已有包的内层结构。

## 完整性校验

每个发布包的 `manifest.json` 记录了内层技能文件夹所有文件的字节数、权限和 SHA-256。在**发布包目录**下运行：

```bash
python -c "import json,hashlib,pathlib; d=pathlib.Path('.'); m=json.load(open('manifest.json')); bad=[f['path'] for f in m['files'] if not (d/f['path']).exists() or hashlib.sha256((d/f['path']).read_bytes()).hexdigest()!=f['sha256']]; print('OK' if not bad else 'MISMATCH: '+', '.join(bad))"
```

当前状态：通用版 19/19、Mac 版 18/18 文件全部匹配（Mac 版含新增源码）。

## 安全与验证

- 发布包不含真实 API 密钥、个人 API 地址、任务状态或生成历史。密钥仅在本地配置，不写入聊天、命令参数或文件。
- 私有恢复状态写在 `output/<技能名>/.state/`，可能含提示词、参考图字节或签名结果 URL，不要作为公开交付物分享。
- 通用版：58 项 Go 测试通过；9 项离线改名检查通过；四种平台二进制已构建并核对架构，其中仅 macOS arm64 实机运行，其余为交叉编译。
- Mac 版：CLI 已从旧的 `codex-image-pm` 全面改名并用源码重编译，二进制内命名空间与输出目录已核验；frontmatter 与引用无旧命名残留；arm64/x86_64 均为 Mach-O 可执行文件。新二进制未在真实 macOS 设备上运行，仅完成静态核验。
- 验证未发起付费生成请求；模型目录列出不代表每个模型当前可用。
- 完整记录见包内 `VALIDATION.zh-CN.md`。二进制为预编译原生程序，使用前请按上一节自行核对哈希。

## 许可

[MIT](LICENSE) © 2026 Markel
