# Phase 8 首轮评审

评审日期：2026-10-04。对照 `docs/dev-plan.md:228` 的 Phase 8 任务及统一发布门禁。

## 范围与结论摘要

当前 HEAD 为 `a5b00323d9d862aad1c5f63d2cd5660e78ccfd2f`，即 Phase 8 提交本身；`git log a5b0032..HEAD` 实际为空。按照请求中“基线 a5b0032 前的 commit 到当前 HEAD”的描述，本次实际评审 `a5b0032^..HEAD`（父提交 `129d4af`），共 7 个变更文件，并检查必要的启动、路由、契约、镜像和测试上下文。未修改业务代码。

发现 **P0：0；P1：8；P2：4**。统计链路尚未接通，查询可确定失败；发布门禁缺少可复核证据。**不建议将本次提交标记为 Phase 8 完成或据此通过公测发布门禁。** 未取得扫描原始结果，不把未知漏洞直接定为 P0，也不据此宣称安全。

## P0

本次范围内未确认新的 P0。安全扫描缺少漏洞 ID、调用链和修复版本，因此无法判定所称 19 项中是否存在达到 P0 的可利用问题；这不是“已证明 P0 为零”。

## P1

### P1-01：聚合查询读错表，所有耗时 SQL 都引用不存在的列

位置：`backend/internal/stats/stats.go:79`、`:95`、`:120`；`backend/internal/server/stats_handler.go:15`；`backend/internal/db/migrations/0010_stats.sql:9`。

`duration_secs` 仅新增在 `backup_stats`，没有任何迁移为 `jobs` 添加该列。两个总体查询却从 `jobs` 读取它，分库和近期查询还读取 `j.duration_secs`。`COALESCE` 无法处理不存在的列。在应用全部 SQL Up 迁移后的 SQLite 上，逐一执行原查询，分别返回 `no such column: duration_secs` / `no such column: j.duration_secs`；空库也失败。`Recorder.Summary` 直接返回错误，handler 即使挂载也只能返回 500。

建议：统一查询实现，用稳定 job/database ID 关联真实统计来源，明确旧 job 无统计时的 NULL/未知语义；避免一对多关联重复 SUM/COUNT。修复后用真实迁移生成的数据库验证总体、分库、近期查询，不能只在手造含额外列的 schema 上测试。

### P1-02：统计写入器和 `/api/stats` 均未接入，表格展示也未交付

位置：`backend/internal/jobs/jobs.go:416`、`:838`；`backend/internal/server/stats_handler.go:10`。关联上下文：`backend/cmd/supabackup/main.go:208`、`backend/internal/server/server.go:187`、`:204`。

全仓搜索只有 `SetStatsRecorder` 定义，没有调用；`NewRunner` 不初始化 recorder，生产路径始终跳过写入。`handleStats` 同样只有定义，没有路由注册，OpenAPI、生成类型和前端没有 stats 接口/表格。正常认证用户请求 `/api/stats` 会进入 API 的 NotFound，而不是该 handler。统计包的 Summary（databases 数组、recent）与 handler 的独立响应（databases 数量）也不一致。

建议：启动时注入 recorder，按现有 OpenAPI/strict handler 模式增加契约和受保护路由，复用一个查询服务，完成表格展示。用 `httptest.NewRecorder` 经真实 Router 验证认证成功后返回 200、未认证拒绝，避免只直接调用 handler。

### P1-03：体积与分段状态记录失真，不满足首版统计口径

位置：`backend/internal/jobs/jobs.go:837`；`backend/internal/stats/stats.go:33`；`backend/internal/db/migrations/0010_stats.sql:3`。对照 `docs/dev-plan.md:22`、`:231`。

一旦接通 recorder，`DumpSize` 和 `ArtifactSize` 都取 `result.SizeBytes`（密文字节数）；现有 `dumper.Result.PlaintextArc` 才是已压缩 archive 字节数。表中没有源库物理体积及测量/未知状态，默认 0 会把“未测量”表达成零。远端提交成功的 job 也没有传 `RemoteCommitted`，记录为 false；验证状态固定 `not_run`。写入只发生在整体成功之后，导出成功但远端上传失败的记录缺失，无法统计导出、远端提交、验证、通知各自的分母与结果。单个 `successRate` 不符合计划明确要求的分段成功率。

