package mysqlstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/tokendance/token-collector/server/db/migrations"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
)

var ErrBatchHashConflict = store.ErrBatchHashConflict

const aggregationWatermarkName = "daily_metrics_committed_v2"

var (
	_ store.UserStore            = (*Store)(nil)
	_ store.InstallationStore    = (*Store)(nil)
	_ store.NonceStore           = (*Store)(nil)
	_ store.BatchStore           = (*Store)(nil)
	_ store.AtomicIngestStore    = (*Store)(nil)
	_ store.BatchRejectionStore  = (*Store)(nil)
	_ store.EventStore           = (*Store)(nil)
	_ store.MetricStore          = (*Store)(nil)
	_ store.LeaderboardStore     = (*Store)(nil)
	_ store.WatermarkStore       = (*Store)(nil)
	_ store.RecomputeMetricStore = (*Store)(nil)
)

type Store struct{ db *sql.DB }

type Accuracy string

const (
	AccuracyExact      Accuracy = "exact"
	AccuracyDerived    Accuracy = "derived"
	AccuracyEstimated  Accuracy = "estimated"
	AccuracyCorrelated Accuracy = "correlated"
)

type AggregationProgress struct {
	SourceMaxEventPK uint64
	CommittedThrough time.Time
	UpdatedAt        time.Time
	EventCount       int
}

type ScopeRef struct {
	Type string
	Key  string
}

type DeletionRequest struct {
	RequestID      string
	UserID         string
	InstallationID string
	Scope          string
	RangeStart     *time.Time
	RangeEnd       *time.Time
	Status         string
	ErrorCode      string
	RequestedAt    time.Time
}

