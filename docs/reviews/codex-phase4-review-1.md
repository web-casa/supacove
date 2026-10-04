# Phase 4 实现评审：调度、并发治理与最小告警

评审日期：2026-10-04。基线：`1d9badb`；评审 HEAD：`741c964`。范围内确有两个提交：`1685e03`（功能实现）、`741c964`（gofmt），共修改 9 个文件。

依据：`docs/dev-plan.md:159–173` 的 Phase 4 任务与 DoD，以及协议 D 的保留保护要求。**结论：未通过 Phase 4 验收。** 未确认 P0；存在调度漏备、告警不可用、认证缺失等 P1 问题。

范围说明：`backend/internal/config/config.go`、`backend/internal/jobs/upload.go` 及 `jobs.go` 在本次提交区间没有差异。其配置校验、保留函数及 `runJob` 接线已经存在于基线；下文将这些路径中的问题明确标为“既有问题”或“Phase 4 未落实的要求”，不归因为本次新引入的回归。仅新增本报告，生产代码和仓库测试未修改。

## P0

未确认无须特殊前提即可造成大范围数据丢失或凭据失陷的 P0。此结论不表示满足发布条件；下列 P1 足以阻止 Phase 4 验收。

## P1

### P1-01：先推进调度游标再入队，失败和同库冲突都会消耗计划时刻

- **位置：** `backend/internal/scheduler/scheduler.go:134–141,179–186`；`backend/internal/jobs/jobs.go:238–261`；迁移 `0009_scheduling.sql:3–9`。
- **问题：** `Scheduler.enqueue` 先独立提交 `last_scheduled_at=now`，然后调用 `Runner.Enqueue`。两步之间崩溃、INSERT 失败或 `ErrAlreadyQueued` 都不会回滚游标。例：昨日开始的长任务仍在运行，今日 00:00 的调度冲突后游标被推进；旧任务结束时不会补上今日备份，只能等下一周期。冲突还只记 Debug，不能表达“当前新鲜度已不足、最近一次计划等待补跑”。
- **证据：** `TestReviewEnqueueAdvancesCursorOnConflict` 复现冲突后今日计划被消耗；`TestReviewEnqueueAdvancesCursorOnInsertFailure` 用 SQLite trigger 注入 INSERT 失败，确认 jobs 为 0 而游标已更新。都是生产函数和真实 SQLite 驱动上的验证。
- **计划差距：** 当前部分唯一索引只能保证“每库至多一个 pending/running”，不能替代 `(database_id, schedule_revision, scheduled_at_utc)` 的计划去重。无 `next_due`/revision/补跑标记；`jobs.scheduled_at` 写的是实际入队时间。跨多周期时确实不会逐个无限补跑，但也没有找出、记录并标注“最近一次错过的计划时刻”。默认游标 0 还会把新建计划追溯到 1970 年：10 月首次配置 12 月 1 日的年计划，首次轮询立即入队。
- **修复：** 在一个 SQLite 事务内核对计划版本、暂停/删除状态，生成或合并最近一次待执行计划，再推进游标；成功提交后唤醒 worker。冲突应保留/合并最新 due 意图，数据库故障必须回滚。持久化计划身份、真实 UTC 计划时间和补跑原因，定义首次启用边界；用 `errors.Is(err, jobs.ErrAlreadyQueued)` 识别冲突。

### P1-02：调度配置没有写入入口，Webhook 表没有接入运行时

- **位置：** `backend/cmd/supabackup/main.go:224–227`；`backend/internal/scheduler/scheduler.go:59–70,215–222,298–306`；迁移 `0009_scheduling.sql:3–18`；`backend/internal/jobs/queries.go:118`；`backend/internal/server/api_phase2.go:132`。
- **问题：** 主程序只 `New/Start`，从未调用 `SetWebhooks`；仓库没有查询 `webhooks` 表的生产代码。即使管理员直接插入 webhook 行，运行实例也不会加载它。cron、时区、最大年龄和暂停字段也没有 API/CLI/配置入口，现有数据库创建/读取契约不包含这些字段。所有新老记录默认 `cron_expr=''`、`max_age_hours=0`，正常操作路径不会启用任何自动备份或超期检查。
- **影响：** 目前交付的是数据库字段和内部函数，尚未形成“部署后持续正确工作”的功能闭环。不能把 `SetWebhooks` 的存在等同于通知已可用。
- **修复：** 增加受认证保护且经过校验的持久配置入口，接入启动加载与配置变更；明确删除/禁用行为。至少验证“配置 → 重启 → 到期入队/发送”的真实集成路径，不能只单测发送函数。

