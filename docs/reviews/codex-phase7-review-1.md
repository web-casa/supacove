# Phase 7 第一轮评审

评审对象：`3161998..6e3e8ff`，实际 HEAD 为 `6e3e8ffcf391bb6ee8daac7e43167cef67f58d54`。日期：2026-10-05。

**总体结论：不通过 Phase 7 完成验收。发现 P0 0 项、P1 6 项、P2 6 项。** 原子 claim、基本状态机和接口接入已有有效实现，但通知可靠性、心跳安全/语义、旧配置升级以及跨库表单隔离仍有缺口。未发现本次直接破坏备份密文、远端提交或保留锚点协议的证据。

口径：对照 `docs/dev-plan.md:214` 的 Phase 7 任务与 DoD、协议 A–E，沿用历轮“生产调用链必须接通”“正常路径测试不替代故障边界”“历史迁移与 Down 分别判定”的原则；特别参考 `codex-phase4-review-1.md` 的 webhook 协议核查、`codex-phase5-review-3.md` 的事务/收敛与迁移口径、`codex-phase8-review-1.md` 的指标兼容性口径。以下路径均相对仓库根，行号为目标提交。

## P1 发现

### P1-01：心跳配置允许普通域名，但实际请求绕过拨号期 SSRF 防护

证据：`backend/internal/jobs/jobs.go:69`；`backend/internal/jobs/heartbeat.go:101`、`:106`；`backend/internal/server/api_phase7.go:24`、`:137`。

`heartbeatClient` 是普通 `http.Client`，没有 `DeliveryClient` 的 link-local 拦截。配置时只拒绝 link-local IP 字面量和一个 metadata 主机名，普通域名解析到 `169.254.169.254`、`fe80::/10`，或合法接收端返回指向 metadata 的重定向，都会在运行时经过默认 transport。新增数据库心跳 API 因此并未落实其宣称的 metadata 边界。此处是管理员可配置的 SSRF，不扩大为未认证攻击。

建议：心跳复用受限 transport，并验证最终拨号地址及每次重定向；全局回退 URL 也走同一边界。用可注入 resolver/dialer/transport 覆盖 DNS 与重定向，不以“连接失败”代替“策略明确拒绝”的断言。

### P1-02：历史 webhook 订阅词未迁移，升级后告警被静默标记为已投递

证据：`backend/internal/outbox/outbox.go:277`、`:377`；`backend/internal/db/migrations/0014_outbox_heartbeat.sql:21`；历史 schema `backend/internal/db/migrations/0009_scheduling.sql:15`。

旧订阅值是 `failure,expired`，新投递器只精确匹配 `backup_failed,backup_expired,verification_failed`。0014 没有转换已有值，旧 DEFAULT 也保留。升级后原有接收端不再匹配；零 targets 路径会把事件标为 `delivered`，管理员甚至看不到投递失败。API List 还会把旧词强转为不在新 OpenAPI enum 内的值。

探针：真实迁移库插入 `events='failure,expired'`，调用 `targets(..., EventBackupFailed)`，返回 **0 个 target**。

建议：前向迁移旧 CSV 词汇并保留用户订阅选择；提供过渡兼容读取，处理旧默认值。增加从旧 schema/旧 webhook 行升级后收到失败和过期通知的测试。

### P1-03：状态提交与 outbox 并非完整原子，存在永久漏通知窗口

证据：`backend/internal/jobs/jobs.go:1021`、`:1034`、`:1039`；`backend/internal/jobs/verify.go:404`、`:415`、`:462`；`backend/internal/scheduler/scheduler.go:317`。

有两个具体路径：

- `jobs.fail` 的 INSERT 出错后仍提交状态事务。SQLite 的语句级 ABORT 不一定回滚整个事务；探针用 `BEFORE INSERT ... RAISE(ABORT)` 注入 outbox 写失败，得到 **job=failed、outbox=0**。若随后停机超过 5 分钟，或失败量超过兜底扫描的最新 10 条，`checkFailures` 不会补齐。这不是“网络投递失败不回滚备份”的实现，而是丢掉通知持久化义务。
- 验证先用独立 Exec 写终态，再单独 `outbox.Enqueue`。两者之间 crash 将留下终态失败但没有通知；启动恢复针对 transitional 验证状态，scheduler 也不扫验证失败。反向地，`finishVerification` 写库持续失败仍无返回值，调用方可以发出尚未成功持久化的失败通知。

