# Phase 8 评审 — 90f41ac

评审日期：2026-10-05。范围：`git diff f373926..90f41ac`；HEAD 确认为 `90f41ac449da54a553f17433f8dac225265ef57b`。对照 `docs/dev-plan.md:228` 的 Phase 8 与统一发布门禁，并沿用历轮“真实出口、权威状态、历史迁移、恢复证据”的评审口径。此报告替换同路径针对早期 `a5b0032` 的报告；不把早期已修复的问题当作本次新增缺陷。

## 总体结论

**P0：0；P1：5；P2：4。暂不通过 Phase 8 完成 / 公测发布门禁。**

源库大小链路、契约路由、失败任务 remediation 接线已经落地；`/api/stats` 确实受认证保护，生成代码没有漂移。但脱敏仍有可复现的残留/组合回归，四出口 canary 存在空断言，三体积及分段成功率尚未完整交付，容量结论超出实际测量范围。没有证据把这些问题升级为 P0，也不能由此宣称已完成全面安全审计。

## P1 发现

### P1-01：值级擦除不处理引号，并会破坏后续已知 secret 的完整匹配

证据：`backend/internal/pgclient/inspect.go:183`、`:199`、`:204`；实际组合调用方 `backend/internal/server/api_phase2.go:120`；对照 `backend/internal/jobs/jobs.go:643`、`:989`。

无监听 Go 探针直接调用提交中的函数，得到：

| 输入 | SanitizeMessage 输出 |
| --- | --- |
| `PASSWORD=one password=two` | `PASSWORD=[REDACTED] password=[REDACTED]` |
| `password='alpha beta' host=x` | `password=[REDACTED]'alpha beta' host=x` |
| `password=alpha beta` | `password=[REDACTED] beta` |
| `password=alpha,beta` | `password=[REDACTED],beta` |
| `password=[REDACTED]` | `password=[REDACTED]]` |
| `password = alpha` | 原样保留 |

遇到起始引号立即停止，意味着合法 quoted keyword conninfo 中的密码全文仍在。更直接的新增回归是先 `SanitizeMessage`、后 `redact.Secrets`：以完整已知密码 `alpha,beta` 调用 `redact.Secrets([]string{secret}, SanitizeMessage("password="+secret))`，仍输出 `password=[REDACTED],beta`。旧函数虽然仅换前缀，但还保留连续的完整密码，后面的 `Secrets` 可以擦掉；新函数先截去一部分，完整匹配不再成立。空格、右方括号、单引号同样复现。连接测试日志使用的正是这个顺序。

主备份路径先 `Secrets` 再 `SanitizeMessage`，完整已知密码在该路径通常已被消除；不能据此推断所有真实任务均泄漏，也不能忽略反向顺序的日志调用方。URI 前缀替换依然不擦除 userinfo，故不能把 SanitizeMessage 单独视为完整 URI scrubber。

建议：统一先移除已知 secret，再进行语法感知的兜底脱敏；支持引号/转义、等号两边空白，并保证 marker 幂等。增加两种调用顺序、多个字段、大小写、引号/空格/标点的回归测试。没有发现业务消费者需要依赖旧的泄漏格式；这里的兼容性风险是脱敏组合，而非展示格式。

### P1-02：四出口 canary 只证明部分 fail() 出口，工件和 webhook 可无条件漏测

证据：`backend/internal/jobs/phase8_canary_test.go:34`、`:90`、`:111`、`:122`、`:136`、`:165`、`:208`。

- 测试直接 `r.fail(...)` 注入拼接好的错误，没有执行连接、dump 或 `runJob`，注释所述“真实坏主机失败”和“fake verifier failure”均未发生。数据库中虽然存了 canary URI，但它没有被解密使用。
- exit 1 的任务落库/GetTask 检查，以及 exit 4 的真实 `backup failed` 日志 capture，有实际价值；不是全部测试都无效。GetTask 也不是 HTTP API 序列化出口。
- exit 2 查询 raw payload 列值得保留，但没有断言行数非零，`Scan`/`rows.Err` 未完整检查；只配置 heartbeat_url，没有创建 webhook 订阅、启动 outbox delivery worker。heartbeat `/fail` 不是 outbox JSON 投递。最后等 3 秒后即使收包为零，循环也直接通过；接收端单次 `Read` 还可能只读到部分 body。
- exit 3 自己写 `{"backupId":"canary"}`，未读取/断言这个 manifest 内容；kit 根本没有生成，`ReadFile` 的文件不存在错误被忽略。真实生成器就算泄漏，该测试也能绿灯。

