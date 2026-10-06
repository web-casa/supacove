# 整体评审（六域并行）— 基线 8b5f5e0 → 修复后 95d26a4

日期：2026-10-06。Codex 额度耗尽（10-10 恢复），本轮改用六个并行评审域：安全与认证、备份内核与协议、可靠性与生命周期、API/统计/指标、前端、发布/CI/文档/合规。各域报告要点合并如下；评审期间发现并已修复的问题标注 **[已修]** 及其提交。

## 总体结论

**核心正确性扎实，无 P0 级数据丢失/假成功/密钥泄漏。** 六个域合计发现 3 个已修复的阻塞项（fresh-clone 编译断裂、/metrics 匿名暴露、IPv6 元数据 SSRF 缺口）与 8 个尚未修复的 P1。剩余 P1 集中在两处：备份内核的故障期资源管理（工件回收、执行时限、停机状态语义），以及统计口径（dump 体积实为密文、export 分母含未尝试任务）。**公开 beta 的判定见文末。**

## 已修复项（评审期间）

| 域 | 发现 | 修复 | 提交 |
|---|---|---|---|
| 发布 | `backend/internal/web/dist/.gitkeep` 被 i18n 提交连带删除，fresh clone 无法编译（CI 首跑必红） | 恢复占位文件，fresh-clone 构建实测通过 | 239b26e |
| 安全 | `/metrics` 在 /api 前缀外，绕过 guard 默认拒绝，匿名可读库名/暂存量/任务结果 | guard 显式纳入 /metrics + no-store；回归测试（匿名 401/认证 200） | 8b5f5e0 |
| 安全 | SSRF dial 控制不拦 AWS IPv6 元数据（fd00:ec2::254）与 NAT64 编码 | 新 internal/netguard 统一边界（link-local + IMDSv6 + NAT64），接入 outbox dial Control 与 validateWebhookURL；双向单测 | 8b5f5e0 |
| 前端 | 390px 登录页语言按钮竖排折断；死词典键 common.requestFailed | `white-space:nowrap`；删键 | 95d26a4 |

## 未修复发现（按域）

### 备份内核与协议（2 P1，有条件放行）
- **P1-K1** 失败/未提交工件无回收路径：目的地长期故障时 staging 涨满配额→全库备份停摆且无自愈；dev-plan 协议 D 的宽限期/deleting 状态机未实现。
- **P1-K2** dump/上传无执行时限：单 worker 可被网络黑洞无限钉死（S3 SDK 无整体超时）；ResumeRemotePhase 同步执行还会钉死启动。
- P2：manifest 对象引用与桶内键脱节；recovery kit 不发布到远端（与 DR 文档场景 3 措辞冲突）；manifest 无 TOC；PlaintextArc 实为密文（与 API-P1-1 同源）；jobUUID 查询错误被吞；远端删除顺序制造假提交窗口；B2 版本化删除语义未处理等 12 条。
- 实测亮点：MinIO 真实 S3 E2E、Phase 6 真实全链路（dump→age→内嵌 PG 恢复→kit 恢复+canary）、ADR-004 信任边界 spike 均 PASS。

### 可靠性与生命周期（3 P1，阻塞判定：是）
- **P1-R1**（新发现，实测）优雅停机（SIGTERM/docker stop）把 running 任务落为 `failed` 而非 `interrupted`：触发假失败通知 + 假 /fail 心跳，并击穿协议 C 重启续传（failed+uploading 的任务 ResumeRemotePhase 不扫）。与 jobs.go 注释的停机承诺矛盾。
- **P1-R2** = P1-K1（失败工件零回收 + 配额锁定，实测放大效应）。
- **P1-R3** = P1-K2（无执行时限，实测挂死 pg_dump 永久阻塞）。
- P2：迁移 0008 Down 段混入 Up 语句（v8 以下回退必败，实测）；RecoverInterrupted 注解非幂等（重启追加膨胀，实测）；outbox dead 无 redrive/清理；Notifier.Stop 双关 panic、Scheduler.Stop 不 join、心跳 goroutine 脱离 wg；scheduler 冲突日志谎报游标推进；scheduler 包零测试。

