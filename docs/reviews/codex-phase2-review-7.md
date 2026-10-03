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

---

## Phase 2 第 9 轮追加复审：f44a3f7 最终单点确认

评审日期：2026-10-03。实际 HEAD：`f44a3f7bc70cb7329bfa4dc70519f7e4ec6c01bd`。范围：`git diff b01ff7e..f44a3f7`，复核上一轮两个 OPEN 及本次差异引入的新回归。使用 `code-reviewer` 技能；仓库仅追加本节，生产代码和仓库测试均未修改。以下定向实测使用 `/tmp` 中的 Go overlay，全部在本轮重新编译、执行。

**两项判定：R8-P1-01 = PARTIALLY；P1-02（API 422 组合转义泄漏）= FIXED。新增回归 1 项：R9-P1-01，连接失败日志泄漏口令。最终结论：未通过。**

沿用原口径：FIXED 表示指定问题本身关闭。API 回传与新增日志出口分别判断，不以日志回归否认固定 422 已关闭原客户端泄漏；也不把原有转义枚举不足重复编号。外部资源、沙箱监听限制和 Docker 跳过不算代码缺陷。

| 指定 OPEN | 判定 | 关闭情况 |
| --- | --- | --- |
| **R8-P1-01：已知秘密替换循环不终止** | **PARTIALLY** | `secret="[REDACTED]"` 在直接调用、API、Runner 三条路径均恢复返回；`secret="REDACTED"` 在同样三条路径仍无限替换。上一轮已明确包含两种秘密，问题尚未关闭。 |
| **P1-02：API 422 组合转义泄漏** | **FIXED** | 普通值、单引号、反斜杠与单引号组合、conninfo 再次转义四组错误，实际响应均为相同固定 422，无 canary。原组合场景在 `b01ff7e` 仍泄漏、在 HEAD 已消除。日志另列新回归；特殊口令导致无法返回归属 R8-P1-01。 |

### R8-P1-01：相等场景已修，子串场景仍阻塞请求和 worker

定位：`backend/internal/dumper/dumper.go:197`–`:213`，尤其是 `:203`、`:206`–`:209`；调用出口为 `backend/internal/server/api_phase2.go:117`、`backend/internal/jobs/jobs.go:666`。

跳过 `form == marker` 能解决替换为自身的场景，但 `REDACTED` 是 `[REDACTED]` 的真子串。每次替换都会再次命中新插入的标记，且文本确实变化，因此 `next != s` 始终成立。下面是连续单次替换的结果；当前每轮还会处理四个相同 form：

```text
failure: REDACTED
failure: [REDACTED]
failure: [[REDACTED]]
failure: [[[REDACTED]]]
...
```

| 实际生产路径 | b01ff7e：`[REDACTED]` / `REDACTED` | f44a3f7：`[REDACTED]` / `REDACTED` |
| --- | --- | --- |
| 直接 `RedactKnownSecrets` | 两组均超过 2 秒 | 返回 / 超过 2 秒。 |
| 连接失败 → `CreateDatabase` → 422 | 两组均超过 2 秒 | 返回固定 422 / 超过 2 秒，尚未返回响应。 |
| 真实子进程 stderr → dumper → Runner 成功日志 | 两组均超过 2 秒 | 完成 / 超过 2 秒，worker 未返回。 |

两种口令经生产 `ParseURI` 接受。12 组对照均在独立、启用 race 的测试进程中执行；超时后发送 SIGQUIT 保存堆栈并回收进程，不以超时当作通过。HEAD 的 dumper、Runner 堆栈位于 `dumper.go:206`；API 另用 Go 的 1 秒测试期限补采堆栈，确认 `api_phase2.go:117` → `RedactKnownSecrets` → `dumper.go:206`。对照仅把本次两个生产改动文件换成 `git show b01ff7e:<path>` 原文，fixture 保持相同。Runner 使用真实 SQLite、age、文件提交与 OS 子进程；网络元数据和 dump 内容为受控 fixture。

这是原 R8-P1-01 的残留，仍为 P1，不另计新回归。修复应对原始输入做有界替换，避免重新扫描已经插入的标记；不能仅增加相等判断，也不能直接跳过 `REDACTED` 而保留原始秘密。

### P1-02：固定 422 已关闭原 API 泄漏

定位：`backend/internal/server/api_phase2.go:118`–`:119`。定向探针在 `pgclient.Test` 边界注入分类错误，组合文本由生产 `QuoteConninfo` 生成；生产 URI 解析、handler、脱敏函数及生成的响应序列化器均保持执行。四组请求的实际状态码均为 422，JSON 只含两个固定字段：

```json
{"code":"connection_test_failed","message":"connection test failed — verify host, port, credentials and TLS mode"}
```

`TestReview9Fixed422` 四个子用例全部 PASS；原秘密 `prefix\segment'CANARY-SUFFIX` 的单层 conninfo 与再次转义输入均不再进入响应。`b01ff7e` 对照在这两组响应中仍含 `CANARY-SUFFIX`。本轮不声称已补齐 `RedactKnownSecrets` 的全部组合枚举：该函数原有组合残留依然可复现，但固定响应已经切断所指定的 API 泄漏路径。

