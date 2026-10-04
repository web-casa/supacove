# Phase 5 + 6 修复复审（第三轮）

评审日期：2026-10-05。基线 `7986963`；被评审提交及工作树 HEAD `ac1587ab6fc89ae5e57e7da2433761d9106d5833`。比较范围：`git diff 7986963..ac1587a`。承接 `docs/reviews/codex-phase5-review-2.md`，所有产品代码行号均指向 `ac1587a`。本轮只写报告，不修改产品实现。

**总体结论：仍不通过 Phase 5/6 完成验收。** 第二轮新增编号中，R2-P1-02（零表丢失）和 R2-P2-01（下载 symlink 越界）可以关闭；R2-P1-01（PG 生命周期）为 **PARTIALLY_FIXED**，启动失败后的部分启动实例仍没有停止。13 个原始编号为 **FIXED 2 / PARTIALLY_FIXED 11 / NOT_FIXED 0 / REGRESSED 0**；P1-06 从第二轮 REGRESSED 改判 PARTIALLY_FIXED，但尚不能关闭。另发现修复引入的后台解密失控、重复入队释放他人 lease 等问题，见新增编号。前向迁移收敛已修复，新增 Down 有回退回归。

## 验证范围、方法与限制

遵守环境限制：没有启动监听服务；Docker 不可用。全部 Go 命令使用 `GOCACHE=/tmp/codex-phase5r3-cache`。临时探针使用现有测试 harness、SQLite、真实 age 加解密、shell PG stub、受控假存储后端和文件系统，不假装真实 PG 已恢复或已停止。探针执行后从仓库删除，副本保存在 `/tmp/codex-phase5r3-probes/`。

| 检查 | 结果与证据边界 |
| --- | --- |
| `go build -buildvcs=false ./backend/...` | 退出 0，日志 `/tmp/codex-phase5r3-build.log`。 |
| `go vet ./backend/...` | 退出 0，日志 `/tmp/codex-phase5r3-vet.log`。 |
| `go test -race ./backend/...` | 其他有测试包通过，无 race 报告；server 在 `TestPhase2DefaultDenyAndAgeStatus` 因 `httptest` 监听 `[::1]:0` 被禁止而 panic，整体退出 1。日志 `/tmp/codex-phase5r3-tests.log`，全套不得记 PASS。 |
| 指定 stub-PG / recovery / jobs / upgrade 用例，`go test -race -v ... -run ...` | 实际执行的单测通过；两个 `TestM1_` 因 Docker 不可用 SKIP。日志 `/tmp/codex-phase5r3-targeted.log`。 |
| 临时 `TestReviewR3*` 探针 | 20 个顶层测试执行通过，无 race 报告；其中部分 PASS 是断言缺陷存在。日志 `/tmp/codex-phase5r3-{probes,extra,budget,down}.log`。 |
| libpq 本机解析验证 | Python ctypes 调用真实 `PQconninfoParse`，`postgresql://db/db?%70assword=review_canary` 被解析为 `password=review_canary`，不需要数据库或网络。真实生成脚本配合 stub 另证密码进入客户端 argv。 |
| 历史升级 | 原版 0004 schema + 版本集合 `{1,4,5…9}`、`{1,4,5…12}` 升到当前版本，`duration_secs`、`verify_profile`、`auth_generation` 均存在，重复 Migrate 成功。 |

尚无正式 runtime 内远端读取→解密→真实恢复→精确对象/数据断言→确认销毁的闭环，也无三平台新目标手动恢复证据。Go race detector 没有发现内存数据竞争，不等于 SQLite/队列/文件删除的逻辑竞态已关闭。

## 第二轮新增编号逐项判定

