# Phase 2 备份执行内核复审（第 7 轮，最终收敛确认）

评审日期：2026-10-03。实际 HEAD：`7f618134741f87c52af741091e1e7c43423f1d73`。范围：`git diff c049105..7f61813`，复核[第六轮报告](codex-phase2-review-6.md)中本次指定的三个残留点及该 diff 引入的新回归。使用 `code-reviewer` 技能。仓库仅新增本报告；生产代码、仓库测试、迁移均未修改，审计探针通过 `/tmp` 中的 Go overlay 执行。

**最终结论：Phase 2 代码与工程门禁未通过。三个指定残留：FIXED 2、PARTIALLY 0、NOT_FIXED 1、REGRESSED 0；新回归 0。** 阻断项是 P1-02：quoted stderr 仍泄漏口令后缀，上一轮已确认的 API 组合转义泄漏也仍在。未确认 P0。

沿用原口径：只判断对应问题是否关闭，不以同一问题域的额外强化否认修复；外部资源、已接受的 libpq HOME 探测、TOC 待 P5、独立工具 M1 边界不作为代码缺陷。本轮计数针对用户明确指定的三个**残留点**，因此 P1-02 的 quoted 修复判 NOT_FIXED；此前普通值脱敏、仅单引号的已知秘密替换等已修分支仍有效，不将历史部分修复误记为回归。其他历史项不在本轮逐项重审范围，也不因省略而自动关闭。

下文 `jobs/`、`dumper/`、`server/`、`pgclient/` 均省略 `backend/internal/`；生产代码行号指 `7f61813`。

| 指定残留 | 判定 | 实测与关闭边界 |
| --- | --- | --- |
| **P1-04：decrypt/URI 解析错误分支取消仲裁** | **FIXED** | `jobs/jobs.go:492`、`:506` 已调用 `finalizeCanceled`，`:500` 另补了解密成功后的取消检查。真实 Cancel + 时间屏障下，解密失败、解密成功、解析失败三种取消均为 `canceled`，`cancel_requested=true`、`finished_at` 已写入，worker maps 清空；不取消的损坏凭据/无效 URI 仍为 failed。 |
| **P1-02：quoted password 正则未完整消耗转义引号** | **NOT_FIXED** | `dumper/dumper.go:170` 虽加入 `\\'` 备选，但 `[^']` 仍能先消耗反斜杠，匹配止于第一个转义引号。真实子进程 stderr → dumper → Runner 成功日志仍含 `CANARY-SUFFIX`；新旧正则对照结果相同。API 的反斜杠与单引号组合秘密也仍泄漏。 |
| **P1-12 / R6-P2-01：回填 basename、已有 manifest 未恢复引用** | **FIXED** | `jobs/jobs.go:157` 改为拼接 staging 路径，`:167`–`:184` 补 manifest 引用恢复。上一轮首次 artifact 引用写入失败、success UPDATE 两次失败、提交后取消三个故障注入全部通过：应有文件与引用保留，无新回填路径的错误 MISSING，不提升为 succeeded。旧版已经存入的 basename 不自动迁移，单列为升级 NOTE。 |

**P1-02：正则改动没有消除原复现，仍是 P1 阻断。**

定位：`dumper/dumper.go:170`、`:177`–`:180`、`:453`；最终日志出口 `jobs/jobs.go:664`–`:666`。

