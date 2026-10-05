# Phase 8 第二轮复审 — 076ba19

日期：2026-10-05。评审范围：`git diff 90f41ac..076ba19`，工作区 HEAD 为 `076ba19`。所有 Go 验证使用 `GOCACHE=/tmp/codex-phase8r2-cache`。

编号依据是 **076ba19 中的** `docs/reviews/codex-phase8-review-1.md`（声明评审基线 90f41ac，P1×5 + P2×4）。注意该报告也在本次 diff 内被替换；90f41ac 树内同名文件还是更早一版，编号不同。本轮不混用两版。用户摘要中的 P2-06 对应第一轮“其他统计边界”吞错观察项，作为附加项单独复核；不虚构 P2-05。

## 总体结论

**不通过 Phase 8 完成 / 公测发布门禁。** 九个正式编号：FIXED 2 项，PARTIALLY_FIXED 5 项，NOT_FIXED 1 项，REGRESSED 1 项。附加 P2-06 的查询错误处理已修复。

最直接的新增 P1 回归是 `/api/stats` 在正常生产 schema 下必定返回 500，而且有两个相互独立的阻断错误。四出口 canary 的重写没有进入被评审提交；Phase 6 新断言检查的密码也没有进入连接串。脱敏改进真实存在，但反向组合和 Unicode 边界仍不能通过。没有发现可据此定为 P0 的问题。

## 逐项判定

| 编号 | 判定 | 证据及关闭情况 |
| --- | --- | --- |
| P1-01 | PARTIALLY_FIXED | `backend/internal/pgclient/inspect.go:183`、`:221`；`backend/internal/server/api_phase2.go:123`。quoted/转义/未闭合引号、多个 password、ASCII 大小写、空白、marker 幂等与日志顺序已改善；反向组合仍留残片，Unicode 索引仍可漏敏。见下文。 |
| P1-02 | NOT_FIXED | `backend/internal/jobs/phase8_canary_test.go:34`、`:109`、`:129`、`:150` 在 diff 中完全未修改。无真实订阅/worker、单次 Read、零收包通过、手写 manifest、忽略 kit 不存在全部仍在。Phase 6 新密码未注入，见 `phase6_e2e_test.go:69` 和 `m1_test.go:42`、`:92`。 |
| P1-03 | REGRESSED | `backend/internal/server/api_phase7.go:405` 读不存在列，`:435` 的 Scan 数量不符，统计卡片从可用退化为消失。`jobs.go:942` 写真实 archive 值正确，但导出分段仍复用总体成功数、通知结果未交付。 |
| P1-04 | PARTIALLY_FIXED | `docs/capacity.md:12`、`:16`、`:22`、`:27`、`:40` 已纠正主要测量边界；`:49` 仍以仅 dump 吞吐给备份窗口总负载，MB/MiB、0.57 与 0.464 的不同分母及实际 Linux 执行位置尚未讲清。 |
| P1-05 | PARTIALLY_FIXED | `.github/workflows/ci.yml:41` 新增扫描 job，`CHANGELOG.md:78` 清理旧工具链限制；但浅克隆不满足 gitleaks PR 扫描范围，Trivy、三平台桶下载恢复记录与相应发布门禁仍缺失。 |
| P2-01 | PARTIALLY_FIXED | `backend/internal/jobs/jobs.go:861`、`:866`、`:943` 使用同一 duration 值写两表，AVG 排除历史 0 已修；接口被 P1-03 阻断，空集合仍返回 0，且“dump duration”实际包含远端上传。 |
| P2-02 | FIXED | `backend/internal/server/api_phase7.go:461` GROUP BY 后主键回连；真实迁移数据库 EXPLAIN 不再出现相关子查询。这里只关闭平方成本问题，不表示当前 GetStats 可用。 |
| P2-03 | PARTIALLY_FIXED | `docs/disaster-recovery.md:26`、`:35`、`:61`、`:65`、`:90` 修复密钥分类、目的地凭据、校验命令、临时盘与 WAL/SHM；`:70` 新增错误 createdb 用法，哈希失败未阻止后续恢复，重建清单还遗漏情况 1。 |
| P2-04 | FIXED | `backend/internal/jobs/remediation.go:13`、`:17`、`:21`、`:23` 已区分 session/transaction、实际 pg_dump 版本、storage 与 retention、dump 与异步验证。关闭本项错误文案；不代表异步验证失败已增加独立 remediation UI。 |
| P2-06（附加） | FIXED | `backend/internal/server/api_phase7.go:408`、`:436`、`:442`、`:449`、`:466` 都检查查询错误并返回 500；不再吞错伪装零值。正常请求也 500 是另一项 SQL 回归。 |