func Open(ctx context.Context, dsn string, migrate bool) (*Store, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.MultiStatements = migrate
	cfg.Params = cloneParams(cfg.Params)
	cfg.Params["time_zone"] = "'+00:00'"
	if migrate {
		adminCfg := *cfg
		adminCfg.DBName = ""
		admin, err := sql.Open("mysql", adminCfg.FormatDSN())
		if err != nil {
			return nil, err
		}
		if err = admin.PingContext(ctx); err == nil {
			var exists int
			err = admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='tokenshow' AND table_name='users'`).Scan(&exists)
			if err == nil && exists == 0 {
				_, err = admin.ExecContext(ctx, migrations.InitialSchema)
			}
			if err == nil {
				err = applyAggregationSafetyMigration(ctx, admin)
			}
			if err == nil {
				var ingestDurabilityApplied int
				err = admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='tokenshow' AND table_name='usage_events' AND column_name='child_session_hash'`).Scan(&ingestDurabilityApplied)
				if err == nil && ingestDurabilityApplied == 0 {
					_, err = admin.ExecContext(ctx, migrations.IngestDurability)
				}
			}
			if err == nil {
				var typedEventFieldsApplied int
				err = admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='tokenshow' AND table_name='usage_events' AND column_name='workspace_hash'`).Scan(&typedEventFieldsApplied)
				if err == nil && typedEventFieldsApplied == 0 {
					_, err = admin.ExecContext(ctx, migrations.TypedEventFields)
				}
			}
		}
		_ = admin.Close()
		if err != nil {
			return nil, fmt.Errorf("apply migration: %w", err)
		}
	}
	if cfg.DBName == "" {
		cfg.DBName = "tokenshow"
	}
	cfg.MultiStatements = false
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(3 * time.Minute)
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func applyAggregationSafetyMigration(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		table      string
		column     string
		definition string
	}{
		{"aggregation_watermarks", "committed_through_at", `DATETIME(3) NOT NULL DEFAULT '1970-01-01 00:00:00.000' AFTER source_max_event_pk`},
		{"data_deletion_requests", "range_start", `DATETIME(3) NULL AFTER deletion_scope`},
		{"data_deletion_requests", "range_end", `DATETIME(3) NULL AFTER range_start`},
	}
	for _, column := range columns {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='tokenshow' AND table_name=? AND column_name=?`, column.table, column.column).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := db.ExecContext(ctx, `ALTER TABLE tokenshow.`+column.table+` ADD COLUMN `+column.column+` `+column.definition); err != nil {
				return err
			}
		}
	}
	var receivedIndex int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema='tokenshow' AND table_name='usage_events' AND index_name='idx_usage_events_received_pk'`).Scan(&receivedIndex); err != nil {
		return err
	}
	if receivedIndex == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE tokenshow.usage_events ADD KEY idx_usage_events_received_pk(received_at,event_pk)`); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tokenshow.daily_cost_metrics (
		metric_date DATE NOT NULL,
		user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		agent_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		cost_currency CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		cost_source VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		cost_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
		discount_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
		source_max_event_pk BIGINT UNSIGNED NOT NULL DEFAULT 0,
		aggregation_version INT UNSIGNED NOT NULL,
		computed_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
		updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
		PRIMARY KEY(metric_date,user_id,agent_id,cost_currency,cost_source),
		KEY idx_daily_cost_user_date(user_id,metric_date),
		CONSTRAINT fk_daily_cost_metrics_user FOREIGN KEY(user_id) REFERENCES tokenshow.users(user_id)
	) ENGINE=InnoDB;
	CREATE TABLE IF NOT EXISTS tokenshow.teams (
		team_id VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		team_name VARCHAR(120) NOT NULL,
		created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
		PRIMARY KEY (team_id)
	) ENGINE=InnoDB;
	CREATE TABLE IF NOT EXISTS tokenshow.team_memberships (
		team_id VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		membership_status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
		created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
		PRIMARY KEY (team_id,user_id),
		KEY idx_team_memberships_user (user_id,membership_status),
		CONSTRAINT fk_team_memberships_team FOREIGN KEY (team_id) REFERENCES tokenshow.teams(team_id) ON DELETE CASCADE,
		CONSTRAINT fk_team_memberships_user FOREIGN KEY (user_id) REFERENCES tokenshow.users(user_id) ON DELETE CASCADE,
		CONSTRAINT chk_team_membership_status CHECK (membership_status IN ('active','removed'))
	) ENGINE=InnoDB`)
	return err
}

func cloneParams(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) CreateUser(ctx context.Context, u *domain.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users
		(user_id,auth_subject_hash,display_name,avatar_url,account_status,leaderboard_visibility,timezone_name,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, u.UserID, u.AuthSubjectHash[:], u.DisplayName, nullString(u.AvatarURL), u.AccountStatus, u.LeaderboardVisibility, u.TimezoneName, u.CreatedAt, u.UpdatedAt)
	return err
}

func scanUser(row interface{ Scan(...any) error }) (*domain.User, error) {
	var u domain.User
	var hash []byte
	var avatar sql.NullString
	err := row.Scan(&u.UserID, &hash, &u.DisplayName, &avatar, &u.AccountStatus, &u.LeaderboardVisibility, &u.TimezoneName, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	copy(u.AuthSubjectHash[:], hash)
	u.AvatarURL = avatar.String
	return &u, nil
}
func (s *Store) GetUser(ctx context.Context, id string) (*domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT user_id,auth_subject_hash,display_name,avatar_url,account_status,leaderboard_visibility,timezone_name,created_at,updated_at FROM users WHERE user_id=?`, id))
}
func (s *Store) GetUserByAuthSubjectHash(ctx context.Context, h [32]byte) (*domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT user_id,auth_subject_hash,display_name,avatar_url,account_status,leaderboard_visibility,timezone_name,created_at,updated_at FROM users WHERE auth_subject_hash=?`, h[:]))
}
func (s *Store) UpdateVisibility(ctx context.Context, id, v string) error {
	r, e := s.db.ExecContext(ctx, `UPDATE users SET leaderboard_visibility=? WHERE user_id=?`, v, id)
	return affected(r, e)
}
func (s *Store) ListPublicUsers(ctx context.Context) ([]*domain.User, error) {
	r, e := s.db.QueryContext(ctx, `SELECT user_id,auth_subject_hash,display_name,avatar_url,account_status,leaderboard_visibility,timezone_name,created_at,updated_at FROM users WHERE leaderboard_visibility='public' AND account_status='active' ORDER BY user_id`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var out []*domain.User
	for r.Next() {
		u, e := scanUser(r)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, r.Err()
}

func (s *Store) CreateInstallation(ctx context.Context, i *domain.Installation) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO installations (installation_id,user_id,device_public_key,device_name,os_type,os_version,architecture,collector_version,installation_status,registered_at,last_seen_at,revoked_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, i.InstallationID, i.UserID, []byte(i.DevicePublicKey), nullString(i.DeviceName), i.OSType, nullString(i.OSVersion), i.Architecture, i.CollectorVersion, i.InstallationStatus, i.RegisteredAt, i.LastSeenAt, i.RevokedAt)
	return e
}
func scanInstallation(row interface{ Scan(...any) error }) (*domain.Installation, error) {
	var i domain.Installation
	var pk []byte
	var name, osv sql.NullString
	var last, rev sql.NullTime
	e := row.Scan(&i.InstallationID, &i.UserID, &pk, &name, &i.OSType, &osv, &i.Architecture, &i.CollectorVersion, &i.InstallationStatus, &i.RegisteredAt, &last, &rev)
	if e != nil {
		return nil, e
	}
	i.DevicePublicKey = ed25519.PublicKey(append([]byte(nil), pk...))
	i.DeviceName = name.String
	i.OSVersion = osv.String
	if last.Valid {
		i.LastSeenAt = &last.Time
	}
	if rev.Valid {
		i.RevokedAt = &rev.Time
	}
	return &i, nil
}

const installationSelect = `SELECT installation_id,user_id,device_public_key,device_name,os_type,os_version,architecture,collector_version,installation_status,registered_at,last_seen_at,revoked_at FROM installations`

func (s *Store) GetInstallation(ctx context.Context, id string) (*domain.Installation, error) {
	return scanInstallation(s.db.QueryRowContext(ctx, installationSelect+` WHERE installation_id=?`, id))
}
func (s *Store) GetInstallationByPublicKey(ctx context.Context, k []byte) (*domain.Installation, error) {
	return scanInstallation(s.db.QueryRowContext(ctx, installationSelect+` WHERE device_public_key=?`, k))
}
func (s *Store) UpdateLastSeen(ctx context.Context, id string, t time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE installations SET last_seen_at=GREATEST(COALESCE(last_seen_at,?),?) WHERE installation_id=?`, t, t, id)
	return affected(r, e)
}
func (s *Store) RevokeInstallation(ctx context.Context, id string, t time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE installations SET installation_status='revoked',revoked_at=? WHERE installation_id=? AND installation_status='active'`, t, id)
	return affected(r, e)
}
func (s *Store) ListByUser(ctx context.Context, id string) ([]*domain.Installation, error) {
	r, e := s.db.QueryContext(ctx, installationSelect+` WHERE user_id=? ORDER BY registered_at`, id)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var out []*domain.Installation
	for r.Next() {
		i, e := scanInstallation(r)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, r.Err()
}

func (s *Store) ConsumeNonce(ctx context.Context, id string, h [32]byte, exp time.Time) (bool, error) {
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var old time.Time
	e = tx.QueryRowContext(ctx, `SELECT expires_at FROM ingest_nonces WHERE installation_id=? AND nonce_hash=? FOR UPDATE`, id, h[:]).Scan(&old)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		_, e = tx.ExecContext(ctx, `INSERT INTO ingest_nonces(installation_id,nonce_hash,expires_at) VALUES(?,?,?)`, id, h[:], exp)
	case e != nil:
		return false, e
	case time.Now().UTC().Before(old):
		return false, nil
	default:
		_, e = tx.ExecContext(ctx, `UPDATE ingest_nonces SET expires_at=?,created_at=CURRENT_TIMESTAMP(3) WHERE installation_id=? AND nonce_hash=?`, exp, id, h[:])
	}
	if e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return true, nil
}
func (s *Store) PruneExpired(ctx context.Context, now time.Time) (int, error) {
	r, e := s.db.ExecContext(ctx, `DELETE FROM ingest_nonces WHERE expires_at<=?`, now)
	if e != nil {
		return 0, e
	}
	n, e := r.RowsAffected()
	return int(n), e
}

func (s *Store) CreateBatch(ctx context.Context, b *domain.IngestBatch) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO ingest_batches(batch_id,installation_id,request_sha256,event_count,accepted_count,duplicate_count,rejected_count,batch_status,received_at,committed_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, b.BatchID, b.InstallationID, b.RequestSHA256[:], b.EventCount, b.AcceptedCount, b.DuplicateCount, b.RejectedCount, b.BatchStatus, b.ReceivedAt, b.CommittedAt)
	return e
}
func scanBatch(row interface{ Scan(...any) error }) (*domain.IngestBatch, error) {
	var b domain.IngestBatch
	var h []byte
	var c sql.NullTime
	e := row.Scan(&b.BatchID, &b.InstallationID, &h, &b.EventCount, &b.AcceptedCount, &b.DuplicateCount, &b.RejectedCount, &b.BatchStatus, &b.ReceivedAt, &c)
	if e != nil {
		return nil, e
	}
	copy(b.RequestSHA256[:], h)
	if c.Valid {
		b.CommittedAt = &c.Time
	}
	return &b, nil
}

const batchSelect = `SELECT batch_id,installation_id,request_sha256,event_count,accepted_count,duplicate_count,rejected_count,batch_status,received_at,committed_at FROM ingest_batches`

func (s *Store) GetBatch(ctx context.Context, id string) (*domain.IngestBatch, error) {
	return scanBatch(s.db.QueryRowContext(ctx, batchSelect+` WHERE batch_id=?`, id))
}
func (s *Store) UpdateBatch(ctx context.Context, b *domain.IngestBatch) error {
	r, e := s.db.ExecContext(ctx, `UPDATE ingest_batches SET accepted_count=?,duplicate_count=?,rejected_count=?,batch_status=?,committed_at=? WHERE batch_id=? AND request_sha256=?`, b.AcceptedCount, b.DuplicateCount, b.RejectedCount, b.BatchStatus, b.CommittedAt, b.BatchID, b.RequestSHA256[:])
	return affected(r, e)
}

func (s *Store) CommitBatch(ctx context.Context, b *domain.IngestBatch, events []*store.IngestEvent, rejected []store.BatchRejection) (*store.IngestCommitResult, error) {
	const maxAttempts = 20
	for attempt := 0; attempt < maxAttempts; attempt++ {
		result, err := s.commitBatchOnce(ctx, b, events, rejected)
		if !isRetryableTransaction(err) {
			return result, err
		}
		delay := time.Duration(attempt+1) * 5 * time.Millisecond
		if delay > 50*time.Millisecond {
			delay = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("commit batch: transaction retry limit exceeded")
}

func (s *Store) commitBatchOnce(ctx context.Context, b *domain.IngestBatch, events []*store.IngestEvent, rejected []store.BatchRejection) (*store.IngestCommitResult, error) {
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO aggregation_watermarks(watermark_name,committed_through_at) VALUES(?,?)`, aggregationWatermarkName, time.Unix(0, 0).UTC()); e != nil {
		return nil, e
	}
	var committedThrough time.Time
	if e = tx.QueryRowContext(ctx, `SELECT committed_through_at FROM aggregation_watermarks WHERE watermark_name=? FOR UPDATE`, aggregationWatermarkName).Scan(&committedThrough); e != nil {
		return nil, e
	}
	if !b.ReceivedAt.After(committedThrough) {
		b.ReceivedAt = committedThrough.Add(time.Millisecond)
	}
	for _, evt := range events {
		evt.Event.ReceivedAt = b.ReceivedAt
	}
	var installationStatus string
	e = tx.QueryRowContext(ctx, `SELECT installation_status FROM installations WHERE installation_id=? FOR UPDATE`, b.InstallationID).Scan(&installationStatus)
	if e != nil {
		return nil, e
	}
	if installationStatus != "active" {
		return nil, fmt.Errorf("installation is not active")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO ingest_batches(batch_id,installation_id,request_sha256,event_count,rejected_count,batch_status,received_at) VALUES(?,?,?,?,?,'received',?) ON DUPLICATE KEY UPDATE batch_id=VALUES(batch_id)`, b.BatchID, b.InstallationID, b.RequestSHA256[:], b.EventCount, b.RejectedCount, b.ReceivedAt)
	if e != nil {
		return nil, e
	}
	existing, e := scanBatch(tx.QueryRowContext(ctx, batchSelect+` WHERE batch_id=? FOR UPDATE`, b.BatchID))
	if e != nil {
		return nil, e
	}
	if existing.InstallationID != b.InstallationID || !bytes.Equal(existing.RequestSHA256[:], b.RequestSHA256[:]) {
		return nil, ErrBatchHashConflict
	}
	if existing.CommittedAt != nil {
		durableRejected, loadErr := queryBatchRejections(ctx, tx, b.BatchID)
		if loadErr != nil {
			return nil, loadErr
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return &store.IngestCommitResult{Batch: existing, Rejected: durableRejected}, nil
	}

	var accepted, duplicates uint32
	for _, evt := range events {
		ok, insertErr := insertIngestEvent(ctx, tx, evt)
		if insertErr != nil {
			return nil, insertErr
		}
		if ok {
			accepted++
		} else {
			duplicates++
		}
	}
	for _, rejection := range rejected {
		_, e = tx.ExecContext(ctx, `INSERT INTO ingest_batch_rejections(batch_id,event_ordinal,event_id,error_code,retryable) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE event_id=VALUES(event_id),error_code=VALUES(error_code),retryable=VALUES(retryable)`, b.BatchID, rejection.Ordinal, rejection.EventID, rejection.ErrorCode, rejection.Retryable)
		if e != nil {
			return nil, e
		}
	}
	b.AcceptedCount = accepted
	b.DuplicateCount = duplicates
	b.RejectedCount = uint32(len(rejected))
	if b.RejectedCount == 0 {
		b.BatchStatus = "committed"
	} else if accepted > 0 {
		b.BatchStatus = "partial"
	} else {
		b.BatchStatus = "rejected"
	}
	now := time.Now().UTC()
	b.CommittedAt = &now
	_, e = tx.ExecContext(ctx, `UPDATE ingest_batches SET accepted_count=?,duplicate_count=?,rejected_count=?,batch_status=?,committed_at=? WHERE batch_id=?`, b.AcceptedCount, b.DuplicateCount, b.RejectedCount, b.BatchStatus, b.CommittedAt, b.BatchID)
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE installations SET last_seen_at=GREATEST(COALESCE(last_seen_at,?),?) WHERE installation_id=?`, b.ReceivedAt, b.ReceivedAt, b.InstallationID)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	cp := *b
	return &store.IngestCommitResult{Batch: &cp, Rejected: append([]store.BatchRejection(nil), rejected...)}, nil
}

type rowQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryBatchRejections(ctx context.Context, q rowQueryer, batchID string) ([]store.BatchRejection, error) {
	rows, err := q.QueryContext(ctx, `SELECT event_ordinal,event_id,error_code,retryable FROM ingest_batch_rejections WHERE batch_id=? ORDER BY event_ordinal`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rejected []store.BatchRejection
	for rows.Next() {
		var rejection store.BatchRejection
		if err = rows.Scan(&rejection.Ordinal, &rejection.EventID, &rejection.ErrorCode, &rejection.Retryable); err != nil {
			return nil, err
		}
		rejected = append(rejected, rejection)
	}
	return rejected, rows.Err()
}

func (s *Store) GetBatchRejections(ctx context.Context, batchID string) ([]store.BatchRejection, error) {
	return queryBatchRejections(ctx, s.db, batchID)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const eventInsert = `INSERT INTO usage_events(event_id,schema_version,batch_id,installation_id,user_id,adapter_id,adapter_version,agent_id,agent_version,provider_id,model_id,event_type,accuracy,source_kind,source_cursor_hmac,raw_fingerprint_hmac,occurred_at,received_at,session_hash,parent_session_hash,workspace_hash,session_end_reason,turn_trigger,child_session_hash,spawned_agent_type,turn_hash,tool_call_hash,token_input,token_output,token_cache_read,token_cache_write,token_reasoning,token_tool,token_total,duration_ms,success,tool_category,skill_key,skill_invoke_type,plugin_key,code_generated_lines,code_accepted_lines,code_added_lines,code_deleted_lines,code_file_count,code_language,cost_amount,cost_currency,cost_source,cost_discount_amount,privacy_policy_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`

func eventArgs(ingestEvent *store.IngestEvent) []any {
	e := ingestEvent.Event
	return []any{e.EventID[:], e.SchemaVersion, e.BatchID, e.InstallationID, e.UserID, e.AdapterID, e.AdapterVersion, e.AgentID, e.AgentVersion, e.ProviderID, e.ModelID, e.EventType, e.Accuracy, e.SourceKind, e.SourceCursorHMAC[:], e.RawFingerprintHMAC[:], e.OccurredAt, e.ReceivedAt, bytesPtr(e.SessionHash), bytesPtr(e.ParentSessionHash), bytesPtr(e.WorkspaceHash), e.SessionEndReason, e.TurnTrigger, bytesPtr(ingestEvent.ChildSessionHash), ingestEvent.SpawnedAgentType, bytesPtr(e.TurnHash), bytesPtr(e.ToolCallHash), e.TokenInput, e.TokenOutput, e.TokenCacheRead, e.TokenCacheWrite, e.TokenReasoning, e.TokenTool, e.TokenTotal, e.DurationMs, e.Success, ingestEvent.ToolCategory, bytesPtr(ingestEvent.SkillKey), ingestEvent.SkillInvokeType, bytesPtr(ingestEvent.PluginKey), e.CodeGeneratedLines, e.CodeAcceptedLines, e.CodeAddedLines, e.CodeDeletedLines, e.CodeFileCount, ingestEvent.CodeLanguage, ingestEvent.CostAmount, ingestEvent.CostCurrency, ingestEvent.CostSource, ingestEvent.CostDiscountAmount}
}
func insertIngestEvent(ctx context.Context, x execer, ingestEvent *store.IngestEvent) (bool, error) {
	r, err := x.ExecContext(ctx, eventInsert, eventArgs(ingestEvent)...)
	if isDuplicate(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	id, _ := r.LastInsertId()
	ingestEvent.Event.EventPK = uint64(id)
	return true, nil
}
func (s *Store) InsertEvent(ctx context.Context, e *domain.UsageEvent) (bool, error) {
	return insertIngestEvent(ctx, s.db, &store.IngestEvent{Event: e})
}

const eventSelect = `SELECT event_pk,event_id,schema_version,batch_id,installation_id,user_id,adapter_id,adapter_version,agent_id,agent_version,provider_id,model_id,event_type,accuracy,source_kind,source_cursor_hmac,raw_fingerprint_hmac,occurred_at,occurred_date,received_at,session_hash,parent_session_hash,workspace_hash,session_end_reason,turn_trigger,turn_hash,tool_call_hash,token_input,token_output,token_cache_read,token_cache_write,token_reasoning,token_tool,token_total,duration_ms,success,code_generated_lines,code_accepted_lines,code_added_lines,code_deleted_lines,code_file_count FROM usage_events`

func scanEvent(row interface{ Scan(...any) error }) (*domain.UsageEvent, error) {
	var e domain.UsageEvent
	var id, sc, rf, sess, parent, workspace, turn, tool []byte
	var av, pv, mv, endReason, turnTrigger sql.NullString
	var ti, to, tcr, tcw, tr, tt, tot, dur, cg, ca, add, del sql.NullInt64
	var succ sql.NullBool
	var files sql.NullInt64
	err := row.Scan(&e.EventPK, &id, &e.SchemaVersion, &e.BatchID, &e.InstallationID, &e.UserID, &e.AdapterID, &e.AdapterVersion, &e.AgentID, &av, &pv, &mv, &e.EventType, &e.Accuracy, &e.SourceKind, &sc, &rf, &e.OccurredAt, &e.OccurredDate, &e.ReceivedAt, &sess, &parent, &workspace, &endReason, &turnTrigger, &turn, &tool, &ti, &to, &tcr, &tcw, &tr, &tt, &tot, &dur, &succ, &cg, &ca, &add, &del, &files)
	if err != nil {
		return nil, err
	}
	copy(e.EventID[:], id)
	copy(e.SourceCursorHMAC[:], sc)
	copy(e.RawFingerprintHMAC[:], rf)
	e.AgentVersion = strPtr(av)
	e.ProviderID = strPtr(pv)
	e.ModelID = strPtr(mv)
	e.SessionHash = hashPtr(sess)
	e.ParentSessionHash = hashPtr(parent)
	e.WorkspaceHash = hashPtr(workspace)
	if endReason.Valid {
		value := domain.SessionEndReason(endReason.String)
		e.SessionEndReason = &value
	}
	if turnTrigger.Valid {
		value := domain.TurnTrigger(turnTrigger.String)
		e.TurnTrigger = &value
	}
	e.TurnHash = hashPtr(turn)
	e.ToolCallHash = hashPtr(tool)
	e.TokenInput = u64Ptr(ti)
	e.TokenOutput = u64Ptr(to)
	e.TokenCacheRead = u64Ptr(tcr)
	e.TokenCacheWrite = u64Ptr(tcw)
	e.TokenReasoning = u64Ptr(tr)
	e.TokenTool = u64Ptr(tt)
	e.TokenTotal = u64Ptr(tot)
	e.DurationMs = u64Ptr(dur)
	e.Success = boolPtr(succ)
	e.CodeGeneratedLines = u64Ptr(cg)
	e.CodeAcceptedLines = u64Ptr(ca)
	e.CodeAddedLines = u64Ptr(add)
	e.CodeDeletedLines = u64Ptr(del)
	if files.Valid {
		v := uint32(files.Int64)
		e.CodeFileCount = &v
	}
	return &e, nil
}
func queryEvents(rows *sql.Rows) ([]*domain.UsageEvent, error) {
	defer rows.Close()
	var out []*domain.UsageEvent
	for rows.Next() {
		e, er := scanEvent(rows)
		if er != nil {
			return nil, er
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) GetEventsByBatch(ctx context.Context, id string) ([]*domain.UsageEvent, error) {
	r, e := s.db.QueryContext(ctx, eventSelect+` WHERE batch_id=? ORDER BY event_pk`, id)
	if e != nil {
		return nil, e
	}
	return queryEvents(r)
}
func (s *Store) ListEventsAfterPK(ctx context.Context, pk uint64, limit int) ([]*domain.UsageEvent, error) {
	r, e := s.db.QueryContext(ctx, eventSelect+` WHERE event_pk>? ORDER BY event_pk LIMIT ?`, pk, limit)
	if e != nil {
		return nil, e
	}
	return queryEvents(r)
}
func (s *Store) MaxEventPK(ctx context.Context) (uint64, error) {
	var n sql.NullInt64
	e := s.db.QueryRowContext(ctx, `SELECT MAX(event_pk) FROM usage_events`).Scan(&n)
	return uint64(n.Int64), e
}
func (s *Store) DeleteEventsByBatch(ctx context.Context, id string) (int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type affectedDate struct {
		userID string
		date   time.Time
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT user_id,occurred_date FROM usage_events WHERE batch_id=? ORDER BY user_id,occurred_date`, id)
	if err != nil {
		return 0, err
	}
	var dates []affectedDate
	for rows.Next() {
		var affected affectedDate
		if err = rows.Scan(&affected.userID, &affected.date); err != nil {
			rows.Close()
			return 0, err
		}
		dates = append(dates, affected)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE batch_id=?`, id)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	progress := AggregationProgress{CommittedThrough: time.Unix(0, 0).UTC()}
	if err = tx.QueryRowContext(ctx, `SELECT source_max_event_pk,committed_through_at,updated_at FROM aggregation_watermarks ORDER BY updated_at DESC LIMIT 1`).Scan(&progress.SourceMaxEventPK, &progress.CommittedThrough, &progress.UpdatedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	for _, affected := range dates {
		if err = rebuildUserMetricDateTx(ctx, tx, affected.userID, affected.date, progress); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *Store) UpsertDailyUserAgentMetric(ctx context.Context, m *domain.DailyUserAgentMetric) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO daily_user_agent_metrics(metric_date,user_id,agent_id,exact_token_total,derived_token_total,estimated_token_total,session_count,child_session_count,interaction_turn_count,model_request_count,tool_call_count,skill_use_count,code_generated_lines,code_accepted_lines,correlated_code_lines,source_max_event_pk,aggregation_version,computed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE exact_token_total=VALUES(exact_token_total),derived_token_total=VALUES(derived_token_total),estimated_token_total=VALUES(estimated_token_total),session_count=VALUES(session_count),child_session_count=VALUES(child_session_count),interaction_turn_count=VALUES(interaction_turn_count),model_request_count=VALUES(model_request_count),tool_call_count=VALUES(tool_call_count),skill_use_count=VALUES(skill_use_count),code_generated_lines=VALUES(code_generated_lines),code_accepted_lines=VALUES(code_accepted_lines),correlated_code_lines=VALUES(correlated_code_lines),source_max_event_pk=VALUES(source_max_event_pk),aggregation_version=VALUES(aggregation_version),computed_at=VALUES(computed_at)`, m.MetricDate, m.UserID, m.AgentID, m.ExactTokenTotal, m.DerivedTokenTotal, m.EstimatedTokenTotal, m.SessionCount, m.ChildSessionCount, m.InteractionTurnCount, m.ModelRequestCount, m.ToolCallCount, m.SkillUseCount, m.CodeGeneratedLines, m.CodeAcceptedLines, m.CorrelatedCodeLines, m.SourceMaxEventPK, m.AggregationVersion, m.ComputedAt)
	return e
}

const metricSelect = `SELECT metric_date,user_id,agent_id,exact_token_total,derived_token_total,estimated_token_total,session_count,child_session_count,interaction_turn_count,model_request_count,tool_call_count,skill_use_count,code_generated_lines,code_accepted_lines,correlated_code_lines,source_max_event_pk,aggregation_version,computed_at FROM daily_user_agent_metrics`

func scanMetric(row interface{ Scan(...any) error }) (*domain.DailyUserAgentMetric, error) {
	m := new(domain.DailyUserAgentMetric)
	e := row.Scan(&m.MetricDate, &m.UserID, &m.AgentID, &m.ExactTokenTotal, &m.DerivedTokenTotal, &m.EstimatedTokenTotal, &m.SessionCount, &m.ChildSessionCount, &m.InteractionTurnCount, &m.ModelRequestCount, &m.ToolCallCount, &m.SkillUseCount, &m.CodeGeneratedLines, &m.CodeAcceptedLines, &m.CorrelatedCodeLines, &m.SourceMaxEventPK, &m.AggregationVersion, &m.ComputedAt)
	return m, e
}
func queryMetrics(r *sql.Rows) ([]*domain.DailyUserAgentMetric, error) {
	defer r.Close()
	var out []*domain.DailyUserAgentMetric
	for r.Next() {
		m, e := scanMetric(r)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, r.Err()
}
func (s *Store) GetDailyMetrics(ctx context.Context, u string, start, end time.Time) ([]*domain.DailyUserAgentMetric, error) {
	r, e := s.db.QueryContext(ctx, metricSelect+` WHERE user_id=? AND metric_date BETWEEN DATE(?) AND DATE(?) ORDER BY metric_date,agent_id`, u, start, end)
	if e != nil {
		return nil, e
	}
	return queryMetrics(r)
}
func (s *Store) GetDailyMetricsAllUsers(ctx context.Context, start, end time.Time) ([]*domain.DailyUserAgentMetric, error) {
	r, e := s.db.QueryContext(ctx, metricSelect+` WHERE metric_date BETWEEN DATE(?) AND DATE(?) ORDER BY metric_date,user_id,agent_id`, start, end)
	if e != nil {
		return nil, e
	}
	return queryMetrics(r)
}
func (s *Store) DeleteAllMetrics(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `DELETE FROM daily_user_agent_metrics`)
	return e
}
func upsertSelectedMetricsTx(ctx context.Context, tx *sql.Tx) error {
	base := ` FROM usage_events e JOIN aggregation_batch_events selected ON selected.event_pk=e.event_pk`
	_, err := tx.ExecContext(ctx, `INSERT INTO daily_user_agent_metrics(metric_date,user_id,agent_id,exact_token_total,derived_token_total,estimated_token_total,session_count,child_session_count,interaction_turn_count,model_request_count,tool_call_count,skill_use_count,code_generated_lines,code_accepted_lines,correlated_code_lines,cost_amount,cost_currency,source_max_event_pk,aggregation_version,computed_at) SELECT e.occurred_date,e.user_id,e.agent_id,COALESCE(SUM(CASE WHEN e.accuracy='exact' THEN e.token_total ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.accuracy='derived' THEN e.token_total ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.accuracy IN('estimated','correlated') THEN e.token_total ELSE 0 END),0),SUM(e.event_type='session_started'),SUM(e.event_type='agent_spawned'),SUM(e.event_type IN('turn_started','turn_completed')),SUM(e.event_type='model_usage_recorded'),SUM(e.event_type='tool_invoked'),SUM(e.event_type='skill_invoked'),COALESCE(SUM(CASE WHEN e.event_type='code_changed' AND e.accuracy IN('exact','derived') THEN e.code_added_lines ELSE 0 END),0),COALESCE(SUM(e.code_accepted_lines),0),COALESCE(SUM(CASE WHEN e.event_type='code_changed' AND e.accuracy='correlated' THEN e.code_added_lines ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.event_type='cost_recorded' THEN e.cost_amount ELSE 0 END),0),COALESCE(MAX(CASE WHEN e.event_type='cost_recorded' THEN e.cost_currency END),'USD'),MAX(e.event_pk),2,CURRENT_TIMESTAMP(3)`+base+` GROUP BY e.occurred_date,e.user_id,e.agent_id ON DUPLICATE KEY UPDATE exact_token_total=exact_token_total+VALUES(exact_token_total),derived_token_total=derived_token_total+VALUES(derived_token_total),estimated_token_total=estimated_token_total+VALUES(estimated_token_total),session_count=session_count+VALUES(session_count),child_session_count=child_session_count+VALUES(child_session_count),interaction_turn_count=interaction_turn_count+VALUES(interaction_turn_count),model_request_count=model_request_count+VALUES(model_request_count),tool_call_count=tool_call_count+VALUES(tool_call_count),skill_use_count=skill_use_count+VALUES(skill_use_count),code_generated_lines=code_generated_lines+VALUES(code_generated_lines),code_accepted_lines=code_accepted_lines+VALUES(code_accepted_lines),correlated_code_lines=correlated_code_lines+VALUES(correlated_code_lines),cost_amount=cost_amount+VALUES(cost_amount),source_max_event_pk=GREATEST(source_max_event_pk,VALUES(source_max_event_pk)),aggregation_version=VALUES(aggregation_version),computed_at=VALUES(computed_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_user_agent_model_metrics(metric_date,user_id,agent_id,provider_id,model_id,exact_token_total,derived_token_total,estimated_token_total,model_request_count,cost_amount,cost_currency,source_max_event_pk,aggregation_version,computed_at) SELECT e.occurred_date,e.user_id,e.agent_id,e.provider_id,e.model_id,COALESCE(SUM(CASE WHEN e.accuracy='exact' THEN e.token_total ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.accuracy='derived' THEN e.token_total ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.accuracy IN('estimated','correlated') THEN e.token_total ELSE 0 END),0),SUM(e.event_type='model_usage_recorded'),COALESCE(SUM(CASE WHEN e.event_type='cost_recorded' THEN e.cost_amount ELSE 0 END),0),COALESCE(MAX(e.cost_currency),'USD'),MAX(e.event_pk),2,CURRENT_TIMESTAMP(3)`+base+` WHERE e.provider_id IS NOT NULL AND e.model_id IS NOT NULL GROUP BY e.occurred_date,e.user_id,e.agent_id,e.provider_id,e.model_id ON DUPLICATE KEY UPDATE exact_token_total=exact_token_total+VALUES(exact_token_total),derived_token_total=derived_token_total+VALUES(derived_token_total),estimated_token_total=estimated_token_total+VALUES(estimated_token_total),model_request_count=model_request_count+VALUES(model_request_count),cost_amount=cost_amount+VALUES(cost_amount),source_max_event_pk=GREATEST(source_max_event_pk,VALUES(source_max_event_pk)),aggregation_version=VALUES(aggregation_version),computed_at=VALUES(computed_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_skill_metrics(metric_date,user_id,agent_id,skill_key,skill_public_name,use_count,exact_use_count,derived_use_count,correlated_use_count,estimated_use_count,success_count,failure_count,duration_ms,source_max_event_pk,aggregation_version,computed_at) SELECT e.occurred_date,e.user_id,e.agent_id,e.skill_key,MAX(e.skill_public_name),COUNT(*),SUM(e.accuracy='exact'),SUM(e.accuracy='derived'),SUM(e.accuracy='correlated'),SUM(e.accuracy='estimated'),SUM(e.success=TRUE),SUM(e.success=FALSE),COALESCE(SUM(e.duration_ms),0),MAX(e.event_pk),2,CURRENT_TIMESTAMP(3)`+base+` WHERE e.skill_key IS NOT NULL GROUP BY e.occurred_date,e.user_id,e.agent_id,e.skill_key ON DUPLICATE KEY UPDATE skill_public_name=COALESCE(VALUES(skill_public_name),skill_public_name),use_count=use_count+VALUES(use_count),exact_use_count=exact_use_count+VALUES(exact_use_count),derived_use_count=derived_use_count+VALUES(derived_use_count),correlated_use_count=correlated_use_count+VALUES(correlated_use_count),estimated_use_count=estimated_use_count+VALUES(estimated_use_count),success_count=success_count+VALUES(success_count),failure_count=failure_count+VALUES(failure_count),duration_ms=duration_ms+VALUES(duration_ms),source_max_event_pk=GREATEST(source_max_event_pk,VALUES(source_max_event_pk)),aggregation_version=VALUES(aggregation_version),computed_at=VALUES(computed_at)`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_cost_metrics(metric_date,user_id,agent_id,cost_currency,cost_source,cost_amount,discount_amount,source_max_event_pk,aggregation_version,computed_at) SELECT e.occurred_date,e.user_id,e.agent_id,e.cost_currency,e.cost_source,SUM(e.cost_amount),COALESCE(SUM(e.cost_discount_amount),0),MAX(e.event_pk),2,CURRENT_TIMESTAMP(3)`+base+` WHERE e.event_type='cost_recorded' AND e.cost_amount IS NOT NULL AND e.cost_currency IS NOT NULL AND e.cost_source IS NOT NULL GROUP BY e.occurred_date,e.user_id,e.agent_id,e.cost_currency,e.cost_source ON DUPLICATE KEY UPDATE cost_amount=cost_amount+VALUES(cost_amount),discount_amount=discount_amount+VALUES(discount_amount),source_max_event_pk=GREATEST(source_max_event_pk,VALUES(source_max_event_pk)),aggregation_version=VALUES(aggregation_version),computed_at=VALUES(computed_at)`)
	return err
}

func rebuildUserMetricDateTx(ctx context.Context, tx *sql.Tx, userID string, metricDate time.Time, progress AggregationProgress) error {
	for _, table := range []string{"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics", "daily_cost_metrics"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=? AND metric_date=DATE(?)`, userID, metricDate); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TEMPORARY TABLE IF EXISTS aggregation_batch_events`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMPORARY TABLE aggregation_batch_events(event_pk BIGINT UNSIGNED NOT NULL PRIMARY KEY,received_at DATETIME(3) NOT NULL) ENGINE=InnoDB`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO aggregation_batch_events(event_pk,received_at) SELECT e.event_pk,e.received_at FROM usage_events e JOIN ingest_batches b ON b.batch_id=e.batch_id WHERE e.user_id=? AND e.occurred_date=DATE(?) AND (e.received_at<? OR (e.received_at=? AND e.event_pk<=?)) AND b.committed_at IS NOT NULL AND b.batch_status IN ('committed','partial')`, userID, metricDate, progress.CommittedThrough, progress.CommittedThrough, progress.SourceMaxEventPK)
	if err == nil {
		err = upsertSelectedMetricsTx(ctx, tx)
	}
	if _, dropErr := tx.ExecContext(ctx, `DROP TEMPORARY TABLE aggregation_batch_events`); err == nil {
		err = dropErr
	}
	return err
}

func (s *Store) GetWatermark(ctx context.Context, n string) (uint64, error) {
	progress, err := s.GetAggregationProgress(ctx, n)
	return progress.SourceMaxEventPK, err
}
func (s *Store) SetWatermark(ctx context.Context, n string, pk uint64, t time.Time) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO aggregation_watermarks(watermark_name,source_max_event_pk,committed_through_at,updated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE source_max_event_pk=GREATEST(source_max_event_pk,VALUES(source_max_event_pk)),updated_at=VALUES(updated_at)`, n, pk, time.Unix(0, 0).UTC(), t)
	return e
}
func (s *Store) RecomputeMetrics(ctx context.Context, name string, _ uint64) error {
	for {
		progress, err := s.AggregateCommittedMetrics(ctx, name, time.Now().UTC(), 1000)
		if err != nil {
			return err
		}
		if progress.EventCount < 1000 {
			return nil
		}
	}
}

func (s *Store) GetAggregationProgress(ctx context.Context, name string) (AggregationProgress, error) {
	var p AggregationProgress
	err := s.db.QueryRowContext(ctx, `SELECT source_max_event_pk,committed_through_at,updated_at FROM aggregation_watermarks WHERE watermark_name=?`, name).Scan(&p.SourceMaxEventPK, &p.CommittedThrough, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		p.CommittedThrough = time.Unix(0, 0).UTC()
		return p, nil
	}
	return p, err
}

func (s *Store) RecomputeCommittedMetrics(ctx context.Context, name string, safeBefore time.Time) (AggregationProgress, error) {
	return s.AggregateCommittedMetrics(ctx, name, safeBefore, 1000)
}

func (s *Store) AggregateCommittedMetrics(ctx context.Context, name string, safeBefore time.Time, limit int) (AggregationProgress, error) {
	if limit <= 0 {
		return AggregationProgress{}, fmt.Errorf("aggregation batch size must be positive")
	}
	safeBefore = safeBefore.UTC().Truncate(time.Millisecond)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return AggregationProgress{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO aggregation_watermarks(watermark_name,committed_through_at) VALUES(?,?)`, name, time.Unix(0, 0).UTC()); err != nil {
		return AggregationProgress{}, err
	}
	var previous AggregationProgress
	if err = tx.QueryRowContext(ctx, `SELECT source_max_event_pk,committed_through_at,updated_at FROM aggregation_watermarks WHERE watermark_name=? FOR UPDATE`, name).Scan(&previous.SourceMaxEventPK, &previous.CommittedThrough, &previous.UpdatedAt); err != nil {
		return AggregationProgress{}, err
	}
	if safeBefore.Before(previous.CommittedThrough) {
		return previous, nil
	}
	if _, err = tx.ExecContext(ctx, `DROP TEMPORARY TABLE IF EXISTS aggregation_batch_events`); err != nil {
		return AggregationProgress{}, err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TEMPORARY TABLE aggregation_batch_events(event_pk BIGINT UNSIGNED NOT NULL PRIMARY KEY,received_at DATETIME(3) NOT NULL) ENGINE=InnoDB`); err != nil {
		return AggregationProgress{}, err
	}
	defer tx.ExecContext(context.Background(), `DROP TEMPORARY TABLE IF EXISTS aggregation_batch_events`)
	result, err := tx.ExecContext(ctx, `INSERT INTO aggregation_batch_events(event_pk,received_at) SELECT e.event_pk,e.received_at FROM usage_events e JOIN ingest_batches b ON b.batch_id=e.batch_id WHERE (e.received_at>? OR (e.received_at=? AND e.event_pk>?)) AND e.received_at<=? AND b.committed_at IS NOT NULL AND b.batch_status IN ('committed','partial') ORDER BY e.received_at,e.event_pk LIMIT ?`, previous.CommittedThrough, previous.CommittedThrough, previous.SourceMaxEventPK, safeBefore, limit)
	if err != nil {
		return AggregationProgress{}, err
	}
	selected, err := result.RowsAffected()
	if err != nil {
		return AggregationProgress{}, err
	}
	checkpointTime, checkpointPK := previous.CommittedThrough, previous.SourceMaxEventPK
	if selected > 0 {
		if err = upsertSelectedMetricsTx(ctx, tx); err != nil {
			return AggregationProgress{}, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT received_at,event_pk FROM aggregation_batch_events ORDER BY received_at DESC,event_pk DESC LIMIT 1`).Scan(&checkpointTime, &checkpointPK); err != nil {
			return AggregationProgress{}, err
		}
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE aggregation_watermarks SET source_max_event_pk=?,committed_through_at=?,updated_at=? WHERE watermark_name=?`, checkpointPK, checkpointTime, now, name); err != nil {
		return AggregationProgress{}, err
	}
	if _, err = tx.ExecContext(ctx, `DROP TEMPORARY TABLE aggregation_batch_events`); err != nil {
		return AggregationProgress{}, err
	}
	if err = tx.Commit(); err != nil {
		return AggregationProgress{}, err
	}
	return AggregationProgress{SourceMaxEventPK: checkpointPK, CommittedThrough: checkpointTime, UpdatedAt: now, EventCount: int(selected)}, nil
}

func (s *Store) CreateSnapshot(ctx context.Context, v *domain.LeaderboardSnapshot) error {
	_, e := s.db.ExecContext(ctx, `INSERT INTO leaderboard_snapshots(snapshot_id,board_key,scope_type,scope_key,metric_key,window_start,window_end,timezone_name,ranking_rule_version,participant_count,source_max_event_pk,data_watermark_at,snapshot_status,generated_at,published_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.SnapshotID, v.BoardKey, v.ScopeType, v.ScopeKey, v.MetricKey, v.WindowStart, v.WindowEnd, v.TimezoneName, v.RankingRuleVersion, v.ParticipantCount, v.SourceMaxEventPK, v.DataWatermarkAt, v.SnapshotStatus, v.GeneratedAt, v.PublishedAt)
	return e
}
func scanSnapshot(row interface{ Scan(...any) error }) (*domain.LeaderboardSnapshot, error) {
	v := new(domain.LeaderboardSnapshot)
	var p sql.NullTime
	e := row.Scan(&v.SnapshotID, &v.BoardKey, &v.ScopeType, &v.ScopeKey, &v.MetricKey, &v.WindowStart, &v.WindowEnd, &v.TimezoneName, &v.RankingRuleVersion, &v.ParticipantCount, &v.SourceMaxEventPK, &v.DataWatermarkAt, &v.SnapshotStatus, &v.GeneratedAt, &p)
	if p.Valid {
		v.PublishedAt = &p.Time
	}
	return v, e
}

const snapshotSelect = `SELECT snapshot_id,board_key,scope_type,scope_key,metric_key,window_start,window_end,timezone_name,ranking_rule_version,participant_count,source_max_event_pk,data_watermark_at,snapshot_status,generated_at,published_at FROM leaderboard_snapshots`

func (s *Store) GetSnapshot(ctx context.Context, id string) (*domain.LeaderboardSnapshot, error) {
	return scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelect+` WHERE snapshot_id=?`, id))
}
func (s *Store) PublishSnapshot(ctx context.Context, id string, t time.Time) error {
	r, e := s.db.ExecContext(ctx, `UPDATE leaderboard_snapshots SET snapshot_status='published',published_at=?,participant_count=(SELECT COUNT(*) FROM leaderboard_entries WHERE snapshot_id=?) WHERE snapshot_id=? AND snapshot_status='building'`, t, id, id)
	return affected(r, e)
}
func (s *Store) SupersedeSnapshot(ctx context.Context, id string) error {
	r, e := s.db.ExecContext(ctx, `UPDATE leaderboard_snapshots SET snapshot_status='superseded' WHERE snapshot_id=? AND snapshot_status='published'`, id)
	return affected(r, e)
}
func (s *Store) LatestPublishedSnapshot(ctx context.Context, b, scope, metric string) (*domain.LeaderboardSnapshot, error) {
	return scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelect+` WHERE board_key=? AND scope_type=? AND metric_key=? AND snapshot_status='published' ORDER BY published_at DESC,snapshot_id DESC LIMIT 1`, b, scope, metric))
}
func (s *Store) CreateEntry(ctx context.Context, e *domain.LeaderboardEntry) error {
	r, er := s.db.ExecContext(ctx, `INSERT INTO leaderboard_entries(snapshot_id,rank_no,user_id,metric_value,previous_rank_no,display_name_snapshot,avatar_url_snapshot,breakdown_json) SELECT ?,?,?,?,?,?,?,? FROM leaderboard_snapshots WHERE snapshot_id=? AND snapshot_status='building'`, e.SnapshotID, e.RankNo, e.UserID, e.MetricValue, e.PreviousRankNo, e.DisplayNameSnapshot, nullString(e.AvatarURLSnapshot), nullString(e.BreakdownJSON), e.SnapshotID)
	return affected(r, er)
}
func (s *Store) ListEntries(ctx context.Context, id string) ([]*domain.LeaderboardEntry, error) {
	r, e := s.db.QueryContext(ctx, `SELECT snapshot_id,rank_no,user_id,metric_value,previous_rank_no,display_name_snapshot,avatar_url_snapshot,breakdown_json FROM leaderboard_entries WHERE snapshot_id=? ORDER BY rank_no`, id)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var out []*domain.LeaderboardEntry
	for r.Next() {
		v := new(domain.LeaderboardEntry)
		var prev sql.NullInt64
		var av, br sql.NullString
		if e = r.Scan(&v.SnapshotID, &v.RankNo, &v.UserID, &v.MetricValue, &prev, &v.DisplayNameSnapshot, &av, &br); e != nil {
			return nil, e
		}
		if prev.Valid {
			x := uint32(prev.Int64)
			v.PreviousRankNo = &x
		}
		v.AvatarURLSnapshot = av.String
		v.BreakdownJSON = br.String
		out = append(out, v)
	}
	return out, r.Err()
}

func (s *Store) LatestPublishedSnapshotScoped(ctx context.Context, board, scopeType, scopeKey, metric string) (*domain.LeaderboardSnapshot, error) {
	return scanSnapshot(s.db.QueryRowContext(ctx, snapshotSelect+` WHERE board_key=? AND scope_type=? AND scope_key=? AND metric_key=? AND snapshot_status='published' ORDER BY published_at DESC,snapshot_id DESC LIMIT 1`, board, scopeType, scopeKey, metric))
}

func (s *Store) UserAllowedInScope(ctx context.Context, userID, scopeType, scopeKey string) (bool, error) {
	var count int
	switch scopeType {
	case "global":
		if scopeKey != "all" {
			return false, nil
		}
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE user_id=? AND account_status='active' AND leaderboard_visibility='public'`, userID).Scan(&count)
		return count == 1, err
	case "private":
		if userID != scopeKey {
			return false, nil
		}
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE user_id=? AND account_status='active'`, userID).Scan(&count)
		return count == 1, err
	case "team":
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships tm JOIN users u ON u.user_id=tm.user_id WHERE tm.team_id=? AND tm.user_id=? AND tm.membership_status='active' AND u.account_status='active' AND u.leaderboard_visibility IN ('public','team')`, scopeKey, userID).Scan(&count)
		return count == 1, err
	default:
		return false, nil
	}
}

func (s *Store) AuthorizeLeaderboardScope(ctx context.Context, userID, scopeType, scopeKey string) (bool, error) {
	if scopeType == "global" {
		return scopeKey == "all", nil
	}
	if userID == "" {
		return false, nil
	}
	if scopeType == "private" {
		return userID == scopeKey, nil
	}
	if scopeType != "team" {
		return false, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE team_id=? AND user_id=? AND membership_status='active'`, scopeKey, userID).Scan(&count)
	return count == 1, err
}

func (s *Store) ListLeaderboardScopes(ctx context.Context) ([]ScopeRef, error) {
	out := []ScopeRef{{Type: "global", Key: "all"}}
	rows, err := s.db.QueryContext(ctx, `SELECT 'private',user_id FROM users WHERE account_status='active' UNION ALL SELECT 'team',team_id FROM teams ORDER BY 1,2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v ScopeRef
		if err = rows.Scan(&v.Type, &v.Key); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PublishSnapshotAtomic(ctx context.Context, watermarkName string, expected AggregationProgress, snap *domain.LeaderboardSnapshot, entries []*domain.LeaderboardEntry) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current AggregationProgress
	if err = tx.QueryRowContext(ctx, `SELECT source_max_event_pk,committed_through_at,updated_at FROM aggregation_watermarks WHERE watermark_name=? FOR UPDATE`, watermarkName).Scan(&current.SourceMaxEventPK, &current.CommittedThrough, &current.UpdatedAt); err != nil {
		return err
	}
	if current.SourceMaxEventPK != expected.SourceMaxEventPK || !current.CommittedThrough.Equal(expected.CommittedThrough) {
		return fmt.Errorf("aggregation watermark changed during snapshot build")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO leaderboard_snapshots(snapshot_id,board_key,scope_type,scope_key,metric_key,window_start,window_end,timezone_name,ranking_rule_version,participant_count,source_max_event_pk,data_watermark_at,snapshot_status,generated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?, 'building',?)`, snap.SnapshotID, snap.BoardKey, snap.ScopeType, snap.ScopeKey, snap.MetricKey, snap.WindowStart, snap.WindowEnd, snap.TimezoneName, snap.RankingRuleVersion, len(entries), expected.SourceMaxEventPK, expected.CommittedThrough, snap.GeneratedAt); err != nil {
		if isDuplicate(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO leaderboard_entries(snapshot_id,rank_no,user_id,metric_value,previous_rank_no,display_name_snapshot,avatar_url_snapshot,breakdown_json) VALUES(?,?,?,?,?,?,?,?)`, entry.SnapshotID, entry.RankNo, entry.UserID, entry.MetricValue, entry.PreviousRankNo, entry.DisplayNameSnapshot, nullString(entry.AvatarURLSnapshot), nullString(entry.BreakdownJSON)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE leaderboard_snapshots SET snapshot_status='superseded' WHERE board_key=? AND scope_type=? AND scope_key=? AND metric_key=? AND snapshot_status='published'`, snap.BoardKey, snap.ScopeType, snap.ScopeKey, snap.MetricKey); err != nil {
		return err
	}
	publishedAt := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE leaderboard_snapshots SET snapshot_status='published',published_at=? WHERE snapshot_id=? AND snapshot_status='building'`, publishedAt, snap.SnapshotID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateDeletionRequest(ctx context.Context, request DeletionRequest) error {
	if request.Scope == "time" {
		request.Scope = "time_range"
	}
	if request.Scope == "all" {
		request.Scope = "all_usage"
	}
	if request.Scope != "installation" && request.Scope != "time_range" && request.Scope != "all_usage" && request.Scope != "account" {
		return fmt.Errorf("invalid deletion scope")
	}
	if request.UserID == "" {
		return fmt.Errorf("user id is required")
	}
	if request.Scope == "installation" {
		var owner string
		if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM installations WHERE installation_id=?`, request.InstallationID).Scan(&owner); err != nil {
			return err
		}
		if owner != request.UserID {
			return fmt.Errorf("installation is not owned by user")
		}
	}
	if request.Scope == "time_range" && (request.RangeStart == nil || request.RangeEnd == nil || !request.RangeEnd.After(*request.RangeStart)) {
		return fmt.Errorf("valid rangeStart and rangeEnd are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO data_deletion_requests(request_id,user_id,installation_id,deletion_scope,range_start,range_end,request_status,requested_at) VALUES(?,?,?,?,?,?,'pending',?)`, request.RequestID, request.UserID, nullString(request.InstallationID), request.Scope, request.RangeStart, request.RangeEnd, request.RequestedAt)
	return err
}

func (s *Store) GetDeletionRequest(ctx context.Context, requestID, userID string) (DeletionRequest, error) {
	var request DeletionRequest
	var installation sql.NullString
	var start, end sql.NullTime
	var errorCode sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT request_id,user_id,installation_id,deletion_scope,range_start,range_end,request_status,error_code,requested_at FROM data_deletion_requests WHERE request_id=? AND user_id=?`, requestID, userID).Scan(&request.RequestID, &request.UserID, &installation, &request.Scope, &start, &end, &request.Status, &errorCode, &request.RequestedAt)
	request.ErrorCode = errorCode.String
	request.InstallationID = installation.String
	if start.Valid {
		request.RangeStart = &start.Time
	}
	if end.Valid {
		request.RangeEnd = &end.Time
	}
	return request, err
}

func (s *Store) ProcessNextDeletion(ctx context.Context, watermarkName string) (string, error) {
	requestID, err := s.processNextDeletion(ctx, watermarkName)
	if err == nil || requestID == "" {
		return requestID, err
	}
	if markErr := s.markDeletionFailed(ctx, requestID, "DELETION_PROCESSING_FAILED"); markErr != nil {
		return requestID, errors.Join(err, fmt.Errorf("mark deletion failed: %w", markErr))
	}
	return requestID, err
}

func (s *Store) processNextDeletion(ctx context.Context, watermarkName string) (string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var request DeletionRequest
	var installation sql.NullString
	var start, end sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT request_id,user_id,installation_id,deletion_scope,range_start,range_end,request_status,requested_at FROM data_deletion_requests WHERE request_status='pending' ORDER BY requested_at,request_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&request.RequestID, &request.UserID, &installation, &request.Scope, &start, &end, &request.Status, &request.RequestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	request.InstallationID = installation.String
	if start.Valid {
		request.RangeStart = &start.Time
	}
	if end.Valid {
		request.RangeEnd = &end.Time
	}
	if _, err = tx.ExecContext(ctx, `UPDATE data_deletion_requests SET request_status='running',started_at=CURRENT_TIMESTAMP(3),error_code=NULL WHERE request_id=?`, request.RequestID); err != nil {
		return request.RequestID, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO aggregation_watermarks(watermark_name,committed_through_at) VALUES(?,?)`, watermarkName, time.Unix(0, 0).UTC()); err != nil {
		return request.RequestID, err
	}
	var progress AggregationProgress
	if err = tx.QueryRowContext(ctx, `SELECT source_max_event_pk,committed_through_at,updated_at FROM aggregation_watermarks WHERE watermark_name=? FOR UPDATE`, watermarkName).Scan(&progress.SourceMaxEventPK, &progress.CommittedThrough, &progress.UpdatedAt); err != nil {
		return request.RequestID, err
	}
	dateQuery := `SELECT DISTINCT occurred_date FROM usage_events WHERE user_id=?`
	dateArgs := []any{request.UserID}
	switch request.Scope {
	case "installation":
		dateQuery += ` AND installation_id=?`
		dateArgs = append(dateArgs, request.InstallationID)
	case "time_range":
		if request.RangeStart == nil || request.RangeEnd == nil {
			return request.RequestID, fmt.Errorf("deletion range is missing")
		}
		dateQuery += ` AND occurred_at>=? AND occurred_at<?`
		dateArgs = append(dateArgs, *request.RangeStart, *request.RangeEnd)
	}
	dateQuery += ` ORDER BY occurred_date`
	dateRows, err := tx.QueryContext(ctx, dateQuery, dateArgs...)
	if err != nil {
		return request.RequestID, err
	}
	var affectedDates []time.Time
	for dateRows.Next() {
		var date time.Time
		if err = dateRows.Scan(&date); err != nil {
			dateRows.Close()
			return request.RequestID, err
		}
		affectedDates = append(affectedDates, date)
	}
	if err = dateRows.Close(); err != nil {
		return request.RequestID, err
	}
	switch request.Scope {
	case "installation":
		if _, err = tx.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id=? AND installation_id=?`, request.UserID, request.InstallationID); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE installations SET installation_status='disabled',revoked_at=COALESCE(revoked_at,CURRENT_TIMESTAMP(3)) WHERE installation_id=? AND user_id=?`, request.InstallationID, request.UserID)
		}
	case "time_range":
		_, err = tx.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id=? AND occurred_at>=? AND occurred_at<?`, request.UserID, *request.RangeStart, *request.RangeEnd)
	case "all_usage", "account":
		_, err = tx.ExecContext(ctx, `DELETE FROM usage_events WHERE user_id=?`, request.UserID)
		if err == nil && request.Scope == "account" {
			_, err = tx.ExecContext(ctx, `UPDATE installations SET installation_status='disabled',revoked_at=COALESCE(revoked_at,CURRENT_TIMESTAMP(3)) WHERE user_id=?`, request.UserID)
		}
		if err == nil && request.Scope == "account" {
			_, err = tx.ExecContext(ctx, `UPDATE users SET auth_subject_hash=RANDOM_BYTES(32),email_lookup_hash=NULL,email_ciphertext=NULL,display_name='Deleted User',avatar_url=NULL,account_status='deleted',leaderboard_visibility='private',deleted_at=CURRENT_TIMESTAMP(3) WHERE user_id=?`, request.UserID)
		}
	default:
		err = fmt.Errorf("invalid deletion scope %q", request.Scope)
	}
	if err != nil {
		return request.RequestID, err
	}
	for _, date := range affectedDates {
		if err = rebuildUserMetricDateTx(ctx, tx, request.UserID, date, progress); err != nil {
			return request.RequestID, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE leaderboard_snapshots SET snapshot_status=CASE WHEN snapshot_status='building' THEN 'failed' ELSE 'superseded' END WHERE snapshot_status IN ('building','published')`); err != nil {
		return request.RequestID, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE data_deletion_requests SET request_status='completed',completed_at=CURRENT_TIMESTAMP(3) WHERE request_id=?`, request.RequestID); err != nil {
		return request.RequestID, err
	}
	if err = tx.Commit(); err != nil {
		return request.RequestID, err
	}
	return request.RequestID, nil
}

func (s *Store) markDeletionFailed(ctx context.Context, requestID, errorCode string) error {
	failureCtx := context.WithoutCancel(ctx)
	tx, err := s.db.BeginTx(failureCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(failureCtx, `UPDATE data_deletion_requests SET request_status='failed',completed_at=CURRENT_TIMESTAMP(3),error_code=? WHERE request_id=? AND request_status IN ('pending','running')`, errorCode, requestID); err != nil {
		return err
	}
	return tx.Commit()
}
func affected(r sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func isDuplicate(e error) bool {
	var me *mysql.MySQLError
	return errors.As(e, &me) && me.Number == 1062
}
func isRetryableTransaction(e error) bool {
	var me *mysql.MySQLError
	return errors.As(e, &me) && (me.Number == 1205 || me.Number == 1213)
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func bytesPtr(v *[32]byte) any {
	if v == nil {
		return nil
	}
	return v[:]
}
func hashPtr(v []byte) *[32]byte {
	if len(v) == 0 {
		return nil
	}
	var x [32]byte
	copy(x[:], v)
	return &x
}
func strPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	x := v.String
	return &x
}
func u64Ptr(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	x := uint64(v.Int64)
	return &x
}
func boolPtr(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	x := v.Bool
	return &x
}
