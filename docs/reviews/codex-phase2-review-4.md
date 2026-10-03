# Phase 2 备份执行内核复审（第 4 轮）

评审日期：2026-10-03。基线与实际 HEAD：`143d6227260cb3e64ff12e65cfa7394a75382489`；范围：`git diff 41d3c91..143d622`。依据为[第三轮报告](codex-phase2-review-3.md)的 OPEN 项、前两轮原始验收条件及 [dev-plan](../dev-plan.md) 的 Phase 2、协议 A/B/E、M1。本轮使用 `code-reviewer` 技能，只评审并编写报告，未修改生产代码或提交永久测试。

**最终判定：Phase 2 代码与工程门禁未通过。** 本轮指定的 **8 项修复：FIXED 2、PARTIALLY 4、NOT_FIXED 1、REGRESSED 1**；另核对的 **P1-14：PARTIALLY**。新增确认 **R4-P1-01：升级静默删除 jobs 历史**、**R4-P2-01：v5 Down 被新增索引阻断**。未确认 P0。

本报告的 FIXED 表示该问题本身关闭，不要求同一问题域实现所有更完善的方案。PARTIALLY 均对应原验收范围内的具体残留。真实 Supabase、联网 CI、双架构、掉电实验等外部资源项单列，不作为代码缺陷；多用户、PITR、Phase 3 远端存储等扩展能力不纳入判定。即使完全排除外部执行限制，下述锁泄漏、升级丢历史、配额突破、错误出口泄密及无引用密文仍足以阻断门禁。

以下源码位置均指 `143d622`；`jobs/`、`dumper/`、`db/` 等省略 `backend/internal/` 前缀。

本轮八项修复与 P1-14 的逐项判定如下。

