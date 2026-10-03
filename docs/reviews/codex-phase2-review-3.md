# Phase 2 备份执行内核复审（第 3 轮）

评审日期：2026-10-03。评审范围：`git diff 0641b30..41d3c91`，实际 HEAD：`41d3c9126d3d28089e7ce8b319d98a161c2d84c2`。仅复审[第二轮报告](codex-phase2-review-2.md)的 OPEN 项及本次修改引入的回归，验收依据仍为 [dev-plan](../dev-plan.md) 的 Phase 2、协议 A/B/E 和 M1。本轮未修改生产代码。

**最终判定：Phase 2 代码未通过；工程门禁未通过；总体未通过。**

按第二轮遗留的原始编号统计：18 项中，本轮 **FIXED 3、PARTIALLY_FIXED 14、REGRESSED 1**，仍 OPEN **15 项（P1 12、P2 3）**；另有第二轮新增的 **R2-P2-01** 仍 OPEN。本轮确认 **R3-P1-01（running Cancel 泄漏互斥锁）**、**R3-P2-01（并发 Stop 提前返回）**，分别关联既有取消/关停问题，不再重复累加到旧编号数量。没有确认 P0。

FIXED 表示指定实现缺陷已闭合，不等于所有故障矩阵已通过；PARTIALLY_FIXED 表示仍有具体未完成路径；REGRESSED 表示有效修复之外引入了更严重的新问题。特别注意：**Stop 的“空函数”回归已修复，但完整关停问题 P1-07 尚未闭合。**

## 验证范围与结果

| 检查 | 实际结果与边界 |
| --- | --- |
| `GOCACHE=/tmp/supabackup-review-go-cache go test -race -count=1 ./backend/...` | **整体 FAIL，server 包受环境阻塞**：`httptest` 创建 TCP listener 返回 `operation not permitted`。其余有测试的包通过；jobs 的真实 PG 测试实际 SKIP。不能称为全量 race 通过。 |
| Go 构建、vet、gofmt | CLI 构建与 `go vet ./backend/...` 退出 0；`gofmt -l backend` 无输出。build 有模块 stat-cache 只读告警，但二进制确实生成并执行。 |
| 临时审计探针 | **19 个顶层测试实际执行完成**，含 SQLite 迁移、调度/关停、真实管道、age 加密、文件故障、直接 API handler 等，无 race 报告。部分测试故意断言缺陷存在，其 PASS 不是产品验收通过。 |
| 提交链故障注入 | 7 个子场景执行生产 Runner、dumper、SQLite、age、manifest 代码；仅用 Go `-overlay` 将 `pgclient.Test` / `CollectDependencies` 两个网络函数替换为固定元数据，并用受控 `pg_dump` 输出。**不是实际 PostgreSQL 备份/恢复测试**。 |
| stderr 读错误注入 | 单独 overlay 仅在 `cmd.Start` 后关闭实际 `stderrPipe`，保留生产读取/监督/错误处理逻辑，验证真实读错误不能主动收敛任务。不是自然发生的 OS 故障复现。 |
| 契约生成 | 使用本机 `oapi-codegen`、`openapi-typescript` 生成到证据目录，与提交文件比较：Go / TypeScript **均无漂移**。数据库 Get/List 的 Scan 及 API JSON 字段另行实测。 |
| 前端 | Node 22.22.2 下 `npm run lint`、`npm run build` 均退出 0。 |
| 原有 M1 等测试 | `TestM1_FullKernelChain`、`TestInterruptedRecoveryAtStartup`、`TestConcurrentEnqueueExactlyOne`、`TestCancelPendingJobReachable` **全部 SKIP**，原因是 Docker 不可用；后两类纯 SQLite 行为另有实际探针。 |
| Docker、发布镜像 | `docker info` 被 socket 权限拒绝；`sudo -n docker info` 被 no-new-privileges 拒绝。未构建/运行发布镜像，未执行 PG14–18 恢复矩阵。 |
| shell、配置、spike | 两个 spike、Spike 2 的 inner.sh、CI 的 14 段 run shell 均通过 `bash -n`；OpenAPI、CI、compose YAML 可解析。实际运行 Spike 2 在 Docker 调用处退出 1；未运行需要真实 Supabase 的 Spike 1。 |
| 启动失败退出 | 构建出的 CLI 在 listener 权限错误后 10 秒内退出 1，没有卡在空闲 worker 的 Stop。未据此声称 SIGTERM + 真实 PG 子进程关停已通过。 |

