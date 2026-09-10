package grayscale

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	mysqlstore "tokendance/internal/store/mysql"
)

type Mirror struct {
	cfg    Config
	source *sql.DB
	target *sql.DB
	now    func() time.Time
}

type Result struct {
	Users          []RankedUser
	Since          string
	Copied         map[string]int64
	RankingQueued  int
	CommunityDates []string
}

type RankedUser struct {
	UserID     string
	TokenTotal uint64
	Handle     string
}

func New(cfg Config, source, target *sql.DB) (*Mirror, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Mirror{cfg: cfg, source: source, target: target, now: time.Now}, nil
}

func (m *Mirror) RunLoop(ctx context.Context) error {
	for {
		result, err := m.Run(ctx)
		if err != nil {
			return err
		}
		ids := make([]string, len(result.Users))
		for i, user := range result.Users {
			ids[i] = user.UserID
		}
		log.Printf("grayscale mirror copied %d users [%s] since %s: %v", len(result.Users), printableSummary(ids), result.Since, result.Copied)
		if !m.cfg.Loop {
			return nil
		}
		timer := time.NewTimer(m.cfg.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *Mirror) Run(ctx context.Context) (Result, error) {
	now := m.now().UTC()
	generation := domain.DayDate(now)
	users, err := m.selectTop(ctx, generation)
	if err != nil {
		return Result{}, err
	}
	if len(users) == 0 {
		return Result{Copied: map[string]int64{}}, fmt.Errorf("no eligible all-time scores for generation %s", generation)
	}
	ids := make([]string, len(users))
	for i, user := range users {
		ids[i] = user.UserID
	}
	if err := m.ensureStateTable(ctx); err != nil {
		return Result{}, err
	}
	since, err := m.refreshSince(ctx, ids, generation)
	if err != nil {
		return Result{}, err
	}
	result := Result{Users: users, Since: since, Copied: map[string]int64{}}

	tx, err := m.target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Result{}, fmt.Errorf("begin target transaction: %w", err)
	}
	defer tx.Rollback()

	handles, err := m.copyUsers(ctx, tx, ids)
	if err != nil {
		return Result{}, err
	}
	result.Copied["users"] = int64(len(handles))

	for _, spec := range []copySpec{
		{table: "user_privacy_settings"},
		{table: "installations", transform: func(columns []string, values []any) error {
			sanitizeInstallationRow(columns, values)
			return nil
		}},
		{table: "installation_adapter_status"},
		{table: "daily_user_agent_metrics", dateColumn: "metric_date"},
		{table: "daily_user_agent_model_metrics", dateColumn: "metric_date"},
		{table: "daily_skill_metrics", dateColumn: "metric_date"},
		{table: "device_daily_aggregates", dateColumn: "metric_date"},
		{table: "user_window_scores"},
	} {
		if spec.table == "installation_adapter_status" {
			written, err := m.copyAdapterStatus(ctx, tx, ids)
			if err != nil {
				return Result{}, err
			}
			result.Copied[spec.table] = written
			continue
		}
		written, err := copyTable(ctx, m.source, tx, m.cfg, spec, ids, sinceFor(spec, since))
		if err != nil {
			return Result{}, err
		}
		result.Copied[spec.table] = written
	}

	if err := m.copyPublicProfiles(ctx, tx, ids, handles); err != nil {
		return Result{}, err
	}
	queued, err := m.enqueueRanking(ctx, tx, ids, now)
	if err != nil {
		return Result{}, err
	}
	result.RankingQueued = queued
	dates := refreshDates(since, generation)
	if err := m.enqueueCommunity(ctx, tx, dates, now); err != nil {
		return Result{}, err
	}
	result.CommunityDates = dates
	if err := m.writePassword(ctx, tx, ids, now); err != nil {
		return Result{}, err
	}
	if err := m.saveState(ctx, tx, ids, generation, now); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit target transaction: %w", err)
	}
	return result, nil
}

func sinceFor(spec copySpec, since string) string {
	if spec.dateColumn == "" {
		return ""
	}
	return since
}

