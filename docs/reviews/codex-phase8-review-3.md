# Phase 8 第三轮复审（收敛确认）— f5ae7ca

日期：2026-10-05。评审范围：`git diff 076ba19..f5ae7ca`；实际 HEAD：`f5ae7ca91e84d1cc2d9002246cbe7971b13565c5`。编号沿用第一轮报告的 P1×5、P2×4，并单独复核第二轮新增项。所有 Go 验证使用 `GOCACHE=/tmp/codex-phase8r3-cache`。本轮只写报告，不修业务代码。

## 总体结论

**尚不通过 Phase 8 后端完整验收 / 公测发布门禁。两个确定性 500 已关闭，不能继续把统计接口判为 REGRESSED。**

九个原始正式编号：**FIXED 3、PARTIALLY_FIXED 6、NOT_FIXED 0、REGRESSED 0**。第二轮 R2-P1-01、R2-P2-01、R2-P2-02 均关闭；附加 P2-06 保持关闭。本轮新增 **R3-P2-01：archive 累计值的覆盖范围未声明**，归入 P1-03 余项，不重复计入九项数量。没有证据支持新增 P0/P1 运行时回归。

实现已有明显收敛：真实迁移 schema 上 GetStats 返回 200；Unicode 字节偏移、转义空格和空格 authority 边界已修；canary 的 webhook 空管线防护真正提交，Phase 6 断言密码也已接到实际凭据；DR 主要操作错误已修。不过导出分段仍等于总体成功率，通知分段仍缺失，archive/耗时仍有诚实性问题。四出口的真实失败链与外部运行证据也不能由现有绿灯替代。

## 逐项判定

| 编号 | 本轮判定 | 证据与剩余边界 |
| --- | --- | --- |
| P1-01 | PARTIALLY_FIXED | `pgclient/inspect.go:183,197,236,297` 修复等长 ASCII 折叠和转义值；新增四条表项实跑通过。生产 Secrets→Sanitize 顺序正确。但 `:172` 仍声称任意顺序安全，`sanitize_test.go:30` 后没有调用 Secrets，仍只测幂等；authority 只认空格/tab，不认换行。具体泄漏复现已修，不把注释余项等同于仍有原 Unicode P1 泄漏。 |
| P1-02 | PARTIALLY_FIXED | `jobs/phase8_canary_test.go:72` 起的真实订阅、notifier、delivered、非空收包及读盘检查已提交；`phase6_e2e_test.go:73` 解析实际密码并校验 `testPW`，设置错误关闭。但 Phase 8 仍直接调用 fail、以无凭据的合成输入生成工件，尚未补真实失败/验证失败四出口联动；Phase 6 真实 E2E 本轮 SKIP，不能宣称已运行通过。 |
| P1-03 | PARTIALLY_FIXED（解除 REGRESSED） | `server/api_phase7.go:399–444` 两个 SQL 错已修；真实迁移探针确认空库、有数据均为 200。三体积可返回；导出分段、通知分段仍不完整；新增 archive 覆盖口径见 R3-P2-01。 |
| P1-04 | PARTIALLY_FIXED | `docs/capacity.md:10,12,49` 已声明 Linux VM、区分两种压缩比分母、限定 dump-only 外推。表内 MB/MiB、客户端执行位置仍不一致；脚本不可压/端到端注释未同步；产品 upload/verify、整体资源峰值尚无测量证据。 |
| P1-05 | PARTIALLY_FIXED | security 的全历史 checkout 已修；扫描配置不等于扫描结果。Trivy、绑定产物的安全报告、三平台桶下载恢复记录及发布门禁仍待交付。 |
| P2-01 | PARTIALLY_FIXED | 接口恢复后成功样本实际返回 10 秒；成功路径双写同一耗时值保持正确。空库仍为 0 而非 unknown；契约仍称 dump duration，而计时包含上传。 |
| P2-02 | FIXED（保持） | `server/api_phase7.go:473` 仍为 GROUP BY 后主键回连；本 diff 未退回相关子查询，无平方成本回归证据。 |
| P2-03 | FIXED | `docs/disaster-recovery.md:32,59,73,91` 覆盖无法找回原密钥的情况 1、损坏文件处理、`set -eu`、维护库连接和 `backups/` 路径；错误哈希停止探针通过。关闭文档操作缺陷，不宣称三平台真实演练完成。 |
| P2-04 | FIXED（保持） | `jobs/remediation.go` 本 diff 未改；上轮关闭的 session/transaction、版本、storage/retention、dump/verify 文案区分保持。 |
| R2-P1-01 | FIXED | 缺列 SUM 已移至真实存在的 backup_stats；5 个 SELECT 表达式对应 5 个 Scan 目标；见迁移探针结果。 |
| R2-P2-01 | FIXED | `.github/workflows/ci.yml:46` 为 `fetch-depth: 0`，且位于 security job 的 checkout。关闭浅克隆历史缺失，不等于 gitleaks 已扫描 clean。 |
| R2-P2-02 | FIXED | `createdb --maintenance-db=... restored` 与随后 `TARGET` 的 host/user/port/db 一致，不再把 URI 当库名。 |
| P2-06（附加） | FIXED（保持） | GetStats 各查询继续检查错误；新增 archive 查询亦返回明确 500，不吞 SQL 错。 |
| 杂散文件 | NOT_FIXED（清理项） | 实际 Git 路径是 `backend/ebhook\|---.\|cat\|`，111 bytes，仍被跟踪且存在。上轮展示末尾的 `:1` 是行号，不是文件名的一部分；内容仍是调试 shell 命令残片。未发现业务调用，不升级为安全漏洞。 |