证据目录：`/tmp/supabackup-phase2-review3-evidence/`。主要日志：`race.log`、`probes-core.log`、`probes-pipeline.log`、`probes-stderr.log`、`probes-api-config.log`、`m1.log`、`build.log`、`vet.log`、`frontend-{lint,build}.log`、`bash-and-scope.json`、`spike2.log`。临时测试源码保存在 `probes/`；两个 overlay 的 JSON 和替换源码也已保存。临时测试已从工作树移除，最终仓库只新增本报告。`/tmp` 是本次会话证据，不是永久 CI 回归测试。

以下代码位置均指 `41d3c91`；`jobs/`、`dumper/`、`pgclient/` 等路径省略了 `backend/internal/`。

## 三个 P1 回归的逐项实测

| 编号 | 判定 | 证据 |
| --- | --- | --- |
| **R2-P1-01：Stop 空函数** | **FIXED（该具体回归）** | `jobs/jobs.go:166` 调用 `lifeCancel()` 并 `wg.Wait()`。探针让 worker 停在接收 context 的受控步骤，调用 Stop 后 context 取消，worker 完成清理，`perDB/cancelFns` 为空；顺序再调用一次 Stop 安全。但 running Cancel 可先把锁永久占住，且并发第二次 Stop 不等待，详见 R3 两项。原始 **P1-07 仍 PARTIALLY_FIXED**；退出状态和进程监督也未全部闭合。 |
| **R2-P1-02：迁移建索引阻断升级** | **FIXED；P1-05 关闭** | `0005_kernel_review.sql:11` 先收敛再于 `:20` 建索引。真实 goose fixture 先 `UpTo(4)` 并确认版本 4，A 库放 running/pending 两行，B 库放 pending/running 两行并保留一条 succeeded 历史；调用生产 `Store.Migrate` 成功升级到 5。保留每库最小 ID 的活动行，其余变 interrupted，写入原因与结束时间；5 条审计记录全部保留，随后重复活动 INSERT 被唯一索引拒绝，再迁移无错误。 |
| **R2-P1-03：软删除未联动** | **PARTIALLY_FIXED；P1-06 仍 OPEN** | `jobs/jobs.go:476` 的 `loadDatabase` 已加守卫，确实阻止加载隐藏目标。但 **Enqueue `:106` 仍是无条件 VALUES INSERT，没有 deleted_at 检查，本次 diff 也没有改这里**。实测 Get 成功 → 删除成功 → Enqueue 仍返回新 ID → claim 后因 `ErrDatabaseNotFound` 变 failed，凭据/recipient 步骤未执行。反向顺序 Enqueue 先赢时，生产删除 SQL 正确影响 0 行。双向原子保护尚未闭合。 |

迁移 SQL 未发现新的升级阻断或误删历史。`id` 是非 NULL 的全局主键，按库分组的 `MIN(id)` 不会跨库误保留重复 ID。注释称“保留最老项为 pending”，**实际保留原状态**：A 库的 running 仍为 running，启动 `RecoverInterrupted` 才将其标记 interrupted；不会被迁移自动重跑。这是注释不准确，不应把该 survivor 误报为迁移失败。已应用旧 v5 的数据库不会重跑这段修改，但旧索引若已成功存在，本来就排除了重复活动行。

## PARTIALLY 残留复核

