# WeKnora Domain Engine 进度

## Phase 1 已审核

基线 `71a86a5748db0d14c06a49f1444b742e7fc8917e`。方案 C 已批准。Flow 1 采用选择性原料提取，保留完整配方表及实验组关系，原件保留，冲突修正为独立 V3.0.1 候选。Flow 2 只读有效完整的 Flow 1 JSON，原文复核独立。真实领域包本地导入，正式 Schema 只落实可确认约束。原生与专业状态分开。

## Phase 2A 已实现

| 文件 | 内容 |
| --- | --- |
| `internal/domainengine/{profile,json,schema_policy}.go` | 有界的单 manifest 导入、2020-12 Schema 编译、通用顺序阶段及依赖指纹、保留草案生成端口 |
| `internal/domainengine/{store,snapshot}.go` | 不可变模板、KB 绑定 CAS、原件与预切片快照、哈希回读及授权删除后的清理端口 |
| `internal/domainengine/{profile_test,store_test}.go`、`testdata/*-synthetic.json` | 两领域合成模板、Schema 正反例及 PostgreSQL 集成 |
| `migrations/versioned/000138_domain_engine.{up,down}.sql` | PostgreSQL 独立 schema 和复合键约束 |
| `internal/application/service/knowledge{,_process,_domain_capture}.go`、`knowledge_domain_capture_test.go` | 原生文件解析捕获、可选覆盖报告、关闭回归及 Unicode 位置测试 |
| `internal/container/container.go`、`internal/router/sync_task_transfer_test.go`、`.env.example` | 默认关闭的 PostgreSQL 装配及原生 SQLite Lite 测试构造兼容 |
| `docs/domain-engine/{ARCHITECTURE,DATA_MODEL,INGESTION_PIPELINE,API_AND_UI,ROADMAP,TEST_PLAN,PROGRESS}.md` | 已批准契约、分期门槛、覆盖、增量重跑及保留政策 |

## 验证

- 通过 `go test -count=1 ./internal/domainengine ./internal/config ./internal/common/redislock ./internal/sourceloc`，共 99 个通过的 Go 测试或子测试。设置 `DOMAIN_ENGINE_TEST_DSN` 使用独立 PostgreSQL 数据库，验证迁移升降、并发幂等、租户隔离、绑定修改、字节和坐标回读、旧尝试及源替换拒绝、原件清理。
- 在临时 ParadeDB 数据库完整执行 PostgreSQL 上行迁移链至 `000138`，确认 `domain_engine.parse_snapshots` 已创建。
- 原生 `TestRouterWiring` 的 Lite/Redis 两种装配，以及 `TestLiteKBCloneEndToEndWithoutRedis` 和 `TestLiteKnowledgeMoveBatchEndToEndWithoutRedis` 通过。原生服务组初测未通过，独立定位复现确认两字符样本不会产生归一化重映射。改用更长的合成段落后，原生服务组定向复测通过，包含 `TestDomainCaptureDisabledUsesOriginalContext`、`TestDomainCapturePreservesFullTextAndCoverage` 和 `TestProcessDocumentSkipsSupersededAttemptWithoutWritingRow`。
- 八份设计文档的相对链接和 `git diff --check` 通过。
- 环境初始回归构建受 Go 模块重定向、磁盘余量及 SQLite 开发头限制。使用上游标签重建受阻归档并比对仓库 `go.sum`，用临时编译空间及锁定依赖内的 SQLite 头文件重试。仓库依赖和锁定文件无需变更。

## 限制与本地验证

- 定向回归覆盖本次改动，未运行完整 Go CI 矩阵及前端套件。分支推送成功，GitHub GraphQL 代理拒绝创建 PR。
- 2A 只保存适配器可读取的原件与预切片快照。捕获失败与原生成功状态独立，持久恢复、用户模板上传、执行器和结果界面列入 2B。解析信号无法证明全文、图表及补充材料完整；2A 不评分模型输入覆盖或科学抽取质量。
- 本地 Claude Code 在 Apple Silicon 验证 PostgreSQL 迁移、被选解析引擎及 PDF、扫描件、表格和补充材料的来源覆盖。到 Phase 3 再用获准的本地 MiniMax 账户验证实际模型、上下文/输出限制、JSON 能力和 usage，并人工抽查真实文献。真实模板、论文和密钥留在本地。
- 待后续阶段完成来源权限回归、模板导入授权、实际分片保真、派生索引授权和备份清理。本次云端未调用模型 API。
