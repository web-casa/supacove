// Package jobs — Phase 8: remediation guidance for the seven error classes
// (dev-plan task: 失败诊断七类的排查文案). The texts are actionable
// first steps, not guarantees; they never embed job-specific data.
package jobs

// RemediationFor returns the operator-facing troubleshooting steps for an
// error class. Unknown/empty classes get a generic entry.
func RemediationFor(class string) string {
	switch class {
	case ClassNetwork:
		return "The database host could not be reached or timed out. Check: host/port reachability from the supabackup host; DNS; firewall rules; for Neon, cold starts can take seconds (retry once before investigating); for Railway, verify the TCP proxy is from an allowed location."
	case ClassAuth:
		return "Authentication was rejected. Check: username/password; password expiry; for Supabase, use the direct connection string (not the pooler) with the postgres user; for Neon, the role/password may have been reset in the console."
	case ClassPermission:
		return "The server refused an operation for this user. Check: the role has CONNECT and appropriate schema privileges; pg_dump needs SELECT on tables (or pg_read_all_data); some managed platforms restrict pg_catalog access — the platform guide lists known limits."
	case ClassClientVer:
		return "The pg_dump client is older than the server and cannot dump from it. Install a matching or newer postgresql-client for the server's major version (the container ships 14-18); verify with `supabackup version` and the manifest's tool versions."
	case ClassDisk:
		return "Local storage ran out or became unwritable. Check: free space on the data/staging volume; the staging quota (SB_STAGING_QUOTA_BYTES); volume permissions for the runtime user (UID 10001 in the container)."
	case ClassStorageUp:
		return "The remote upload or verification failed, or the retention cleanup failed. The local artifact is retained. Check: object-store credentials and endpoint reachability; bucket existence and write permission; the destination's diagnostic test button; transient provider errors usually succeed on the next run."
	case ClassVerify:
		return "The dump was produced but failed verification. The artifact is retained but marked failed — inspect the error detail; common causes are pg_dump warnings treated as errors or an interrupted stream. Re-run the backup before trusting any earlier artifact."
	default:
		return "Unclassified failure. Check the server log around this job's timestamp for the redacted error detail, then re-run the backup; if it reproduces, file an issue with the error class and (redacted) message."
	}
}