表中 backend 路径均相对于 `backend/internal/`；范围定位以下文具体说明为准。

## GetStats：关闭两个 500，但不能把单成功样本当作统计语义全覆盖

`newTestEnv` → `newTestEnvWithConfig` 使用 `db.Open` 和 `store.Migrate`（`server_test.go:53–65`），所以提交的回归测试确实使用生产迁移 schema，不是手写缩水 schema。空库请求必须 200，已经足以捕获这两个与数据内容无关的 SQL 错误；有数据分支又检查了三体积和三项分段率。

本环境禁止 socket 监听。单独运行 `TestPhase8GetStatsRegression` 在 `httptest.NewServer` 失败，尚未发出请求；不能记为该 HTTP 测试通过。为验证 SQL，本轮临时编写同包、无监听探针，用真实 `db.Open` + 全部 `Migrate`，直接调用生产 `apiService.GetStats`，要求具体类型为 `api.GetStats200JSONResponse`：

| 数据状态 | 实际结果 |
| --- | --- |
| 空库 | 200；三体积为 0；overall/export/remote/verify 均 null；avgDurationSecs 为 0 |
| 成功任务、无 stats 行 | 200；source=873、ciphertext=405、archive=0；avg=10；三项分段率=100 |
| 同一任务补 stats（dump=300） | 200；source=873、ciphertext=405、archive=300；avg=10；三项分段率=100 |
| 再加已提交本地工件、上传失败任务 | 200；export=50、remote=50；构造 verify=failed 后 verify=50 |
| 再加 succeeded / verify=unsupported | 200；verify≈33.333，确认 unsupported 被计入 vBad |

后两行是状态组合 SQL 探针，不宣称真的执行了上传/验证；其中失败任务上的 verify=failed 专门验证 SQL 没有契约所称的 succeeded 限制。

提交测试仍有覆盖缺口（`server_phase8_test.go:24,28,35,67`）：

- `&& false` 分支永不执行，空库只断言 200，没有断言 null 分段率。
- 注释说会插入 failed upload 和 failed job，实际只插入一个 succeeded/verified/committed 任务，所有分段率都是 100，无法发现错误分母。
- AVG 只排除序列化文本 `"avgDurationSecs":0`，字段缺失/null/错误非零值都可能通过，应直接检查数值 10；Scan 错误也不应忽略。

这些缺口不否定它对两个 500 的有效回归保护。应补的是混合状态和 unknown 覆盖，不是重做迁移测试。

### R3-P2-01：新增 archive 查询把不完整快照之和公开为全部成功备份之和

