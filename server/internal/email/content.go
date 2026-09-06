package email

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderEmail returns the localized subject and plain-text body for an outbox
// message. Unknown template keys fall back to the legacy subject and raw JSON
// payload so no message is left unrenderable.
func renderEmail(templateKey, locale, payloadJSON string) (string, string) {
	code := ""
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err == nil {
		code = payload.Code
	}
	zh := strings.HasPrefix(locale, "zh")

	switch templateKey {
	case "auth.register_code":
		if zh {
			return "TokenDance 注册验证码", fmt.Sprintf(
				"您好！\n\n您正在注册 TokenDance 账号，本次验证码为：%s\n\n"+
					"验证码 10 分钟内有效，请勿泄露给他人。如果这不是您本人的操作，请忽略此邮件。\n\nTokenDance 团队", code)
		}
		return "Your TokenDance verification code", fmt.Sprintf(
			"Hi there!\n\nYour TokenDance verification code is: %s\n\n"+
				"The code expires in 10 minutes and should not be shared. If you didn't request it, you can safely ignore this email.\n\n— The TokenDance team", code)
	case "auth.password_reset_code":
		if zh {
			return "TokenDance 重置密码验证码", fmt.Sprintf(
				"您好！\n\n您正在重置 TokenDance 账号密码，本次验证码为：%s\n\n"+
					"验证码 10 分钟内有效，请勿泄露给他人。如果这不是您本人的操作，请立即修改密码并联系我们。\n\nTokenDance 团队", code)
		}
		return "Your TokenDance password reset code", fmt.Sprintf(
			"Hi there!\n\nYour TokenDance password reset code is: %s\n\n"+
				"The code expires in 10 minutes and should not be shared. If you didn't request it, please reset your password immediately and contact us.\n\n— The TokenDance team", code)
	case "teams.invitation":
		return renderTeamInvitation(zh, payloadJSON)
	}
	return "TokenDance: " + strings.ReplaceAll(templateKey, "_", " "), payloadJSON
}

func renderTeamInvitation(zh bool, payloadJSON string) (string, string) {
	var payload struct {
		InvitationID string `json:"invitationId"`
		Role         string `json:"role"`
		TeamName     string `json:"teamName"`
		InviterName  string `json:"inviterDisplayName"`
		URL          string `json:"url"`
		ExpiresAt    string `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		if zh {
			return sanitizeEmailSubject("TokenDance 团队邀请"), "您收到一份团队邀请。请登录 TokenDance 查看详情。\n\nTokenDance 团队"
		}
		return sanitizeEmailSubject("TokenDance team invitation"), "You received a team invitation. Sign in to TokenDance to review it.\n\n— The TokenDance team"
	}
	teamName := strings.TrimSpace(payload.TeamName)
	if teamName == "" {
		if zh {
			teamName = "一个团队"
		} else {
			teamName = "a team"
		}
	}
	inviter := strings.TrimSpace(payload.InviterName)
	if inviter == "" {
		if zh {
			inviter = "一位成员"
		} else {
			inviter = "a teammate"
		}
	}
	role := localizedTeamRole(payload.Role, zh)
	url := sanitizeInviteURL(payload.URL)
	expires := strings.TrimSpace(payload.ExpiresAt)
	if zh {
		body := fmt.Sprintf("您好！\n\n%s 邀请你以%s身份加入「%s」。\n", inviter, role, teamName)
		if url != "" {
			body += "\n请在登录后打开以下页面确认加入：\n" + url + "\n"
		} else {
			body += "\n请登录 TokenDance 后在邀请页确认加入。\n"
		}
		if expires != "" {
			body += "\n邀请有效期至：" + expires + "\n"
		}
		if payload.InvitationID != "" {
			body += "\n邀请编号：" + payload.InvitationID + "\n"
		}
		body += "\n如果这不是写给你的邀请，请忽略此邮件。\n\nTokenDance 团队"
		return sanitizeEmailSubject("TokenDance 团队邀请：" + teamName), body
	}
	body := fmt.Sprintf("Hi there!\n\n%s invited you to join %s as %s.\n", inviter, teamName, role)
	if url != "" {
		body += "\nSign in and open this page to confirm:\n" + url + "\n"
	} else {
		body += "\nSign in to TokenDance and confirm the invitation from your team page.\n"
	}
	if expires != "" {
		body += "\nThis invitation expires at: " + expires + "\n"
	}
	if payload.InvitationID != "" {
		body += "\nInvitation ID: " + payload.InvitationID + "\n"
	}
	body += "\nIf this invitation was not meant for you, you can ignore this email.\n\n— The TokenDance team"
	return sanitizeEmailSubject("TokenDance team invitation: " + teamName), body
}

func localizedTeamRole(role string, zh bool) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin":
		if zh {
			return "管理员"
		}
		return "an admin"
	default:
		if zh {
			return "成员"
		}
		return "a member"
	}
}

func sanitizeEmailSubject(subject string) string {
	subject = strings.ReplaceAll(subject, "\r", " ")
	subject = strings.ReplaceAll(subject, "\n", " ")
	return strings.TrimSpace(subject)
}

func sanitizeInviteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n<>") {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(lower, "https://") {
		return raw
	}
	return ""
}
