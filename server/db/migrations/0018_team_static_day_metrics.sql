-- Team static day metrics + stable usage contributors.
-- DROP invite-join membership FK so leave can DELETE current memberships.

ALTER TABLE team_invite_link_joins
  DROP FOREIGN KEY fk_link_joins_membership;

CREATE TABLE team_usage_contributors (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '代理主键；不作同步水位',
  created_at BIGINT UNSIGNED NOT NULL COMMENT '创建时的UTC毫秒；重试不改',
  updated_at BIGINT UNSIGNED NOT NULL COMMENT '最近实际变更UTC毫秒',
  delete_at BIGINT UNSIGNED NULL COMMENT '软删除UTC毫秒；NULL为有效；隐私清理可硬删',
  extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不承载身份、状态、凭据或事件正文',
  team_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '所属团队ID',
  user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '内部用户关联；仅用于重入去重、删除和授权，不对外暴露历史身份',
  contributor_key CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '首次加入时生成tco_加26位随机标识；同队重入复用，不由membership派生',
  membership_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '当前成员关系ID；离队为NULL，不保留旧任期引用',
  retired_at BIGINT UNSIGNED NULL COMMENT '退出冻结时间UTC毫秒；在队为NULL，不作为joined_at统计截断',
  PRIMARY KEY (id),
  UNIQUE KEY uk_tuc_team_user (team_id, user_id),
  UNIQUE KEY uk_tuc_contributor (contributor_key),
  UNIQUE KEY uk_tuc_membership (membership_id),
  KEY idx_tuc_user (user_id, delete_at),
  KEY idx_tuc_team (team_id, delete_at),
  CONSTRAINT fk_tuc_team FOREIGN KEY (team_id) REFERENCES teams(team_id),
  CHECK ((membership_id IS NULL AND retired_at IS NOT NULL)
      OR (membership_id IS NOT NULL AND retired_at IS NULL)),
  CHECK (JSON_TYPE(extra) = 'OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin
COMMENT='团队稳定统计身份；支持退出保留、重入覆盖与隐私删除，不是任务队列';

CREATE TABLE team_member_day_metrics (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '代理主键；不作为同步或消费水位',
  created_at BIGINT UNSIGNED NOT NULL COMMENT '首次创建UTC毫秒；覆盖不改',
  updated_at BIGINT UNSIGNED NOT NULL COMMENT '最近实际更新UTC毫秒；无变化重试不刷新',
  delete_at BIGINT UNSIGNED NULL COMMENT '软删除UTC毫秒；NULL为有效；范围覆盖清除旧行时设置',
  extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务属性、任务状态或事件正文',
  team_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '所属团队ID；必须与contributor所属团队一致',
  contributor_key CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '稳定统计身份；关联内部身份表，重入不改变',
  metric_date DATE NOT NULL COMMENT '日历标签；v2为团队本地日，旧日汇总保留来源日历并由quality标识',
  row_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '规范维度元组SHA256；见静态方案第17节；不含统计值',
  metric_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT 'usage/activity/cost/skill事实族；防止跨模型币种拆分重复计数',
  agent_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '工具标识；NULL表示未知，不与空串混用',
  provider_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '模型提供商；活动事实族为NULL，未知为NULL',
  model_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '模型标识；活动事实族为NULL，不猜测归属',
  currency VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT 'cost行的真实币种；usage/activity为NULL',
  token_exact_total DECIMAL(30,0) NOT NULL DEFAULT 0 COMMENT 'usage行可信exact Token整数；其他事实族为0',
  token_derived_total DECIMAL(30,0) NOT NULL DEFAULT 0 COMMENT 'usage行derived Token整数；不含estimated',
  usage_event_count DECIMAL(30,0) NOT NULL DEFAULT 0 COMMENT 'usage事实数；旧汇总无法推断时为0并由quality说明',
  reported_cost_amount DECIMAL(30,8) NOT NULL DEFAULT 0 COMMENT 'cost行已记录费用；以currency计价，其他族为0',
  estimated_cost_amount DECIMAL(30,8) NOT NULL DEFAULT 0 COMMENT 'cost行尚未被账单替换的估算；不跨币种相加',
  skill_id BIGINT UNSIGNED NULL COMMENT 'skill事实族的telemetry_skills内部身份；其他族为NULL；不以公开名称去重',
  skill_use_count DECIMAL(30,0) NOT NULL DEFAULT 0 COMMENT 'skill有效调用事实数；用于排行与总量SUM；其他事实族为0',
  skill_stats JSON NOT NULL COMMENT 'Skill属性v1；成功失败和时长覆盖等附属计数；非skill为空对象；见第24节',
  resources JSON NOT NULL COMMENT '资源属性v1；输入输出和缓存配对的十进制字符串与known/observed；非usage为空对象',
  activity JSON NOT NULL COMMENT '活动属性v1；代码、时长毫秒、消息与覆盖计数；非activity为空对象',
  hourly JSON NOT NULL COMMENT '小时Token属性v1；UTC毫秒桶与exact/derived整数；不伪造旧日汇总的小时分布',
  quality JSON NOT NULL COMMENT '质量属性v1；来源日历、已知样本计数及旧汇总标记；禁止缺必填键',
  max_received_at DATETIME(3) NULL COMMENT '来源最晚入库UTC时间；来源未知为NULL，不是页面请求时间',
  rule_version VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '投影规则版本；拟议为6，实施前核对',
  PRIMARY KEY (id),
  UNIQUE KEY uk_tmdm_identity (contributor_key, metric_date, row_key),
  KEY idx_tmdm_team_date (team_id, metric_date, delete_at, metric_kind),
  KEY idx_tmdm_contributor (contributor_key, metric_date, delete_at),
  CONSTRAINT fk_tmdm_team FOREIGN KEY (team_id) REFERENCES teams(team_id),
  CONSTRAINT fk_tmdm_contributor FOREIGN KEY (contributor_key)
    REFERENCES team_usage_contributors(contributor_key),
  CHECK (metric_kind IN ('usage','activity','cost','skill')),
  CHECK (JSON_TYPE(skill_stats) = 'OBJECT'),
  CHECK (JSON_TYPE(resources) = 'OBJECT'),
  CHECK (JSON_TYPE(activity) = 'OBJECT'),
  CHECK (JSON_TYPE(hourly) = 'OBJECT'),
  CHECK (JSON_TYPE(quality) = 'OBJECT'),
  CHECK (JSON_TYPE(extra) = 'OBJECT')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin
COMMENT='团队贡献者静态日指标；有效读必须过滤delete_at IS NULL';
