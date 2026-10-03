# Phase 2 备份执行内核复审（第 6 轮，收敛确认）

评审日期：2026-10-03。实际 HEAD：`c049105e4692ead9c10018f7bef222ef978883dc`。范围：`git diff 9609e17..c049105`，仅判定[第五轮报告](codex-phase2-review-5.md)中本次指定的七项残留及该 diff 引入的新回归。使用 `code-reviewer` 技能；仅新增本报告，生产代码、仓库测试与迁移文件均未修改。

**最终结论：Phase 2 代码与工程门禁未通过。七项判定：FIXED 4、PARTIALLY 2、NOT_FIXED 1、REGRESSED 0。** 仍有转义秘密泄漏、解密/解析取消终态错误、恢复对账不完整。新增确认一项恢复路径回归 R6-P2-01，归入 P1-12 的修复范围，不重复计入七项统计。未确认 P0。

沿用第五轮口径：FIXED 表示对应问题本身关闭，不要求同一问题域达到理想实现。外部资源项及已接受的 libpq HOME 探测、TOC 待 P5、独立工具 M1 边界均不作为代码缺陷；原验收满足后的强化记 NOTE。其余历史问题不在本轮逐项重审范围，不因省略而自动关闭。

下表及正文的 `jobs/`、`dumper/`、`server/`、`db/` 路径省略 `backend/internal/`；行号均指 `c049105`。

| 指定项 | 判定 | 实测及关闭边界 |
| --- | --- | --- |
| **1. P1-14：负对照 fixture 不覆盖抽样 ID** | **FIXED** | `jobs/m1_test.go:184` 改为 500 行。真实单用户 PostgreSQL 上，干净 fixture 的 verifier 返回 nil，篡改后返回 `content mismatch at id 1`；仅把篡改 SQL 换为 `SELECT 1`，原测试现在 FAIL。原“缺行错误导致未篡改也通过”已关闭。 |
| **2. P1-02：转义口令后缀泄漏** | **PARTIALLY** | `dumper/dumper.go:185` 新增单引号转义替换，单独调用及仅含单引号的 API 422 已通过。但成功 stderr 仍先被 `sanitize` 切碎，原 `CANARY-SUFFIX` 继续进入日志；同时含反斜杠、单引号的合法 conninfo 值也未被完整替换，实测 API 422 泄漏。 |
| **3. P1-04：解密/URI 解析错误路径取消仲裁** | **NOT_FIXED** | 指定 diff 没有这两个分支的修改。`jobs/jobs.go:469`–`:478` 仍直接 `fail`。两组真实 Cancel + 时间屏障实测均为 `status=failed, cancel_requested=true`。此前 loadDatabase、网络阶段及 Cancel 解锁的已关闭问题不重开。 |
| **4. P1-13：总预算扣除已有占用** | **FIXED** | `dumper/dumper.go:242`–`:252` 计算 `QuotaBytes-used`，`:362` 交给写前检查器。已有 716,800 字节、quota 1,048,576 时，新增 716,800 字节明文流被拒绝，无最终新文件，清理后仍仅 716,800 字节；较小流成功，总量 917,048。 |
| **5. P1-12：恢复函数文件对账** | **PARTIALLY** | `jobs/jobs.go:124` 起确实扫描 staging，发现孤儿密文并回填，文件保留，任务不被置为 succeeded；正常绝对路径的缺失引用会标记，首次无目录启动正常返回。但回填使用 basename、核验使用工作目录，产生 R6-P2-01；已有 manifest 未恢复引用的第五轮残留仍在。 |
| **6. R5-P2-01：墓碑名称碰撞** | **FIXED** | `server/api_phase2.go:165` 增加 6 字节随机后缀。生产 handler 创建 `prod` 与 `prod (deleted #1)` 后，删除原记录返回 204。另测升级保留历史、删除后同名重建 201、重复活名称 409 均通过。 |
| **7. R4-P2-01：Down 段缺失** | **FIXED** | `db/migrations/0005_kernel_review.sql:37`–`:47` 恢复实际回退。真实 goose/SQLite Down 后版本为 4，五个 v5 新列及活动任务索引均移除，再 Migrate 成功；含历史任务的往返保留 ID、所属库、状态、密文路径/哈希/大小和 manifest 引用，外键检查通过。 |

**P1-02：函数局部修复成立，生产出口仍泄漏。**

`RedactKnownSecrets` 的三项替换分别处理原值、仅反斜杠加倍、仅单引号转义，没有生成同时执行两种转义的完整值。仓库已有 `pgclient.QuoteConninfo`（`pgclient/pgclient.go:207`）实现该组合规则，可复用其规则。单引号与反斜杠均需转义也符合 [PostgreSQL conninfo 文档](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING)。

