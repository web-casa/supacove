# Phase 7 第三轮复审（收敛确认）

评审日期：2026-10-05。基线：`33519e1`；修复提交及实际 HEAD：`b4c078e6b221643aba0caad188d5a4c452150602`。范围：`git diff 33519e1..b4c078e`，对照第一、二轮报告并追踪相关生产调用链。下文行号均对应本次修复提交，路径相对仓库根；历史代码另行标明。本轮只写报告，不修改生产代码。

**总体结论：尚未达到 Phase 7 可验收状态。本轮待关闭的 6 项中，FIXED 5 项、PARTIALLY_FIXED 1 项。唯一剩余阻断为 R2-P1-01（P1）：包含 `verification_failed` 的合法组合订阅经 Down→Up 后，备份失败/过期订阅仍停留在旧词汇，静默失效。** 普通双事件循环已经修复，但不足以证明整个支持词表的循环安全。此缺口沿用 R2-P1-01，不重复增加新编号；本轮未确认其他需要独立编号的新回归。

累计口径：原第一轮 12 项均可在第二轮已声明的适用边界内关闭；第二轮新增两项中，R2-P2-01 关闭，R2-P1-01 部分修复，即 **14 项累计 FIXED 13 / PARTIALLY_FIXED 1**。这不等于本轮重新执行了所有历史端到端验收。

## 一、逐项判定

| 编号 | 第三轮判定 | 证据与关闭边界 |
| --- | --- | --- |
| P1-04 | **FIXED（按 beta 范围关闭）** | `docs/dev-plan.md:218` 明确 beta 排除 start，列入 v1.0；`docs/deployment.md:36` 同步说明无 start、共享 fallback 和 fail/success 语义。此前生产 fail 链路的 FIXED 判断维持。本轮按题述范围变更评审，不再以未实现 start 阻断 beta，也不声称 start 已交付。 |
| P1-05 | **FIXED** | `backend/internal/server/api_phase7.go:140` 在 HTTP URL 校验前排除 `"-"`，`:164` 在 period 必填条件排除它；`backend/internal/jobs/heartbeat.go:58` 设置显式 disabled，`:134` 的 fail 路径尊重该值。`api/openapi.yaml:916` 改为 0 不发成功 ping、失败 ping 仍可发送（显式禁用除外）。直接调用实际 API service 的无监听探针覆盖初始仅传 `"-"`、URL+period 改为仅 `"-"`、禁用时清零 period，均返回 200 并符合预期。 |
| P2-04 | **FIXED（现有写入入口/单实例模型）** | `backend/internal/db/migrations/0014_outbox_heartbeat.sql:44` 是普通 partial index，不再重命名历史数据或强制唯一索引。含三个 `dup` 和一个 `dup #2` 的真实升级测试通过。`backend/internal/jobs/queries_schedule.go:114` 用单条 `INSERT ... SELECT ... WHERE NOT EXISTS`，`:124` 将零影响行映射为重复名称错误。32 个并发同名创建的 race 探针得到 1 次成功、31 次 `ErrWebhookNameExists`、数据库仅 1 行。 |
| P2-06 | **FIXED（错误可见性）** | `backend/internal/server/metrics.go:84` 查询 remote 失败后只标错，不输出这两个 gauge。jobs、verification、last_success、protection 的 Scan/rows.Err 均有标错（`:52`、`:108`、`:143`、`:191` 等），last_success 的 Query 错误也有标记。`:233` 的 stagingBytes 返回失败位，WalkDir/Info 错误可见。关闭 DB 的无监听采集探针确认 6 个 SQL collector 均报错、remote gauges 缺席；不存在的 staging 路径报 staging 错；健康采集仍有两个旧 jobs gauge 别名。部分采集值的限制见第四节。 |
| R2-P1-01 | **PARTIALLY_FIXED，仍为 P1** | 真实 goose 双事件 Up→Down→Up 测试通过，Down 恢复旧订阅匹配，普通 `backup_expired` 不再双重转换。但 `0014:36` 的整行 `NOT LIKE '%verification_failed%'` 使合法三事件组合在 Down 后永远跳过 Up 转换。真实 goose 探针复现，详见第二节。 |
| R2-P2-01 | **FIXED（有界整事务重试）** | `backend/internal/jobs/verify.go:412` 在实际 runVerification 路径最多调用 3 次完整事务函数；`:493` 起的事务仍将终态与 outbox 原子提交。ABORT 故障探针确认首次回滚保持 running/outbox=0，撤销故障后重试得到 failed/outbox=1；持续故障恰好 3 次失败，随后调用实际启动 sweep 可重新排队并提交。持续故障后没有运行期自动重验，边界见第三节；不因此否定本次恢复有界重试的修复。 |