建议：验证终态与通知共用一个短事务并返回提交结果。备份失败记录优先保留的策略可以接受，但 outbox 写失败时必须有持久、无 5 分钟/10 条窗口限制的补偿义务，不能只日志后遗忘。增加 INSERT 失败、状态提交后立即 crash、恢复超过兜底窗口的测试。

### P1-04：`pingFail` 只有测试调用，实际备份失败不会发送 `/fail`

证据：`backend/internal/jobs/heartbeat.go:85`；`backend/internal/jobs/jobs.go:990`；`backend/internal/jobs/phase7_test.go:93`。

全仓调用检索显示 `pingFail` 除定义外只有测试调用。`jobs.fail` 提交后仅记录日志，没有触发它；生产代码只有成功 ping。于是现有测试证明了辅助函数能发送，不能证明“备份失败 → fail ping”链路成立。计划要求的 start 协议也没有生产实现。

建议：在确认失败状态已提交后接通一次失败 ping，明确取消、重复失败调用与 start 的协议语义。测试必须通过真实失败路径触发，不直接调用 `pingFail`。追加 suffix 时用 URL 路径操作，避免 `?token=...` URL 被拼成查询值中的 `/fail`。

### P1-05：全局回退在默认 period=0 时绕过快照年龄门控，也无法按库明确关闭

证据：`backend/internal/jobs/heartbeat.go:41`、`:61`；`backend/internal/server/api_phase7.go:162`；`backend/internal/db/migrations/0014_outbox_heartbeat.sql:25`；`api/openapi.yaml:909`。

回退 SQL 本身正确，但迁移后所有库默认 period=0；API 仅在库自身 URL 非空时要求 period，因此启用 `SB_HEARTBEAT_URL` 的常见升级路径没有年龄门控。探针对 1000 小时前的已远端提交快照返回 `skip=""`，会发送成功 ping，违反“旧密文晚上传不得消除超期”。现有测试甚至将无 period 放行固定为预期。

此外，契约说 `heartbeatUrl` 空值表示 disabled，实际空值会启用全局 URL。多个库共用回退端点时，一个正常库还可能持续替失败库消除 silence；不能把该模式宣称为每库独立死人开关。

建议：区分继承/禁用/显式 URL；对实际生效的回退配置也要求可用的 period/grace，未配置时拒绝成功背书或明确降级为非保护性信号。补充回退配置的文档与测试。

### P1-06：切换数据库复用 ScheduleForm，上一库草稿会写进下一库

证据：`frontend/src/App.tsx:235`、`:308`、`:314`、`:317`。

`ScheduleForm` 渲染时没有 `key={scheduleFor}`，其 `form` state 也不随 `databaseId` 清空。操作序列：打开 A 的调度，改 cron/heartbeat/paused 但不保存，直接点 B 的 Schedule & heartbeat，然后保存。React 保留同一位置的组件 state，mutation 使用 B 的 ID，却优先读取 A 的草稿值。可能误暂停 B，或使 B 给 A 的外部监控端点发成功 ping。

建议：按数据库 ID 隔离组件和草稿，加载失败时禁用保存；成功后更新/失效该库 `['schedule', id]` 缓存。增加 A→B 切换及 GET 失败时不可误覆盖配置的交互测试。本项是静态调用链确认，未声称已做浏览器端复现。

## P2 发现

### P2-01：真实投递丢失原事件 Header，且接收端拿不到 outbox event ID

证据：`backend/internal/outbox/outbox.go:237`、`:310`、`:314`；`backend/internal/server/api_phase7.go:361`；基线已删除的 `backend/internal/scheduler/webhook.go:32`。

原请求携带 `X-Supabackup-Event`，新真实投递仅有 Content-Type；测试投递却仍携带该事件 Header。依赖 Header 路由的既有接收端会退化，测试按钮成功不能代表真实事件兼容。`entry.eventID` 虽从数据库读取，却没有进入 Header 或 JSON。接收方因此无法按承诺的 event ID 对重试/崩溃重放去重；过期通知仅有数据库和年龄，不能恢复精确的 4 小时桶 ID。

探针捕获真实 `post` 请求：事件 Header 为空，body 只有调用方 payload，没有 event ID。

建议：统一构造测试/真实请求，恢复事件 Header，并在稳定契约字段或 Header 中发送 event ID，重试保持不变。补请求字节级断言。

### P2-02：投递结果写库短暂失败后永久停在 delivering，运行期不再收敛

证据：`backend/internal/outbox/outbox.go:198`、`:244`、`:328`、`:338`、`:440`。