建议：保留 fail 单测，但四出口门禁另走实际 backup/verifier 链；从已注册 webhook 驱动 outbox worker，要求至少一条预期 event、已送达状态和完整收包；要求真实 manifest/kit 存在且读取成功，逐一检查内容。故障路径不生成工件时，增加成功工件路径，而非用手写文件代替。

### P1-03：新增统计契约仍只有两体积和总成功率，旧 dump_size 错误也未修复

证据：`api/openapi.yaml:1109`、`:1131`；`backend/internal/server/api_phase7.go:392`；`frontend/src/App.tsx:219`；`backend/internal/jobs/jobs.go:935`；`backend/internal/dumper/dumper.go:543`。要求：`docs/dev-plan.md:231`。

StatsSummary/卡片只有源库物理大小与密文总量，没有 compressed archive 指标。`backup_stats.dump_size` 继续取 `result.SizeBytes`，和 artifact_size 完全相同；已有的正确来源是 `result.PlaintextArc`。注释写“三体积 / segmented success rate”不能补足缺失的字段。

successRate 仅为 succeeded/终态 job 总数，没有导出、远端提交、验证、通知的分段分母/结果。导出成功后上传失败会合并成一个失败任务，异步验证失败仍可计为 succeeded；因此不能用此百分比回答计划要求的各阶段可靠性。

建议：补 archive 持久化及公开字段，明确源库“每库最新已知”和 archive/ciphertext“累计历史”的不同口径；按权威阶段状态统计成功率，明确未配置/未执行/未知，不以 0 假装成功或有效测量。本项是历轮未关闭范围，不是声称这些底层问题全由本 diff 引入。

### P1-04：容量材料把可压 fixture 与客户端采样，写成不可压的产品端到端资源上限

证据：`scripts/capacity-benchmark.sh:29`、`:42`、`:50`、`:65`、`:78`、`:87`；`docs/capacity.md:13`、`:23`、`:27`、`:31`、`:39`。产品管道：`backend/internal/dumper/dumper.go:353`、`:409`。

1. `encode(gen_random_bytes(180),'hex')` 生成 360 字节、仅 16 种字符的文本，并非 zlib 最坏情况。本轮独立随机字节压缩探针：原始随机 bytes 的压缩比约 1.0003，hex 文本约 0.5697。405/873 的缩减不能全归因于页/行头开销。该 fixture 可保留为随机 hex 负载，但不能称“不可压”。
2. 每 0.2 秒读取 VmHWM，只能获得该进程存活且可读时的历史高水位，退出前最后一段峰值可能漏掉；initdb 的子进程、恢复用 postgres 服务端、应用 Go 进程均未计入。“所有阶段/端到端 RSS <12 MB”不成立。计时还包含轮询尾延迟，对 0.41/0.83 秒阶段影响明显。
3. 脚本顺序 dump 到明文文件再 age，加密后直接恢复原明文；没有密文 hash/decrypt/基线检查，没有上传/read-back，没有把 PG start/stop 纳入阶段计时。~24 秒只是所列阶段的近似和，不能标成产品“备份→可恢复验证”端到端结果。
4. 产品备份是 pg_dump→age 流式写密文，不会同时落盘一份 archive；文档“协议 A 暂存 = archive+密文”混入了 benchmark 的做法。验证另需明文、展开数据目录/WAL及历史保留量，报告也没有实际峰值磁盘测量。
5. 405/17≈24 MB/s 是 archive 吞吐，873/17≈51 MB/s 才是源库吞吐，表格却将前者标“对源库”。`du -m` 为分配空间的向上取整 MiB，并非逻辑字节数；不能与 pg_database_size 的原始 bytes 不加说明直接互换。由此推出“4h≈340GB 源库”依据不一致。

