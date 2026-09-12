package grayscale

import (
	"strings"
	"testing"
	"time"
)

func TestConfigRejectsProductionTarget(t *testing.T) {
	cfg := Config{
		SourceDSN:    "user:pass@tcp(127.0.0.1:3307)/tokendance_prod",
		TargetDSN:    "user:pass@tcp(127.0.0.1:3307)/tokendance_prod",
		SourceSchema: "tokendance_prod",
		TargetSchema: "tokendance_prod",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected production target to be rejected")
	}
}

func TestConfigRejectsSameDatabase(t *testing.T) {
	cfg := Config{
		SourceDSN:    "user:pass@tcp(127.0.0.1:3307)/tokendance_dev",
		TargetDSN:    "user:pass@tcp(127.0.0.1:3307)/tokendance_dev",
		SourceSchema: "tokendance_dev",
		TargetSchema: "tokendance_dev",
		AllowTarget:  true,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected same-database pair to be rejected")
	}
}

func TestConfigAllowsProdToDevOnSameHost(t *testing.T) {
	cfg := Config{
		SourceDSN:    "app:pass@tcp(127.0.0.1:3307)/tokendance_prod",
		TargetDSN:    "app:pass@tcp(127.0.0.1:3307)/tokendance_dev",
		SourceSchema: "tokendance_prod",
		TargetSchema: "tokendance_dev",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("prod→dev on the same instance should be allowed: %v", err)
	}
}

func TestMirrorHashIsStableAndDistinct(t *testing.T) {
	first := MirrorHash("auth", "usr_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	again := MirrorHash("auth", "usr_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	other := MirrorHash("email", "usr_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if len(first) != 32 {
		t.Fatalf("hash length %d", len(first))
	}
	if string(first) != string(again) {
		t.Fatal("hash should be deterministic")
	}
	if string(first) == string(other) {
		t.Fatal("different kinds must not collide")
	}
}

func TestChooseHandleKeepsOriginalUnlessTaken(t *testing.T) {
	taken := map[string]string{"darrenhoo": "usr_other"}
	got := chooseHandle("darrenhoo", "usr_mine", taken)
	if got == "darrenhoo" {
		t.Fatal("should not steal another test user's handle")
	}
	if !handlePattern.MatchString(got) {
		t.Fatalf("generated handle %q is invalid", got)
	}
	kept := chooseHandle("jiayu", "usr_mine", map[string]string{})
	if kept != "jiayu" {
		t.Fatalf("free handle should be kept, got %q", kept)
	}
}

func TestSanitizeUserScrubsSecrets(t *testing.T) {
	columns := []string{"user_id", "auth_subject_hash", "email_lookup_hash", "email_ciphertext", "email_verified_at", "avatar_object_id", "account_status", "handle"}
	values := []any{"usr_aaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("prod-auth"), []byte("prod-email"), []byte("ciphertext"), time.Now(), "obj_1", "deleted", "Jiayu"}
	handle := sanitizeUserRow(columns, values, map[string]string{})
	if handle != "jiayu" {
		t.Fatalf("handle %q", handle)
	}
	if string(values[1].([]byte)) == "prod-auth" {
		t.Fatal("auth hash was not replaced")
	}
	if values[3] != nil || values[4] != nil || values[5] != nil {
		t.Fatal("email and avatar object should be cleared")
	}
	if values[6] != "active" {
		t.Fatalf("deleted accounts should be revived for grayscale, got %v", values[6])
	}
}

func TestSanitizeInstallationRotatesDeviceKey(t *testing.T) {
	columns := []string{"installation_id", "device_public_key"}
	values := []any{"ins_aaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("prod-device-key-bytes-32_____")}
	sanitizeInstallationRow(columns, values)
	got := values[1].([]byte)
	if string(got) == "prod-device-key-bytes-32_____" {
		t.Fatal("device key must not be copied")
	}
	if len(got) != 32 {
		t.Fatalf("rotated key length %d", len(got))
	}
}

func TestRefreshDatesCapsFullHistory(t *testing.T) {
	dates := refreshDates("1000-01-01", "2026-09-10")
	if len(dates) != 2 || dates[0] != "2026-09-09" || dates[1] != "2026-09-10" {
		t.Fatalf("unexpected community dates %v", dates)
	}
	recent := refreshDates("2026-09-09", "2026-09-10")
	if strings.Join(recent, ",") != "2026-09-09,2026-09-10" {
		t.Fatalf("incremental dates %v", recent)
	}
}

func TestIntersectColumnsKeepsSharedWritableOrder(t *testing.T) {
	source := tableMeta{columns: []string{"user_id", "token_total", "generated"}}
	target := tableMeta{columns: []string{"token_total", "user_id", "extra"}}
	got := intersectColumns(source, target)
	if strings.Join(got, ",") != "user_id,token_total" {
		t.Fatalf("intersection %v", got)
	}
}

func TestUpsertSQLSkipsPrimaryKeyAssignments(t *testing.T) {
	sql := upsertSQL("tokendance_dev", "users", []string{"user_id", "display_name"}, map[string]struct{}{"user_id": {}})
	if !strings.Contains(sql, "`display_name` = VALUES(`display_name`)") {
		t.Fatalf("missing assignment: %s", sql)
	}
	if strings.Contains(sql, "`user_id` = VALUES(`user_id`)") {
		t.Fatalf("primary key should not be assigned: %s", sql)
	}
}

func TestUserRefreshPreservesTestLoginIdentity(t *testing.T) {
	identity := []string{"auth_subject_hash", "email_lookup_hash", "email_ciphertext", "email_verified_at"}
	columns := append([]string{"user_id", "display_name"}, identity...)
	query := upsertSQL("tokendance_dev", "users", columns, map[string]struct{}{"user_id": {}})
	parts := strings.SplitN(query, " ON DUPLICATE KEY UPDATE ", 2)
	if len(parts) != 2 {
		t.Fatalf("missing upsert clause: %s", query)
	}
	for _, column := range identity {
		if !strings.Contains(parts[0], quote(column)) {
			t.Fatalf("new mirrors still need sanitized %s", column)
		}
		if strings.Contains(parts[1], quote(column)) {
			t.Fatalf("refresh overwrites test login field %s", column)
		}
	}
	if parts[1] != "`display_name` = VALUES(`display_name`)" {
		t.Fatalf("public profile must still refresh: %s", parts[1])
	}
}