建议：分别记录源库物理体积（不可测为 NULL）、`PlaintextArc`、密文大小和测量口径；从实际阶段结果持久化状态，覆盖失败/取消/中断/恢复提交路径，通知和验证的未配置、未执行、失败不能伪装为成功或零。

### P1-04：`/metrics` 删除现有监控序列，且 CHANGELOG 仍宣称存在

位置：`backend/internal/server/metrics.go:31`；`CHANGELOG.md:43`。

本提交将已有 `supabackup_jobs_total{status=...}` 替换为两个新名称，删除 pending/running/canceled/interrupted 序列、`supabackup_last_success_age_seconds`、活跃数据库/目的地数量。既有看板/告警查询会失去数据，尤其失去备份陈旧度指标；这是本次实际引入的回归。CHANGELOG 却继续写“jobs by status, last success age, databases, destinations”。

建议：保留已有序列并修正错误处理；如确需变更，应提供迁移与弃用安排及准确发布说明。增加指标名称、全部状态、陈旧度的回归断言。

### P1-05：安全扫描摘要不可审计，stdlib 来源不能视为应用不受影响

位置：`CHANGELOG.md:49`、提交 `a5b0032` 的 message；关联 `go.mod:3`、`Dockerfile:8`、`.github/workflows/ci.yml`。对照计划任务 3。

仓库只有“19 stdlib findings、0 code vulnerabilities”的文字，没有扫描输出、GO/CVE ID、可达调用链、扫描命令/版本/时间/退出码、目标构建信息及逐项处置。无法知道 19 是模块级命中、导入级命中还是可达符号级命中，也无法验证“0 code vulnerabilities”。stdlib 是漏洞来源；应用是否调用受影响路径是另一维度。升级工具链可能是修复途径，但当前实际运行仍为 Go 1.26.0，不能把“上游有修复”写成发布产物已修复。govulncheck 也不替代应用鉴权、注入、secret 等人工审查。

官方说明：扫描取决于具体构建配置，源码分析显示受影响调用链，并有静态分析限制；JSON/SARIF 输出模式即使检出漏洞也可能退出 0，CI 不能只检查该模式的进程退出码。[govulncheck 官方文档](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)。

本次尝试 `govulncheck ./backend/...`，因沙箱禁止 DNS/socket 请求 `vuln.go.dev` 而退出 1，属于**扫描未完成**，不是“发现漏洞”或“扫描通过”。未复现那 19 项，也不臆造 ID/修复版本。现有 CI 没有 govulncheck、Trivy、gitleaks 门禁，本提交没有四出口 secret canary 的复查记录。

建议：提交脱敏原始报告与逐项来源/可达性/利用前提/修复版本表；选定覆盖相关修复的工具链重建实际镜像并复扫，记录 binary build info 与镜像 digest；补齐镜像、secret 和四出口复查的判定流程。

### P1-06：推荐部署流程实际启用开发 Compose，生产恢复能力易被误解

位置：`docs/deployment.md:8`、`:43`、`:78`；关联 `compose.yaml:1`、`Dockerfile` 的 runtime/runtime-spike。

指南将 `docker compose up -d` 作为推荐部署，但仓库 Compose 明确是开发环境：`SB_INSECURE_COOKIE=1`、debug 日志、固定 PG/MinIO 凭据，PG 和 MinIO 数据位于 tmpfs。端口仅绑定 loopback 降低直接暴露风险，但这不等于生产配置；经反代访问时仅设置 `SB_PUBLIC_ORIGIN` 也不会自动关闭 insecure cookie。操作者若把示例 MinIO 用作备份目的地，容器销毁/重建会丢失对象。

此外发布 runtime 只安装 PG 客户端，没有 initdb/postgres 服务端二进制；启动路径没有接入 verifier，CHANGELOG 的嵌入式验证表述应限定为现有模块能力，不能暗示推荐镜像已执行验证。

建议：提供可直接使用的生产示例（TLS、Secure cookie、持久数据、UID 10001 卷权限、真实 BYOS、secret 挂载），明确开发 Compose 的临时数据边界和验证支持状态；不要要求用户自行猜测安全覆盖参数。

### P1-07：恢复指南缺少关键自检，复制命令会暴露凭据及明文

位置：`docs/deployment.md:46`、`:55`、`:63`；对照计划协议 E、Phase 8 发布材料/真实恢复门禁。

