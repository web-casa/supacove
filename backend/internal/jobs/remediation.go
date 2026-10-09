// Package jobs — Phase 8: remediation guidance for the seven error classes
// (dev-plan task: 失败诊断七类的排查文案). The texts are actionable
// first steps, not guarantees; they never embed job-specific data.
// Both languages live side by side (i18n.Msg) so they cannot drift; callers
// pick the language — API responses localize via Accept-Language, persisted
// job records stay English.
package jobs

import "github.com/web-casa/supacove/backend/internal/i18n"

// RemediationMsg returns the operator-facing troubleshooting steps for an
// error class in both supported languages. Unknown/empty classes get a
// generic entry.
func RemediationMsg(class string) i18n.Msg {
	switch class {
	case ClassNetwork:
		return i18n.Msg{
			En: "The database host could not be reached or timed out. Check: host/port reachability from the supacove host; DNS; firewall rules; for Neon, cold starts can take seconds (retry once before investigating); for Railway, verify the TCP proxy is from an allowed location.",
			Zh: "无法连接数据库主机或连接超时。请检查：supacove 所在主机到目标主机/端口的连通性；DNS；防火墙规则；Neon 冷启动可能需要数秒（先重试一次再排查）；Railway 请确认 TCP 代理所在区域是允许的位置。",
		}
	case ClassAuth:
		return i18n.Msg{
			En: "Authentication was rejected. Check: username/password; password expiry; for Supabase, use the direct connection string or the SESSION pooler (the TRANSACTION pooler / '-pooler' host does not support pg_dump); for Neon, the role/password may have been reset in the console.",
			Zh: "身份验证被拒绝。请检查：用户名/密码；密码是否过期；Supabase 请使用直连字符串或 SESSION pooler（TRANSACTION pooler / 主机名含“-pooler”不支持 pg_dump）；Neon 的角色/密码可能在控制台中被重置过。",
		}
	case ClassPermission:
		return i18n.Msg{
			En: "The server refused an operation for this user. Check: the role has CONNECT and appropriate schema privileges; pg_dump needs SELECT on tables (or pg_read_all_data); some managed platforms restrict pg_catalog access — the platform guide lists known limits.",
			Zh: "服务器拒绝了这个用户的某项操作。请检查：角色是否拥有 CONNECT 及相应的 schema 权限；pg_dump 需要表的 SELECT 权限（或 pg_read_all_data）；部分托管平台会限制 pg_catalog 访问——平台指南列出了已知限制。",
		}
	case ClassClientVer:
		return i18n.Msg{
			En: "The pg_dump client is older than the server and cannot dump from it. Install a matching or newer postgresql-client for the server's major version (the container ships 14-18); check the selected client with `<pg_dump path> --version` and compare against the manifest's server version.",
			Zh: "pg_dump 客户端版本低于服务器，无法从它导出。请安装与服务器主版本匹配或更新的 postgresql-client（容器内置 14–18）；用 `<pg_dump 路径> --version` 查看选中的客户端，并与清单中的服务器版本对比。",
		}
	case ClassDisk:
		return i18n.Msg{
			En: "Local storage ran out or became unwritable. Check: free space on the data/staging volume; the staging quota (SB_STAGING_QUOTA_BYTES); volume permissions for the runtime user (UID 10001 in the container).",
			Zh: "本地存储已写满或不可写。请检查：data/staging 卷的剩余空间；暂存配额（SB_STAGING_QUOTA_BYTES）；运行用户对卷的写权限（容器内 UID 10001）。",
		}
	case ClassStorageUp:
		return i18n.Msg{
			En: "The remote upload or its read-back verification failed. The local staged artifact is RETAINED (nothing needs a re-dump). Check: object-store credentials and endpoint reachability; bucket existence and write permission; the destination's diagnostic test button; transient provider errors usually succeed on the next run.",
			Zh: "远端上传或其回读校验失败。本地暂存的工件已保留（无需重新导出）。请检查：对象存储凭据与端点可达性；桶是否存在且有写权限；使用目的地的诊断测试按钮；供应商的瞬时错误通常下次运行即可成功。",
		}
	case ClassVerify:
		return i18n.Msg{
			En: "The dump pipeline failed its integrity verification. This class covers the DUMP phase only; async restore-verification results appear separately as the task's verifyStatus (a verified/failed value on a succeeded job). Inspect the error detail and re-run the backup.",
			Zh: "导出流水线未通过完整性校验。此类别仅覆盖导出阶段；异步恢复验证的结果单独显示为任务的 verifyStatus（成功任务上的 verified/failed 值）。请查看错误详情并重新备份。",
		}
	default:
		return i18n.Msg{
			En: "Unclassified failure. Check the server log around this job's timestamp for the redacted error detail, then re-run the backup; if it reproduces, file an issue with the error class and (redacted) message.",
			Zh: "未分类的失败。请在服务器日志中查找该任务时间戳附近的已脱敏错误详情，然后重新备份；如果可复现，请附带错误类别和（已脱敏的）消息提交 issue。",
		}
	}
}
