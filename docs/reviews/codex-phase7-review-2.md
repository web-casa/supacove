# Phase 7 第二轮复审

评审日期：2026-10-05。基线：`6e3e8ff`；修复提交及实际 HEAD：`33519e1109bdb9247a381c10fbbee4fcc1803f21`。范围：`git diff 6e3e8ff..33519e1`，并追踪生产调用链、历史迁移和上一轮报告。本文行号均指修复提交，路径相对仓库根；明确标注的历史证据除外。

**总体结论：仍不通过 Phase 7 完成验收。原 12 项：FIXED 8 项、PARTIALLY_FIXED 4 项、NOT_FIXED 0 项、REGRESSED 0 项；另发现本次改动引入的 P1 1 项、P2 1 项。** 未把同一问题在原编号和新编号重复计数。原子通知、补偿推进性、投递租约和心跳拨号防护已有实质改善；阻碍关闭的是禁用配置入口、start 协议范围、历史名称碰撞与不完整的采集错误处理。新增问题涉及迁移回退/再次升级和验证失败结果的运行期收敛。

## 一、原编号逐项判定

| 编号 | 判定 | 证据与关闭边界 |
| --- | --- | --- |
| P1-01 | **FIXED** | `backend/internal/jobs/jobs.go:69` 使用 `outbox.DeliveryClient(5s)`；`heartbeat.go:110`、`:145` 的成功/失败请求均使用它。`backend/internal/outbox/outbox.go:149` 的 transport 在 Dialer.Control 检查最终 IP，重定向的新连接也经过该边界，没有启用环境代理绕过。现有 `TestLinkLocalDenied` 在 race 下通过。没有声称完成 DNS/重定向真实网络演练。 |
| P1-02 | **FIXED（沿用未应用旧版 0014 的前提）** | `backend/internal/db/migrations/0014_outbox_heartbeat.sql:28` 转换现存 live 旧订阅；`backend/internal/db/upgrade_test.go:323`、`:333`、`:340` 的真实旧库升级测试通过，`failure,expired` 得到 `backup_failed,backup_expired`，与 `backend/internal/outbox/outbox.go:411` 的精确匹配一致。不能覆盖已经应用旧版 0014 的持久库；原地修改口径见第五节，Down/再次 Up 的新回归见 R2-P1-01。 |
| P1-03 | **FIXED** | `backend/internal/jobs/jobs.go:997`、`:1048` 优先同事务提交；INSERT 失败由 deferred Rollback 撤销状态，再走 state-only 回退。`backend/internal/scheduler/scheduler.go:324`、`:374` 对备份/验证失败分别无时间窗补偿。`backend/internal/jobs/verify.go:475`、`:495`、`:503`、`:519` 在同事务更新验证终态及入队，失败返回 false，不对未提交状态发通知。故障探针验证了回滚和恢复后原子提交；405 条积压推进与幂等见第二节。新事务缺少运行期重试是另列的 R2-P2-01，不否定原子性修复。 |
| P1-04 | **PARTIALLY_FIXED** | `/fail` 的生产链路已关闭：`backend/internal/jobs/jobs.go:1023` 只在失败提交后调用；`backend/internal/jobs/heartbeat.go:157` 正确按 URL path 加 suffix。重复 fail/回退探针只捕获一次请求。但 `heartbeat.go:16` 仅用注释宣布 v1 不发 start，`docs/dev-plan.md:218` 仍要求区分 start/fail/success；没有同步的验收范围变更依据。因此“生产 fail 不通”已修复，“完整 start 协议交付”未关闭。如明确批准 v1 排除 start 并同步计划/用户文档，本项可关闭，不要求为了代码形式盲目补 start。 |
| P1-05 | **PARTIALLY_FIXED** | `backend/internal/jobs/heartbeat.go:56`、`:79` 正确实现禁用标记和无条件 period 门控，48 组合探针通过。但 `backend/internal/server/api_phase7.go:134`、`:140` 把 `"-"` 交给 HTTP URL 校验，实际 API 返回 400；`:159` 还会把禁用标记视为需要 period 的 URL。`api/openapi.yaml:916` 仍写“0 disables the age gate”，与实现相反。详见第三节。 |
| P1-06 | **FIXED** | `frontend/src/App.tsx:246` 增加 `key={scheduleFor}`，切换库会重建表单 state，原 A 草稿误写 B 的原因消除。静态确认，前端 build/lint 通过；未声称浏览器复现。上一轮附带建议的 GET 失败禁用保存、保存后失效 schedule 缓存仍未落实（`:329`、`:334`、`:400`），是既存建议残项，不将其包装为本次引入的跨库回归。 |
| P2-01 | **FIXED** | `backend/internal/outbox/outbox.go:320`、`:321` 发送事件类型与稳定 event ID；无监听 RoundTripper 捕获断言通过。`backend/internal/server/api_phase7.go:264`、`:270`、`:369`、`:370` 的测试发送使用 json.Marshal 及相同 Header 契约。构造代码仍各自实现，并非真的抽出一个共用函数，但线上/测试字段一致。测试 ID 格式见第四节。 |
| P2-02 | **FIXED（单实例投递模型）** | `backend/internal/outbox/outbox.go:257` claim 写 15 分钟租约；`:227` 每 pass 恢复过期租约；`:335`、`:350`、`:359` 的结果写入均经 `execRetry`。探针注入 delivered UPDATE ABORT 后撤销故障、推进租约到期，下一 pass 得到 delivered，不再永久 delivering。运行期恢复能力成立；延迟、单实例和监控边界见第四节。 |
| P2-03 | **FIXED** | `backend/internal/jobs/heartbeat.go:105` 至 `:123` 仅在请求完成且响应未被判拒绝后写时间戳，构造/网络错误和 HTTP >=400 均提前返回。race 探针覆盖成功请求后异步写库。共享 `*sql.DB` 可并发使用，Runner 构造后未重赋值 authDB；不能仅因移入 goroutine 就认定 Go data race。退出生命周期限制见第四节。 |
| P2-04 | **PARTIALLY_FIXED** | `backend/internal/db/migrations/0014_outbox_heartbeat.sql:35` 保留最老同名行并给其他行追加 ID，普通三重同名测试通过。但生成名未排除与已有真实名称碰撞：id=1 `ops`、id=2 `ops`、id=3 `ops #2` 会在 `:45` 建索引时失败。真实旧库 goose 升级探针复现 `UNIQUE constraint failed: webhooks.name`。不能据 `upgrade_test.go:360` 的三个相同名称 fixture 推断所有历史名称可升级。 |
| P2-05 | **FIXED** | `backend/internal/server/metrics.go:69` 至 `:72` 恢复两个旧 gauge 别名并明确 DEPRECATED；`:57` 明确新 jobs_total 是当前状态分布，不是单调 counter。无需因 `_total` 命名另判格式错误。 |
| P2-06 | **PARTIALLY_FIXED** | `backend/internal/server/metrics.go:32`、`:189` 增加 collector 错误信号；outbox 查询失败确实省略值（`:145`）。但 remote 出错后仍无条件输出零（`:77` 至 `:85`），与修复摘要“失败不打印零值”不符。jobs/verification/protection 仍忽略 Scan 和 rows.Err（`:50`、`:100`、`:164`）；last-success Query 错误无标记（`:116`），staging WalkDir/Info 错误仍被吞（`:211`）。关闭 DB 的探针确认 remote 同时输出 0 与 error=1；部分其他错误仍完全不可见。 |