## 阻断与新增问题的具体证据

### R2-P1-01：统计接口存在两个独立的确定性 500（新增，归入 P1-03）

1. `backend/internal/server/api_phase7.go:405` 在 `FROM jobs` 中求 `SUM(dump_size)`，但该字段只存在于 `backup_stats`（`backend/internal/db/migrations/0010_stats.sql:7`）。本提交没有给 jobs 增列，也没有 JOIN stats。
2. `backend/internal/server/api_phase7.go:429` 开始只 SELECT 五个表达式，`:435` 却传六个 Scan 目标。`vFailed` 已合并 failed/unsupported，又多传了 `vUnsupported`。

无监听 Go 探针使用真实 `db.Open` + 全量 `Migrate`，直接调用生产 `apiService.GetStats`：

```text
production schema: response=api.GetStats500JSONResponse
first query: SQL logic error: no such column: dump_size (1)
after hypothetical column repair: response=api.GetStats500JSONResponse
segment scan shape: sql: expected 5 destination arguments in Scan, not 6
```

第二次调用仅在一次性测试数据库中补列，生产文件未修改。这证明只修第一条 SQL 不足以恢复接口。前端 `frontend/src/App.tsx:221` 遇到查询错误直接 `return null`，因此表现为整个 Statistics 区块消失。`make api-check`/vet/build 均无法发现这种运行时 SQL 错误。

建议从明确权威的数据源读取 archive，避免 JOIN 重复统计 job；同时修正 SELECT/Scan，并加入真实迁移后的空库、有成功/失败任务两类 GetStats 回归用例。

### P1-01 余项：组合并非双向安全，Unicode 小写副本不能复用原字节索引

`inspect.go:172` 声称任意顺序安全，但单独 marker 幂等不能保证完整 secret 匹配。无监听调用实际函数得到：

| 输入 / 调用 | 实际结果 |
| --- | --- |
| `password='it\'s' x` | `password=[REDACTED] x` |
| `password='unterminated` | `password=[REDACTED]` |
| `PASSWORD=one password=two` | 两个值均擦除 |
| `spassword=keep` | 保留，符合整词要求 |
| `postgres://:5432/db` | `postgres-uri://[REDACTED]/db` |
| `PostGreSQL://u:secret@host/db` | `postgres-uri://[REDACTED]/db` |
| 先 Secrets 再 Sanitize，secret=`alpha,beta` | `password=[REDACTED] host=x` |
| 先 Sanitize 再 Secrets，同一 secret | `password=[REDACTED],beta host=x` |
| `password=alpha\ beta host=x` | `password=[REDACTED] beta host=x` |
| `İ password=secret` | 原样输出，值未擦除 |
| `İ postgres://u:secret@host/db` | `İpostgres-uri://[REDACTED]/u:secret@host/db` |

最后两例是 Unicode U+0130：`strings.ToLower` 改变 UTF-8 字节长度，`inspect.go:184`、`:223` 的 lower 副本偏移被拿去切原 msg，结果错位。URI 例直接保留 userinfo；keyword 例未识别赋值。此处是本轮新增发现，不声称旧 keyword 实现对 Unicode 原本安全。

真正的连接测试日志已改为正确顺序（`api_phase2.go:123`），主备份链也先做已知 secret 擦除（`jobs.go:643`），因此不能把上述函数探针扩大为“所有真实任务都泄漏”。但 `jobs.go:992` 的 fail 兜底直接依赖 Sanitize，且公共 scrubber 的“双向安全”承诺仍不成立。bare conninfo 的反斜杠转义空格也没有被当作值的一部分处理。

额外诊断损失：`postgres://u:p@host error reading /tmp/x` 输出 `postgres-uri://[REDACTED]/tmp/x`，因为 authority 扫描只认 `/`，不认消息终止边界（`:205`）。应明确 URI/token 边界，避免吞掉后面的错误描述。