### P1-03：失败告警的 SQLite 时间表达式错误，永远筛不出任务

- **位置：** `backend/internal/scheduler/scheduler.go:250–256`。
- **问题：** `strftime('%s','now','-300')` 使用了无效修饰符，返回 SQL `NULL`。`finished_at > NULL` 不为真，所以新近失败任务也不会发送告警。这与 P1-02 相互独立：补上 Webhook 接线仍无法收到失败通知。
- **证据：** `TestReviewFailureWindowIsNull` 在生产使用的 modernc SQLite 上插入一条刚失败的任务，原查询结果为 0，改成 `'-300 seconds'` 的对照查询为 1。
- **修复：** 使用带单位的修饰符或参数化整数 cutoff，显式处理查询、Scan、`rows.Err()` 错误。窗口查询也需要明确消费语义：当前每 30 秒重扫最近 5 分钟的最新 10 条，修正表达式后将重复发送，窗口内第 11 条及更老记录可能一直被排除，停机跨窗则丢失。完整事务 outbox 属于 P7；P4 至少应有可解释的扫描游标/去重策略，避免固定 LIMIT 永久漏掉一部分失败事件。

### P1-04：`/metrics` 完全绕过认证，注释与实际路由相反

- **位置：** `backend/internal/server/server.go:203–213,291–297`。
- **问题：** `s.guard` 只挂在 `/api` 的路由组，`r.Get("/metrics", ...)` 在组外。更易遗漏的是：`guard` 本身也会放行不以 `/api` 开头的路径，因此简单改为 `r.With(s.guard).Get("/metrics", ...)` 仍不够。
- **证据：** `TestReviewMetricsAnonymous` 用完整 Router 和 `httptest.NewRecorder`，无 Cookie、无效 Cookie 两种请求均返回 200 和指标正文，不依赖真实端口。
- **影响：** 与 Phase 4“默认受保护”直接冲突。当前内容仅是进程指标，不能夸大为备份凭据泄漏；但不能依靠部署者额外配置反代来替代默认认证。
- **修复：** 抽取不依赖 `/api` 前缀的认证中间件或明确的监控凭据机制，直接保护该路由。覆盖匿名、无效/过期/撤销凭据拒绝及有效凭据成功的完整 Router 测试。

### P1-05：异常 cron 能使进程崩溃，无法满足的日期被当作持续到期

- **位置：** `backend/internal/scheduler/scheduler.go:75–86,152–175`；`go.mod` 中 `robfig/cron/v3 v3.0.1`。
- **问题一：** 当前固定版本的 `ParseStandard("CRON_TZ=UTC")` 会在提取缺少空格的时区前缀时切片越界。`isDue` 没有先验证表达式结构，调度 goroutine 没有故障隔离，一条这样的持久配置会触发未恢复 panic，终止进程。现阶段配置只能绕过应用入口写入数据库；以后接入配置时此路径会直接暴露，不能依赖 ParseStandard 总是返回 error。
- **问题二：** `0 0 31 2 *` 可以完成语法解析，但 `Next` 返回零值表示没有可执行时刻。`isDue` 只检查 `next.After(now)`，将零值判为到期，每轮都尝试执行备份；任务若在 30 秒内完成，就会反复新建备份。
- **证据：** `TestReviewCronEdges` 分别捕获上述 panic、确认不可达日期返回 due。探针中的 recover 仅用于记录证据，生产路径没有该保护。
- **修复：** 配置写入和历史配置加载均进行结构、时区、可达性校验；显式处理 `Next(...).IsZero()`；错误配置禁用并发出可见错误，不能拖垮所有库的调度。去掉未使用的 `NewParser`，统一真正用于校验与求值的 parser。

### P1-06：Webhook 失败日志直接泄漏 URL secret，消息替换也不构成脱敏