## 二、剩余阻断：R2-P1-01 的混合订阅 Down→Up

定位：`backend/internal/db/migrations/0014_outbox_heartbeat.sql:31–37`，尤其 `:36`；Down 为 `:58–61`。相关消费者：`backend/internal/outbox/outbox.go:419` 起按逗号拆分并精确匹配；`:290` 的零目标路径会将通知标记 delivered。

无需构造非法 CSV 或直接写入不受支持的事件：`backend/internal/jobs/queries_schedule.go:91` 的 ValidEventTypes 本来就允许同时选择三个事件。以下是本轮真实 goose 探针的步骤与实际结果：

1. `db.Open(tempDir)` + `Store.Migrate()` 建立当前真实 schema。
2. 插入 API 可合法创建的订阅 `backup_failed,backup_expired,verification_failed`。
3. 对同一数据库创建 `goose.NewProvider(goose.DialectSQLite3, db, os.DirFS("migrations"))`，执行 `provider.Down(ctx)`，真实回退 14→13。
4. 读取订阅，再执行 `provider.Up(ctx)` 并读取订阅。

```text
原始：backup_failed,backup_expired,verification_failed
Down：failure,expired,verification_failed
再Up：failure,expired,verification_failed
期望：backup_failed,backup_expired,verification_failed
```

`TestR3MixedVocabularyCycle` 按“再 Up 应等于原始值”断言，**实际失败**。同次运行中的仓库既有 `TestWebhookVocabularyDownUpCycle`、`TestLegacyDuplicateWebhookNamesSurvive` 均通过。

原因是 guard 作用于整行，只要保留了 `verification_failed`，所有旧备份事件都不转换。因此混合订阅回升后，`backup_failed` 和 `backup_expired` 都不匹配此接收端；`verification_failed` 仍匹配。若该备份事件没有其他匹配接收端，outbox 将以零目标 delivered 结束，失败/过期告警静默遗漏。

这属于 R2-P1-01 未覆盖完的循环兼容性，不再单列 R3 编号。只订阅新备份词、只订阅 verification、旧默认双事件三类都不足以触发它；必须补含 verification 与备份事件组合的测试。

**Down 的反向 REPLACE 本身没有本轮所疑的再命中冲突。** `backup_failed→failure` 的结果不含后续匹配串 `backup_expired`；`backup_expired→expired` 也不会生成任何反向匹配的新词。`verification_failed` 不含 `backup_failed`，会保留。对当前合法词表，转换顺序安全；问题发生在之后的 Up 整行排除，而非 Down 二次命中。

现有 `backend/internal/db/upgrade_test.go:349` 的测试确实使用真实旧库、真实迁移和 `provider.Down/Up`，不是单纯执行替代 SQL；`:411` 的断言也证明旧 `failure,expired` 恢复。结合历史 `3161998:backend/internal/scheduler/scheduler.go:341` 发出 `failure`、`:381` 用 `strings.Contains(wh.Events, event)` 匹配，足以确认该 fixture 的旧词表匹配恢复。**没有运行旧二进制的网络投递集成测试，也不能由这个双事件 fixture 推导全部合法订阅组合均兼容。**

关闭要求：对每个 CSV token 做独立、精确、幂等的双向转换，保留无对应旧事件的 verification token；不要用“整行已含任意新词则整行跳过”代替 token 转换。补全三个合法事件的所有非空组合（至少覆盖三事件组合、backup_failed+verification_failed、backup_expired+verification_failed）的真实 Down→Up，断言完整订阅恢复；旧词和新词混合输入也应按 token 归一。建议同时断言实际 targets 匹配结果，避免只验证 SQL 执行不报错。

## 三、并发唯一性、禁用配置和验证重试

### API 唯一性

CreateWebhook 的检查和写入是同一条 SQLite 写语句，不是应用先 SELECT 再 INSERT 的两个时间窗口。SQLite 写入串行化，加上生产 `backend/internal/db/db.go:95` 的进程锁、`:112` 的 busy_timeout、`:125` 的 4 连接池，符合当前单实例语义。探针使用真实生产 Store 的 4 连接池和同步起跑的 32 个 goroutine，返回 1 成功/31 冲突，无 SQLITE_BUSY 或 race 报告。