| 编号 | 判定 | 证据与范围 |
| --- | --- | --- |
| R2-P1-01 | **PARTIALLY_FIXED** | `backend/internal/verifier/verifier.go:157,202–218,259` 命名返回 + 单一 deferred 清理确实覆盖启动成功后失败/unsupported/取消。`:256–259` 在 start 报错时未设置 `pgStarted`，仍直接删除可能活跃的 pgdata；新解密取消方案另引入后台工作未 join。 |
| R2-P1-02 | **FIXED** | `backend/internal/manifest/manifest.go:83` 去掉 omitempty；`backend/internal/jobs/verify.go:279–282` 将显式 0 传给引擎；`backend/internal/server/api_phase2.go:287–291,339–342` 返回非 nil 的 verifyTables=0。零表探针及生成脚本的零表匹配/不匹配测试通过。这只关闭零值丢失，不关闭对象集合证明。 |
| R2-P2-01 | **FIXED** | `backend/internal/server/api_phase3.go:183–210,267,294` 两种下载均走 `os.OpenRoot` + root.Open。Linux 上文件 symlink / 父目录 symlink 越界探针均被拒绝，目录内相对 symlink 可读。没有把残余的特殊文件检查缺口算作 symlink 回归。 |

### R2-P1-01：已恢复哪些保证，仍在哪里失败

`verifier.go:202–218` 在 run context 已取消时仍建立独立清理 context，先 stop，再删除明文和工作目录；停止不确认则保留目录。`:208–212` 把原 verified 改为 failed。`stopConfirmed`（`:379–397`）无 PID 文件时返回成功，有 PID 文件时 fast stop，失败后 immediate stop。`CleanupResidual`（`:97–120`）先停止残留 marker 对应实例，停止不确认则保留，不再无条件 RemoveAll。这些控制流是实质修复。

**仍有 P1 阻断：start 非零不等于 postgres 没启动。** `pg_ctl -w start` 可能已经 fork postmaster、写 PID，再因启动等待超时/ctx 取消退出。`verifier.go:256–259` 只在命令成功后设 `pgStarted=true`；此时 deferred 不调用 stop，直接删目录。`TestReviewR3StartFailure` 用 start 失败且 PID marker 存在的 stub 实测：failed、stop 0 次、workdir 已删除。该证据证明控制流，不声称 stub 是活 PG。应从尝试 start 起承担清理责任，或在所有出口检查 PID 并执行有界 stop；PID 检查错误不能一律当作不存在（`:400–402` 目前对任意 Stat 错误都返回 false）。

停止失败保留以后仍可继续处理下一次验证；启动 `CleanupResidual` 失败在 `backend/cmd/supabackup/main.go:257–260` 只 warning 后继续。这意味着“单 worker”还不能保证“最多一个活 PG”。磁盘预算仍是压缩密文 2 倍 + 256 MiB（`verifier.go:341–357`），没有展开数据/WAL 的执行期上限。Duration 在 deferred stop 前计算（`:309`），不包含最多约 30 秒停止阶段。

`hashFile` 的分块复制（`:407–445`）确实检查 ctx，但只在读写块之间，不能打断阻塞 Read。`decryptToFile` 则只让调用者提前返回，实际解密 goroutine 不观察 ctx，不关闭/等待底层复制，见 R3-P1-01；不能把它称为已实现完整 Stop/预算收敛。

### 四个指定 PG 测试究竟证明什么

| 测试 | 判定 |
| --- | --- |
| `TestStopOnRestoreFailurePath`，`verifier_test.go:357–370` | 有效证明 pg_restore 失败后调用 stop，成功停止后工作目录消失。 |
| `TestStopOnUnsupportedPath`，`:375–389` | 有效证明缺扩展 classified unsupported 时调用 stop。未断言目录删除或真实进程退出。 |
| `TestFailedStopPreservesEvidence`，`:395–421` | 有效证明停止失败保留目录，之后 sweep 可回收；**没有证明 verified→failed 降级**：`verifyInput`（`:347–352`）未设置 ExpectedTables，默认 0，而 psql stub（`:289`）输出 3，原结果在停止前已是 count mismatch failed。补充探针设置 ExpectedTables=3 后确实到达 restored 结果，stop 失败把它降为 failed 并保留目录。产品降级逻辑正确，原断言不足。 |
| `TestCleanupResidualStopsLivePID`，`:426–444` | 写文件内容 `1`，并无活 postmaster。只证明检测 PID marker→调用 stop→删除目录的顺序，不能证明“live PID 已停止”。 |

