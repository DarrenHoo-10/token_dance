package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"tokendance/internal/domain"
)

const (
	teamExportLeaseDuration = 5 * time.Minute
	teamExportMaxAttempts   = 5
	teamExportObjectTTL     = 24 * time.Hour
)

type teamExportClaim struct {
	exportID              string
	teamID                string
	requesterUserID       string
	requesterMembershipID string
	snapshotID            string
	authRevision          uint64
	kind                  string
	filterJSON            []byte
	leaseToken            string
	leaseGeneration       uint64
	attemptCount          uint16
}

type teamExportAuth struct {
	ok           bool
	revoke       bool
	teamStatus   string
	role         domain.TeamPublicRole
	membershipID string
	authRevision uint64
}

func (w *Worker) ProcessTeamExports(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	completed := 0
	var firstErr error
	for i := 0; i < 3; i++ {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		claim, err := w.claimTeamExportJob(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		if err := w.executeTeamExport(ctx, claim); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			code := "TEAM_EXPORT_FAILED"
			if errors.Is(err, errTeamExportRevoked) {
				if markErr := w.revokeTeamExport(context.Background(), claim, "TEAM_EXPORT_REVOKED"); markErr != nil && firstErr == nil {
					firstErr = fmt.Errorf("revoke team export: %v; persist: %w", err, markErr)
				}
				continue
			}
			if markErr := w.failTeamExportJob(context.Background(), claim, code); markErr != nil && firstErr == nil {
				firstErr = fmt.Errorf("team export failed: %v; persist: %w", err, markErr)
			} else if firstErr == nil {
				firstErr = err
			}
			continue
		}
		completed++
	}
	return completed, firstErr
}

var errTeamExportRevoked = errors.New("team export revoked")