## 二、通知补偿 SQL、幂等与批次推进

`backend/internal/scheduler/scheduler.go:333` 是：

```sql
WHERE o.event_id = 'backup_failed:job:' || j.id
```

这是 SQLite 文本拼接，结果与 `jobs.go:1031` 的 `fmt.Sprintf("backup_failed:job:%d", jobID)` 相同，不是加法，也没有字符串引号内误包列名。验证失败查询 `scheduler.go:381` 同理。

`NOT EXISTS` 的子查询在记录入队后变为存在，因此下一轮的 `NOT EXISTS` 为 false。`ORDER BY j.id DESC LIMIT 200` 不会永远重复取相同的最新 200 条。真实迁移数据库插入 405 条历史失败任务，连续四次调用实际 `reconcileNotifications`，outbox 数量为 **200→400→405→405**；没有时间窗限制。有限积压、数据库写入恢复后可推进；每周期持续新增超过 200 条或最新 200 条持续不可写时，不保证老条目的公平性，这是吞吐/持续故障条件，不能误称本次 SQL 在健康情况下不推进。

主路径与补偿同时入队也不会创建两条通知：`backend/internal/outbox/outbox.go:73` 至 `:79` 使用 UNIQUE event_id + `ON CONFLICT(event_id) DO NOTHING`，涵盖主事务、回退后的 best-effort enqueue 和 scheduler。outbox 状态为 pending/delivering/delivered/dead 都满足 EXISTS，因此不会绕过 dead 的重试上限反复创建事件。该去重只针对入队；HTTP 仍是至少一次交付。

