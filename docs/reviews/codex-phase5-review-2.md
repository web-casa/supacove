# Phase 5 + 6 修复复审（第二轮）

评审日期：2026-10-05。指定比较范围：`git diff 2dcbbc8..7986963`；工作树 HEAD：`79869637e64cbe30ac25e21e895edf8da6806209`。上一轮报告：`docs/reviews/codex-phase5-review-1.md`（基线 `2dcbbc8`、被评审版本 `283a3d5`，P1 10 项 / P2 3 项）。为判别“修复引入”和“此前已有”，另核对 `283a3d5..7986963`，不把整个基线 diff 都当作新修复。

**结论：仍不通过 Phase 5/6 完成验收。13 个旧编号中，FIXED 2 项、PARTIALLY_FIXED 10 项、NOT_FIXED 0 项、REGRESSED 1 项。** 脚本执行与生产接线已实质修复，但停止 PG 的所有失败路径出现回归，保留策略、恢复证明及持久状态收敛仍不完整。另确认新的零表计数丢失问题，以及新 kit 下载端点的符号链接边界缺口。

## 验证方法与限制

所有代码行号指向 `7986963`。未修改产品实现。临时审计测试执行后删除，只保留本报告；探针源码和日志保存在本机 `/tmp/codex-phase5r2-probes/`、`/tmp/codex-phase5r2-probes.log`，便于本次会话复查。报告中的 PASS 探针有意断言缺陷存在，不能理解为产品门禁通过。

| 检查 | 结果 |
| --- | --- |
| `GOCACHE=/tmp/codex-phase5r2-cache go build ./backend/...` | 退出 0；有一条 Go VCS 模块元数据缓存写入只读目录的警告。另以 `go build -buildvcs=false ./backend/...` 验证，退出 0、无诊断。 |
| 同缓存下 `go vet ./backend/...` | 退出 0，无诊断。 |
| 同缓存下 `go test -race ./backend/...` | jobs、recovery、verifier、platform、config、db 等包通过，无 race 报告；server 在 `TestPhase2DefaultDenyAndAgeStatus` 因 `httptest` 监听 `[::1]:0` 被禁止而 panic。全套不能记为通过。 |
| 同缓存下 `go test -race -v ./backend/internal/jobs -run '^TestM1_'` | 两个 M1 用例因 PostgreSQL 容器不可用而 SKIP；不是恢复闭环通过。 |
| 临时 `go test -race -v`，对 verifier/recovery/jobs/db/server 运行 `-run '^TestReview'` | 8 个顶层测试、12 个叶子场景全部执行，退出 0，无 race 报告。server 探针直接调用 handler，不监听端口。 |
| 真实生成的恢复脚本 + shell 工具 stub | 原有 `TestHappyPath` 等用例通过；追加空格 conninfo 密码探针，确认密码进入客户端 argv。 |
| 真实 age 加解密 + PG 工具 stub | 复现 restore 失败、unsupported 不调用 stop；stop 两次失败仍 verified 并删除工作目录。没有启动真实 PG。 |
| SQLite / 文件 / 假存储后端探针 | 复现本地 verified/pending 被 prune、远端 running lease 被删除、缺 manifest/零表被降级、最终状态写失败停留 running。 |
| 历史迁移升级探针 | 使用基线原始 0004/0008 SQL，其余旧 SQL 迁移到 v9，再换当前迁移升至 v12；迁移成功，但 `SELECT duration_secs FROM jobs` 报 `no such column`。`verify_profile` 正常存在。 |
| 新下载 handler 探针 | staging 内 `restore.sh` 指向目录外 canary 的 symlink，返回 200 并读到目录外内容。 |

日志分别在 `/tmp/codex-phase5r2-{tests,build,build-novcs,vet,m1,probes}.log`。环境禁止监听端口且 Docker 不可用，不能验证正式镜像内恢复、恶意归档演练、三平台新目标手动恢复或远端下载到精确数据断言的闭环。先前 ADR 中的 spike 证据不能替代修复版本的集成证据。

## 旧编号逐项判定