建议：保留原始测量值但修正标题/单位/测量边界；使用 bytea 随机 fixture 或明确 hex 压缩比。用进程退出资源统计与整个进程组/cgroup 峰值，补上传和真实验证链；区分源库、archive、密文以及验证临时盘。Apple Silicon 宿主机描述还需说明脚本实际运行的 Linux 环境：现脚本依赖 `/proc`、GNU date 和 `/usr/lib/postgresql/18/bin`，无法直接在 macOS 复现。

### P1-05：安全与真实恢复发布门禁仍无仓库内可判定证据

证据：`CHANGELOG.md:66`；`.github/workflows/ci.yml:8`；`docs/dev-plan.md:233`、`:238`；`docs/disaster-recovery.md:65`。

本次写入了 govulncheck=0 / gitleaks clean 的结论，升级工具链也确实生效，但没有提交可复核的扫描配置、版本、输出和目标产物标识；Trivy 门禁未交付，CI 无相应扫描步骤。四出口回归目前也不能作通过证据（P1-02）。季度演练建议不是三平台“备份→桶下载→新目标按恢复包手动恢复”的脱敏记录，CI 亦不能判定这些发布门禁。

这是 Phase 8 完成/发布条件未满足，不是推断当前存在某个漏洞，也不否定用户报告的本地扫描结果。建议提交绑定本 commit/镜像的扫描与三平台演练证据，并补能阻止不合格版本发布的门禁。CHANGELOG 的 Known limitations 同时仍写 Go 1.26.0 的 19 项问题（`:78`），需同步清理过时口径。

## P2 发现

### P2-01：平均耗时读取从未写入的 jobs.duration_secs，成功任务仍显示空值

证据：`backend/internal/server/api_phase7.go:402`；`backend/internal/jobs/jobs.go:861`、`:941`；`frontend/src/App.tsx:233`。

成功 UPDATE 不写 `jobs.duration_secs`；实际耗时只写 `backup_stats.duration_secs`。新接口 AVG 读取 jobs 的默认 0，卡片又将 0 渲染为 `—`。这次不会再因缺列报 SQL 错，但指标仍没有接通。

建议以一个权威来源写入/查询实际耗时；旧记录应为未知或明确的历史估算，不和新记录混算。增加真实成功任务到 StatsSummary 的检查。

### P2-02：MAX(id) 相关子查询按每条历史任务重复扫描同库历史，形成平方成本

证据：`backend/internal/server/api_phase7.go:432`；`backend/internal/db/migrations/0004_backup_kernel.sql:47`；`frontend/src/App.tsx:220`。

每个外层成功 job 都计算该库 MAX(id)。现有索引 `(database_id, created_at DESC)` 不能直接满足“满足大小条件的最大 id”。SQLite EXPLAIN 显示 `SCAN j → CORRELATED SCALAR SUBQUERY → SEARCH j2 USING INDEX idx_jobs_database_created(database_id=?)`。

可复现数据：Python SQLite 同结构同查询，单库 1,000/3,000/6,000 行耗时约 0.125/1.169/4.963 秒。另用实际生产迁移和项目 modernc 驱动、直接调用 GetStats 的无监听探针，3,000 行耗时 **3.43 秒**。前端每分钟刷新，历史增长后会持续占用 DB 连接/CPU。

值语义本身在当前单备份 worker、ID 顺序执行条件下正确：每库只取最大成功且 >0 的已知值，不会把多份备份重复求和；后续 unknown=0 不覆盖已知值。注意包含已软删除库的历史，API 应明确口径。

建议先 `GROUP BY database_id` 求最大合格 id 再按主键回连，或提供与条件/排序相符的索引并验证计划。补新值、旧值、unknown、失败、多库及大历史集测试。

### P2-03：主密钥灾难恢复步骤不完整，照做仍可能无法启动或上传

证据：`docs/disaster-recovery.md:22`、`:28`；`backend/internal/config/config.go:169`、`:184`；`backend/internal/jobs/jobs.go:669`；`backend/internal/jobs/destinations.go:146`。

文档把“丢失/损坏”统一写成能登录、调度照跑，实际格式损坏/权限不合规会在 LoadOrCreateSecret 阶段阻止启动。只有文件缺失后生成新密钥，或格式正确但内容改变，才进入旧凭据无法解密的状态。