| 编号 | 判定 | 已满足条件及具体残留 |
| --- | --- | --- |
| **R3-P1-01：running Cancel 丢 Unlock** | **NOT_FIXED** | `jobs/jobs.go:223` 拿锁后，已注册 cancelFn、尚未注册 cancelFn 两条分支仍直接返回，均未 Unlock；本轮 diff 没有修改 Cancel。实测 Cancel 返回后任务可写成 canceled、子进程和临时文件可清理，但 worker 卡在 `:320`，下一任务 pending，Stop 等待 `wg` 不返回。详见全链实测。 |
| **R3-P2-01：并发 Stop 不 join** | **FIXED** | `jobs/jobs.go:189` 用 `stopOnce.Do` 完成取消、`wg.Wait`、关闭 `stopDone`，所有调用再等待该 channel。实测 worker 在取消后被清理屏障阻塞时，两个 Stop 均不返回；释放屏障后均返回，顺序重复调用也安全。R3-P1-01 仍可使 worker 无法结束，但不据此否认“并发第二个 Stop 提前返回”已关闭。 |
| **P1-04：早期阶段取消** | **PARTIALLY** | recipient 返回错误、连接测试、依赖采集三个阶段已执行取消终态仲裁；recipient 用真实 worker，两个网络阶段用明确的受控回调实测为 canceled。pending Cancel 和提交末端条件 UPDATE 也继续有效。但注册握手已取消 context、随后进入 `loadDatabase` 时，`:358`–`:360` 仍直接 fail；实测为 `failed + cancel_requested=true + load database record: context canceled`。正常 Stop 取消 recipient 时也仍写 failed，恢复仅处理 running，不能变成 interrupted。R3-P1-01 另阻断清理；握手及 `ranToCancellation` 的查询错误仍被忽略。 |
| **P2-01：claim 扫描** | **FIXED** | `jobs/jobs.go:247` 读取候选，`:275` 跳过忙库。实测第一库 busy、第二库空闲，第二任务被 claim 并进入执行，第一任务仍 pending，两个 ID 均不变。现有单 worker、每库至多一条活动记录的 Phase 2 模型下，`LIMIT 50` 不构成本条原先的首项阻塞缺陷；不以将来大规模并发调度需要分页为由保留 OPEN。 |
| **P1-02：引号 canary、已知秘密接线** | **PARTIALLY** | `dumper/dumper.go:168`–`:175` 已删除普通单引号口令值；`jobs/jobs.go:347`、`:352`、`:374` 已接线已知口令，实测普通 canary 在 panic 日志和 task error 中消失。但合法反斜杠转义单引号仍泄漏后缀；成功 stderr 摘要中的无标签已知口令仍保留；CreateDatabase 错误出口仍仅调用无效的 `pgclient.SanitizeMessage`，受控错误注入得到包含完整 canary 的 API 响应。不是原先“只有定义、完全未接线”，也尚未达到原有四出口验收。 |
| **P1-13：Write 硬边界、非法 config** | **PARTIALLY** | 写入错误现在能在流中触发生产者终止，并归为 disk/ErrStagingFull；8 MiB 生产者在 1 MiB 配额下未完成输出，临时文件和凭据清理成功。但 `dumper/dumper.go:516` 先写、`:518` 才检查，仍越界；预算未扣除已有文件，快流仍能成功提交超总配额；`config/config.go:55` 对非法、负数、溢出值仍静默禁用配额，本轮无 config diff。详见定量结果。 |
| **P1-12：Recover 对账、tmp 清理** | **PARTIALLY** | `staging/staging.go:79`–`:80` 已清理 `.durable-*` 和 `.manifest.json.tmp`，实测保留最终密文及 manifest。`RecoverInterrupted` 增加了向错误信息追加恢复说明的 SQL，但完全没有读取文件、核验哈希或解析 manifest；首次 artifact 引用 UPDATE 失败后仍留下无引用密文，恢复不能发现。success UPDATE 连续失败或提交后取消，磁盘 manifest 存在而 API `HasManifest=false`，仍未修。 |
| **R2-P2-01：name 部分唯一** | **REGRESSED** | 新建 schema 上同名复用和活名称唯一性有效：直接 API handler 实测 `201(id=1) → 204 → 201(id=2) → 重复活名称409`，旧任务保留。但修改的是已存在的 v5，旧 v5 实例升级不重跑该 SQL，同名仍失败；更严重的是从 v4 升级时 `DROP TABLE databases` 触发 FK cascade，全部 jobs 被删除。新建库行为已修，升级整体倒退，详见 R4-P1-01。 |
| **P1-14：内容核对与负对照（额外核对）** | **PARTIALLY** | 目标库计数、ID 1/250/500 的 payload 检查是第三轮已关闭的子问题，本轮继续认可。`m1_test.go` 和 CI 在本轮均无改动：仍无损坏目标负对照、无不可 SKIP 模式；恢复仍依赖应用解密函数，同一集群建 verifydb，未先移除应用/SQLite。属于原 M1 工程验收残留，不以缺真实 Supabase 或联网环境为由降级。 |

**新增回归 R4-P1-01：v4→v5 迁移成功却清空所有 jobs。**

定位：`db/migrations/0005_kernel_review.sql:45`；关联 `0004_backup_kernel.sql:29` 的 `REFERENCES databases(id) ON DELETE CASCADE`，以及 `db/db.go` 对每个连接启用的 `foreign_keys(1)`。

重建父表时，先复制 databases，再 DROP 原父表，并不能保住引用原表的 jobs。真实 goose/modernc fixture 先 `UpTo(4)`，创建两条 database、四条活动 job 和一条 succeeded 历史 job（含 artifact 路径），然后调用生产 `Store.Migrate`。结果为：

```text
foreign_keys=1
Migrate error=nil
schema=5
databases=2
jobs_before=5
jobs_after=0
pre-migration snapshot jobs=5
```

这不是假设的 SQL 语义，也不是无法启动：升级成功，服务将继续运行，只是任务历史、取消/执行状态和产物引用已从活动数据库消失。上一轮 P1-05 的“重复活动任务收敛且保留历史”因此回归。预迁移快照实际保留了五条记录，存在人工回滚来源；不能把本缺陷描述成连备份也不可恢复，但快照不会自动把已删除 jobs 恢复到在线库。