故障探针：BEFORE INSERT trigger `RAISE(ABORT)` 使原子失败事务回滚，`fail()` 回退后得到 job=failed/outbox=0；这条持久 failed 状态就是后续扫描的补偿依据。验证路径同样注入 INSERT ABORT，`finishVerificationWithNotify` 返回 false，verify_status 保持 running；撤销故障再调用，终态和一条通知一起提交。不存在原来的“状态已提交但永远漏通知”窗口，也没有未持久化验证终态的通知。

扫描阶段先收集并关闭 rows 再写库（`scheduler.go:353`、`:401`），不会持有同一读游标边扫描边追加 outbox。仍建议补 Scan/rows.Err 日志（`:347`、`:395`），当前错误只结束当前批次，下一轮可重试，未据此新增永久丢失结论。

## 三、心跳行为矩阵与 API 契约

以下两表的笛卡尔积覆盖要求的 **3×2×2×2×2=48** 个组合。`U` 为数据库 URL，`F` 为全局 fallback；period 分 0/正，hasDestination 与 remoteCommitted 分别取布尔值。探针对新鲜快照逐组合调用真实 `heartbeatFor`，均符合下面的运行时结果。

| U | F | 最终 URL / 禁用结果 |
| --- | --- | --- |
| 空 | 空 | 无 URL，不发 |
| 空 | 有 | 使用 F |
| 有 | 空或有 | 使用 U |
| `-` | 空或有 | 明确禁用，不发 success，也不发 fail |

仅对最终有 URL 的行继续门控：

| period | hasDestination | remoteCommitted | success 结果 |
| --- | --- | --- | --- |
| 0 | 任意 | 任意 | 不发；有 destination 且未提交时，日志先显示未提交原因 |
| 正 | true | false | 不发 |
| 正 | true | true | 仅快照年龄 <= period+grace 时发 |
| 正 | false | false 或 true | 本地-only 例外；仍必须通过同一年龄门控 |

证据：`backend/internal/jobs/heartbeat.go:55`、`:71`、`:79`、`:83`。年龄恰等于阈值允许，超过才拒绝；老密文晚上传不会绕过 fallback 的门控。`pingFail` 传入当前时刻/已提交/无 destination（`:130`），随后只要求非空 URL 且不是显式禁用（`:131`），所以 period=0 也能报失败；这是合理的失败信号语义，不能要求失败必须先证明成功新鲜度。`-` 分支在设置 dec.url 前就返回，禁用成立，虽然依赖 skip 文本作二次判断较脆弱。

**配置入口未闭合：** 对真实 `PutDatabaseSchedule` 传 `heartbeatUrl="-", heartbeatPeriodHours=24`，无监听 API 探针返回：

```text
400 invalid_request: heartbeatUrl: URL scheme must be http or https
```

