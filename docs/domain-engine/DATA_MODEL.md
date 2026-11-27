# 数据模型与 V3.0 契约

## 附件清单

以下 SHA-256 取自 ZIP 中的原文件。设计 PR 记录指纹和契约，不提交附件正文。发布领域包时原件须逐字节保存于 `original/` 或获准的不可变制品库。DOCX 抽取后的执行文本另存，记录抽取器、哈希与可读差异，禁止覆盖原件。

| 文件 | SHA-256 |
| --- | --- |
| Knowledge_Extractor_V3_0.txt | e001cd887d24f5c08cb9a2de06e2f07e7aa2b22332914d6e88c60427a3d569a3 |
| Knowledge_Extractor_JSON_V3_0.json | 5bd0e9efb2b61e44dd304328da7368ecc28c531bd891c389e6b1fa39ef0eb28a |
| Semantic_Analyzer_Prompt_V3_0.docx | 34ccfc2e30a2b9b41ae755c6a67d23d13f8f7e16236084cf001dadf1f1aed44d |
| Semantic_Analyzer_JSON_V3_0.json | 8e4c67fd8a08a09ef6e293bf5e5b911d65376a5c7e7ece5d29c381dc60eedfba |

Flow 1 Prompt V3.0 的输出契约正文称 V7.1，模板共 18 个根字段。选材规则要求关键原料独立、背景归类，原文第 100 行却要求逐命名原料与表格行对账。保持 V3.0 原件；建议独立 V3.0.1 候选明确保留差异成分与组别，背景归类。需人工确认冲突解释与测试 fixture 后发布执行版。Flow 2 模板共 23 根字段，`paper_id` 与 Flow 1 完全一致，`source_verification_status` 必须为 `analysis_json_only`，默认来源仅完整 Flow 1 JSON。跨文献候选及局部 ID 只作为建议；证据保留 `source_field_paths`，无原文验证声明。

转换交付需逐字段记录 Prompt/模板来源、类型、required、nullability、枚举、数组元素、跨字段条件及未决项。Flow 2 的数值形字符串按原契约保留、不得统一转 number；区分缺项、null、空列表。按明确的字段形状约定约束未知键。示例中的 null 或空数组不能推导唯一类型。使用多份正例与负例测试，跨制品引用另做确定性校验。领域 envelope 放在模型 JSON 外。

## PostgreSQL 逻辑 schema `domain_engine`

所有域内查询带作用域。KB 数据用 `(tenant_id, kb_id, id)` 复合键及外键，模板用租户级复合键；子资源 ID 不构成权限。KB 所属租户来自平台，不信任请求体。根实体关联原生 KB/文档 ID，生命周期由适配器校验与协调。

| 表 | 关键字段、约束与索引 |
| --- | --- |
| profiles / profile_revisions | 租户、模板 ID、修订号、状态、manifest JSONB、制品哈希、作者、父修订。唯一 `(tenant, profile, revision)`。草稿用 ETag，发布后不可编辑。复制有来源，导入再校验 |
| kb_bindings | `(tenant,kb)` 唯一，当前 profile_revision、绑定版本、阶段模型引用、启用状态与隐私预算；保留绑定审计 |
| documents / document_revisions | native document ID、source hash、解析引擎和配置版本、原件引用、当前激活 generation。原生 ID 映射唯一；相同字节但不同解析配置可以有不同修订 |
| artifacts / artifact_inputs | 制品 UUID、kind、SHA-256、大小、媒体类型、对象 key/JSONB、Schema hash、生成者与上游 ID DAG。按 scope+hash 去重数据字节；JSONB 可查询副本，原始 JSON 字节独立保留。原文、解析坐标、图片、原响应、有效 JSON、验证和语义结果分别保存 |
| jobs / stage_runs / outbox | 快照 ID、profile revision、阶段输入/配置 hash、尝试序号、租约 fencing token、due_at、状态、错误码、取消记录；到期索引与 stage_key 唯一约束；事务内追加 outbox |
| generations / records / edges | generation 绑定快照与映射版本，record_type + 局部 ID 在 generation 内唯一。无局部 ID 时从不可变制品及 Pointer 生成。属性 JSONB、受控过滤另建类型化索引。边的两端都约束到同 generation，保存 predicate 和证据引用 |
| evidence / record_evidence | 证据 UUID、来源类别、Flow 2/1 Pointer、原文 artifact/hash、坐标体系及可选页/表/区间、摘录匹配状态；记录或边可关联多个证明或反证。区分原文可核实事实、作者观点、AI 推断、二手分析及未知，不自动提升置信度 |
| concepts / concept_mentions / concept_events / review_items | registry 首期按 tenant+KB+profile lineage；概念 UUID、facet、双语标签、别名、版本，mention 绑定不可变记录和证据。合并/拆分写事件和可撤销重定向，不改过去记录；审核含状态、原因、审核人、并发版本 |
| index_links / usage_events | generation+chunk 逻辑 key 唯一，追踪目标 KB/文档、校验和、索引状态；usage 按 attempt 记输入/输出 token、来源 reported/estimated/unknown、成本和请求标识，重算不得覆盖原账 |