stub 在 initdb 阶段就 touch PID（`:266–272`），start 永远返回 0（`:276–286`），所以四项都漏掉部分启动失败。`stopCalls`（`:302–313`）只确认至少一次 stop，不能证明 fast/immediate 全部调用次数；日志格式（`:265`）使用双反斜杠换行，不能依赖其按真实换行计数。补充取消 restore 探针已证明 ctx 取消后仍调用 stop，真实进程行为仍待集成环境。

## 原始 13 项复审

| 编号 | 判定 | 当前提交证据 / 未关闭内容 |
| --- | --- | --- |
| P1-01 | **FIXED** | `backend/internal/recovery/kit.go:147,165,172` 客户端命令仍是单行；`kit_test.go:267–271` 现在日志含工具名，实际执行通过。 |
| P1-02 | **PARTIALLY_FIXED** | `kit.go:98` 拒绝无空格、带空格和大小写 pattern；`:137–144` 私有临时目录/trap 保持有效。URI 百分号编码密码字段仍绕过，详见下文。 |
| P1-03 | **PARTIALLY_FIXED** | `kit.go:110–135,147–157,165–174` 哈希门禁、失败传播保持；`:147` 仍只以用户表判非空，纯函数/视图/序列目标可通过，缺版本/角色/profile 依赖预检。 |
| P1-04 | **FIXED** | `backend/cmd/supabackup/main.go:238–265`、`backend/internal/config/config.go:122–138` 生产接线和显式启用仍在；本 diff 未破坏。正式镜像实际运行本环境未验证。 |
| P1-05 | **PARTIALLY_FIXED** | `jobs/verify.go:191–225,244–282` 新增远端密文 fallback，但不下载 manifest；本地路径非空而文件已丢失也不 fallback，bucket-only 仍 skipped，详见下文。 |
| P1-06 | **PARTIALLY_FIXED** | 成功启动后的 deferred stop 已恢复，`jobs/jobs.go:425–429` sweep 加入 WaitGroup；仍有部分启动失败删除活数据目录、解密 goroutine 未 join、预算不足。 |
| P1-07 | **PARTIALLY_FIXED** | `verifier.go:498–512` env 白名单保持，未新增每来源可信门槛/已测试扩展版本白名单/UI 信任告知；`jobs/jobs.go:916–917` 成功任务仍按全局配置进入验证。 |
| P1-08 | **PARTIALLY_FIXED** | `jobs/verify.go:267–282,318–319,349–353` 缺/坏 manifest skipped、零表/profile 改善；`verifier.go:322` 仍把数量匹配称为对象集合匹配，未核对对象名/角色/版本/恢复选项。 |
| P1-09 | **PARTIALLY_FIXED** | `upload.go:267–268,291–294,393–408` pending/running、已有 lease、本地最新 verified 都获得顺序场景保护；原子删除权未实现，重复入队可移除活 lease，kit 仍没有桶内交付。 |
| P1-10 | **PARTIALLY_FIXED** | `platform/platform.go:107–121` 范围文案保留；`jobs/jobs.go:379–383,686,901` 仍按 host 检测，忽略保存的平台覆盖；无三平台 profile/目标恢复演练。 |
| P2-01 | **PARTIALLY_FIXED** | `platform/platform.go:19–48,77–95` 域名边界已修复；`server/api_phase2.go:151–157` 注册 warning 保留，前端展示、编辑/测试流程诊断仍未接入。 |
| P2-02 | **PARTIALLY_FIXED** | `jobs/jobs.go:386–390,425–429` 重启成功写 platform+verify 状态、nil verifier sweep；`verify.go:356–360` 有界终态重试。仍缺原子 claim/重启可靠脱敏/运行期持久失败收敛；HasRecoveryKit 与下载能力仍不一致。 |
| P2-03 | **PARTIALLY_FIXED** | PG failure/unsupported/PID-marker、新队列/零表/最终 psql 等测试有价值；缺真实恢复销毁闭环，FailedStop 降级和 MissingShaTool 测试仍有假阳性，详见下文。 |

