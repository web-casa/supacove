# Phase 5 + 6 实现评审（第一轮）

评审日期：2026-10-04。评审基线：`2dcbbc8`；最终版本：`283a3d5`（当前 HEAD）。对照 `docs/dev-plan.md:175–212` 与 `docs/adr/ADR-004-embedded-verifier-trust-boundary.md`。

**结论：不能作为 Phase 5/6 完成验收。发现 P0 0 项、P1 10 项、P2 3 项。** 恢复脚本当前不能正常执行；正式服务不会启动恢复验证，即使手动接线也会把密文当作明文恢复。建议修复 P1 并补充真实闭环证据后再验收。

## 范围与验证说明

用户描述为“新增 2 个 commit”，但 `git log 2dcbbc8..283a3d5` 实际有 6 个：`fe3f84a`、`129d4af`、`a5b0032`、`bfd484e`、`20d7112`、`283a3d5`。本报告按指定基线到 HEAD 的最终状态评审，重点覆盖 platform、recovery、verifier、jobs、迁移 0011，以及启动接线、下载和保留策略这些直接依赖。

`backend/internal/jobs/phase3_test.go` 在该区间没有 diff，也没有提交修改记录；未找到描述中的测试更新。Phase 8 统计仅评估其与验证状态的一致性，其余 Phase 8 问题不扩展到本报告。

执行结果：

| 检查 | 结果与限制 |
| --- | --- |
| `go build ./backend/...` | 使用可写的 `GOCACHE=/tmp/codex-phase5-go-cache` 后通过；另运行 `go build -buildvcs=false ./backend/...` 通过。默认缓存路径首次因只读文件系统失败。 |
| `go test -race ./backend/...` | 使用上述缓存后，jobs、platform、db、dumper 等包通过；server 包在 `TestPhase2DefaultDenyAndAgeStatus` 因 `httptest` 监听 `[::1]:0` 被沙箱拒绝而失败，不能声称全套通过。recovery/verifier 均无测试文件。 |
| `go test -race -v ./backend/internal/jobs -run '^TestM1_'` | 两个 M1 用例因 Docker 容器不可用而 SKIP；不是恢复闭环通过证据。 |
| 对实际生成的脚本运行 `bash -n` | 通过。语法检查无法识别错误的命令续行语义。 |
| 实际生成脚本 + 假 age/pg_restore 执行探针 | `pg_restore` 的唯一参数为字面量 `\`，下一行作为命令执行，脚本退出 127。探针只替换外部工具，脚本来自真实生成器。 |
| 已存在 dump + 缺失密文探针 | 脚本退出 1，但原先存在的 `restore-job-42.dump` 被 cleanup 删除。 |
| 平台函数探针 | `supabase.com.evil.invalid` → supabase；`postgres.railway.internal` → generic；Neon `-pooler` host + 5432 → 无警告。 |

没有执行真实三平台手动恢复、正式镜像恢复验证或恶意 dump 演练。下面区分直接复现与静态调用链证据；资源耗尽、秘密泄露等后果属于有条件风险，未声称已在生产触发。

## P0

未确认当前正式入口可达的 P0。尤其不能把 ADR-004 明确接受的“同 UID 可读应用文件”本身重新定义为漏洞；需要修复的是未落实其启用条件、环境白名单与资源治理。当前 verifier 未接线，因此下面部分风险会在接线后暴露，不能直接补一个 `SetVerifier` 就发布。

## P1

### P1-01：脚本使用双反斜杠续行，正常恢复必然失败

位置：`backend/internal/recovery/kit.go:96–104`。

模板是 Go raw string，`\\` 会原样生成两个反斜杠。shell 将它们解释成一个字面量反斜杠，换行仍结束命令。实际探针显示 `pg_restore-arg=<\>`，随后 `--dbname=postgresql://test@localhost/empty: not found`，退出 127。psql 的续行也存在同一问题。`bash -n` 返回 0 不能证明脚本可执行。

修复：raw string 中使用一个反斜杠续行，或改成单行命令。增加 argv 捕获断言，并在新建 PostgreSQL 目标上执行完整恢复，不能只验语法。

### P1-02：明文临时文件不是独占文件，cleanup 会删除已有文件；运行时密码暴露在 argv

位置：`backend/internal/recovery/kit.go:68–80,96–104`。