Go `regexp.Compile` 使用 leftmost-first 语义，并非自动选择整个表达式的最长匹配。本次 `(?:[^']|\\')*` 的第一个备选包含反斜杠：遇到 `\'` 时先把 `\` 当普通字符消耗，随后末尾 `(')` 已可成功匹配引号，第二个备选没有机会扩展该值。实测 `quotedPwRe.FindString` 直接确认该边界。参见 [Go regexp.Compile 文档](https://pkg.go.dev/regexp#Compile)。

```text
input:        password='prefix\'CANARY-SUFFIX'
regex match:  password='prefix\'
old sanitize: password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'
new sanitize: password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'
success log:  stderr_excerpt="password=[REDACTED]'[REDACTED]'CANARY-SUFFIX'\n"
```

这里不是仅凭正则推断：探针启动真实 OS 子进程，将 canary 写入 stderr；生产 dumper 完成读取、age 加密、提交，生产 Runner 落库为 succeeded 并写日志，日志确实保留后缀。后续 `RedactKnownSecrets` 已找不到被前一步切碎的完整秘密，不能补救。

| 脱敏探针 | 结果 |
| --- | --- |
| 普通秘密，完整成功日志出口 | PASS，秘密被删除。 |
| `prefix'CANARY-SUFFIX`，完整成功日志出口 | **FAIL，后缀泄漏，与上一轮相同。** |
| 单引号、反斜杠与单引号组合、多个单引号，经 QuoteConninfo → sanitize → RedactKnownSecrets | **均 FAIL**；新旧正则保留相同后缀。 |
| 普通值、仅反斜杠，经同一函数链 | PASS。 |
| 仅单引号，直接 RedactKnownSecrets / CreateDatabase 422 | PASS，保持上一轮已修效果。 |
| `prefix\segment'CANARY-SUFFIX`，直接 RedactKnownSecrets / CreateDatabase 422 | **仍 FAIL**，组合转义值未删除。 |

API 残留定位：`dumper/dumper.go:193`–`:195` 只分别生成反斜杠加倍、单引号转义两种值，没有生成两者组合；`server/api_phase2.go:114`–`:115` 不经过本次修改的 `sanitize`。受控错误注入后，生产 handler 的实际 422 响应仍含 `CANARY-SUFFIX`。这已在第六轮明确列出，不是新增验收条件，也不重复编号为新回归。上述 stderr/连接错误均为审计注入，不声称 PostgreSQL 在本次自然输出过口令。

最小修复方向：quoted 值的普通字符分支应排除反斜杠，使转义序列完整消耗，例如其值部分采用 `(?:[^'\\]|\\.)*`；已知秘密替换复用 `pgclient.QuoteConninfo` 的组合转义规则，在破坏原文结构前完成替换。单引号和反斜杠均需转义是合法 conninfo 规则，见 [PostgreSQL 连接字符串文档](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING)。补入真实 stderr 日志与 API 422 的 canary 回归断言，不能只测单独的替换函数。

**P1-04：错误分支与解密成功窗口均已关闭。**

在原 `crypto.Decrypt`、`pgclient.ParseURI` 入口加临时时间屏障：Runner 先完成 loadDatabase 和取消函数注册，测试调用生产 `Cancel` 并确认返回 nil，再放行真实解密/解析实现。没有替换密码学、解析结果或终态 SQL。新增矩阵使用原始 `pgclient.Test`/`CollectDependencies`，无需网络替身；这些早期路径在连接 PostgreSQL 前已经结束。

| 时序 | 实际终态 | 对照 |
| --- | --- | --- |
| 损坏 ciphertext，在 Decrypt 屏障期间 Cancel | canceled | `cancel_requested=true`，错误消息为空。 |
| 有效 ciphertext，在 Decrypt 屏障期间 Cancel | canceled | 命中解密后的新检查。 |
| 加密存储的无效 URI，在 ParseURI 屏障期间 Cancel | canceled | 原解析函数实际报错后正确仲裁。 |
| 损坏 ciphertext，不 Cancel | failed | 保留凭据不可读错误。 |
| 无效 URI，不 Cancel | failed | 保留 URI 格式错误。 |

五组均写入 `finished_at`，Stop 后 `cancelFns/perDB` 均为 `0/0`，无测试侧 Unlock 或终态补写。原第六轮的两个失败探针也在本 HEAD 重新运行，均 PASS。

**P1-12 / R6-P2-01：新回填路径和 manifest 恢复满足原验收。**

服务工作目录与 staging 不同的情况下，逐项复跑上一轮同一故障注入：

| 场景 | RecoverInterrupted 后结果 |
| --- | --- |
| 首次 artifact_path UPDATE 失败，磁盘密文已提交 | job 仍 failed；回填 `<staging>/backup-job1.dump.age`，`os.Stat` 成功，无错误 MISSING。该分支尚未生成 manifest，不要求凭空恢复。 |
| success UPDATE 连续两次失败 | job 仍 failed；密文引用保留，恢复绝对 manifest 路径，`HasManifest=true`。 |
| 提交后取消赢得仲裁 | job 仍 canceled；密文和 manifest 保留，恢复 manifest 引用，`HasManifest=true`。 |
| running、空引用，磁盘存在同名文件 | interrupted；回填正确路径，无错误 MISSING。 |
| 已有正常绝对路径，文件存在 / 文件缺失 | 分别保持正常 / 添加 MISSING 说明。 |
| 首次启动无 staging 目录、无引用 | 返回 nil，running 转 interrupted。 |

故障注入使用生产 SQLite trigger、Runner、dumper、age 加密与文件提交；只有网络元数据和 pg_dump 输出使用受控替身。合成 dump 数据不冒充可恢复的 PostgreSQL archive。另有 empty/absolute/legacy_basename 路径矩阵使用磁盘 fixture，所有文件均保留，不将任务自动提升为 succeeded。

**NOTE：旧 basename 与“绝对路径”的边界。** `path != ""` 的历史行不会被本补丁改写；若旧版已回填 `backup-job1.dump.age`，新恢复仍保留该 basename，并在不同工作目录下标记 MISSING。本轮专门实测确认了这一点。第六轮要求修复的是新回填与核验路径不一致，本次已关闭；旧数据规范化属于升级补救，不升级为新的阻断条件。如果旧版本实际运行过恢复，应另行修复这些历史引用。另外实现是 `filepath.Join(stagingDir, name)`：stagingDir 为绝对路径时回填绝对路径，配置为相对路径时仍为相对路径，与正常生产提交的路径约定一致，并非无条件调用 `filepath.Abs`。

**单用户 PostgreSQL、工程检查与证据边界。**

新建隔离 PostgreSQL **18.4** cluster，以 [官方单用户入口](https://www.postgresql.org/docs/18/app-postgres.html) `postgres --single -j` 执行真实 SQL，不开 listener。沿用仅替换 Docker 启动/SQL 传输的 harness，当前 500 行 seed、抽样 ID 和比较器保持原样：干净 fixture 返回 nil，篡改 ID=1 后返回 `content mismatch at id 1`；原负对照 PASS，仅将篡改 SQL 改成 `SELECT 1` 的变异测试预期 FAIL。此结果证明 SQL/比较器门禁有效；decrypt/parse 取消在连接前发生，其 canceled 证据来自上文真实 Runner + SQLite 探针，不声称单用户 PG 可以接受 pgx/pg_dump 网络连接。

| 检查 | 实际结果 |
| --- | --- |
| 原生 `GOCACHE=/tmp/supabackup-review-go-cache go test -race -json -count=1 ./backend/...` | 退出 1：server 的 httptest listener 被沙箱拒绝；另 `TestAdvisoryLockAcrossProcesses` 一次报 `child never acquired the lock`。不将该运行写成通过。 |
| 同一全量命令，加沿用的 `router-overlay.json` | **退出 0；74 个顶层测试 PASS、5 个 Docker 集成测试 SKIP；无 race 报告。** 10 个有测试包通过、6 个无测试文件。跨进程锁原测试也通过，未修改其实现或断言。 |
| 本轮定向审计（含真实 PG 对照，不含无篡改变异） | **12 个不同顶层测试：8 PASS、4 FAIL**。失败项均属 P1-02：成功 stderr 日志、API 422、组合已知秘密替换、quoted 正则对照；无 race 报告。 |
| 单用户 PG 无篡改变异 | 预期 FAIL，落入 `NEGATIVE CONTROL PASSED` 的 fatal 断言。 |
| vet / CLI build / gofmt / diff check | vet、CLI build 退出 0；gofmt 与 diff check 无输出。build 有只读模块 stat-cache 告警，但二进制成功生成。 |

Router overlay 仅把测试 HTTP 传输改为进程内 RoundTripper → 生产 Router → Recorder，保留路由、中间件、handler、CookieJar 和原测试断言，不声称验证真实 TCP/TLS 监听。原生运行中的跨进程锁超时保留为执行记录；随后的完整检查及该测试单独 `-race -count=3` 均通过（`advisory-lock-repeat.log`），不据此认定本 diff 引入新回归，也不声称已证明首次超时的根因。

证据目录：`/tmp/supabackup-phase2-review7-evidence/`。`core.log` 保存重放的 stderr/API/恢复探针；`barrier.log`、`early-matrix.log` 保存早期取消及负对照；`review7.log` 保存新旧正则匹配和 basename/绝对路径对照；`negative-single-pg.log`、`negative-no-tamper.log`、`single-pg-trace.jsonl` 保存真实 SQL 与变异；`race-native.jsonl`、`race-router.jsonl` 保存完整门禁结果。`README.md`、`summary.json`、`sources.sha256`、overlay 和审计源码提供复现入口。所有结果均在当前 HEAD 新执行，未沿用上轮日志。

本次 diff **未确认新回归**；原 R6-P2-01 的新回填问题已关闭。前端、契约生成、release image、双架构、真实 Supabase、联网 CI、TLS 实连和掉电实验不在本轮执行范围，不作为额外阻断。P1-02 原 canary 泄漏仍可复现，尚不能给出“通过”或“修改后通过”；**Phase 2 代码与工程门禁最终：未通过。**

---

## Phase 2 第 8 轮追加复审：b01ff7e 单点收敛

评审日期：2026-10-03。实际 HEAD：`b01ff7eddca181d97e6ccb98c839d2dfa8fde891`。范围：`git diff 7f61813..b01ff7e`，仅复核 P1-02 残留与该 diff 新回归。沿用前文判定口径；不重开其他历史项，不把环境限制或额外强化作为新代码阻断。仓库仅追加本节，生产代码和仓库测试均未修改；审计探针通过 `/tmp` 中的 Go overlay 执行。

**单项判定：P1-02 = PARTIALLY。quoted 正则的原根因已 FIXED，但同一项上一轮已确认的 API 组合转义泄漏仍 NOT_FIXED。新回归 1 项：R8-P1-01，已知秘密替换循环不终止。最终结论：未通过。** 本轮不把新回归合并为 P1-02 的 REGRESSED，也不以组合残留否认 quoted 分支确已修复。

| 验收点 | 判定 / 实测 |
| --- | --- |
| `[^']` 抢先消耗反斜杠、导致合法 quoted password 提前结束 | **FIXED**。新正则完整匹配单引号、反斜杠与单引号组合、相邻转义、多单引号四组 `QuoteConninfo` 结果；plain 形式两组也通过。 |
| 上一轮真实子进程 stderr → dumper → Runner 成功日志 | **PASS**。重跑 `TestReview5SuccessStderrLog`，普通秘密与 escaped quote 均无 canary；原 escaped 用例由 FAIL 转 PASS。 |
| `TestRound7QuotedRedaction` 及已知秘密的 raw / quote-escaped / double-escaped 三种形式 | **PASS**。秘密为 `prefix'CANARY-SUFFIX` 时三种输入均完整替换。 |
| 已有反斜杠与单引号组合秘密，经 `QuoteConninfo` → `RedactKnownSecrets` / API 422 | **NOT_FIXED**。重跑上一轮 `TestReview6RedactEscapes/combined` 与 `TestReview6APIEscapedCanary`，仍含 `CANARY-SUFFIX`。这是原验收残留，不是新增条件。 |
| 新增迭代清扫的终止性 | **REGRESSED，R8-P1-01**。合法口令 `[REDACTED]`、`REDACTED` 在直接调用、生产 API handler、Runner 成功日志三条路径均卡住；旧实现的六组对照全部返回。 |

### P1-02：正则已修，组合枚举仍不完整

定位：`backend/internal/dumper/dumper.go:170`、`:171`、`:193`–`:198`；实际泄漏出口 `backend/internal/server/api_phase2.go:114`–`:115`。

新普通字符分支排除了反斜杠，因此 `\'` 必须由 escaped-pair 分支整体消耗；这关闭了上一轮在 [Go leftmost-first 匹配语义](https://pkg.go.dev/regexp#Compile)下的原复现。下面代码块均表示**实际字符**，没有再套 `%q` 展示转义：

```text
input:       password='prefix\'CANARY-SUFFIX'
old match:   password='prefix\'
HEAD match:  password='prefix\'CANARY-SUFFIX'
HEAD output: password=[REDACTED]'[REDACTED]'
```

对用户输入中的 `password='prefix\\'CANARY-SUFFIX'` 也单独按**两个实际反斜杠**运行了探针：匹配止于 `password='prefix\\'`，`sanitize` 输出仍含 `CANARY-SUFFIX`；直接 `RedactKnownSecrets` 可以删除完整值，但在 `sanitize` 之后调用已无法补救。按 [libpq 语法](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING)，两个反斜杠表示一个字面反斜杠，其后的引号是结束引号；因此它不等同于上面的合法单层 escaped quote。若它来自日志二次转义，仍需在破坏完整秘密前清扫。此处记录输入层级边界，不把原有行为另计为本次正则新回归。`TestRound7QuotedRedaction:11` 的 raw string 实际只有一个反斜杠。

组合残留的最小复现使用项目已有的 `pgclient.QuoteConninfo`：

```go
secret := `prefix\segment'CANARY-SUFFIX`
msg := "password=" + pgclient.QuoteConninfo(secret)
out := dumper.RedactKnownSecrets([]string{secret}, msg)
// msg 和 out 均为：password='prefix\\segment\'CANARY-SUFFIX'
```

令 `Q` 表示给单引号加反斜杠，`B` 表示反斜杠加倍。本补丁枚举 `sec`、`Q(sec)`、`B(sec)`、`B(Q(sec))`，而生产 `QuoteConninfo` 在 `pgclient.go:208`–`:210` 生成的是 `Q(B(sec))`。二者顺序不可交换；新循环没有任何形式命中，重复执行也不能补齐。

| 组合实测，秘密均为 `prefix\segment'CANARY-SUFFIX` | 结果 |
| --- | --- |
| `B(Q(sec))`：先转义引号、再加倍反斜杠 | PASS，本补丁新增覆盖。 |
| `Q(B(sec))`：先加倍反斜杠、再转义引号，即原 `QuoteConninfo` 组合 | **FAIL，原残留。** |
| `B(Q(B(sec)))`：上述 conninfo 再加倍反斜杠 | FAIL，补充边界；不另计缺陷。 |

生产 `CreateDatabase` 的实际 422 响应仍为以下内容（此处是 JSON，因此反斜杠另有 JSON 转义）：

```json
{"code":"connection_test_failed","message":"connection test failed: password=[REDACTED]'prefix\\\\segment\\'CANARY-SUFFIX'"}
```

该响应由受控连接错误驱动生产 handler 生成，错误值来自生产 `QuoteConninfo`，未替换脱敏函数；不声称 PostgreSQL 本轮自然输出过口令。新测试只以不含原始反斜杠的秘密测试三种表示，故不能覆盖这个已有残留。修复应复用 `QuoteConninfo` 的组合顺序，并继续用此 API 422 探针验收。

### R8-P1-01：迭代替换会重新命中占位符，导致请求 / worker 不返回

**新回归，P1。** 定位：`backend/internal/dumper/dumper.go:196`–`:206`，尤其是 `:202`–`:203`。

```go
dumper.RedactKnownSecrets([]string{"[REDACTED]"}, "failure: [REDACTED]")
dumper.RedactKnownSecrets([]string{"REDACTED"}, "failure: REDACTED")
```

第一例每次把 `[REDACTED]` 换成自身，仍无条件设置 `changed=true`，永远无法退出。第二例会继续替换刚插入标记内的 `REDACTED`，不断添加方括号，因此仅增加 `new == old` 检查也不足以解决。两种秘密经 `url.UserPassword` 正确编码后均通过生产 `ParseURI`，不是非法口令前提。

| 路径，每条均测试上述两种秘密 | `7f61813` 对照 | `b01ff7e` |
| --- | --- | --- |
| 直接 `RedactKnownSecrets` | 两组返回，退出 0 | 两组超过 2 秒，堆栈位于替换循环。 |
| `CreateDatabase` 连接失败 → 422 handler | 两组返回 422，退出 0 | 两组超过 2 秒，堆栈为 `api_phase2.go:114` → `RedactKnownSecrets`。 |
| 真实子进程 stderr → dumper → Runner 成功日志 | 两组完成，退出 0 | 两组超过 2 秒，堆栈为 `jobs.go:666` → `RedactKnownSecrets`。 |

对照只将 `dumper.go` 换为 `git show 7f61813:backend/internal/dumper/dumper.go` 的原文，其他调用链保持相同，因而能归因到本 diff。探针在独立、启用 race 的测试进程中运行；进入目标路径后由外部期限终止并通过 SIGQUIT 保存堆栈，没有把超时算成测试通过，也没有遗留无限运行的 goroutine。循环不检查上下文，受影响的请求或备份执行无法正常返回，属于可用性回归。

修复应对原始输入做有界替换，避免再次扫描已插入的占位符；同时保留组合转义覆盖。补入占位符相等、包含关系以及真实 handler / Runner 路径的终止性断言。

### 工程验证与最终结论

运行环境：`go1.26.0 linux/arm64`，`GOCACHE=/tmp/supabackup-review-go-cache`。所有结果均在本 HEAD 新执行。

| 检查 | 实际结果 |
| --- | --- |
| 原生 `go test -race -json -count=1 ./backend/...` | **退出 1**：server 的 `httptest` 监听被沙箱拒绝。已执行顶层测试 59 PASS、5 SKIP、1 FAIL；未发现 race 报告。`TestRound7QuotedRedaction`、跨进程 advisory lock 均通过。 |
| 相同全量命令，增加上一轮已检查过的 `router-overlay.json` | **退出 0：75 个顶层测试 PASS、5 个 Docker 集成测试 SKIP**；10 个有测试包通过、6 个无测试文件；无 race 报告。 |
| 定向 quoted / 已知秘密 / 成功日志 / API 探针 | 8 个顶层测试中 5 PASS、3 FAIL；失败为原组合秘密、组合矩阵、API 422。两个实际反斜杠用例的 PASS 仅表示观察完成，不表示无泄漏。 |
| 新回归旧版 / HEAD 对照 | 旧版 6/6 返回，HEAD 6/6 超时，均已进入目标路径；堆栈证实阻塞在生产替换循环。 |

Router overlay 只将测试 HTTP 传输改为进程内 Router → Recorder，保留生产路由、中间件、handler、CookieJar 和原断言；不声称原生监听或真实 TCP/TLS 已通过。API / Runner 定向探针替换网络元数据或连接错误，真实进程 stderr、生产脱敏、age、SQLite 与文件提交保持执行；合成 dump 不冒充真实数据库备份。本轮未重跑其他已关闭项的专项故障矩阵，也未把 Docker SKIP 或监听限制记为新代码缺陷。

证据目录：`/tmp/supabackup-phase2-review8-evidence/`。`core.log`、`baseline-probes.log` 保存正则、转义组合与出口断言；`marker-summary.json`、`marker-*.log` 保存六组新旧终止性对照及堆栈；`race-native.jsonl`、`race-router.jsonl` 保存全量测试。`README.md`、`summary.json`、`sources.sha256`、overlay 和探针源码提供复现入口。`git diff --check` 通过。

**第 8 轮最终结论：未通过。P1-02 = PARTIALLY（quoted 原根因已修，既有 API 组合转义泄漏未修）；新增 R8-P1-01 必须修复。当前补丁尚不能判为“通过”或“修改后通过”。**