### R9-P1-01：新增连接失败日志直接写入原始口令

**新增回归，P1，REGRESSED。** 定位：`backend/internal/server/api_phase2.go:115`–`:117`，尤其是 `:116` 的 `"err", err.Error()`。

本次新增日志同时传入两个名为 `err` 的属性，第一个没有脱敏。实测使用与生产 `backend/cmd/supabackup/main.go:100` 相同的 `slog.NewJSONHandler`，原始日志字节保留两个字段，后一个不会覆盖已经写出的前一个。普通秘密即能复现；以下仅省略时间、级别等非关键字段：

```json
{"msg":"connection test failed","name":"fixture","err":"password authentication failed: CANARY-secret-pw","err":"password authentication failed: [REDACTED]"}
```

`TestReview9LogNoSecret` 四个子用例全部 FAIL；同一 fixture 在 `b01ff7e` 没有该错误日志，四组日志保密断言全部 PASS。探针检查原始日志字节，并逐 token 保留重复键，未通过解码成 map 丢掉第一个 `err`。实际结果：

| 秘密 / 错误表示 | 第一个原始 `err` | 第二个声称脱敏的 `err` |
| --- | --- | --- |
| 普通秘密 / 原值 | 泄漏 | 无 canary。 |
| 含单引号 / conninfo | 泄漏 | 无 canary。 |
| 反斜杠与单引号组合 / conninfo | 泄漏 | **仍含 `CANARY-SUFFIX`。** |
| 上述 conninfo 再次转义 | 泄漏 | **仍含 `CANARY-SUFFIX`。** |

所以只删除第一个 `err` 尚不足以关闭这个新增日志出口：第二个仍调用未覆盖 `Q(B(secret))` 的既有替换逻辑，`SanitizeMessage` 也只是插入标记，没有移除剩余值。以上归为同一个新增日志泄漏回归，不把旧替换函数的组合不足另计新缺陷。修复需移除原始错误属性，并确保唯一日志字段不含秘密；可记录固定错误文本及安全的分类信息，若保留错误详情则必须通过原值、组合转义和日志最终字节的保密断言。fixture 使用合成 canary 错误，不声称本轮真实 PostgreSQL 自然输出过口令。

### 工程检查与证据

环境：`go version go1.26.0 linux/arm64`；`GOCACHE=/tmp/supabackup-review-go-cache`。全量门禁未加载连接错误注入或定向探针。

| 检查 | 本轮结果 |
| --- | --- |
| 原生 `go test -race -json -count=1 ./backend/...` | **退出 1**：server 的 `httptest` 监听报 `socket: operation not permitted`；59 个顶层测试 PASS、5 SKIP、1 FAIL；无 race 报告。未将原生运行写为通过。 |
| 相同命令，增加 `-overlay /tmp/supabackup-phase2-review9-evidence/router-overlay.json` | **退出 0：75 个顶层测试 PASS、5 个 Docker 集成测试 SKIP**；10 个有测试包通过、6 个无测试文件；无 race 报告。 |
| 定向 API / 日志 / 脱敏探针，均以 `-race` 编译 | HEAD 共 6 个顶层测试：4 PASS、2 FAIL。失败分别为新增日志泄漏、既有组合替换残留。quoted 矩阵、三种简单秘密表示及仓库 `TestRound7QuotedRedaction` 均通过。 |
| 占位符终止性对照 | 基线 6/6 超时；HEAD 3/6 返回、3/6 超时，详见上表；无 race 报告。 |
| `git diff --check` | 通过。 |

Router overlay 沿用并重新核对上一轮的差异，仅替换测试 HTTP 传输为进程内 RoundTripper → 生产 Router → Recorder，保留 CookieJar、路由、中间件、handler 和原断言；不声称验证了真实 TCP/TLS 监听。Docker 资源不可用导致五项 SKIP，以上环境边界均不计为缺陷。本轮未重开其他历史项或扩大外部验收范围。

证据目录：`/tmp/supabackup-phase2-review9-evidence/`。`core-server-head.log` / `core-server-baseline.log` 保存 422 与日志新旧对照，`core-dumper-*.log` 保存脱敏矩阵；`marker-summary.json`、`marker-*.log` 保存终止性对照与堆栈；`race-native.jsonl`、`race-router.jsonl` 保存全量门禁。`README.md`、`builds.json`、`summary.json`、`sources.sha256`、overlay 和探针源码提供复现入口。

**第 9 轮最终结论：未通过。R8-P1-01 = PARTIALLY，P1-02（API 422 组合转义泄漏）= FIXED；R8-P1-01 的子串死循环与新增 R9-P1-01 日志泄漏仍为 P1 阻断。当前不能判为“通过”或“修改后通过”。**

---

## Phase 2 第 10 轮追加复审：7fad0b7 单点收敛

评审日期：2026-10-03。实际 HEAD：`7fad0b75c07a107c6bd6baf902516eab1cc08756`。范围：`git diff f44a3f7..7fad0b7`，仅复核本轮指定的两个修复、marker 终止性及差异引入的新回归。使用 `code-reviewer` 技能；仓库仅追加本节，生产代码、仓库测试均未修改。所有实测均在本轮重新编译、执行，定向探针通过 `/tmp` 中的 Go overlay 加载。

