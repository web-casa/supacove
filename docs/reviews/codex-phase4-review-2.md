# Phase 4 收尾 + Phase 7 第二轮评审

评审日期：2026-10-04。评审 HEAD：`3e9341d20814adf98d7ed37300b2d0a4a4cc1e21`。

## 范围与结论摘要

**当前实现不能验收为 Phase 4/7 完成：P0 0 项、P1 5 项、P2 5 项。** `/metrics` 仍匿名可访问，业务指标输出违反 Prometheus 文本规范，心跳会泄露 URL 并掩盖没有足够新远端备份的情况，通知没有 outbox 保证。

范围说明：本地 `HEAD == 3e9341d`，因此严格的 `git log 3e9341d..HEAD` 为空。本报告将用户点名的 `3e9341d` 提交纳入评审，并沿 `41d3c91..HEAD` 核查相关 Phase 3/4/7 调用链；不会声称存在基于 `3e9341d` 的后续修复。对照 `docs/dev-plan.md` 的 Phase 4、Phase 7 和 §0.5 A/C/D/E，参考上一轮报告，区分新增问题与未完成要求。只写评审文件，未修改生产实现或仓库测试。

## P0

未发现足够证据支持本轮新增 P0。认证旁路涉及运维及业务汇总信息，没有证据表明该端点直接泄露数据库凭据或备份内容，按 P1 处理。

## P1

### P1-01：移入 guard 分组仍未保护 `/metrics`

- **位置：** `backend/internal/server/server.go:204`、`:288`。
- **原因：** 路由确实执行 `s.guard`，但 guard 开头对所有非 `/api` 前缀直接调用下游并返回。`/metrics` 因此不会执行 session 验证，也不会设置 guard 中的 `Cache-Control: no-store`。提交说明和指标注释中的“已认证”与实际行为不符。
- **复现：** 临时 `TestReview47Metrics` 通过真实 `Server.Router()` 和内存 Recorder 请求 `/metrics`，匿名及 `sb_session=invalid` 均得到 HTTP 200 和业务指标。
- **影响：** 公网部署暴露任务状态、数据库/目的地数量及资源信息；匿名调用还可以反复触发 SQL 聚合。违反 Phase 4 任务 5 和默认拒绝原则。
- **修复：** 将身份验证从 API 专用内容校验中拆开，明确覆盖 metrics；或显式将该路径纳入认证范围。验证必须经过 Router，覆盖匿名、非法、过期、撤销 session 拒绝与有效凭据成功，不能只直接调用 handler。

### P1-02：同一指标重复输出 HELP/TYPE，导致抓取格式无效

