# Phase 2 备份执行内核复审（第 2 轮）

评审日期：2026-10-03。上一轮：[codex-phase2-review-1.md](codex-phase2-review-1.md)。修复范围：`git diff 13a26c1..0641b30`；实际 HEAD：`0641b30dcb21b1e9ba4c084041524bc8aa353a15`。依据仍为 `docs/dev-plan.md` 的协议 A/B/E、Phase 2 任务与 M1 DoD。本次仅评审，未修改生产实现。

**总体结论：Phase 2 代码与工程门禁均未通过。** 上轮 24 项中：**FIXED 6、PARTIALLY_FIXED 15、NOT_FIXED 2、REGRESSED 1**；仍有 **18 项上轮问题 OPEN，其中 P1 14 项、P2 4 项**。另确认软删除后名称无法复用等回归，详见后文。没有确认 P0。

判定口径：FIXED 表示该缺陷的实现已修复，不代表所有平台、真实网络和故障矩阵都已验证；PARTIALLY_FIXED 表示有有效修复但仍有具体未闭合路径；NOT_FIXED 表示核心缺陷仍在（新增未接线字段不算修复）；REGRESSED 表示原有行为进一步退化。环境阻塞单独列出，不伪装成代码缺陷，也不计为测试通过。

## 验证与证据

