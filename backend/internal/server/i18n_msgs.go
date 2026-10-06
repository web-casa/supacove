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
	"github.com/cloudfan/supabackup/backend/internal/redact"
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
	case "link-local and cloud metadata addresses are not allowed":
		return i18n.T(ctx, msg, "不允许链路本地地址与云元数据地址")
	case "metadata endpoints are not allowed":
		return i18n.T(ctx, msg, "不允许云元数据端点")
	}
	if rest, ok := strings.CutPrefix(msg, "invalid URL: "); ok {
		return i18n.T(ctx, "invalid URL: ", "URL 无效：") + rest
	}
	return msg
}

// cronMsg localizes the fixed part of scheduler.ValidateCronExpr errors. The
// quoted expression may contain spaces, so the unreachable-time branch is
// rewritten by matching the fixed prefix AND suffix — never by splitting —
// and the English output stays byte-identical to the original error.
func cronMsg(ctx context.Context, err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "invalid cron expression "); ok {
		return i18n.T(ctx, "invalid cron expression ", "无效的 cron 表达式 ") + rest
	}
	const noFire = " has no reachable fire time"
	if rest, ok := strings.CutPrefix(msg, "cron expression "); ok {
		if expr, ok := strings.CutSuffix(rest, noFire); ok {
			return i18n.T(ctx, "cron expression ", "cron 表达式 ") + expr +
				i18n.T(ctx, noFire, " 没有可达的触发时间")
		}
	}
	return msg
}

// pgURIMsg localizes the app-owned pgclient.ParseURI validation reasons; the
// dynamic values (quoted host/port) stay as-is.
func pgURIMsg(ctx context.Context, err error) string {
	msg := err.Error()
	if zh, ok := pgURIFixed[msg]; ok {
		return i18n.T(ctx, msg, zh)
	}
	for prefix, zhPrefix := range pgURIPrefixes {
		if rest, ok := strings.CutPrefix(msg, prefix); ok {
			return i18n.T(ctx, prefix, zhPrefix) + rest
		}
	}
	return msg
}

var pgURIFixed = map[string]string{
	"connection URI is not a valid URL":                                           "连接 URI 不是有效的 URL",
	"URI scheme must be postgres:// or postgresql://":                             "URI 协议必须是 postgres:// 或 postgresql://",
	"connection URI query string is malformed":                                    "连接 URI 的查询字符串格式错误",
	"connection URI must not contain a fragment":                                  "连接 URI 不能包含 # 片段",
	"URI is missing the host":                                                     "URI 缺少主机名",
	"empty host":                                                                  "主机名为空",
	"zone identifiers and multi-host lists are not supported":                     "不支持 zone 标识符和多主机列表",
	"IPv6 zone identifiers are not supported":                                     "不支持 IPv6 zone 标识符",
	"URI is missing the user":                                                     "URI 缺少用户",
	"URI is missing the database name":                                            "URI 缺少数据库名",
	"connection fields must not contain control characters (NUL, CR, LF, VT, FF)": "连接字段不能包含控制字符（NUL、CR、LF、VT、FF）",
	"application_name too long":                                                   "application_name 过长",
	"remote connections must set sslmode explicitly (recommend verify-full; allow/prefer/require/verify-ca/disable are accepted for explicit choices)": "远程连接必须显式设置 sslmode（推荐 verify-full；也接受 allow/prefer/require/verify-ca/disable）",
}

var pgURIPrefixes = map[string]string{
	"invalid port ":                     "端口无效：",
	"invalid host ":                     "主机名无效：",
	"invalid sslmode ":                  "sslmode 无效：",
	"invalid connect_timeout ":          "connect_timeout 无效：",
	"application_name: ":                "application_name：",
	"parameter ":                        "参数 ",
	"unsupported connection parameter ": "不支持的连接参数 ",
}

// destinationMsg localizes the app-owned destination name and storage config
// validation texts. Fixed texts are matched BEFORE secret redaction — they
// never contain credentials, and redaction of short keys would mangle them;
// provider diagnostics fall through to redaction and stay as-is otherwise.
func destinationMsg(ctx context.Context, err error, secrets []string) string {
	msg := err.Error()
	if zh, ok := destinationFixed[msg]; ok {
		return i18n.T(ctx, msg, zh)
	}
	return redact.Secrets(secrets, msg)
}

//nolint:gosec // G101: the map NAMES config keys in validation messages; no credentials live here
var destinationFixed = map[string]string{
	"name must be 1-100 characters":      "name 长度需为 1–100 个字符",
	"platform must be one of s3, r2, b2": "platform 必须是 s3、r2、b2 之一",
	"bucket name is not a valid S3 bucket name (3-63 chars, lowercase letters/digits/dot/dash)": "桶名不是有效的 S3 桶名（3–63 个字符，小写字母/数字/点/横线）",
	"endpoint is required for r2/b2 (https://host[:port])":                                      "r2/b2 需要填写 endpoint（https://host[:port]）",
	"endpoint must be http(s)://host[:port] when set":                                           "endpoint 一经设置必须是 http(s)://host[:port]",
	"region is required for b2 (see the B2 S3 endpoint's region)":                               "b2 需要填写 region（见 B2 S3 端点的 region）",
	"access key and secret key are required":                                                    "access key 和 secret key 为必填项",
	"prefix must not contain NUL or '..'":                                                       "prefix 不能包含 NUL 或“..”",
	"prefix may contain letters, digits, slash, dot, underscore, dash only":                     "prefix 只能包含字母、数字、斜杠、点、下划线、横线",
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