此外，用 `41d3c91` 的原始 v5 SQL 完整迁移，再换成当前 migrations 调用 Migrate，因版本已经是 5，不会重放修改过的 v5。软删除后同名 INSERT 仍报 `UNIQUE constraint failed: databases.name (2067)`。这是 R2-P2-01 的升级漏修，不另外计一个新回归编号。

修复应有可到达的向前迁移，安全处理父子表重建并保留历史；同时覆盖未升级的 v4 和已应用旧 v5。永久迁移测试需要断言任务 ID、状态、artifact 引用、历史行数及外键完整性，不能只断言 Migrate 无错误和索引存在。

**新增回归 R4-P2-01：v5 Down 无法移除被部分索引引用的列。**

定位：`0005_kernel_review.sql:47` 新增 `idx_databases_name_live`，但 Down 的 `:64` 直接 DROP COLUMN deleted_at，没有处理此索引及原 name 唯一性。新建 schema 后调用真实 goose Provider.Down，返回：

```text
error in index idx_databases_name_live after drop column:
no such column: deleted_at
```

正常服务启动只走 Up，本项不意味着所有启动都会失败；影响是仓库提供的迁移回退路径被本次变更破坏。应同步定义安全的 Down 行为；存在多个已删除同名记录时，还需明确如何回到旧版全表唯一模型，不能仅删除索引就声称恢复了 v4 约束。

关键实测对实现改善和残留分别给出了证据。

| 场景 | 实际结果 |
| --- | --- |
| **running Cancel → dump/worker 清理 → 后续任务 → Stop** | 生产 Runner 启动真实 OS 替身子进程，经生产 dumper/age 管道执行；仅 pgclient 两个网络函数用 overlay 返回元数据。Cancel 返回 nil；子进程 PID 已不存在，staging 无 `.inprogress`/PGPASSFILE，首任务 canceled。下一任务仍 pending，Stop 在观察窗口内不返回，`r.mu.TryLock()` 失败。测试仅为收尾主动 Unlock 后，worker maps 才清空且 Stop 返回。这个 PASS 探针断言的是缺陷存在，**全链验收未通过**。 |
| **Cancel 的注册窗口** | 无网络、无 overlay：人为停在 running 已落库、cancelFn 尚未注册的合法窗口，Cancel 同样返回时持锁，补查 goroutine 也受阻。该分支和已注册分支都未修。测试释放泄漏锁后再退出。 |
| **并发 Stop** | worker 接到取消后等清理屏障；第一、第二 Stop 均等待超过 1 秒。释放屏障后两个调用均结束；重复 Stop 安全。没有 Cancel 锁泄漏介入时，这个指定回归已关闭。 |
| **单文件写入硬边界** | 直接调用真实 countingWriter，quota=1024，`Write(4096)` 返回 `n=4096` 与 errQuotaExceeded，底层已保存4096字节。实际 age 流中也采样到临时密文至少 **1,049,016 > 1,048,576** 字节；采样值是观察到的下界，不冒称精确峰值。 |
| **流中及时中止** | 8 MiB 生产者未到“全部输出完成”标记便被终止，Run 返回 ErrStagingFull，临时文件/凭据清理。原来“先完整生成8 MiB再判断”的子问题已修；并未因此满足写入前硬限制。 |
| **已有产物计入总预算** | 已有700 KiB时，新任务约27ms内成功提交717,160字节，总量 **1,433,960 > 1,048,576**。500ms watchdog 来不及运行；writer 拿到的是全额 quota，而非剩余预算。旧密文保留。 |
| **非法配额配置** | `1048576` 正常；`1MiB`、`-1`、`99999999999999999999999999` 均 `err=nil, StagingQuotaBytes=0`。没有拒绝配置。 |
| **软删除后同名复用** | 新 schema 的真实数据库加直接 Create/Delete handler 通过；仅连接测试用固定 PG 元数据。旧 v5 升级 fixture 未通过。v4 升级名称索引建成，但付出全部 jobs 丢失的代价。三条路径不能合并成“同名复用已修”。 |
| **已提交密文首次落引用失败** | 用 SQLite trigger 只阻断 artifact_path UPDATE，执行完整生产提交链。文件已提交，任务 failed、path/state 为空；移除注入后 RecoverInterrupted 仍不发现文件。 |
| **恢复对账** | running+磁盘有密文但 DB 无引用：恢复后仍无引用；committed+磁盘缺文件：照样追加“artifact committed”说明；存在 path、state 为空的合成不一致 fixture：引用被清空，实际文件保留。后者证明新增 SQL 不核验文件，但不单凭这个合成状态另报正常路径必现的数据丢失回归。 |
| **manifest / 最终状态** | manifest 写失败仍保留密文引用并置 committed_no_manifest；首次 success UPDATE 失败约500ms后重试可成功；持续失败为 failed；提交后 cancel 不会误写 succeeded；已是 interrupted 不被覆盖。持续失败及提交后 cancel 的 manifest 路径仍缺，API 错报 HasManifest=false。 |
| **启动 tmp 清理** | `.durable-review.tmp`、`backup-job1.dump.age.manifest.json.tmp` 实际删除；最终密文/manifest 保留。非空 `.inprogress` 目录删除失败准确返回错误，removed 不虚报。 |