| 检查 | 实际结果 |
| --- | --- |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/...` | **整体 FAIL，server 包被环境阻塞**：`httptest` 创建监听 socket 时 `operation not permitted`。其他有测试的包返回通过；jobs 的 Docker 测试实际 SKIP。没有把这一轮称为全量 race 通过。 |
| `go build` / `go vet ./backend/...`（同一 GOCACHE） | 构建 CLI、vet 均退出 0。构建输出过模块 stat-cache 只读告警，但二进制生成成功。 |
| 定向 `go test -race -v ... -run '^TestReview2' -count=1` | 17 个不依赖网络的临时审计探针全部执行完成，无 race 报告。部分探针**断言缺陷存在**，其 PASS 不能解释成产品验收通过。源码与日志见下述证据目录。 |
| 既有 M1 / 恢复 / 并发 / pending 取消测试 | 单独执行 `go test -v ./backend/internal/jobs -run 'TestM1\|TestInterrupted\|TestConcurrentEnqueueExactlyOne\|TestCancelPending' -count=1`：四项全部 SKIP，原因均为 `docker run` 不可用。后两项纯 SQLite 逻辑本可脱离 Docker，已由本轮探针实际验证。 |
| Docker / sudo | `docker info`：socket permission denied；`sudo -n docker info`：no-new-privileges 禁止提权。**没有启动真实 PG，没有完成真实 PG M1 或真实 pg_dump 大 stderr 集成复现**。大 stderr 已用真实 OS 管道及受控替身进程验证。 |
| Bash | 两个 `scripts/spike*.sh`、CI 的 14 段 run shell、Spike 2 生成的 inner.sh 均通过 `bash -n`。实际运行 Spike 2 到 Docker 调用时退出 1，socket 被拒绝；Spike 1 未连接真实 Supabase，不能声称恢复通过。 |
| YAML | OpenAPI、CI、compose 可解析；这不是 OpenAPI 语义校验或重新生成契约无漂移证明。 |
| pgx / libpq | pgx v5.10.0 与本机 libpq `PQlibVersion=180004` 实际解析 8 组全引号 DSN；字段一致，DSN 不含密码。PG17/18 的 pg_dump 也执行了连接尝试，但连接不可用，详情见下文；不能据此声称真实认证成功。 |
| 仓库产物 | 指定修复删除了 `backend/internal/jobs/-`。本轮执行后未发现该文件或其他新增明文 dump；真实 M1 在有 Docker 的干净 checkout 中仍需补验。 |

会话证据保存在 `/tmp/supabackup-phase2-review2-evidence/`：`race.log`、`m1.log`、`probes-kernel.log`、`probes-edges.log`、`probes-extra.log`、`libpq.log`、`dsn-cases.json`、`bash.log`、`spike2.log`、`probes/`。临时 Go 探针已从工作树移除，副本保留于该目录；最终仓库只新增本报告。`/tmp` 为本次会话证据，不是已提交的永久 CI 测试。

## 上轮逐项判定

下表代码行号均指 `0641b30`。`dumper`、`jobs`、`pgclient` 等缩写均指 `backend/internal/` 下同名目录。

### P1

| 编号 | 判定 | 已修复部分与仍存在的证据 |
| --- | --- | --- |
| P1-01 | **PARTIALLY_FIXED** | `dumper/dumper.go:375` 持续排空 stderr，`:363` 消费者失败会杀原进程组；两者实测有效。但 `:399` 先 `wg.Wait()`，之后才开始 Wait-grace。逃离原进程组的持管道子进程使取消 6 秒后仍阻塞，手动清理后才返回；`:392` 的 stderr 读错误也没有立即触发统一取消。 |
| P1-02 | **PARTIALLY_FIXED** | `pgclient/pgclient.go:97` 不再回显 URL 解析错误，`pgclient/pgx.go:44` 在解析后赋密码，相关探针通过。但 `dumper/dumper.go:166`、`pgclient/inspect.go:149` 仍只替换标签，密码原值未删除；新增 `RedactKnownSecrets` 无调用。实测成功 stderr 摘要仍含 canary；`jobs/jobs.go:269` 仍原样记录 panic。 |
| P1-03 | **FIXED** | `pgclient/pgclient.go:194` 拒绝 NUL/CR/LF/VT/FF，`:207` 所有字段单引号包裹且转义；pgx 增加允许键集合。20 组跨 user/password/dbname/application_name 控制字符载荷均被拒绝；特殊引号、反斜杠与空格经 pgx/libpq 双解析保持原值。 |
| P1-04 | **PARTIALLY_FIXED** | claim 排除取消标志、claim UPDATE 加条件、注册 cancelFn 后补查标志；pending 不再实际执行，探针得到 canceled。但 `Cancel` 仍只置位，pending 要等 worker 空闲才结算；运行中取消实测仍为 failed。`jobs/jobs.go:359` 成功 UPDATE 无状态/取消条件，也不检查 RowsAffected，提交与取消没有原子胜负规则。 |
| P1-05 | **PARTIALLY_FIXED** | 新部分唯一索引与冲突映射有效：32 个同步并发入队恰有 1 成功、31 个 ErrAlreadyQueued，终态后可再次入队。但 `0005_kernel_review.sql:7` 直接建索引，未处理旧版可产生的重复活动行；真实 v4 SQLite fixture 升级失败、停留 v4，服务无法启动。 |
| P1-06 | **PARTIALLY_FIXED** | `server/api_phase2.go:160` 单语句软删除保留历史，List/Get 隐藏目标。但 `jobs/jobs.go:106` 无 deleted_at 条件，`:373` 仍能加载已删除目标。实测“Trigger 已完成 Get → Delete 成功 → Enqueue”仍创建任务并进入凭据加载；删除和入队未形成双向一致性保护。 |
| P1-07 | **REGRESSED** | 虽新增 lifecycle context、WaitGroup、startOnce，`jobs/jobs.go:163` 的 Stop 却是空函数；原版至少能通知空闲循环退出。实测 Stop 连续调用两次后活动 context 仍有效，直到显式 Cancel 才结束。`main.go:215` 的 defer Stop 无法保障 SQLite 关闭前 join；新 lifeCancel/wg 未用于生产关停。 |
| P1-08 | **PARTIALLY_FIXED** | `main.go:42` 启动清理 PG*、子进程清理有效，均有探针。但 pgx 固定 application_name=`supabackup`，libpq 仍用 URI 自定义值；显式 CA/client cert/HOME 文件策略未统一。清掉 PGSSLROOTCERT 后也没有可信 CA 的替代配置入口。 |
| P1-09 | **PARTIALLY_FIXED** | 非本机 URI 未显式 sslmode 会拒绝；错误 query、重复键、分号解析错误被拒绝。数据库新增 sslmode 列并写入，但 `jobs/queries.go:101` 的 DTO、`server/api_phase2.go:259` 的映射与 OpenAPI 均不返回该状态，也无实际 TLS/证书验证结果，持续可见要求未落实。 |
| P1-10 | **PARTIALLY_FIXED** | 新增 schema owner、部分表 ACL 和 default ACL owner 查询，采集编码，扩展 manifest 类型。但 `jobs/jobs.go:323` 未填 DumpStartedAt/RestoreNote/ServerEncoding；实际 TOC、恢复 profile/版本、locale 仍缺失，角色依赖仍漏 type owner、RLS、default ACL grantee 等，采集与 dump 不共用快照。 |
| P1-11 | **PARTIALLY_FIXED** | `dumper/dumper.go:457` 补了 artifact rename 后目录 fsync。但 manifest 的 `jobs/jobs.go:409` 仍是 WriteFile → Rename，没有文件或父目录 fsync；随后即写 succeeded。未实测掉电，判定依据为缺失的持久化步骤。 |
| P1-12 | **NOT_FIXED** | 新 artifact_state 列没有生产读写。manifest 失败后仍不保存 artifact 路径/哈希；成功 UPDATE 失败仍只日志后返回，RecoverInterrupted 仍仅改状态，`.manifest.json.tmp` 无对账/清理。`jobs/jobs.go:380` 的保留引用注释未对应实现。 |
| P1-13 | **PARTIALLY_FIXED** | dumper 增加 QuotaBytes 和轮询，但 `NewRunner` (`jobs/jobs.go:79`) 只填 StagingDir，运行时 config/main 无配额接线。即使手动启用，探针以 1 MiB 配额在约 90 ms 内成功提交 2,097,848 字节密文，早于 500 ms 首次检查；不是硬预算。 |
| P1-14 | **NOT_FIXED** | `jobs/m1_test.go:134` 的 countRows 仍硬编码 `-d appdb`，仍可能检查源库而非 verifydb。仍用应用 agekey 解密、同集群建库、未移除应用状态；CI 没有强制不得 SKIP 的 M1。新增测试没有修复这一核心假阳性。 |
| P1-15 | **PARTIALLY_FIXED** | Dockerfile 已声明安装 PG14–18 客户端。但 `dumper/dumper.go:104` 仍无条件允许任意较新主版本回退；没有经过恢复验证的组合列表，RestoreNote 也未赋值。真实 release image 的 14–18 恢复矩阵未执行。 |

### P2

| 编号 | 判定 | 已修复部分与仍存在的证据 |
| --- | --- | --- |
| P2-01 | **PARTIALLY_FIXED** | 删除重写 job ID 的 reschedule，startOnce 防重复启动；探针证实 ID 稳定、无自唤醒。但 `jobs/jobs.go:203` 遇到首项 perDB 忙即返回 false，实际没有按注释继续扫描其他库；第二个可运行库也被挡住。该分支在当前同步单 worker 正常路径罕见，不夸大为必然饥饿。 |
| P2-02 | **FIXED** | 密码在 ParseConfig 后赋值，空密码不再吃掉 dbname；无密码及特殊口令矩阵保留 Database/User/Password。trust/证书认证的真 PG 验证受环境限制。 |
| P2-03 | **PARTIALLY_FIXED** | URI 控制字符拒绝、正常执行的 PGPASSFILE 删除错误经 errors.Join 返回。但 `staging/staging.go:73`、`:76` 启动清理仍忽略删除错误且虚报 removed，`:83` 仍忽略 liveJobIDs；探针确认路径实际存在却返回 removed 且 err=nil。 |
| P2-04 | **PARTIALLY_FIXED** | 临时文件准备移到 pipes 前，20 次 OpenFile 失败实测 FD 7→7。但第二个 StderrPipe 创建失败时 (`dumper/dumper.go:338`) 仅关闭 dumpOut 的读端，StdoutPipe 留在 cmd.Stdout 的写端未关闭，且未进入 Cmd.Start 的清理逻辑。该受 FD 压力限制的剩余分支为源码确认，未冒称已实测 EMFILE。 |
| P2-05 | **PARTIALLY_FIXED** | 增加 ENOSPC/EDQUOT 分类与 error_code/retryable 列，但后两列没有写入或 API 映射；加密写入错误仍被 `%v` 包装后统一 ClassUnknown (`dumper/dumper.go:435`)，新增结构化分类到不了该路径。deadline/EIO、dump locale、权限测试仍未补齐。 |
| P2-06 | **FIXED** | stdout 已改为标准 identity 文本，先检查写入再存 recipient。本轮原样保存后自身 verify 与官方 age 库 ParseIdentities 均通过；`/dev/full` 注入失败后 settings 未激活 recipient。未安装独立 age CLI，未把库解析测试说成独立 CLI 恢复。 |
| P2-07 | **FIXED** | `server/server.go:100` 事务更新 recipient 与 key ID，API 先 TrimSpace；ageStatus 传播查询错误。第二条 SQL 用 trigger 注入失败，实测公钥回滚；关闭 DB 后状态读取返回错误，没有伪报未配置。 |
| P2-08 | **FIXED** | 明文 `backend/internal/jobs/-` 从当前树删除，新增该精确路径的忽略规则；本轮测试未产生明文归档。历史提交仍含旧 fixture，未发现必须按生产秘密处理的新证据；专门的 CI 产物扫描仍值得补充。 |
| P2-09 | **FIXED** | netip 解析支持 IPv4/IPv6，zone/multihost 明确拒绝；`::1`、`2001:db8::1` 在 pgx/libpq 得到相同 host/port。没有声称受限环境下完成 IPv6 真连接。 |

## 重点回归核验

### 1. 全引号 DSN：未确认兼容性回归，不能将网络失败误判为引号错误

`QuoteConninfo` 为每个值加单引号，对反斜杠、单引号分别转义；这种形式符合 [libpq keyword/value 语法](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING)。本轮将生产 `ConnInfo.DSN()` 输出交给本机 `PQconninfoParse`，覆盖 IPv4、IPv6、域名、含空格/单引号/反斜杠的 user/dbname。7 个 DSN 字段逐个比较，8 组全部一致；密码不在 DSN 中。pgx 相同矩阵也通过，包括空密码。

另调用 `/usr/lib/postgresql/{17,18}/bin/pg_dump -w -Fc -d <DSN>`，两者在当前受限环境均退出 1，stderr 仅为 `pg_dump: error:`；无引号普通 DSN 对照结果完全相同，`PQconnectdb` 同样未提供有效错误文本。因此这里只确认**解析兼容**，无法证明成功连接，更没有证据把该退出归咎于全引号 DSN。真实口令认证、pgpass 特殊字符与 TLS 一致性仍需在可用 PG 环境执行。

### 2. Wait-grace 与提交链：普通收敛改善，管道监督仍有缺口

实测结果：

| 探针 | 结果 |
| --- | --- |
| stderr >1 MiB，然后输出 16 字节 archive 并正常退出 | 可完成加密，离线解密得到完整 `COMPLETE-ARCHIVE`；没有原先的 stderr 塞满死锁。 |
| stdout 1 MiB + 无效 recipient | 及时失败并清掉本轮凭据/临时产物。 |
| `setsid` 子进程继承管道，父任务取消 | 取消 6 秒后仍不返回，超过 WaitDelay=5s；杀掉该逃逸组后才返回 context canceled。探针自行清理了创建的子进程。 |
| 进程先输出并关闭 stdout/stderr，然后 sleep 30 | 约 10.04 秒触发 grace 杀进程，返回 `pg_dump failed: signal: killed`，staging 无最终文件。 |
| 正常输出后非零退出 | 既有 `TestRunRejectsDumpThatFailsAfterOutput` 随包测试执行通过。 |

没有发现 Wait-grace 超时后绕过 `waitErr` 检查的新增成功路径。真正缺口是 grace 在 `wg.Wait()` **之后**才启动，无法给外部 reader 收敛兜底；Go 管理的复制 goroutine 与本代码自建 goroutine 也不能混为一谈。依据已核对本机 Go 1.26 `os/exec` 源码及 [Cmd.WaitDelay / StdoutPipe 文档](https://pkg.go.dev/os/exec)。

提交后半段仍然不可靠：只在 `dumper.go:420` 检查一次取消，之后文件 Sync/rename、manifest、SQLite 成功 UPDATE 都没有统一取消仲裁。artifact 的目录 fsync 已添加，但 manifest 仍不 durable；rename 后目录 Sync/Stat/凭据清理出错，还会出现最终文件存在而 Run 返回错误的分支，需要持久化引用和启动对账。不能靠成功日志或注释证明这些条件已成立。文件与目录同步的区别见 [fsync(2)](https://man7.org/linux/man-pages/man2/fsync.2.html)。

### 3. 部分唯一索引与 Enqueue：新建库去重成立，升级和目标生命周期未闭合

32 并发探针确证 fresh schema 上活动唯一性成立；失败不再制造两个 pending。未知 database ID 直接调用 Enqueue 得到原始 SQLite FK 错误，而非其声明的 ErrDatabaseNotFound；API 的事先 Get 检查通常遮住这一差异，但该方法契约已退化。

用实际 goose Provider `UpTo(..., 4)` 构造旧库，再插入同目标的一条 running 和一条 pending，执行生产 Migrate：`CREATE UNIQUE INDEX` 报 `UNIQUE constraint failed: jobs.database_id (2067)`，schema 保持 v4。迁移前快照被保留，**不是迁移静默破坏数据**；问题是旧版已知可产生的数据使升级停机，而且 main 在迁移成功前不会调用 RecoverInterrupted。必须在建索引前明确去重/保留历史策略，并覆盖 running+pending、pending+pending。

软删除只保护了“入队先发生 → 删除应拒绝”的方向。相反顺序中，TriggerBackup 先 Get 活目标，删除随后成功，再执行 INSERT；由于物理行仍在、FK 合法，任务被接受，runJob 也仍能解密该目标凭据。探针已重放这一顺序。应让 Enqueue 的 INSERT 同时以 `deleted_at IS NULL` 为前提，并检查实际插入行数；不能继续依赖 API 的分离查询。

### 4. 软删除查询：历史保住了，名称与执行入口出现不一致

探针确认 ListDatabases 隐藏已删除项、GetDatabase 返回 ErrNotFound、GetTask 仍能读取历史，这几处符合保留历史的方向。新增的具体回归是：`databases.name` 仍为无条件 UNIQUE（`0004_backup_kernel.sql:21`），所以 Delete 成功后同名重新注册报 409；该行又不在列表中，用户没有 rename/undelete 入口。旧版硬删除可复用名称。需要对活记录建立名称唯一约束，或提供明确的恢复/复用流程，同时保持历史 ID 稳定。

### 5. PG* 清理：实际执行有效，但不能代替 CA 与运行参数统一

探针实际调用 `main()` 的 version 路径，8 个注入的 PGHOSTADDR/PGOPTIONS/PGSERVICE/PGSERVICEFILE/PGSSLROOTCERT/PGSSLKEY/PGPASSFILE/PGPASSWORD 均被删除，SB_DATA_DIR 保持不变；另一探针在实际 dumper 子进程中只看到应用新建的 PGPASSFILE。没有发现清理破坏非 PG 运行配置，也没有把绕过 main 的包内 ParseConfig 行为误报为生产入口仍继承环境。

剩余问题为配置语义：自定义 application_name 在 pgx 被覆盖；CA 既不写入 DSN，也没有显式为 pgx 固定来源。本机无用户根证书时，verify-full 的 pgx TLSConfig.RootCAs 为 nil，而 libpq 的证书文件/信任根规则不同，见 [libpq SSL 说明](https://www.postgresql.org/docs/18/libpq-ssl.html)。仅安装系统 ca-certificates 不等于已给两条链建立一致信任配置。清掉 PGSSLROOTCERT 是预期的环境隔离，但依赖私有 CA 的部署失去了旧入口，而新受控入口尚不存在；必须补完配置与真 TLS fixture 后才能关闭 P1-08。

## OPEN 清单与关闭条件

### 上轮仍 OPEN 的 18 项

| 编号 | 尚需完成的具体工作与验收 |
| --- | --- |
| P1-01 | 将 stdout、stderr、Wait 和取消纳入同一个有界监督；错误时关闭自有端点并回收，覆盖逃逸持管道者、stderr 读错误、ENOSPC。探针证明普通大 stderr 已好，但不能据此关闭整项。 |
| P1-02 | 真正删除已知密码及编码/转义形式，统一 API/job/log/panic 脱敏；限长应放在脱敏后。让 fake 实际输出 canary，再扫描全部出口。本轮 `password=CANARY-secret-pw` 得到的仍是 `password=[REDACTED]CANARY-secret-pw`。 |
| P1-04 | pending Cancel 原子进入终态；运行中用户取消=canceled、实例停机=interrupted；最终提交用带状态/取消条件的原子 UPDATE 与 RowsAffected 仲裁。对注册握手查询错误不能忽略。 |
| P1-05 | 为 v4 已有重复活动行提供迁移收敛策略；保留审计历史与在执行任务的处置依据。fresh-schema 的唯一索引测试已通过，升级 fixture 仍阻断。 |
| P1-06 | Enqueue 原子验证活目标；覆盖删除先赢/入队先赢两种顺序；删除成功后不得出现新活动任务。最终成功 UPDATE 仍需核对 1 行。 |
| P1-07 | 实现 Stop 生命周期取消与 join，覆盖直接 Stop、SIGTERM、HTTP bind 失败；在关闭 SQLite 前收敛 worker/进程/凭据，Stop 两次安全，返回后不再访问 store。当前空实现必须修复。 |
| P1-08 | 固定双方一致的 app name、受控 CA 与客户端证书/默认 HOME 文件策略；注入 PG* 后验证端点、用户、DB、TLS 与 CA/hostname 正反例。 |
| P1-09 | 将 sslmode 与实际 TLS/验证状态接入 DTO、OpenAPI、API 查询；不能仅保存一列或写一次日志。升级旧记录时也应明确未知状态。 |
| P1-10 | 实际 archive TOC、完整角色依赖、编码/locale、恢复 profile/工具/目标限制与快照一致性；新增字段必须赋值。现在 dumpStartedAt 会序列化为 `0001-01-01T00:00:00Z`，RestoreNote 为空、ServerEncoding 未输出，是新增接线缺陷。 |
| P1-11 | manifest 使用完整 durable-write（Write/Sync/Close/Rename/目录 Sync）；全部完成才成功落库，补边界故障注入。 |
| P1-12 | artifact 提交意图/路径/哈希落库、失败留引用、启动对账、终态有界重试；BUSY/manifest ENOSPC/rename 后崩溃都能恢复且不重 dump。schema 中一个未读写的 artifact_state 不能解决这些路径。 |
| P1-13 | 将预算接入运行配置；用写入预算/预留保证硬边界，保护 SQLite 安全余量，补预检估算；不能依赖 500 ms 扫描或把一次配额取消误标其他原因。 |
| P1-14 | 修复 countRows 的目标；关闭并移除应用状态，独立 age/PG 工具在新集群恢复并核对内容/结构/sequence/ACL；故意损坏目标必须失败；CI mandatory 模式不得 SKIP。 |
| P1-15 | 限定并实测允许回退的恢复组合，manifest 写明目标限制；发布镜像 PG14–18 实测。较新 pg_dump 能导出旧库不等于旧目标可恢复，见 [pg_dump 兼容性说明](https://www.postgresql.org/docs/18/app-pgdump.html)。 |
| P2-01 | 冲突首项不要挡住其他库；跳过忙目标或独立调度条件。保持当前已修复的 ID 不变和不自唤醒行为。 |
| P2-03 | 启动清理逐项传播错误，removed 只包含确实删除项；保护 live 引用或收紧为独占启动接口。正常 defer RemoveAll 返回错误的改进需保留。 |
| P2-04 | 每个建管道失败分支关闭已拥有的两端；尤其第二条管道创建失败时 cmd.Stdout 写端不能留给 GC。保留已验证的 OpenFile 失败 FD 稳定性。 |
| P2-05 | 打通 error_code/retryable 的生产写入及 API，保留 errors.Is/As 链；覆盖密文 ENOSPC/EDQUOT/EIO、deadline/SQLSTATE、固定 locale 和权限不足。 |

### 本轮新增或加重的回归定位

下列前四项与上面的旧编号关联，不另重复计入“上轮 OPEN 18 项”；R2-P2-01 是额外的具体行为回归。

| 回归编号 | 严重度 | 位置与影响 | 关联 |
| --- | --- | --- | --- |
| R2-P1-01 | P1 | `jobs/jobs.go:163` Stop 从关闭 stopCh 退化为空函数，连空闲 worker 都不会由 Stop 停止。 | P1-07 |
| R2-P1-02 | P1 | `0005_kernel_review.sql:7` 在旧版合法但重复的活动行上建索引失败，阻断升级启动；已实测。 | P1-05 |
| R2-P1-03 | P1 | `jobs/jobs.go:106`、`:373` 与软删除未联动，已隐藏的目标仍可创建并执行任务；已实测。 | P1-06 |
| R2-P2-02 | P2 | `manifest/manifest.go:20` 改名后的 DumpStartedAt 未赋值，旧的非零创建时间变为 year 1；新增恢复说明/编码字段也未接线。 | P1-10 |
| R2-P2-01 | P2 | `server/api_phase2.go:160` 保留 tombstone，但 name 仍全表唯一；删后同名新增失败且列表无可操作目标。 | 新增，需与 P1-06 一起修复 |

另保留两项事实提醒，不重复新增问题编号：`dumper.Result.PlaintextArc` 实测为 216（密文字节），解密后的 archive 实际为 16，仍沿用上轮已指出的计数位置错误；stderr 原始保留上限为 16,384，标签替换后摘要达 16,394，最终输出并未严格遵守同一上限。二者都不能靠字段改名或注释算作解决。

## 工程门禁与最终结论

**Phase 2 代码：未通过。工程门禁：未通过。总体：未通过。**

构建、vet、若干重要修复与定向 race 探针已给出正面证据，但仍存在可复现的凭据残留、管道不收敛、关停退化、升级失败、删除/入队竞态和配额失效。状态机、manifest、本地产物对账、恢复矩阵的核心要求尚未完成，不能判为“修改后通过”。

全量 race 在本环境被 listener 权限阻塞；真 PG/M1 与容器 Bash spike 被 Docker 权限阻塞，这些需要在可执行环境补跑。即便解除环境限制，当前 M1 仍检查源库、允许 SKIP，现有 CI 的绿色也不能证明 M1。下一轮应先闭合上述 P1，再提供不可跳过、能检测恢复结果损坏的独立恢复门禁及相应永久回归测试。