| 旧编号 | 判定 | 主要依据 / 未关闭内容 |
| --- | --- | --- |
| P1-01 | **FIXED** | 所有客户端命令改为单行；生成脚本实际执行通过，不再传入字面量反斜杠。 |
| P1-02 | **PARTIALLY_FIXED** | 私有临时目录、umask、trap 已实现；合法的 `password = '…'` conninfo 绕过密码拒绝并进入 argv。 |
| P1-03 | **PARTIALLY_FIXED** | 哈希与表计数门槛、psql 错误传播已修复；只以用户表判空，缺工具版本和 profile 依赖预检。 |
| P1-04 | **FIXED** | 正式入口接线、显式配置门槛、启停日志与 runtime PG18 清单已落实；镜像实际运行仍待外部环境验证。 |
| P1-05 | **PARTIALLY_FIXED** | 本地密文哈希 → age 解密 → 恢复已实现；未对远端读回的字节执行解密恢复，缺完整远端恢复证明。 |
| P1-06 | **REGRESSED** | 单 worker/有界队列/Runner join 改善；旧版的所有出口 deferred stop 被删除，失败分支不停止 PG。预算和启动收敛也未完整关闭。 |
| P1-07 | **PARTIALLY_FIXED** | minimalEnv、全局默认关闭、部署告知已实现；无每来源可信门槛、已测试扩展白名单与 UI 告知。 |
| P1-08 | **PARTIALLY_FIXED** | 增加表数/扩展名检查和 unsupported；未核对对象集合、角色/版本/恢复选项；缺 manifest 仍可 verified，profile 证据不完整。 |
| P1-09 | **PARTIALLY_FIXED** | kit 持久引用、下载、回填和远端 last-verified 保护已加入；本地验证锚点、排队及远端 lease 保护、桶内独立交付仍缺失。 |
| P1-10 | **PARTIALLY_FIXED** | Auth/Storage 范围说明已纠正，撤回通用脚本可直接迁移 Supabase 项目的建议；无平台 profile，仍忽略用户确认平台。 |
| P2-01 | **PARTIALLY_FIXED** | 标签边界、大小写/尾点、Neon -pooler、Railway 私网已修复，注册响应增加 warning；前端未显示，连接测试/编辑流程未接入。 |
| P2-02 | **PARTIALLY_FIXED** | Task/API/schema 和 manifest 快照说明已补齐；重启成功路径仍空状态，写失败不重试，恢复 profile/零值和重启脱敏仍有缺口。 |
| P2-03 | **PARTIALLY_FIXED** | 新增实际脚本、age、环境、队列/Stop、保留和配置测试；没有真实恢复闭环，若干测试断言不足，未覆盖已复现回归。 |

### P1-01：续行语义修复

证据：`backend/internal/recovery/kit.go:146,160,164,171` 均为单行命令。`backend/internal/recovery/kit_test.go:243–262` 使用真实生成器与可执行 shell stub，检查 `--dbname`、`--exit-on-error`、`--no-owner` 和明文路径，实际通过。这一缺陷可独立关闭；未因为缺真实数据库演练重新打开续行问题。

### P1-02：临时资源修复，密码门槛可绕过

证据：`kit.go:83,136–143` 设置 `umask 077`，使用 `mktemp -d`、0700 目录、仅删除本次目录的 EXIT trap，并将 INT/TERM/HUP 转为非零退出。正常执行及 pg_restore 失败 cleanup 测试通过。固定工作目录明文文件与删除既有文件的问题已经移除。

剩余 P1：`kit.go:97–103` 的 `*password=*` 只识别无空格形式，不能识别 libpq 合法的 `host=db user=backup password = 'review_canary' dbname=target`。追加探针用原 `newHarness` 执行真实脚本，退出 0，argv 日志中含 canary；`kit.go:146,164,171` 会原样传给客户端。URI 中编码后的参数名也需要由解析器处理，不能靠扩大 shell glob 保证“密码只走 PGPASSWORD”。[PostgreSQL libpq conninfo 文档](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-CONNSTRING) 明确允许关键字、等号和值之间的空白。

建议：限制接受的连接参数格式，使用能理解 libpq 语法的解析/校验并拒绝所有密码字段；补带空格、引号、URI query 编码等 argv 用例。现有 `kit_test.go:297–302` 只覆盖最简单的两种密码写法。

### P1-03：哈希、psql 传播修复，自检仍不完整

证据：`kit.go:109–134` 检查工具、密文存在和 SHA-256，先于解密；不再仅在注释中显示哈希。`kit.go:146–156` 默认拒绝存在用户表的目标，有显式风险覆盖选项。`kit.go:164–173` 返回 pg_restore / 最终 psql 的失败，使用 `-X`、`ON_ERROR_STOP=1` 并提示部分写入；`kit.go:183–198` 检查最终计数是否为数字及是否匹配。