P1-02 的 canary 结果需要明确区分出口与编码形式。真实 fake pg_dump stderr 的普通 `password='CANARY-secret-pw'` 已被删除；无标签 `connection rejected: CANARY-secret-pw` 在 `Result.StdErrExcerpt` 仍出现。该摘要当前不直接映射到任务 API，不能据此声称它已经经任务 API 外泄。

对合法 conninfo 转义形式，生产 `QuoteConninfo`、`RedactKnownSecrets`、`sanitize` 联合探针得到：

```text
raw secret: prefix'CANARY-SUFFIX
input:      password='prefix\'CANARY-SUFFIX'
output:     password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'
```

已知秘密替换未处理单引号转义，正则把转义引号当成结束边界。这属于第一轮明确要求的特殊字符口令验收。

另直接调用生产 CreateDatabase handler，仅给 `pgclient.Test` 注入含本次连接口令的错误；API 422 内容为：

```json
{"code":"connection_test_failed","message":"connection test failed: password=[REDACTED]'CANARY-secret-pw'"}
```

定位是 `server/api_phase2.go:112` 调用的 `pgclient/inspect.go:149`，后者仍只替换标签，未移除值。本探针证明错误出口仍泄漏受控 canary，不声称真实 PostgreSQL 在本次环境已发出该错误。job 的普通原始口令/panic 脱敏则已实测通过，必须保留这项正面结论。

P1-14 的代码与工程内容核对如下；本轮没有把 Docker 不可用本身算成缺陷。

| 验收内容 | 当前状态 |
| --- | --- |
| 查询恢复目标而非源库 | **已关闭**。`m1_test.go:136` 用 `ci.DBName`，`:305` 传入 verifyURI。 |
| payload 内容核对 | **已有部分实现**。`:155` 只查询 ID 1/250/500，逐条比较 md5；不是单纯行数检查。 |
| 损坏目标负对照 | **未实现**。没有清空目标、删行、改 payload、破坏 sequence/ACL 后断言验证失败的测试。由查询范围可知：保持行数、仅改变未抽样行的 payload 不会被现有比较发现；此为源码推导，未冒称本轮实跑了真实 PG 篡改实验。 |
| 脱离应用恢复 | **未实现原定隔离步骤**。`:278` 仍用 `agekey.DecryptStream`；store/runner 的关闭留在 defer，未在恢复前删除；`:288`–`:300` 在原 `pgContainer` 建 verifydb 并 `--no-owner` 恢复。独立 age CLI、全新集群、移除应用状态等原验收步骤未接入。 |
| CI 必跑且不可跳过 | **仓库工程缺口仍在**。`:49`、`:83` 对依赖/容器失败 SKIP，CI 只普通 `go test -race ./backend/...`，没有 mandatory 模式。本轮 M1、Interrupted、ConcurrentEnqueue、CancelPending 四个现有测试全部 SKIP，但包返回 PASS。 |