复杂度方面可以认可：两个 helper 各 lowercase 一次，游标单调推进，Builder 累加，已消除上一轮反复重建全文的 O(k·n) 模式，整体为 O(n) 时间/空间；不应继续把旧复杂度当未修复项。修复建议是 ASCII 不变长大小写匹配，统一 Secrets→语法脱敏的调用约束，并补上述行为测试，而非把 marker 幂等解释为任意顺序安全。

### P1-02 余项：修复摘要中的四出口实现未提交，Phase 6 新 canary 为空断言

`git diff 90f41ac..076ba19 -- backend/internal/jobs/phase8_canary_test.go` 无输出。

- 接收端仍是固定 8192 buffer 的单次 `Read`（`:39`），没有 `io.ReadAll`。
- 仍只调用 `r.fail`（`:78`）；没有 CreateWebhook、notifier worker 或真实坏主机执行。
- outbox 查询可以零行，Scan 错误被忽略（`:109`、`:115`），也未检查 rows.Err。
- manifest 仍为测试自己写的 `{"backupId":"canary"}`，没有检查其内容；kit 缺失或空内容都通过（`:129`、`:133`）。
- 接收超时后没有 `n > 0` 断言，没有 delivered 状态断言，空切片直接通过（`:150`、`:163`）。因此不存在摘要宣称的“delivered 但零收包必失败”保护。

Phase 6 的正向工件检查确实更强：`phase6_e2e_test.go:199` 起要求密文/kit 存在，kit 必须含真实哈希，`:213` 要求 manifest 可读；空 kit 会因旧哈希断言失败，manifest 缺失会失败，但空 manifest 不会因本次新增的 Contains 检查失败。

关键是 `phase6_e2e_test.go:69` 声明的 `CANARY-e2e-P@ss-7c21` 根本未进入 URI。真实 helper 使用 `m1_test.go:42` 的 `CANARY-integration-P@ssw0rd`，URI 为 `postgres://postgres:<testPW>@…`（`:92`）；新替换只匹配 `:cap` 或无密码的 `postgres://postgres@`，两者都不存在。本轮独立执行相同替换得到 `contains asserted canary = false`。后续注册/pg_dump 仍用旧密码，检查新字符串不能证明真实密码不泄漏。也不能只把 URI 换成新密码而不更新 PostgreSQL 角色密码；最小可行做法是直接断言实际 `testPW`，并在执行前检查解析后的 password 与 canary 相同。

### P1-03 / P2-01 余项：分段与耗时口径仍需闭环

- **导出分段**：`api_phase7.go:429` 只数 succeeded，`:489` 分母为全部终态 job，等于总体成功率。`jobs.go:845` 的上传失败发生在 dump/manifest 成功之后，却让 export 计失败。取消于排队、启动恢复的 interrupted、尚未执行 dump 的失败也进入分母。可以定义“所有请求最终导出率”，但必须保留“已导出、远端失败”的成功分子；当前代码既不符合这个定义，也不是“导出尝试成功率”。应以阶段权威状态记录分子、分母和未执行。
- **远端与验证**：committed/deleted 对 upload failure、verified 对 failed/unsupported 的意图合理，分母为零返回 nil 是正确方向；verify 的“among succeeded”注释与 SQL 仅过滤非运行状态不完全一致。先修 Scan，再用实际状态组合测试。通知分段仍没有字段/展示，未满足 `docs/dev-plan.md:22`。
- **耗时双写**：现在 success UPDATE、重试 UPDATE 和 stats.Entry 用同一个 `dumpDuration`，不存在两次 `time.Since` 导致的数值漂移。但两个写入不在同一事务，stats 是 best effort，崩溃/写失败仍可能只有 jobs 有值；应以 jobs 为耗时权威，不能假设每个成功 job 都有 stats 行。
- **空值新增展示回归（P2）**：`api_phase7.go:403` 用 COALESCE 把无有效样本变 0，`:480` 总返回非 nil；`frontend/src/App.tsx:233` 新改为仅判断 null，因此修复 SQL 后空库/全历史 0 会显示 `0.0s`，原来显示 `—`。使用可空 AVG 保留 unknown。
- **契约口径漂移（P2）**：`api/openapi.yaml:1148` 将其称为 measured dump durations，然而 `jobs.go:861` 在远端上传完成后才取时间，包含 manifest 和上传。要么改名/说明为备份执行耗时，要么在 dump 完成时停止计时；不能把网络耗时误读为 pg_dump 性能。