### P1-02：扩大 glob 仍不是 libpq 密码字段校验

`recovery/kit.go:98` 的新增模式能拦第二轮确认的 `password = '…'`，现有 `TestInlinePasswordRefused`（`kit_test.go:310–323`）实际通过。但连接 URI 的 query key 可编码：

```text
postgresql://db/db?%70assword=review_canary
```

本机 libpq 的 `PQconninfoParse` 明确返回 password 字段，真实脚本 + 工具 stub 成功执行，argv 日志含 canary。因此“密码只走 PGPASSWORD”仍不成立；这是第二轮已提出的残余，不另记新引入。建议限制支持的连接格式并使用对应解析器校验密码字段。[PostgreSQL libpq 连接字符串文档](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING) 描述 URI 百分号编码与 keyword/value 空白语法。

### P1-08 / R2-P1-02：零值已修复，证据措辞只修了一半

`manifest.go:83,125–134` 已能区分新 manifest 的 0 与旧 manifest 缺字段，worker 探针收到 ExpectedTables=0。`verify.go:267–277` 对文件缺失和 JSON 语法坏记录 skipped，不再执行引擎；指定 MissingManifest 与临时坏 JSON 探针通过。`verifyProfileOf` 和终态 SQL（`:318–319,349–353`）把结果 ServerVersion 交给 `verifier.ProfileName`，版本证据不再固定常量。

然而 `verifier.go:321–322` 新措辞仍是 `manifest-declared object set matched`，条件仅为 ExpectedTables>=0，实际验证（`:284–300`）只检查数量及扩展名。正确数量但错误表名、缺视图/函数/序列仍无法识别。旧 manifest 无 tableCount 的分支（`:323–325`）已诚实说明缺 cross-check；新 manifest 分支仍虚报集合证明。应写“table count matched”，或真正保存并核对对象集合。PGDump 前独立连接采集 deps 的流程（`jobs/jobs.go:734–744`）也没有变为同一 dump 快照。

### P1-05：新增远端 fallback 未形成桶内独立恢复链

`jobs/verify.go:244` 只在数据库 artifact_path 为空时下载。路径非空但文件已被外部清理时仍让 verifier hash open 失败，不回退。`pruneLocalArtifacts`（`upload.go:418–420`）同时清空 artifact_path 和 manifest_path；新逻辑成功下载密文后，`:267` 继续读空/缺失本地 manifest，必然 skipped。探针保留远端两对象而清空两条本地路径：remote Get 成功，结果仍是 manifest unreadable、engine 调用 0。没有实现远端 manifest 获取，更没有从桶读回精确数据的证明。

此外 fetch 在 `verify.go:247` 传 lifeCtx，直到 `:288` 才创建 verifyTimeout，下载不计入验证预算。见 R3-P2-02。正常仍有本地密文时不读取远端恢复，组合证据的范围应继续明确。

### P1-09：顺序保护成立，“单一 map 操作关 TOCTOU”不成立

enqueue 先设 map 的确延长了 lease 到 queued（`verify.go:98–104`）；满队列的**不同 job**会正常 release（`:136`），已有 pending/running 会被远端/local retention 跳过（`upload.go:267–268,407–408`），本地最后 verified 锚点（`:393–402`）也已补齐。现有 queued/prune 测试和本轮 local-only anchor 探针通过。

但 map 操作是无条件赋值，没有 check-and-set、更没有独占 deletion lease。`deleteRemoteBackup` 的 `verifyBusy` 在 `upload.go:291` 检查完成即释放 mutex；`:299–327` 删除远端、本地和清空引用都在锁外。受控 Delete 屏障探针：删除通过 busy 检查 → enqueue 成功持锁 → 放行 Delete → 仍清空该 job 的本地路径并写 deleted，且 map 仍 busy。此为第二轮删除 TOCTOU 的未关闭部分，**不需要 Go data race**。

