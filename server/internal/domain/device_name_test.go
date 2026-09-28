package domain

import "testing"

func TestInitialDeviceName(t *testing.T) {
	generic := "TokenDance Desktop"
	fallback := InitialDeviceName(&generic, "macos", "ins_ABC12345")
	got := fallback
	if *got != "Mac · C12345" {
		t.Fatalf("legacy label = %q", *got)
	}
	custom := "  Work MacBook  "
	got = InitialDeviceName(&custom, "macos", "ins_ABC12345")
	if *got != "Work MacBook" {
		t.Fatalf("custom label = %q", *got)
	}
	other := InitialDeviceName(nil, "macos", "ins_XYZ98765")
	if *other == *InitialDeviceName(nil, "macos", "ins_ABC12345") {
		t.Fatal("different installations must have different fallback labels")
	}
	if !IsAutoDeviceName(fallback, "macos", "ins_ABC12345") {
		t.Fatal("generated fallback should be replaceable by a later machine name")
	}
	if IsAutoDeviceName(&custom, "macos", "ins_ABC12345") {
		t.Fatal("custom alias must never be treated as an automatic label")
	}
}
