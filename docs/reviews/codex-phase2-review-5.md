# Phase 2 备份执行内核复审（第 5 轮，收敛确认）

评审日期：2026-10-03。实际 HEAD：`9609e17bdebfcfab739bf102c2c9397b55bd9c79`。累计范围：`git diff 41d3c91..9609e17`；本轮修复归因：`git diff 143d622..9609e17`。依据为[第四轮报告](codex-phase2-review-4.md)及本轮指定的七项验收。使用 `code-reviewer` 技能；仅新增本报告，未修改生产代码或提交永久测试。

**最终结论：未通过。七项判定：FIXED 2、PARTIALLY 4、NOT_FIXED 1。** Cancel 锁泄漏、v4→v5 级联删除 jobs 两个具体问题均已关闭，不能再以同一问题域的其他缺陷否认这两项修复。仍有原范围内的取消终态、秘密脱敏、总配额、恢复对账和负对照残留；新增确认一项软删除名称碰撞回归，另确认上一轮 Down 回归未关闭且退化为静默回退不完整。未确认 P0。

FIXED 仅表示对应问题本身关闭。真实 Supabase、联网 CI、双架构、掉电实验，以及明确接受的 libpq HOME 文件探测、TOC 采集待 P5，均不作为代码缺陷或本轮阻断条件。上轮未列出的额外强化只可记 NOTE。本报告的残留均有上轮记录或本次明确要求，不追加多 worker 调度、全表逐行内容核对等验收条件。其余未改动历史问题不在本轮逐项重审范围，也不因省略而自动关闭。

以下 `jobs/`、`db/`、`dumper/`、`server/` 等路径均省略 `backend/internal/`；行号指 `9609e17`。

| 指定项 | 判定 | 本轮证据及关闭边界 |
| --- | --- | --- |
| **R3-P1-01：running Cancel 丢 Unlock** | **FIXED** | `jobs/jobs.go:226`–`:253` 两条分支均在锁内取出并 delete，锁外调用 cancelFn。实测完整链路：running Cancel 返回、子进程回收、临时密文/凭据清理、worker maps 清空、同库后续任务 succeeded、Stop join，未用测试侧 Unlock 救援。注册窗口回调可再次取得 mutex；lifeCtx 取消后轮询退出。 |
| **R4-P1-01：v4→v5 清空 jobs** | **FIXED** | `0005_kernel_review.sql` 已无父表重建。真实 goose/SQLite 升级后 jobs **5→5**；另一组升级加 API fixture 中 **3→3**，历史 ID、状态、密文路径、哈希、大小、manifest 引用均保持，重复活动行按原规则变 interrupted，外键检查通过。Delete 204 后名称变 `reuse (deleted #1)`，同名重建 201、重复活名称 409。名称边界和 Down 单列，不重开“级联丢 jobs”。 |
| **P1-04：loadDatabase/凭据解密阶段取消** | **PARTIALLY** | `jobs.go:379` 的 loadDatabase 错误分支已正确 finalizeCanceled，握手后已取消 context 实测得到 canceled；连接测试和依赖采集取消也保持有效。但 `:387`–`:396` 的解密/URI 解析失败仍直接 fail。实测解密阶段收到真实 Cancel，随后真实解密报错，最终仍是 `failed + cancel_requested=true`。 |
| **P1-02：CreateDatabase 422、成功 stderr 脱敏** | **PARTIALLY** | 普通 canary 的 API 422 及成功日志接线均已通过，不能再称“未接线”。但上轮已列出的合法单引号转义口令仍泄漏后缀；本轮新增成功日志字段使该后缀实际进入日志。详见下文，仍归同一 P1-02，不重复编号。 |
| **P1-13：写前硬边界、非法配置** | **PARTIALLY** | 单文件写前预检和非法配置拒绝两个指定子问题均 **FIXED**：quota=1024 时 Write(4096) 返回 n=0、底层0字节；非法、负数、溢出配置均报错。上轮明确列出的“已有文件未扣减总预算”仍在：已有700 KiB时又成功提交717,160字节，总量 **1,433,960 > 1,048,576**。 |
| **P1-14：有效负对照** | **NOT_FIXED** | 新增测试确已执行过，但 fixture 只有10行，校验器要求 ID 1/250/500 共3行；篡改前后均返回 `content probe returned 1 lines, want 3`。将篡改 SQL 换成 `SELECT 1` 后测试仍 PASS。这个测试尚不能证明 payload 损坏被检出；不是因 Docker 不可用而判未修。 |
| **P1-12：启动对账、tmp 清理** | **PARTIALLY** | tmp 清理保持 **FIXED**：`.durable-*`、`.manifest.json.tmp` 删除，最终密文/manifest 保留。启动调用已存在，但 `RecoverInterrupted:94`–`:117` 仍仅执行三条 SQL，无文件枚举、读取或核验。首次引用写入失败留下的无引用密文实测仍无法发现；manifest 引用不一致也仍在。对账部分 **NOT_FIXED**，本轮提交未修改该函数。 |