| 原编号 | 本轮判定 | 已闭合部分、仍 OPEN 的具体路径 |
| --- | --- | --- |
| **P1-01** | **PARTIALLY_FIXED** | `dumper/dumper.go:396` 只保存 stderr 读错误，未立即统一取消；`:405` 仍先 `wg.Wait()`，之后才等待进程。强制关闭 stderr 读端后，任务仍等 stdout/进程，须外部取消才返回，且返回的 context 错误遮住了 stderr 读错误。另一无 overlay 探针中，`setsid` 脱离进程组的持管道后代在取消 6 秒后仍阻塞，手动 kill 才返回，超过 WaitDelay=5s。残留没有修复。 |
| **P1-02** | **PARTIALLY_FIXED** | `dumper/dumper.go:166` 已删除简单 `password=value` 的 value；实际 fake stderr 验证通过。但 `password='CANARY-secret-pw'` 输出仍为 `password=[REDACTED]'CANARY-secret-pw'`，无标签 canary 也保留。`RedactKnownSecrets` (`:176`) 仍只有定义，没有生产调用。`pgclient/inspect.go:149` 仍只替换标签；`jobs/jobs.go:305` 原样记录 panic。探针确认 panic 日志和持久 task error 都含 canary。 |
| **P1-04** | **REGRESSED** | pending Cancel 现在立即写终态，成功 SQL 的状态/取消条件和 RowsAffected 仲裁也有效。但 running Cancel **丢失 Unlock**，见 R3-P1-01。取消终态处理只包围 `cfg.Run` 错误：在 recipient/连接测试/依赖阶段取消仍走 fail；实测 recipient 阶段为 `failed + cancel_requested=true`。注册后复查与 `ranToCancellation` 的查询错误仍可被忽略。 |
| **P1-08** | **PARTIALLY_FIXED** | 本次未修改 pgclient TLS 构造或 CA/证书策略。URI 白名单 `pgclient.go:36` 无受控 CA/client cert 入口，`pgx.go:24` 仍依赖解析器默认 TLS 配置；清 PG* 不等于禁用默认用户证书文件。所用 pgx v5.10.0 的 `pgconn/defaults.go` 也会探测用户 `.postgresql` 文件。仍缺统一 CA、客户端证书和默认文件策略及真实 hostname/CA 正反例；不能只凭镜像安装 ca-certificates 关闭本项。上轮已指出的自定义 application_name 与 pgx 固定值差异也未修改。 |
| **P1-09** | **PARTIALLY_FIXED（DTO 接线已闭合）** | `jobs/queries.go:117`、`:137` 的 SELECT/Scan 次序一致；`server/api_phase2.go:257`、OpenAPI 和两个生成契约均已接线。真实 SQLite + 直接 API handler 返回正确 `sslMode=verify-full` 及时间字段。但它只表达配置，`pgclient.TestResult` 仍只含版本，无实际是否 TLS / 是否验链 / hostname 校验结果；旧记录返回空字符串。第二轮要求的实际验证状态与未知状态表达仍未完成，不能把 `sslMode` 当作实测 TLS 结论。 |
| **P1-10** | **PARTIALLY_FIXED；R2-P2-02 FIXED** | `jobs/jobs.go:384` 已填非零 DumpStartedAt、RestoreNote、ServerEncoding，并如实填 `restoreVerified=false` 和 FDW 数据不默认覆盖的说明；提交链探针验证序列化值正确。仍没有实际 archive TOC、locale、恢复 profile/明确 restore 工具版本，age 仍只有库名；角色查询 `pgclient/inspect.go:106` 仍漏 type owner、RLS、default ACL grantee 等；依赖与 dump 不共用快照。字段接线修好了，不代表协议 E 已实现完整。 |
| **P1-11** | **FIXED** | `jobs/durable.go:11` 执行同目录安全临时文件 → Write → 文件 Sync → Close → Rename → 父目录 Sync，错误返回调用方；`jobs/jobs.go:419` 使用它，并在成功 SQL 之前完成。普通写入/0600 权限/rename 失败及临时文件清理实测通过。未模拟掉电，也未覆盖每个 Sync/Close 的底层故障；这是验证边界，不再宣称实现仍缺 fsync。崩溃对账残留归 P1-12。 |
| **P1-12** | **PARTIALLY_FIXED** | `jobs/jobs.go:373` 提前保存密文路径/哈希/大小及 committed；manifest 失败保存 committed_no_manifest；条件成功 UPDATE 检查行数并延迟 500ms 重试一次。这些分支均实测有效。但引用保存仍在文件提交之后，且首次引用写入无重试/提交意图。注入该 UPDATE 失败时，磁盘有密文、DB 路径为空，RecoverInterrupted 不会发现它。该函数 `:90` 仍仅改 running 状态，无产物/哈希/manifest 对账；启动清理也不识别遗留 `.durable-*.tmp`。连续成功 UPDATE 失败或提交后取消时，manifest 实际存在但路径未保存、API HasManifest=false。 |
| **P1-13** | **PARTIALLY_FIXED** | config → main `:209` → SetQuota → dumper 已接线，合法 `1048576` 配置实测保留。**`:370` 的检查在 EncryptStream 完成后，不在 countingWriter.Write (`:497`) 内**。1 MiB 预算下先完整输出 8 MiB，观察到临时密文至少 8,325,288 字节才拒绝；另在已有 700 KiB 产物时，37ms 内又成功提交 717,160 字节，总占用 1,433,960 > 1,048,576。独立总预算、写入时硬边界、控制盘余量及规模预检仍未闭合。此外 `config.go:55` 将非法、负值、溢出配置静默变为 0；实测 `1MiB/-1/超大整数` 均 err=nil 且禁用配额，应拒绝错误配置。 |
| **P1-15** | **PARTIALLY_FIXED** | Dockerfile 声明安装 PG14–18 客户端，且本轮没有再修改它；不能把声明当作发布镜像实测。RestoreNote 已写目标限制，改善了说明；`dumper/dumper.go:104` 仍允许任意更高主版本客户端，没有受恢复验证约束的回退组合。仍需限定可用组合并在真实 release image 完成矩阵。 |
| **P2-01** | **PARTIALLY_FIXED（本轮残留未修）** | `jobs/jobs.go:227` 仍只取第一条；`:239` 发现该库忙就返回 false，没有继续扫描。实测第一库 perDB=true、第二库可运行，第二任务仍 pending；ID 保持稳定，也没有自唤醒。注释中的“keep scanning”没有对应实现。当前同步单 worker 正常执行少见该分支，不夸大为日常必然饥饿。 |
| **P2-03** | **FIXED** | `staging/staging.go:61` 改为启动专用接口；生产调用位于实例独占锁之后、worker 启动之前。删除逐项收集并 Join 错误，只有实际成功才加入 removed。构造非空 `.inprogress` 目录阻止删除，同时放正常残留和凭据目录：返回准确的两项 removed 及阻塞项错误，已提交密文保留。原有运行结束 RemoveAll 错误传播也保留。 |
| **P2-05** | **PARTIALLY_FIXED（密文 ENOSPC 残留未修）** | `pgclient/errors.go:32` 能识别 ENOSPC/EDQUOT，但密文错误在 `dumper/dumper.go:442` 仍以 unknown 和 `%v` 包装。将测试临时密文路径指向 `/dev/full` 后，实际写入失败却得到 class=unknown，`errors.Is(err, ENOSPC)=false`。error_code/retryable 仍只有迁移定义，无生产写入/DTO 映射；提交链失败实测仍为空/0。deadline/EIO、SQLSTATE 与固定 locale 矩阵也未补齐。 |