明文输出是当前目录固定文件 `restore-job-<id>.dump`，没有私有临时目录、独占创建、权限约束或进程间隔离。cleanup 在确认密文存在之前注册，而且不判断文件是否由本次执行创建。已复现：预先写入该文件，移除密文，运行脚本后已有文件被删除。并行运行同一脚本也会互相影响。生成器不设置 `umask 077`，不能保证外部 age 的输出行为满足计划要求的 0600；使用普通文件创建的假 age 探针实际产生 0644，但这不是对真实 age 默认模式的实测。

目标 URI 通过脚本位置参数进入，并继续作为 pg_restore/psql 的 `--dbname` 参数；如果含密码，操作系统进程参数会包含密码。脚本确实没有嵌入源库凭据，但这不等于运行时凭据传递安全。仅 `trap ... EXIT` 也缺少针对 HUP/INT/TERM 的明确处理，未提供中断后清理证据。

修复：使用独占 0700 临时目录、0600 明文文件与 `umask 077`，只清理本次创建的资源；明确明文落盘边界。密码交互读取或独立安全输入，使用正确转义的受限 PGPASSFILE，传给客户端的 conninfo 不含密码。对退出和可捕获信号统一清理并保留失败退出码。

### P1-03：恢复前自检缺失，可能在错误/非空目标上部分写入

位置：`backend/internal/recovery/kit.go:53–100`。

SHA256 只出现在注释中，没有密文哈希核对；“EMPTY database”也只是注释。执行顺序直接解密再恢复，没有工具/版本、目标为空、profile 扩展/角色依赖检查。`--no-owner` 不提供非空保护，`--exit-on-error` 也不回滚此前已执行的对象和数据。非空但对象名称不重叠的目标可以被继续写入；有冲突时可能在中途失败。失败没有说明“目标可能已部分写入”。后续 psql 查询失败还被 `|| echo "0"` 吞掉，修复续行符后会误报完成。

修复：按 P5 第 2 项实现启动前自检，明确哈希仅检错、不是签名；通用 profile 默认拒绝非空目标，平台预置对象采用明确且已测试的允许规则；失败提示部分写入并返回非零。psql 使用 `-X`、必要的错误退出选项，并禁止吞掉验证失败。

### P1-04：生产启动没有初始化 verifier，正式镜像也没有服务端二进制

位置：`backend/cmd/supabackup/main.go:209–228`；`backend/internal/jobs/jobs.go:423,861`；`Dockerfile:38–50,85–92`。

全仓调用搜索只找到 `SetVerifier` 的定义，没有调用；`verifier.New` 也没有生产调用。Runner 的 verifier 默认为 nil，正式服务所有备份都会跳过恢复验证。正式 `runtime` 仅安装 PG 客户端，服务端只在明确“不发布”的 `runtime-spike` 中。

修复：先落实 ADR 的管理员可信来源显式启用配置与告知，再完成初始化、启动失败处理和 Runner 接线；正式发布镜像纳入已测试 profile 的服务端清单。未启用必须记录可区分的状态与原因，不能用空字符串代替功能状态。

### P1-05：异步入口把 age 密文传给要求明文的 pg_restore；没有远端解密验证

位置：`backend/internal/jobs/jobs.go:865`；`backend/internal/verifier/verifier.go:65–68,120–123`。

`result.ArtifactPath` 是 dumper 原子提交的 `.dump.age` 密文。`verifier.Input.DumpPath` 明确要求已解密 custom-format dump，Verify 内部直接将路径传给 pg_restore，没有 age 解密步骤，也没有 identity 输入。因此手动配置 verifier 后，正常备份会在恢复阶段失败。用真实 age 库生成封装并交给 PG18 `pg_restore --list` 的探针也被拒绝；该探针仅证明密文不能作为 archive 输入，不作为真实 dump 闭环证据。

现有远端 readback 验证只做密文完整读回和哈希。新增 verifier 读取本地文件，没有与远端完整读取/解密链结合，不能满足 P6 最低判据。

修复：明确离线 identity 与自动验证的密钥架构；取得并校验提交对象的完整密文，安全解密到受限临时资源，再执行恢复。区分传输、解密与 SQL 失败。不能把私钥默认引入备份服务来绕过既有密钥边界。