**两项判定：R9-P1-01 = PARTIALLY；P1-02（本轮指定的组合转义脱敏）= PARTIALLY。新回归数：0。最终结论：未通过。** 双 `err` / 原始错误属性已删除，marker 死循环也已关闭；但合法秘密经过 conninfo 再次转义后，仍有值完整进入唯一的日志 `err` 字段。

沿用上一轮关闭口径：上一轮已明确“只删除第一个 `err` 尚不足以关闭这个新增日志出口”，唯一字段也必须通过组合转义保密断言。因此“双字段子项 FIXED”不等于 R9-P1-01 整项 FIXED。下表两项的未关闭部分是**同一个组合脱敏残留**，不重复计为两个新缺陷。上一轮已判 FIXED 的固定 API 422 响应仍为 FIXED，没有重新打开该客户端泄漏；本轮 P1-02 判定针对用户指定的 `RedactKnownSecrets` 组合覆盖。

| 本轮指定项 | 判定 | 实测与关闭边界 |
| --- | --- | --- |
| **R9-P1-01：连接失败日志同时写原始与脱敏 `err`** | **PARTIALLY** | `server/api_phase2.go:117`–`:119` 已只保留一个脱敏调用，原始 `err.Error()` 属性删除；实际 JSON 日志逐 token 检查确认只有一个 `err`，并有不含密码的 `keyword_view`。上一轮四组日志探针全部转 PASS。但新增的相邻反斜杠 / 引号及连续反斜杠 fixture 仍在唯一字段中泄漏，原日志保密问题未完全关闭。 |
| **P1-02：已知秘密的组合转义替换不足** | **PARTIALLY** | `dumper/dumper.go:197`–`:207` 的单次正则替换修复了原 `prefix\segment'CANARY-SUFFIX` 的 conninfo 及再次转义场景。生成矩阵由基线 26/35 PASS 提升为 31/35 PASS；相邻 `\'` 或三个连续反斜杠经过同样两层转义后，仍有 4 个表示用例 FAIL。属于原组合覆盖不足的残留，不是新的转义层级要求。 |

### 剩余 P1：两层转义不等于每字符最多两个前置反斜杠

定位：`backend/internal/dumper/dumper.go:199`–`:200`；实际日志出口为 `backend/internal/server/api_phase2.go:119`。

最小复现使用生产 `QuoteConninfo` 和标准库 JSON 编码，不手写超出约定层级的转义文本：

```go
secret := `prefix\'CANARY-SUFFIX` // 原始秘密：一个反斜杠紧邻一个单引号
encoded, _ := json.Marshal(pgclient.QuoteConninfo(secret))
input := string(encoded)
output := dumper.RedactKnownSecrets([]string{secret}, input)
// 实测 output == input，CANARY-SUFFIX 完整保留。
```

原始 `\'` 经 `QuoteConninfo` 变成单引号前 **3 个实际反斜杠**，再经过 JSON 转义变成 **6 个**。当前表达式给原始反斜杠最多分配 `2 + 1 = 3` 个，再给单引号最多分配 2 个，总共只能匹配 **5 个**。开头额外的 `\\{0,2}` 位于 `prefix` 之前，不能弥补字符串中部的缺口。同理，三个连续原始反斜杠经过 conninfo 与 JSON 后变成 12 个，当前表达式连同下一个普通字符的前缀最多消耗 11 个。

| 秘密 / 表示 | `f44a3f7` | `7fad0b7` |
| --- | --- | --- |
| `prefix\segment'CANARY-SUFFIX` / conninfo、反斜杠再次加倍、JSON(conninfo) | 三组 FAIL | 三组 PASS。原复现已修。 |
| `prefix\'CANARY-SUFFIX` / 单层 conninfo | FAIL | PASS。 |
| `prefix\'CANARY-SUFFIX` / 反斜杠再次加倍、JSON(conninfo) | 两组 FAIL | **两组仍 FAIL。** |
| `prefix\\\segment'CANARY-SUFFIX` / 单层 conninfo | FAIL | PASS。 |
| `prefix\\\segment'CANARY-SUFFIX` / 反斜杠再次加倍、JSON(conninfo) | 两组 FAIL | **两组仍 FAIL。** |

以上秘密均通过生产 `ParseURI`。生成矩阵共 5 种秘密 × 7 种表示，包含原值、单引号转义、反斜杠加倍、两种组合及实际 JSON 编码；旧版 / HEAD 使用相同 fixture，仅 overlay 两个被审生产文件为 `f44a3f7` 原文进行对照。没有由 PASS 转为 FAIL 的矩阵用例，因此不标 REGRESSED，也不为同一残留新增编号。

生产 handler 的两组补充日志探针均复现泄漏。错误在 `pgclient.Test` 网络边界受控注入，探针保留生产 URI 解析、错误分类、handler、脱敏和 `slog.NewJSONHandler`；逐 token 检查原始 JSON 字节，避免 map 解码掩盖重复键。实际结果是 `err_count=1`、`keyword_view_count=1`，但唯一 `err` 的解码后值仍为以下字符内容，单引号前有 **6 个实际反斜杠**：