CA/证书判断依据：清理 PG* 并不能消除 libpq 的默认 `~/.postgresql/root.crt`、客户端证书和私钥路径；`require` 还会受到已有 root CA 文件影响。这些行为由 [PostgreSQL SSL 文档](https://www.postgresql.org/docs/18/libpq-ssl.html)明确规定。跨版本回退也不能仅根据 client ≥ server 推断旧版本恢复可行，见 [pg_dump 兼容性说明](https://www.postgresql.org/docs/18/app-pgdump.html)。

## 其余第二轮 OPEN 项

| 编号 | 判定 | 证据与剩余工作 |
| --- | --- | --- |
| **P1-07** | **PARTIALLY_FIXED** | R2-P1-01 的空函数已关闭。但 R3-P1-01 可令 Stop 卡锁，R3-P2-01 令并发 Stop 提前返回；P1-01 的持管道进程仍可能阻止 join。此外本轮 active Stop 探针的任务被写为 failed，重启恢复只处理 running，不会将其改为 interrupted。不能将注释“停机由启动恢复标 interrupted”当作实现。 |
| **P1-14** | **PARTIALLY_FIXED（从 NOT_FIXED 改判）** | `jobs/m1_test.go:136` 的 countRows 已使用 URI 的 `ci.DBName`；`:150` 新增目标库三个 payload 的核对。因此旧“统计源库”的具体假阳性已修。仍用应用 `agekey.DecryptStream`，应用 DB/worker 未先关闭移除，在同一 PG 集群新建 verifydb，未证明独立工具/全新集群恢复；没有破坏恢复目标的负对照和不可 SKIP 的 M1 模式。真实测试本次全部因 Docker SKIP。 |
| **P2-04** | **PARTIALLY_FIXED（残留未修）** | `dumper/dumper.go:336` 第二个 StderrPipe 创建失败只关闭 dumpOut 读端，StdoutPipe 保存在 cmd.Stdout 的写端未显式关闭，且没进入 Cmd.Start 的清理路径；该分支无修改。本轮为源码确认，未声称实际制造了 EMFILE。 |
| **R2-P2-01** | **NOT_FIXED** | `0004_backup_kernel.sql` 的 databases.name 仍全表 UNIQUE，软删除未释放名称。实测删除后同名 INSERT 仍报 UNIQUE constraint failed，而隐藏目标已不在列表；本次没有名称重用策略变更。 |

P1-05、P1-06 的判定见前述迁移/软删除实测，不另重复计数。第二轮已 FIXED 的其他项不在本轮扩审范围内。

## 本轮新增回归

### R3-P1-01：running Cancel 返回前没有释放 r.mu

**位置：** `jobs/jobs.go:204` 至 `:219`，关联 P1-04/P1-07。本次 diff 删除了原有 `r.mu.Unlock()`，两个 running 分支均没有补回。

Cancel 的 SQL 更新成功后拿锁，有已注册 cancelFn 时调用它，无 cancelFn 时启动补查 goroutine，随后直接 return。前者让 worker 完成 `runJob` 后卡在 `:280`，后者还会让补查 goroutine 卡在它自己的 Lock；Stop 则卡在 `:167`。一次取消即可使唯一 worker 后续调度停止，关停也不能完成。

**实测：** worker 阻塞在支持取消的 recipient 回调；调用真实 Cancel 返回 nil，context 确实取消，但 `r.mu.TryLock()` 失败，随后 Stop 200ms 内不返回。只有测试主动解锁救援，Stop 才完成。救援后 DB 状态为 failed、cancel_requested=true，还复现了取消早期阶段的终态残留。该探针不依赖 Docker、网络或 overlay。

**修复要求：** 保证每次成功 Lock 均释放；取出 cancelFn 后在锁外调用，补查遵守生命周期并被管理。用包含 running Cancel → worker 清理 → 后续任务 → Stop 的永久回归测试覆盖两个注册窗口；不能只测 pending Cancel。

### R3-P2-01：并发第二次 Stop 不满足返回前已 join

**位置：** `jobs/jobs.go:168` 至 `:170`，关联 P1-07。

首个调用设置 stopped=true 后释放锁，随后取消并等待；第二个调用一看到 stopped 就返回，没有等待同一个完成信号。**实测**让 worker 在取消后等待一个清理屏障，首个 Stop 未返回时，第二个 Stop 已返回。若调用方据此关闭 store，worker 仍可能访问它。

当前 main 的单次 defer Stop 不直接触发该并发场景，因此定为 P2，并与顺序重复调用已通过区分。修复应让所有 Stop 调用都等待同一完成事件；“取消只执行一次”和“每个调用都等待结束”是两个要求。

## 指定的新回归检查结果

| 检查点 | 结论 |
| --- | --- |
| GET/List/Scan 列序 | **通过**：server_version → sslmode → created_at → updated_at 的 SELECT/Scan 顺序一致；真实 fixture 的版本、模式、111/222 时间值及直接 API JSON 都正确。未发现新增列错位。 |
| 迁移收敛 SQL | **通过**：真实 v4 双活动行升级到 v5、审计保留、唯一索引拒重、重复迁移均验证；注释与 survivor 状态不一致已单独说明，不误判为升级失败。 |
| successSQL 条件 | **条件与仲裁通过，提交恢复仍部分完成**：正常 running/未取消可成功；在保存 artifact 时用 SQLite trigger 设置 cancel_requested 后，成功 UPDATE 影响 0 行，最终 canceled；同一位置改为 interrupted 后不会被覆盖为 succeeded。首次成功 UPDATE 用 trigger 失败、第二次恢复时约 527ms 后成功；持续失败则 failed 且密文引用保留。提交后取消不保存 manifest_path 的残留归 P1-12，不能把整个提交链判为通过。 |
| spike 脚本、CI、Dockerfile 未动 | **确认无 diff**：两个 spike、CI、Dockerfile 在 0641b30 和 41d3c91 的 Git blob 相同。语法检查通过；Spike 2 运行受 Docker 权限阻塞，Spike 1 无真实 Supabase 资源，因此没有新增运行成功证据。 |
| 新增生命周期代码 | **发现 R3-P1-01、R3-P2-01**，不能因 Stop 非空、pending Cancel 通过就忽略 running 与并发关停分支。 |

## OPEN 清单、外部资源与门禁

仍 OPEN 的旧编号如下，修复条件沿用第二轮报告，本轮已验证关闭的子问题不应重新要求实现：

- **P1：** P1-01、P1-02、P1-04、P1-06、P1-07、P1-08、P1-09、P1-10、P1-12、P1-13、P1-14、P1-15。
- **P2：** P2-01、P2-04、P2-05，以及第二轮额外回归 R2-P2-01。
- **本轮回归定位：** R3-P1-01、R3-P2-01，归入上述取消/关停项，不重复统计。
- **已关闭：** P1-05、P1-11、P2-03；具体回归 R2-P1-01、R2-P1-02、R2-P2-02。R2-P1-03 仅部分关闭。

外部资源/执行环境待补项：

| 项目 | 所需资源与验收证据 |
| --- | --- |
| 全量 race 与真实服务关停 | 允许本机 listener 的环境；重跑 server suite，并覆盖活跃 PG 备份时 SIGTERM、进程退出、凭据清理和 SQLite 关闭顺序。当前 listener 拒绝是环境限制，不是业务断言失败。 |
| 真实 PG 与 M1 | 可用 Docker/PG；独立 age/pg_restore 工具；移除应用/SQLite 后在新集群恢复，核对数据、结构、sequence、owner/ACL，并有损坏目标负对照。**不可 SKIP 的 CI 模式是仓库工程缺口，不只是缺 Docker。** |
| TLS/CA 一致性 | 可控 CA、正确/错误 hostname、受信任/不受信任证书及客户端证书 fixture，分别验证 pgx/libpq。统一证书配置入口与实际状态记录仍是代码工作。 |
| PG14–18 发布镜像矩阵 | 可构建真实 runtime image 的环境及所需镜像/包源，验证各 server/client/restore 组合。安装声明与恢复说明不能替代矩阵。 |
| Spike 1 / Spike 2 | Spike 1 需要授权的 Supabase 测试项目及连接资源；Spike 2 需要可用 Docker 与 runtime-spike 镜像。当前未形成新的恢复成功证据。 |
| 耐久性/底层故障矩阵 | 可注入 Sync/Close/目录 fsync、BUSY/ENOSPC/EDQUOT/EIO 及崩溃点的测试环境；本轮完成普通 durable-write、rename 错误和实际 `/dev/full` 写失败，未冒称覆盖掉电。 |

**Phase 2 代码：未通过。工程门禁：未通过。总体：未通过。** 构建、vet、格式、契约生成和前端检查通过，迁移与多个修复已有正面证据；但新增锁泄漏可以直接停止唯一 worker，仍可复现秘密泄漏、管道不收敛、删除后入队、配额超写和无引用密文，均不能归咎于外部环境。故不适合判为“修改后通过”。应先修复这些代码路径并补永久回归测试，再在可执行环境完成不可跳过的 M1、TLS、真实关停与镜像恢复门禁。
