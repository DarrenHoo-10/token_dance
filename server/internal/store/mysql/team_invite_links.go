package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func (s *teamsStore) CreateInviteLinkTx(ctx context.Context, in store.CreateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	var result *store.CreateInviteLinkTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		out, err := s.createInviteLinkTx(ctx, tx, in)
		result = out
		return err
	})
	return result, err
}

func (s *teamsStore) createInviteLinkTx(ctx context.Context, tx *sql.Tx, in store.CreateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
		return nil, err
	}
	actor, err := s.lockUser(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if err := actor.requireActiveOnboarded(); err != nil {
		return nil, err
	}
	team, err := s.lockTeam(ctx, tx, in.TeamID)
	if err != nil {
		return nil, err
	}
	if team.Status != domain.TeamStatusActive {
		return nil, errTeamNotFound()
	}
	mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if !isManager(*team, mem) {
		if mem == nil {
			return nil, errTeamNotFound()
		}
		return nil, errPermissionDenied()
	}
	now, err := s.txNow(ctx, tx, in.Now)
	if err != nil {
		return nil, err
	}
	if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
		return nil, err
	} else if rec != nil {
		link, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelectSQL+` WHERE link_id = ?`, rec.ResultID))
		if err != nil {
			return nil, errCommandResultUnavailable()
		}
		return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: link}, nil
	}
	link := in.Link
	if link.LinkID == "" {
		id, err := newTeamID(domain.InviteLinkIDPrefix)
		if err != nil {
			return nil, err
		}
		link.LinkID = id
	}
	if link.MaxUses < domain.InviteLinkMaxUsesMin || link.MaxUses > domain.InviteLinkMaxUsesMax {
		return nil, errInvalidArgument("maxUses must be between 1 and 100")
	}
	link.TeamID = in.TeamID
	link.CreatorUserID = in.ActorUserID
	link.Status = domain.InviteLinkActive
	if link.Version == 0 {
		link.Version = 1
	}
	link.UsedCount = 0
	link.CreatedAt = now
	if !link.ExpiresAt.After(now) {
		return nil, errInvalidArgument("invite link expiry must be in the future")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_invite_links (
			link_id, team_id, creator_user_id, token_hash, token_ciphertext, encryption_key_version,
			status, version, max_uses, used_count, created_at, expires_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, 0, ?, ?, NULL)`,
		link.LinkID, link.TeamID, link.CreatorUserID, link.TokenHash[:], link.TokenCiphertext, link.EncryptionKeyVersion,
		link.Version, link.MaxUses, link.CreatedAt, link.ExpiresAt,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, errTeamUnavailable(err)
		}
		return nil, fmt.Errorf("insert invite link: %w", err)
	}
	if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "invite_link_create", "invite_link", link.LinkID, map[string]any{
		"maxUses": link.MaxUses,
	}, now); err != nil {
		return nil, err
	}
	if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "invite_link", link.LinkID, now); err != nil {
		return nil, err
	}
	return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxChanged, Link: &link}, nil
}

func (s *teamsStore) RevokeInviteLinkTx(ctx context.Context, in store.RevokeInviteLinkTxInput) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		link, err := s.lockInviteLink(ctx, tx, in.LinkID)
		if err != nil {
			return err
		}
		if link.TeamID != in.TeamID {
			return errInviteLinkNotFound()
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			return nil
		}
		mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if link.Version != in.ExpectedVersion {
			return errVersionConflict()
		}
		if link.Status != domain.InviteLinkActive {
			_, err = s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "invite_link", in.LinkID, now)
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invite_links
			SET status = 'revoked', revoked_at = ?, version = version + 1, token_ciphertext = x''
			WHERE link_id = ?`, now, in.LinkID); err != nil {
			return fmt.Errorf("revoke invite link: %w", err)
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "invite_link_revoke", "invite_link", in.LinkID, map[string]any{
			"result": "revoked",
		}, now); err != nil {
			return err
		}
		_, err = s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "invite_link", in.LinkID, now)
		return err
	})
}

