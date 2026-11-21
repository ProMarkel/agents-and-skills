# agents-and-skills

> A personal collection of agents, skills and prompt collections I created or refined — Codex skills with bundled native CLIs, WorkBuddy skills, and curated prompt references.

个人创建与优化的 agents / skills 集合，用于公开分享。包含三类内容：带原生 CLI 的 Codex 技能、纯文本 WorkBuddy 技能、以及整理自公开文章的提示词参考。前者开箱即用，运行时不需要 Python / Node / Go。

## 内容一览

| 分类 | 内容 | 位置 |
| --- | --- | --- |
| Codex 技能（带原生 CLI） | 2 个图片生成技能 | 根目录发布包 + `packages/` ZIP |
| WorkBuddy 技能（纯文本） | 3 个技能 | [`workbuddy-skills/`](workbuddy-skills/) |
| 提示词参考 | 2 份整理文档 | [`prompts/`](prompts/) |

## Codex 技能

| 技能 | 调用名 | 平台 | CLI 文件名 | 凭证方式 | 发布 |
| --- | --- | --- | --- | --- | --- |
| Codex ImageGen | `$codex-image-gen` | macOS (arm64/amd64)、Windows (amd64/arm64) | `codex-image-gen-*` | 环境变量；macOS GUI 会话兼容 | 2026-11-21 |
| Codex ImageGen Mac | `$codex-image-gen-mac` | macOS (arm64/amd64) | `codex-image-gen-mac-*` | Keychain + `~/.zprofile`，zsh login shell | 2026-11-22 |

两个技能互不依赖，可同时安装共存。核心逻辑相同，差异在平台覆盖与凭证持久化方式；默认输出目录分别为 `output/codex-image-gen/` 与 `output/codex-image-gen-mac/`，均与技能名一致：

- 需要 Windows，或偏好临时环境变量配置 → 用通用版
- 只在 mac 上使用，希望密钥存入钥匙串免重复配置 → 用 Mac 版

两者都通过兼容的图片 API 网关工作：读取 `CODEX_API_URL` 与 `CODEX_API_KEY`，支持模型自动选择、批量生成和失败恢复。未配置 URL 时不会静默切换，需用户明确选择是否使用内置预设地址。

## WorkBuddy 技能

三个纯文本技能，无二进制、无外部依赖（除脚本注明者），把整个技能文件夹复制到 WorkBuddy 技能目录即可。

| 技能 | 用途 | 组成 | 依赖 |
| --- | --- | --- | --- |
| [course-guide-builder](workbuddy-skills/course-guide-builder/) | 根据视频课程链接生成交互式 HTML 学习指南（模块标签、可展开课时卡、B 站嵌入、测验与练习、嵌入模式） | SKILL.md + HTML 模板 | 无 |
| [pdf-add-page-numbers](workbuddy-skills/pdf-add-page-numbers/) | 给 PDF 加 `X / Y` 格式页码，输出 `<原名>_有页码.pdf` | SKILL.md + Python 脚本 | `pypdf`、`reportlab`（首次运行自动安装） |
| [resume-optimizer](workbuddy-skills/resume-optimizer/) | 中文简历五维评分、40+ 项审查、精修与 JD 定制、导出 A4 PDF | SKILL.md + 评分标准 + HTML 模板 + 导出脚本 | 导出 PDF 需系统 Edge |

- `course-guide-builder` 的 SKILL.md 含完整 CSS/JS 模式与 B 站嵌入要点，模板文件是唯一起点；支持 `?embed=1` 嵌入模式。
- `resume-optimizer` 的评分标准、模板示例中的真实项目名、流水数字与个人履历已全部替换为通用占位符，保留方法论与结构。技能不会编造公司、项目或数据。

## 提示词参考

[`prompts/`](prompts/) 下两份文档整理自公开微信公众号文章，属于**二次整理的资料**，非本人原创技能：

| 文档 | 内容 | 来源 |
| --- | --- | --- |
| `workbuddy-100plus-prompts.md` | 12 大类、约 130 条 WorkBuddy 通用提示词速查 | 文档头部注明 |
| `workbuddy-prompts-collection.md` | 50+ 指令模板 + agency-agents 211 个角色提示词库 | 文档头部列有原文与备份链接 |

转载内容请遵循原作者的使用与授权约定；此处仅作个人整理存档与速查。

## ZIP 发布包

不想 clone 整个仓库的话，可直接下载单个 Codex 技能的 ZIP（解压后为带日期的发布包目录，结构同下）：

- [codex-image-gen-20261121.zip](packages/codex-image-gen-20261121.zip) — 通用跨平台版，11.7 MB
- [codex-image-gen-mac-20261122.zip](packages/codex-image-gen-mac-20261122.zip) — macOS 专享版，11.8 MB

ZIP 保留 Unix 权限位，`bin/` 下原生 CLI 解压后即可执行，无需 `chmod`。内容、权限和 SHA-256 与仓库内发布包目录逐文件一致。

WorkBuddy 技能与提示词参考是纯文本，直接浏览或下载对应目录文件即可，无需 ZIP。

## 快速开始

### Codex 技能

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

### WorkBuddy 技能

把 `workbuddy-skills/` 下对应技能的**整个文件夹**（含 `assets/`、`scripts/`、`references/`）复制到 WorkBuddy 用户技能目录，例如 `~/.workbuddy/skills/<技能名>/`。三个技能互不依赖，复制后重开会话即可按 SKILL.md 中的 description 触发。脚本类技能首次运行会按需安装 Python 依赖。

## 目录结构

```
agents-and-skills/
├── README.md
├── LICENSE                              # MIT
├── .gitattributes                       # 二进制/文本文件换行符规则
├── .gitignore                           # 排除运行时产物与本地凭证
├── packages/                            # 两个 Codex 技能的 ZIP 发布包
├── workbuddy-skills/                    # 三个纯文本 WorkBuddy 技能
│   ├── course-guide-builder/            # SKILL.md + assets/HTML 模板
│   ├── pdf-add-page-numbers/            # SKILL.md + scripts/Python
│   └── resume-optimizer/                # SKILL.md + assets/ + references/ + scripts/
├── prompts/                             # 两份提示词整理文档（转自公众号）
│   ├── workbuddy-100plus-prompts.md
│   └── workbuddy-prompts-collection.md
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

后续新增 Codex 技能时沿用同样约定：每个技能一个独立发布包目录，内含安装/验证文档、manifest 和内层技能文件夹；不改动已有包的内层结构。WorkBuddy 技能按 `workbuddy-skills/<技能名>/` 放置，发布前去除 `_user_meta.json` 等本地安装元数据。

## 完整性校验

每个 Codex 发布包的 `manifest.json` 记录了内层技能文件夹所有文件的字节数、权限和 SHA-256。在**发布包目录**下运行：

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
- `resume-optimizer`：评分标准与模板中的真实项目名、流水数字、年限、团队规模及个人偏好表述已全部替换为通用占位符；方法论、清单与结构完整保留。
- 提示词参考为公开文章的二次整理，来源已在文档头部与上文注明，使用前请遵循原作者授权约定。
- 完整记录见 Codex 包内 `VALIDATION.zh-CN.md`。二进制为预编译原生程序，使用前请按上一节自行核对哈希。

## 许可

[MIT](LICENSE) © 2026 Markel