```text
password authentication failed: password=[REDACTED]'prefix\\\\\\'CANARY-SUFFIX'
```

`keyword_view` 实测为 `localhost:5432/appdb (user app, sslmode disable)`，未包含密码；实际 API 响应仍为固定 422，无 canary。这里没有否认双字段删除，也不声称真实 PostgreSQL 在本轮自然输出过这些口令。失败来自上一轮已经检查的 conninfo 再次转义路径，只改变了合法秘密中反斜杠的位置 / 数量。

仓库 `TestRedactEscapeMatrix` 实测 PASS，但其 `sec` 不含原始反斜杠，五个名称实际只有三种不同表示：`quoteEscaped == jsonEscaped`、`backslashDbl == doubleEscaped`，且没有调用 JSON 编码器，不能覆盖上述缺口。修复方向是根据实际转义转换生成匹配形式或建立等价的转义匹配规则，对原始输入做有界替换；补入相邻 `\'`、连续反斜杠以及真实 JSON 编码用例，并断言最终日志字节无秘密。避免重新引入扫描替换标记的循环。

### Marker 终止性：R8-P1-01 已关闭

**辅助复核判定：FIXED。** 仓库 `TestRedactMarkerSecretTerminates` 实测 PASS。另将 `[REDACTED]`、`REDACTED`、`prefix[REDACTED]suffix` 分别送入直接 `RedactKnownSecrets`、生产 `CreateDatabase`、真实子进程 stderr → dumper → Runner 成功日志三条路径，**9/9 返回，退出码均为 0**。API 返回固定 422，Runner 完成加密、文件提交并落库为 succeeded；不存在上一轮 `REDACTED` 无限扩张的阻塞。

这些探针在独立、启用 race 的测试进程中运行，每次设置 15 秒 Go 测试期限和 30 秒外部进程上限，均未触发期限。新实现对每个秘密只执行一次替换，标记即使仍含秘密字面值，也不会被同一秘密无限重扫；不把标记中的 `REDACTED` 文本本身误报为口令泄漏。

### 工程验证与最终结论

环境：`go1.26.0 linux/arm64`，`CGO_ENABLED=1`，`GOCACHE=/tmp/supabackup-review-go-cache`。全量工程检查不加载连接错误 fixture 或审计探针。

| 检查 | 本轮实际结果 |
| --- | --- |
| 原生 `go test -race -json -count=1 ./backend/...` | **退出 1**：server 的 `httptest` 监听报 `socket: operation not permitted`。61 个顶层测试 PASS、5 SKIP、1 FAIL；无 race 报告。 |
| 同一全量命令，增加 `-overlay /tmp/supabackup-phase2-review10-evidence/router-overlay.json` | **退出 0：77 个顶层测试 PASS、5 个 Docker 集成测试 SKIP**；10 个有测试包通过、6 个无测试文件；无 race 报告。 |
| 仓库 escape-matrix、marker 终止及 `TestRound7QuotedRedaction` | **全部 PASS**，包含在上述全量检查和 HEAD 定向检查中。 |
| 上一轮 quoted、三种简单表示、组合矩阵、固定 422、原始日志四场景探针 | HEAD **全部 PASS**；日志每条只有一个 `err`，四个原场景无 canary。 |
| 本轮生成矩阵与补充 handler 探针 | HEAD 定向检查总计 8 个顶层测试 PASS、2 FAIL。失败是生成矩阵的 4 个表示用例和 handler 的 2 个日志用例，均归属同一个组合转义残留；无 race 报告。 |
| Marker 三秘密 × 三路径 | **9/9 PASS**；无 race 报告。 |
| `git diff f44a3f7..7fad0b7 --check` / 追加报告后的 `git diff --check` | 通过。 |

Router overlay 重新核对了与 HEAD 的差异，仅替换测试 HTTP 传输为进程内 RoundTripper → 生产 Router → Recorder，保留 CookieJar、路由、中间件、handler 和原断言；不声称原生 TCP/TLS 监听通过。五项集成测试因 Docker PostgreSQL 容器不可用而 SKIP，环境限制不计代码缺陷。Runner 探针执行真实 SQLite、age、文件提交和 OS 子进程，网络元数据与 dump 内容为受控 fixture，不冒充真实数据库备份。

证据目录：`/tmp/supabackup-phase2-review10-evidence/`。`core-head.jsonl` / `core-baseline.jsonl` 保存全部定向新旧对照；`marker-summary.json`、`marker-*.log` 保存终止性检查；`race-native.jsonl`、`race-router.jsonl` 保存全量结果；`README.md`、`summary.json`、`sources.sha256`、overlay、差异和探针源码提供复现入口。

**第 10 轮最终结论：未通过。R9-P1-01 = PARTIALLY（双 err 子项 FIXED，唯一字段仍泄漏）；P1-02 = PARTIALLY；新回归 0。R8-P1-01 marker 不终止已 FIXED，固定 API 422 继续保持 FIXED。剩余组合转义日志泄漏仍为 P1 阻断，不能判为“通过”或“修改后通过”。**

---

## Phase 2 第 11 轮追加复审：416a11a 收敛确认