func (m *Mirror) selectTop(ctx context.Context, generation string) ([]RankedUser, error) {
	rows, err := m.source.QueryContext(ctx, `
		SELECT s.user_id, s.token_total, COALESCE(NULLIF(TRIM(u.handle), ''), '')
		FROM `+quote(m.cfg.SourceSchema)+`.user_window_scores s
		JOIN `+quote(m.cfg.SourceSchema)+`.users u ON u.user_id = s.user_id AND u.account_status = 'active'
		WHERE s.window_key = 'all' AND s.generation = ? AND s.eligible = TRUE
		ORDER BY s.token_total DESC, s.registered_at ASC, s.user_id ASC
		LIMIT ?`, generation, m.cfg.Limit)
	if err != nil {
		return nil, fmt.Errorf("select top users: %w", err)
	}
	defer rows.Close()
	var users []RankedUser
	for rows.Next() {
		var user RankedUser
		if err := rows.Scan(&user.UserID, &user.TokenTotal, &user.Handle); err != nil {
			return nil, fmt.Errorf("scan top user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (m *Mirror) copyUsers(ctx context.Context, tx *sql.Tx, ids []string) (map[string]string, error) {
	taken, err := m.takenHandles(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	handles := make(map[string]string, len(ids))
	written, err := copyTable(ctx, m.source, tx, m.cfg, copySpec{
		table: "users",
		transform: func(columns []string, values []any) error {
			userID := asString(values[columnIndex(columns, "user_id")])
			handles[userID] = sanitizeUserRow(columns, values, taken)
			return nil
		},
	}, ids, "")
	if err != nil {
		return nil, err
	}
	if written != int64(len(ids)) {
		return nil, fmt.Errorf("expected %d users, copied %d", len(ids), written)
	}
	return handles, nil
}

func (m *Mirror) takenHandles(ctx context.Context, tx *sql.Tx, ids []string) (map[string]string, error) {
	taken := map[string]string{}
	queries := []string{
		`SELECT handle, user_id FROM ` + quote(m.cfg.TargetSchema) + `.users
		 WHERE handle IS NOT NULL AND user_id NOT IN ` + inClause(len(ids)),
		`SELECT handle, user_id FROM ` + quote(m.cfg.TargetSchema) + `.public_user_profiles
		 WHERE handle IS NOT NULL AND user_id NOT IN ` + inClause(len(ids)),
	}
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query, anyStrings(ids)...)
		if err != nil {
			return nil, fmt.Errorf("list target handles: %w", err)
		}
		for rows.Next() {
			var handle, userID string
			if err := rows.Scan(&handle, &userID); err != nil {
				rows.Close()
				return nil, err
			}
			taken[handle] = userID
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return taken, nil
}

func (m *Mirror) copyPublicProfiles(ctx context.Context, tx *sql.Tx, ids []string, handles map[string]string) error {
	_, err := copyTable(ctx, m.source, tx, m.cfg, copySpec{
		table: "public_user_profiles",
		transform: func(columns []string, values []any) error {
			userID := asString(values[columnIndex(columns, "user_id")])
			sanitizePublicProfileRow(columns, values, handles[userID])
			return nil
		},
	}, ids, "")
	return err
}

func (m *Mirror) copyAdapterStatus(ctx context.Context, tx *sql.Tx, ids []string) (int64, error) {
	sourceMeta, err := loadTableMeta(ctx, m.source, m.cfg.SourceSchema, "installation_adapter_status")
	if err != nil {
		return 0, err
	}
	targetMeta, err := loadTableMeta(ctx, tx, m.cfg.TargetSchema, "installation_adapter_status")
	if err != nil {
		return 0, err
	}
	columns := intersectColumns(sourceMeta, targetMeta)
	selectSQL := "SELECT " + quotedList(columns) + " FROM " + quote(m.cfg.SourceSchema) + ".installation_adapter_status a" +
		" WHERE a.installation_id IN (SELECT installation_id FROM " + quote(m.cfg.SourceSchema) + ".installations WHERE user_id IN " + inClause(len(ids)) + ")"
	rows, err := m.source.QueryContext(ctx, selectSQL, anyStrings(ids)...)
	if err != nil {
		return 0, fmt.Errorf("query installation_adapter_status: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, upsertSQL(m.cfg.TargetSchema, "installation_adapter_status", columns, targetMeta.pk))
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var written int64
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return written, err
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			return written, fmt.Errorf("upsert installation_adapter_status: %w", err)
		}
		written++
	}
	return written, rows.Err()
}

func (m *Mirror) enqueueRanking(ctx context.Context, tx *sql.Tx, ids []string, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id, window_key, generation, token_total, revision
		FROM `+quote(m.cfg.TargetSchema)+`.user_window_scores
		WHERE user_id IN `+inClause(len(ids)), anyStrings(ids)...)
	if err != nil {
		return 0, fmt.Errorf("list window scores: %w", err)
	}
	type score struct {
		userID, window, generation string
		tokens, revision           uint64
	}
	var scores []score
	for rows.Next() {
		var row score
		if err := rows.Scan(&row.userID, &row.window, &row.generation, &row.tokens, &row.revision); err != nil {
			rows.Close()
			return 0, err
		}
		scores = append(scores, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, row := range scores {
		taskID, err := newTaskID("rob_")
		if err != nil {
			return queued, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+quote(m.cfg.TargetSchema)+`.ranking_outbox (
				task_id, user_id, window_key, generation, token_total, revision,
				op_type, task_status, next_attempt_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'upsert', 'pending', ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				token_total = VALUES(token_total),
				task_status = 'pending',
				applied_at = NULL,
				claim_token = NULL,
				locked_by = NULL,
				lease_expires_at = NULL,
				next_attempt_at = VALUES(next_attempt_at),
				updated_at = VALUES(updated_at)`,
			taskID, row.userID, row.window, row.generation, row.tokens, row.revision, now, now, now,
		); err != nil {
			return queued, fmt.Errorf("enqueue ranking outbox: %w", err)
		}
		queued++
	}
	return queued, nil
}

func (m *Mirror) enqueueCommunity(ctx context.Context, tx *sql.Tx, dates []string, now time.Time) error {
	pending := make([]string, 0, len(dates))
	for _, date := range dates {
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM `+quote(m.cfg.TargetSchema)+`.community_stats_outbox
				WHERE metric_date = ? AND task_status IN ('pending', 'leased')
			)`, date).Scan(&exists); err != nil {
			return fmt.Errorf("check community outbox: %w", err)
		}
		if exists == 0 {
			pending = append(pending, date)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	return mysqlstore.EnqueueCommunityStatsOutboxTx(ctx, tx, pending, now)
}

func (m *Mirror) writePassword(ctx context.Context, tx *sql.Tx, ids []string, now time.Time) error {
	if strings.TrimSpace(m.cfg.Password) == "" {
		return nil
	}
	hash, err := crypto.HashPassword(m.cfg.Password, crypto.DefaultArgon2Params)
	if err != nil {
		return fmt.Errorf("hash grayscale password: %w", err)
	}
	for _, userID := range ids {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+quote(m.cfg.TargetSchema)+`.user_password_credentials (
				user_id, password_hash, password_algorithm, credential_version,
				failed_login_count, password_changed_at, created_at, updated_at
			) VALUES (?, ?, 'argon2id', 1, 0, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				password_hash = VALUES(password_hash),
				credential_version = credential_version + 1,
				failed_login_count = 0,
				locked_until = NULL,
				password_changed_at = VALUES(password_changed_at),
				updated_at = VALUES(updated_at)`,
			userID, hash, now, now, now,
		); err != nil {
			return fmt.Errorf("upsert grayscale password: %w", err)
		}
	}
	return nil
}

func (m *Mirror) ensureStateTable(ctx context.Context) error {
	_, err := m.target.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+quote(m.cfg.TargetSchema)+`.`+quote(stateTable)+` (
			state_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			state_value VARCHAR(1024) NOT NULL,
			updated_at DATETIME(3) NOT NULL,
			PRIMARY KEY (state_key)
		) ENGINE=InnoDB`)
	if err != nil {
		return fmt.Errorf("create mirror state table: %w", err)
	}
	return nil
}

func (m *Mirror) refreshSince(ctx context.Context, ids []string, today string) (string, error) {
	fullHistory := "1000-01-01"
	var storedUsers, storedDay sql.NullString
	err := m.target.QueryRowContext(ctx, `
		SELECT
			(SELECT state_value FROM `+quote(m.cfg.TargetSchema)+`.`+quote(stateTable)+` WHERE state_key = 'users'),
			(SELECT state_value FROM `+quote(m.cfg.TargetSchema)+`.`+quote(stateTable)+` WHERE state_key = 'copied_through')`,
	).Scan(&storedUsers, &storedDay)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("read mirror state: %w", err)
	}
	if !storedUsers.Valid || storedUsers.String != strings.Join(ids, ",") {
		return fullHistory, nil
	}
	refreshFrom := domain.DayDate(m.now().UTC().AddDate(0, 0, -(m.cfg.RefreshDays - 1)))
	if storedDay.Valid && storedDay.String < refreshFrom {
		return storedDay.String, nil
	}
	return refreshFrom, nil
}

func (m *Mirror) saveState(ctx context.Context, tx *sql.Tx, ids []string, today string, now time.Time) error {
	pairs := [][2]string{
		{"users", strings.Join(ids, ",")},
		{"copied_through", today},
	}
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO `+quote(m.cfg.TargetSchema)+`.`+quote(stateTable)+` (state_key, state_value, updated_at)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE state_value = VALUES(state_value), updated_at = VALUES(updated_at)`,
			pair[0], pair[1], now,
		); err != nil {
			return fmt.Errorf("save mirror state: %w", err)
		}
	}
	return nil
}

func refreshDates(since, today string) []string {
	start, err := time.Parse("2006-01-02", since)
	if err != nil || since <= "1000-01-01" {
		start, _ = time.Parse("2006-01-02", today)
		start = start.AddDate(0, 0, -1)
	}
	end, err := time.Parse("2006-01-02", today)
	if err != nil {
		return []string{today}
	}
	if start.After(end) {
		start = end
	}
	var dates []string
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day.Format("2006-01-02"))
		if len(dates) > 14 {
			dates = dates[len(dates)-14:]
		}
	}
	return dates
}

func newTaskID(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", err
	}
	return prefix + token, nil
}