func (w *Worker) claimTeamExportJob(ctx context.Context) (*teamExportClaim, error) {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin team export claim: %w", err)
	}
	defer tx.Rollback()

	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	row := tx.QueryRowContext(ctx, `
		SELECT export_id, team_id, requester_user_id, requester_membership_id,
		       snapshot_id, auth_revision, export_kind, filter_json, attempt_count
		FROM team_export_jobs
		WHERE (
		    (status = 'queued' AND next_attempt_at <= ?)
		    OR (status = 'running' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
		)
		ORDER BY created_at ASC, export_id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now, now)

	claim := &teamExportClaim{}
	if err := row.Scan(
		&claim.exportID,
		&claim.teamID,
		&claim.requesterUserID,
		&claim.requesterMembershipID,
		&claim.snapshotID,
		&claim.authRevision,
		&claim.kind,
		&claim.filterJSON,
		&claim.attemptCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("select team export claim: %w", err)
	}
	if claim.attemptCount >= teamExportMaxAttempts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_export_jobs
			SET status = 'failed', lease_token = NULL, lease_expires_at = NULL,
			    error_code = 'TEAM_EXPORT_MAX_ATTEMPTS'
			WHERE export_id = ?`, claim.exportID); err != nil {
			return nil, fmt.Errorf("mark team export exhausted: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit exhausted team export: %w", err)
		}
		return nil, sql.ErrNoRows
	}

	token, err := newTeamLeaseToken("tex_")
	if err != nil {
		return nil, err
	}
	claim.leaseToken = token
	leaseUntil := now.Add(teamExportLeaseDuration)
	res, err := tx.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'running',
		    lease_token = ?,
		    lease_generation = lease_generation + 1,
		    lease_expires_at = ?,
		    attempt_count = attempt_count + 1,
		    error_code = NULL
		WHERE export_id = ?
		  AND (
		    (status = 'queued' AND next_attempt_at <= ?)
		    OR (status = 'running' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
		  )`, token, leaseUntil, claim.exportID, now, now)
	if err != nil {
		return nil, fmt.Errorf("update team export claim: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return nil, fmt.Errorf("team export claim lost: %w", err)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT lease_generation FROM team_export_jobs WHERE export_id = ?",
		claim.exportID,
	).Scan(&claim.leaseGeneration); err != nil {
		return nil, fmt.Errorf("read team export generation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit team export claim: %w", err)
	}
	return claim, nil
}

func (w *Worker) executeTeamExport(ctx context.Context, claim *teamExportClaim) error {
	auth, err := w.authorizeTeamExport(ctx, claim)
	if err != nil {
		return err
	}
	if !auth.ok {
		return errTeamExportRevoked
	}

	payload, err := w.buildTeamExportCSV(ctx, claim)
	if err != nil {
		return err
	}

	objectKey := fmt.Sprintf("team-exports/%s/%s/%d.csv", claim.teamID, claim.exportID, claim.leaseGeneration)
	if err := w.storage.PutObject(ctx, objectKey, bytes.NewReader(payload), int64(len(payload)), "text/csv; charset=utf-8"); err != nil {
		return fmt.Errorf("upload team export object: %w", err)
	}

	auth, err = w.authorizeTeamExport(ctx, claim)
	if err != nil {
		return err
	}
	if !auth.ok {
		return errTeamExportRevoked
	}
	return w.completeTeamExportJob(ctx, claim, objectKey, sha256.Sum256(payload), uint64(len(payload)))
}

func (w *Worker) authorizeTeamExport(ctx context.Context, claim *teamExportClaim) (teamExportAuth, error) {
	var (
		teamStatus   string
		ownerUserID  string
		authRevision uint64
		membershipID sql.NullString
		baseRole     sql.NullString
		endedAt      sql.NullTime
		account      sql.NullString
	)
	err := w.db.QueryRowContext(ctx, `
		SELECT t.status, t.owner_user_id, t.auth_revision,
		       m.membership_id, m.base_role, m.ended_at, u.account_status
		FROM teams t
		LEFT JOIN team_memberships m
		  ON m.team_id = t.team_id AND m.membership_id = ? AND m.user_id = ?
		LEFT JOIN users u ON u.user_id = ?
		WHERE t.team_id = ? AND EXISTS (
			SELECT 1 FROM team_analysis_snapshots s
			WHERE s.snapshot_id = ? AND s.team_id = t.team_id AND s.rule_version = ?)`,
		claim.requesterMembershipID, claim.requesterUserID, claim.requesterUserID, claim.teamID,
		claim.snapshotID, domain.TeamAnalysisRuleVersion,
	).Scan(&teamStatus, &ownerUserID, &authRevision, &membershipID, &baseRole, &endedAt, &account)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return teamExportAuth{revoke: true}, nil
		}
		return teamExportAuth{}, fmt.Errorf("authorize team export: %w", err)
	}
	return decideTeamExportAuth(claim, teamStatus, ownerUserID, authRevision, membershipID, baseRole, endedAt, account), nil
}

func decideTeamExportAuth(
	claim *teamExportClaim,
	teamStatus, ownerUserID string,
	authRevision uint64,
	membershipID sql.NullString,
	baseRole sql.NullString,
	endedAt sql.NullTime,
	account sql.NullString,
) teamExportAuth {
	out := teamExportAuth{
		teamStatus:   teamStatus,
		membershipID: membershipID.String,
		authRevision: authRevision,
	}
	if teamStatus != string(domain.TeamStatusActive) {
		out.revoke = true
		return out
	}
	if !account.Valid || account.String != string(domain.AccountStatusActive) {
		out.revoke = true
		return out
	}
	if !membershipID.Valid || membershipID.String != claim.requesterMembershipID || endedAt.Valid {
		out.revoke = true
		return out
	}
	if authRevision != claim.authRevision {
		out.revoke = true
		return out
	}
	role := domain.TeamRoleMember
	if ownerUserID == claim.requesterUserID {
		role = domain.TeamRoleOwner
	} else if baseRole.Valid && baseRole.String == string(domain.TeamBaseRoleAdmin) {
		role = domain.TeamRoleAdmin
	}
	out.role = role
	if role != domain.TeamRoleOwner && role != domain.TeamRoleAdmin {
		out.revoke = true
		return out
	}
	out.ok = true
	return out
}

func (w *Worker) buildTeamExportCSV(ctx context.Context, claim *teamExportClaim) ([]byte, error) {
	var publishedGeneration uint64
	var snapshotAuth uint64
	var snapshotStatus string
	if err := w.db.QueryRowContext(ctx, `
		SELECT published_generation, auth_revision, status
		FROM team_analysis_snapshots
		WHERE snapshot_id = ? AND team_id = ? AND rule_version = ?`, claim.snapshotID, claim.teamID, domain.TeamAnalysisRuleVersion,
	).Scan(&publishedGeneration, &snapshotAuth, &snapshotStatus); err != nil {
		return nil, fmt.Errorf("load export snapshot: %w", err)
	}
	if snapshotStatus != string(domain.SnapshotReady) || snapshotAuth != claim.authRevision || publishedGeneration == 0 {
		return nil, fmt.Errorf("export snapshot is not a ready match for auth_revision")
	}

	filters := parseTeamExportFilters(claim.filterJSON)
	query := `
		SELECT membership_id, metric_date, visibility_mask, agent_id, provider_id, model_id, currency,
		       token_exact_total, token_derived_total, usage_event_count,
		       reported_cost_amount, estimated_cost_amount, legacy_aggregate
		FROM team_analysis_rows
		WHERE snapshot_id = ? AND build_generation = ?
		ORDER BY metric_date ASC, membership_id ASC, agent_id ASC, provider_id ASC, model_id ASC`
	rows, err := w.db.QueryContext(ctx, query, claim.snapshotID, publishedGeneration)
	if err != nil {
		return nil, fmt.Errorf("read team export rows: %w", err)
	}
	defer rows.Close()

	buf := new(bytes.Buffer)
	cw := csv.NewWriter(buf)
	header := append(teamExportHeader(claim.kind), "data_source")
	if err := cw.Write(header); err != nil {
		return nil, err
	}

	acc := make(map[string][]string)
	legacySources := make(map[string]bool)
	order := make([]string, 0)
	for rows.Next() {
		var (
			membershipID sql.NullString
			metricDate   sql.NullTime
			mask         uint32
			agentID      sql.NullString
			providerID   sql.NullString
			modelID      sql.NullString
			currency     sql.NullString
			tokenExact   string
			tokenDerived string
			usageCount   string
			reported     string
			estimated    string
			legacy       bool
		)
		if err := rows.Scan(
			&membershipID, &metricDate, &mask, &agentID, &providerID, &modelID, &currency,
			&tokenExact, &tokenDerived, &usageCount, &reported, &estimated, &legacy,
		); err != nil {
			return nil, fmt.Errorf("scan team export row: %w", err)
		}
		if !rowMatchesExportFilter(filters, agentID.String, providerID.String, modelID.String) {
			continue
		}
		if claim.kind == string(domain.TeamExportMembers) && mask&visNamed == 0 {
			continue
		}
		agent := agentID.String
		if mask&visClassification == 0 {
			agent = unsharedClassificationBucket
			providerID.String = ""
			modelID.String = ""
			membershipID.Valid = membershipID.Valid && claim.kind != string(domain.TeamExportMembers)
			if claim.kind != string(domain.TeamExportMembers) {
				membershipID.Valid = false
			}
		}
		key, record := teamExportRecord(claim.kind, membershipID, metricDate, agent, providerID.String, modelID.String, currency.String, tokenExact, tokenDerived, usageCount, reported, estimated)
		legacySources[key] = legacySources[key] || legacy
		if existing, ok := acc[key]; ok {
			acc[key] = addExportNumeric(existing, record)
			continue
		}
		acc[key] = record
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, key := range order {
		source := "telemetry_v2"
		if legacySources[key] {
			source = "includes_legacy_daily_utc"
		}
		if err := cw.Write(escapeCSVRecord(append(acc[key], source))); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (w *Worker) completeTeamExportJob(ctx context.Context, claim *teamExportClaim, objectKey string, digest [32]byte, size uint64) error {
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	expiresAt := now.Add(teamExportObjectTTL)
	res, err := w.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'completed',
		    object_key = ?,
		    file_sha256 = ?,
		    file_size = ?,
		    expires_at = ?,
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    error_code = NULL
		WHERE export_id = ? AND lease_token = ? AND lease_generation = ? AND status = 'running'`,
		objectKey, digest[:], size, expiresAt, claim.exportID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("complete team export: %w", err)
	}
	if err := requireOneRow(res); err != nil {
		return fmt.Errorf("complete team export: %w", err)
	}
	return nil
}