| 受控 canary | 当前结果 |
| --- | --- |
| 普通值、仅单引号、仅反斜杠，直接调用 `RedactKnownSecrets` | 均去除秘密。 |
| `prefix'CANARY-SUFFIX`，先已知秘密替换再 sanitize | 去除秘密；认可本次新增替换的局部修复。 |
| `prefix\segment'CANARY-SUFFIX`，按生产 `QuoteConninfo` 编码后调用 | 原转义值保留，仍含 `CANARY-SUFFIX`。 |
| 仅单引号口令，生产 CreateDatabase handler 的 conninfo 错误注入 | 返回 422，秘密已去除。 |
| 反斜杠和单引号组合口令，同一生产 handler | 返回 422，消息仍含 `CANARY-SUFFIX`。 |
| 仅单引号口令，真实子进程 stderr → dumper → 成功任务日志 | 任务 succeeded，日志仍含 `CANARY-SUFFIX`。 |

最后一项完整日志出口仍复现第五轮的同一个缺陷：

```text
input stderr: password='prefix\'CANARY-SUFFIX'
success log:  stderr_excerpt="password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'\n"
```

原因在 `dumper/dumper.go:450`：`sanitize` 先按不理解转义的正则删除口令前半段；`jobs/jobs.go:633` 再调用已知秘密替换时，已经找不到完整原值或转义值。应在破坏原文结构前完整删除已知秘密，并覆盖组合转义及真实出口。上述 stderr/连接错误均为受控注入，不声称真实 PostgreSQL 在本次主动输出过口令；这不影响其证明 API/日志出口缺陷。

**P1-04：本次声称的两个取消分支没有进入基线。**

分别在 `crypto.Decrypt`、`pgclient.ParseURI` 执行前加临时时间屏障，先让生产 Runner 完成 loadDatabase 和 context 注册，再调用生产 `Cancel`，返回 nil 后放行原函数。解密使用损坏 ciphertext，URI 解析使用真实加密但无效的 URI；未替换原密码学/解析逻辑或其错误值。

```text
Decrypt error branch: Cancel=nil; status=failed; cancel_requested=true
ParseURI error branch: Cancel=nil; status=failed; cancel_requested=true
```

两分支都绕过已有 `ranToCancellation` / `finalizeCanceled`。本项是第五轮残留未修，并非取消锁再次回归。

**P1-12 及新增 R6-P2-01（P2）：恢复回填错误路径，并把真实文件误报为 MISSING。**

定位：`jobs/jobs.go:153`–`:157` 使用 `e.Name()` 持久化引用，而 `:184` 执行 `os.Stat(p)`。生产提交使用包含 staging 目录的路径；服务工作目录不必等于 staging。当前正常启动顺序没有改变工作目录来满足新回填路径。

对真实生产提交链注入“首次 artifact_path UPDATE 失败”，保留实际 age 密文，移除 trigger 后调用 RecoverInterrupted，得到：

```text
disk:  <staging>/backup-job1.dump.age       # 文件仍在
DB:    artifact_path="backup-job1.dump.age", artifact_state="committed"
status: failed
error: ... [recovery: unreferenced ciphertext restored to this job]
           [recovery: referenced artifact file is MISSING from staging]
```

另用 running 无引用 fixture 复现同样结果，终态正确为 interrupted。回填及“不删除”已经有效，但刚恢复的引用不能按原路径约定定位文件，且本次新代码立刻添加错误的缺失告警。这是 **R6-P2-01 新回归**，同时构成 P1-12 未完全关闭的证据；不重复编号成两个独立缺陷。最小修复应使回填与核验使用一致的 staging 路径。

第五轮明确列出的 manifest 对账也仍未完成：

| 生产故障注入，随后调用 RecoverInterrupted | 本轮结果 |
| --- | --- |
| success UPDATE 连续两次失败 | job failed，密文引用保留，磁盘 manifest 存在，但 `manifest_path=""`、`HasManifest=false`。 |
| 提交后 cancel 赢得仲裁 | job canceled，密文和 manifest 存在，但 `manifest_path=""`、`HasManifest=false`。 |
| 正常绝对路径引用，文件存在 | 保留引用，不误报缺失。 |
| 正常绝对路径引用，文件缺失且 staging 存在 | 增加 MISSING 说明。 |
| 首次无 staging 目录、无 artifact 引用 | 返回 nil，running 转 interrupted，无文件操作错误。 |

不要求恢复时自动宣告备份验证成功；本轮所有恢复探针都没有将任务提升为 succeeded。缺失 manifest 引用属于已列明的对账验收，不是新增“更完善方案”。

**有效负对照、配额、并发和迁移验证。**

