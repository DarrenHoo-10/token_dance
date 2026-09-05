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