### P1-06：验证 goroutine 脱离 Runner 生命周期且无资源预算，退出不能收敛

位置：`backend/internal/jobs/jobs.go:426–436,861–880`；`backend/internal/verifier/verifier.go:79–95,168–184,198–213`。

每次成功备份直接启动 goroutine，context 来源是 `Background()`，没有加入 Runner 的 WaitGroup。Stop 只等待备份 worker；后续数据库关闭时验证仍可能写状态，进程退出也不保证执行 deferred cleanup。虽然备份并发为 1，连续备份可以留下多个同时运行的 PG 验证实例，未实现并发 1、有界队列、解压后磁盘预算、备份优先或启动残留回收。

五分钟 context 也不是完整 wall-time 上限：`stopPG` 使用无 context 的 exec；`CombinedOutput` 在截断错误消息之前已将所有输出收集进内存；停止错误被忽略后直接删目录。`Input.Timeout` 未被 Verify 使用。`setupDirs` 第二个目录创建失败时，第一个目录没有回收。

修复：使用绑定 Runner 生命周期的单 worker 和有界持久/可收敛队列；Stop 取消并等待验证退出。按恢复峰值预留磁盘，工具输出有界读取，cleanup 使用独立有界超时并确认 PG 已停；启动回收残留，记录失败，部分目录创建也必须清理。

### P1-07：ADR-004 的环境白名单、可信来源启用边界和扩展限制未落地

位置：`backend/internal/verifier/verifier.go:107,121,168–184`；`backend/internal/jobs/jobs.go:861–865`；`docs/deployment.md`。

子进程环境直接 `append(os.Environ(), "LC_ALL=C")`，会继承应用的环境凭据和影响 libpq 的配置，违反 ADR 的白名单要求。所有归档扩展直接交给 pg_restore，没有按已测试 profile 预检/限制。只要设置全局 verifier，所有成功任务都会自动验证，没有每来源可信性与管理员显式启用控制，也没有 UI/发布文档说明验证与应用同 UID、能读取应用可读文件。

修复：建立最小环境白名单并固定 PG 连接配置；从依赖和 TOC 检查 profile 支持的扩展；以明确启用和可信来源为门槛，提供 UI/部署文档告知。环境白名单减少凭据继承，不能被宣传为恶意 dump 沙箱。

### P1-08：没有 profile/预期对象校验，verified 不能证明声明范围恢复成功

位置：`backend/internal/verifier/verifier.go:28–31,65–72,126–141`；`backend/internal/jobs/jobs.go:865–872`。

验证输入没有 manifest、预期 TOC/对象集合、profile ID、版本/扩展组合或恢复选项；恢复零错误后仅统计用户表数量，数量为 0 也可 verified。合法但缺失声明对象的归档无法被识别。缺角色/扩展或预算不足只会失败，无法按 P6 区分 `unsupported` / `skipped` / `failed`。`--no-owner` 不等于完成角色初始化，原库 ACL 依赖缺失角色时也会失败。

修复：定义并持久化已测试 profile 和选项，恢复前做版本/依赖支持判断，恢复后比较预期对象集合；表数仅作诊断。公开通过措辞使用“在 profile X 上按选项 Y 恢复成功”。静态 fixture 另做精确数据断言。

### P1-09：恢复包没有持久化引用或下载/远端交付，保留策略未升级验证锚点

位置：`backend/internal/jobs/jobs.go:846–856,898–901`；`backend/internal/db/migrations/0011_verify_kit.sql:7–8`；`backend/internal/jobs/upload.go:115–118,219–259,361–370`；`backend/internal/server/api_phase3.go:178–256`。

脚本在远端提交、任务成功之后才生成，仅写本地。没有更新 `recovery_kit_path` 或 `jobs.platform`，没有上传脚本，也没有对应下载接口；已有下载只交付密文。写入失败仅日志记录，重启恢复远端提交路径不会补生成。脚本权限由 durableWriteFile 固定为 0600，与说明中的 `./restore.sh` 不一致；本地 prune 仅删除密文/manifest，脚本会遗留。用户依下载通道不能得到所称“自包含恢复包”。

