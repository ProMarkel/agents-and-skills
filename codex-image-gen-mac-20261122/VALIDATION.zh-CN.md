# 验证记录 · Codex ImageGen Mac · 2026-11-22

本次将 macOS 专用技能从 Codex Image PM 更名为 Codex ImageGen Mac（`codex-image-gen-mac`）：CLI 文件名、模块名、默认输出/状态命名空间、文档和 manifest 全部同步更新。CLI 使用通用版源码改名后用 Go 1.27.4 重新编译，非简单重命名。

本次验证：

- 源码从通用版 `codex-image-gen` 复制而来，模块名与全部命名空间字符串改为 `codex-image-gen-mac`（4 处），`go vet ./...` 通过。
- Go 测试在 Windows 上执行：除 `TestNestedRelativeOutputResolvedUnderDirectory` 因测试内硬编码 Unix 路径风格而失败外，其余通过。该失败在**未改名的原始通用版源码**上同样复现，与本次改名无关；源码原本的验证平台为 macOS arm64。
- darwin arm64 与 amd64 两个 Mach-O 二进制使用 `CGO_ENABLED=0 GOOS=darwin -trimpath` 交叉编译生成；已核对 Mach-O 头架构标记（arm64 / x86_64）与构建信息（模块 `codex-image-gen-mac`）。
- 二进制内字符串已核验：包含 `output/codex-image-gen-mac` 与 `codex-image-gen-mac-scope-v1`，旧的 `codex-image-pm` 相关字符串已完全移除。
- `SKILL.md` frontmatter、UI 元数据及 Markdown 内部引用已全量扫描，无旧命名残留。
- 新增 `src/` 目录（11 个源码/测试文件），使技能包自包含、构建可复现；manifest 现覆盖 18 个文件，字节数与 SHA-256 校验通过。
- `models` 网关目录请求与钥匙串配置在 Apple Silicon 上的验证由上一版记录（改名不影响凭证与网络逻辑）。
- 未在制作分享包时发起付费图片生成请求。模型目录成功不保证其中每个模型都能生成。
- 包内不含真实密钥、个人 API URL、任务状态、缓存或历史生成内容。
- ZIP 发布包已核对：CRC 通过，21 个文件与分享目录逐字节 SHA-256 一致，二进制权限位 `0o755` 保留。

注意：本次编译环境与原始包不同（Go 1.27.4 + Windows 交叉编译），新二进制体积较原包增大，属编译器版本差异。仅完成静态核验，未在真实 macOS 设备上运行新二进制。