应仅在调度 API 把 `-` 当专用禁用值，并从 period 必填判断中排除它；不要使普通 webhook URL 验证接受 `-`。同时把 `api/openapi.yaml:916` 改为“0 不发送成功 ping”，说明 fallback 也需要 period、fail 可绕过年龄门控。当前 `:912`、`:948` 承诺支持禁用，API 却拒绝，属于可复现的用户路径缺口。共享 fallback 仍不能当作每库独立外部监控端点；一个健康库可刷新共用 endpoint，这一既有限制需要用户文档明确。

### URL suffix

对 `backend/internal/jobs/heartbeat.go:157` 的真实函数探针得到：

| 输入 | 输出 |
| --- | --- |
| `https://hc-ping.com/abc?token=1` | `https://hc-ping.com/abc/fail?token=1` |
| `https://hc-ping.com/abc?token=1#frag` | `https://hc-ping.com/abc/fail?token=1#frag` |
| `https://hc-ping.com` | `https://hc-ping.com/fail` |

题述第一个 URL 实际有 `/abc` path，纯 host 才是空 path，二者均正确。fragment 保留在 URL 字符串中，不作为 HTTP 请求目标发给服务器；query 保持原值，不会把 `/fail` 塞入 token。

## 四、指定新增风险核查

| 核查点 | 判定与证据 |
| --- | --- |
| `execRetry` 阻塞 | 确实串行阻塞当前 delivery pass，但不是“3×600ms”。`outbox.go:388` 至 `:392` 最多 3 次 Exec，sleep 为 200+400+600ms=1.2s，最后一次失败后也睡；ABORT 探针约 1.208s。SQLite busy_timeout 为 5s（`db/db.go:112`），锁等待会额外增加时间。十条都失败时仅 sleep 就约 12s；建议最后一次不睡且 sleep 可响应 ctx，属有界延迟，不判为永久投递阻塞。 |
| 15min 租约与 10×10s | `outbox.go:228` 的 10 是每 pass 最多事件数，不是 receiver 数上限。每条事件临发送前才 claim；10 个 target 各耗 10s 时，一条约 100s，小于 900s。10 条事件总 pass 可约 1000s，但各自租约独立，不能拿整个 pass 时间直接判定单条租约不足。targets 查询没有数量上限（`:397`），>90 个慢 target 可超过 15min；当前串行模型不会在该条发送中途自行恢复。 |
| 单实例假设 | `db/db.go:95` 有 OS 独占 flock，生产只启动一个 notifier（`cmd/supabackup/main.go:299`）；`outbox.go:196` 同步执行 pass，运行期恢复只在下一 pass 开始。当前假设成立。将来并发 pass/多 notifier 需要 owner token/fencing，现有无 owner 租约不是分布式排他协议；不能把 claim 注释泛化成所有并发投递都安全。 |
| 崩溃后的重放延迟 | `outbox.go:210` 的 startup recoverStuck 仅改 state，不清新的租约时间。崩溃后 pending 可能仍等到原 15min 到期才被 claim。不会永久卡死，但不同于立即重放，建议启动独占恢复时同步重置 next_attempt_at。 |
| TestWebhook event ID | `api_phase7.go:270` 为 `test-YYYYMMDDTHHMMSS.nnnnnnnnn`，真实事件为 `backup_failed:job:N` 等。Header ID 契约是稳定去重键，没有统一格式要求；测试是独立单次事件，不经 outbox 重试，格式不同本身不构成缺陷。时间生成不是严格无碰撞 ID，如未来承诺强唯一性再用随机 ID。 |
| fail 双 commitFailure / 重复 ping | `jobs.go:997` 失败才走 `:1003`，两者都不调用 ping；只有统一的 `:1023` 调一次。重复对同一终态任务 fail，`:1057` 的 active 条件及 `:1061` 的 RowsAffected 检查使两次尝试都失败并提前返回。故障注入触发 fallback，再重复 fail，总共只捕获 1 次 `/fail`；不存在所疑的“双提交路径双 ping”。 |
| authDB 并发与生命周期 | `jobs.go:112` 构造时赋值，未发现生产运行中重赋值。并发调用同一 `*sql.DB` 无须为指针加锁；定向 race 没有报告。心跳仍是 detached goroutine（`heartbeat.go:102`、`:137`），`Runner.Stop` 只等待 r.wg（`jobs.go:472`），故 shutdown 可发生请求/成功时间戳未完成，DB 关闭时 Exec 会报错；这是生命周期/尽力发送限制，不等于内存 data race。后续应纳入取消及等待，不能称退出后成功时间戳有强持久保证。 |
| metrics sort import | `metrics.go:9` 被 `:196` 的 sort.Strings 使用，build/vet 通过，没有 unused import；修复摘要在 remote 零值方面不准确，但 import 本身无问题。 |
| Overview NullInt64 | `jobs/queries_schedule.go:201`、`:214` 与 started_at 的 INTEGER/NULL 类型一致；消除字符串解析，无成功任务仍为 never。`TestOverviewStates`、`TestScheduleConfigRoundTrip` 在 race 下通过，未发现此改动回归。 |