未关闭：只查询 `pg_tables`，仅有视图、函数、序列等对象的非空数据库仍会通过。工具检查是 `command -v`，不检查客户端/目标版本、扩展版本、角色和 TOC 依赖；平台预置对象也只有风险 override，没有已测试的允许集合。当前改动降低了常见误恢复风险，不能称为完整“非空数据库拒绝 + profile 自检”。

### P1-04：生产接线及默认关闭修复

证据：`backend/cmd/supabackup/main.go:238–265` 在显式启用时读取 identity、定位 PG、构造 verifier、回收残留、调用 `SetVerifier` / `SetVerifyIdentity` 并记录 ENABLED；安装缺失返回启动错误，关闭时记录 disabled。`backend/internal/config/config.go:95–97,122–138` 默认关闭，启用时要求 identity 文件，拒绝 symlink、非普通文件和组/其他用户权限。`Dockerfile:51–53,70` 在正式 runtime 安装 PG18 server，仍使用 UID 10001。

`config_test.go:98–169` 覆盖默认关闭、缺私钥文件、弱权限、symlink 和有效配置，已通过。文件内容尚不在启动时校验，0600 空/无效 identity 能启动后逐个失败；作为配置健壮性待补，不否定已完成的接线。目录权限与内容校验也不能把同 UID verifier 变成沙箱。

### P1-05：正确解密本地密文，未形成远端恢复链

证据：`backend/internal/jobs/verify.go:152–189` 将 DB 中的密文路径、密文 SHA、identity 和 manifest 预期值传给 verifier。`verifier.go:181–195,231–234,357–377` 先哈希，再使用 age 库解密到独占创建的 0600 文件，pg_restore 使用明文路径。`verifier_test.go:79–145` 实测真实 age 往返、哈希不符、失败删除明文和权限。

未关闭：`backend/internal/jobs/upload.go:102,129–149` 的远端 readback 只哈希并丢弃字节；`verify.go:152–160` 只读本地文件，缺本地 staging 就 skipped，不下载远端。可以说“远端哈希相符 + 本地恢复检查”提供组合证据，不能说已完成“远端读取 → 解密 → profile 恢复”门禁。测试也未证明 bucket-only 工件能独立完成这条链。

### P1-06：生命周期部分改善，但停止路径回归（阻断）

正向证据：`jobs/verify.go:28,59–71` 单 worker、容量 4；`jobs.go:405–408,435–447` worker 加入 Runner WaitGroup、Stop 取消并 join。`verify.go:180–198` timeout 及关机回到 pending；`verifier.go:165–169,385–395,469–515` 部分目录创建回收、环境白名单、64 KiB 有界输出；`verify_lifecycle_test.go:210–255,261–281` 覆盖 join 和满队列。

**回归 R2-P1-01**：旧 `283a3d5` 的 Verify 在全部出口 deferred 调用 stopPG；修复版 `verifier.go:174–175` 只 deferred 删除文件/工作目录，唯一 `stopConfirmed` 调用在 `:262`。启动失败但 PG 已部分启动、扩展检查失败/unsupported、pg_restore 失败、计数不符等（`:213–255`）均直接返回，不停止 PG。三个 stub 场景实测：

- restore_failure：`failed`，stop 调用 **0**，工作目录已删除。
- 缺扩展：`unsupported`，stop 调用 **0**，工作目录已删除。
- 所有 SQL 检查成功但两个 stop 都失败：`verified`，stop 调用 **2**，带 still-running warning，工作目录仍删除。

stub 不启动进程，因此没有声称现场存在遗留真实 PG；它直接证明生产控制流没有发停止命令。启动后失败会失去 PID/data 证据并可能留下 PG，破坏“单验证实例”、Stop 收敛及资源治理。`stopConfirmed:315–329` 只有升级停止使用独立取消边界，失败只拼接 warning；必须所有出口使用独立有界 cleanup context，停止确认失败保留可回收证据并报告清理失败，禁止继续删除仍在使用的数据目录。