- **位置：** `backend/internal/scheduler/webhook.go:22–36`；`backend/internal/scheduler/scheduler.go:273–281`。
- **问题：** Webhook URL 常以路径或 query 携带 bearer token。网络错误及 HTTP 4xx/5xx 路径直接记录完整 `url`，`err` 中还可能再次包含该 URL。`strings.ReplaceAll(message, "password=", "password=[REDACTED]")` 只插入标记，例如 `password=TEST_SECRET` 会变成 `password=[REDACTED]TEST_SECRET`，原值仍在；200 字节截断也不是安全边界。
- **证据：** `TestReviewWebhookRequestAndURLSecretLog` 用 mock transport 返回 403，路径和 query 中的测试标记原样进入生产日志。没有使用真实凭据或外部接收端。
- **边界：** 正常备份路径已对已知 PG/目的地 secret 做部分清洗，不能据此声称所有存量 error_message 都含明文密码；这里确认的是新发送器的 URL 日志泄漏，以及其消息处理无法提供二次保护。由于 P1-02，当前主程序尚不会执行发送；接线时必须一并修复。
- **修复：** 日志仅记录 webhook ID/安全名称、状态码和受控错误类别；移除 URL 路径、query、userinfo 及原始网络错误中的敏感上下文。通知优先发结构化错误类别/固定说明，需要附加文本时复用可靠脱敏机制并覆盖编码变体。不要把共享替换函数中的同类缺陷继续复制到新出口。

### P1-07：缺少持久配置快照与计划版本，暂停/修改和 pending 的语义未落实

- **位置：** 迁移 `0009_scheduling.sql`；`backend/internal/scheduler/scheduler.go:103–109,179–185`；`backend/internal/jobs/jobs.go:245–247,592–645`。
- **性质：** Phase 4 明确要求、尚未实现；不是本次改坏了原 worker。
- **问题：** 入队仅保存 database ID 和随机 backup UUID，源连接、recipient、目的地等到 `runJob` 才读取。现有内存中的 `destSnapshot` 能避免同一次导出过程中重读目的地，但不是入队时的持久配置快照。调度器加载配置后，入队 UPDATE 也不校验 `schedule_paused`/revision；暂停不会处理已经 pending 的任务，旧的加载结果仍可入队。删除时 UPDATE 影响 0 行也没有被检查。
- **影响：** 无法兑现“改配置后旧任务按快照完成”，也无法审计某个任务属于哪个计划版本。已有 API 对部分活动任务的修改限制不能替代计划变更状态机。
- **修复：** 在入队事务内保存不可变配置或版本引用；明确暂停、改 cron、删除时对 pending 和 running 分别采取的行为；计划版本变更与待执行合并/撤销原子完成。不要在补 API 时再用互不相干的多条 UPDATE 拼接。

### P1-08：长任务仍可占满唯一执行槽，并发治理没有完成

- **位置：** `backend/internal/jobs/jobs.go:358–376,479–486,541–553`；`backend/internal/jobs/upload.go:72–100`；`docs/dev-plan.md:164,167,171–172`。
- **性质：** Phase 4 要求未落实，执行器代码本次未改。
- **问题：** worker 同步运行完整 `runJob`，job context 只有取消，没有整体执行 deadline；dump/数据库查询或缓慢远端读写可长时间阻塞唯一执行槽。上传重试的等待仍在同一个执行函数中。现有外层退避仅 1 秒、2 秒，不能把它描述成已经实现了“持久长重试释放槽位”。
- **队列边界：** 部分唯一索引确实限制每库一个 active，且 worker 按 job ID FIFO 领取；因此不应称单库 pending 无界或完全没有排队顺序。但全局 pending 上限、同库最新计划的持久合并、重试就绪队列及能力不足的可见状态尚未实现。一个卡住的库会阻止其他三个竞争库及立即备份推进。
- **修复：** 增加基于持续时间的任务 deadline 和锁等待限制；把远端重试等待持久化，等待期间释放执行资源、复用原 artifact；定义队列总预算与按库调度规则。补低峰/超时/锁等待的运维说明，用假时钟及可阻塞 backend 测 DoD，不必依赖真实网络超时。

## P2

### P2-01：时区与新鲜度的实际语义没有如实建模

