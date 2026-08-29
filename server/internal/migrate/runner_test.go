package migrate

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestMigrationEmbedLoading(t *testing.T) {
	runner := NewRunner(nil)
	if err := runner.LoadFromEmbed(); err != nil {
		t.Fatalf("failed to load embedded migrations: %v", err)
	}

	migs := runner.GetMigrations()
	if len(migs) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(migs))
	}

	expected := []string{"0001", "0002", "0003"}
	for i, m := range migs {
		if m.Version != expected[i] {
			t.Errorf("migration %d: expected version %s, got %s", i, expected[i], m.Version)
		}
		if len(m.ChecksumHex) != 64 {
			t.Errorf("migration %d: invalid checksum hex length %d", i, len(m.ChecksumHex))
		}
		if len(m.Content) == 0 {
			t.Errorf("migration %d: content is empty", i)
		}
	}
}

func TestValidateBaselineRecords(t *testing.T) {
	now := time.Now().UTC()
	u1 := "usr_user1"
	u2 := "usr_user2"

	// Valid records: unique active account deletions
	validRecords := []domain.DataDeletionRequest{
		{RequestID: "del_1", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
		{RequestID: "del_2", UserID: &u2, DeletionScope: "account", RequestStatus: domain.DeletionStatusRunning, RequestedAt: now},
		{RequestID: "del_3", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusCompleted, RequestedAt: now},
		{RequestID: "del_4", UserID: &u1, DeletionScope: "installation", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
	}

	if err := ValidateBaselineRecords(validRecords); err != nil {
		t.Errorf("expected valid records to pass: %v", err)
	}

	// Invalid records: duplicate pending account deletions for same user
	invalidRecords := []domain.DataDeletionRequest{
		{RequestID: "del_1", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
		{RequestID: "del_2", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusRunning, RequestedAt: now},
	}

	if err := ValidateBaselineRecords(invalidRecords); err == nil {
		t.Errorf("expected duplicate active account deletion to fail baseline guard")
	}
}
