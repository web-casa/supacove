# 质量加固批次评审 1

日期：2026-10-07。评审 HEAD：`b39d4ec`；主范围：`git diff fa7a15e..HEAD`。

## 总体结论

**NEEDS_FIXES**。

发现 **2 个 P1、7 个 P2**。其中生成类型漂移会直接阻断 frontend CI 和依赖它的 image job；工件回收在删除失败时仍清空引用，破坏周期清理的重试能力。三项指定变异全部被现有测试击中，0008 修复后的逐级 Down 实测通过，但不能据此认定所有故障矩阵承诺已受保护。

本报告区分「本批新增」和「修复链遗留」：`settleCtxGone`、`ctxgone_test.go`、主要回收逻辑、计数器、stats SQL、0008 Down 修复已在基线 fa7a15e 中。本轮按指定重点复核它们，不把遗留缺陷归因于 f391c1e。

已阅读 `docs/reviews/overall-review-1.md`。工作树及 tracked files 中未找到 `docs/quality-plan` 或同名提案，故按用户提供的轻量工具链方案核对，无法验证提案原文的全部验收条款。采用 code-reviewer 技能。未修改工作区业务代码；补充探针与变异仅在 `/tmp/quality-review-copy` 的 HEAD 导出副本执行。

## 逐项判定表

| 项目 | 判定 | 依据与边界 |
|---|---|---|
| rename 失败清理 | 通过 | 锁定 disk 分类及无 `.inprogress`；未独立断言 Result=nil，但当前失败路径不会返回成功结果 |
| 丢失本地工件引用 | 通过，范围有限 | 确实恢复本地路径、committed 状态并检查注解幂等；注释中的「远端提交」并非此测试覆盖内容 |
| multipart 中断、响应丢失 | 不充分 | Q5：fake 只有完整对象 map，没有 upload ID/part/abort 模型；重试测试只证明同 key 可覆盖 |
| 并发入队 | 通过 | 16 并发得到 1 成功、15 ErrAlreadyQueued，并查询 active 行数 |
| 失败后的 retention anchor | 通过，范围有限 | 锁定一个 succeeded anchor 不被新 failed 任务挤掉；不覆盖所有 verify lease/keep_days 情形 |
| settleCtxGone 三路及 panic | 实现通过；覆盖部分 | 用户取消优先，其次 lifeCtx 停机，最后预算失败；panic defer 先调用同一分流器。三路测试通过，无专门 panic 交错与 /fail 心跳 spy |
| PruneExpiredArtifacts | 不通过 | Q2、Q3。先关游标再删文件正确，TTL/终态过滤基本正确；删除失败和无 manifest 状态未闭环 |
| ResumeRemotePhase | 实现基本合理；门禁不充分 | 每任务派生预算，context-free 状态修复保留 interrupted；Q6 的预先取消测试实际绕过上传。预算 cancel 用 defer 延至整轮结束，不是立刻释放；未发现本批新增状态回归 |
| rows.Err 新增处理 | 通过 | RecoverInterrupted 返回已修改数量 n 和 error；Reconcile 返回 nil,error；后台无返回值函数记录并退出，claim 返回 false；scheduler loadSchedules 丢弃不完整列表并记录。避免依据半份结果执行删除/入队 |
| countingReader | 通过 | n>0 且 err!=nil 时仍计字节并原样传播错误；计数只由加密消费者写，wg.Wait 后读取；错误路径不发布成功 Result。相关包 race 通过 |
| stats export 分母 | 通过（已披露口径） | `api_phase7.go:448` 分子和分母都要求 started_at 非 NULL；排除 pending/running。已开始后 canceled/interrupted 计失败，符合注释的保守口径；不是精确 dump-stage 尝试遥测 |
| 0008 Down 链 | 通过，实测 | Goose 在 fresh migrated SQLite 逐级 15→14→13→12→11→10→9→8→7→6→5→4→3→1；仓库无 v2。不是只测 0008 SQL，也不声称覆盖所有历史数据形态 |
| scheduler 日志 | 通过 | 冲突明确说明游标未推进；对应 enqueue 事务回滚语义。新增迭代错误有日志，未把半份日程当完整列表 |
| 独立恢复 | 架构通过；执行受限 | 不构造 Runner/Store/session，外部 identity + 密文 + script 输入正确；恢复目标为独立 DB，但在同一测试 PG 实例；输入来自本地最终工件，未走远端下载。真实恢复本轮 SKIP；篡改断言见 Q7 |
| age Close / waitErr / 401 三变异 | 通过，实测杀死 3/3 | 见验证记录，均为断言失败而非编译失败 |
| golangci/nolint | 配置能运行，排除需收窄 | v2.14.0 报 0 issues，nolintlint 无错位/无效注释；理由审核见 Q9 和下表 |
| CI 五 job、权限、并发、needs | 结构通过；整体不通过 | actionlint 通过，contents:read，PR cancel-in-progress，image needs 四门禁、Trivy failure 阻断均正确；Q1 导致前端 drift 必红 |
| CI 版本 | 部分钉定 | Go 工具显式版本；Actions 多为可移动 major tag，Trivy action 固定 release tag，未额外固定扫描数据库快照，不等于结果永久可复现 |
| oasdiff | 静态通过，未实跑 | baseline 与当前 OpenAPI 字节相同；`breaking --fail-on ERR baseline current` 方向正确；release 更新基线是人工约定。当前未安装 oasdiff |
| Prometheus 门禁 | 不充分 | Q8：测试读取/fixture/parser 有缺口，make 管道还会丢失 curl 错误 |
| Makefile | 不通过 | Q4：重复 lint recipe 覆盖新增门禁；api-breaking/metrics-check 未进 check，也未列入 .PHONY |
| Vitest / Playwright | 单元通过；E2E 未验收 | 28 单元/组件测试通过；CI 没有调用 Playwright 或安装其浏览器，不能把 5 个 e2e 用例算作 CI 已验收。当前沙箱不能启动 HTTP 服务 |
| 新 fake 字段及前端整理 | 未发现额外行为回归 | bool 零值关闭、Put 内使用原 mutex，目标 race 测试通过；session QueryClient 抽取后 main 仍使用同一生产入口。生成文件格式整理引入 Q1 |