- **位置：** `backend/internal/server/metrics.go:32`。
- **原因：** `supabackup_jobs_total` 的 HELP 和 TYPE 放在六状态循环内部，每次响应重复六次，后五次 TYPE 出现在该指标已有样本之后。
- **依据：** Prometheus 0.0.4 规范要求每个指标至多一个 HELP、一个 TYPE，TYPE 必须位于首个样本之前。[官方文本格式规范](https://prometheus.io/docs/instrumenting/exposition_formats/)。
- **复现：** `TestReview47Metrics` 统计实际响应，确认各出现六次。这是确定的格式违规；本轮没有运行官方解析器或真实 Prometheus 抓取，不将结构检查冒充解析器验收。
- **影响：** 规范解析器可拒绝整个响应，进程指标也无法随该次 scrape 入库；空 jobs 表同样触发问题。
- **修复：** HELP/TYPE 移到循环之前。固定六个 status label 是低基数且安全的；当前值是状态库存，应作为 gauge，可改名为 `supabackup_jobs`，不要仅因 `_total` 后缀把它改成 counter。加入解析器验收。

### P1-03：心跳没有按库与快照新鲜度门控，本地成功也能发送“成功”信号

- **位置：** `backend/internal/jobs/jobs.go:775`、`:830`；`backend/internal/jobs/upload.go:35`；`backend/internal/jobs/heartbeat.go:17`；`backend/cmd/supabackup/main.go:212`。
- **原因：** Runner 只有一个全局 heartbeat URL，任意库 `succeeded` 后无条件 ping。`uploadAndCommitRemote` 对无目的地的本地模式直接返回 nil，随后同样记成功并 ping；发送前没有检查远端 committed、完整性状态、快照年龄、每库期望周期/grace，也没有 start/fail/success 区分。
- **触发与影响：** A 库持续成功可掩盖 B 库从未成功；仅本地备份可重置外部监控；长时间排队/上传的旧快照在完成时重新 ping，掩盖超期。违背 Phase 7 任务 2。恢复上传路径 `ResumeRemotePhase` 在 `jobs.go:360` 记成功后也没有该通知门控/交付路径，语义不统一。
- **边界：** 正常配置了目的地的新上传会先流式校验密文、发布远端 manifest，再更新 succeeded；不是所有成功都未经完整性校验。问题是心跳没有显式使用这些条件以及快照新鲜度。
- **修复：** 每库持久化心跳配置及外部周期/grace；发送意图建立在本库合格远端提交和足够新快照上，明确时间依据及精度。恢复提交复用同一路径。至少测试两个库相互隔离、本地成功不 ping、旧密文晚提交不消除超期及失败/取消不发 success。

### P1-04：心跳 URL 进入启动日志和网络错误日志

- **位置：** `backend/cmd/supabackup/main.go:214`；`backend/internal/jobs/heartbeat.go:31`、`:36`。
- **原因：** 启动直接输出 URL 前 20 字节；短 URL 会完整暴露，userinfo 或短路径 secret 也可能落在前缀内。HTTP 出错时输出原始 err；`http.Client.Do` 包装的 `*url.Error` 包含请求 URL 的路径和 query。请求构建错误同样不应直接输出原始 URL 相关错误。
- **复现：** `TestReview47Heartbeat` 使用 mock RoundTripper 返回受控网络错误，生产日志包含路径 `BEARER_SECRET` 和 query `QUERY_SECRET`，没有访问外网或使用真实 secret。
- **影响：** 日志读取者可能拿到具备 bearer 能力的 ping 地址并伪造健康信号。URL 不入日志的要求未满足。
- **修复：** 启动只记录 enabled/配置 ID；失败只记录受控类别、状态码与安全 ID。复用 Webhook 发送器目前记录 `%T` 而不输出原始网络 err 的做法。覆盖请求构建、DNS/TLS、超时、重定向失败和带 userinfo/query/path 的 URL。

### P1-05：Phase 7 通知仍是临时轮询，没有持久 outbox，既漏报又重放

- **位置：** `backend/internal/scheduler/scheduler.go:305`、`:375`；`backend/internal/scheduler/webhook.go:16`；`backend/internal/db/migrations/0009_scheduling.sql`。
- **原因：** 每轮只查询最近五分钟的最新十条失败，再直接起 goroutine。没有与状态提交同事务的 outbox、event ID、交付状态、重试预算/退避；HTTP 429/5xx 只记日志。超期每轮重发。
- **触发与影响：** 失败落库后进程停机超过五分钟，该失败不再进入查询；短期超过十条失败时老事件可能一直被挤出窗口。窗口内同一事件每 30 秒重复发送，不能保证重放去重；接收端不可用时不存在逐事件恢复。
- **归属：** 上轮在 Phase 4 最小告警范围列为后续完善，如今明确声明 Phase 7 实现，升级为验收阻塞项；不是声称本提交新引入了该架构缺口。
- **修复：** 状态变化与 outbox insert 同事务，异步有界 worker 按事件交付，持久化下一次重试/尝试数和可查询状态，event ID 支持接收端去重；超期按转换或 cooldown 控制。不得让发送失败回滚备份。测试提交后发送前 crash、超窗重启、超过十条事件、429、超时和重复交付。

## P2

### P2-01：心跳有独立五秒超时，但同步阻塞唯一备份 worker

- **位置：** `backend/internal/jobs/heartbeat.go:27`；`backend/internal/jobs/jobs.go:830`。
- **原因：** `context.Background()` + 五秒 deadline 的确不依赖 job 的剩余时限；但 `r.pingHeartbeat()` 是直接同步调用，不是注释宣称的 fire-and-forget。网络超时期间无法推进本地/远端保留及下一个任务；停止 Runner 也不能取消该独立 context。
- **复现：** mock transport 等待受控 channel，探针确认请求拥有独立不超过五秒的 deadline，而调用在 transport 被释放前不返回。
- **边界：** 发送失败不修改已提交成功状态，这是正确的；最长五秒请求等待不等于永久卡死，响应体仅关闭、没有无界读取。
- **修复：** 用有界异步发送队列并关联服务生命周期，限制单请求超时，停止时取消/等待。避免简单无界起 goroutine。测试慢接收端不占执行槽、停止收敛以及请求失败不影响 succeeded。

### P2-02：ValidateCronExpr 没有接线，且自身仍会 panic；tick recover 粒度过大

- **位置：** `backend/internal/scheduler/scheduler.go:92`、`:109`、`:124`、`:150`、`:205`。
- **原因：** 仓库检索只有 `ValidateCronExpr` 定义，无生产调用者；不存在提交说明所称“配置写入时验证”。底层 cron v3.0.1 parser 对没有空格的 `CRON_TZ=UTC` 使用负索引切片，validator 本身也 panic。`TestReview47CronValidatorPanic` 已复现。
- **panic guard 评价：** 外层及每 tick recover 已接线，可防该 tick 的 panic 杀死进程，并让后续 tick 继续；但坏行每轮都会再 panic，跳过该行之后的所有库和末尾 `checkFailures`。不能称为配置隔离或调度正常恢复。外层 recover 后函数结束，也不负责重新启动 loop。
- **修复：** 在解析前拒绝缺字段/非法时区前缀，写入入口真正调用非 panic validator，校验独立 IANA 时区及前缀冲突；历史脏数据逐库隔离，不能中断整轮；补坏库后还有好库、连续 tick 与失败通知继续执行的测试。

### P2-03：LoadWebhooks 查询已接线，但失败静默且配置仍无可用管理入口

- **位置：** `backend/cmd/supabackup/main.go:230`；`backend/internal/scheduler/scheduler.go:54`；`api/openapi.yaml`；`backend/internal/server/api_phase2.go`。
- **确认修复：** LoadWebhooks 在 scheduler.Start 之前加载数据库存量行并 SetWebhooks；返回 rows.Err，排除 deleted_at 非空记录。`TestReview47LoadAndRollback` 验证过滤正确，上轮“完全未接线”已解决。
- **剩余问题：** main 的 if 丢弃 `werr`，查询失败会无日志地带空订阅启动。没有调度/Webhook 配置 API 或受支持 CLI，没有 cron 字段的配置写入者；新安装仍无法通过产品入口启用自动调度和通知。运行期只加载一次，也没有显式 reload 机制。
- **修复：** 配置加载失败应明确报错或暴露退化状态；提供持久配置入口及验证、测试通知功能，定义更新/软删除何时生效。配置入口属于前轮遗留，启动时静默丢错是本次接线问题。

### P2-04：业务指标吞 SQL 错误并把任务完成时间当成备份新鲜度

- **位置：** `backend/internal/server/metrics.go:35`、`:43`、`:53`。
- **原因：** 所有 Scan 错误被丢弃，失败后继续输出零库存且 HTTP 200；QueryRow 没有 request context。最后成功指标取全局 `MAX(finished_at)`，不同库及本地/远端成功混在一起，晚上传旧快照使 age 变小。
- **复现：** 探针关闭 DB 后调用 handler，仍获得 HTTP 200 和数据库数零；这是查询失败伪装成有效观测值。晚上传口径问题由 SQL 和调用链静态确认。
- **计划缺口：** Phase 7 任务 5 的远端提交成功率、验证状态分布、上次成功时间戳及暂存占用未提供；新增注册数不能替代这些指标。现有三个进程 gauge 也不是 CPU/RSS 等完整进程采集，但计划没有要求完整 collector，不另行报错。
- **修复：** 查询使用 context，聚合读取失败要可见，不发送假零；可返回失败或显式 collector error 并避免错误业务样本。定义任务/远端提交/验证的分段口径，输出合格快照时间戳及暂存占用，无成功状态明确表达。低基数汇总应能反映超期库数量，单个成功库不能遮蔽其他库。

### P2-05：SB_HEARTBEAT_URL 未验证，心跳/Webhook 默认跟随任意重定向

- **位置：** `backend/cmd/supabackup/main.go:212`；`backend/internal/config/config.go:65`；`backend/internal/jobs/heartbeat.go:34`；`backend/internal/scheduler/webhook.go:32`。
- **原因：** URL 绕过 config.Load 原样传给 Runner，未在启动时校验绝对 HTTP(S)、host、userinfo 等；错误 scheme/相对地址直到备份完成才报错。两个发送器都使用 http.DefaultClient，无解析/连接地址或逐跳重定向政策，可转向 metadata/link-local，或 HTTPS 降为 HTTP；HTTP 状态仅 `>=400` 判失败，没有限定 2xx。
- **边界：** env 和数据库配置由管理员控制，不是已证明的匿名 SSRF 或 metadata 凭据窃取。SSRF 完整边界在计划 Phase 5，故此处列 P2；对外接线时应同步落实。已有请求 deadline、未读取响应正文，不能误报为缺少所有超时或无界读正文。
- **修复：** 将 heartbeat URL 纳入集中配置校验，错误提示不回显 secret；采用专用受控 client，明确 HTTP(S)、禁用或逐跳复核重定向、拒绝 metadata/link-local，内网需求显式放行。只接受 2xx 为交付成功。

## 前轮修复与 §0.5/Phase 4 遗留核查

下表为已有事项跟踪，未重复计入上面的 5 P1 / 5 P2；它们仍影响“Phase 4 收尾”的完整验收。

| 项目 | 本轮判断 |
|---|---|
| 入队和游标原子提交 | `scheduler.enqueue` 已使用同一事务，并通过 EnqueueTx 插入。临时 trigger 注入 INSERT 失败，cursor 保持零；上轮非原子问题已修。冲突也回滚 cursor，日志却仍称 cursor advanced，应纠正描述。 |
| 失败时间窗口 | cutoff 改为 Go Unix 时间参数，修复之前 SQLite 无效 modifier。最新十条和五分钟窗口的可靠性问题见 P1-05。 |
| Webhook URL 错误日志 | `postWebhook` 不再输出 URL 或原始网络错误，上轮该项已修；心跳重新引入同类缺陷。`redactNotify` 只替换 postgres scheme、不去掉后面的 userinfo，不能为任意存量错误文本提供完整二次脱敏。 |
| 持久调度身份和配置快照 | jobs 插入仅持有 database ID、随机 backup UUID 和当前入队时间，没有 schedule revision + scheduled UTC 唯一身份，源/recipient/目的地在运行时读取。内存目的地快照保护同次 dump/upload，但不满足入队时持久配置快照。暂停/改 cron/删除的 pending 处理、DST/补跑时间语义未完成。 |
| 公平与资源治理 | 单库 active 唯一约束存在，但 worker 仍单槽同步运行、jobCtx 仅 WithCancel，远端重试等待不释放槽，全局 pending 预算与过载可见性不足。不能把早读目的地或取消检查描述为完成 P1-08。 |
| 新鲜度时间 | lastSuccessAge 使用 worker started_at，并非可观测真实导出快照；manifest 已诚实说明导出开始不等于服务端快照。Phase 7 健康检查、首页四类状态及外部关机实测/文档尚不能验收。 |
| 协议 C | 新上传确有意图 → 密文流式读回校验 → manifest 发布 → 状态更新，不能将心跳门控缺失误写成正常上传省略完整性校验。 |
| 协议 D | `upload.go:219` 仍按 newest committed 选锚点，未显式核验 remote_verified/验证域；删除未先落 deleting 状态，第二对象失败仍留 committed。远端删除连带清理本地，unlink 失败后仍可清引用，活动下载租约保护不足。前轮已报告，本轮静态确认未修；详见第一轮报告及 Phase 3 报告，不宣称本轮重新运行这些故障注入。 |

## 配置校验确认

`config.Load` 对 `SB_LOCAL_KEEP` 默认 5、要求正整数；`SB_STAGING_QUOTA_BYTES` 默认 0、要求非负 int64，非法文本/负数/溢出会返回错误，且 main 确实调用 SetLocalKeep/SetQuota。临时 `TestReviewLoadRetentionValidation` 的十组边界全部通过。这两项没有发现新回归。

配额注释定义的是**每次密文写入上限**，不是所有 staging 文件累计预算。不能把接受 quota=0 视为 bug，也不能宣称总暂存占用已有硬上限。`SB_HEARTBEAT_URL` 没有进入 Config，是本轮新增校验缺口，见 P2-05。

## 验证记录与测试缺口

运行工具链：Go 1.26.0 linux/arm64。缓存放 `/tmp`，遵守当前文件系统权限。

| 验证 | 结果与限度 |
|---|---|
| `GOCACHE=/tmp/codex-phase47-go-cache go build ./backend/...` | 退出 0，通过；模块只读 stat-cache 出现写入警告，没有影响构建结果。 |
| `GOCACHE=/tmp/codex-phase47-go-cache go test -race -count=1 -json ./backend/...` | 退出 1。11 个有测试包通过；server 的 TestPhase2DefaultDenyAndAgeStatus 因 httptest listener 被环境禁止而 panic，不能报告全量 race 通过。 |
| PG/MinIO 集成 | 5 项 jobs PG 测试与 TestMinIOEndToEnd 共 6 项因容器不可用跳过，未完成真实数据库/对象存储验收。 |
| 临时 overlay 定向 race 探针 | 四个包、5 个顶层测试通过，包含配置的十组子测试。覆盖真实 Router 匿名/非法 cookie、重复 HELP/TYPE、关闭 DB、heartbeat deadline/同步等待/URL 泄漏、validator panic、Webhook 软删除和入队 rollback。缺陷探针 PASS 表示缺陷已复现，不表示生产代码合格。 |
| `bash -n` | scripts 下两个 shell 文件通过，仅语法检查。 |
| Python `yaml.safe_load` | compose.yaml、api/openapi.yaml、api/cfg.yaml、.github/workflows/ci.yml 全部通过，仅语法可解析。 |

证据：`/tmp/codex-phase47-race.jsonl`、`/tmp/codex-phase47-race.stderr`、`/tmp/codex-phase47-build.log`；探针源文件、overlay 映射及结果在 `/tmp/codex-phase47-probes/`。未向仓库注入临时测试；没有调用真实心跳/Webhook 服务。

仓库 scheduler 包没有测试；没有 heartbeat/metrics 专项回归，config 现有测试主要覆盖 secret 文件。至少补齐以下验收：

1. **认证与指标：** 经 Router 覆盖匿名/无效/过期/撤销/有效 session；用官方生态解析器校验完整指标响应；DB 错误不伪造零，旧快照晚提交不重置保护状态，label 集合有界。
2. **死人开关：** 本地成功、远端未验证、过期快照、两库相互遮蔽、恢复提交、start/fail/success；独立超时、失败不改成功状态、发送不占 worker、停止收敛、所有日志无 URL secret。外部关机触发需真实服务与文档证据。
3. **调度：** validator 非 panic 与真实写入接线，非法表达式/无可达日期/错误时区；单库 panic 不影响后续库或失败扫描；LoadWebhooks 查询失败、软删除、重启加载和配置变更策略。
4. **通知：** outbox 与状态原子性、commit 后 crash、停机超过五分钟、超过十条失败、429/5xx/超时、持久退避与 event ID 去重、有界发送；通知错误消息及出站重定向边界。
5. **Phase 4/§0.5：** 入队提交前后 crash、冲突回滚、跨周期补跑/DST/时钟跳变、暂停/改计划、持久配置快照；长任务加三库竞争、重试释放槽与 pending 预算；保护锚点、部分删除恢复、unlink 失败保留引用、下载租约。

## 总体结论与最关键 3 件事

**不建议将该 HEAD 标记为 Phase 4 收尾或 Phase 7 完成。** 原子调度入队、失败 SQL 窗口、Webhook 启动加载和 tick recover 有实际改进，但 metrics 认证修复未生效，新增业务指标不可按规范抓取，死人开关尚不能表达每库真实保护状态；通知持久交付及 Phase 4 核心验收仍缺失。

1. **先修监控入口：** 消除 metrics 的非 `/api` 放行，修复重复 HELP/TYPE 与查询失败假零，加入真实路由认证和解析器回归。
2. **让心跳代表真实的新备份且不泄密：** 每库配置，远端提交 + 完整性 + 快照年龄共同门控，统一恢复路径；删除 URL 前缀/原始错误日志，以有界异步队列发送。
3. **补持久通知与调度验收：** 状态同事务 outbox、可恢复重试和 event ID；接通配置验证并隔离坏计划，完成配置快照、资源治理及 §0.5 保留保护测试后再声明完成。