## 五、0014 原地修改与新引入问题

### 原地修改口径

沿用上一轮报告的条件：**若 0014 仍属未发布且不存在需要保留的已应用旧版 0014 数据库，继续在该迁移修正可接受；“第二次编辑”本身不是问题。** 不能反过来假定这些环境不存在。`backend/internal/db/migrate.go:36` 至 `:41` 对无 pending 的数据库直接返回；同版本新增 UPDATE 不会重跑。

因此，若任何持久环境已经应用 `6e3e8ff` 的 0014，本次订阅转换对它无效，必须追加前向修复迁移及“已应用旧 0014 → 新代码”测试。现有 `TestUpgradeWebhookVocabulary` 是旧 schema 首次升级，不能证明这一路径。P1-02 的 FIXED 仅在上面的原定前提内，不是对已应用环境的无条件放行。

旧 `0009` 的 events DEFAULT 仍未修改；目前 API 创建显式提供新词汇，未发现正常新写入依赖旧默认值，故未仅因 DEFAULT 存在继续阻断 P1-02。也不能据此允许未迁移的旧写入方继续写默认值。

### R2-P1-01：Down 保留新词汇会丢失旧版失败通知；再次 Up 还会破坏过期订阅

**新增 P1。** 证据：`backend/internal/db/migrations/0014_outbox_heartbeat.sql:29`、`:59`。Down 注释承认转换不逆转，但称旧二进制把新词当作 “unknown-but-harmless CSV” 不准确：旧版本 `3161998:backend/internal/scheduler/scheduler.go:341` 以 `failure` 发信号，`:381` 用 `strings.Contains(wh.Events, event)` 过滤。`backup_failed` 不包含 `failure`，因此原有失败通知在回退后静默消失。`backup_expired` 包含 `expired`，不能夸大为旧版本所有事件一律停止。

此外，Up 的全字符串 REPLACE 会再次命中新词 `backup_expired` 内的 `expired`。执行目标文件的完整 Up→Down→Up（最小相关 SQLite schema）观察到：

```text
首次 Up: failure,expired -> backup_failed,backup_expired
Down:    backup_failed,backup_expired
再次 Up: backup_failed,backup_backup_expired
```

再次升级后新投递器精确匹配 `backup_expired`，该订阅失效并可能走零 target delivered。此问题由本次新增的数据转换引入，与此前 Down 仅撤销新增 schema 不同。Down 的当前说明不足以构成安全回退契约。

建议：对 CSV token 做精确、幂等的词汇转换，并设计可用的逆向词汇转换或明确仅允许恢复升级前快照的回退流程。保留重命名后的接收端名称可以接受，无需为了逆转名称重新制造冲突；关键是不能把失败告警失效称为 harmless。补真实 goose Up→Down→Up 和旧版订阅匹配测试。

### R2-P2-01：验证失败的新事务一次错误即放弃，运行期不再保存已得出的结果

**新增 P2。** 证据：`backend/internal/jobs/verify.go:406`、`:480`、`:515`；对照仍保留的旧通用终态写入重试 `:460`，以及仅在启动恢复的 `:160`。新 `finishVerificationWithNotify` 在 Begin/UPDATE/INSERT/Commit 任一错误后直接返回 false，调用方立即 return，没有复用原有三次终态写入重试，也没有运行期持久补偿该未提交结果。

