package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
)

// ensure interface compliance
var (
	_ store.UserStore         = (*Store)(nil)
	_ store.InstallationStore = (*Store)(nil)
	_ store.NonceStore        = (*Store)(nil)
	_ store.BatchStore        = (*Store)(nil)
	_ store.EventStore        = (*Store)(nil)
	_ store.MetricStore       = (*Store)(nil)
	_ store.LeaderboardStore  = (*Store)(nil)
)

// Store is a concurrency-safe in-memory implementation of all store interfaces.
type Store struct {
	mu sync.RWMutex

	users         map[string]*domain.User            // keyed by user_id
	usersByAuth   map[[32]byte]string                 // auth_subject_hash -> user_id
	installations map[string]*domain.Installation     // keyed by installation_id
	instByPubKey  map[string]string                   // hex(pub_key) -> installation_id
	nonces        map[string]time.Time                // "install_id:hex(nonce_hash)" -> expires_at
	batches       map[string]*domain.IngestBatch      // keyed by batch_id
	events        []*domain.UsageEvent                // append-only, ordered by event_pk
	eventDedup    map[string]bool                     // "install_id:hex(event_id)" -> true
	nextEventPK   uint64
	metrics       map[string]*domain.DailyUserAgentMetric   // "date:user:agent" -> metric
	snapshots     map[string]*domain.LeaderboardSnapshot    // keyed by snapshot_id
	entries       map[string][]*domain.LeaderboardEntry     // keyed by snapshot_id
}

func New() *Store {
	return &Store{
		users:         make(map[string]*domain.User),
		usersByAuth:   make(map[[32]byte]string),
		installations: make(map[string]*domain.Installation),
		instByPubKey:  make(map[string]string),
		nonces:        make(map[string]time.Time),
		batches:       make(map[string]*domain.IngestBatch),
		eventDedup:    make(map[string]bool),
		nextEventPK:   1,
		metrics:       make(map[string]*domain.DailyUserAgentMetric),
		snapshots:     make(map[string]*domain.LeaderboardSnapshot),
		entries:       make(map[string][]*domain.LeaderboardEntry),
	}
}

func hexBytes(b []byte) string {
	return fmt.Sprintf("%x", b)
}

// ---- UserStore ----

func (s *Store) CreateUser(_ context.Context, u *domain.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.UserID]; ok {
		return fmt.Errorf("user %s already exists", u.UserID)
	}
	if _, ok := s.usersByAuth[u.AuthSubjectHash]; ok {
		return fmt.Errorf("auth subject hash already registered")
	}
	cp := *u
	s.users[cp.UserID] = &cp
	s.usersByAuth[cp.AuthSubjectHash] = cp.UserID
	return nil
}

func (s *Store) GetUser(_ context.Context, userID string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[userID]
	if !ok {
		return nil, fmt.Errorf("user %s not found", userID)
	}
	cp := *u
	return &cp, nil
}

func (s *Store) GetUserByAuthSubjectHash(_ context.Context, hash [32]byte) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uid, ok := s.usersByAuth[hash]
	if !ok {
		return nil, fmt.Errorf("user not found by auth hash")
	}
	cp := *s.users[uid]
	return &cp, nil
}

func (s *Store) UpdateVisibility(_ context.Context, userID, visibility string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return fmt.Errorf("user %s not found", userID)
	}
	u.LeaderboardVisibility = visibility
	u.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *Store) ListPublicUsers(_ context.Context) ([]*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.User
	for _, u := range s.users {
		if u.LeaderboardVisibility == "public" && u.AccountStatus == "active" {
			cp := *u
			result = append(result, &cp)
		}
	}
	return result, nil
}

// ---- InstallationStore ----

func (s *Store) CreateInstallation(_ context.Context, inst *domain.Installation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.installations[inst.InstallationID]; ok {
		return fmt.Errorf("installation %s already exists", inst.InstallationID)
	}
	pkHex := hexBytes(inst.DevicePublicKey)
	if _, ok := s.instByPubKey[pkHex]; ok {
		return fmt.Errorf("device public key already registered")
	}
	cp := *inst
	s.installations[cp.InstallationID] = &cp
	s.instByPubKey[pkHex] = cp.InstallationID
	return nil
}