其余未关闭：`CleanupResidual:87–104` 只 RemoveAll，未检查/停止存活进程；`main.go:257–259` 回收失败仅 warning 后继续。`checkDiskBudget:282–296` 只按 **2×压缩密文 + 256 MiB** 检查一次，没有按展开后的数据、索引/WAL 预算或执行期上限。高压缩大库仍可耗尽应用数据卷；backup 与 verifier 并行，无预留/备份优先。`hashFile:338–346`、`decryptToFile:357–377` 的文件复制不观察 context，不能保证完整 wall-time/Stop 上限。

启动重排队 goroutine 在 `jobs.go:407` 未加入 wg；`ResumePendingVerifications:122–124` 遍历无取消检查，enqueue/finish 使用无 context Exec。故 Stop 等待 worker 并不等于等待所有恢复入队/状态写操作。

### P1-07：白名单生效，可信来源及扩展 profile 仍缺

`verifier.go:385–395,505–508` 子进程只获得 HOME/locale/TZ/PATH；不再 append `os.Environ`。`verifier_test.go:54–76` canary 覆盖 PGPASSWORD、PGOPTIONS、AWS secret、LD_PRELOAD，测试通过。`config.go:122–138` 和 `docs/deployment.md:52–60` 明确显式启用、same UID、非沙箱和私钥不再离线。这些是真正修复。

剩余：启用后的所有成功备份都进入验证（`jobs.go:837–843,904–905`），没有每来源可信属性/过滤，也没有 UI 中的信任告知；全局部署文档要求“只信任来源”不能在混合来源实例中落实 ADR。扩展只有“manifest 名称是否在本机 available”检查（`verifier.go:217–228,417–434`），不是已测试扩展/版本白名单；没有 TOC 检查，缺 manifest 时连名称检查都绕过。未把同 UID 本身重新列为漏洞，也未把白名单宣传为恶意 dump 隔离。

### P1-08：表数/扩展检查不是对象集合证明

证据：`verifier.go:244–256` 比较表数量和扩展是否存在，支持 `unsupported`。但 Input（`:111–125`）没有预期 schema/对象名、角色、源/目标支持版本和恢复选项；相同数量但错误对象、缺视图/函数/序列等无法识别。扩展只比较名称，忽略 manifest 版本；ACL 所需角色未初始化，仍被笼统归为 failed。`--no-owner` 不等于忽略 ACL 角色依赖。

`jobs/verify.go:164–178` 读不到/解析不了 manifest 仍继续，ExpectedTables=-1、扩展为空，可被 verified。临时探针移除 manifest，fake engine 收到 -1 并返回 verified，持久结果仍为 verified；真实 verifier 在 `verifier.go:244,249` 也会跳过预期检查。缺旧字段可明确降级，当前应有而缺失的 manifest 不能静默降低门禁。

新增 **R2-P1-02**：`manifest/manifest.go:81` 的 `tableCount,omitempty` 将新备份的真实 **0** 表序列化为缺失；`HasTableCount:123–132` 因此返回 false，`verify.go:165–170` 变成 -1。探针将新 manifest 写为零表，worker 输入实测 -1，fake engine 返回一张表仍被持久化 verified。新字段应显式表示零与未知，不能通过 omitempty 丢失零值。这也令首次生成 kit（传 deps.TableCount=0）与启动回填 kit（读缺失变 -1）采用不同门槛。

`finishVerification:234–238` 把 profile 固定写成 `embedded-local`；虽然 `Result.ServerVersion`（`verifier.go:268`）和 `profileName` 已生成，却未用于结构化持久值；恢复选项也没有记录。成功 Detail 宣称 expected-object set matched（`:269`），即使预期未知也会出现该措辞。仍不能给出 ADR 所要求的“在 profile X 上按选项 Y 恢复成功”的完整证据。

此外 `pgclient/inspect.go:108–112` 在 dump 开始前的独立连接读取表数（`jobs.go:713,726`），并非 pg_dump 同一快照；正常 DDL 可造成 false failure。不能将这个计数无条件称为归档对象基线，应从 TOC 或同一导出快照构建预期。

### P1-09：kit 交付改善，验证锚点和 lease 保护仍不完整

`jobs.go:848–852,886–898` 在成功路径写 platform/verify 状态并生成、持久化 kit 引用。`jobs/kit.go:22–91` 启动回填，`main.go:277–279` 在启动 worker 前执行。`api/openapi.yaml:642–669`、生成 handler 和 `server/api_phase3.go:263–300` 提供下载；改为 `sh restore.sh` 与 0600 文件匹配。`upload.go:256–265` 同时保护最新 committed 与最新 verified，单线程无状态变化场景的保留测试通过。