- **位置：** `backend/internal/scheduler/scheduler.go:108–114,153–175,189–201`；`backend/internal/jobs/jobs.go:525,541,683,713`；`backend/internal/manifest/manifest.go:17–20`；迁移 `0009_scheduling.sql:9`。
- **时区：** 无效 IANA 名称静默回退 UTC；表达式中的 `CRON_TZ=` 又会覆盖独立的 `cron_tz` 字段，日志仍输出后者，可能在错误时刻执行。正常五字段 cron 用 `.In(loc)` 传给该版本库是有效的，不能误报为“cron_tz 完全不生效”。
- **DST 实测：** `America/New_York` 的 `30 2 * * *` 在 2026-03-08 春季跳时跳过不存在的 02:30；`30 1 * * *` 在 2026-11-01 秋季回拨会命中两个不同 UTC 时刻。实现没有文档说明或业务测试锁定这一行为，也没有明确时钟前后跳后的策略。
- **新鲜度：** `lastSuccessAge` 使用 worker 领取时的 `started_at`，不是导出快照时刻；实际 dump 开始更晚，当前 manifest 已明确真实快照时刻不可观测。这种估算通常会提前报超期，不能宣称精确快照年龄。数据库查询失败、从未成功及时间缺失都被折叠为 `1e9`，丢失错误原因；`freshness_hours` 列没有任何读写者。`schedule_paused` 还会同时停止超期检查，暂停调度是否也暂停保护监控需要明确规定。
- **修复：** 拒绝无效/冲突时区，定义 DST、初次启用、时钟跳变语义。持久化可解释的导出时间依据及其精度，区分无备份、状态未知、已超期；没有真实快照时间就明确标注保守估计，不要把领取时间重命名为快照时间。

### P2-02：通知缺少并发预算、停止等待和重复告警控制

- **位置：** `backend/internal/scheduler/scheduler.go:67–70,74–98,113–123,298–306`；`backend/internal/scheduler/webhook.go:15`。
- **问题：** 每个数据库 × 每个目标直接起 goroutine，没有并发上限；持续超期时每 30 秒重发一次，即每库每目标每天最多约 2,880 次。10 秒 timeout 限制单次请求寿命，但不能限制同一轮的大量请求。传入的 context 被丢弃，发送使用 `context.Background()`；`Stop` 只关 channel，不等待当前 tick/发送结束，关闭数据库或退出时仍可能有任务在执行。
- **并发安全边界：** `SetWebhooks` 与读取 slice header 使用同一 mutex，当前内部 payload 构造后只读，不能据此认定已有 map race。可是 setter 没有复制输入 slice，调用方后续修改元素会绕过锁；`TestReviewSetWebhooksAliasesInput` 证实别名关系，未把它夸大为已存在的生产竞态。`Start` 也不保证只启动一次，当前 main 只调用一次。
- **修复：** 小型有界发送队列/worker、继承生命周期 context、停止后 join；按状态转换或 cooldown 限流超期告警；复制配置 slice。按精确事件集合匹配，避免 `strings.Contains` 的子串订阅。完整通知 outbox 与重试系统仍可按计划留到 P7。

### P2-03：Webhook 重定向没有出站边界，POST 格式可能被重定向改变

- **位置：** `backend/internal/scheduler/webhook.go:22–35`；对照 `docs/dev-plan.md:188`。
- **问题：** `http.DefaultClient` 默认跟随重定向，没有逐跳 origin/IP 校验。mock transport 验证 HTTPS 接收端返回 307 后，同一 JSON POST 可以转向 `http://169.254.169.254/metadata`；测试没有实际访问 metadata。301/302/303 还可能把 POST 改成 GET；最终响应仅以 `>=400` 判失败，未要求 2xx。
- **边界：** 当前没有读取响应正文，未发现无界响应体读取；有 10 秒超时。这里是出站目的地/协议控制缺失，不能据此宣称已经窃取 metadata 凭据。完整 SSRF 策略在计划 P5，故作为 P2 提前列出；修复 P1-02 开放 Webhook 配置时应同步确定边界。
- **修复：** 使用专用 client，限定 HTTP(S)、默认拒绝或严格验证重定向，解析/连接及每跳复核 metadata/link-local；管理员需要内网目标时显式配置。仅 2xx 视为成功，保留受控失败状态。

### P2-04：为加入 0009 弱化了迁移失败测试，关键安全门禁不再被覆盖