定位：`backend/internal/server/api_phase7.go:417–419`；相关写入 `jobs/jobs.go:934–950`，公开契约 `api/openapi.yaml:1149–1152`，展示 `frontend/src/App.tsx:235`。

新查询对 `backup_stats WHERE dump_size > 0` 求和；而 ciphertext 和任务成功数来自 jobs。stats.Record 在成功 UPDATE、恢复包生成之后独立 best-effort 写入，失败只记日志；旧成功任务也不会自动补齐 stats。因此成功任务无 stats 是正常历史/故障状态，不能当作不可能输入。探针中一个成功任务返回 `totalArtifactBytes=405`、`totalDumpBytes=0`，证明该值的覆盖范围不同。

**读取 backup_stats 本身可接受；当前未声明的“全部成功 archive 总量”不可接受。** OpenAPI 仍写 “over succeeded backups”，前端仍标 “Dump archive total”，没有样本覆盖数量、unknown 标识或“仅已记录样本”的说明；代码里“数据在哪张表”的注释不等于公开口径声明。已有部分样本时展示的是可信外观的非零小计，更容易被用于错误容量比较。历史上按 ciphertext 写入的 dump_size 也没有来源版本/修正机制，不能推断所有旧记录均为精确 archive 值。

建议优先把真实 archive 大小随成功状态可靠持久化，并定义历史未知；或明确字段仅为已测量样本小计，暴露覆盖情况、避免与全量 ciphertext 直接比较。新查询另无 job 状态过滤，schema 对 job_id 也无 UNIQUE；当前单次成功写入流程没有证明会重复记账，故不把潜在重复/孤立状态另报成已发生缺陷。

### 仍未关闭的 P1-03 / P2-01

- **导出率**：`:437` 仍只数 succeeded，`:498` 分母为全部终态任务。生产 `jobs.go:845–850` 上传失败发生在 dump 完成之后，仍使 export 失败。通知分段未添加。以上是上轮遗留，不是本轮新引入。
- **平均耗时**：空集合 COALESCE 为 0，输出非 nil，前端显示 `0.0s`；应保留 unknown。`jobs.go:861` 在远端上传后才停止计时，契约 “Measured dump durations” 仍不准确。
- **验证口径**：SQL 只排除 pending/running jobs，不限定 succeeded，契约注释却限定 succeeded；对非标准/历史状态组合应统一定义。failed/unsupported 合并 Scan 本身正确。

## 脱敏：新增表项有效，顺序承诺仍需清理

pgclient 全包及 race 均通过。四个新增表项分别覆盖 keyword 的 İ、URI 的 İ、authority 后空格诊断词、bare 值反斜杠空格，均真实调用生产 SanitizeMessage 并比较完整预期值；不是空断言。

额外无监听探针输出：

| 输入/调用 | 输出 |
| --- | --- |
| `İ password=secret` | `İ password=[REDACTED]` |
| `İ postgres://u:secret@host/db` | `İ postgres-uri://[REDACTED]/db` |
| `password=alpha\ beta host=x` | `password=[REDACTED] host=x` |
| `postgresuri postgres://u:secret@host/db` | `postgresuri postgres-uri://[REDACTED]/db` |
| `postgresuri://u:secret@host/db` | 原样保留 |
| `postgres://u:p@host\nerror reading /tmp/x`（真实换行） | `postgres-uri://[REDACTED] reading /tmp/x` |
| Secrets→Sanitize，已知 secret=`alpha,beta` | `password=[REDACTED] host=x` |
| Sanitize→Secrets，同一 secret | `password=[REDACTED],beta host=x` |

`redactURIUserinfo` 找到非 URI 的 postgres 前缀后会写回并推进 8 bytes，继续寻找后面的合法 scheme；不会被前面的 `postgresuri` 卡住或漏掉后续 URI。`postgresuri://` 不是支持的 PostgreSQL scheme，保留不作为新的合法 URI 漏敏问题；现有 marker `postgres-uri://` 同样不被重复改写。

