package domain

func AlwaysPublicSettings(userID string) UserPrivacySettings {
	return UserPrivacySettings{
		UserID:                userID,
		PublicProfileEnabled:  true,
		LeaderboardVisibility: LeaderboardVisibilityPublic,
		ShowBio:               true,
		ShowTokenTotal:        true,
		ShowTrends:            true,
		ShowActivityCalendar:  true,
		ShowAgentBreakdown:    true,
		ShowSkillRanking:      true,
		ShowAchievements:      true,
	}
}

func ApplyAlwaysPublic(p *UserPrivacySettings) {
	if p == nil {
		return
	}
	forced := AlwaysPublicSettings(p.UserID)
	forced.PrivacyVersion = p.PrivacyVersion
	forced.CreatedAt = p.CreatedAt
	forced.UpdatedAt = p.UpdatedAt
	*p = forced
}

func RevealPublicProfile(pub *PublicUserProfile) {
	if pub == nil {
		return
	}
	pub.ProfileStatus = ProfileStatusPublished
	pub.ShowBio = true
	pub.ShowTokenTotal = true
	pub.ShowTrends = true
	pub.ShowActivityCalendar = true
	pub.ShowAgentBreakdown = true
	pub.ShowSkillRanking = true
	pub.ShowAchievements = true
}