- **位置：** `backend/internal/db/db_test.go:291–322`；`backend/internal/db/upgrade_test.go:111–214`。
- **问题：** 本次删除“注入 pending migration 后调用 `Migrate`”的测试路径，改为直接调用 `backupBeforeMigrate`。这只能证明辅助函数失败，不能证明真实升级会拒绝继续写入。注释声称 Upgrade fixture 已覆盖错误传播，但这些测试分别检查成功升级或 CLI 拒绝 pending，没有注入备份失败并断言 `Migrate` 拒绝执行 DDL。
- **修复：** 保留当前迁移集合，注入一个高于实际 target version 的临时迁移，破坏备份目录后执行真正的 `Migrate`，断言错误、版本和目标列均不变；不要再固定使用会与未来真实迁移冲突的 `0009_test_*`。

## 保留路径复核：基线已存在的问题

以下项不计作本次新增回归，但用户指定的保留验收仍未通过；可与 [Phase 3 报告](codex-phase3-review-1.md) 中对应事项合并跟踪。

| 级别 | 函数与位置 | 本轮结果、影响与修复方向 |
|---|---|---|
| P1-R01 | `runRemoteRetention`，`upload.go:219–259` | 仅把最新 committed 行作为锚点，不要求 `remote_verified=1`、不持久化资格。`TestReviewUnverifiedBackupReplacesAnchor` 在“旧已验证 + 新未验证 committed”的遗留状态夹具中，确认最后一个已验证副本会被删。正常新上传已经强制校验；此探针验证的是保留函数对升级遗留状态的保护缺口，不声称每次正常 runJob 都会丢锚点。须隔离/复核遗留状态，事务维护合格锚点，无合格锚点时禁止破坏性清理。 |
| P1-R02 | `deleteRemoteBackup`，`upload.go:266–299` | 密文 DELETE 成功而 manifest DELETE 失败时直接 return，数据库仍是 committed；没有 deleting 意图或逐对象进度。失败未回滚备份成功状态是正确方向，但部分删除不等于仍然完整可下载。须持久化删除阶段、独立恢复并排除下载；本轮静态确认，与前次报告已复现的问题一致。 |
| P2-R01 | `deleteRemoteBackup`，`upload.go:280–296`；`runJob`，`jobs.go:819–822` | `TestReviewLocalKeepOverriddenByRemote` 复现 `localKeep=10, keepRemote=1`：本地 prune 刚保留的旧文件被远端 retention 删除。配置传参虽分开，实际副本生命周期未解耦。远端删除只更新远端状态，本地文件完全交给本地策略。 |
| P2-R02 | `pruneLocalArtifacts`，`upload.go:346–372`；`deleteRemoteBackup`，`:282–296` | unlink 出错后仍清空路径，查询失败或路径跳过时也可能继续清空。`TestReviewPruneLosesFailedDeleteReference` 用不可删除的非空目录注入失败，确认磁盘内容存在而引用丢失。仅删除成功/确认不存在时清空对应引用，保留失败状态与重试；两条候选查询也应检查 `rows.Err()`。错误日志不应等同于已完成可追踪清理。 |
| P2-R03 | `runRemoteRetention` / `pruneLocalArtifacts`；`api_phase3.go:181–207,224–244` | 远端没有下载租约，本地仅排除 uploading，未覆盖下载引用。15 分钟预签名 URL 发出后可被下一轮保留提前删除；本地存在取路径到打开文件之间的竞争。需要引用/租约保护。已打开的 Linux 文件不会仅因 unlink 必然中断，本报告不作该扩大断言。 |

补充边界：`runJob` 确实在成功落库后调用本地 prune 和远端 retention，清理失败不把备份改为失败；但 retention 再次读取当前目的地，而非复用本任务的快照。失败/待上传产物也不在 succeeded/committed 清理集合中，且清理依赖后续成功任务触发。连续故障的预算、恢复和保护冲突告警仍需与既有 Phase 3 遗留事项一起解决。

## 已验证正确或不应误报的部分