本地 `:407` 检查到 `:414–420` 删除仍是同类窗口。需要验证/删除共享互斥的 claim；单纯在 reader 更早赋 true 不能赋予 remover 原子删除权。重复入队的 bool lease 所有权错误见 R3-P1-02。

**锁顺序检查：本 diff 未发现 retention 与 verifyBusy 的死锁。** Query rows 在调用 verifyBusy 前已关闭（`upload.go:246,388`），verifyBusy 只短暂取得 `r.mu`（`verify.go:369–372`），acquire 也在 DB Exec 前解锁。这里的问题恰是互斥范围过短，而不是 SQL 与 mutex 相互等待。

### P2-02：状态与 join 的实质改善和残余

`ResumeRemotePhase` 成功 SQL（`jobs.go:386–390`）新增 platform、verify_status、verify_detail，使用与正常 runJob 相同的 `initialVerifyState`（`verify.go:85–93`）。sweep 无条件启动并 Add/Done（`jobs.go:425–429`），nil verifier/无 identity 时 settled skipped（`verify.go:166–174`）；相关启动测试通过。Stop（`jobs.go:457–468`）取消 lifetime 并等 worker+sweep，原“sweep 未加入 wg”可以关闭。

`finishVerification`（`verify.go:356–360`）实做三次 Exec。暂时失败 trigger 在 250ms 后撤销的探针中，第三次成功写 verified；不再是一次失败立即放弃。永久失败仍只 log、释放 lease，过渡状态无当前进程重试队列，依赖重启；sweep 遍历和 Exec 不观察 ctx（`:171–178`），大量失败项会逐个消耗约 1.2 秒，虽然 join 成立，Stop 无整体时限。worker 可先退出、sweep 再入队，留下 pending 队列和内存 lease 到 Runner 销毁，并非终态运行期收敛。

`acquireVerifyLease` 与 running UPDATE 都不检查 RowsAffected（`:102–109,231–235`），重复/终结任务仍执行。重启链 `pgclientSanitize`（`:184`）仍调用 `pgclient/inspect.go:159–166` 的字符串前缀替换，不能去掉实际 password 正文，未沿用原任务 knownSecrets。上述残余与新 bool 所有权问题需一起修复。

`HasRecoveryKit`（`queries.go:11–13,83`）现在能拒绝丢失文件/目录；但 `os.Stat` 跟随越界 symlink，也不检查可读性和 staging containment。探针在 DB 记录 staging 内指向外部普通文件的 kit symlink，API-safe task 宣称 available，而下载受 OpenRoot 拒绝。应让 availability 和下载共享可打开的文件规则。

## 新引入问题与回归

### R3-P1-01 — REGRESSED：取消解密只放弃等待，后台复制不停止、不 join

位置：`backend/internal/verifier/verifier.go:454–489`；底层 `backend/internal/agekey/agekey.go:85–97`。

新增 goroutine 执行没有 ctx 的 `DecryptStream`，ctx.Done 分支立即返回，源/目标文件 descriptor 只有 goroutine 自己完成后才关闭。大密文解密被取消后仍占 CPU、FD、磁盘；Linux 上删除 workdir 不会终止对已 unlink 文件的写入或立即释放其空间。单 worker 可以开始下一次验证，Runner Stop 只等待 worker，不等待这次解密。

`TestReviewR3DecryptAbandoned` 在已 canceled ctx 下调用 decryptToFile 得到错误，再向 FIFO 送入真实 age 密文，后台 goroutine随后消费并写出完整明文，证明“返回 canceled”与“工作停止”不是一回事。FIFO 只是可控阻塞读的测试装置，不是利用前提。应让实际解密读写观察 context，取消时停止底层 I/O 并等待关闭完成，不能只丢弃 channel 等待。