func (s *teamsStore) RegenerateInviteLinkTx(ctx context.Context, in store.RegenerateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	var result *store.CreateInviteLinkTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		old, err := s.lockInviteLink(ctx, tx, in.LinkID)
		if err != nil {
			return err
		}
		if old.TeamID != in.TeamID {
			return errInviteLinkNotFound()
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			link, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelectSQL+` WHERE link_id = ?`, rec.ResultID))
			if err != nil {
				return errCommandResultUnavailable()
			}
			result = &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: link}
			return nil
		}
		mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if old.Version != in.ExpectedVersion {
			return errVersionConflict()
		}
		if old.Status != domain.InviteLinkActive || !old.ExpiresAt.After(now) || old.UsedCount >= old.MaxUses {
			result = &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: old}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invite_links
			SET status = 'revoked', revoked_at = ?, version = version + 1, token_ciphertext = x''
			WHERE link_id = ?`, now, in.LinkID); err != nil {
			return fmt.Errorf("revoke old invite link: %w", err)
		}
		created, err := s.createInviteLinkTx(ctx, tx, store.CreateInviteLinkTxInput{
			ActorUserID: in.ActorUserID,
			TeamID:      in.TeamID,
			Link:        in.NewLink,
			Idempotency: in.Idempotency,
			Now:         now,
		})
		if err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "invite_link_regenerate", "invite_link", created.Link.LinkID, map[string]any{
			"replaced": in.LinkID,
		}, now); err != nil {
			return err
		}
		result = created
		return nil
	})
	return result, err
}

func (s *teamsStore) AcceptInviteLinkTx(ctx context.Context, in store.AcceptInviteLinkTxInput) (*store.AcceptInviteLinkTxResult, error) {
	var peek domain.TeamInviteLink
	if err := s.db.QueryRowContext(ctx, `
		SELECT team_id, creator_user_id FROM team_invite_links WHERE link_id = ?`, in.LinkID,
	).Scan(&peek.TeamID, &peek.CreatorUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errInviteLinkNotFound()
		}
		return nil, fmt.Errorf("peek invite link: %w", err)
	}
	var result *store.AcceptInviteLinkTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		out, err := s.acceptInviteLinkTx(ctx, tx, in, peek)
		result = out
		return err
	})
	return result, err
}

func (s *teamsStore) acceptInviteLinkTx(ctx context.Context, tx *sql.Tx, in store.AcceptInviteLinkTxInput, peek domain.TeamInviteLink) (*store.AcceptInviteLinkTxResult, error) {
	if !in.Sharing.Valid() {
		return nil, errInvalidArgument("invalid sharing flags")
	}
	if _, err := s.lockUsers(ctx, tx, in.ActorUserID, peek.CreatorUserID); err != nil {
		return nil, err
	}
	actor, err := s.lockUser(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if err := actor.requireEmailVerified(); err != nil {
		return nil, err
	}
	team, err := s.lockTeam(ctx, tx, peek.TeamID)
	if err != nil {
		return nil, err
	}
	link, err := s.lockInviteLink(ctx, tx, in.LinkID)
	if err != nil {
		return nil, err
	}
	if link.TeamID != peek.TeamID || link.CreatorUserID != peek.CreatorUserID {
		return nil, errInviteLinkNotFound()
	}
	if !hashesEqual(link.TokenHash, in.TokenHash) {
		return nil, errInviteLinkNotFound()
	}
	now, err := s.txNow(ctx, tx, in.Now)
	if err != nil {
		return nil, err
	}
	if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := s.replayTeamContext(ctx, tx, in.ActorUserID, team.TeamID)
		if err != nil {
			return nil, errInviteLinkAlreadyUsed()
		}
		return &store.AcceptInviteLinkTxResult{Outcome: store.TeamsTxReplay, Context: ctxn}, nil
	}
	if team.Status != domain.TeamStatusActive {
		return nil, errInviteLinkNotFound()
	}
	current, err := s.lockCurrent(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	history, err := s.lockLatestHistory(ctx, tx, team.TeamID, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	join, err := s.lockLinkJoin(ctx, tx, in.LinkID, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if join != nil {
		if current != nil && current.MembershipID == join.MembershipID {
			mem, err := s.loadMembership(ctx, tx, current.MembershipID)
			if err != nil {
				return nil, err
			}
			if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
				return nil, err
			}
			return &store.AcceptInviteLinkTxResult{
				Outcome: store.TeamsTxAlreadyMember,
				Context: teamContextOf(*team, *mem, true),
			}, nil
		}
		return nil, errInviteLinkAlreadyUsed()
	}
	if current != nil && current.TeamID != team.TeamID {
		return nil, errMembershipExists()
	}
	if current != nil && current.TeamID == team.TeamID {
		mem, err := s.loadMembership(ctx, tx, current.MembershipID)
		if err != nil {
			return nil, err
		}
		if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
			return nil, err
		}
		return &store.AcceptInviteLinkTxResult{
			Outcome: store.TeamsTxAlreadyMember,
			Context: teamContextOf(*team, *mem, true),
		}, nil
	}
	if history != nil && history.EndReason != nil && *history.EndReason == domain.TeamEndReasonRemoved {
		return nil, errReinvitationRequired()
	}
	if link.Status != domain.InviteLinkActive {
		return nil, errInviteLinkRevoked()
	}
	if !link.ExpiresAt.After(now) {
		return nil, errInviteLinkExpired()
	}
	if link.Version != in.ExpectedVersion {
		return nil, errVersionConflict()
	}
	creatorMem, err := s.currentMembership(ctx, tx, team.TeamID, link.CreatorUserID)
	if err != nil {
		return nil, err
	}
	if !isManager(*team, creatorMem) {
		return nil, errInviteLinkNotFound()
	}
	if link.UsedCount >= link.MaxUses {
		return nil, errInviteLinkExhausted()
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE team_invite_links
		SET used_count = used_count + 1
		WHERE link_id = ? AND status = 'active' AND expires_at > ? AND used_count < max_uses`,
		link.LinkID, now,
	)
	if err != nil {
		return nil, fmt.Errorf("consume invite link slot: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, errInviteLinkExhausted()
	}
	membershipID, err := newTeamID(domain.MembershipIDPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.occupyMembership(ctx, tx, occupyMembershipInput{
		MembershipID: membershipID,
		TeamID:       team.TeamID,
		UserID:       in.ActorUserID,
		BaseRole:     domain.TeamBaseRoleMember,
		Sharing:      in.Sharing,
		JoinedAt:     now,
	}); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_invite_link_joins (link_id, user_id, membership_id, joined_at)
		VALUES (?, ?, ?, ?)`, link.LinkID, in.ActorUserID, membershipID, now); err != nil {
		if isDuplicateKey(err) {
			return nil, errInviteLinkAlreadyUsed()
		}
		return nil, fmt.Errorf("insert invite link join: %w", err)
	}
	if _, err := s.bumpAuth(ctx, tx, team.TeamID); err != nil {
		return nil, err
	}
	if err := s.insertAudit(ctx, tx, team.TeamID, in.ActorUserID, "invite_link_accept", "invite_link", link.LinkID, map[string]any{
		"membershipId": membershipID,
	}, now); err != nil {
		return nil, err
	}
	if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
		return nil, err
	}
	loadedTeam, err := s.loadTeam(ctx, tx, team.TeamID)
	if err != nil {
		return nil, err
	}
	loadedMem, err := s.loadMembership(ctx, tx, membershipID)
	if err != nil {
		return nil, err
	}
	return &store.AcceptInviteLinkTxResult{
		Outcome: store.TeamsTxChanged,
		Context: teamContextOf(*loadedTeam, *loadedMem, false),
	}, nil
}

func (s *teamsStore) lockLinkJoin(ctx context.Context, tx *sql.Tx, linkID, userID string) (*domain.TeamInviteLinkJoin, error) {
	var join domain.TeamInviteLinkJoin
	err := tx.QueryRowContext(ctx, `
		SELECT link_id, user_id, membership_id, joined_at
		FROM team_invite_link_joins
		WHERE link_id = ? AND user_id = ?
		FOR UPDATE`, linkID, userID).Scan(&join.LinkID, &join.UserID, &join.MembershipID, &join.JoinedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock invite link join: %w", err)
	}
	return &join, nil
}