指南直接解密并将含密码 URI 传给 `pg_restore`/`psql`，密码进入 argv 和可能的 shell history；identity 重定向文件与明文 dump 均未要求 0600/受限 umask，未写明明文落盘及清理。没有下载 manifest、核对完整密文 SHA-256、格式/工具版本、角色/扩展依赖、平台 profile 和目标空库检查；`SELECT count(*) FROM pg_tables` 包含系统表，不能证明预期业务对象或数据恢复成功。单个通用模板没有体现 Supabase 专用恢复边界。

建议：使用受限 `PGPASSFILE`，安全创建 identity/临时明文，增加解密前密文哈希校验（注明检错非来源签名）和 profile/版本依赖检查，恢复失败明确目标可能已部分写入；给出三平台独立恢复步骤与 fixture 数据断言。脚本语法通过也不能替代实际恢复演练。

### P1-08：升级、灾难恢复、三平台演练与容量门禁缺少发布证据

位置：`docs/deployment.md:74`、`CHANGELOG.md:5`；关联 `backend/internal/db/upgrade_test.go`、`.github/workflows/ci.yml`。对照 `docs/dev-plan.md:232`、`:234`、`:239`。

既有 upgrade 测试覆盖历史 v1 schema 到当前版本，但本提交没有带历史 job/凭据的 v9→v10 专项迁移、迁移失败后的恢复演练。文档只解释主密钥丢失，缺少 SQLite/WAL 丢失、仅桶+私钥幸存时的操作、旧桶归属/清理禁用边界、升级前备份与失败恢复流程。没有三平台从桶下载到新目标手动恢复的脱敏记录；`scripts/spike1-supabase-restore.sh` 的存在不能证明本发布版本已完成三平台门禁。

容量材料也未给出固定测试机、低压缩比 fixture、dump/upload/verify 分段耗时、峰值 RSS/磁盘、总负载边界和 RPO 语义；CI 无法判定这些发布门禁。alpha 标签可以说明成熟度，但不能作为计划共用 beta 发布门禁已完成的证据。

建议：补充带 commit/artifact/profile/工具版本/数据断言的演练报告、迁移与灾难恢复操作清单、容量报告，并让发布流程检查必需证据；未覆盖的平台应公开缩减支持范围。

## P2

### P2-01：总体、分库、近期过滤与身份口径不一致

位置：`backend/internal/stats/stats.go:86`、`:101`、`:124`。

总体含软删除库和 interrupted，分库只含活跃且至少一次 succeeded 的库，近期又包含软删除库但排除 canceled/interrupted。仅失败或从未备份的活跃库在分库表完全消失；总数也不一定等于 succeeded+failed+canceled。软删除会改名，近期 JOIN 显示 tombstone 名而非历史名称。`GROUP BY d.id,d.name` 本身按稳定 ID 分组是正确的，但响应只返回 name，没有 ID。`ORDER BY j.id` 表示入队顺序，不能保证完成时间的最近顺序。

复现：仅为后续诊断在临时 SQLite 的 jobs 添加 duration 列，准备 3 个库（活跃成功、活跃仅失败、软删除成功）和 5 个终态 job（2 成功、1 失败、1 中断、1 取消）：总体 total=5、succeeded=2、failed=1、canceled=1；分库仅返回活跃成功库；近期返回软删除 tombstone、失败、成功三条。该辅助列未写入仓库，不能视为 P1-01 已修复。

建议：明确历史/活跃统计范围与成功率分母，展示 interrupted，分库采用保留零结果库的 LEFT JOIN 并在 JOIN 内放状态条件，返回 ID，保留历史名称，最近完成按 finished_at+id 排序。若保留累计产出字节，应说明它不等于当前存储占用。

### P2-02：统计 HTTP 辅助查询吞错，metrics 复用变量可输出错误计数

位置：`backend/internal/server/stats_handler.go:35`、`:40`；`backend/internal/server/metrics.go:32`、`:38`。

最近成功、库/目的地数量的 Scan 错误被忽略，会返回 200 和看似有效的零值；metrics 第二次查询失败时，同一个 n 可能保留第一次成功数并被输出为失败数。建议统一处理 Query/Scan 错误、记录脱敏诊断，查询失败不要冒充真实零值，并给出对应故障注入测试。

### P2-03：新增迁移缺少单 job 唯一约束与补偿策略

位置：`backend/internal/db/migrations/0010_stats.sql:5`；`backend/internal/stats/stats.go:21`；`backend/internal/jobs/jobs.go:837`。

