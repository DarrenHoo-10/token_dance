package ranking

import (
	"math"
	"math/big"
	"sort"
	"testing"
	"time"
)

func TestEncodeTokenZeroAndCarry(t *testing.T) {
	zero, err := EncodeTokenUint64(0)
	if err != nil {
		t.Fatal(err)
	}
	if zero != "999999999999999999999999999999" {
		t.Fatalf("zero encoding: %s", zero)
	}
	one, _ := EncodeTokenUint64(1)
	nine, _ := EncodeTokenUint64(9)
	ten, _ := EncodeTokenUint64(10)
	if !(ten < nine && nine < one && one < zero) {
		t.Fatalf("carry order: ten=%s nine=%s one=%s zero=%s", ten, nine, one, zero)
	}
	decoded, err := DecodeToken(zero)
	if err != nil || decoded.Uint64() != 0 {
		t.Fatalf("decode zero: %v %v", decoded, err)
	}
}

func TestEncodeTokenMaxAndOverflow(t *testing.T) {
	max64, err := EncodeTokenUint64(math.MaxUint64)
	if err != nil {
		t.Fatal(err)
	}
	almost, err := EncodeTokenUint64(math.MaxUint64 - 1)
	if err != nil {
		t.Fatal(err)
	}
	if !(max64 < almost) {
		t.Fatalf("max uint64 should sort first: max=%s almost=%s", max64, almost)
	}
	round, err := DecodeToken(max64)
	if err != nil || round.Uint64() != math.MaxUint64 {
		t.Fatalf("decode max: %v %v", round, err)
	}
	limit := new(big.Int).Set(maxToken30)
	if _, err := EncodeToken(limit); err != nil {
		t.Fatalf("max 30-digit value: %v", err)
	}
	over := new(big.Int).Add(limit, big.NewInt(1))
	if _, err := EncodeToken(over); err != ErrTokenOverflow {
		t.Fatalf("expected overflow, got %v", err)
	}
	if _, err := EncodeToken(big.NewInt(-1)); err != ErrNegativeToken {
		t.Fatalf("expected negative, got %v", err)
	}
	if _, err := EncodeToken(nil); err != ErrNegativeToken {
		t.Fatalf("expected nil as negative, got %v", err)
	}
}

func TestEncodeMemberTiesAndOrder(t *testing.T) {
	t0 := time.UnixMilli(1_750_000_000_000).UTC()
	t1 := t0.Add(time.Millisecond)
	samples := []struct {
		tokens uint64
		at     time.Time
		id     string
	}{
		{0, t1, "usr_b"},
		{0, t0, "usr_a"},
		{0, t0, "usr_c"},
		{10, t1, "usr_d"},
		{9, t0, "usr_e"},
		{math.MaxUint64, t0, "usr_f"},
	}
	want := []struct {
		tokens uint64
		at     time.Time
		id     string
	}{
		{math.MaxUint64, t0, "usr_f"},
		{10, t1, "usr_d"},
		{9, t0, "usr_e"},
		{0, t0, "usr_a"},
		{0, t0, "usr_c"},
		{0, t1, "usr_b"},
	}
	encoded := make([]string, len(samples))
	for i, sample := range samples {
		member, err := EncodeMember(sample.tokens, sample.at, sample.id)
		if err != nil {
			t.Fatal(err)
		}
		tokens, at, id, err := DecodeMember(member)
		if err != nil || tokens != sample.tokens || id != sample.id || !at.Equal(sample.at) {
			t.Fatalf("roundtrip %s: tokens=%d at=%s id=%s err=%v", member, tokens, at, id, err)
		}
		encoded[i] = member
	}
	sort.Strings(encoded)
	for i, member := range encoded {
		tokens, at, id, err := DecodeMember(member)
		if err != nil {
			t.Fatal(err)
		}
		if tokens != want[i].tokens || id != want[i].id || !at.Equal(want[i].at) {
			t.Fatalf("rank %d got tokens=%d id=%s at=%s want %+v", i, tokens, id, at, want[i])
		}
	}
}

func TestEncodeMemberRejects(t *testing.T) {
	now := time.UnixMilli(1).UTC()
	if _, err := EncodeMember(1, now, ""); err != ErrInvalidUserID {
		t.Fatalf("empty user: %v", err)
	}
	if _, err := EncodeMember(1, now, "usr|bad"); err != ErrInvalidUserID {
		t.Fatalf("pipe user: %v", err)
	}
	if _, err := EncodeMember(1, time.Time{}, "usr_ok"); err != ErrInvalidRegisteredAt {
		t.Fatalf("zero time: %v", err)
	}
	if _, err := EncodeMember(1, time.UnixMilli(-1), "usr_ok"); err != ErrInvalidRegisteredAt {
		t.Fatalf("negative time: %v", err)
	}
}