仍有 P1 保留缺口，已实测：

1. `pruneLocalArtifacts`（`upload.go:353–399`）不查询 verify_status，**本地-only 最后一个 verified** 在较新 pending 出现、keep=1 时被删除。即使存在 remote，最新 verified 的本地恢复证明也不参与本地锚点规则。
2. lease 只在 `runVerification:143–150` 执行期间持有；排队 pending 无保护。探针旧 pending + 新 pending、keep=1 后，旧路径清空，尚未执行就失去验证输入。
3. `runRemoteRetention:263–272` / `deleteRemoteBackup:284–317` **完全不检查 verifyBusy**，实测 running + `verifyInFlight=true` 的旧 job 仍变 deleted，并删除本地 ciphertext/manifest。恢复读本地期间同样会被破坏。
4. 本地 prune 的 `verifyBusy` 检查（`:384`）与删除（`:387–399`）分离，没有原子获取删除权。验证可能在检查后开始；这是逻辑 TOCTOU，不会由 Go race detector 自动检出。

kit 仍没有上传到桶。只保留桶内密文/manifest、丢失实例时不能取到本工具所称 kit；local prune 不删除脚本是有意设计（`upload.go:345–350`），但 local-only 密文被删除后会留下不可恢复工件和可下载脚本。回填仅能读本地 manifest（`kit.go:56–59`），已被 prune 的远端成功任务无法重建丢失 kit。新下载路径另见 R2-P2-01。

### P1-10：Supabase 范围说明纠正，平台支持仍未交付

