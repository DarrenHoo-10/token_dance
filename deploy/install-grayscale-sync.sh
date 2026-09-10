#!/bin/bash
set -euo pipefail
# Run on the TokenDance cloud host as ubuntu with passwordless sudo.
# Creates a dedicated MySQL login that can SELECT prod and write tokendance_dev.

BIN_SRC=${1:-/tmp/token-dance-grayscale-sync}
SERVICE_SRC=${2:-/tmp/token-dance-grayscale-sync.service}
install -d -o root -g tokendance -m 0755 /opt/token-dance/tools/grayscale-sync
install -o root -g tokendance -m 0755 "$BIN_SRC" /opt/token-dance/tools/grayscale-sync/token-dance-grayscale-sync
install -o root -g root -m 0644 "$SERVICE_SRC" /etc/systemd/system/token-dance-grayscale-sync.service

PASS=$(openssl rand -hex 24)
SQL=$(mktemp)
trap 'rm -f "$SQL"' EXIT
cat >"$SQL" <<EOF
CREATE USER IF NOT EXISTS 'tokendance_grayscale'@'%' IDENTIFIED BY '${PASS}';
ALTER USER 'tokendance_grayscale'@'%' IDENTIFIED BY '${PASS}';
GRANT SELECT ON tokendance_prod.* TO 'tokendance_grayscale'@'%';
GRANT ALL PRIVILEGES ON tokendance_dev.* TO 'tokendance_grayscale'@'%';
FLUSH PRIVILEGES;
CREATE TABLE IF NOT EXISTS tokendance_dev.community_daily_stats (
  metric_date     DATE NOT NULL,
  tokens_total    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  developers      INT UNSIGNED NOT NULL DEFAULT 0,
  code_lines      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  interactions    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cost_amount     DECIMAL(20, 6) NOT NULL DEFAULT 0,
  is_final        TINYINT(1) NOT NULL DEFAULT 0,
  computed_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                  ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS tokendance_dev.community_agent_daily_stats (
  metric_date     DATE NOT NULL,
  agent_id        VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  tokens_total    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  computed_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                  ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, agent_id)
) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS tokendance_dev.community_stats_outbox (
  task_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  metric_date      DATE NOT NULL,
  task_status      VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  claim_token      CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  locked_by        VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_expires_at DATETIME(3) NULL,
  attempt_count    SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_error_code  VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  applied_at       DATETIME(3) NULL,
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                   ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (task_id),
  KEY idx_community_outbox_poll (task_status, next_attempt_at, created_at),
  KEY idx_community_outbox_date (metric_date),
  KEY idx_community_outbox_cleanup (task_status, applied_at)
) ENGINE=InnoDB;
EOF
docker exec -i usercenter-mysql sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot' <"$SQL"

umask 027
install -d -o root -g tokendance -m 0750 /etc/token-dance/secrets
printf '%s\n' "tokendance_grayscale:${PASS}@tcp(127.0.0.1:3307)/tokendance_prod?parseTime=true&loc=UTC" > /etc/token-dance/secrets/grayscale_source_dsn
printf '%s\n' "tokendance_grayscale:${PASS}@tcp(127.0.0.1:3307)/tokendance_dev?parseTime=true&loc=UTC" > /etc/token-dance/secrets/grayscale_target_dsn
chown root:tokendance /etc/token-dance/secrets/grayscale_source_dsn /etc/token-dance/secrets/grayscale_target_dsn
chmod 0640 /etc/token-dance/secrets/grayscale_source_dsn /etc/token-dance/secrets/grayscale_target_dsn

cat > /etc/token-dance/grayscale.env <<'ENV'
TOKENDANCE_SOURCE_MYSQL_DSN_FILE=/etc/token-dance/secrets/grayscale_source_dsn
TOKENDANCE_MYSQL_DSN_FILE=/etc/token-dance/secrets/grayscale_target_dsn
TOKENDANCE_SOURCE_SCHEMA=tokendance_prod
TOKENDANCE_TARGET_SCHEMA=tokendance_dev
TOKENDANCE_GRAYSCALE_LIMIT=10
TOKENDANCE_GRAYSCALE_REFRESH_DAYS=2
TOKENDANCE_GRAYSCALE_INTERVAL=5m
ENV
chown root:tokendance /etc/token-dance/grayscale.env
chmod 0640 /etc/token-dance/grayscale.env

systemctl daemon-reload
echo 'installed binary and secrets'