字节等长 ASCII 折叠解决了偏移错位，没有观察到新的 panic。剩余 authority 边界仅认 space/tab，换行后的 error 被吞掉是残余诊断损失，不能声称“所有空白”已经处理。生产关键调用方 `api_phase2.go:123`、`jobs.go:643` 顺序正确；但 inspect.go 注释仍同时写任意顺序安全和必须 AFTER，测试也没有调用 redact.Secrets。删除错误承诺并添加真正组合测试即可，不要求支持没有业务需求的反向组合。

## Canary：设置错误关闭，真实管线覆盖仍须区分

### Phase 8

重写确实存在于目标提交，改善可以认可：

- 注册 `backup_failed` webhook，启动真实 outbox worker；要求 entry 非空且 delivered。
- 接收端 `io.ReadAll(io.LimitReader(..., 1<<20))`，本例小 JSON 可完整读取；要求至少一个非空 body，检查所有收到的 body；数据库 payload 要求非零行且检查 Scan/rows.Err。
- kit/manifest 实际调用生成器、落盘、读取、非空断言；日志要求出现 `backup failed`。原先“文件不存在/零收包也通过”已经修复。

不过 `phase8_canary_test.go:92` 仍直接 `r.fail` 注入密码，没有 runJob/真实连接失败/验证失败。`:113` 的 manifest 只给 BackupID/Database.Name，`:135` 的 KitInput 也是固定安全值，均与已注册数据库的凭据没有数据流关联；它们是生成器 smoke test，无法发现生产调用方把 conninfo 填入工件的问题。GetTask 仍不是 HTTP API 序列化出口。

收包未解析 event/job_id，也未与 delivered entry 关联，建议补预期事件标识检查；当前隔离 fixture 的真实订阅加 delivered 断言已经比单独“有 heartbeat 请求”强，不再沿用上轮“完全没有 outbox 投递”的错误结论。

本轮单独运行此测试也因 httptest 监听受限而失败，真实 delivery 与并发清理路径不能记为已执行通过。

### Phase 6

`m1_test.go:42,64,92` 用同一个 `testPW` 设置容器角色密码和 URI。`phase6_e2e_test.go:73–80` 解析该 URI，要求非空且等于 testPW 后才注册到应用。独立使用同形 URI 调用生产 ParseURI，实际密码为 `CANARY-integration-P@ssw0rd`，包含 `@` 也能正确解析；上轮“断言不存在于管线的密码”的错误已关闭。

kit 的泄漏断言位于成功 ReadFile 和真实密文哈希断言之后（`:208–216`）；manifest 的泄漏断言也在 ReadFile 错误检查之后（`:218–224`），位置有效。manifest 本处仍没有非空/解析/BackupID 对应断言，但此前等待真实 verified 提供额外管线证据，不能等同于 Phase 8 合成工件。

真实 E2E 在 `startTestPostgres` 因 docker run 不可用而 SKIP，尚未执行解析门禁及后续恢复；独立解析探针只关闭设置问题，不替代容器、恢复包执行与工件检查的完整证据。继续补真实失败链及在可用环境运行这两类测试，才可整体关闭 P1-02。

## 容量、DR 与 CI

容量新增的 dump-only 上界限定正确；0.57 与 0.464 的不同分母已明确；Linux VM 执行声明比上轮清楚。但 `capacity.md:22,23,34–35,52` 仍把由 `du -m` 得到的 405 标成 MB，仅将 24 改成 MiB/s，恢复吞吐又写 65 MB/s。873 的原始 bytes/二进制 pretty 输出转换没有证据，跨单位直接写 405/873≈0.464 也不能成为精确实测比。`:14` 仍写“宿主机 pg_dump”，与 Apple Silicon 宿主机/Linux VM 的区分不清。应统一以原始 bytes 换算、明确客户端在 VM 中执行，并保存原始输出。

`scripts/capacity-benchmark.sh:9,29–30,73` 仍称 hex 不可压、恢复阶段端到端，与文档限定不一致；RPO “实际上限/最坏”需限定健康稳态，故障时无固定上限。产品 upload/read-back/真实 verify、进程组 RSS 与磁盘峰值仍未测量。本轮未重跑容量测试，不替旧数字背书。