## 新发现（P1–P2）

### Q1 · P1 · 提交的前端生成类型无法通过自己的 drift 门禁（本批新增）

位置：`frontend/src/api/schema.d.ts:7`、`frontend/package.json:14`、`.github/workflows/ci.yml:113`。

在 HEAD 独立副本运行 `npm run gen:api`，使用 package-lock 锁定的 openapi-typescript **7.13.0**，再与提交文件 cmp，退出 **1**：`differ: byte 135, line 7`。提交文件被改为二空格、多行 union；生成器仍输出四空格及单行 union。即使类型语义一致，CI 使用字节级 git diff，必然失败；image job 因 needs 同时被阻断。`make api-check` 也受影响。后端 oapi-codegen v2.5.0 再生成则完全一致。

建议提交生成器原始输出，或把明确、固定版本的格式化步骤纳入 gen:api 和所有生成入口；用一次再生成后无 diff 验收。

### Q2 · P1 · 删除失败仍清空引用，周期回收失去重试能力（修复链遗留）

位置：`backend/internal/jobs/upload.go:413`、`:417`。

os.Remove 报错后只记日志，随后仍清空 artifact_path、manifest_path、artifact_state，甚至输出 reclaimed。下一轮 SQL 只选 committed 且非空路径，因而不会重试。暂时权限故障恢复后，磁盘仍被占用，可再次卡住 staging 配额。启动 RecoverInterrupted 能重新发现有规范文件名的密文，但这不恢复周期清理承诺；若仅 manifest 删除失败、密文已删除，则该 manifest 也没有此恢复入口。

**实测**：seed 过期 failed 工件，将 staging 父目录 chmod 0500，再调用 sweep；输出 `artifact still exists=true path="" state=""`。补充断言 `unlink failed but retry reference was erased` 失败。

建议仅在各文件删除成功或已不存在时清引用；失败保留可重试状态，成功日志/计数只记录实际回收。加入删除失败→权限恢复→再次 sweep 的回归。

### Q3 · P2 · committed_no_manifest 工件永不进入 TTL 回收（修复链遗留）

位置：`backend/internal/jobs/upload.go:381`；生产来源：`backend/internal/jobs/jobs.go:911`。