更早 OPEN 项按原范围继续复核；以下未改动项不会因“本轮未修”被自动算成新回归。

| 编号 | 判定 | 本轮证据与原验收残留 |
| --- | --- | --- |
| **P1-01** | **PARTIALLY** | 普通消费者错误杀进程组逻辑仍在。`dumper/dumper.go:405` stderr 读错误只记录；`:412` 先等自有 reader，之后才启动 Wait。无 overlay 的真实 `setsid` 持管道后代探针在取消6秒后仍阻塞，外部 kill 该后代后才返回，临时文件随后清理。stderr 读错误路径本轮为源码复核，未重复声称注入实测。 |
| **P1-05** | **REGRESSED（历史保留部分）** | 活动任务部分唯一索引和去重 SQL 仍在；但新增 DROP 父表令迁移后的 jobs=0，上一轮通过的升级历史保留条件回归。与 R4-P1-01 是同一缺陷，不重复计数。 |
| **P1-06 / R2-P1-03** | **PARTIALLY** | `loadDatabase` 隐藏目标守卫继续有效。但 `jobs/jobs.go:125` 无条件 VALUES INSERT 未改；实测 API 先 Get、Delete 成功后，Enqueue 仍接受，最终因目标隐藏失败。相反顺序中 pending 先存在则删除正确拒绝。需保持已修的加载守卫并补齐入队原子条件。 |
| **P1-07** | **PARTIALLY** | Stop 的空函数及并发早退两个具体回归均关闭；R3-P1-01/P1-01 仍能阻断 join。正常 Stop 在 recipient 阶段还把任务落为 failed，非 interrupted；这是本轮实际结果，不能靠 startup 注释视为已修。 |
| **P1-08** | **PARTIALLY** | pgx/URI 代码无 diff：`pgclient/pgx.go:48` 固定 application_name=supabackup，DSN 仍可传自定义 AppName；`ConnInfo` 没有统一受控 CA/client-cert 来源或默认 HOME 证书策略。残留依据是配置实现不一致，真实 CA/hostname 矩阵缺执行证据另归环境项。 |
| **P1-09** | **PARTIALLY** | 非本机显式 sslmode、解析拒绝错误 query、DTO/契约接线的已关闭部分不重开。真实 SQLite Get/List、直接 API handler 再验字段和值正确。但 API 只有配置模式；`pgclient.TestResult` 仍仅版本字段，没有实际 TLS/证书验证结果，旧记录仍空字符串，未达到原报告要求的配置与实际状态区分。 |
| **P1-10** | **PARTIALLY** | 非零 dumpStartedAt、编码、RestoreNote、FDW 不覆盖数据的说明、restoreVerified=false 的接线再次通过提交链探针。manifest 类型仍没有实际 TOC、locale、恢复 profile/restore 工具版本，age 仅库名；依赖查询仍缺 type owner/RLS/default ACL grantee 等原列明项。不是要求新增 PITR 或完整平台克隆。 |
| **P1-15** | **PARTIALLY** | Dockerfile 的14–18客户端声明和恢复目标限制说明继续认可；`dumper/dumper.go:104` 仍接受任意 client≥server，未限制为已声明可用的回退组合。代码缺口是回退策略，真实 release image 矩阵及双架构验证是外部待验项，二者分别记录。 |
| **P2-04** | **PARTIALLY** | `dumper/dumper.go:343` 第二条 StderrPipe 失败时仍只关闭 dumpOut 读端，未关闭 StdoutPipe 留在 cmd.Stdout 的写端，也未进入 Start 清理。本轮为源码确认，没有声称实造 EMFILE。此前 OpenFile 提前、普通 Start 失败路径改善保持。 |
| **P2-05** | **PARTIALLY** | quota exceeded 新分支可分类为 disk；但实际 `/dev/full` 密文写失败仍为 unknown，`errors.Is(err, ENOSPC)=false`，源于 `dumper/dumper.go:453` 的 `%v` 包装。生产失败记录 error_code/retryable 仍为空/0，Task DTO 也未暴露这些字段。 |

