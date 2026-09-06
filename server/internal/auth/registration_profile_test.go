package auth

import (
	"context"
	"strings"
	"testing"
)

func TestRegistrationSavesSelectedProfile(t *testing.T) {
	svc, st, _ := setupAuthService(t)
	ctx := context.Background()
	email := "avatar-new@tokendance.dev"
	if err := svc.RequestRegistrationCode(ctx, email, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	code := svc.EmailSink().LatestCode(email)
	// Invalid profile input must not consume a valid verification challenge.
	for _, in := range []RegistrationProfile{
		{DisplayName: strings.Repeat("猫", 81), AvatarID: "cat"},
		{DisplayName: "猫\n猫", AvatarID: "cat"},
		{DisplayName: "薄荷小猫", AvatarID: "https://example.com/avatar.png"},
		{DisplayName: "薄荷小猫", AvatarID: "../cat"},
	} {
		if _, err := svc.CompleteRegistration(ctx, email, code, "Password123!", "/me", "zh-CN", "UTC", in); err == nil {
			t.Fatalf("accepted invalid profile %+v", in)
		}
	}
	res, err := svc.CompleteRegistration(ctx, email, code, "Password123!", "/me", "zh-CN", "UTC", RegistrationProfile{DisplayName: "  薄荷小猫  ", AvatarID: "fox"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.Profile().GetUserProfile(ctx, res.User.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if u.DisplayName != "薄荷小猫" || u.AvatarURL == nil || *u.AvatarURL != "/images/avatars/fox.png" {
		t.Fatalf("profile not persisted: %+v", u)
	}
}

func TestRegistrationProfileFallbacks(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		names := map[string]bool{}
		for i := 0; i < 20; i++ {
			name, avatar, err := resolveRegistrationProfile(RegistrationProfile{}, locale)
			if err != nil || name == "" || name == "Token Dancer" || !strings.HasPrefix(avatar, "/images/avatars/") {
				t.Fatalf("invalid defaults: %q %q %v", name, avatar, err)
			}
			names[name] = true
		}
		if len(names) < 2 {
			t.Fatal("default names are not randomized")
		}
	}
}