旧同名行仍能按 id 管理，targets 也不会按 name 去重。`TestLegacyDuplicateWebhookNamesSurvive` 证明升级不因 `dup #2` 碰撞而失败、总行数为 4；它只断言 COUNT，未逐字段断言 name/URL/deleted_at 不变。“不重命名、不删除”的进一步证据来自迁移源码确实只 UPDATE events，没有再写 name 或删除行。后续可强化 fixture 断言，当前不另列阻断。

这不是数据库对任意直接 SQL 写入者的唯一性保证；新增写入入口必须复用该创建逻辑。现有 API 入口的并发检查足够，不要求为了名称重新引入会阻断历史数据的 unique index。

### `"-"` 与 period 的合并语义

无监听探针直接调用 `apiService.PutDatabaseSchedule`，按顺序得到：

| 修改前 | 请求 | 修改后 / 结果 |
| --- | --- | --- |
| 空 URL、period=0 | 仅 heartbeatUrl=`-` | `-`、period=0，200 |
| 上一步 | URL + period=24 | 真实 URL、period=24，200 |
| 真实 URL、period=24 | 仅 heartbeatUrl=`-` | `-`、period=24，200 |
| `-`、period=24 | `-` + period=0 | `-`、period=0，200 |

保留原 period 是现有“先读配置、只覆盖非 nil 字段”的语义；disabled 在年龄计算和 fallback 之前返回，保留 period 不会重新打开成功或失败心跳。显式传入负数/超范围 period 仍按正常字段校验报错，短路仅针对 URL 校验和 period 必填要求。回头只改为真实 URL 时，保留的正 period 可继续使用；若已清零则须同时提供正 period。

仓库新增 `server_phase7_test.go:76` 实际传了 period=24，单靠它不能证明“无需 period”；上表补足了该边界。本轮探针是 service 级验证，不冒充完整鉴权 HTTP 集成测试。

### 验证失败的原子重试与观测

用 `BEFORE INSERT ON notification_outbox ... RAISE(ABORT, ...)` 注入错误，经真实 `runVerification` 执行失败结果落库：

- 短暂故障：首个 enqueue 错误日志出现后，查询得到 verify_status=running、outbox=0；撤销 trigger，调用方的下一次整事务重试提交 failed 与唯一 outbox 记录。
- 持续故障：收到恰好 3 次 enqueue 错误日志，runVerification 返回后仍为 running。撤销 trigger 后调用 `ResumePendingVerifications`，实际从队列取回该任务，再执行验证可提交终态。

启动 sweep 的 SQL 在 `backend/internal/jobs/verify.go:163` 明确包含 `status='succeeded' AND verify_status IN ('pending','running')`，生产 `backend/internal/jobs/jobs.go:435` 会调用它。启用 verifier 且有 identity 时重新排队；缺少 verifier/identity 则收敛为带理由的 skipped，不能声称一定保留上一次失败结论。

运行期可通过每次事务失败的日志、任务 verify_status，以及 `supabackup_verification_total{status="running"}` 观察该过渡状态。但 running 不区分“仍在验证”和“终态提交已耗尽重试”，目前没有专门的重试耗尽指标。

**scheduler 补偿扫描并不处理 running。** `backend/internal/scheduler/scheduler.go:379` 只扫 failed/unsupported；它负责已提交失败状态的通知补偿，不能把丢失的验证结果重新写入。三次都失败后仍依赖下一次启动重新验证或人工恢复。这是本次有界重试的明确边界，不是“持续失败也能在运行期自动收敛”的证明。

`verify.go:417` 仍在最后一次失败后多睡 600ms，整个失败序列 sleep 合计 1.2s，且不响应 ctx；这不同于本次优化过的 outbox execRetry。与旧通用 finishVerification 重试策略一致，属于有界退出延迟建议，不据此继续阻断 R2-P2-01。数据库锁等待时间另计。

## 四、metrics 与附带修复核查