**重点验收一：Cancel 全链通过。**

`TestReview5DumpCancelWholeChain` 启动生产 Runner，使用真实 OS 子进程、生产 dumper/age/文件提交链，仅将 pgclient 的 Test/CollectDependencies 两个网络函数替换为固定元数据。替身 pg_dump 首次输出后阻塞，第二次正常结束。结果如下：

```text
job=1 canceled
pg_dump child reaped; staging temps and PGPASSFILE removed
cancelFns/perDB cleared
next job=2 succeeded
Stop joined; no rescue Unlock
```

另外，注册窗口测试分别覆盖 cancelFn 延后注册和 runner lifetime 取消：回调在锁外执行且 entry 删除；Stop 后等待并补入回调，轮询没有继续调用它。原有并发 Stop 清理屏障探针也通过，两个调用均等待清理完成。这里验证的是取消与 worker 生命周期；替身输出不是可恢复的 PG archive，不把这项实测称为完整 M1。

**重点验收二：真实 v4 fixture 升级与软删除通过。**

第一组通过生产 goose Provider `UpTo(4)` 构造两条 databases、四条活动 jobs、一条含 artifact 引用的 succeeded 历史，启用真实 `foreign_keys=1`，再调用 `Store.Migrate`：版本5、databases=2、jobs=5；预迁移快照也保留5条 jobs。

第二组使用 v4 的两条 databases、三条 jobs，升级后逐项核对历史 ID=1 的 succeeded 状态、artifact path/hash/size、manifest path；重复活动 job ID=3 被收敛为 interrupted，`pragma_foreign_key_check` 返回0行。随后调用生产 DeleteDatabase/CreateDatabase handler：

```text
v4 -> v5: jobs 3 -> 3
DeleteDatabase(1) -> 204; name = "reuse (deleted #1)"
CreateDatabase("reuse") -> 201
CreateDatabase("reuse") again -> 409
jobs after delete/recreate = 3
```

迁移、数据库约束、删除 handler 均未替换；创建 handler 的连接测试使用上述 PG 元数据替身。该结果足以关闭本次“升级成功但 jobs 丢失”，不要求重建父表或引入新的 schema 方案。

**重点验收三：负对照实际执行，但测试通过原因错误。**

原样运行 `go test -v -count=1 ./backend/internal/jobs -run '^TestM1_NegativeControl$'` 得到 **SKIP**：Docker socket 无权限，`sudo -n docker` 也被 no-new-privileges 限制。这个环境结果不算代码缺陷，也没有把它报为真实负对照通过。