manifest 写入失败时，生产代码保留完整密文并将状态设为 committed_no_manifest、任务设为 failed；sweep 却只选 committed。该终态不会被正常 resume 或 succeeded retention 回收，超出配置 TTL 仍永久占据 staging。现有 TestPruneExpiredArtifacts 全部 seed committed，无法发现此遗漏。短期保留以供手工恢复合理，但应在 TTL 契约中覆盖，或明确配置单独的保留策略并说明永久保留的容量代价。

### Q4 · P2 · 重复 lint recipe 让新增本地门禁完全失效（本批新增）

位置：`Makefile:67`、`:72`。

**实测 `make -n lint`** 输出 `overriding recipe for target 'lint'` / `ignoring old recipe for target 'lint'`，最后只执行 gofmt 和 go vet。新增 golangci-lint、eslint、stylelint 配方被整体覆盖。`check` 另跑 eslint，但仍不恢复被覆盖的 golangci/stylelint，也没有新增 Vitest/oasdiff/promtool 验收，故「Everything CI runs」已不准确。

建议合并 lint recipe，明确 check 的实际覆盖，并补齐新增目标的 .PHONY。

### Q5 · P2 · multipart 故障测试把待证明的“不残留”直接写进 fake（本批新增覆盖缺口）

位置：`backend/internal/jobs/phase3_test.go:54`；`backend/internal/jobs/faultmatrix_test.go:115`、`:143`。

failAnyPut 在读入任何字节、创建任何 multipart 状态前直接返回；检查 objects 为空只能证明「完整对象未出现」，不能证明上传中的 parts 已清理。losePutResponse 在存储后报错能模拟丢响应的基本情形，但当前一直报错至外部解除，测试随后直接调用 uploadAndCommitRemote，不经过真实启动 resume；没有检查恢复前 status，也没有 manifest 提交响应丢失的分支。`failPut["ANY"]` 不是通配符，实际由 failAnyPut 控制全部失败。

这不是纯理论区别：固定依赖 manager **v1.23.11** 的 `upload.go:884` 使用原 `u.ctx` 执行 AbortMultipartUpload，abort 错误被吞。**独立 SDK 探针实测**：UploadPart 中取消 context 后，abort 被调用，但收到的 ctx 已为 `context canceled`。不能承诺真实停机取消一定清理 parts。AWS 明确说明未完成 parts 需 complete/abort 才释放，并且版本桶每次 complete 产生新版本；对象 map 的两个 key 也不能证明版本桶没有新增版本。[AWS multipart 文档](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)

建议增加 storage 层可控 transport 或真实 S3 测试：至少成功一个 part 后中断、核对 upload ID/abort 的可用 context、列出未完成 uploads；丢响应分支检查内容/hash及真实 ResumeRemotePhase。将「同 key 逻辑重提」和「无遗留 parts/版本」分开陈述。当前 MinIO E2E 的 multipart 段只测成功上传，不能补上此故障证明。

### Q6 · P2 · 启动停机测试从未进入需要修复的恢复路径（修复链遗留覆盖缺口）

位置：`backend/internal/jobs/ctxgone_test.go:300`、`:305`；入口：`backend/internal/jobs/jobs.go:324`。

测试在调用之前取消 ctx；首个 QueryContext 即返回 canceled，函数直接退出。seed 本来就是 interrupted/uploading，后续状态、工件及零通知断言自然全绿，ctxRefusingBackend 没有被用到。

**变异实测**：在副本 ResumeRemotePhase 函数第一行直接 `return`，`TestResumePhaseShutdownRestoresInterruptedSilently` 仍 **PASS**。

建议让 backend Put 发出「已经进入上传」信号后再触发父 context 取消，强制断言调用次数、running→interrupted、无通知/失败心跳；另用等待 ctx.Done 的 backend 验证 resume 自身预算。

### Q7 · P2 · 篡改测试接受任意失败，无法锁定 SHA256 前置拒绝（本批新增）

位置：`backend/internal/jobs/standalone_restore_test.go:139`。

测试只要求 CombinedOutput 返回非 nil error；缺少 HASH/MISMATCH 时执行的是 Logf 而不是 Fatal。即使删除脚本 hash 门禁，后续 age 认证失败仍能让测试通过；启动 shell 失败等也同样通过。没有断言退出码，更没有证明未调用 decrypt/pg_restore。

建议检查 `*exec.ExitError.ExitCode()==1` 及明确的 `ciphertext SHA-256 mismatch` 输出；用命令 spy/标记文件证明 age 解密和 pg_restore 未执行。现有独立恢复正例的零退出码、表数输出和 count/max 数据检查则有效。

