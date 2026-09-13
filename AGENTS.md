# 部署规则

- 测试服务使用独立的 `release` 分支，从干净的 `release` 工作区构建发布；允许包含尚未合入 main、已经完成 CR 和功能测试的改动。测试功能改完后立即发布测试服务，无需用户再次要求。
- 生产环境和桌面端正式发布仅允许部署 `main`：发布前拉取远端，确认改动已合入 `origin/main`，构建时 `HEAD` 必须等于 `origin/main`。
- 每次发布记录实际构建分支和完整提交 SHA。测试服务使用独立服务、配置及 `tokendance_dev`，不得覆盖生产实例或连接 `tokendance_prod`。
- 新建表必须包含：`id`（BIGINT UNSIGNED AUTO_INCREMENT，表主键）、`created_at` / `updated_at`（BIGINT UNSIGNED，UTC 毫秒）、`delete_at`（BIGINT UNSIGNED NULL，软删除）、`extra`（JSON NOT NULL DEFAULT JSON_OBJECT()）。业务身份用独立 UNIQUE，不把 `id` 当同步水位。读路径过滤 `delete_at IS NULL`。`extra` 不放必需业务字段、任务状态、凭据或事件正文。现有表不按此规则回填。
- 新建表每个字段都必须有 `COMMENT`，说明含义、单位和空值语义；新增列同样要有 COMMENT。
- 通用表设计原则：不要无脑平铺字段。同一实体属性下的信息，如果不需要频繁独立查询、筛选、索引或数据库聚合，优先放入有明确业务含义的 JSON 字段，并定义结构、类型、单位、缺失值及更新语义；主外键、唯一约束、常用过滤/排序/分组与高频数值汇总字段保留独立列。必需业务 JSON 使用专用字段，不塞进 `extra`。该原则适用于所有表设计，不仅是团队统计表。