### R2-P2-01：gitleaks 新 job 浅克隆不具备扫描所需历史

证据：`.github/workflows/ci.yml:45` 没有 fetch-depth，checkout 默认仅取一个 commit。gitleaks-action v2 在 PR 中取首尾提交 SHA，然后使用 `firstSHA^..lastSHA`，多提交 push 也需要父提交历史。默认浅克隆通常缺少这个范围，扫描会因 revision 不存在报错，无法形成有效审计结果。应使用 `fetch-depth: 0` 或明确补齐所有所需对象。[checkout v4 官方说明](https://github.com/actions/checkout/tree/v4)、[gitleaks v2 实现](https://raw.githubusercontent.com/gitleaks/gitleaks-action/v2/src/gitleaks.js)。

CI 其余重点核查：

- Python `yaml.safe_load` 通过，所有 run 块 `bash -n` 通过；security job 缩进和多行 run 有效。
- govulncheck 安装/执行没有 continue-on-error；失败会使 job 失败，并跳过后续默认条件的 gitleaks。`@latest` 每次需确认/构建工具，版本浮动、网络失败和耗时没有独立上限配置；本轮未实跑安装/扫描，不能给出秒数或“0 漏洞”结论。建议固定工具版本、记录版本与扫描目标，必要时拆开独立门禁。
- **fork PR 不等于没有 GITHUB_TOKEN**：GitHub 会提供只读 token，本提交确实传入它；读取公开 PR commit 列表不要求写 token。默认 PR 评论可能因写权限失败，上游对写评论失败会警告，不能武断认定所有 fork PR 因 token 必失败。可关 `GITLEAKS_ENABLE_COMMENTS`，无需为此改为执行不可信代码的特权事件。[GitHub fork PR 规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request)、[gitleaks v2 实现](https://raw.githubusercontent.com/gitleaks/gitleaks-action/v2/src/gitleaks.js)。
- 若仓库属于 Organization，v2 另要求 GITLEAKS_LICENSE，当前 env 未传；fork PR 通常也拿不到自定义 secret。仓库未配置 remote，本轮不推断归属，此项为部署条件而非已证实该仓库失败。[gitleaks v2 入口](https://raw.githubusercontent.com/gitleaks/gitleaks-action/v2/src/index.js)。
- 新 job 不补足 Trivy 和三平台实际恢复记录。image 的 needs 未包含 security；没有证据表明已将全部门禁接入发布流程或分支保护，不能把增加 YAML 等同于门禁已经运行通过。

### P1-04 余项：容量数字的解释与外推边界

51 与 24 的并存已解释清楚：873/17≈51 是源库物理体积口径，405/17≈24 是 archive 口径，不是矛盾。4 小时乘 51 约 734,400 MB，粗略写 700 GB 在量级上可理解；但 `docs/capacity.md:49` 仍称“备份窗口可支撑总量”，漏掉同一 worker 的上传等阶段，应限定为 **dump-only 的理想外推上界**。未测量上传/真实验证/磁盘峰值，就还不能关闭容量发布门禁。

0.57 是随机 hex payload 的 zlib 输出/输入比；405/873≈0.464 是 archive/源库物理体积比，分母含表、索引、页等，两者并不冲突。当前 `:34` 紧邻三体积又写“压缩比≈0.57”，没有解释不同分母。目标提交已没有显式“46%”表列，但原始体积隐含该比值，不能把 0.57 当 873→405 的实测比。

`scripts/capacity-benchmark.sh:86` 用 `du -m` 输出分配空间 MiB，文档仍写 MB；`:88` 的 pg_size_pretty 与原始 bytes 也应统一换算。`:30` 脚本注释仍称 hex 不可压。`docs/capacity.md:10`、`:14` 称 Apple Silicon 宿主机客户端，`:78` 又要求 Linux `/proc` 与 Debian PG 布局，仍缺实际执行 Linux VM/容器的说明；Docker Desktop 存在本身不证明宿主机 pg_dump 在 Linux 运行。RPO “实际上限/最坏”措辞（`:62`、`:65`）也应限定健康稳态，故障无固定上限。

### R2-P2-02：恢复文档新 createdb 命令未连接 TARGET

`docs/disaster-recovery.md:70` 的 `createdb "$TARGET"` 将 URI 当作待创建的数据库名字；createdb 的位置参数是 dbname，不像 pg_restore -d/psql 那样解析为连接串。它仍按默认连接环境访问服务器，可能连错本地服务器并创建错误名称的库。应明确维护库连接与目标名称，例如 `createdb --maintenance-db="postgresql://user@host:5432/postgres" restored`，随后两个命令使用同一目标 URI。[PostgreSQL createdb 文档](https://www.postgresql.org/docs/current/app-createdb.html)。

哈希命令现在存在，但代码块没有 `set -e`/显式退出条件（`:58`、`:61`）；整段复制执行时校验失败仍继续解密、恢复，不能叫强制门禁。应让哈希失败及 mktemp/age/createdb 失败明确停止，并将恢复放在失败即退出的脚本环境中。

密钥分类与目的地同损描述正确，但 `:32` 的“情况 2/3”应覆盖情况 1/3，以及无法找回原密钥的情况 2；情况 2 还需要明确先安全替换损坏文件才能启动重建。pre-migrate 现在说明每版本一份/5 批次有价值，不过文件实际在 `<dataDir>/backups/`（`backend/internal/db/backup.go:14`、`:18`），文档 `:89` 仅写数据目录；legacy 保留规则仍未列出。这些不应掩盖已经修好的 WAL/SHM 隔离与交叉引用。

## 验证记录与范围限制

| 验证 | 结果 |
| --- | --- |
| `make api-check` | 通过；后端/前端生成契约无漂移 |
| `go vet ./backend/...` | 通过 |
| 前端 `npm run build` | tsc + Vite 通过 |
| `go test` pgclient / redact / db | 全包通过 |
| 同批 jobs / server 全包 | 沙箱禁止 httptest 监听：`socket: operation not permitted`；不算通过，也不算产品回归 |
| `go test -race` pgclient / redact / db | 全包通过；pgclient 同时包含本轮临时探针 |
| jobs 选定 race | VerifyResumeRunsAtStartup、StopCancelsAndRequeues、NoVerifierMeansExplicitSkip、VerifyDetailRedacted、ManifestMissingIsHonestSkip 均通过 |
| 无监听 GetStats + 真迁移探针 | 复现两个 500；第一错误为缺列，临时补列后暴露 Scan 数量错误 |
| GROUP BY 的真实驱动 EXPLAIN | `CO-ROUTINE m → SCAN jobs USING INDEX idx_jobs_database_created → SCAN m → SEARCH j2 USING INTEGER PRIMARY KEY`；不再有相关子查询。未重跑大样本计时，不编造性能数字 |
| 无监听脱敏探针 | 通过的边界与未通过的组合/Unicode 实际输出见上表 |
| Phase 6 E2E 单独执行 | Docker run 不可用而 SKIP；命令 exit 0 不代表 E2E 通过 |
| Phase 6 URI 替换独立复现 | 新断言 canary 未进入 URI |
| CI YAML / Bash | Python PyYAML 解析及所有 run 块 `bash -n` 通过 |
| govulncheck / gitleaks / Trivy / 容量 / 三平台恢复 | 未执行；仅检查实现/文档，不为扫描或容量结论背书 |

未发现本 diff 引入明确的新数据 race；有限 race 用例不能证明完整通知/真实恢复链无竞态。额外提交了无关文件 `backend/ebhook|---.|cat|:1`，内容是调试 shell 命令残片，建议清除；没有发现其被业务调用，不把它升级为安全漏洞。

临时 Go 探针已删除，仅保留本报告；未修改业务实现。

## 最关键的 3 件事

1. **先恢复统计接口**：同时修缺列和 Scan 数量，加入真实迁移后的 GetStats 测试，再核对 export 分母、unknown 耗时及三体积权威来源。
2. **让 canary 确实携带凭据且出口不能为空**：提交缺失的四出口重写，断言实际连接密码、完整 webhook 收包、真实 manifest/kit；补 Unicode、转义和调用顺序测试。
3. **完成可运行的发布证据**：修 gitleaks 历史获取和恢复命令，约束容量外推；补绑定提交/镜像的扫描与三平台桶下载恢复记录，再宣告 Phase 8 门禁通过。