### R3-P1-02 — REGRESSED：重复请求 + overflow 可清掉现有活 lease

位置：`backend/internal/jobs/verify.go:98–109,113–116,130–137,231–235`。

bool map 没有持有者检查：同 job 可多次写 true、排多条请求；overflow 的 release 会 delete 同 job 原本队列/执行持有的 true，然后写 skipped。完成其中一个重复请求也会提前释放其余请求的 lease。启动 sweep 与正常 enqueue 并行（`jobs.go:405–428,916–917`），没有原子去重，属于可达控制流，不能只假定每 job 只会入队一次。

探针同 job enqueue 5 次：队列仍有 4 条该 job 请求，verifyBusy=false、DB=skipped；另探针对已 verified job enqueue，pending/running UPDATE 影响 0 行，仍执行引擎并将终态覆盖 failed。原先缺 dedup 已是残余；本轮把 lease 移到 enqueue 后，overflow **释放其他请求 lease** 是新增破坏。应在 mutex 下拒绝已有 lease，并原子 claim 持久状态、检查 RowsAffected；release 必须属于取得 lease 的那个请求，overflow 不应改写其他请求状态。

### R3-P2-01 — REGRESSED：pending SQL 失败泄漏无工作持有的 lease

位置：`backend/internal/jobs/verify.go:99–107,130–131`。

先写 map，SQL 失败返回 false，却未 release；没有排队 worker 会释放这个 lease。SQLite fail trigger 探针：queue=0、busy=true。既可能长期阻止 retention，也把 job 留在等待重启的过渡状态。应回滚本次获取，记录或安排可恢复状态；不能让失败请求占住无主 lease。

### R3-P2-02 — REGRESSED：新增远端下载绕过 verifyTimeout，关机被记终态 skipped

位置：`backend/internal/jobs/verify.go:194–225,247–252,288–305`。

新增下载使用 runner lifetime context，没有验证 deadline；io.Copy 也没有主动 context 检查，依赖 backend reader 行为。10ms verifyTimeout + 遵守 context 的慢 Get 探针实耗约 200ms，直到 runner context deadline 才结束。大型/慢速工件可占用 worker 和 staging，完全不受该任务 5 分钟默认预算约束。shutdown 发生于 fetch 时走早期 skipped，而不是后面的 shutdown→pending 逻辑。应在加载/下载前建立整个 verification 的 deadline，并统一 shutdown 收敛；同时补 remote manifest 和文件缺失 fallback。

### R3-P2-03 — REGRESSED：0013 的 no-op Up 对应破坏性 Down

位置：`backend/internal/db/migrations/0013_jobs_duration_repair.go:25–26,33–45`。

fresh 库已由 0004 建 duration_secs，所以 0013 Up no-op；但 Down 无条件删已有 duration_secs。探针 fresh Migrate→provider.Down（13→12）后列消失，v12 所需 stats 查询将失败，既有 duration 数据也被删。这是迁移回退路径回归，未观察到主服务自动调用 Down，不能扩大为正常升级会丢数据。修复型迁移应避免删除早期版本拥有的列，或可靠记录本迁移是否创建该列；如不支持回退，应明确禁止而不是提供错误 Down。

## os.OpenRoot 平台语义与剩余边界

Linux 实测确认受约束打开阻止 leaf/父目录 symlink 逃逸，文件句柄在 Root.Close 后仍可用于响应。项目 `go.mod:3` 要求 Go 1.26，OpenRoot 的最低 Go 1.24 不是新增构建兼容问题。

