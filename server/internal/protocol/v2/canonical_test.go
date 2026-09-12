package v2_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	v2 "tokendance/internal/protocol/v2"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

func TestGoldenContentHashAndAck(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "schemas", "protocol", "v2", "fixtures", "golden", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []map[string]any
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		name, _ := fixture["name"].(string)
		switch name {
		case "ack_duplicate_vs_conflict":
			base := fixture["base_event"].(map[string]any)
			conflict := fixture["conflict_event"].(map[string]any)
			baseHash, err := v2.ComputeContentHash(base)
			if err != nil {
				t.Fatalf("%s base hash: %v", name, err)
			}
			if baseHash != fixture["base_hash"] {
				t.Fatalf("%s base hash mismatch: got %s want %s", name, baseHash, fixture["base_hash"])
			}
			if got := v2.ClassifyAck(nil, baseHash); got != "accepted" {
				t.Fatalf("fresh => accepted, got %s", got)
			}
			if got := v2.ClassifyAck(&baseHash, baseHash); got != "duplicate" {
				t.Fatalf("same hash => duplicate, got %s", got)
			}
			conflictHash, err := v2.ComputeContentHash(conflict)
			if err != nil {
				t.Fatalf("%s conflict hash: %v", name, err)
			}
			if conflictHash != fixture["conflict_hash"] {
				t.Fatalf("%s conflict hash mismatch", name)
			}
			if got := v2.ClassifyAck(&baseHash, conflictHash); got != "conflict" {
				t.Fatalf("different hash => conflict, got %s", got)
			}
		default:
			event := fixture["event"].(map[string]any)
			canonical, err := v2.ContentHashCanonicalJSON(event)
			if err != nil {
				t.Fatalf("%s canonical: %v", name, err)
			}
			if canonical != fixture["canonical_json"] {
				t.Fatalf("%s canonical mismatch\n got: %s\nwant: %s", name, canonical, fixture["canonical_json"])
			}
			hash, err := v2.ComputeContentHash(event)
			if err != nil {
				t.Fatalf("%s hash: %v", name, err)
			}
			if hash != fixture["content_hash"] {
				t.Fatalf("%s hash mismatch: got %s want %s", name, hash, fixture["content_hash"])
			}
			withName := cloneMap(event)
			if skill, ok := withName["skill"].(map[string]any); ok {
				skill["publicName"] = "another-display-name"
				withName["skill"] = skill
				alt, err := v2.ComputeContentHash(withName)
				if err != nil {
					t.Fatal(err)
				}
				if alt != hash {
					t.Fatalf("%s publicName must be excluded from hash", name)
				}
			}
		}
	}
}

func TestNegativeFixtures(t *testing.T) {
	root := filepath.Join(repoRoot(t), "schemas", "protocol", "v2", "fixtures", "negative")
	dup, err := os.ReadFile(filepath.Join(root, "duplicate_keys.json.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.RejectDuplicateKeys(string(dup)); err == nil {
		t.Fatal("duplicate keys must be rejected")
	}
	unknown, err := os.ReadFile(filepath.Join(root, "unknown_field.json"))
	if err != nil {
		t.Fatal(err)
	}
	event, err := v2.DecodeEventMap(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.ComputeContentHash(event); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	negative, err := os.ReadFile(filepath.Join(root, "negative_count.json"))
	if err != nil {
		t.Fatal(err)
	}
	negEvent, err := v2.DecodeEventMap(negative)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.ComputeContentHash(negEvent); err == nil {
		t.Fatal("negative counts must be rejected")
	}
}

func cloneMap(in map[string]any) map[string]any {
	raw, _ := json.Marshal(in)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
