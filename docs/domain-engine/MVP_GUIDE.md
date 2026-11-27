# Phase 2B 本地操作指南

在 Phase 0 已验证的标准开发环境中使用 Go 后端、Vue、PostgreSQL、Redis 和 DocReader。先备份数据库，再在本地 `.env` 设置 `DOMAIN_ENGINE_ENABLED=true` 并确认 app 的 `REDIS_ADDR` 和 PostgreSQL 配置。启动时迁移至 `000139`。自管迁移部署应使用现有迁移方式。SQLite/Lite 不开启该功能。

```bash
make dev-start DEV_ARGS=--no-langfuse
# 分别在两个终端中启动
make dev-app
make dev-frontend
```

1. 登录为所在工作区管理员。为本地合成测试注册已授权的 Mock 或兼容聊天模型，并记录各阶段模型 ID。Phase 3 经授权后再使用真实 MiniMax 账户和资料。
2. 打开一个文档知识库，选择 **Domain engine → Profiles & configuration**。导入 `internal/domainengine/testdata/` 下任一合成 manifest，或创建新草稿。在 **Stage editor** 分别设定模型、Prompt 与正式 JSON Schema。TXT/DOCX 导入显示抽取后可编辑文字。JSON 输出示例可预览，正式 Schema 仍需由用户审定。
3. 校验并发布。编辑已发布配置时修改 manifest 的 revision，复制时为新模板填写新的 ID。选择已发布修订并开启 **Automatic processing**，保存绑定。
4. 通过知识库原生上传按钮上传一份合成 TXT 或 Markdown。无需再次提交阶段。到 **Processing** 查看状态和各段使用量，可预览原文、解析报告、原始模型输出和有效 JSON，并下载这些制品。
5. 对已捕获阶段失败选择 **Retry**，程序从该阶段恢复。捕获失败的 **Retry** 会调用原生重新解析。换用不同绑定版本后可选择 **Run current binding**，从共享快照及仍有效的连续阶段前缀恢复。模型配置指纹变化需发布或重新保存新绑定版本再重跑。已经在运行的旧版本任务继续用已锁定配置，关闭绑定会撤销老版本访问及运行资格。

测试数据始终使用合成内容。后端集成测试对 `weknora_domain_*_test` 专用数据库创建随测试删除的表和 schema。可选 Redis 必须是专用空实例。使用本地安全配置注入数据库连接，不在命令历史或提交中保存真实密钥。

```bash
go test -count=1 ./internal/domainengine
go test ./internal/application/service ./internal/router ./internal/container \
  -run 'Test(Domain|RouterWiring|LiteKnowledgeMoveBatchEndToEnd|LiteKBCloneEndToEnd|ProcessDocumentSkipsSupersededAttempt)'
npm --prefix frontend ci
npm --prefix frontend test
npm --prefix frontend run type-check
npm --prefix frontend run build
```

设置 `DOMAIN_ENGINE_TEST_DSN` 启用 PostgreSQL 集成，另设 `DOMAIN_ENGINE_TEST_REDIS_ADDR` 启用真实 Redis 消息删除和重新派发检查。生产备份需遵循工作区删除与保留政策。源删除、替换、移动、KB 删除和租户删除会立即清理域数据库记录。禁用绑定撤销访问并取消尚未完成的任务，历史字节保留至原生清理。图片保留引用和来源位置，独立图片归档和科学视觉解析需要后续开发。
