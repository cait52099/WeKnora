# WeKnora 开发规范

- 专业知识引擎的设计与验收入口见 [Phase 1 进度](docs/domain-engine/PROGRESS.md) 和 [路线图](docs/domain-engine/ROADMAP.md)。设计审核前保持 Phase 1 文档范围。
- 用当前提交的源码、CI、迁移和锁文件验证平台契约。修改原生入口前，检查申请范围、现有调用者、权限、重试及源版本保护。遵循更深目录的现有规范，例如 `cli/AGENTS.md`。
- 保留原生 KB 入口及默认行为。领域字段只在配置、映射和 fixture 中定义。保护原始 Prompt、JSON 示例、原文及已发布修订，修改以新版本交付。
- 云端自动化测试使用 Mock LLM 和合成数据。在本地用授权账号验证模型与 Apple Silicon。日志、测试制品和提交中禁止写入密钥或保密论文。
- Go 修改使用 gofmt 和定向测试。Vue 修改使用 `npm ci` 并执行相关测试、类型检查和构建。数据库改动明确支持范围、迁移和恢复验证。