func (w *Worker) failTeamExportJob(ctx context.Context, claim *teamExportClaim, code string) error {
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	status := "queued"
	if claim.attemptCount >= teamExportMaxAttempts {
		status = "failed"
	}
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = ?, lease_token = NULL, lease_expires_at = NULL,
		    error_code = ?, next_attempt_at = ?
		WHERE export_id = ? AND lease_token = ? AND lease_generation = ?`,
		status, code, now.Add(teamAnalysisRetryDelay), claim.exportID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("fail team export: %w", err)
	}
	return nil
}

func (w *Worker) revokeTeamExport(ctx context.Context, claim *teamExportClaim, code string) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE team_export_jobs
		SET status = 'revoked', lease_token = NULL, lease_expires_at = NULL, error_code = ?
		WHERE export_id = ? AND lease_token = ? AND lease_generation = ?`,
		code, claim.exportID, claim.leaseToken, claim.leaseGeneration)
	if err != nil {
		return fmt.Errorf("revoke team export: %w", err)
	}
	return nil
}

func teamExportHeader(kind string) []string {
	switch kind {
	case string(domain.TeamExportMembers):
		return []string{"membership_id", "tokens_exact", "tokens_derived", "usage_events", "reported_cost", "estimated_cost", "currency"}
	case string(domain.TeamExportAgents):
		return []string{"agent_id", "tokens_exact", "tokens_derived", "usage_events", "reported_cost", "estimated_cost", "currency"}
	case string(domain.TeamExportModels):
		return []string{"provider_id", "model_id", "tokens_exact", "tokens_derived", "usage_events", "reported_cost", "estimated_cost", "currency"}
	default:
		return []string{"metric_date", "tokens_exact", "tokens_derived", "usage_events", "reported_cost", "estimated_cost", "currency"}
	}
}