注释称每 job 一条，但 job_id 没有 UNIQUE，重复 Record 会产生多条；成功 job 更新与 best-effort Record 不在同一事务，期间退出或写入失败会永久缺统计，恢复远端成功路径也不补记。当前查询还未读此表，不能声称现状已因重复行造成聚合翻倍；接入 JOIN 后则须防范。

建议：为 job_id 加唯一约束，定义幂等插入/更新、阶段状态更新及启动补偿策略；验证重复调用、崩溃窗口和恢复路径。

### P2-04：发布说明与实际版本/支持契约不同步

位置：`docs/deployment.md:6`、`:11`、`:79`、`:80`；`CHANGELOG.md:39`、`:43`；`README.md:5`。

文档写 Go 1.24+，实际 go.mod 要求 1.26.0（旧 Go 可能自动下载工具链，并非直接满足版本要求）；clone URL 仍为 `your-org` 占位；README 仍为 Phase 1 开发中。指南声称 API Token 自动化，但当前 guard/契约只有 session cookie，bootstrap/CSRF token 不是自动化 API Token。metrics 默认认证是真的，但反代 IP 白名单本身不能替代应用会话认证。

建议：按发布时实际能力修订版本、仓库地址、认证和监控接入示例，补发布日期/可定位版本、升级与恢复文档链接，并准确表述已实现、未接入和未验证能力。

## 验证结果与测试缺口

| 验证 | 结果与边界 |
|---|---|
| `GOCACHE=/tmp/codex-phase8-go-cache go build ./backend/...` | 通过，Go 1.26.0 linux/arm64。出现 module stat cache 只读警告，最终退出 0；不是镜像构建验证。 |
| `GOCACHE=/tmp/codex-phase8-go-cache go test -race ./backend/...` | 整体退出 1：server 首个测试启动 httptest listener 被环境拒绝（socket: operation not permitted）。其余带测试的包通过；不能称全套 race 通过，不能把环境限制计为实现缺陷。 |
| Python SQLite 执行所有 SQL Up 迁移及原统计查询 | SQL 迁移通过；4 个含 duration 的查询全部复现缺列。Go migration 0003 属于认证迁移，此 SQL 复现不替代 goose 完整升级测试。 |
| Python YAML | compose、OpenAPI、codegen 配置、CI YAML 可解析。 |
| `bash -n` | 两个 scripts/*.sh 以及 CI 所有 run 脚本语法通过。 |
| `govulncheck ./backend/...` | 网络/DNS 被沙箱阻止，扫描未完成。没有给 19 项摘要背书。 |

Phase 8 提交没有新增任何测试；stats 包输出 `[no test files]`。优先补充：

1. 从真实迁移 schema 执行所有查询：空库、各终态、仅失败库、零记录库、软删除、不同完成顺序、缺统计、重复记录、NULL 未测量，以及分段分母。
2. 经实际启动装配完成一次成功/失败/远端恢复提交，断言 backup_stats 数量、三体积和远端状态；经过真实 Router 检查 stats 路由、认证、响应契约和 DB 故障。
3. v9→v10 历史数据保持、失败回滚/恢复、重复 Record 与终态写入之间的中断补偿；已有 metrics 契约回归。
4. 在允许监听端口的环境重跑全部 race 测试；在可联网的发布环境保存安全扫描证据、实际镜像扫描和三平台恢复/容量演练结果。

## 总体结论与最关键 3 件事

**本提交可以作为 Phase 8 的初步实现，尚不具备按计划验收或通过公测发布门禁的条件。** 0010 建表语句及参数化 INSERT 无明显注入问题，但编译通过没有覆盖 SQL 执行、装配和发布可恢复性；19 项 stdlib 摘要也不能证明应用没有受影响的漏洞。

1. **接通并修正统计链路**：修复读错表/缺列、注入 recorder、挂载受保护契约路由，真实记录三体积和分段结果，补真实迁移 schema 与路由测试。
2. **建立可复核的安全发布结果**：保存漏洞 ID/可达性/修复版本，升级工具链重建并复扫实际产物，补 Trivy、gitleaks、secret canary 门禁，恢复被删除的监控序列。
3. **补齐生产部署与恢复证据**：区分开发 Compose 与生产配置，安全且平台感知地恢复，提交升级/灾难恢复、三平台手动演练和容量/RPO 报告，再按 Phase 8 清单验收。