- **`/metrics` 文本：** 三个名称、HELP/TYPE、数值样本、末尾换行符合当前简单文本输出结构；都是 gauge，无 label，因此没有 task/backup/table ID 或自由错误文本造成的高基数。问题在认证。任务/远端成功率等更丰富指标在 P7，不单独作为 P4 阻塞项。未运行 Prometheus 官方解析器，不能把结构检查表述成真实 Prometheus 抓取验收。
- **Webhook 原始请求：** POST + JSON，`Content-Type: application/json`，事件 Header 为 `backup_failed`/`backup_expired`，与 payload 的 event 一致；有 10 秒超时且关闭响应体。格式探针已通过。计划没有要求 Slack 等厂商专用消息格式或 HMAC，不把缺少这些功能列为错误。
- **配置校验：** `config.Load` 的 `SB_LOCAL_KEEP` 默认 5、要求正整数；`SB_STAGING_QUOTA_BYTES` 默认 0、要求非负 int64。临时测试验证默认、最小值、非法字符串、负数和溢出均按预期处理；`runServe` 正确调用 `SetLocalKeep/SetQuota`。这些不是本次新增改动。配额注释定义的是单次密文写入上限，不能直接宣传为总 staging 占用预算。
- **互斥与时间：** SQLite 的单库 active 唯一索引确实存在；IANA 时区的正常求值已验证；`time.NewTicker` 本身不会为每个 cron 建独立 goroutine。不能因为事务调度不完整就否定这些已经存在的保护。

## 验证记录与测试缺口

### 本轮运行

1. `GOCACHE=/tmp/codex-phase4-go-cache go build ./backend/...`：**通过**。最初默认缓存位于只读目录，改用 `/tmp` 缓存后成功；构建曾输出模块 stat-cache 写入警告，但最终退出码为 0。
2. `GOCACHE=/tmp/codex-phase4-go-cache go test -race -count=1 -json ./backend/...`：**整体未通过，原因是环境限制**。11 个有测试的包通过；server 在 `TestPhase2DefaultDenyAndAgeStatus` 创建 httptest listener 时因 `socket: operation not permitted` panic。PostgreSQL/MinIO 容器启动不可用，6 项集成测试跳过：`TestM1_NegativeControl`、`TestM1_FullKernelChain`、`TestInterruptedRecoveryAtStartup`、`TestConcurrentEnqueueExactlyOne`、`TestCancelPendingJobReachable`、`TestMinIOEndToEnd`。不能报告为“全量 race 通过”或“真实桶/PG 已验收”。
3. 临时 Go overlay：**12 个顶层定向探针在 `-race` 下通过**，覆盖上文入队失败/冲突、cron 边界、SQL 窗口、Webhook 格式/日志/重定向、匿名 metrics、本地与远端保留、unlink 失败、遗留锚点及配置校验。缺陷探针断言的是不正确行为确实存在，PASS 不代表生产实现合格；配置与正常格式探针则断言预期正确行为。初版 metrics 探针同样受端口限制，最终改为内存 Recorder 后通过。
4. 两个 `scripts/*.sh` 的 `bash -n` 通过；Python `yaml.safe_load` 检查 `compose.yaml`、`api/openapi.yaml`、`api/cfg.yaml`、`.github/workflows/ci.yml` 全部通过。该检查仅证明语法可解析，未运行 shell 业务或工作流。

原始 race 输出：`/tmp/codex-phase4-race.jsonl`。临时探针、overlay 映射及最终结果：`/tmp/codex-phase4-probes/`、`output-final.txt`。这些是本地评审证据，不属于提交内容；本文已记录其夹具、调用点和结果，不能把临时探针当作仓库已有测试。

### 应加入仓库的回归测试（具体函数名）

当前 scheduler 包没有任何仓库测试；metrics 没有专门的认证/格式测试；config 现有测试只覆盖 secret 文件，未覆盖 `Load` 的两个数值项。