func teamExportRecord(
	kind string,
	membershipID sql.NullString,
	metricDate sql.NullTime,
	agent, provider, model, currency, tokenExact, tokenDerived, usage, reported, estimated string,
) (string, []string) {
	switch kind {
	case string(domain.TeamExportMembers):
		id := ""
		if membershipID.Valid {
			id = membershipID.String
		}
		return id + "|" + currency, []string{id, tokenExact, tokenDerived, usage, reported, estimated, currency}
	case string(domain.TeamExportAgents):
		return agent + "|" + currency, []string{agent, tokenExact, tokenDerived, usage, reported, estimated, currency}
	case string(domain.TeamExportModels):
		return provider + "|" + model + "|" + currency, []string{provider, model, tokenExact, tokenDerived, usage, reported, estimated, currency}
	default:
		date := ""
		if metricDate.Valid {
			date = metricDate.Time.Format("2006-01-02")
		}
		return date + "|" + currency, []string{date, tokenExact, tokenDerived, usage, reported, estimated, currency}
	}
}

func addExportNumeric(existing, incoming []string) []string {
	out := append([]string(nil), existing...)
	offset := len(out) - 6
	if offset < 0 {
		return existing
	}
	for _, idx := range []int{offset, offset + 1, offset + 2} {
		sum := parseOrZeroInt(out[idx])
		sum.Add(sum, parseOrZeroInt(incoming[idx]))
		out[idx] = sum.String()
	}
	for _, idx := range []int{offset + 3, offset + 4} {
		sum := parseOrZeroRat(out[idx])
		sum.Add(sum, parseOrZeroRat(incoming[idx]))
		out[idx] = formatMoney(sum)
	}
	return out
}

func parseOrZeroInt(value string) *big.Int {
	n := new(big.Int)
	if strings.TrimSpace(value) == "" {
		return n
	}
	if _, ok := n.SetString(strings.TrimSpace(value), 10); !ok {
		return big.NewInt(0)
	}
	return n
}

func parseOrZeroRat(value string) *big.Rat {
	if r, ok := parseDecimalRat(value); ok {
		return r
	}
	return new(big.Rat)
}

func parseTeamExportFilters(raw []byte) teamExportFilters {
	var filters teamExportFilters
	if len(raw) == 0 {
		return filters
	}
	_ = json.Unmarshal(raw, &filters)
	return filters
}

type teamExportFilters struct {
	Agent    *string `json:"agent"`
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
}

func rowMatchesExportFilter(filters teamExportFilters, agent, provider, model string) bool {
	if filters.Agent != nil && *filters.Agent != "" && agent != *filters.Agent && agent != unsharedClassificationBucket {
		return false
	}
	if filters.Provider != nil && *filters.Provider != "" && provider != *filters.Provider {
		return false
	}
	if filters.Model != nil && *filters.Model != "" && model != *filters.Model {
		return false
	}
	return true
}

func escapeCSVRecord(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = escapeCSVFormula(value)
	}
	return out
}

func escapeCSVFormula(value string) string {
	if value == "" {
		return value
	}
	r := []rune(value)
	first := r[0]
	if first == '=' || first == '+' || first == '-' || first == '@' || unicode.IsControl(first) {
		return "'" + value
	}
	return value
}
