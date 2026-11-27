# 实施路线图

以下新增文件为具体落点建议。迁移编号按实际落地基线分配，接口见 [API](API_AND_UI.md)，用例见 [测试](TEST_PLAN.md)。每阶段独立 PR，通过回归后再默认启用后续能力。

| 阶段 | 优先级与依赖 | 源码、接口与测试 | 验收及责任 |
| --- | --- | --- | --- |
| P0，环境实证 | P0，与 Phase 1 并行 | 只读核对 `docker-compose.yml`、`docker-compose.dev.yml`、`docker/Dockerfile.docreader`、`internal/models/providers/minimax.go`；产出 OS/架构、镜像 digest、模型限额、是否支持受控 JSON/usage 的脱敏记录 | 本地 Claude Code 在 Apple Silicon 运行原版，验证 PDF/扫描、提供商认证与可用嵌入。云端只用 Mock |
| P1，快照与领域包 | P0，设计 PR 审核后 | 新增 `internal/domainengine/{profile,artifact,contracts}.go` 和 `profile_test.go`，`migrations/versioned/<next>_domain_engine.{up,down}.sql`。修改 `knowledge_create.go`、`knowledge_process.go` 与 `internal/container/container.go` 走可选适配器，新增 `knowledge_domain_capture_test.go`，输出 SerializeParseSnapshot 接口。领域包在 `domain-profiles/cosmetic-v3/` 与 `domain-profiles/automotive-fixture/` | Codex Cloud 验证 native-only 无写入、同一解析只捕获一次、原件/hash 可恢复，两个包由同一编译器加载。本地确认附件存放与冲突解释 |
| P2，两阶段执行与任务 | P0，依赖 P1 | 新增 `internal/domainengine/{pipeline,worker,outbox,model_port}.go`，扩展 `internal/types/task.go`、`internal/router/task.go`，根据能力门决定 `sync_task.go` 无操作或注册。新增 `internal/domainengine/worker_test.go`、`recovery_test.go`、`internal/handler/domain_jobs.go`、`internal/router/routes_domain.go`。接口 RunStage、RetryStage、CancelJob，默认无原文的 Flow 2 | Codex Cloud 用 Mock 完成单次上传、跨重启从 checkpoint 恢复、重复投递、旧 worker/取消、token 账本。Claude Code 验证模型参数与产品限额，密钥只在本地 |
| P3，记录、证据与模板体验 | P1，依赖 P2 | 新增 `internal/domainengine/{projection,evidence,registry,query}.go` 及同名测试，`internal/handler/{domain_profiles,domain_records}.go`；新增 `frontend/src/api/domain-engine/index.ts`、`frontend/src/views/knowledge/domain/{ProfileStudio,DomainJobs,KnowledgeExplorer}.vue`，修改 `KnowledgeBaseEditorModal.vue` 与 `KnowledgeBase.vue`；新增 schema/mapping 测试、租户权限路由测试、Vue 表单及探索测试 | Codex Cloud 完成两套互不相似字段的动态界面及审核、精确统计、导入/导出。Claude Code 做适量真实文献证据抽查 |
| P4，检索、图与 Agent | P1，依赖 P3 以及派生 KB 权限证明 | 新增 `internal/domainengine/{index_adapter,graph,agent_query}.go` 与测试，连接 `internal/application/service/knowledge_create.go` 的 passage/manual 路径；在 `session_agent_qa.go` 的 Agent 配置入口接入类型化工具，至少覆盖 `routes_domain_test.go`、`agent_query_test.go`、`index_adapter_test.go`。新增 `frontend/src/views/knowledge/domain/DomainGraph.vue` | Codex Cloud 测切换、删除/撤权、重复索引、召回过滤、引用去重和精确查询；达不到隔离时保持仅 Explorer。Claude Code 验证小样本召回与引用 |
| P5，交付 | P2，依赖 P4 | 新增 `tests/domain_engine/` 的运行/恢复与导出回放测试及管理员指南，审视 `.github/workflows/app.yml` 与 `frontend.yml` 的实际覆盖 | Codex Cloud 跑全链合成 CI；本地做 Apple Silicon 端到端、延时与预算基准、备份恢复。目标指标由 P0 测量后确认 |

P1 的快照试验要确认原生保存边界能关联不可变源版本，以及能从持久记录找回未派发的任务。若只有重复解析能恢复，记录触发场景和成本，交由 ADR 审核。P4 前需要证明受管派生物在 native API、全局搜索、共享 Agent、文件代理和导出均有正确授权。源文档删除后，撤回派生索引是发布门槛。