[Go os.Root 官方文档](https://pkg.go.dev/os#Root) 说明它允许目录内相对 symlink、拒绝绝对 symlink；js 平台的 symlink 校验有 TOCTOU，plan9/js 不跟随目录重命名，且不限制 mount、/proc 或设备文件。当前 Linux 服务不应因为 js 限制重新打开已修复的 symlink 逃逸；也不能把 Root 当作特殊文件沙箱。OpenRoot 本身会跟随 staging 根路径的 symlink，根配置必须可信。

`server/api_phase3.go:206` 只检查 IsDir，**没有检查 Mode().IsRegular()**。FIFO 探针被接受；无 writer 的 FIFO 会阻塞在 root.Open，之后 handler 也不使用请求 context 中断。此前下载也是 IsDir 检查，所以这是残余边界缺口，不另记此次新引入。建议用适当的非阻塞打开/类型策略，再检查 fd 的普通文件属性；仅在阻塞 open 之后 Stat 不足以拒绝 FIFO。

## 迁移收敛判定

| 目标 | 判定 | 证据 |
| --- | --- | --- |
| 0013 前向补 duration_secs | **FIXED** | `0013_jobs_duration_repair.go:20–30` 先查 pragma，有列 no-op、无列 ADD，旧库和 fresh 均通过。 |
| 旧版本表缺 3 的乱序升级 | **FIXED** | `migrate.go:78–83` WithAllowOutofOrder(true)；0003 条件补列（`0003_auth_generation.go:19–30`）可晚执行。本轮 `{1,4,5…9}` / `{1,4,5…12}` 探针实际收敛。 |
| TestUpgradeFromOriginal0004 fixture | **FIXED**（所覆盖的升级路径） | `upgrade_test.go:216–261` schema SQL 与 `git show 13a26c1:.../0004_backup_kernel.sql` 一致（省去原文件头说明注释）；`:269–287` 使用历史 provider 并确认无列，`:296–301` 当前升级成功后查询列。原测试仅 `{1,4}`，本轮补测更晚版本缺口。 |
| 历史文件不可改约定 | **PARTIALLY_FIXED** | 本次 diff 只新增 0013、未继续改旧 SQL；但 0004 retro-edit 与 `0008_review_fixes.sql:14–15` 的 Down ADD 仍在。前向补列关闭升级事故，不等于修复所有历史 Down。 |
| 0013 rollback | **REGRESSED** | 见 R3-P2-03；不能把前向测试通过推广为回退安全。 |

## 测试证据有效性补充

recovery harness 新增工具名（`kit_test.go:150`），现在 hash/nonempty 分支的未执行 pg_restore 断言有意义；新增最终 psql 失败（`:374–380`）确实在 restore 之后失败；已知零表测试（`:386–394`）覆盖 0→0 成功、0→3 拒绝，均可认可。

**TestMissingShaTool 仍是假阳性。** `kit_test.go:177–180` 放不可执行的同名文件，只会使 PATH 搜索继续；`:205` 仍包含 `/usr/bin:/bin`。实际 `-v` 日志明确是 ciphertext SHA-256 mismatch，并非缺哈希工具，测试却 PASS。需要构造真正没有哈希工具的 PATH 并断言对应错误。PG FailedStop 断言不足已见前文；其余 marker/stub 测试应与真实 PostgreSQL 集成证明分开记录。

## 最关键的 3 件事

1. **关闭进程和后台 I/O 的生命周期缺口**：尝试 start 后所有出口都确认停止，清理失败阻止再开实例；解密实际停止并 join；把远端下载纳入同一预算。否则 Stop 完成仍可能有后台工作或失去 PG 回收证据。
2. **建立有所有权的验证/删除 claim**：去重与 RowsAffected 检查、失败获取回滚、overflow 只释放本请求 lease、retention 原子取得删除权；让 verified/queued 工件保护在并发场景也成立。
3. **让交付声明与验证证据一致**：彻底解析拒绝内联密码，改正对象集合措辞，补 remote manifest 与桶内恢复闭环；修正 migration Down 和假阳性测试，再在可运行正式 PG 的环境验证恢复数据及确认销毁。

本轮可关闭零表丢失、Linux 下载 symlink 越界、前向旧库缺列，以及 startup sweep 未 join 等具体子问题；**不能宣称 P1 清零或 Phase 5/6 已完成。**