评审日期：2026-10-03。实际 HEAD：`416a11a44c1ee12689cce020feca06e1dde8c0f3`。范围：`git diff 7fad0b7..416a11a`，复核第十轮两个 PARTIALLY 及本次差异引入的新回归。使用 `code-reviewer` 技能。仓库仅追加本节；生产代码、仓库测试均未修改。定向探针通过 `/tmp` 中的 Go overlay 加载，旧 fixture 明确复用，所有结果均在本轮重新编译、执行。

**两项判定：R9-P1-01 = FIXED；P1-02（指定的组合转义脱敏残留）= FIXED。新回归数：1（R11-P1-01，P1，REGRESSED）。最终结论：未通过。** 原 4/35 失败已经关闭；阻断原因是本次移除 dumper 值级脱敏后，原先安全的 stderr 又能携带凭据进入 Runner 成功日志。

沿用原口径：FIXED 判断指定问题本身是否关闭，已满足验收后的强化仅标 NOTE；外部资源限制不计缺陷。本轮不因新发现的 dumper 回归而将已修好的 API 单字段日志、组合转义矩阵改判 PARTIALLY。下述三个新增失败形态归为同一项跨层脱敏回归，不重复计数。

| 本轮指定项 | 判定 | 实测与关闭边界 |
| --- | --- | --- |
| **R9-P1-01：双 `err` 与唯一字段中的组合转义泄漏** | **FIXED** | `server/api_phase2.go:117`–`:119` 仅保留 `keyword_view` 和一个经过 `redact.Secrets` 的 `err`。原四组日志 fixture，加相邻反斜杠/单引号、三个连续反斜杠两组残留，共 **6/6 PASS**。逐 token 保留重复键检查最终 JSON 日志，六条均为 `err_count=1`、`keyword_view_count=1`，无 canary；安全摘要均为 `localhost:5432/appdb (user app, sslmode disable)`。固定 422 响应保持通过。 |
| **P1-02：3+ 连续反斜杠导致组合转义 4/35 失败** | **FIXED** | `redact/redact.go:27`–`:36` 使用每字符任意数量前置反斜杠并单次替换。仓库 `TestSecretsMatrix` 通过；另原样复跑上一轮由生产 `QuoteConninfo` 和真正 `json.Marshal` 生成的 5×7 矩阵，`7fad0b7` **31/35 PASS、4 FAIL**，HEAD **35/35 PASS**。同样 35 个输入经真实子进程 stderr → dumper excerpt → Runner → 最终 JSON 成功日志，HEAD 也为 **35/35 PASS**。 |

基线矩阵使用 `git show 7fad0b7:backend/internal/dumper/dumper.go` 中的真实 `RedactKnownSecrets`；HEAD 矩阵仅迁移测试包和调用名至 `redact.Secrets`，秘密、表示生成器和断言未变。原失败的两种秘密分别经过反斜杠再次加倍和 JSON(conninfo) 的四个用例全部转 PASS，没有提高转义层级或更换验收标准。

Marker 终止性未回归：仓库两个终止性测试通过；`[REDACTED]`、`REDACTED`、`prefix[REDACTED]suffix` 经真实 dumper/Runner 路径均完成加密、提交、manifest 和 succeeded 状态，**3/3 返回**。不把替换标记本身包含 `REDACTED` 当成秘密泄漏。

### R11-P1-01：移除 dumper 值级脱敏后，跨层成功日志重新泄漏凭据

**新增回归，P1，REGRESSED；计数 1。** 改动定位：`backend/internal/dumper/dumper.go:169`–`:175`。相关保留边界：`:381`–`:394`，excerpt 生成：`:431`、`:485`；最终日志出口：`backend/internal/jobs/jobs.go:665`–`:667`。

旧实现会删除 `password=` 后的值和 PostgreSQL URI 主体；新实现只改写 URI scheme，并把值级删除全部交给 jobs 中的完整秘密匹配。这个交接不总能保留可匹配的原值：URI 中的密码可能是百分号编码，stderr 保留上限可能切断密码，scheme 改写也可能发生在密码内部。`redact.Secrets` 的反斜杠匹配规则无法补救这些内容变化。

以下是**相同 Runner 探针在基线与 HEAD 的实测对照**。秘密均通过生产 `ParseURI` 往返校验；最终状态均为 succeeded，artifact 和 manifest 均存在。表中是最终 JSON 日志中 `stderr_excerpt` 的解码值；长填充部分及尾部换行省略。

| 触发形态 | 合成 stderr 输入 | `7fad0b7` 最终日志 | `416a11a` 最终日志 |
| --- | --- | --- | --- |
| URI 百分号编码 | 秘密 `CANARY-P@ss/word`，由 `url.UserPassword` 生成 `postgres://app:CANARY-P%40ss%2Fword@localhost/appdb?sslmode=disable` | `postgres://[REDACTED]`，PASS。 | `postgres-uri://[REDACTED]app:CANARY-P%40ss%2Fword@localhost/appdb?sslmode=disable`，**FAIL，完整可逆编码口令仍在**。 |
| 保留边界截断普通密码 | 填充文本后接 `password=CANARY-cut-PASSWORD-SUFFIX`，16 KiB 边界恰好落在 `CANARY-cut-` 后 | 尾部为 `password=[REDACTED]`，PASS。 | 尾部为 `password=CANARY-cut-`，**FAIL，口令前缀泄漏**。 |
| scheme 出现在合法秘密内部 | 秘密 `prefixpostgres://CANARY-SUFFIX`，stderr 为 `password='prefixpostgres://CANARY-SUFFIX'` | `password=[REDACTED]'[REDACTED]'`，PASS。 | `password='prefixpostgres-uri://[REDACTED]CANARY-SUFFIX'`，**FAIL，替换拆碎原值后保留秘密内容**。 |