更关键的是保留策略仍选择最新 committed/succeeded 作为锚点，不考虑验证等级；较新备份验证失败或仍在验证时，旧已验证备份可能被删除。下一次备份清理旧本地文件也可与仍读取该文件的验证竞争。这违反 P6 的“新锚点须验证等级达标后事务替换；验证失败/积压不降级”。

修复：将 kit、manifest、密文的交付与状态关联起来，持久化路径/平台，提供安全下载及失败重试，统一清理并明确 `sh restore.sh` 或可执行权限。保留策略以验证达标的事务锚点和执行中的文件租约保护对象；新增备份未达标不能删除上一验证锚点。

### P1-10：Supabase 指南与实际导出范围冲突，通用恢复不能替代平台 profile

位置：`backend/internal/platform/platform.go:61–70`；`backend/internal/jobs/jobs.go:755–763,849`；`backend/internal/dumper/dumper.go` 的 dump 参数构造。

指南断言 Auth users 不包含、属于独立服务；但当前导出是全库 pg_dump，没有应用 schema profile 排除，manifest 也声明所有用户 schema。Supabase Auth 数据在 auth schema，Storage 元数据在 storage schema，不能和外部 Storage 文件混为一谈。实际范围必须依据 TOC/权限声明。[Supabase 数据库说明](https://supabase.com/docs/guides/database/overview)、[Storage schema 说明](https://supabase.com/docs/guides/storage/schema/design)。

指南建议把全库 archive 用 `--no-owner` 恢复到预置 auth/storage 的新项目；该选项只跳过对象所有权，不能解决既有 schema/对象、ACL 角色、托管扩展与版本兼容。官方恢复流程专门处理托管 schema 和角色，不能由这里的通用模板替代。[Supabase 备份恢复指南](https://supabase.com/docs/guides/platform/migrating-within-supabase/backup-restore)。

修复：落实 ADR-003/P5 的 Supabase profile，beta 若只支持应用 schema 就明确导出和恢复范围，基于实际 TOC 展示包含/排除/平台外部资源。不要根据 host 重新生成与用户确认平台不同的指南；提交新 Supabase 项目的真实手动演练，裸 PG 验证不能替代。

## P2

### P2-01：平台域名匹配过宽，Neon 池化规则错误且提示未接入向导

位置：`backend/internal/platform/platform.go:22–50`；`backend/internal/server/api_phase2.go`。

`strings.Contains` 把 `supabase.com.evil.invalid`、`notneon.tech.example` 等任意包含字符串的域名误分类，未检查 DNS 标签边界；Railway 私网 `postgres.railway.internal` 被当成 generic。当前仅影响指南选择，没有证据表明这里承担 SSRF 放行，因此不定性为 SSRF 绕过。

Neon 判断完全依赖 6543，漏掉官方 pooled endpoint 的 `-pooler` hostname；实际探针在默认 5432 返回空提示。官方按 hostname 的 `-pooler` 区分连接池，导出指引要求关闭 pooling。[Neon 连接指南源码](https://github.com/neondatabase/website/blob/main/content/docs/get-started/connect-neon.md)、[Neon 导出指南源码](https://github.com/neondatabase/website/blob/main/content/docs/guides/export-neon-postgres-compatible.md)。此外 PoolingHint 仅被测试调用，用户注册/连接测试不会看到任何新增提示；自动 Detect 仅在生成 kit 时调用，没有形成三平台向导。

修复：规范化 host 后做官方 endpoint 标签/后缀匹配，识别 Neon `-pooler` 与 Railway 私网特征；把建议接入现有连接检测流程并保留用户确认，不因私网名称直接拒绝。Neon 冷启动独立超时和 Railway 部署位置可达性检查仍需按 P5 补齐。

### P2-02：迁移可执行不等于状态闭环；API、manifest、统计仍无法反映验证结果

位置：`backend/internal/db/migrations/0011_verify_kit.sql:3–8`；`backend/internal/jobs/jobs.go:770–773,869–879,890`；`backend/internal/jobs/queries.go:10–69`；`backend/internal/server/api_phase2.go:279`。

六列有默认值，现有显式 SELECT/Scan 不会因为新增列本身错位；本次 db 测试通过。但是只有四个 verify 列在异步完成后被写入，未提交 pending/running，nil verifier、排队、进程崩溃都表现为空字符串。状态写入失败只日志记录，没有重试/重启收敛；异步错误 Detail 未经已有任务脱敏链处理。Task 查询和 API 不读取任何新列，用户不可见。

manifest 本地与远端仍固定 `RestoreVerified=false`，统计固定 `not_run`，异步完成没有更新或关联验证记录。于是数据库可能 verified，下载的 manifest 与统计仍声称未验证。

修复：定义持久状态机及原因、独立验证记录/profile、写失败重试和重启收敛；让 API/统计读取同一权威记录。已提交 manifest 可以保留为备份时刻快照，但必须明确其时间语义，并另提供关联验证证明，不能让多个状态源互相矛盾。错误输出先脱敏再截断、持久化和日志记录。

### P2-03：新增测试不足，现有通过结果不能覆盖 Phase 5/6 DoD

位置：`backend/internal/platform/platform_test.go:7–45`；`backend/internal/recovery/`；`backend/internal/verifier/`；`backend/internal/jobs/phase3_test.go`。

platform 只测几个正例，且 Neon 用例把端口误判写成了预期；recovery/verifier 没有测试；phase3_test 本次没有更新。现有 jobs 测试在 nil verifier 下通过，只能间接说明跳过分支没有破坏原链路，无法验证异步接入、迁移新状态或脚本行为。

最少补充以下门禁：

1. 平台伪后缀、大小写/尾点、Neon pooled 5432、Railway 私网、用户平台覆盖及向导输出。
2. 生成脚本实际 argv、完整执行、错私钥、哈希不符、缺工具/旧版本、非空目标、SQL 错误、已有同名文件、并发运行、信号 cleanup、密码不进入客户端 argv、明文 0600。
3. 正式镜像内 fixture → 密文提交/远端读取 → 解密 → profile 恢复 → 对象集合与精确数据断言 → 销毁；传输截断、合法 age 封装的无效 dump、缺角色/扩展、声明对象缺失分别分类。
4. nil verifier 的明确状态、可信来源启用、环境 canary 不继承、并发上限/满队列、超时/磁盘不足、Stop 等待、重启回收、状态写失败收敛、验证期间保留策略保护。
5. 迁移后历史任务与新任务状态的 API 可见性，以及 Supabase/Neon/Railway 三平台各一次桶下载与新目标手动恢复记录。

## 已确认的正向点与边界

- nil verifier guard 存在，未配置时不会直接解引用 nil；不能因此认为功能已接线。
- 验证状态更新采用绑定参数，未发现这段 SQL 的拼接注入。
- TARGET_URL 在模板中有 shell 引号，未发现位置参数自身被 eval 为 shell；问题在 argv 凭据暴露、续行和自检。
- 当前 jobs 提供的 UUID/文件名为程序生成，不含用户原始 URI/密码，生成文件也为 0600；未发现源库凭据直接嵌入 kit。
- 生成器 API 本身对元数据没有编码：文件名/UUID 直接进入 shell 双引号和注释。当前调用路径受控，未作为独立可利用漏洞计数；以后复用外部 manifest 时必须校验/编码，并补 `$()`、引号、换行用例。
- Verify 显式关闭 TCP、使用专用 socket 目录、`--exit-on-error`，有 deferred 停止/删除意图；缺失的是受控生命周期、白名单、预算与清理结果保证。
- 没有默认 `--clean`/`--create`。这不代替非空目标拒绝，也不保证失败时零写入。

## 总体结论与最关键 3 件事

**不通过 Phase 5/6 完成验收。** 当前实现保留了既有备份主链的一部分行为，但恢复脚本不可用、自动验证未交付，安全与状态闭环尚未满足计划/ADR。P1 修复完成后，应在可运行 Docker/监听端口的环境重跑完整竞态测试和真实恢复门禁。

1. **先使恢复包安全且可执行**：修正续行，补哈希/空库/profile 自检，独占受限明文临时资源、安全密码输入、可靠 cleanup，并实际交付可下载 kit。
2. **建立真实验证链**：显式可信来源启用，正式镜像/启动接线，安全远端读取与解密，按已测试 profile 核对预期对象，状态由同一权威记录提供。
3. **在启用前完成生命周期与锚点治理**：单并发有界队列、恢复磁盘预算、Stop/重启收敛、环境白名单，以及保护上一个已验证备份的保留策略；用故障注入证明主备份链不被验证拖垮。