成功/retry/dead 写库失败都只记录日志；claim 只读 pending，`recoverStuck` 只在 Start 执行。探针令 delivered UPDATE 临时 ABORT，移除故障后再执行投递 pass，状态仍为 **delivering**。失败结果落库遭遇同类故障时不会继续重试，也不会到达 dead。pending/dead 指标均不计 delivering，会掩盖积压。

建议：有界重试结果写入，并提供当前 owner 可安全执行的运行期恢复/租约；不要直接周期性重置所有 delivering。补 delivering 数量/年龄可观测性及状态写入故障测试。

### P2-03：last_heartbeat_at 在请求成功前更新，错误请求也显示已心跳

证据：`backend/internal/jobs/heartbeat.go:98`、`:118`；`backend/internal/server/api_phase7.go:68`。

时间戳写在 goroutine 外，无论 URL 构造失败、网络失败、非成功响应，均立即更新。探针传入不可构造的 URL，`last_heartbeat_at` 仍变为当前时间。将 UPDATE 放在 goroutine 外避免不了语义错误，也使备份调用同步等待 SQLite 写入。

建议：明确区分 last attempt 与 last successful heartbeat；若现字段代表成功心跳，只在确认成功响应后更新，失败信号单独记录。异步写入也要纳入 Runner 生命周期，不能只为测试易断言而提前写。

### P2-04：0014 对历史同名 live webhook 无升级策略

证据：`backend/internal/db/migrations/0014_outbox_heartbeat.sql:22`；`backend/internal/db/migrations/0009_scheduling.sql:11`。

旧表没有名称唯一约束，两个同名 live 行是合法历史状态。0014 直接建 unique index 会失败，导致升级启动被阻止。最小 SQLite fixture 已复现 `UNIQUE constraint failed: webhooks.name`。

**同名软删行不是问题**：一条 live + 一条同名软删行的 Up/Down 探针通过，部分索引正好排除了软删行。应处理的是多条 live 数据，不能为“去重”删除接收端。

建议：保留全部接收端的确定性重命名/显式冲突修复方案，并做带旧重复行的升级测试及迁移失败恢复验证。

### P2-05：已有 metrics 序列被无过渡删除，升级后原告警规则失效

证据：`backend/internal/server/metrics.go:34`。对照基线同文件中的 `supabackup_jobs_succeeded`、`supabackup_jobs_failed`。

本次改为 `supabackup_jobs_total{status=...}`，两个原有序列不再输出。已有 Prometheus 规则/仪表盘会变为空序列；本次是增强指标，没有兼容别名、弃用窗口或迁移说明。历轮对指标更名也采用同一口径。

建议：保留旧 gauge 别名并发布迁移说明；新增指标注明其为当前数据库状态分布而非单调 counter。名称带 `_total` 但类型为 gauge 不直接判为格式错误，不能据此套用 counter 的 rate 语义。

### P2-06：metrics 查询错误变成零值/部分成功，故障时会给出误导读数

证据：`backend/internal/server/metrics.go:54`、`:105`、`:139`、`:167`。

远端统计直接忽略 Scan error，失败时仍输出 committed=0、upload failures=0；分组查询忽略 rows.Err，Scan 失败跳行；staging walk 无法读目录时输出 0。HTTP 仍为 200，观测端无法区分空系统与控制盘/查询故障。统计故障恰恰是 Phase 7 需要显式暴露的状态。

建议：收集后再输出；关键查询失败返回 scrape 错误，或提供明确的 collection error 指标并省略无效值。staging 若保留 best-effort 语义，至少暴露采集失败/时间，不能把未知标作真实 0。

## 重点核查结果与不升级为缺陷的事项