### Q8 · P2 · metrics 格式门禁没有覆盖它声称验证的转义输入（本批新增）

位置：`backend/internal/server/server_metrics_format_test.go:30`、`:44`、`:110`；`Makefile:50`。

fixture 只插入 database，没有 succeeded job；实际 database 名标签由 metrics.go 的成功任务 JOIN 才输出。**用同一 fixture 直接调用 handleMetrics（Recorder，无 TCP）实测：`fixture emits database label=false`**。因此特殊名称没有进入被检验文本。再以合法 `database="we\"ird"` 检查同一 labelPairRe，匹配后的值被 ContainsAny 引号判断错误拒绝；按逗号 Split 也不支持合法带逗号标签值。单次 Body.Read 忽略 error 又不能保证获得整个 scrape。

外部 `metrics-check` 使用普通 shell 的 `curl | promtool`，只传播最后一段退出状态：curl 的连接/401 失败可能被空输入解析成功掩盖。当前未安装 promtool，此项管道问题为静态判定，未声称实跑该工具。

建议使用真实 Prometheus parser/promtool，读取完整 body，seed 成功任务并断言特殊名称样本确实出现；抓取失败必须先退出，再验证非空文本。

### Q9 · P2 · 全局排除过宽，部分理由与真实命中不符（本批新增）

位置：`.golangci.yml:40`、`:59`、`:74`。

0700/0600 本身不是全局关闭所有文件权限规则的理由。移除 G301/G302/G306 排除后实跑：**21 条**；生产命中仅 db.go:73、dumper.go:346 的目录 chmod 0700，其余为测试中的宽权限/可执行脚本。可以局部解释两处目录误报并按测试范围排除，保留对未来生产 0644 密钥写入的检测。

G702 是命令注入污点分析，不是配置注释所说的文件路径规则；重新启用后本轮 **0 条 G702**，没有当前豁免必要。G124 的 4 处命中还包含生产 CSRF cookie 的刻意 HttpOnly=false，并非全是 SB_INSECURE_COOKIE。noctx 的说明声称每个 outbound client 都有 Timeout/netguard，也不适用于 storage/s3.go 的 SDK client；备份超时依靠上层 job context，不能把说明当作已经存在的客户端约束。

建议依据实际命中局部豁免，修正理由。未发现新增可利用的权限漏洞；此项是安全门禁覆盖失真，不把所有告警认定为漏洞。

## 排除项逐项核对

| 配置 | 结论 |
|---|---|
| errcheck Rows.Close | 手工消费后关闭并补 rows.Err，当前设计成立；close-only 错误仍被全局忽略，应限缩理由 |
| DB.Close、Store.Close | 主要退出/测试清理；生产 graceful shutdown 有显式错误处理。关闭全局检查仍无法保障未来调用 |
| Tx.Rollback | defer 清理，commit 单独检查，可接受 |
| os.File.Close | read/错误清理可接受；写成功路径 dumper/durable/secret 显式检查 Close。全局排除远宽于此语义 |
| io.ReadCloser.Close | 当前主要 S3/HTTP 读侧释放；读错误另行处理，可接受 |
| os.Remove、os.RemoveAll | 不总是已失败路径：config.go:269 在成功落盘后删除 temp，dumper 的失败清理亦可能遗留 quota；「每处都已有失败」理由不成立。建议显式 `_ =`/日志代替 blanket |
| fmt.Fprintf | metrics/HTTP 输出为主要调用，响应已开始后难补救；可接受有限范围，不应推广到持久文件写 |
| fmt.Sscanf | jobs.go:194、229 为正则筛选文件名取 ID，测试另有 fixture 解析；不属于输出写/cleanup，理由应单独说明，溢出/解析失败应显式处理 |
| G104 | 与 errcheck 重复，接受但继承 errcheck 豁免盲区；恢复后 113 条，不能声称这些都被另一工具检查 |
| G304 | 恢复后 32 条，包含 staging/恢复输入、CLI 路径及测试，不只是两个配置项；未据此发现新的外部路径穿越 |
| G703 | 恢复后 9 条，配置/数据目录及测试输出为主；应准确描述 operator-controlled 与下载 openUnderStaging 的不同边界 |
| G702 | 恢复后 0 条，命令注入规则；建议启用 |
| G124 | 恢复后 4 条；dev Secure 开关和生产 double-submit CSRF 的非 HttpOnly 需要分别说明 |
| G204 | 恢复后 27 条，pg_dump/pg_restore 等子进程确为产品功能；固定程序、argv、PGPASSFILE/context 是实际边界，可局部豁免 |
| G301/G302/G306 | 见 Q9；全局排除不必要 |
| generated api 路径 | generated:strict 已排除，额外 errcheck/errorlint 规则本次命中 0，冗余但不构成失败 |
| nolint 注释 | 所有当前注释经 nolintlint 通过；sqlclosecheck 手工关游标后写入理由成立，G115 有边界检查，G101/G401/G501 为配置名/测试 fixture，SA1019 明确说明 manager 弃用例外。没有检出错行或无解释注释 |