func (s *Store) GetInstallation(_ context.Context, installationID string) (*domain.Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.installations[installationID]
	if !ok {
		return nil, fmt.Errorf("installation %s not found", installationID)
	}
	cp := *inst
	return &cp, nil
}

func (s *Store) GetInstallationByPublicKey(_ context.Context, pubKey []byte) (*domain.Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.instByPubKey[hexBytes(pubKey)]
	if !ok {
		return nil, fmt.Errorf("installation not found by public key")
	}
	cp := *s.installations[id]
	return &cp, nil
}

func (s *Store) UpdateLastSeen(_ context.Context, installationID string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.installations[installationID]
	if !ok {
		return fmt.Errorf("installation %s not found", installationID)
	}
	inst.LastSeenAt = &t
	return nil
}

func (s *Store) RevokeInstallation(_ context.Context, installationID string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.installations[installationID]
	if !ok {
		return fmt.Errorf("installation %s not found", installationID)
	}
	inst.InstallationStatus = "revoked"
	inst.RevokedAt = &t
	return nil
}

func (s *Store) ListByUser(_ context.Context, userID string) ([]*domain.Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.Installation
	for _, inst := range s.installations {
		if inst.UserID == userID {
			cp := *inst
			result = append(result, &cp)
		}
	}
	return result, nil
}

// ---- NonceStore ----

func (s *Store) ConsumeNonce(_ context.Context, installationID string, nonceHash [32]byte, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := installationID + ":" + hexBytes(nonceHash[:])
	if exp, ok := s.nonces[key]; ok && time.Now().UTC().Before(exp) {
		return false, nil // replay
	}
	s.nonces[key] = expiresAt
	return true, nil
}

func (s *Store) PruneExpired(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := 0
	for k, exp := range s.nonces {
		if now.After(exp) {
			delete(s.nonces, k)
			pruned++
		}
	}
	return pruned, nil
}

// ---- BatchStore ----

func (s *Store) CreateBatch(_ context.Context, b *domain.IngestBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.batches[b.BatchID]; ok {
		return fmt.Errorf("batch %s already exists", b.BatchID)
	}
	cp := *b
	s.batches[cp.BatchID] = &cp
	return nil
}

func (s *Store) GetBatch(_ context.Context, batchID string) (*domain.IngestBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.batches[batchID]
	if !ok {
		return nil, fmt.Errorf("batch %s not found", batchID)
	}
	cp := *b
	return &cp, nil
}

func (s *Store) UpdateBatch(_ context.Context, b *domain.IngestBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.batches[b.BatchID]; !ok {
		return fmt.Errorf("batch %s not found", b.BatchID)
	}
	cp := *b
	s.batches[cp.BatchID] = &cp
	return nil
}

// ---- EventStore ----

func (s *Store) InsertEvent(_ context.Context, e *domain.UsageEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dedupKey := e.InstallationID + ":" + hexBytes(e.EventID[:])
	if s.eventDedup[dedupKey] {
		return false, nil
	}
	cp := *e
	cp.EventPK = s.nextEventPK
	s.nextEventPK++
	s.events = append(s.events, &cp)
	s.eventDedup[dedupKey] = true
	return true, nil
}

