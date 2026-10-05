// Boundary translation for app-owned messages that are produced inside
// domain packages without request context (sentinels, fmt.Errorf templates).
// Matching is by exact sentinel text or stable prefix; anything unrecognized
// falls back to the original English so a drifted message can never render
// as the wrong language — it degrades, never mistranslates.
package server

import (
	"context"
	"strings"

	"github.com/cloudfan/supabackup/backend/internal/i18n"
)

// errJSONL is not needed — call sites use i18n.T directly inside errJSON.

// authMsg localizes the fixed validation texts of auth.ValidateUsername /
// auth.ValidatePassword (the min/max constants live in the auth package).
func authMsg(ctx context.Context, err error) string {
	switch err.Error() {
	case "username must be 3-64 characters":
		return i18n.T(ctx, err.Error(), "用户名长度需为 3–64 个字符")
	case "username may contain letters, digits, dot, dash, underscore only":
		return i18n.T(ctx, err.Error(), "用户名只能包含字母、数字、点、横线、下划线")
	case "password must be at least 12 characters":
		return i18n.T(ctx, err.Error(), "密码长度至少 12 个字符")
	case "password must be at most 128 characters":
		return i18n.T(ctx, err.Error(), "密码长度最多 128 个字符")
	default:
		return err.Error()
	}
}

// webhookURLMsg localizes the app-owned SSRF validation texts of
// validateWebhookURL; wrapped url.Parse failures keep their English detail.
func webhookURLMsg(ctx context.Context, err error) string {
	msg := err.Error()
	switch msg {
	case "URL scheme must be http or https":
		return i18n.T(ctx, msg, "URL 协议必须是 http 或 https")
	case "URL must include a host":
		return i18n.T(ctx, msg, "URL 必须包含主机名")
	case "link-local addresses are not allowed (cloud metadata protection)":
		return i18n.T(ctx, msg, "不允许链路本地地址（云元数据防护）")
	case "metadata endpoints are not allowed":
		return i18n.T(ctx, msg, "不允许云元数据端点")
	}
	if rest, ok := strings.CutPrefix(msg, "invalid URL: "); ok {
		return i18n.T(ctx, "invalid URL: ", "URL 无效：") + rest
	}
	return msg
}

// cronMsg localizes the fixed part of scheduler.ValidateCronExpr errors; the
// embedded expression and the third-party parser detail stay as-is.
func cronMsg(ctx context.Context, err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "invalid cron expression "); ok {
		return i18n.T(ctx, "invalid cron expression ", "无效的 cron 表达式 ") + rest
	}
	if rest, ok := strings.CutPrefix(msg, "cron expression "); ok {
		_, after, _ := strings.Cut(rest, " ")
		return i18n.T(ctx, "cron expression ", "cron 表达式 ") + rest[:len(rest)-len(after)-1] +
			i18n.T(ctx, " has no reachable fire time", " 没有可达的触发时间")
	}
	return msg
}

// webhookCreateMsg localizes the fixed validation texts of
// jobs.CreateWebhook (name/events); unknown errors pass through.
func webhookCreateMsg(ctx context.Context, err error) string {
	switch err.Error() {
	case "name must be 1-100 characters":
		return i18n.T(ctx, err.Error(), "name 长度需为 1–100 个字符")
	}
	if rest, ok := strings.CutPrefix(err.Error(), "unknown event type: "); ok {
		return i18n.T(ctx, "unknown event type: ", "未知的事件类型：") + rest
	}
	return err.Error()
}

// uploadInFlightMsg localizes the destination-busy text (a fresh error each
// time, so matching is by the fixed string).
func uploadInFlightMsg(ctx context.Context, err error) string {
	if err.Error() == "an upload for this destination is in flight" {
		return i18n.T(ctx, "an upload for this destination is in flight", "该目的地有一个上传正在进行中")
	}
	return err.Error()
}

// assignMsg localizes the assign-busy text (the English text also serves as
// the match key on this path).
func assignMsg(ctx context.Context, err error) string {
	if strings.Contains(err.Error(), "not found or a job is active") {
		return i18n.T(ctx, "database not found or a job is active", "数据库不存在或有任务正在进行中")
	}
	return err.Error()
}

// deliveryDetailMsg localizes the webhook test-delivery outcomes; %d details
// keep their number.
func deliveryDetailMsg(ctx context.Context, detail string) string {
	if detail == "delivery failed (network or refused destination; see server log)" {
		return i18n.T(ctx, detail, "投递失败（网络错误或目标拒绝连接；详见服务器日志）")
	}
	if rest, ok := strings.CutPrefix(detail, "receiver returned status "); ok {
		return i18n.T(ctx, "receiver returned status ", "接收方返回状态码 ") + rest
	}
	if detail == "request build failed" {
		return i18n.T(ctx, detail, "请求构造失败")
	}
	return detail
}