一次短暂 outbox INSERT 故障足以把已经得到失败结论的验证留在 running、outbox=0。故障消失后 scheduler 只扫描 failed/unsupported 终态（`scheduler.go:379`），不会重新保存这个结果；需下次进程启动重新验证，或显式人工恢复。故障探针确认回滚后保持 running；撤销故障不会自行改变该状态，测试必须再次调用提交函数才能落盘。重启收敛路径存在，所以不判为原 P1-03 的永久已提交状态漏通知，但相对原有有界重试是运行期恢复退化。

建议：对整个“终态 + outbox”事务做有界重试，不能在重试中拆开原子性；重试耗尽后为未完成验证提供可追踪的重排/恢复机制。没有持久化就不发通知的原则应保留。

## 六、验证记录与边界

统一使用 `GOCACHE=/tmp/codex-phase7r2-cache`。未监听端口，未修改产品实现。

| 验证 | 实际结果 |
| --- | --- |
| `go build ./backend/...` | 通过；仅打印只读模块 stat cache 写入警告，退出码 0。 |
| `go vet ./backend/...` | 通过。 |
| 相关五包 `go test ... -run '^$'` | 编译通过；用于确认改动后的测试代码也可编译，不计为功能测试通过。 |
| db/jobs/outbox 定向 `go test -race -count=1`：Upgrade、Upgraded、CLI 前缀及 OverviewStates、ScheduleConfigRoundTrip、EnqueueDedup、NoTargetsIsSuccess、LinkLocalDenied | 通过；包含新加的真实旧词汇、普通重复名称升级测试。 |
| 临时五包 `TestReviewR2*`，`-race -count=1 -v` | 全部通过观察断言，未报告 race：48 心跳组合、URL 后缀、fallback fail 只 ping 一次、验证 INSERT 故障回滚、异步成功时间戳、事件 Headers、结果 UPDATE 故障及租约恢复、405 条推进与重复扫描、真实 API 拒绝 `-`、remote 错误仍输出零、真实 goose 重命名碰撞。这里“通过”包括确认缺陷存在的观察断言，不表示这些异常符合需求。 |
| 目标 0014 SQL 最小 fixture 的 Up→Down→Up | 复现 `backup_backup_expired`；这不是完整 goose 回退演练，正常升级及名称碰撞另用真实 goose 测试验证。 |
| 前端 `npm run build`、`npm run lint` | 均通过；未执行浏览器 A→B 交互。 |

临时探针已从仓库删除，仅保留评审报告。复核用探针副本位于本机 `/tmp/codex-phase7r2-probes/`；运行输出为 `/tmp/codex-phase7r2-probes.log`、`/tmp/codex-phase7r2-migration.log`，不是仓库发布依赖。探针使用真实迁移库、SQLite ABORT trigger、自定义 RoundTripper 和 httptest.NewRecorder，不使用 httptest.NewServer。

未运行全量依赖监听的网络/race 测试；没有将本环境无法监听记为产品测试失败，也没有声称 race 全量通过。仍需可监听环境中的网络重定向/超时/429、真实进程崩溃重放、外部死人开关停机告警以及浏览器交互验收。

## 最关键的 3 件事

1. **闭合心跳配置和验收契约。** 让 API 真正接受 `-`，修正 period=0 文档，明确批准或补齐 start 的交付范围；保留已修好的年龄门控和拨号边界。
2. **把升级与回退做完整。** 解决生成名称与现有名称碰撞；让订阅转换精确且可重入，修正 Down 的失效行为/说明；若存在已应用旧 0014 环境，用新前向迁移修复。
3. **补运行期故障收敛与观测真实性。** 对验证终态事务做有界重试，完整处理 metrics 的 Query/Scan/rows.Err/文件遍历错误并省略无效值；将本轮无监听故障探针变成长期回归测试，再补真实网络与停机验收证据。