`platform.go:107–121` 已说明 full pg_dump 涵盖 auth/storage 的数据库记录，排除 Storage 文件、Edge Function code 和平台配置；明确 generic 脚本不做 Supabase 项目迁移。这与当前全库导出方向一致，纠正了上轮“Auth users 独立于数据库”的错误。[Supabase Storage schema 文档](https://supabase.com/docs/guides/storage/schema/design) 也区分数据库元数据和实际对象存储文件。

但文本“ALL schemas/includes auth/storage”仍未基于实际 TOC/权限逐项声明；未实现 Supabase profile 或新项目演练。`platform.go:118` 建议 plain PostgreSQL 只要求 major >= source，与 manifest 的 >= dumping-client-major（`jobs.go:765–766`）不完全一致，且缺角色/扩展说明。

`jobs.go:665,889` 始终按 host 检测生成指南，未复用已保存的用户平台（`:758` 的 manifest 仍用数据库平台字段）。自定义域名下用户确认 Supabase，kit 可得到 Generic，仍与上轮要求冲突。不能将说明文案改善视为三平台恢复 profile 通过。

### P2-01：识别修复，向导链只接了一部分

`platform.go:19–48` 按 DNS 标签后缀、大小写和尾点规范化识别；`:77–95` 识别 Neon -pooler，`:44` 支持 railway.internal。`platform_test.go:8–64` 含伪后缀、FQDN、大小写、5432 pooled 和 direct negative，测试通过。旧误分类案例均关闭。

`server/api_phase2.go:151–157` 把 poolingWarning 写入创建响应，`jobs.go:666–667` 也记录提示。但前端除生成 schema 外未引用 poolingWarning，且不是独立连接测试/编辑流程的统一诊断。没有用户平台覆盖测试、Neon 冷启动独立超时或 Railway 当前部署可达性诊断；故按旧编号完整范围判为部分修复，而不是否认域名匹配修复。

### P2-02：API 与快照语义修复，权威状态仍有洞

`jobs/queries.go:56–60,87–92` SELECT 与 Scan **23 列一一对齐**；Get/List/LastTask 使用同一 helper。`jobs.go:845–859` success UPDATE 两次 Exec 都是 **8 个占位符 / 8 个参数**，取消条件与 RowsAffected 保留，无错位。`server/api_phase2.go:318–345`、`api/openapi.yaml:1050–1077`、`api/api.gen.go:279–309`、`frontend/src/api/schema.d.ts:582–607` 已暴露状态、详情、耗时/profile 与 kit 可用性，服务端二进制可构建。

`jobs.go:777–783` 明确 manifest 是备份时刻快照，job API 是实时权威状态，不需要异步改写已远端提交的 manifest。`jobs.go:908–918` 对 stats Entry 也标为记录时刻快照；但 aggregate stats 不提供当前验证等级，不能说统计恢复保证已闭环。

未关闭：

- `jobs.go:372–376` 的 **ResumeRemotePhase 成功 UPDATE** 仍只写 succeeded/manifest_path，不写 verify_status/platform；其后 Start 只恢复 pending/running（`verify.go:104–107`），新中断任务会成功但验证状态为空，也不会排队。禁用 verifier 后启动时 `ResumePendingVerifications:101–103` 直接返回，历史 pending/running 可永久保持过渡状态。
- `finishVerification:227–240` SQL 失败只日志，无重试/重入队。SQLite trigger 注入最终 verified 写失败后，实测保持 running，队列为空；只能寄希望于重启。状态转移 UPDATE（`:85–89,137–141`）也不检查 RowsAffected，重复入队可再执行已终结任务。新备份入队与启动扫描并行（`jobs.go:405–407`），需要原子 claim/dedup。
- 重启使用 `pgclientSanitize`（`verify.go:123,130`）代替已捕获的 knownSecrets；`pgclient/inspect.go:159–166` 只是把 `password=` 改为 `password=[REDACTED]`、URI 前缀改名，**实际秘密正文仍保留**。原始任务链的 knownSecrets 脱敏确有改善（`verify_lifecycle_test.go:405–440`），但重启链不能同等声称 credential-free。
- `taskToAPI:332–334` 在 verified 零表时省略 verifyTables；`HasRecoveryKit` 仅据路径非空（`queries.go:73`），文件丢失仍宣称 available。前者已直接 handler/mapping 探针确认。profile 版本/选项未持久化见 P1-08。

### P2-03：测试显著增加，覆盖与有效性不足

新增测试有价值，且相关包 race 测试通过：platform 伪后缀/大小写/尾点；recovery 的真实 shell 执行和参数；verifier 的真实 age、0600、hash gate 和 env canary；jobs fake engine 的 Stop join/overflow、回填和顺序保留；config 的启用门槛。

但不能作为 Phase 5/6 DoD：

- `verifier_test.go:147–181` 的 CleanupResidual 只造空目录，无 live PID；Verify 只测空 identity / bad hash（`:199–237`），不会到达 PG start 之后。没有覆盖回归的失败 stop、停止失败、timeout cleanup、disk budget 或真实 SQL 恢复。
- `verify_lifecycle_test.go:299–332` 人工先放入 inFlight map，再顺序调用本地 prune，未测试 queued、获取 lease/删除竞争或远端 retention；`:338–367` 只覆盖静止远端 last-verified 锚点，不覆盖本地-only。
- kit stub 的日志只记录 `$*`，没有工具名/每个参数边界（`kit_test.go:147–165`）；`:269,286` 用 contains `pg_restore` 来证明未执行，实际执行的 stub 日志也没有该名称，断言不足。`TestMissingShaTool:340–342` 只是去掉 stub，但 harness PATH 仍含 `/usr/bin:/bin`（`:192`），会使用真实哈希工具，再因 hash mismatch 失败，不能证明缺工具分支。
- `TestPsqlFailureNotSwallowed:333–335` 所有 psql 都失败，实际只测试第一个自检失败，没有测试 restore 成功后最终 psql 失败。信号 trap 仅字符串检查，未执行信号 cleanup；脚本错 identity、明文权限、并发互不干扰、工具旧版本也缺真实执行断言。harness 的 tableCount=0 默认映射为 -1（`:121–125`），恰好漏掉零表。
- 没有正式 runtime 的远端 fixture → age 解密 → 恢复 → 对象集合/精确行值 → 确认销毁，也无三平台新目标手动演练。无 Docker 的本轮也没有补得出这些证据。

## 新问题与回归汇总

### R2-P1-01：失败路径停止 PG 的 deferred cleanup 被移除

分类：**修复引入的 P1 回归**，对应旧 P1-06。位置：`verifier.go:174–175,213–255,262,272–275`。复现与修复要求见 P1-06。不能只在成功路径停止，也不能把 still-running 当作普通 verified warning 后删目录。

### R2-P1-02：新 manifest 零表被序列化为未知，丢失恢复门槛

分类：**修复引入的 P1 正确性问题**，对应 P1-08 / P2-02。位置：`manifest.go:81,123–132`、`verify.go:165–170`、`kit.go:66–69`。新字段必须保留已知零值并明确兼容旧 manifest；缺声明不能输出 expected-object set matched。

### R2-P2-01：新 recovery-kit 下载路径只做词法 containment，symlink 可越界

分类：**新端点引入的 P2 边界问题**；旧 artifact 下载已使用相同弱规则（`api_phase3.go:232–244`），不是首次发现的任意匿名读漏洞。

位置：`api_phase3.go:275–287`。`filepath.Abs` 清理 `..`、prefix 校验阻止直接路径越界，但不解析 symlink；staging 内 kit 路径指向目录外文件时，os.Open 会跟随并返回其内容。直接 handler 探针返回 200、body 为目录外 canary。全局 guard 仍保护路由（`server.go:204–208`）；利用需要能布置 staging symlink/引用的条件，没有声称未经认证即可利用，也不把 same UID 本身重复列为漏洞。

建议：在文件打开时使用受目录约束的打开机制并限制常规文件，拒绝符号链接逃逸，覆盖父目录 symlink 和检查/打开竞争。仅 Abs/prefix 不满足注释所称“resolve under staging”。

## 迁移约定与升级一致性

**0012 本身遵守向前迁移约定**：`backend/internal/db/migrations/0012_verify_profile.sql:4–8` 只新增 verify_profile 并提供 Down；相对上一轮版本没有改写 0011 或其他已应用迁移，`git diff 283a3d5..7986963 -- backend/internal/db/migrations` 只有新增 0012。已有 v11 升级新增这一列没有显式 Scan/UPDATE 错位。

但指定完整基线 diff 中，`0004_backup_kernel.sql:39` 新增 duration_secs，`0008_review_fixes.sql:14–15` 还在 **Down 区段**追加同名 ADD COLUMN。两者相对 `283a3d5` 已存在，因此不是这次修复新引入；仍违反历史迁移不可修改约定，0012 也没有向前补齐该列。

实测基线 v9 → 当前 v12：Migrate 返回成功，verify_profile 存在，jobs.duration_secs 不存在。新装库通过测试依赖的是改过的 0004，不能证明旧部署升级正确；`server/stats_handler.go:21` 和 `stats/stats.go:98` 读 duration_secs 可报错。应在后续迁移兼容旧/新安装补列，恢复历史迁移约定并测试基线、v11、fresh 三条路径；不要再把修复塞进旧 Up/Down。

## Race / 一致性判断

未观察到 Go data race，不能据此排除 SQLite 状态与文件删除的逻辑竞态。已确认：successSQL/Scan 列对齐，正常任务的 succeeded 与初始 verify 状态同条 UPDATE，错误详情先由任务 redactor 处理再截断（`verify.go:221–225`）。

仍需处理的并发边界是：队列 pending 与 retention、lease check 与 remove、远端 retention 与验证终态变更、启动扫描与正常入队重复、Stop 与未加入 wg 的 startup resumer。用持久状态原子 claim、统一的验证/删除 lease 和锚点选择保证这些边界；不能依赖单个内存 map 的瞬时检查。新字段实现需覆盖状态写失败与文件缺失，而不只是 happy path。

## 总体结论与最关键的 3 件事

**不通过 Phase 5/6 完成验收；可以关闭续行及生产接线这两个独立缺陷，不能宣称 P1 已清零。** 本次没有确认 P0。新的确认问题为 P1 2 项（其中停止 PG 是旧项回归）及 P2 1 项，其余是上轮编号的未完成修复，避免重复计数。构建/vet 与可执行单测改善不替代完整恢复和销毁证据。

1. **先修复进程与工件生命周期**：所有启动后出口确认停止 PG；停止失败保留回收证据；启动清理先收敛进程；统一 queued/running lease 和本地/远端 verified 锚点，禁止删除验证输入或最后可恢复副本。
2. **把 verified 变成可审计的恢复证明**：显式零/未知语义、缺 manifest 拒绝或诚实降级、真实对象集合/角色/版本/选项 profile、可信来源与扩展白名单、远端读取解密闭环；彻底堵住 conninfo 密码进 argv 的绕过。
3. **完成状态与升级门禁再验收**：共享正常/重启成功终结路径，状态写失败重试/收敛、原子去重及可靠重启脱敏；仅用新迁移修复历史列；补真实 runtime 恢复、精确数据、失败 cleanup、竞态保留和三平台手动演练证据。
