# 项目文档

文档按过程文档、项目事实和 spec 三类维护。本文件是统一入口。

| 目录 | 回答的问题 | 维护方式 |
| --- | --- | --- |
| [过程文档](process/README.md) | 一个需求为什么做、如何设计、如何实现和验收？ | 一个需求一个目录，保存该需求的完整过程 |
| [项目事实](facts/README.md) | 项目各模块现在实际是什么样、如何工作？ | 按模块长期维护，随实现变化更新 |
| [spec](spec/README.md) | 系统应该遵守哪些行为、接口和数据约定？ | 按规范主题维护，变更时明确兼容性和生效状态 |

## 目录约定

```text
docs/
  README.md
  process/
    README.md
    <需求名>/
      README.md
      ...调研、设计、实施、验收记录及该需求的附件
  facts/
    README.md
    <模块名>.md
  spec/
    README.md
    <规范主题>.md
  images/                 # 多处文档共用的图片
```

需求目录使用可读的英文短名，例如 `historical-reconstruction`。一个需求的多轮修改、CR 和验收放在同一目录，避免每轮对话都在 docs 根目录新建文件。简单需求只需一个 README，按实际需要增加文件，不要求固定文档套件。

项目事实以模块为单位，例如桌面端、采集、同步、服务端统计、账号与设备、网站、构建发布。默认一模块一文件，内容确实复杂时再建模块目录。spec 以行为或契约为单位，例如事件协议、统计口径、事件准入、设备绑定与版本兼容。

## 内容边界与更新规则

- **过程文档记录决策经过。** 包括问题、调研证据、方案取舍、实现进度、CR 和验证结果；不能仅凭某份已完成的方案推断它已经实现或上线。
- **项目事实记录当前实现。** 写明模块职责、代码入口、主要链路、数据存储、运行方式和已知限制，并关联代码与验证依据。源码已实现、本机已验证、正式环境已部署必须区分；不确定的状态明确标注。
- **spec 记录要求。** 描述字段含义、统计规则、接口行为、幂等与兼容性、异常处理及验收条件。标明草案、已确认或已废弃，并说明适用范围；已确认不等于已经实现。
- 需求完成时，保留过程记录，将最终已实现行为更新到相关项目事实；涉及契约变化时同时更新 spec，并在三者之间建立链接。
- 同一条规则只在对应 spec 中完整定义，项目事实和过程文档引用它；同一模块现状只在对应事实文档中维护，避免复制出多份互相冲突的说明。
- facts 和 spec 使用稳定文件名，历史由 Git 保存。只有确实需要并存支持的协议版本才分版本维护，不因每次编辑递增文件名中的 v1/v2/v3。
- 各目录 README 维护自己的索引。只收录可安全提交的内容，不保存账号凭据、原始会话、本机私人用量或测试数据库副本。

## 现有文档过渡入口

以下链接是现有资料索引，**尚未逐篇完成现行性核对和迁移，不能直接当成已确认的事实或 spec**。整理时先检查内容，再按上述职责归位；混合文档需要提炼事实或规范，原始过程仍归对应需求。移动文件时同步修复引用。

| 主题 | 现有资料 |
| --- | --- |
| 开发与发布 | [开发构建](development.md)、[桌面发布](desktop-release-publishing.md)、[更新机制](desktop-updates.md)、[macOS 方案](macos-technical-design.md) |
| 采集与同步重构 | [完整技术方案](event-pipeline-refactor-technical-plan-v1.md)、[采集架构](collector-plugin-architecture-and-acceptance.md)、[数据读取与渲染](collector-data-rendering.md) |
| 历史重建与修复 | [重建和设备归属](reconstruction-and-device-ownership-0.1.27.md)、[吞吐修复](collection-throughput-fixes.md)、[Cursor 修复](cursor-collection-fix.md) |
| 数据与统计约定 | [统计计量契约](metric-contract-v1.md)、[时间准入](event-time-admission-v1.md)、[客户端 DDL](event-pipeline-ddl-v3.md)、[服务端 DDL](event-pipeline-server-ddl-v1.md) |
| 用户与设备 | [产品方案](tokendance-user-product-spec-v1.md)、[用户系统设计](tokendance-user-system-technical-design.md)、[验收矩阵](user-system-acceptance-test-matrix.md) |
| 桌面界面 | [悬浮球设计](tokendance-floating-orb-design-v2.md)、[悬浮球技术方案](tokendance-floating-orb-technical-design-v1.md)、[运动规则](tokendance-orb-motion.md)、[下载页设计](download-and-docs-ui-design.md) |

根目录旧文档、`ddl/`、`ui-prototypes/` 和辅助脚本暂保留原路径。需求专属原型和截图随需求归档；共用图片留在 `images/`；可执行生成与验证脚本后续整理到 `tools/`，文档只保留使用说明和引用。
