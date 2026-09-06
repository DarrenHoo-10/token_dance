package email

import (
	"strings"
	"testing"
)

func TestRenderEmailRegisterCodeLocalized(t *testing.T) {
	subject, body := renderEmail("auth.register_code", "zh-CN", `{"code":"123456"}`)
	if subject != "TokenDance 注册验证码" {
		t.Fatalf("unexpected subject %q", subject)
	}
	if !strings.Contains(body, "123456") {
		t.Fatalf("body should contain the code, got %q", body)
	}

	subject, body = renderEmail("auth.register_code", "en-US", `{"code":"654321"}`)
	if subject != "Your TokenDance verification code" {
		t.Fatalf("unexpected subject %q", subject)
	}
	if !strings.Contains(body, "654321") {
		t.Fatalf("body should contain the code, got %q", body)
	}
}

func TestRenderEmailPasswordResetLocalized(t *testing.T) {
	subject, body := renderEmail("auth.password_reset_code", "zh-CN", `{"code":"246810"}`)
	if subject != "TokenDance 重置密码验证码" {
		t.Fatalf("unexpected subject %q", subject)
	}
	if !strings.Contains(body, "246810") {
		t.Fatalf("body should contain the code, got %q", body)
	}
}

func TestRenderEmailUnknownTemplateFallsBackToPayload(t *testing.T) {
	subject, body := renderEmail("verification_code", "en-US", `{"code":"123456"}`)
	if subject != "TokenDance: verification code" {
		t.Fatalf("unexpected subject %q", subject)
	}
	if body != `{"code":"123456"}` {
		t.Fatalf("fallback body should be the raw payload, got %q", body)
	}
}

func TestRenderEmailTeamInvitationLocalized(t *testing.T) {
	payload := `{"invitationId":"tiv_0123456789abcdefghijklmnop","role":"member","teamName":"星河开发组","inviterDisplayName":"Ada","url":"/teams/invitations/tiv_0123456789abcdefghijklmnop","expiresAt":"2026-09-13T08:00:00.000Z"}`
	subject, body := renderEmail("teams.invitation", "zh-CN", payload)
	if !strings.Contains(subject, "团队邀请") || strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("unexpected zh subject %q", subject)
	}
	if !strings.Contains(body, "星河开发组") || !strings.Contains(body, "Ada") || !strings.Contains(body, "成员") {
		t.Fatalf("zh body missing fields: %q", body)
	}
	if strings.Contains(body, payload) {
		t.Fatalf("zh invitation body leaked raw json")
	}

	subject, body = renderEmail("teams.invitation", "en-US", payload)
	if !strings.Contains(subject, "team invitation") || strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("unexpected en subject %q", subject)
	}
	if !strings.Contains(body, "Ada") || !strings.Contains(body, "a member") || !strings.Contains(body, "/teams/invitations/") {
		t.Fatalf("en body missing fields: %q", body)
	}
}

func TestRenderEmailTeamInvitationNeverFallsBackToRawJSON(t *testing.T) {
	raw := `{"not":"a valid invitation","url":"javascript:alert(1)"}`
	subject, body := renderEmail("teams.invitation", "en-US", raw)
	if strings.Contains(body, raw) || strings.Contains(body, "javascript:") {
		t.Fatalf("invitation template leaked raw payload or unsafe url: %q", body)
	}
	if !strings.Contains(subject, "invitation") {
		t.Fatalf("expected generic invitation subject, got %q", subject)
	}

	_, body = renderEmail("teams.invitation", "zh-CN", `{not-json`)
	if strings.Contains(body, "{not-json") {
		t.Fatalf("invalid json fell back to raw payload: %q", body)
	}
	if !strings.Contains(body, "团队邀请") {
		t.Fatalf("expected generic zh invitation body, got %q", body)
	}
}
