# 实施路线图

| 阶段 | 优先级及依赖 | 文件、接口、测试与验收 | 责任 |
| --- | --- | --- | --- |
| 2A | P0，基于已审核设计 | 新增 `internal/domainengine/`、PostgreSQL migration、预切片适配器、两个合成模板；编译可配置 Stage 与 Schema，版本、绑定、完整快照与关闭回归测试 | Codex Cloud |
| 2B | P0，依赖 2A | `internal/domainengine/{worker,outbox,model_port}.go`、`internal/router/{task,routes_domain}.go`、`internal/handler/domain_jobs.go`、`frontend/src/views/knowledge/domain/`。本地模板导入、基本配置、按阶段恢复、状态及 JSON 结果；Mock 一次上传两阶段、断点恢复、覆盖和用量 | Codex Cloud |
| 3 | P0，依赖 2B | 验证 `internal/models/providers/minimax.go`、`docker/Dockerfile.docreader` 与本地 CPU/镜像实际能力；真实样本与人工来源核对。账户、额度、结构化响应、usage、ARM64 和导入边界通过方可推广 | 本地 Claude Code |
| 4A | P1，依赖 2B/3 | `internal/domainengine/{projection,evidence,registry,query}.go`、`internal/handler/domain_records.go`、`KnowledgeExplorer.vue`。用两领域验证归一、审核、证据、精确统计及权限 | Codex Cloud，真实质量由本地抽查 |
| 4B | P1，依赖 4A 及派生授权证明 | `internal/domainengine/{index_adapter,graph,agent_query}.go`、`session_agent_qa.go`、`DomainGraph.vue`，对接 Wiki。验收原文、专业语义和精确查询联合使用，三类数据独立就绪，撤权与删除同步收敛 | Codex Cloud，真实召回由本地验证 |
| 5 | P2，依赖 4B | `tests/domain_engine/`、管理员与恢复说明、CI。批量吞吐、预算、备份恢复与正式交付验证 | 云端压力及恢复演练，本地端到端与性能基准 |

所有后续改动遵循 [测试计划](TEST_PLAN.md)。2A 不新增任务队列、领域 API 或 UI，不改变部署拓扑。