| 被测函数 | 建议测试名与必须断言的内容 |
|---|---|
| `Scheduler.enqueue`、`Runner.Enqueue` | `TestScheduleEnqueueAtomicRollback`、`TestScheduleCrashBeforeAfterCommit`、`TestScheduleConflictRetainsLatestDue`：事务任一失败游标与 job 同进退；同库长任务/立即备份冲突后最新 due 不丢；重启同一计划身份不重复。 |
| `isDue` / 计划求值器 | `TestCronIANAAndDST`、`TestCronRejectsMalformedTimezonePrefix`、`TestCronUnreachableDate`、`TestCronTimezoneConflict`：普通 IANA、春跳/秋重、零 Next、无空格前缀、错误时区不静默回退。 |
| `Scheduler.tick`、计划变更操作 | `TestScheduleFirstEnable`、`TestScheduleCatchUpLatestOnly`、`TestScheduleClockJump`、`TestSchedulePauseAndRevision`：首次启用边界、跨周期仅补最近一次且保留原计划 UTC、时钟前后跳、暂停/改计划与入队竞争。使用 fake clock，避免靠 sleep 和真实日期偶然命中。 |
| `Runner.claimAndRun`、`runJob`、`uploadAndCommitRemote` | `TestQueuedJobUsesConfigSnapshot`、`TestLongTaskDoesNotStarveOtherDatabases`、`TestUploadRetryReleasesSlot`、`TestGlobalPendingBudget`：配置变更前后隔离、一个阻塞任务加三个竞争库、重试复用同一 artifact、过载状态可见。 |
| `loadSchedules`、运行时 Webhook 配置加载 | `TestScheduleConfigPersistsAcrossRestart`、`TestWebhookConfigLoadedAtStartup`：通过真实配置入口写入、重启后实际调度/通知、软删除与禁用生效。 |
| `checkFailures` | `TestFailureWindowIncludesRecentJob`、`TestFailureScanOverTenJobs`、`TestFailureNotificationDeduplicated`：SQL cutoff 边界、超过 10 条、公平推进、重复轮询、扫描错误、重启/超窗语义。 |
| `lastSuccessAge` | `TestFreshnessUsesExportTime`、`TestFreshnessDelayedUpload`、`TestFreshnessUnknownAndNoSuccess`：领取与导出时间不同、旧密文晚上传不重置年龄、查询失败不伪装为真实年龄、暂停时监控策略。 |
| `postWebhook`、`fireWebhooks`、`SetWebhooks`、`Stop` | `TestWebhookJSONContract`、`TestWebhookSecretsAbsentFromLogsAndPayload`、`TestWebhookRedirectPolicy`、`TestWebhookTimeoutAndShutdown`、`TestWebhookConcurrencyBound`、`TestWebhookConfigCopyIsolation`：结构/事件一致，URL 与消息 secret 不泄漏，307/跨 origin/metadata 拦截，2xx/429/5xx，取消/join、有界发送、配置复制。用注入 client 或 mock transport，避免测试依赖外部网络。 |
| `Server.Router`、`handleMetrics` | `TestMetricsRequiresAuthentication`、`TestMetricsExpositionFormat`：匿名与无效/过期/撤销凭据拒绝，有效监控凭据成功；用文本解析器校验 HELP/TYPE/sample，无动态高基数 label。认证测试必须走 Router。 |
| `pruneLocalArtifacts`、`deleteRemoteBackup`、`runRemoteRetention` | `TestLocalAndRemoteRetentionIndependent`、`TestPruneRetainsFailedDeleteReferences`、`TestRetentionRejectsUnverifiedAnchor`、`TestRetentionPartialDeleteRecovery`、`TestRetentionHonorsDownloadLease`：10/1 与 1/10、count/day、unlink/DB 故障、无合格锚点、部分删除崩溃恢复、活动引用。现有 `TestRetentionPolicyKeepsAnchor` 仅保护最新行，且夹具默认未设置 remote_verified，不能证明合格锚点协议。 |
| `config.Load`、`Store.Migrate` | `TestLoadLocalKeepValidation`、`TestLoadStagingQuotaValidation`；恢复 `TestMigrateRefusesWhenPreMigrationBackupFails` 的完整升级路径，补 `TestUpgrade0008To0009PreservesJobs`，验证默认字段、历史数据和备份失败时拒绝升级。 |

## 总体结论与最关键 3 件事

**不建议将当前 HEAD 标记为 Phase 4 完成。** 它提供了 cron 轮询、JSON 发送和指标输出的基础，但持久调度事务、配置接线、最低告警可用性、认证和并发治理仍缺少关键保证；保留路径也存在已确认的既有缺口。构建成功和部分测试通过不足以覆盖 Phase 4 的 DoD。

1. **先保证不静默漏备：** 原子完成计划入队与游标更新，加入计划身份、最近一次补跑、配置快照和冲突合并；处理异常 cron，并以崩溃/竞争/时钟/DST 测试验收。
2. **让最小告警真正可用且不泄密：** 接通持久配置与 Webhook 加载，修正失败 SQL，限制重复/并发发送，清理 URL 与错误消息 secret；同时为 `/metrics` 加真实认证。
3. **兑现资源与副本保护：** 给长任务和重试明确执行预算，补队列治理；本地/远端保留独立，合格锚点与活动引用不可删，清理失败保留状态并能重试。将对应回归用例纳入 CI 后再验收。