为实际核对 SQL 与校验逻辑，在 `/tmp` 初始化本机 PostgreSQL **18.4**，采用官方支持的单用户模式执行真实建表、插入、UPDATE、SELECT。临时 Go overlay 仅替换 `startTestPostgres` 的 Docker/网络启动部分；PATH 中的审计适配器将 `docker exec … psql -c SQL` 转交给 `postgres --single -j`，把真实返回行格式化为 `psql -At` 输出。原测试主体、10行 seed、校验器均保持不变。此模式无需监听端口，命令及协议依据见 [PostgreSQL 官方文档](https://www.postgresql.org/docs/18/app-postgres.html)。

| 在真实 PostgreSQL 上执行的对照 | 结果 |
| --- | --- |
| 原 `TestM1_NegativeControl` 主体、10行、真实 UPDATE 篡改 | **PASS**，但 verifier 返回的是缺少2行的形状错误，未进入 payload 比较。 |
| 同一10行 fixture，篡改前调用 verifier | `content probe returned 1 lines, want 3`。 |
| 同一10行 fixture，篡改后调用 verifier | 完全相同错误。 |
| 变异测试：只把原测试的篡改 SQL 换成 `SELECT 1` | 仍 **PASS**，证明未篡改数据也能满足所谓负对照。 |
| 审计补充：种500行，先验证，再篡改 ID=1 | 未篡改时返回 nil；篡改后返回 `content mismatch at id 1: got "tampered" want "c4ca4238a0b923820dcc509a6f75849b"`。 |

因此 payload 比较器本身有检测能力，本轮新增测试没有有效验证它。最小修复是用包含全部抽样 ID 的有效 fixture，先断言同一目标通过，再篡改，并断言失败来自内容不一致，而非连接失败或缺行。无需为关闭此项新增全库逐行比对。单用户实测不等同于原生 Docker M1 全链，也没有执行 pg_dump/pg_restore 恢复门禁。

**P1-02 残留：转义口令后缀进入新增成功日志。**

定位：`dumper/dumper.go:168`、`:183`–`:189`、`:438`，`jobs/jobs.go:549`–`:551`。`RedactKnownSecrets` 仅处理原始值和反斜杠加倍，不处理 conninfo 单引号转义；sanitize 又先按不理解转义的正则切碎口令，日志侧已无法完整替换。

普通口令已通过生产 API handler 的受控错误注入：

```json
{"code":"connection_test_failed","message":"connection test failed: password=[REDACTED]'[REDACTED]'"}
```

普通无标签成功 stderr 也已变为 `connection rejected: [REDACTED]`。但对合法口令 `prefix'CANARY-SUFFIX`，让真实替身子进程输出对应 conninfo 形式，完整生产成功链实际记录：

```text
input stderr: password='prefix\'CANARY-SUFFIX'
success log:  stderr_excerpt="password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'\n"
```

这是受控 stderr 注入证明的日志出口缺陷，不声称本次真实 PostgreSQL 主动打印过口令。上轮只确认未脱敏摘要留在 Result，本轮新增日志使其获得日志出口；仍按已列出的特殊字符秘密验收保留 P1-02。应在破坏原文结构前覆盖合法转义形式，并对实际 API/任务错误/日志出口保留 canary 断言。

**P1-04 残留：解密错误路径仍绕过取消仲裁。**

定位：`jobs/jobs.go:387`–`:396`。已取消 context 在 loadDatabase 处现在正确落 canceled；本项不否认该修复。另用临时 overlay 在真实 `crypto.Decrypt` 前加时间屏障，加载已损坏的凭据后调用生产 `Cancel`，再放行执行原 Decrypt；密码学实现和错误值未替换。结果是：

```text
Cancel returned nil
status=failed cancel_requested=true
error=stored credentials are unreadable — the master secret changed or the data is corrupted; re-add the database
```

缺陷限定为“解密/解析报错与取消相遇”的终态仲裁，不声称普通有效凭据解密会无限阻塞或所有取消都会失败。本轮要求明确包含凭据解密阶段；应在这些早期失败分支沿用已有取消仲裁。URI 解析分支的同类遗漏为源码复核，本轮未另做时间屏障实测。

**P1-13 残留：单文件硬边界有效，总预算仍可突破。**

定位：`dumper/dumper.go:211`–`:223` 的启动检查只判断 used 是否已满；`:350` 仍把完整 `QuotaBytes` 给新文件 writer，没有扣除 used。真实加密流实测已有700 KiB、新增717,160字节，约30ms完成并提交，总量1,433,960字节；500ms watchdog 来不及干预。这正是上轮已定量复现的残留。

已通过的部分无需重做：Write(4096)/quota1024 底层写入0字节；8 MiB 生产者在1 MiB单文件预算下提前停止，未出现完成标记，临时文件和凭据清理成功；`1MiB`、`-1`、溢出十进制配置均拒绝，空值/0/1048576正常。修复总预算需把已有占用计入写入可用额度，并保留写前检查；本轮不要求为将来的多 worker 设计资源调度系统。

**P1-12 残留：调用恢复函数不等于文件对账。**

`main.go:201`、`:211` 已依次调用启动清理和 RecoverInterrupted，但后者与上一轮相同，仍只改 SQL 状态及说明。真实生产提交链加 SQLite trigger 注入得到：

| 场景 | 本轮实际结果 |
| --- | --- |
| 密文 rename 成功，首次 artifact_path UPDATE 被 trigger 拒绝 | 密文仍在，job failed、path/state为空；移除 trigger 后调用 RecoverInterrupted，引用仍为空。 |
| running 记录没有引用，磁盘存在对应最终文件 | 恢复后 interrupted，未发现最终文件。 |
| 记录标 committed，但对应文件不存在 | 仍追加“artifact committed before interruption”说明，没有发现缺失。 |
| success UPDATE 连续两次失败 | 密文引用保留、job failed；实际 manifest 存在，但 `HasManifest=false`。 |
| 提交后 cancel 赢得仲裁 | job canceled，密文保留；实际 manifest 存在，manifest_path仍空。 |

manifest 写失败保留密文引用并设置 committed_no_manifest、success 首次写失败后重试成功、终态不被 success 覆盖的已有有效分支均继续通过。tmp 清理也通过，不能因对账缺失将它重开。应实际发现并核对最终文件与任务/manifest 引用，记录缺失或不一致，且不能因恢复扫描而将未验证备份置为 succeeded。

**新增回归 R5-P2-01：墓碑名称与合法活名称碰撞，删除返回500。**

定位：`server/api_phase2.go:165`，错误返回 `:171`–`:172`。通过生产 CreateDatabase handler 创建 `prod`（id=1）与 `prod (deleted #1)`，两次均201；随后删除 id=1，UPDATE 命中仍保留的 name UNIQUE 约束，handler 返回 **500**，数据库仍保持活跃。两个名称均符合当前公开校验规则，不需要并发、损坏数据库或非法输入。迁移注释“带 id 就不会碰撞”不成立，因为用户可创建同一字符串。

应确保系统墓碑名称不会与合法用户名称冲突，或者让约束真正限定于活记录，并覆盖历史数据。此项是本轮新改名策略带来的行为回归，独立于已关闭的升级丢 jobs；不升级为 P1 数据丢失。

**R4-P2-01：Down 回归未关闭，本轮退化为静默不完整回退。**

定位：`db/migrations/0005_kernel_review.sql:33`–`:35` 后已没有任何 Down 段。本轮直接删除了原 Down SQL。真实 goose Provider 实测：

```text
Down error=<nil>
after Down: version=4, v5 database columns=2, v5 active-job index=1
Migrate again: index idx_jobs_active_per_database already exists (1)
```

Down 不再因 deleted_at 索引而报错，但 schema 根本未回退，版本账本却变成4；下一次正常 Migrate 失败，不能以 Down 返回 nil 判修复。[goose 文档](https://pressly.github.io/goose/documentation/annotations/)允许省略 Down 标记，但这不代表它会自动反演 Up。本项沿用 R4-P2-01，记为 **NOT_FIXED／行为退化**，不另算一个新的独立编号。应提供正确回退，或明确拒绝且不改变版本账本；正常只走 Up 的新实例不受这个回退路径影响。

另核对与名称改动直接相关的旧 **R2-P2-01：PARTIALLY**。使用 `41d3c91` 原始 v5 SQL 建库，模拟旧版已经完成的软删除（deleted_at非空、name未改）；换当前迁移并调用 Migrate 后，同名 INSERT 仍报 `UNIQUE constraint failed: databases.name`。当前 handler 能释放“本次删除”的名称，但不会回填此前已删除行。该旧 v5 场景在上一轮已列明，本轮延续原编号，不与 R4-P1-01混为一项，也不称为新强化要求。

**验证记录与证据边界。**

| 检查 | 结果 |
| --- | --- |
| 原生 `GOCACHE=/tmp/supabackup-review-go-cache go test -race -count=1 ./backend/...` | **整体 FAIL（环境）**：server 的 httptest 监听报 `socket: operation not permitted`；其余有测试的包返回通过。jobs 原生5个集成测试另以 `-v` 确认全部 SKIP，没有声称全量 race 或原生 M1 通过。 |
| 定向审计测试，含 `-race` | **25个不同顶层测试实际执行：19 PASS、6 FAIL**，没有 race 报告。去重统计包含随后实际执行的解密屏障测试和单用户负对照，不计其前置 SKIP。部分 PASS 是断言缺陷仍存在，例如总配额越界、无引用密文，以及无效负对照；不代表产品验收通过。 |
| 负对照变异测试 | 另一次执行，仅把篡改 SQL 换为 SELECT 1，测试仍 PASS；这正是门禁失效证据。原测试主体的单用户版本及500行审计对照均真实执行 SQL。 |
| vet / build / gofmt | `go vet ./backend/...`、CLI build 退出0；gofmt无输出。build 有模块 stat-cache 只读告警，二进制仍成功生成。 |
| 本轮未重跑 | 前端、契约生成、release image、双架构、真实 Supabase、TLS实连、掉电实验；相关代码无本轮变化或依赖外部资源，不将缺执行结果计为新增缺陷。 |

证据目录：`/tmp/supabackup-phase2-review5-evidence/`。`probes.log`、`migration.log`、`decrypt.log` 保存核心实测；`m1-native.log`、`jobs-native.log` 明确记录原生 SKIP；`negative-single-pg.log`、`negative-no-tamper.log`、`single-pg-trace.jsonl` 保存真实 PostgreSQL 对照与变异执行；`race.log`、`vet.log`、`build.log`、`scope.json`、`probe-summary.json` 保存检查与范围。`README.md` 提供重放命令，全部审计源码和 overlay 亦保存在该目录。部分复用测试保留 Review4 名称，日志均是本轮在当前 HEAD 重新执行产生；临时探针未进入工作树。

**代码：未通过。有效负对照门禁：未通过。最终：未通过。** 判定依据是上述已复现的代码与测试缺陷，不是外部环境限制。Cancel 解锁、升级保留 jobs、loadDatabase 取消、普通秘密接线、单文件写前限额、非法配置拒绝、tmp 清理均明确认可；关闭剩余具体路径后再做收敛复核。