| 核查点 | 判定与边界 |
| --- | --- |
| claim 原子性 | `outbox.go:237` 在事务内以单条 UPDATE 子查询选择 pending，SQLite 写事务序列化，提交后才发送。无需以外层再加一次 pending 条件来“修复”同一原子语句。8 个并发 claim 探针只有 1 次领取；没有双进程实测，不把此探针夸大为多实例支持。 |
| 两进程与 recoverStuck | 若绕过应用 advisory lock，第二个 Notifier.Start 可重置第一实例的 delivering，再次领取后旧 owner 能写新 owner 的状态。现有产品协议是单实例本地卷，入口有 OS 锁，因此不作为支持范围内的 P1；若未来多 worker，需 owner/lease fencing，现实现不能直接复用。 |
| retry/dead | 正常单 worker 路径的状态条件有效；30、60、120、240 秒后第 5 次失败 dead，1h 是数学上限，并不意味着五次中实际会等到 1h。批量发给多个接收端时成功端会随失败端一起重试，符合至少一次语义，前提是补 P2-01 的接收方去重键。 |
| Stop | 等待当前 pass，无数据 race 的直接证据；它不主动取消 pass，最多继续领取至 10 条，每条串行发送所有 webhook。独立 Stop 的耗时可达约 10×接收端数×10s，并非单次 10s；主程序关闭时共享 ctx 的取消有助退出。应测试慢接收端/取消，不能宣称已证明有界快速停机。 |
| jobs.fail 回滚 | UPDATE error 和 RowsAffected=0 都显式 Rollback；未见这两条路径泄漏事务。Begin 失败仍落到“backup failed”日志但未持久化，应连同 P1-03 改为明确失败传播/补偿。 |
| webhook SSRF | `DeliveryClient` 的 Control 拿到实际拨号 IP，SplitHostPort 错误直接拒绝，IPv4/IPv6 link-local 检查有效；不是“对域名二次解析造成重绑定窗口”。默认重定向继续用同一 transport，新连接仍经过检查；loopback/private 放行符合既有自托管范围。带 IPv6 zone 的 LookupIP 失败偏向拒绝，可能影响可用性但不是已证明的绕过。 |
| 配置验证细节 | http/https、cron、时区和数值范围基本有检查；`http://:80` 等空 Hostname 以及无效端口的完整可拨号性并未穷尽。不是任意 URL 都已验证成功。未在实际网络上验证 metadata/DNS 重绑定。 |
| TestWebhook 与真实 client | 二者使用同一个 `DeliveryClient` 工厂，超时相同，4 KiB 限量读取有效；不是同一实例/同一请求构造，Header 差异见 P2-01。测试 JSON 用 `%q` 而非 json.Marshal，对带控制字符的名称还需补合法 JSON 用例。 |
| heartbeat 成功等级 | `jobs.go:904` 的常量 true 出现在 `uploadAndCommitRemote` 成功返回且成功状态写入之后；有目的地时远端校验/提交先完成，nil destination 是现有明确 local-only 分支，未发现据此误把上传失败报成功。计划字面要求“至少远端”，local-only 例外须写入产品文档，不能在报告中替用户批准修改 DoD。 |
| overview NULL/Sscanf | 无子查询行返回 NULL，使用 NullString 接住；started_at 是 INTEGER epoch，Sscanf 当前能正确解析，空库/从未成功测试通过。没有证据支持“Scan NULL 导致 overview 全部 500”。可简化为 NullInt64 避免宽松解析。 |
| overview 状态语义 | 只由最后 succeeded 计算 fresh/expired/never，最后任务 failed 另列，验证状态正交，符合本次约定。实现沿用按 job ID 排序；常规单库串行 enqueue 下没有新证据证明顺序颠倒。没有提供独立验证年龄；前端对空 verify 状态不显示“未验证”。 |
| dueFn 退化 | `queries_schedule.go:232` 固定传 0 且未读取时区，导致非暂停有效 cron 的 ScheduleDue 几乎总为 true。但该字段未映射到 API，也不用于真正调度，列为内部缺口，不夸大为调度已被破坏。 |
| 时间语义 | overview/metrics 用 started_at，心跳用 dumpStart；`jobs.go:589` 与 `:754` 之间有预检阶段，前者会保守高估快照年龄。这继承了旧调度口径，应统一真实快照字段，但不会因该差异让旧数据变新。 |
| N+1/采集成本 | overview 是一条带三个相关子查询的 SQL，不是 Go 层 N+1 往返；每库查最近状态仍随历史量增长。scheduler 的 per-db lastSuccessAge 属既有 N+1。每次 scrape 完整遍历 staging 并查询 jobs/outbox，全量历史没有本次清理上限；宜缓存/后台采样，但未做容量压测，不能给出已证实的延迟数字。 |
| label 基数 | status/state 是有限枚举；database name 是 O(注册库数)，不存在 job ID/object key 标签爆炸，但没有硬性库数量上限，称“绝对低基数”不成立。数据库名重建产生时间序列 churn，需给部署规模预算。 |
| CSRF/cookie 与下载 | 新变更继续走 same-origin credentials 与 cookie 派生 CSRF header；同源 kit/artifact GET 链接会携带会话 cookie，正确且无需把 CSRF 放进 URL。Task.remoteState 已从后端映射到生成契约。预签名需先经会话认证获取，之后是短期 bearer URL，符合既有协议。未发现新认证绕过。 |
| 前端结果展示 | “last job FAILED — retry pending”由 failed 状态直接推导，不能证明已有重试排队；立即备份/删除失败未显示 mutation error，预签名 Promise 失败也无反馈，异步 window.open 可能被浏览器拦截。应补真实状态及错误展示；未将浏览器差异声称为已实测必现。 |