func (s *Store) GetEventsByBatch(_ context.Context, batchID string) ([]*domain.UsageEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.UsageEvent
	for _, e := range s.events {
		if e.BatchID == batchID {
			cp := *e
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *Store) ListEventsAfterPK(_ context.Context, afterPK uint64, limit int) ([]*domain.UsageEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.UsageEvent
	for _, e := range s.events {
		if e.EventPK > afterPK {
			cp := *e
			result = append(result, &cp)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (s *Store) MaxEventPK(_ context.Context) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return 0, nil
	}
	return s.events[len(s.events)-1].EventPK, nil
}

func (s *Store) DeleteEventsByBatch(_ context.Context, batchID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []*domain.UsageEvent
	removed := 0
	for _, e := range s.events {
		if e.BatchID == batchID {
			dedupKey := e.InstallationID + ":" + hexBytes(e.EventID[:])
			delete(s.eventDedup, dedupKey)
			removed++
		} else {
			kept = append(kept, e)
		}
	}
	s.events = kept
	return removed, nil
}

// ---- MetricStore ----

func metricKey(date time.Time, userID, agentID string) string {
	return date.Format("2006-01-02") + ":" + userID + ":" + agentID
}

func (s *Store) UpsertDailyUserAgentMetric(_ context.Context, m *domain.DailyUserAgentMetric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *m
	s.metrics[metricKey(cp.MetricDate, cp.UserID, cp.AgentID)] = &cp
	return nil
}

func (s *Store) GetDailyMetrics(_ context.Context, userID string, start, end time.Time) ([]*domain.DailyUserAgentMetric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.DailyUserAgentMetric
	for _, m := range s.metrics {
		if m.UserID == userID && !m.MetricDate.Before(start) && !m.MetricDate.After(end) {
			cp := *m
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].MetricDate.Before(result[j].MetricDate)
	})
	return result, nil
}

func (s *Store) GetDailyMetricsAllUsers(_ context.Context, start, end time.Time) ([]*domain.DailyUserAgentMetric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*domain.DailyUserAgentMetric
	for _, m := range s.metrics {
		if !m.MetricDate.Before(start) && !m.MetricDate.After(end) {
			cp := *m
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *Store) DeleteAllMetrics(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = make(map[string]*domain.DailyUserAgentMetric)
	return nil
}

// ---- LeaderboardStore ----

func (s *Store) CreateSnapshot(_ context.Context, snap *domain.LeaderboardSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snapshots[snap.SnapshotID]; ok {
		return fmt.Errorf("snapshot %s already exists", snap.SnapshotID)
	}
	cp := *snap
	s.snapshots[cp.SnapshotID] = &cp
	return nil
}

func (s *Store) GetSnapshot(_ context.Context, snapshotID string) (*domain.LeaderboardSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[snapshotID]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	cp := *snap
	return &cp, nil
}

func (s *Store) PublishSnapshot(_ context.Context, snapshotID string, publishedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[snapshotID]
	if !ok {
		return fmt.Errorf("snapshot %s not found", snapshotID)
	}
	snap.SnapshotStatus = "published"
	snap.PublishedAt = &publishedAt
	return nil
}

func (s *Store) SupersedeSnapshot(_ context.Context, snapshotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[snapshotID]
	if !ok {
		return fmt.Errorf("snapshot %s not found", snapshotID)
	}
	snap.SnapshotStatus = "superseded"
	return nil
}

func (s *Store) LatestPublishedSnapshot(_ context.Context, boardKey, scopeType, metricKey string) (*domain.LeaderboardSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *domain.LeaderboardSnapshot
	for _, snap := range s.snapshots {
		if snap.BoardKey != boardKey || snap.ScopeType != scopeType || snap.MetricKey != metricKey {
			continue
		}
		if snap.SnapshotStatus != "published" {
			continue
		}
		if best == nil || snap.PublishedAt.After(*best.PublishedAt) {
			cp := *snap
			best = &cp
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no published snapshot found")
	}
	return best, nil
}

func (s *Store) CreateEntry(_ context.Context, e *domain.LeaderboardEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *e
	s.entries[cp.SnapshotID] = append(s.entries[cp.SnapshotID], &cp)
	return nil
}

func (s *Store) ListEntries(_ context.Context, snapshotID string) ([]*domain.LeaderboardEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ents := s.entries[snapshotID]
	result := make([]*domain.LeaderboardEntry, len(ents))
	for i, e := range ents {
		cp := *e
		result[i] = &cp
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RankNo < result[j].RankNo
	})
	return result, nil
}