本机 PostgreSQL **18.4** 新建隔离 cluster，按[官方单用户模式](https://www.postgresql.org/docs/18/app-postgres.html)执行真实 SQL，无 listener。临时 overlay 仅将 Docker 启动 harness 换成单用户入口；适配器把 `docker exec … psql -c SQL` 转交 `postgres --single -j`，将实际结果行转成 `psql -At` 格式。当前 500 行 seed、抽样查询、payload 比较和负对照断言均保持原样。

| PostgreSQL 对照 | 实际结果 |
| --- | --- |
| 500 行，不篡改 | verifier = nil。 |
| 同一 fixture，UPDATE ID=1 的 payload | `content mismatch at id 1: got "tampered" want "c4ca4238a0b923820dcc509a6f75849b"`。 |
| 当前原测试主体 | PASS，确实检测到 payload mismatch。 |
| 只把原测试的 UPDATE 改成 `SELECT 1` | FAIL，落入 `NEGATIVE CONTROL PASSED` 的 fatal 断言；这是期望的变异结果。 |
| 旧 10 行 fixture，不篡改 / 篡改 | 两次仍都是 `content probe returned 1 lines, want 3`。 |

请求中“10 行不篡改 → verifier nil”与固定抽样 ID 1/250/500 不相容；本轮以当前已改的 **500 行**验收 clean→nil / tampered→mismatch，同时保留旧 10 行作为形状错误对照，没有修改 verifier 来迎合该描述。单用户探针证明 SQL/比较器/负对照门禁有效，不代表原生 Docker pg_dump→pg_restore M1 全链已经执行。

总预算探针使用真实 OS 生产者和 age 加密流：`quota=1,048,576`、已有 `716,800`，writer 剩余额度 `331,776`。超额流返回 `ErrStagingFull`，无提交、凭据与临时文件已清理；200,000 字节明文流成功，密文 200,248 字节，总量 917,048。另复核单次 `Write(4096)` 在预算 1024 时返回 `n=0`、底层 0 字节；8 MiB 生产者被提前终止，完成标记不存在。原单 worker 验收满足，不追加多 worker 预算调度要求。

并发探针覆盖真实 dump 子进程取消、注册窗口回调和同时调用 **2×Stop + Cancel**。Cancel 返回后 mutex 可立即取得，两个 Stop 均等待清理屏障，随后 join 完成，cancelFns/perDB 清空。完整取消链还验证子进程回收、临时文件/凭据删除、同库下一任务 succeeded。没有测试侧救援 Unlock，未发现 data race。

迁移通过生产 goose/SQLite 执行：v4→v5 fixture 保留全部 3 条 jobs 及历史引用；随后 Delete 204、同名 Create 201、重复活名称 409。独立 Down→Up 探针分别核对 schema 和含历史数据的往返，均通过，避免仅以 Down 返回 nil 判修复。

**NOTE（不阻断已关闭项）。** 负对照可在仓库测试中再加入 clean 前置断言和 mismatch 类型断言；当前 fixture 形状伪通过已经实测关闭。墓碑随机后缀可再做极小概率碰撞重试或改为 live-only 唯一约束；不据此重开本次合法名称的确定性碰撞。Down 会丢失所删除的 v5 专用列值，这在迁移注释中已明确，不将可正常往返的 schema 回退误判为原“静默未回退”。

**执行结果与证据边界。**

| 检查 | 结果 |
| --- | --- |
| 原生 `GOCACHE=/tmp/supabackup-review-go-cache go test -race -count=1 ./backend/...` | 整体 FAIL（环境）：server 的 httptest listener 被沙箱拒绝，`socket: operation not permitted`。 |
| 同一全量命令，加 `-overlay …/router-overlay.json`，并用 `-json` 记录 | **退出 0；74 个顶层测试 PASS、5 个 Docker 集成测试 SKIP；无 race 报告。** 10 个有测试包通过，6 个包无测试文件。 |
| 本轮定向审计测试，均使用 `-race -count=1` | **21 个不同顶层测试：14 PASS、7 FAIL**，无 race 报告。FAIL 对应本报告的脱敏、早期取消和恢复对账残留；不把子测试或旧日志重复计数。 |
| 负对照无篡改变异 | 单独执行，预期 FAIL；不计入上行 21 个测试。 |
| vet / CLI build / gofmt / diff check | `go vet ./backend/...`、CLI build 退出 0，gofmt 与 `git diff --check` 无输出。build 有模块 stat-cache 只读告警，目标二进制成功生成。 |

Router 替身只替换测试传输：`http.Client` 的请求经进程内 RoundTripper 进入真实 `Server.Router()`，使用 `httptest.NewRecorder`；生产路由、中间件、handler、CookieJar 和测试断言保留。匿名客户端及检查 Secure cookie 的无 cookie 客户端也接入同一传输。首版替身漏接后者，产生一次环境失败；已修正并重新跑完整 backend，以上统计取最终完整运行。该结果不声称覆盖真实 TCP/TLS 监听。

证据目录：`/tmp/supabackup-phase2-review6-evidence/`。`core.log`、`probes.log`、`barrier.log`、`api-history.log` 保存定向复现；`negative-single-pg.log`、`negative-no-tamper.log`、`single-pg-trace.jsonl` 保存真实 SQL 和变异执行；`race-native.log`、`race-router.jsonl` 保存全量检查与 SKIP 原因；`README.md`、`summary.json` 及全部 overlay/审计源码保存重放方式和统计。使用的 Review4/Review5 命名探针均在当前 HEAD 重新运行。网络元数据替身和合成 pg_dump 输出仅用于 worker/文件/日志路径，不冒充完整 M1 备份恢复。

前端、契约生成、release image、双架构、真实 Supabase、联网 CI、TLS 实连、掉电实验不在本轮执行结果内，不以这些外部项阻断。**当前代码验收未通过；全量可执行 race 检查与本项负对照通过，但定向残留尚未关闭，因此 Phase 2 代码与工程门禁最终：未通过。**