截断 fixture 的构造为：

```go
secret := "CANARY-cut-PASSWORD-SUFFIX"
stderr := strings.Repeat("x", (16<<10)-len("password=")-len("CANARY-cut-")) +
    "password=" + secret
```

这里的首次截断发生在 stderr 读取保留逻辑中，进入 `sanitize` 前已经只剩口令前缀；新 `sanitize` 的 `len(s) > stderrKeep` 检查不能恢复完整值，也不会给恰好 16 KiB 的该输入追加截断标记。旧 `plainPwRe` 可将保留到结尾的整个 `password=` 值删除，新路径只寻找完整秘密，因此泄漏。第三个 fixture 则完全不依赖截断或额外编码，仅本次新增的 scheme 改写就会破坏完整秘密匹配。

上述三组保密断言在基线 **3/3 PASS**，HEAD **3/3 FAIL**；属于本次实际退化，不是新增验收条件，也不是仅观察到内存中的中间 excerpt。探针执行真实 OS 子进程、管道读取、SQLite、age、文件提交和生产 `slog.NewJSONHandler`；仅连接检查、依赖元数据和 dump 内容为受控 fixture。不声称真实 PostgreSQL 在本轮自然输出过这些口令，也不将这些成功日志泄漏误记为 API 422 泄漏或数据库错误字段泄漏。

修复应保证日志出口删除凭据值：处理完整原值时先脱敏再做会改变内容的标签替换；为 URI 的编码凭据保留安全删除能力；对截断处可能残留的凭据片段采取删除或丢弃策略。不能只增大 `\\*` 的匹配能力，也不能简单取消流读取上限。修复后应以以上真实跨层的最终日志断言验收，并保持现有组合矩阵和 marker 终止性通过。

### 验证结果、NOTE 与最终结论

环境：`go1.26.0 linux/arm64`，`CGO_ENABLED=1`，`GOCACHE=/tmp/supabackup-review-go-cache`。全量工程检查不加载连接错误 fixture 或审计探针。

| 检查 | 本轮实际结果 |
| --- | --- |
| 原生 `go test -race -json -count=1 ./backend/...` | **退出 1**：server 的 `httptest` 监听报 `socket: operation not permitted`；63 个顶层测试 PASS、5 SKIP、1 FAIL。未将原生运行写为通过。 |
| 同一全量命令，增加 `-overlay /tmp/supabackup-phase2-review11-evidence/router-overlay.json` | **退出 0：79 个顶层测试 PASS、5 个 Docker 集成测试 SKIP**；11 个有测试包通过、6 个无测试文件。 |
| 仓库 redact 矩阵、marker 测试及 `TestRound7QuotedRedaction` | **全部 PASS**，已包含于上述全量检查。 |
| 上一轮真实组合矩阵重放 | 基线 31 PASS / 4 FAIL；HEAD **35/35 PASS**。 |
| API 固定 422 / 唯一日志字段 | HEAD 六场景全部 PASS；基线相邻反斜杠/单引号与三个连续反斜杠两场景仍 FAIL，符合上一轮记录。 |
| 真实子进程 → dumper → Runner → JSON 日志 | 原组合矩阵 **35/35 PASS**；marker **3/3 返回**；新增回归三个场景 **基线 PASS → HEAD FAIL**。 |
| HEAD 定向探针合计 | 6 个顶层测试 PASS、1 FAIL；唯一失败测试为 `TestReview11CrossLayerRegressions` 的三个子用例。 |
| Race / diff 检查 | 所有运行均无 data race 报告；`git diff 7fad0b7..416a11a --check` 及追加后的 `git diff --check` 通过。 |

Router overlay 已重新核对，仅替换测试 HTTP 传输为进程内 RoundTripper → 生产 Router → Recorder，保留 CookieJar、路由、中间件、handler 和原断言；不声称验证了真实 TCP/TLS 监听。Docker PostgreSQL 容器不可用导致五项 SKIP。以上环境边界不计缺陷，不改变本轮新回归的代码判定。