## 验证记录

Go 命令均使用 `GOCACHE=/tmp/final-cache`；golangci 后续使用 `GOLANGCI_LINT_CACHE=/tmp/quality-lint-cache`。前端使用 Node 22.22.2。

| 验证 | 结果 |
|---|---|
| `go build ./backend/...`、`go vet ./...` | PASS；初次 build 有只读 module stat cache warning，进程退出 0 |
| `go test -count=1 -timeout=15m ./backend/...` | **未全通过，环境阻断**：jobs/outbox/server 的 httptest 因 `socket: operation not permitted` panic；不可计为代码测试全绿 |
| jobs 故障矩阵和 ctxgone 定向 `-race -v -count=1` | 11 项 PASS，独立恢复 1 项 SKIP（Docker 不可访问） |
| agekey/dumper/db/recovery/verifier 全包 `-race -count=1` | 5 包 PASS（带环境前置条件的测试仍可能 skip） |
| golangci-lint v2.14.0 | PASS，0 issues；仅 generated exclusion 命中 0 的 warning |
| 权限/其他排除项审计配置 | 恢复权限规则得到 21 命中；其余六项得到 185 命中，分类数见上表 |
| npm lint / stylelint / test / npx tsc --noEmit | PASS；Vitest 4 文件、28 测试 |
| actionlint、git diff --check fa7a15e..HEAD | PASS |
| 后端类型再生成 + cmp | PASS，oapi-codegen v2.5.0 |
| 前端类型再生成 + cmp | **FAIL**，openapi-typescript 7.13.0，Q1 |
| api baseline 与当前规范 cmp | 相同；oasdiff 本机未安装，未实跑 breaking 命令 |
| Goose 逐级 Down | PASS；每次 Down 后读取 provider.GetDBVersion，最终确认 v1，详细链见判定表 |
| 指定变异：保留 aw.Close 调用但忽略其 error | **被杀死**：close_fail_test.go:53，`err=<nil> (writes reached 12 of 12)`；两段探测确实定位最终 flush |
| 指定变异：跳过 waitErr 非零分支 | **被杀死**：dumper_test.go:115，`dump that exits non-zero must fail` |
| 指定变异：保留 me=null，但不 removeQueries | **被杀死**：session.test.ts:29，overview 缓存仍有值，expected undefined |
| 额外变异：ResumeRemotePhase 立即 return | **存活**：TestResumePhaseShutdownRestoresInterruptedSilently PASS，Q6 |
| 额外复现：过期工件 unlink 权限错误 | FAIL（预期揭露缺陷），文件仍在而引用已空，Q2 |
| SDK multipart cancellation 探针 | 原 ctx canceled 被传入 abort，Q5；这是 SDK 层探针，不冒充真实 S3 结果 |
| metrics fixture/解析探针 | 无 database 标签；合法转义引号被自制 parser 拒绝，Q8 |

环境限制：Docker API socket permission denied；`sudo -n docker` 也受 no-new-privileges 限制。当前 sandbox 不允许提升权限或监听 TCP，因此真实 PostgreSQL/MinIO、完整 HTTP suite、Playwright、image smoke/Trivy 和 promtool 均未完成。未把 skip 视作通过，也未为此请求重复授权。

工作区保护：评审过程中观察到 `CHANGELOG.md` 被外部修改，未编辑、回退或纳入本轮结论；评审自身只新增本报告。所有探针和变异均保留在 /tmp，不进入提交。
