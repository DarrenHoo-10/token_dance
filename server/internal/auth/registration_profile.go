package auth

import (
	"math/rand/v2"
	"strings"

	"tokendance/internal/domain"
	"tokendance/internal/profile"
)

// RegistrationProfile is optional for older clients. Avatar IDs are a fixed
// catalog, never user-provided URLs or storage paths.
type RegistrationProfile struct {
	DisplayName string
	AvatarID    string
}

var registrationAvatars = []string{"cat", "fox", "panda", "bunny"}

func resolveRegistrationProfile(in RegistrationProfile, locale string) (string, string, error) {
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		adjectives := []string{"Sunny", "Cosmic", "Mellow", "Lucky", "Curious", "Dancing", "Minty", "Dreamy"}
		animals := []string{"Cat", "Fox", "Panda", "Bunny", "Otter", "Penguin", "Koala", "Owl"}
		if locale == "zh-CN" {
			adjectives = []string{"晴天", "星际", "软糖", "幸运", "好奇", "跳舞", "薄荷", "追梦"}
			animals = []string{"小猫", "狐狸", "熊猫", "兔兔", "海獭", "企鹅", "考拉", "猫头鹰"}
		}
		name = adjectives[rand.IntN(len(adjectives))] + animals[rand.IntN(len(animals))] + "_" + randomHandleSuffix(4)
	}
	if err := profile.ValidateDisplayName(name); err != nil {
		return "", "", err
	}
	id := in.AvatarID
	if id == "" {
		id = registrationAvatars[rand.IntN(len(registrationAvatars))]
	}
	for _, allowed := range registrationAvatars {
		if id == allowed {
			return name, "/images/avatars/" + id + ".png", nil
		}
	}
	return "", "", domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidArgument", "invalid avatar selection", nil, nil)
}