## 0014 迁移判定

`git log --all -- backend/internal/db/migrations/0014_outbox_heartbeat.sql` 仅发现目标提交新增此文件，基线不存在。**在本轮尚未发布、未有需要保留的已应用 0014 环境的前提下，原地修改可接受**，不能仅因本次编辑过新增文件就判违反历史迁移约定。但“不曾提交”不等于“不曾应用”；若有持久环境已应用旧版 0014，必须新增前向修复迁移，不能依靠 goose 重跑同版本。

0014 Down 仅删自己新增的四列、索引和 outbox 表；与上一轮 0013 删除前序拥有列的问题不同，没有发现它误删旧 schema 列。SQLite 最小 fixture 的 Up→Down 已通过，同名软删行保留；正常回退丢弃本 Phase 通知/心跳数据是显式 schema 撤销。此探针不替代 goose 全升级/备份恢复演练；历史 live 同名冲突见 P2-04，历史订阅转换见 P1-02。

## 验证记录

| 检查 | 实际结果 |
| --- | --- |
| `GOCACHE=/tmp/codex-phase7-cache go build ./backend/...` | 通过。工具链打印只读模块 stat cache 写入警告，但未阻断构建。 |
| `GOCACHE=/tmp/codex-phase7-cache go vet ./backend/...` | 通过。 |
| `GOCACHE=/tmp/codex-phase7-cache go test -race ./backend/...` | 未全量通过：jobs/outbox/server 在 httptest 本地监听报 `socket: operation not permitted` 并 panic。与题述“httptest 可用”不一致，以实际执行为准；不能判为实现测试失败，也不能宣称 race 全绿。其余输出中的 db/auth/dumper/recovery/storage/verifier 等包通过。 |
| 无监听定向 race 测试 | `TestOverviewStates`、`TestScheduleConfigRoundTrip`、`TestEnqueueDedup`、`TestNoTargetsIsSuccess` 通过。 |
| 临时审查探针，真实迁移 Go store | 8 goroutine claim=1；legacy targets=0；请求缺事件 Header/ID；UPDATE ABORT 后下一 pass 仍 delivering；1000h 旧快照回退不门控；无效 URL 更新 last heartbeat；INSERT ABORT 后 failed/outbox=0。均在 `-race` 下完成，未报告 race。 |
| SQLite 0014 边界探针 | 同名 live+软删 Up/Down 通过；同名 live+live 唯一索引创建失败。使用最小相关 schema，非完整 goose 升级演练。 |
| 前端 `npm run build` / `npm run lint` | 均通过；未执行浏览器交互/E2E。 |

临时 Go 探针仅用于评审，执行后已删除，产品代码未修改。复现方法：使用现有 `testOpen`/`newPhase3Runner` 创建真实迁移库；以 SQLite BEFORE INSERT/UPDATE trigger `RAISE(ABORT, 'probe')` 注入单语句失败；以自定义 RoundTripper 捕获请求而不监听网络。上述测试“通过”指观察结果被确认，不是这些异常行为符合需求。

尚未提供/完成的验收证据：真实外部死人开关关机告警与配置文档；start/fail/success 完整链路；429/超时的精确请求与重试断言、真实 crash 后恢复；跨库表单交互；大历史数据 scrape 成本。周期健康检查任务也未由本 diff 展示完整低频+jitter 链路，不以本次局部功能覆盖声称全部 Phase 7 已交付。

## 最关键的 3 件事

1. **让通知确实不丢且升级仍可达**：修复状态/outbox 原子性或持久补偿、旧订阅转换、投递状态写失败收敛，并把稳定 event ID 发给接收端。
2. **让死人开关只为应当背书的备份发信号**：统一拨号防护，接通生产 fail/start，关闭回退 period=0 的年龄门控漏洞，区分禁用/继承/成功与尝试时间。
3. **补可操作的发布证据**：隔离跨库表单草稿，保留指标兼容与错误可见性，在可监听环境重跑 race/网络测试，并补历史升级和真实外部关机演练。