DR 手动流程 Bash 语法通过；用临时密文及错误 SHA 执行校验段，非零退出且没有到达后续步骤。createdb 的维护库参数与官方接口一致，关闭上轮连错库问题。[PostgreSQL createdb 文档](https://www.postgresql.org/docs/current/app-createdb.html)。这不是实际数据库恢复试验。

security job 的 YAML 和全部 run 块 `bash -n` 通过，解析确认 fetch-depth 为整数 0；checkout 会获取全部历史，满足 gitleaks 的 PR commit-range 获取需要。[checkout v4](https://github.com/actions/checkout/tree/v4)、[gitleaks v2 实现](https://raw.githubusercontent.com/gitleaks/gitleaks-action/v2/src/gitleaks.js)。govulncheck 无 continue-on-error，失败会阻断该 job 并跳过默认条件下的后续扫描；仍使用浮动 `@latest`。`image.needs` 仍不含 security，仓库外 required checks/发布流程是否强制执行未知。没有实际 Actions run，不能报扫描 clean 或发布门禁已生效。

## 验证记录

| 验证 | 结果 |
| --- | --- |
| pgclient / redact / db 全包普通测试 | PASS |
| pgclient / redact / db 全包 race | PASS，含临时脱敏探针 |
| 无监听 GetStats + 全量真实迁移 | 普通测试与 race 均 PASS；空库/成功/无 stats/混合状态结果见上 |
| jobs 选定 race | PASS：VerifyResumeRunsAtStartup、StopCancelsAndRequeues、NoVerifierMeansExplicitSkip、VerifyDetailRedacted、ManifestMissingIsHonestSkip |
| server / jobs 全包 | 环境受限：httptest socket operation not permitted；不记 PASS |
| 单独 TestPhase8GetStatsRegression / TestPhase8SecretCanary | 同样监听受限，未完成测试主体 |
| TestPhase6RealVerificationEndToEnd | Docker run 不可用而 SKIP；exit 0 不等于 E2E 通过 |
| 实际形状 URI 的 ParseURI | PASS，Password 等于 testPW |
| `make api-check` | PASS，后端/前端生成契约无漂移 |
| `go vet ./backend/...` | PASS |
| CI YAML、fetch-depth、所有 run 块 Bash 语法 | PASS |
| DR Bash 语法、错误哈希停止 | PASS；没有连接真实恢复目标 |
| govulncheck / gitleaks / Trivy / 容量 / 三平台恢复 | 未执行，不提供扫描或恢复通过结论 |

有限 race 用例未报告新 data race；新 webhook 收包与日志缓冲均有 mutex，未发现明确的新增共享数据竞态，但未运行 delivery race，不能扩展成全链无竞态结论。未修改前端业务代码，未重复前端构建。临时探针在核查后删除，仅保留此报告。

## 剩余验收清单

仓库内先收敛：导出/通知分段与权威状态、archive 已知样本覆盖及历史口径、unknown 耗时、脱敏顺序注释和真实组合测试、真实失败链 canary、容量单位与脚本注释、杂散文件清理。这些不是等待外部凭据就会自动解决的问题。

外部证据需绑定最终提交/产物（后续修复提交应重新验证）：

1. 可监听、可运行 Docker/PG/age 的环境中，两个 Phase 8 测试、Phase 6 实际恢复 E2E 和完整后端 race 的非 SKIP 结果；包含预期 webhook event、工件和恢复断言。
2. govulncheck、gitleaks、Trivy 的工具版本、扫描范围、完整结果摘要、commit SHA/镜像 digest；以及失败确实阻断发布的 Actions/required-checks 配置证据。
3. Supabase、Neon、Railway 各一次备份→桶下载→新目标按恢复包手动恢复的脱敏记录，含 artifact ID、profile、工具版本、fixture 数据断言。自动验证不替代手工恢复门禁。
4. 固定机器和 fixture 的原始容量输出：产品 dump/upload/read-back/verify 分段时间、整体 RSS/磁盘峰值、对象存储与网络条件、调度总负载边界；明确 bytes/MB/MiB 与故障下 RPO 无固定上限。

本轮可验收的是具体修复子项，不能以它们替代 Phase 8 完整后端及发布条件。
