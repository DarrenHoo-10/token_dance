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
	}
	return "TokenDance: " + strings.ReplaceAll(templateKey, "_", " "), payloadJSON
}