即使重新注册数据库，目的地的 secret 同样由旧主密钥加密，仍会报 `destination secret unreadable — the master secret changed; re-create the destination`。仅重录连接串不等于“备份链立即恢复”，还需重建目的地凭据、重新绑定并验证远端提交。`stored credentials are unreadable` 是任务失败文案，不是登录错误。

建议按密钥缺失、格式损坏、有效但错误三种情况写步骤，优先恢复原密钥；必须重建时列出所有受影响凭据和配置。场景 3 命令也应统一目标连接（createdb、pg_restore、psql 当前不一致）、强制校验密文哈希、设受限 umask/清理明文并优先复用现有恢复套件，避免把未比较基线的表数查询当作恢复验证。

### P2-04：remediation 混淆两种 verification，且 Supabase pooler 建议过度排除

证据：`backend/internal/jobs/remediation.go:13`、`:17`、`:21`、`:23`；`backend/internal/dumper/dumper.go:357`、`:508`、`:583`；`backend/internal/server/api_phase2.go:305`；`backend/cmd/supabackup/main.go:82`。

- verification 文案说常见原因是“pg_dump warnings treated as errors”，但当前 dumper 没有把 warnings 升为 ClassVerify 的逻辑；非零退出按 classifyDumpFailure 分类，提交前失败还会删除 `.inprogress`，不能泛称 artifact retained。真正的异步 restore 验证写独立 verify_status，任务仍 succeeded，不进入仅失败任务的 remediation 分支，因此实际验证失败用户看不到这段建议。应将建议绑定真实 verify_status/verify_detail，分别描述导出失败和恢复验证失败。
- Supabase “not the pooler”把 session pooler 一并排除；官方允许 direct 或 session pooler 的迁移路径，IPv4-only 部署可能需要后者，应明确禁止/不支持的是哪种模式，不能一律换 direct。[Supabase 迁移说明](https://supabase.com/docs/guides/platform/migrating-to-supabase/postgres)。
- `supabackup version` 只打印应用版本，不会显示 pg_dump client 版本；客户端版本排查应使用实际选中二进制的 `--version`。storage 文案将 retention cleanup 也描述成失败任务原因，而正常保留清理失败仅日志告警。

Neon 冷启动作为网络超时的排查方向、磁盘配额环境变量、容器 UID 10001 均未发现本次明确错误；permission 文案是初步检查，不应当作完整 SELECT/sequence/RLS 权限配置保证。

## 重点核查中可认可的部分与边界

- **认证和契约**：`server.go:201` 将生成 API 挂在 `/api`，先经过 default-deny guard，匿名白名单没有 stats。无监听真实 Router 探针实测 GET `/api/stats` 返回 **401**。`make api-check` 通过。未发现 stats 被挂到 root/metrics 边界之外。
- **物理体积采集**：`inspect.go:124` 忽略单独 size 查询错误，且没有显式事务，普通权限拒绝不会让后续 role 查询进入 aborted transaction。PostgreSQL 对该函数要求目标库 CONNECT 或 pg_read_all_stats，并非必须 superuser；不能把所有 Supabase/Neon 受限角色都推断为必失败。[PostgreSQL 官方文档](https://www.postgresql.org/docs/current/functions-admin.html#FUNCTIONS-ADMIN-DBSIZE)。本轮没有真实托管库权限测试；超时/连接断开仍可能令后续查询失败，不等于“所有错误都不影响备份”。当前 0=unknown 有代码/契约约定；多库合计缺少已测量覆盖率，展示需防误解。
- **Go 版本**：本轮测试实际使用已缓存 Go **1.26.6**；CI 的 `go-version-file: go.mod` 会跟随最低版本。Dockerfile `ARG GO_VERSION=1.26` 是浮动 minor tag，不意味着固定在 1.26.0；若缓存基础镜像较旧，默认 GOTOOLCHAIN=auto 可下载满足 go 指令的工具链，否则构建失败，而非悄悄使用旧版本。[Go 工具链说明](https://go.dev/doc/toolchain)。未实建镜像，不宣称验证其 digest/扫描状态；锁定构建版本和记录 build info 可提高可复现性，当前未单列为阻断缺陷。
- **迁移回退测试**：改为 Down 到 `<14` 与新增 0015 一致，db 全包测试及相关 race 测试通过，未发现无限循环/回错目标。
- **灾难文档场景 1 SQL**：`settings(key,value)` 与 0004 schema 和 age init 使用的列、键名一致，不能沿用“schema 不匹配”的疑虑。recipient 是公钥，manifest 中可取得。场景 1 的“见场景 4”应为场景 3。
- **pre-migrate 保留**：`db/backup.go:15`、`:55` 实际为最多 5 个有版本升级批次，每源版本保留最新一份，不是简单最近 5 次尝试；无法分类的 legacy 文件另有保留规则。“最近数份”虽不构成份数冲突，最好写出目录与批次规则。回滚说明还应提醒隔离旧 DB/WAL/SHM，避免故障现场混用。
- **RPO / 负载**：单 worker 及 per-DB cron 与描述一致，但公式只能是健康、稳定负载下的近似，不是故障条件下的上限。运行中再到期会去重并推进游标（`scheduler.go:184`），不能保证每次 cron 都对应一份备份；还需计入轮询抖动、失败/重试与恢复时间。稳定条件应按各库周期写 `Σ(service_time_i / period_i) < 1` 并留余量，同一窗口则比较所有库完整服务时间总和；不能仅以 dump 速率或“单库最慢×队列深度”作为异周期库的总负载条件。异步 verifier 不占备份 worker，但共享 CPU/盘，仍会影响吞吐。
- **脱敏复杂度**：每个 password= 都重新 lowercase 剩余字符串并重建全文，最坏约 O(k·n)，不是单次线性扫描。通常短错误消息成本有限，本次没有将其夸大为独立 DoS；仍建议一次扫描构建输出并限制错误长度。
- **其他统计边界**：`api_phase7.go:417` 起多个查询忽略错误，异常时会返回 HTTP 200 + 零值；应统一返回错误或明确 unknown，不能把 DB/ctx 错误伪装为 0。本轮未把每个可靠性欠缺拆成单独计数。

## 验证记录

所有 Go 命令使用 `GOCACHE=/tmp/codex-phase8-cache`。临时探针已删除，仅保留此报告。

| 验证 | 结果 |
| --- | --- |
| `make api-check` | 通过；后端/前端生成代码无漂移 |
| `go test` pgclient、redact、db、stats | 前三包通过；stats 无测试文件 |
| 同批 jobs / server 全包测试 | 受沙箱监听限制中止：httptest `socket: operation not permitted`，不记作业务回归或通过 |
| 无监听 SanitizeMessage / Secrets 探针 | 复现 P1-01 表内结果和组合残片 |
| 无监听真实 Router / GetStats 探针 | 未认证 401；真实迁移 + 3,000 行查询约 3.43s，源库合计正确 |
| `go test -race` 选定升级/词汇 Down-Up/重复名称/验证脱敏/并发入队/启动恢复/未配置 verifier 测试 | db、jobs 通过；同命令 pgclient/redact 的过滤模式无匹配，不算该两包 race 全覆盖 |
| `go vet ./backend/...` | 通过 |
| 前端 `npm run build` | tsc + Vite 通过 |
| `bash -n scripts/capacity-benchmark.sh` | 通过；不代表性能数据可复现 |
| capacity / Docker / 三平台 / 安全扫描 | 按环境约束未重跑；不为提交中的实测值与扫描结论背书 |

WaitGroup.Go 等机械替换未发现新的明确 race；有限 race 结果不能证明完整异步链无竞态。新 canary 本身需要监听，本轮只静态评估其路径与断言，未将它记作运行通过。

## 最关键的 3 件事

1. **修复脱敏组合并重做真实四出口 canary**：完整 secret、部分残片、空出口都必须能令门禁失败。
2. **接通真实统计**：archive 与 ciphertext 分离、分段成功率、有效耗时，并消除每分钟刷新的平方查询成本。
3. **收紧发布证据**：更正容量/灾难文档，补真实产品链测量、三平台恢复与扫描结果；证据齐备之前不要将 Phase 8 或公测门禁标为通过。