### API/统计/指标（2 P1，不阻塞底线但建议 beta 前处理）
- **P1-A1**（实测）`totalDumpBytes` 计的是 age 密文而非压缩归档，与 `totalArtifactBytes` 同值——"三体积"实为两体积，dashboard 同数显示两遍；历史评审早已指出未修。
- **P1-A2**（实测）export 分母含 canceled/interrupted（从未尝试 dump），与自身注释矛盾，压低成功率；契约无 description。
- P2：scheduleDue 死契约字段（底层游标硬编码 0，补映射即全库误报到期）；41 处 500 响应为空 Error `{"code":"","message":""}`；notifications limit>200 静默钳到 50；platform 枚举运行时不校验；webhook url maxLength 未强制；多数路径未声明 401/403；interrupted 计数不可见；CancelTask 把 DB 错误误标 409 且未本地化；stats.Summary 死代码；metrics 部分失败仍渲染可信 0；/metrics 全表扫描。
- 实测确认已修的历史问题：export≠overall、通知分段已交付、avgDuration unknown、archive 覆盖率声明、GROUP BY 线性计划、webhook 并发唯一、i18n 匹配表与英文回退策略、/metrics 认证。

### 安全与认证（修复后无阻塞）
- 修复后剩余 P2：storage destination endpoint 不在 SSRF 边界内（仅管理员配置、状态码级 oracle）；redact.Secrets 不匹配 \uXXXX 转义形态（当前路径不可达，记为已知限制）；sanitizeForStore 死代码。
- 历史 P0/P1 修复全部在位且有命名测试锚定；脱敏链、会话/CSRF、下载遏制三条主线实测未破。

### 前端（无 P0/P1）
- 10 条 P2 中已修 2 条；余：切语言后已缓存服务端消息 15–30s 延迟换语言（已知取舍，轮询自愈）；登出失败静默；presigned window.open 弹窗拦截风险；tabs 语义不全；时间戳均为 Tab 停靠点；无 React 错误边界；单 chunk 369KB。
- 实测：lint/tsc/build/audit 全绿；en/zh 313 key 对称；CSRF/401/下载授权/两步删除全验证。

### 发布/CI/文档/合规（阻塞公开 beta 的外部动作清单）
- P1：CI 从未运行（无 remote）、扫描声明无绑定 commit 的证据、image job 不依赖 security job、Trivy 缺失、三平台恢复演练缺失、deployment.md 把 dev compose（含 SB_INSECURE_COOKIE=1 与弱口令）当推荐生产路径、裸机 Go 版本写 1.24（实为 1.26.6）、clone URL 占位符、org 仓库缺 GITLEAKS_LICENSE。
- P2：govulncheck@latest 浮动、CI 无 tags 触发/permissions/concurrency、Dockerfile GO_VERSION 浮动、默认 stage 是 runtime-spike、无双架构/release workflow、e2e/ 与前端无测试、.zcode/plans 入库、缺 SECURITY.md/CONTRIBUTING/issue 模板、CHANGELOG 无日期无 tag、shellcheck 两处轻微、无 SPDX 头。

## 公开 beta 判定

**代码侧：修复 P1-R1（停机状态语义）后即可进入受控 beta**——它是唯一会在最常见运维路径（docker stop/升级）上产生错误告警并破坏续传承诺的问题，修复面小。P1-K1/K2（工件回收、执行时限）与 P1-A1/A2（统计口径）建议在 beta 首月内关闭：前者是慢性必然故障（目的地凭据过期即全库停摆），后者是用户可见的误导数字。

**发布侧（需你操作）**：推 remote → CI 全绿（含 image needs security、补 Trivy）→ branch protection required checks → 修 deployment.md 生产路径 → 推送前全历史 gitleaks → 清理 .zcode/plans → 决定是否随仓公开 docs/reviews。三平台恢复演练与双架构镜像归入正式发布门禁，beta 可在 README 声明"仅源码构建 + 已知限制"。
