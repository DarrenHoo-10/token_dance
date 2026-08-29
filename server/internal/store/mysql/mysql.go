package mysql

import (
	"database/sql"
	"time"

	"tokendance/internal/store"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{
		db: db,
	}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Auth() store.AuthStore               { return &authStore{db: s.db} }
func (s *Store) Profile() store.ProfileStore         { return &profileStore{db: s.db} }
func (s *Store) Privacy() store.PrivacyStore         { return &privacyStore{db: s.db} }
func (s *Store) Analytics() store.AnalyticsStore     { return &analyticsStore{db: s.db} }
func (s *Store) Device() store.DeviceStore           { return &deviceStore{db: s.db} }
func (s *Store) Ingest() store.IngestStore           { return &ingestStore{db: s.db} }
func (s *Store) Export() store.ExportStore           { return &exportStore{db: s.db} }
func (s *Store) Search() store.SearchStore           { return &searchStore{db: s.db} }
func (s *Store) Leaderboard() store.LeaderboardStore { return &leaderboardStore{db: s.db} }
func (s *Store) Media() store.MediaStore             { return &mediaStore{db: s.db} }

// Helper conversions for database/sql scanning

func scanBytes32(b []byte) [32]byte {
	var res [32]byte
	copy(res[:], b)
	return res
}

func scanBytes32Ptr(b []byte) *[32]byte {
	if len(b) == 0 {
		return nil
	}
	var res [32]byte
	copy(res[:], b)
	return &res
}

func bytes32Slice(b [32]byte) []byte {
	return b[:]
}

func bytes32PtrSlice(b *[32]byte) []byte {
	if b == nil {
		return nil
	}
	return b[:]
}

func nullStringFromPtr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: *s, Valid: true}
}

func ptrFromNullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func nullTimeFromPtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func ptrFromNullTime(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}
