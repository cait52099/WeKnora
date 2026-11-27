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

## Phase 2B 交付

基线 `96a3f7f52cde041c98af840f068dcd14bc95e56c` 包含已审核的 Phase 2A 提交 `81fb4ab31e4c0871da84387440c1a74a5fa5154a`。本分支包含以下改动。

| 文件 | 内容 |
| --- | --- |
| `internal/domainengine/{job,worker,partition,semantic,reprocess,results,import_preview}.go` | 提交意图、任务队列、恢复与 fence、有界重试、分片覆盖、语义规则与前缀复用、产物读取及导入预览 |
| `migrations/versioned/000139_domain_jobs.{up,down}.sql` | 持久任务、阶段记录、访问 epoch 和原生生命周期清理 |
| `internal/application/service/{knowledge_create,knowledge_domain_capture,domain_models}.go` | 上传、预切片和原生模型端口 |
| `internal/router/{router,task,routes_domain}.go`、`internal/container/container.go` | 专用 Asynq 队列、唤醒恢复及经授权的域路由 |
| `frontend/src/views/knowledge/KnowledgeBase.vue`、`domain/DomainStudio.vue` | KB 配置、模板 Studio、阶段任务、解析覆盖和文件预览及下载 |
| `docs/domain-engine/`、`.env.example` | 更新部署开关、设计、验收与 [本地操作指南](MVP_GUIDE.md) |

### 实际验证

- 定向 Go 执行共 **114 个通过的测试或子测试**，包括含真实 PostgreSQL 与专用 Redis 的 106 项，以及原生解析、容器装配和 Lite 复制/移动的 8 项。
- 在两套合成模板中验证入队意图至解析快照、前后阶段执行、Flow 2 输入隔离、任务锁定、重试调用数、用量、增量重跑、取消和跨 KB 隔离。实际删除一个已入 Redis 的 Asynq 任务后验证重发。注入数据库清理错误并验证回滚及重试。模型调用均使用 Mock。
- 临时 ParadeDB 上行迁移链完整通过至 `000139`，定向集成也验证域迁移回滚。两个变更 Vue 单文件组件的脚本与模板通过 Vue 编译器。
- 首次 PostgreSQL 测试发现 advisory lock 的参数被推断为 text，显式 bigint 转换后通过。并行开发期间启动的原生构建遇到新文件未纳入的编译错误，冻结源文件后重跑通过。最终联合复测一度因 PostgreSQL 临时文件空间不足中止，清理可重建的 Go 缓存后重跑通过。

### 未验证与已知边界

- 云代理对锁定的 SheetJS tarball 返回 HTTP 403，`npm ci` 未完成。因此未运行全量前端测试、类型检查、Vite 构建或浏览器流程，也没有实机界面截图。未运行全量 Go CI 矩阵。云端集成从原生行及快照接口开始，未通过浏览器上传贯通整个栈。
- 现有文件保存与领域意图登记是两个提交，提交意图已落库之后可恢复丢失的唤醒。原生行创建后、意图入库前的进程故障仍可能只留下原生记录。域模型调用一旦发送，随后撤权/取消会隔离晚到结果，供应商已发生的计费可能不可见。
- 默认保留仍在授权范围内的历史制品，清理跟随原生源生命周期。独立图片归档、真实视觉覆盖、定期备份清理及域级专业索引不在此阶段。跨 KB 移动会清除旧域数据，目的 KB 需要按新绑定处理。
- 本地 Claude Code 按 [指南](MVP_GUIDE.md) 验证原生一次上传的浏览器流程、Apple Silicon 和 Compose，然后在 Phase 3 验证 MiniMax 实际上下文、结构化输出与用量，用授权文献人工复核位置和科学结果。真实 Prompt、论文和密钥留在本地。
