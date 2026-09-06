package mysql

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"tokendance/internal/domain"
)

func (s *ingestStore) CommitAggregate(ctx context.Context, in domain.AggregateCommit) (*domain.AggregateAck, error) {
	if err := in.Snapshot.Validate(in.ReceivedAt); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var userID, status, account string
	err = tx.QueryRowContext(ctx, "SELECT user_id,installation_status FROM installations WHERE installation_id=? FOR UPDATE", in.InstallationID).Scan(&userID, &status)
	if err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, domain.ErrDeviceDisabled
	}
	if err = tx.QueryRowContext(ctx, "SELECT account_status FROM users WHERE user_id=? FOR UPDATE", userID).Scan(&account); err != nil {
		return nil, err
	}
	if account != "active" {
		return nil, domain.ErrAccountSuspended
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ingest_nonces(installation_id,nonce_hash,expires_at,created_at) VALUES(?,?,?,?)", in.InstallationID, in.NonceHash[:], in.ReceivedAt.Add(10*time.Minute), in.ReceivedAt); err != nil {
		if isDuplicateKey(err) {
			return nil, domain.ErrNonceReplay
		}
		return nil, err
	}
	var revision int64
	var hash []byte
	err = tx.QueryRowContext(ctx, "SELECT revision,content_hash FROM device_daily_aggregates WHERE installation_id=? AND metric_date=? FOR UPDATE", in.InstallationID, in.Snapshot.Day).Scan(&revision, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && revision > in.Snapshot.Revision {
		return nil, domain.NewAppError(409, "AGGREGATE_STALE_REVISION", "sync.staleRevision", "newer aggregate already stored", nil, domain.ErrInvalidArgument)
	}
	if err == nil && revision == in.Snapshot.Revision && hex.EncodeToString(hash) != hex.EncodeToString(in.Digest[:]) {
		return nil, domain.NewAppError(409, "AGGREGATE_REVISION_CONFLICT", "sync.revisionConflict", "revision content differs", nil, domain.ErrInvalidArgument)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// A partially rebuilt day must not erase previously ingested usage. Further
		// corrections are allowed only after the day has a published aggregate base.
		rows, e := tx.QueryContext(ctx, `SELECT agent_id,COALESCE(SUM(CASE WHEN accuracy IN ('exact','derived') THEN COALESCE(token_total, COALESCE(token_input,0)+COALESCE(token_output,0)+COALESCE(token_cache_read,0)+COALESCE(token_cache_write,0)+COALESCE(token_reasoning,0)) ELSE 0 END),0) FROM usage_events WHERE installation_id=? AND occurred_date=? GROUP BY agent_id`, in.InstallationID, in.Snapshot.Day)
		if e != nil {
			return nil, e
		}
		totals := map[string]string{}
		for rows.Next() {
			var agent, total string
			if e = rows.Scan(&agent, &total); e != nil {
				rows.Close()
				return nil, e
			}
			totals[agent] = total
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for agent, total := range totals {
			if !coversAggregate(in.Snapshot, agent, total) {
				return nil, domain.NewAppError(409, "AGGREGATE_COVERAGE_INCOMPLETE", "sync.incompleteCoverage", "historical source coverage is incomplete", nil, domain.ErrInvalidArgument)
			}
		}
	}
	if revision != in.Snapshot.Revision {
		payload, e := json.Marshal(in.Snapshot.Rows)
		if e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO device_daily_aggregates(installation_id,user_id,metric_date,revision,content_hash,payload_json,updated_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE revision=VALUES(revision),content_hash=VALUES(content_hash),payload_json=VALUES(payload_json),updated_at=VALUES(updated_at)`, in.InstallationID, userID, in.Snapshot.Day, in.Snapshot.Revision, in.Digest[:], payload, in.ReceivedAt)
		if err != nil {
			return nil, err
		}
		if err = MarkAggregateDirtyDayTx(ctx, tx, userID, in.Snapshot.Day, in.ReceivedAt); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &domain.AggregateAck{Day: in.Snapshot.Day, Revision: in.Snapshot.Revision, SHA256: hex.EncodeToString(in.Digest[:])}, nil
}