映射示例，仅存在于化妆品包：Flow 1 `materials_and_technologies`、`methodology`、`key_findings` 按各自 ID 映射；`associated_finding_ids` 解析为有方向引用；Flow 2 三种性质数组即使共用 `property_id` 也用不同 record_type。无唯一引用的标签进入审核。术语候选先按授权范围、facet、别名及可选向量召回限定集合，由 AI 提议 exact/new/broader/narrower/related/uncertain，只有批准的合并才改变有效解析。单位原样保存，只有配置允许且量纲一致才增加换算值。

导出包含 profile revision、原件、解析、JSON、引用关系、任务及索引 manifest。对象备份与 PostgreSQL generation 指针按一致性快照恢复并重建索引。正常重跑长期保留旧制品；明确删除请求先撤回授权和投影，再按租户数据政策处理制品和备份。

## 已批准的契约与 Phase 2A 存储

Flow 1 的关键原料、差异原料和直接影响发现的原料单列，背景按类别汇总，完整配方表格和实验组关系保留。修订以独立 V3.0.1 候选保存。真实 Prompt、论文及密钥不进入公开仓库；运行包由用户本地导入。对无法从契约确定的科学约束保留待确认项。

Phase 2A 将版本正文保存为不可变字节，SHA-256 标识内容。同一租户、模板、修订号的冲突内容拒绝写入。KB 绑定使用版本 CAS。快照以租户、KB、文档、解析尝试、绑定版本和内容指纹关联；原件 BYTEA 按文档与 SHA-256 去重，解析快照另存解析与规范化的全文、坐标、覆盖信息及版本。可授权清除参与同一事务，后续扩容可替换存储适配器。

2A 的配置导入为单个 UTF-8 JSON manifest，限制大小、嵌套、阶段数及 Schema 展开量。Schema 要显式声明 2020-12，只支持无循环的本地顶级 `$defs` 引用。分类和映射保存为声明性 JSON，解释器与 Schema 路径交叉校验由投影阶段提供。阶段指纹纳入上游阶段定义，独立于映射编辑。执行时还需加入实际输入、解析版本及模型配置指纹。

读回时校验原件和快照哈希。授权删除后可通过内部存储接口清除原件及级联快照；自动化租户清退、版本保留和备份清理由后续生命周期流程调用，2A 不对外提供历史制品接口。

`000139` 新增 `jobs`、`stage_runs` 和 KB 访问 epoch。任务保存模板、绑定版本、阶段开关、模型指纹和限制，模型密钥从原生模型管理解析。阶段尝试保存 raw bytes、独立有效 JSON、错误、用量与输入覆盖。版本更新保留此前获准的作业，禁用绑定撤销旧 epoch。历史默认保留到源替换、移动、删除或上级作用域删除；数据库事务中的清理失败会回滚，原生操作可重试。备份保留按部署方策略处理。