第三轮已关闭的 **P1-11、P2-03** 保持 FIXED：durable helper 的普通写入、0600、rename 失败和临时文件清理再次通过；启动清理逐项错误传播也通过。未因缺少掉电实验重开 P1-11。GET/List 列序、successSQL 条件与 RowsAffected、失败重试/保留已提交密文的既有有效分支，均未发现新的退化。R2-P1-02 原先的“建唯一索引前未收敛”本身仍已关闭；本轮迁移丢历史单独定位 R4-P1-01。

本轮验证记录如下。

| 检查 | 结果与限制 |
| --- | --- |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race -count=1 ./backend/...` | **整体 FAIL，server 包受环境阻塞**：httptest listener 报 `socket: operation not permitted`。其他有测试的包通过；jobs 的真实 PG 测试 SKIP。没有称为“全量 race 通过”。 |
| 定向临时审计测试 | **31 个不同顶层探针实际执行，24 PASS、7 FAIL**，无 race 报告。7个 FAIL 对应早期取消、迁移丢历史、旧v5名称、Down、硬边界、转义口令、API canary。部分 PASS 刻意断言缺陷存在，如锁泄漏、总配额突破、无引用密文，不等于产品通过。overlay 测试在普通轮次 SKIP 后，均另以指定 overlay 实际执行。 |
| 真实执行与替身边界 | Cancel/Stop、SQLite、迁移、进程组、管道、age 加密、文件提交、manifest 和 handler 都执行生产代码。PG overlay 只替换 Test/CollectDependencies 为固定元数据、可取消阻塞或指定错误；pg_dump 用受控脚本，**不是可恢复的真实 PG archive，也不是 M1**。 |
| `go vet ./backend/...`、CLI build、gofmt | vet/build 退出0；移除临时测试后 gofmt 无输出。build 产生模块 stat-cache 只读告警，CLI 二进制成功生成。 |
| 契约生成 | 本机 oapi-codegen、openapi-typescript 生成到证据目录，与提交版比较，Go/TS 均无漂移。 |
| 前端 | Node22.22.2 下 npm lint/build 均退出0。 |
| Docker / M1 | docker info socket 权限拒绝；四个现有 jobs 集成测试全部 SKIP。本轮没有构建运行镜像、真实 PG 恢复、TLS 实连、Supabase spike、双架构或掉电实验。 |
| 改动范围核验 | config、M1、CI、Dockerfile、两个 spike、pgclient 的 pgx/inspect、manifest 类型等与 `41d3c91` 的 blob 相同。工程门禁新增不足有源码依据，不是未看到外部 CI 结果的推测。 |

证据目录：`/tmp/supabackup-phase2-review4-evidence/`。主要记录为 `probes-core.log`、`probes-pipeline-api.log`、`probes-cancel-dump.log`、`race.log`、`m1.log`、`scope.json`、`build.log`、`vet.log`、`frontend-{lint,build}.log`、两份生成物 diff；临时源码在 `probes/`，PG overlay 及原始 v5 SQL 同目录保存。`README.md` 说明重放方法和探针判定方式。临时测试已从工作树移除，最终仓库只新增本报告；`/tmp` 证据不是已提交的永久回归测试。

外部待验事项单列：可监听网络的环境重跑 server race；可用 Docker/PG 执行真实 M1、TLS 和 release-image 恢复矩阵；真实 Supabase、联网 CI、双架构、隔离掉电实验按各自资源补证。它们不增加本报告代码缺陷数量。仓库内缺少 M1 mandatory 模式、负对照和独立恢复步骤，则仍属于工程门禁本身的缺口。

**代码：未通过。工程门禁：未通过。最终：未通过。** 不适合判为“修改后通过”：当前 HEAD 仍有可稳定复现的核心可靠性/安全缺陷，且本轮引入了成功升级却删除全部任务历史的 P1 回归。应先关闭上述明确代码路径并纳入永久回归测试；已经通过的并发 Stop、claim 扫描、普通引号脱敏、流中提前中止、tmp 清理等子问题无需重做或扩展验收范围。