**NOTE（不阻断原两项关闭）：** `redact/redact_test.go:25` 的 `json` 实际与 `backslashDbl` 相同，仓库矩阵仍未直接调用 JSON 编码器，且仅检查完整秘密/完整 form 是否残留。建议将本轮已通过的生产 `QuoteConninfo` + 标准库 [`json.Marshal`](https://pkg.go.dev/encoding/json#Marshal) fixture 和最终日志保密断言纳入仓库。当前真实组合验收已经由独立探针满足，因此这里只记覆盖强化，不据此改判 PARTIALLY。

证据目录：`/tmp/supabackup-phase2-review11-evidence/`。`race-native.jsonl` / `race-router.jsonl` 保存全量结果，`probes-head.jsonl` / `probes-baseline.jsonl` 保存 API 与真实 Runner 新旧对照，`matrix-baseline.jsonl` 保存旧版四个失败重现。`README.md` 提供复现命令和 fixture 边界；`summary.json` 保存计数及六条保留重复键的日志检查，`sources.sha256`、overlay、探针源码及差异文件提供复核入口。

**第 11 轮最终结论：未通过。R9-P1-01 = FIXED；P1-02（组合转义残留）= FIXED；新回归 1（R11-P1-01，P1）。原两个问题已关闭，但本次引入的跨层成功日志凭据泄漏仍需修复，当前不能判为“通过”或“修改后通过”。**

---

## Phase 2 第 12 轮追加复审：1fb0350 单点确认

评审日期：2026-10-03。实际 HEAD：`1fb0350f09c17dd9d80230dfb88771886fb4be02`。范围：`git diff 416a11a..1fb0350`，仅复核 R11-P1-01 的三个既有泄漏形态与本次差异的新回归。使用 `code-reviewer` 技能。仓库仅追加本节，生产代码和仓库测试未修改；审计探针通过 `/tmp` Go overlay 加载，所有结果均在本轮重新执行。

**判定：R11-P1-01 = NOT_FIXED（P1，仍阻断）。新回归数：0。最终结论：未通过。** 三个既有 fixture 在 `416a11a` 与 HEAD 的生产 Runner 最终成功日志中均为 **3/3 FAIL**。新增 helper 局部有效，但没有接入生产路径，不能据此判为 PARTIALLY；本轮确认的是原问题未关闭，不另计 REGRESSED，也不重开此前已关闭的组合转义问题。

### R11-P1-01：新增 excerptOf 未接线，生产路径三个形态全部仍泄漏

**主要定位：** `backend/internal/dumper/dumper.go:173`、`:216`–`:218`、`:483`–`:485`、`:538`；日志出口 `backend/internal/jobs/jobs.go:665`–`:667`。

本轮代码实际调用链为：

```text
子进程 stderr → 保留前 16 KiB → sanitize(string(stderrData))
→ Result.StdErrExcerpt → jobs redact.Secrets → slog JSON 成功日志
```

`Run` 在 `dumper.go:484` 仍调用 `sanitize`；生产代码没有调用 `excerptOf`，仅新增测试调用它。因此新增的尾行丢弃和 `removePasswordForms` 均未执行。本 diff 对实际 `sanitize` 路径只是去掉 scheme 后的 `[REDACTED]` 标签及原长度检查，并未在日志出口前删除凭据值。`jobs.go:514` 记录的是原始 `ci.Password`，`:667` 的 `redact.Secrets` 仍无法匹配百分号编码、截断片段或被 scheme 改写拆开的原值，不能承担修复主体。

以下重放上一轮**原样保留的** `TestReview11CrossLayerRegressions`。每例均通过生产 `ParseURI` 往返校验，任务为 succeeded，artifact 和 manifest 均存在。表中为最终 JSON `stderr_excerpt` 的解码内容，省略尾部换行和截断例的填充字符。

| 原泄漏形态 | `416a11a` 最终日志 | `1fb0350` 最终日志 | 判定 |
| --- | --- | --- | --- |
| URI 百分号编码，秘密 `CANARY-P@ss/word` | `postgres-uri://[REDACTED]app:CANARY-P%40ss%2Fword@localhost/appdb?sslmode=disable` | `postgres-uri://app:CANARY-P%40ss%2Fword@localhost/appdb?sslmode=disable` | **NOT_FIXED**，可逆编码口令仍在。 |
| 16 KiB 保留边界切断 `CANARY-cut-PASSWORD-SUFFIX` | 尾部 `password=CANARY-cut-` | 尾部 `password=CANARY-cut-` | **NOT_FIXED**，口令前缀仍在。 |
| 原始秘密 `prefixpostgres://CANARY-SUFFIX` 内含 scheme | `password='prefixpostgres-uri://[REDACTED]CANARY-SUFFIX'` | `password='prefixpostgres-uri://CANARY-SUFFIX'` | **NOT_FIXED**，原值仍先被改写、再匹配失败。 |

另用真实 `Config.Run` 单独读取 `Result.StdErrExcerpt`，三例均已含凭据内容；再调用真实 `redact.Secrets` 仍为 **3/3 FAIL**。这与真实 Runner 最终日志结果一致，不能确认“dumper excerpt 已删值 → jobs 仅防线”的预期分工成立。

### 即使接入 helper，raw 跳过条件仍会使第三个 fixture 泄漏

`removePasswordForms` 虽以 `forms := []string{pw}` 开始，但循环条件是 `f != "" && f != pw`，明确跳过原始口令，也跳过所有恰好等于原值的表示。对带换行的第三个原 fixture 实测：

```text
password:     prefixpostgres://CANARY-SUFFIX
input:        password='prefixpostgres://CANARY-SUFFIX'\n
excerptOf:    password='prefixpostgres-uri://CANARY-SUFFIX'
jobs redact:  password='prefixpostgres-uri://CANARY-SUFFIX'
```

所以仅把 `Run` 改为调用 `excerptOf` 仍不足以关闭原问题，必须让原始值也在 scheme 改写前被完整删除。带完整换行、保留非敏感诊断前缀的五种表示探针结果为：**raw FAIL；backslash-doubled、quote-escaped、percent-all、percent-special 四项 PASS**。原三 fixture 经正确模拟保留上限后直接进入 helper，则 URI 编码和尾行截断两项 PASS，scheme 项 FAIL。这些是同一未关闭问题的原因与定位，不重复登记为独立新回归。

### 指定仓库测试通过，但当前断言不能证明修复

`TestExcerptOfReviewFixtures` 的三个 fixture 与 `TestExcerptOfPercentEncodingMatrix` 的两个输入均没有换行，`excerptOf` 在 `dumper.go:177`–`:180` 将它们全部清空；实测五个输出均为 `""`。测试因此全部 PASS，尚未覆盖保留下来的完整诊断行中的值删除。

此外，截断 fixture 直接传入完整长字符串，没有经过生产 16 KiB 保留切片，且只检查完整口令；scheme fixture 也只检查完整原值。这样的断言不能捕获已被切断或改写后仍含 `CANARY` 的泄漏。本轮独立探针使用真实保留切片、完整行换行和凭据片段断言；跨层验收继续使用上一轮原 fixture 与最终日志断言，未改变关闭标准。

修复应把 `excerptOf(stderrData, t.Conn.Password)` 接入实际结果生成处，修正跳过 raw 的条件，确保值删除先于 scheme 改写；以原三例真实 Runner 最终日志无凭据验收，并将非空完整行、真实截断及片段检查纳入仓库测试。继续保留 stderr 全量排空与有界保留，以及已通过的组合转义和 marker 终止性。此处只记录修复要求，未修改实现。

### 验证结果、证据边界与最终结论

环境：`go1.26.0 linux/arm64`，`CGO_ENABLED=1`，`GOCACHE=/tmp/supabackup-review-go-cache`。全部 Go 运行使用 `-race -count=1`，全量工程检查不加载连接 fixture 或审计探针。

| 检查 | 本轮实际结果 |
| --- | --- |
| 仓库 `TestExcerptOfReviewFixtures`、`TestExcerptOfPercentEncodingMatrix` | **全部 PASS**；空输出原因见上文。附带 `TestParseURIRoundTripStillValid` 也 PASS。 |
| 原生 `go test -race -json -count=1 -timeout=120s ./backend/...` | **退出 1**：server 的 `httptest` 监听报 `socket: operation not permitted`；66 个顶层测试 PASS、5 SKIP、1 FAIL。 |
| 同一全量命令，加本轮 `router-overlay.json` | **退出 0：82 个顶层测试 PASS、5 个 Docker 集成测试 SKIP**；11 个有测试包通过、6 个无测试文件。 |
| HEAD helper 与真实 dumper 定向探针 | 1 个顶层测试 PASS、3 FAIL：完整行 scheme、五形态中的 raw、真实 dumper 三例保密断言失败。 |
| 原三例真实 Runner 最终日志，新旧对照 | `416a11a` **3/3 FAIL**；HEAD **3/3 FAIL**。属于既有泄漏持续存在。 |
| 原 5×7 组合矩阵重放及真实 Runner 路径 | 直接 `redact.Secrets` **35/35 PASS**；真实子进程 → dumper → Runner 最终日志 **35/35 PASS**。 |
| Marker 终止性 | 三种秘密 `[REDACTED]`、`REDACTED`、`prefix[REDACTED]suffix` 经真实 Runner **3/3 返回并 succeeded**；仓库 marker 测试也 PASS。 |
| Race / diff 检查 | 所有运行均无 data race 报告；`git diff 416a11a..1fb0350 --check` 及追加后的 `git diff --check` 通过。 |

Router overlay 已核对差异，仅替换测试 HTTP 传输为进程内 RoundTripper → 生产 Router → Recorder，保留 CookieJar、路由、中间件、handler 和原断言；不声称原生 TCP/TLS 监听通过。Docker PostgreSQL 容器不可用导致五项 SKIP，环境限制不计缺陷。Runner 使用真实 SQLite、OS 子进程与管道、age、文件提交及生产 JSON 日志，只有网络元数据和 dump/stderr 内容是受控 fixture，不声称真实 PostgreSQL 自然输出过这些口令。

证据目录：`/tmp/supabackup-phase2-review12-evidence/`。`stock-excerpt.jsonl` 保存指定测试；`dumper-probes.jsonl` 保存 helper 与真实 dumper 结果；`probes-head.jsonl` / `probes-baseline.jsonl` 保存原 fixture 的跨层对照；`race-native.jsonl` / `race-router.jsonl` 保存全量检查。`README.md` 提供复现命令与退出码，`summary.json`、`sources.sha256`、overlay、差异及探针源码提供复核入口。

**第 12 轮最终结论：未通过。R11-P1-01 = NOT_FIXED；新回归 0。三个既有生产日志泄漏形态全部仍可复现，P1 阻断未关闭，不能判为“通过”或“修改后通过”。**
