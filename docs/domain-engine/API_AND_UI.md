# API 与 Vue 设计

接口均为拟新增，前缀 `/api/v1`。使用原平台认证与租户上下文。分页游标绑定 filter 和 generation，错误响应保留原生 envelope，并附 machine code、trace ID 或受限的 JSON Pointer。写操作以 `If-Match` 管理并发，冲突返回 409/412。异步变更返回 202 和可查询 ID；重试请求支持作用域内 Idempotency-Key。未知或无权的子资源遵循上游防枚举语义。

| 方法与路径 | 契约与权限 |
| --- | --- |
| GET/POST `/domain-profiles`，GET/PATCH `/domain-profiles/:id` | 列出作用域内模板、创建或改草稿。模板管理依赖管理员权限；KB 编辑者可从允许绑定的已发布版本中选择 |
| POST `/domain-profiles/import`，GET `/domain-profiles/:id/revisions/:rev/export` | 带校验和的版本包；限制文件数、解压大小、路径与 Schema 引用；导出保留原件、排除密钥 |
| POST `/domain-profiles/:id/{clone,validate,test,publish}` | 复制为新草稿，验证回传字段诊断；测试带 fixture、模型及成本上限，测试只写隔离制品；发布固定 revision 和 manifest hash |
| GET/PUT `/knowledge-bases/:id/domain/binding` | get 或绑定发布版本，包含每阶段授权 model_id 和可选映射状态；owner/admin + KB write。回滚是切换已发布 revision，现有结果由重跑计划明确迁移 |
| GET `/knowledge-bases/:id/domain/{jobs,records,graph,reviews}` | 当前租户和 KB 的分页结果，带源版本、支持的映射字段、运行/质量状态。读镜像、计数和统计权限跟随 KB read |
| GET `/knowledge-bases/:id/domain/jobs/:jobID`，POST 同路径加 `/{retry,pause,resume,cancel}` | 阶段时间线、重试次数、token 来源；动作按当前授权且 scope 再校验，重试从成功 checkpoint 恢复 |
| POST `/knowledge-bases/:id/domain/reprocess/{preview,apply}` | preview 固定受影响源版本及模型预算、最长有效期和输入哈希；apply 领取一次，条件变化提示重做预览 |
| GET `/knowledge-bases/:id/domain/{records/:recordID,evidence/:evidenceID,artifacts/:artifactID}` | 记录及证据详情；工件内容下载继承原件权限，永不返回对象存储裸 key |
| POST `/knowledge-bases/:id/domain/reviews/:reviewID/resolve` | 决议、审核人、版本和原因，保持原证据。接受概念合并或证据修订需审核授权 |
| POST `/knowledge-bases/:id/domain/query` | `record_type, filters, select, group_by, aggregate, limit`，字段来自编译配置，查询使用参数绑定，返回口径、generation 与来源。Agent 调同一服务，权限取用户、Agent 范围和 KB grant 交集 |
| POST `/knowledge-bases/:id/domain/exports` | 生成带 manifest 的可审计导出，按照原件下载与敏感配置权限，短时授权下载 |

默认关闭 API Key 专业接口，待明确能力及范围后开放。所有涉及派生物、聚合、导出和异步执行的权限都要校验来源 KB。导出与下载与原生原件权限保持一致。

Vue 复用原生文件/拖放入口，KB 设置增加可用的领域配置与阶段模型绑定。原生视图展示原生解析状态，专业视图展示报告、重试、暂停/恢复和证据质量。Profile Studio 提供 Prompt、模板实例、Schema、分类、映射与模型编辑，测试和发布分开，显示版本差异及影响，AI 辅助只生成可编辑草案。Explorer 从编译后的受控配置得到列、排序、筛选、类型及证据卡。映射缺失时显示数据形状和对应状态，不隐式猜测科学字段。图视图可点击边跳到证据，RAG 引用应区分原文、Flow 衍生与推断。大输出按页预览，内容转义，下载先授权。新增视图含国际化键、键盘可达性、加载、空态、错误及取消反馈。

## 分阶段交付

Phase 2A 提供内部配置校验与持久层；Phase 2B 才发布本地领域模板导入、Stage 配置、任务状态与 JSON 预览接口及基础 UI。界面区分解析覆盖、模型实际输入、抽取质量及结构化记录/检索/图谱的就绪状态。简易模式通过可选草案生成接口接入，编辑、审核及发布前的校验与手动模式一致。

2A Go 接口提供 `ImportProfile(io.Reader)`、分阶段 `ValidateOutput` 与 `StageFingerprints`，以及发布、读取和绑定版本的持久层。该接口只能由已完成授权的后端代码调用。浏览器、CLI 与 Agent 均不直连该持久层。

## Phase 2B 实际接口

工作区会话管理员可列出及读取发布修订，调用 `/domain-profiles/{validate,publish}` 和 `/domain-profiles/preview/{txt,docx,example}`。草稿编辑、复制和文件导出在浏览器完成。绑定和任务端点位于 `/knowledge-bases/:id/domain`，沿用 KB owner/admin 与 write grant。任务提供 `/jobs/:job/{runs,parse,retry,cancel,reprocess}` 和 `/runs/:attempt/{raw,valid}`。源文档预览使用现有知识详情权限。操作步骤见 [MVP 指南](MVP_GUIDE.md)。