- `stagingBytes()` 全仓只有 `metrics.go:161` 一个调用点，已按 `(int64, bool)` 接收；build/vet 通过。WalkDir 回调先处理 err，再访问 DirEntry，不会因读取失败的 nil entry 直接 panic。
- `failed := func(collector string)` 捕获的是本次请求的局部 map。jobs 代码块内后声明的 `var succeeded, failed int64` 只在内部作用域遮蔽名称；该声明前的标错调用和块外的 collector 调用仍是闭包。未引入共享 map 或采集并发写入。
- 健康响应仍有 `supabackup_jobs_succeeded` 和 `supabackup_jobs_failed`。jobs Query 整体失败时不输出这两个值；remote QueryRow/Scan 失败时不输出两个 remote gauge，错误信号存在。
- jobs/verification/last_success/protection 的 Scan/rows.Err 都显式调用 failed。此类游标错误主要做静态核对；本轮没有逐项动态注入所有 driver Scan/rows.Err 故障，不能将关闭 DB 的 Query 故障探针说成全覆盖。
- staging 失败时仍输出尽力累计值（可能为 0），部分游标失败也可能输出已读的部分分布/别名，并附 collector error=1。因此本项关闭含义是“失败可见且 remote 不再假报零”；不是所有 collector 都具有失败即省略全部样本的语义。告警应结合 scrape_errors 判断数据是否完整，后续可统一失败时省略样本。
- `backend/internal/outbox/outbox.go:393` 去掉最后一次失败后的 sleep，`:396` 的等待响应 ctx。无监听探针测得 3 次失败耗时约 **605ms**，符合 200+400ms；已取消 ctx 立即返回并将取消错误交给 onFail。
- `outbox.go:212` 启动恢复将 state 改 pending 且 next_attempt_at 改 now。把原租约设为未来 15 分钟的探针确认恢复后立即到期。这里“立即”指无需等原租约，实际 HTTP 重放仍由 Start 的下一次 ticker pass 驱动，不是在 recoverStuck 内同步发送。
- 本轮定向 race 检查未报告新 data race。未改动的 detached heartbeat shutdown 生命周期、单 notifier 租约边界继续沿用第二轮限制，不扩大为已解决。
- 迁移文件头部 `0014:5` 仍声称重命名旧同名数据后建 unique index，`:28` 声称 TOKEN-EXACT 也不符合整行 guard 实现。建议随 R2-P1-01 修复更新注释，避免维护者依赖错误保证。

## 五、验证记录、迁移前提与验收要求

所有 Go 命令使用 `GOCACHE=/tmp/codex-phase7r3-cache`。

| 验证 | 结果 |
| --- | --- |
| `go test ./backend/internal/db -count=1`（加入临时探针前） | PASS，现有整个 db 包测试通过。 |
| `go test ./backend/internal/db -run 'TestR3MixedVocabularyCycle\|TestWebhookVocabularyDownUpCycle\|TestLegacyDuplicateWebhookNamesSurvive' -v -count=1` | 新三事件循环断言 FAIL；两个既有测试 PASS。这是本轮确认的产品缺口。 |
| `go test -race ./backend/internal/jobs ./backend/internal/server -run 'TestR3\|TestOverviewStates\|TestScheduleConfigRoundTrip\|TestVerifyResumeRunsAtStartup\|TestNoVerifierSweepSettlesStaleStates' -v -count=1` | PASS；包括并发创建、瞬时/持续验证写入故障、running sweep、无监听 schedule/metrics 探针。 |
| `go test -race ./backend/internal/outbox -run 'TestR3\|TestEnqueueDedup\|TestNoTargetsIsSuccess\|TestLinkLocalDenied' -v -count=1` | PASS；包括租约重置及重试取消/延迟探针。 |
| `go build ./...` | 退出码 0；期间出现 Go 向只读 module stat cache 写入失败的提示，最终构建成功。 |
| `go vet ./...` | PASS，退出码 0。 |
| `git diff --check` | PASS。 |

临时探针已从仓库移除，副本留在本地 `/tmp/codex-phase7r3-probes/`（db/jobs/server/outbox 各一份）；仓库交付只有本报告。没有执行需要监听端口的完整 server/outbox HTTP 测试，也没有执行外部死人开关实测或旧二进制网络投递演练。既有 Phase 7 DoD 中的外部关机告警等证据不能由这些无监听测试替代。

0014 原地编辑继续沿用第二轮条件：只有在它尚未发布、没有需要保留的已应用旧版 0014 数据库时，这种修复形式才足够。`backend/internal/db/migrate.go:36` 起的无 pending 快返不会重跑版本相同但内容变化的迁移。如果已有环境应用先前 0014，则它可能保留旧事件词或旧 unique index，必须另加前向修复迁移及对应升级测试；本轮没有证据证明此类环境不存在，也不重复按新编号计数。

达到本轮收敛要求仍需：修复 R2-P1-01 的逐 token 转换，加入包含 verification 的合法组合循环回归，并确认上述迁移发布前提。其余 5 项不要求重新设计。此后结合既有 Phase 7 DoD 的完整环境验收证据，再判定 Phase 7 可验收，当前不放行。
