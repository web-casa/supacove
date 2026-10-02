# Phase 1 第五轮复审

评审日期：2026-10-03。评审提交：`45979649ad5b6d61ad4598e0ba73dbbd4a6d2c81`；范围：`git diff db42d83..4597964`，共 13 个文件（含第四轮报告）。仅复核第四轮 OPEN 项及本提交引入的回归，不重新展开已关闭问题。下文业务文件行号均指 `4597964`。

**结论：Phase 1 的“代码与工程门禁”未通过。** 待迁移库持锁 CLI、secret 重试持久化确认、login 长度与字符语义、角色 SQL 格式及 FAIL 名称误判已关闭；取消备份的正式产物隔离也通过实测。但旧格式恢复点仍被合并删除，Spike 1 表枚举失败仍会报告成功，health/details 契约仍遗漏 503，并出现检查项减少、镜像健康检查竞态及错误 RSS 归属三项回归。即使完全排除真实 Supabase、远程 CI、socket 和双架构待执行项，仍不足以判为“通过”或“修改后通过”。

判定按缺陷范围给出：FIXED 为该项已关闭；PARTIALLY_FIXED 为有效修复但仍有实质残留；NOT_FIXED 为原根因未消除；REGRESSED 为相关变更新增故障或错误验证结果。交叉编号不是独立缺陷计数。第四轮 OPEN 及其交叉编号共 **14 条记录：FIXED 6 / PARTIALLY_FIXED 4 / NOT_FIXED 1 / REGRESSED 3**。本轮新增根因 **3 项：P1 × 1、P2 × 2，无新增 P0**。

## 逐项关闭判定

| 第四轮 OPEN 编号 | 第五轮判定 | 修复效果、残留与证据 |
|---|---|---|
| R3-P1-01：待迁移库持锁 CLI 拒写 | **FIXED** | `backend/internal/db/migrate.go:135–143` 调用 provider `HasPending`。真实二进制、实际 flock 下，原版 v1 与修改版 v1 均退出 1，报 pending migrations；版本仍为 1，bootstrap token 数量为 0。当前 v3 持锁 CLI 成功；v999 拒绝。未将 socket 环境限制用于重开本项。 |
| P1-06：pending schema 防线 | **FIXED** | 与 R3-P1-01 共用根因。未初始化库持锁 CLI 拒绝；两种 v1 在独占锁分支均迁移到 v3 后成功；未来 v999 的独占锁和持锁旁路均拒绝。锁外路径没有自行迁移旧库。原始升级 fixture 和新增 pending 测试通过。 |
| P1-04：已存在路径目录 fsync | **FIXED** | `config/config.go:130–133、180–201` 让已有 secret 与 EEXIST 递归读取同样经过 `syncDir`。0300 目录中首次发布、第二次读取及 16 路并发读取均返回目录 Open 错误；恢复 0700 后返回原 key，文件字节不变。原并发创建测试 `-race -count=100` 通过。未注入 Sync EIO、Close 错误或断电；错误传播代码已核对，不将未做的故障实验写成通过。 |
| P1-08：失败产物隔离与恢复点保留 | **PARTIALLY_FIXED** | `db/backup.go:30–50` 改用 `.db.inprogress`，成功后 rename。64 MiB SQLite 在临时文件出现后取消，返回 `context canceled`，正式文件和临时文件均不存在；随后再次备份成功，重复 10 次通过。版本数字排序、失败迁移不裁剪、同源重试保留均通过。**旧格式误删仍在，见 R4-P1-01。** |
| R4-P1-01：旧格式恢复点错误去重 | **NOT_FIXED** | 改为保留较新的旧文件，但仍把所有旧时间戳文件压成一个 batch；存在五个新版本时全部旧格式文件仍被删除。未解决“无法判断源版本却合并恢复点”的根因。自带测试只断言总文件数，不能证实旧文件均保留，详见下文。 |
| P0-02：login 下限及有界校验残留 | **FIXED** | `server/api.go:106–114` 在调用 IssueSession/KDF 前验证 username 3–64、password 12–128 rune。1 字符用户名/密码、2 字符用户名、11 字符密码均 400；65 字符用户名、129 字符密码均 400。12/50/128 个汉字密码可 bootstrap 201、login 200；4 个汉字 bootstrap 400。既有 body 上限、限流及 KDF 测试通过。原 P0 编号不再保留 OPEN。 |
| P1-09：rune 语义及 details 503 契约 | **PARTIALLY_FIXED** | password 验证与 login 均按 rune；四汉字/50 汉字边界已修。**`api/openapi.yaml:48–64` 未修改，details 仍仅声明 200/401。** 真实 Router、已登录后关闭 SQLite，`/api/health/details` 返回 503 JSON `storage_unavailable`。Go/TS 生成物与 spec 同步，但同步的是仍遗漏 503 的契约。 |
| P1-14：check 前置与工程门禁 | **REGRESSED** | `.NOTPARALLEL` 下，`frontend` 现在在 `api-check` 前执行，`npm ci` 顺序已修；`.gitkeep` 保留通过实测。**Go 生成器仍无安装前置，单独 api-check 仍无 npm 前置；同时删除了原 check 的前端 lint 和最终 Go build**，见 R5-P2-01。 |
| R3-P1-03：角色 SQL 的 shell `%I` | **FIXED** | `spike1-supabase-restore.sh:121` 使用 `%%I`，真实 Bash 输出完整 `SELECT format('CREATE ROLE %I NOLOGIN', :'role');`。实际 PG 18.4 单用户后端对普通、单双引号及换行角色名生成 DDL、执行并查询确认角色存在。SQL 层问题关闭；脚本枚举仍拆分换行，归入 R2-P1-04。 |
| R2-P1-04：对象名边界与 pattern | **PARTIALLY_FIXED** | `%I` 与 psql 变量路径可用，旧 `count_rows()` 死代码已删除；未发现本轮可执行 SQL 注入证据。**换行仍被 schema 的 `tr`、role/table 的 `mapfile -t` 拆分；新增 escape_pattern 错把 pg_dump 模式视为 LIKE，只处理 `%/_/反斜杠`，没有正确处理 `* ? []`、大小写和引号。** |
| P1-18：Spike 1 实验有效性 | **PARTIALLY_FIXED** | 角色格式和 FAIL sentinel 已修。**表枚举失败仍退出 0**：`mapfile ... < <(psql ...) || ...` 捕获的是 mapfile 状态；无表记录被当成成功。profile 共用目标、只做末尾一次行数比较、角色仅 NOLOGIN 占位、权限/RLS/FK/TOC 独立断言缺失、restore stderr 覆盖及含凭据 URL 进入 argv 等第四轮残留未改。真实项目未执行不是本项判定依据。 |
| R4-P2-01：对象名含 FAIL 误判 | **FIXED** | `spike1:187–204` 使用独立退出码和 COUNT_ERROR。普通名字与 `FAIL_invoices` 均在两端计数 7 时退出 0；两端 count 失败退出 1，不再从名称文本推断失败。失败日志仍可能打印 `exact row counts match`，但最终状态正确，不据此重开原 sentinel 缺陷。 |
| P1-19：Spike 2 代码内可验证部分 | **REGRESSED** | EXIT trap 与按归档一半截断有效；注入首次 restore 退出 9，确实执行 stop/remove，保留退出 9；坏档错误首行已输出。**新增服务端采样可能选中脚本 shell，伪称 postmaster RSS**，见 R5-P2-02。有效采样断言、恢复 backend 子进程资源、失败清理结果断言、独立同 UID 文件/进程边界及 setpriv SKIPPED 仍算成功等残留未关闭。 |
| P2-04：PR 日期 fallback 与 smoke | **REGRESSED** | BUILD_DATE 增加 `'unknown'` 非空兜底；该值不等于实测构建时间。新增 HTML、health 状态和 stop/退出码断言，但 **只 sleep 3 后立即要求 healthy，产生正常启动误失败竞态**，见 R5-P1-01。HTML 只 grep 产品名，未请求 JS/CSS；双架构实际验证另列外部项。 |

## 仍然 OPEN 的代码问题与本轮回归

### 1. P1-08 / R4-P1-01：仍删除无法按版本归类的恢复点

位置：`backend/internal/db/backup.go:76–80、96–116`；测试：`backend/internal/db/db_test.go:445–453`。

所有旧格式文件仍被赋予 version 0，只把 `legacy[n-1]` 放入候选集合。随后按版本倒序取五个 batch。所谓“Never delete anything we cannot classify”循环中的 `keep[lg.path] = len(unique) > 0 && keep[lg.path]` 不会把任何原来为 false 的项变为 true，不能保护旧文件。

| 真实裁剪函数输入 | 第五轮实际结果 |
|---|---|
| 两个旧文件：20260101、20260102；无新格式文件 | 删除 20260101，只保留 20260102，未达五份仍丢恢复点。第四轮是错误保留较旧文件，本轮只是换了 survivor。 |
| 同上两旧文件 + v1～v5 五个新版本 | 两份旧文件全部删除。 |
| v5～v10 六个新版本 | 正确保留 v10、删除 v5；这项历史修复保持有效。 |
| 历史 v1 + 五次失败的 v3→v4 迁移，再成功 | 历史 v1 与最新 v3 均保留；失败重试不会挤占已知源版本。 |

新测试添加两个旧文件和一个 v5 文件，却仅检查 `len(left) >= 2`，因此“一旧一新”就能通过。应逐名断言需要保护的恢复点。旧文件无法推断同源版本，不能据此合并；可保守排除出自动删除集合，或实现有明确迁移规则的时间保留策略，未知格式应保护。当前误删是代码阻断，不能归因于外部实验未执行。

### 2. P1-18 / R2-P1-04：Spike 1 仍可假成功且名称边界不完整

位置：`scripts/spike1-supabase-restore.sh:43–50、57–70、99–105、175–180`。

使用受控 PostgreSQL 客户端替身执行**提交中的完整 Bash 脚本**：候选 restore 成功，仅表枚举命令返回非零，最终得到两个空 counts 文件、`exact row counts match`、退出 **0**。这不依赖真实 Supabase。最小 Bash 语义复现为：

```bash
mapfile -t TABLES < <(exit 7) || echo enumeration-failed
printf 'mapfile rc=%s, tables=%s\n' "$?" "${#TABLES[@]}"
# mapfile rc=0, tables=0；没有进入错误分支
```

应将枚举结果写入临时文件并明确检查 psql 退出码，再读取记录；不能靠进程替换外层的 `||` 判断子进程成功。相同模式也出现在角色/模式枚举，应一起核对。

名称探针中，一个 `line\nbreak` 表被拆成两个 `tbl=` 参数；换行角色同样被拆分。schema 枚举的 `psql -z` 设置的是字段分隔符，不是记录分隔符，随后 `tr '\n' '\0'` 会破坏合法名称内换行，应使用真正的 NUL 记录边界。参见 [PostgreSQL 18 psql 选项](https://www.postgresql.org/docs/18/app-psql.html)。

实际捕获 `schema_flags` 对 `App*prod` 仍输出未加模式引号的 `-n App*prod`。pg_dump 的 schema 参数使用 psql 名称模式，包含大小写折叠、`*`/`?` 通配和正则字符；并非 SQL LIKE。双引号内双写内部引号才可按名称精确匹配。参见 [pg_dump 的 schema 选项](https://www.postgresql.org/docs/18/app-pgdump.html)与 [psql Patterns](https://www.postgresql.org/docs/18/app-psql.html#APP-PSQL-PATTERNS)。本轮 PG 单用户探针对文档规定的 `app*prod` 正则语义同时匹配到 `app*prod` 和 `appXprod`；**未宣称在受限环境完成真实 pg_dump 联机匹配实验**。

此外，第四轮要求的各 profile 干净目标、完整角色属性/成员关系与权限恢复、各自 RLS/FK/TOC/行数断言和独立 stderr 文件均没有补上。P-A 的 schema、data 分别增加 passed 数，P-B 意外成功也增加 candidate passed 数，末尾没有“完整 profile 恢复且独立校验成功”的记录。以上都是脚本本身可审查的缺口，不是要求现在提供真实项目才能判断。

### 3. P1-09：details 503 仍未声明

位置：`api/openapi.yaml:48–64`，实际返回分支 `backend/internal/server/server.go:305–309`。

关闭已登录测试实例的 SQLite 后，session guard 返回 `503 application/json`，body 为 `{"code":"storage_unavailable","message":"session store unavailable"}`；spec 和生成类型仍仅列 details 的 200/401。补 503 声明、重新生成 Go/TS，并对 guard 错误响应做契约断言即可关闭。此项不是新的认证绕过或 P0 风险。

### 4. R5-P1-01（新回归，关联 P2-04）：smoke 在首次 healthcheck 前拒绝正常容器

位置：`.github/workflows/ci.yml:85–91`；配置依据：`Dockerfile` 的 `HEALTHCHECK --interval=30s --start-period=5s`。

应用 HTTP 已可用并不代表 Docker 已完成健康检查。当前引擎的 start interval 默认 5 秒，容器最初为 `starting`；脚本仅 sleep 3，其余检查执行较快时会在首轮探针前断言 healthy 而退出 1。旧引擎不支持 start interval 时还可能需要等待常规 interval。该时序依据 [Docker HEALTHCHECK 官方说明](https://docs.docker.com/reference/dockerfile/#healthcheck)，不是本轮实测容器结论。

将工作流 smoke 原文交给 `bash -e -o pipefail`，仅替换 Docker/HTTP 返回值：UID、healthz、bootstrap 全部正常而 health 为合法 `starting` 时，退出 **1**，未执行 stop；health 为 healthy 时才继续。应有总超时的轮询，超时输出 inspect/logs，并用退出清理保障失败路径。不能把正在健康检查的正常状态直接当失败。

同一探针中，根 HTML 含产品名但指向缺失 JS，smoke 仍退出 0，因为它从不请求资产。这延续第四轮资产验证残留；本轮实际构建资产可用，不代表当前 smoke 能拦截以后资产丢失。

### 5. R5-P2-01（新回归，关联 P1-14）：调整依赖顺序时减少了 check 检查范围

位置：`Makefile:46`，对比 `db42d83` 的 check recipe。

此前 check 在依赖完成后执行 `npm run lint` 和 `$(MAKE) build`；现在只有 `lint test frontend api-check`。`lint` 只检查 Go，`frontend` 只执行 npm ci/build。因此前端 ESLint 不再运行，前端嵌入后也不再执行 Go binary 构建。实际 `make -n check` 输出确认两条命令均缺失。远程 frontend job 仍有 lint，不能替代 README 承诺的本地入口。

顺序修复同时仍缺少 Go 生成器安装依赖：隔离目录模拟未安装 `oapi-codegen` 时，api-check 直接报可执行文件不存在；未安装 node_modules 时，单独 api-check 报 `openapi-typescript: not found`。应建立固定版本工具和 npm 依赖前置，恢复完整检查集合。联网下载本身的环境限制另算；本条判定针对 Makefile 没有安装规则及明确删除命令。

### 6. R5-P2-02（新回归，关联 P1-19）：新加的 postmaster RSS 可能实际采样脚本 shell

位置：`scripts/spike2-embedded-pg.sh:57、68–76`。

内层通过 `bash -c '<整个脚本>'` 执行，其 argv 本身包含 `postgres -D /tmp/pgdata`。新增 `pgrep -f "postgres -D /tmp/pgdata" | head -1` 会匹配该 shell。在容器中它通常是 PID 1，排在真实 postmaster 前面。使用真实 Bash、pgrep、`/proc` 执行原表达式，**无任何 PG 服务端运行也选中了当前 shell**，`/proc/<selected>/cmdline` 为脚本自身；这不是替身 RSS 推断。

应从此次实例的 `postmaster.pid` 等明确来源取 PID，再覆盖执行恢复工作的 backend 等子进程；只取 postmaster 的 VmHWM 本身也不足以代表整个临时 PG 服务端的峰值。还应断言至少采到有效值。受控完整脚本探针中 postmaster 显示 **0 MiB** 仍输出 `SPIKE_OK`，说明缺失采样也不会被门禁拒绝。

P1-19 的其他代码残留：

- EXIT trap 已对首次 restore 非零执行 stop/remove；但 stop 错误仍 `|| true`，失败路径没有清理结果断言。正常路径断言不能代表失败路径验证。
- 自适应截断已正确取原文件一半；stderr 写入容器内文件并输出首行，完整文件没有导出，`docker run --rm` 后不能复核全部错误。不得将任意 restore 失败都视为已经证明“由截断触发”。
- 同 UID secret 只由脚本创建，仍无另一个验证进程的读取/进程边界探针；setpriv 不可用仍 `exit 0`，外层仍输出 `SPIKE2_DONE`。受控执行再次复现该假完成状态。

## 实测与证据边界

仓库仅新增本报告，业务代码、自带测试、生成物均未修改。隔离副本由 `git archive 4597964` 建立于 `/tmp/codex-phase1-review-5/repo`；探针与日志位于 `/tmp/codex-phase1-review-5/`。路径为本机复核材料，非提交内持久测试产物。部分探针专门断言观察到的缺陷，**探针 PASS 不代表被评审项通过**。

| 验证 | 结果和边界 | 本地日志（上述目录下） |
|---|---|---|
| 原始 `go test -race -count=1 ./backend/...` | Go 1.26.0 linux/arm64；auth/config/db/limiter 通过，server 因 httptest 创建 socket 被沙箱拒绝而中止；命令退出 1，不能写成原样全绿。 | `logs/original-race.log` |
| 隔离全量测试与 Review5 探针 | 仅替换测试 transport 为真实 Router + ResponseRecorder，保留 cookie jar、RemoteAddr、非 nil Body，并补 Store cleanup；业务实现原样。`go test -race -count=1 -v ./backend/...` 全部通过。覆盖旧库升级/reset/revoke、pending CLI、Unicode/503、secret 重试和备份错误行为。不能替代真实 TCP/超时验收。 | `logs/isolated-race.log`、`setup-probes.py` |
| 真实二进制 CLI 矩阵 | 新库、两种历史 v1、当前 v3、未来 v999，各测持锁/独占锁，共十种 CLI 组合；pending 两类 v1 均无 token 写入，当前库成功，未来库拒绝。serve 在 guard 后受 socket 限制退出，未声称真实监听成功。 | `logs/binary-matrix.log`、`binary-matrix.py` |
| 重复故障与并发检查 | 原 secret 测试包 `-race -count=100` 通过；0300 目录重试与 64 MiB 备份中途取消 `-race -count=10` 通过。备份故障发生在临时输出出现后，区别于提交自带“调用前已取消”的测试。 | `logs/secret-repeat.log`、`logs/fault-repeat.log` |
| 静态与构建 | `go vet ./backend/...`、业务 gofmt、`git diff --check db42d83..4597964`、两脚本 bash -n 通过；后端真实构建成功。ShellCheck 返回 1，仅给出 SC2015/SC2086 既有 info，不另列新发现。 | `logs/vet.log`、`logs/shellcheck.log` |
| 实际 PostgreSQL 探针 | 全新 initdb；PG 18.4 单用户后端，无 socket。捕获真实 shell printf 文本，将 psql `:'role'` 以等价 SQL 字面量代入，再由真实 PG format 生成并执行角色 DDL；查询确认三个名称均存在。**没有运行完整联网 psql/pg_dump/Supabase 恢复链路。** | `logs/pg-init.log`、`logs/pg-probes.log`、`pg-probes.py` |
| Spike 1 Bash 行为 | 完整脚本 + 客户端替身：正常和 FAIL 名称退出 0，count 失败退出 1，枚举失败错误退出 0；名称/argv 边界已捕获。替身不能证明真实恢复结果。 | `logs/spike1-probes.log`、`logs/identifier-boundaries.log`、`spike-probes.py` |
| Spike 2 Docker 与控制流 | 真脚本在 docker run 被 Docker socket 权限拒绝；sudo 受 no-new-privileges 限制。替身运行实际内层 shell（固定 /tmp 改为隔离目录）：restore 退出 9 后已清理；半档截断、错误首行输出有效；setpriv SKIPPED 仍退出 0。真实 Bash/pgrep 另验证 PID 误选。**无本提交容器 RSS 或隔离性实测结果。** | `logs/spike2-real.log`、`logs/spike2-probes.log`、`logs/pg-probes.log` |
| 工程入口与 CI 控制流 | YAML 可解析，make dry-run 确认遗漏检查；无生成器/无 node_modules 的 api-check 失败；执行 smoke 原文，合法 starting 状态被拒、缺失资产未被请求。替身结果不冒充远程 CI。 | `logs/engineering.log`、`logs/make-check-plan.log`、`logs/api-check-no-generator.log`、`logs/api-check-no-node.log`、`engineering-probes.py` |
| 前端、生成物与 embed | Node 22.22.2；正常离线 npm ci 在 esbuild 安装 hook 的 spawnSync 处 EPERM。使用离线缓存并显式禁用安装 hook 后，真实 make frontend、单独 npm lint/build 成功；Go/TS 重生成逐字节一致，`.gitkeep` 为零字节且存在。带资产 CGO_ENABLED=0 Go 构建成功；真实 Router 的 JS/CSS 返回 200 与正确 MIME。没有宣称默认联网安装通过。 | `logs/frontend-install.log`、`logs/frontend-build.log`、`logs/frontend-lint.log`、`logs/generated-check.log`、`logs/assets.log` |

## 合并后的 OPEN 清单

| OPEN 根因（交叉编号合并） | 关闭所需工作 | 性质 |
|---|---|---|
| P1-08 / R4-P1-01 | 保护无法推断版本的旧恢复点，修正逐名断言；失败备份隔离已关闭，不再重复要求修复。 | **P1 代码阻断** |
| P1-09 | details 503 声明、生成物与实际 guard 响应一致。 | 契约任务未完成；非 P0 |
| P1-14 / R5-P2-01 | 工具/依赖前置完整，check 恢复前端 lint 与最终后端构建。 | **工程门禁未完成**；新减项为 P2 |
| P1-18 / R2-P1-04 | 枚举失败传播、NUL 记录边界、精确 schema 模式、独立 profile/日志/角色和对象断言。 | **P1 实验脚本阻断** |
| P1-19 / R5-P2-02 | 正确且有效的服务端资源采样、失败清理结果、完整错误留存、同 UID 独立进程边界；缺失工具不能报告 DONE。 | Spike 代码门禁未完成；新 RSS 误归属为 P2 |
| P2-04 / R5-P1-01 | 有超时地等待健康状态，失败清理；校验真实 JS/CSS。非空日期兜底已有，若要求真实构建日期应生成时间值。 | **新增 P1 CI 竞态**；资产为旧残留 |

已关闭且不再列 OPEN：**R3-P1-01、P1-06、P1-04、P0-02、R3-P1-03、R4-P2-01**。此前已 FIXED 项不因本轮环境无法执行而重开。

## 需要用户资源或决策的外部项

这些是既有外部待执行事项，不计新增发现，也不用于替代上述代码问题的修复。

1. **ADR-003 真实项目实验**：提供有代表性数据、Auth 用户、自定义角色、RLS、跨 schema FK 和声明支持扩展的源 Supabase 测试项目，以及可为各候选 profile 独立初始化的空目标；提供适当权限的直连或 Session pooler 连接方式。修好脚本后执行并把归档 TOC、独立 stderr、对象/权限/数据核对写入 ADR。
2. **可运行 Docker、socket 和联网工具安装的验证环境**：运行原样 race/HTTP 测试、serve 与 CLI 矩阵、慢 body 超时、compose、真实 runtime 镜像 healthcheck/资产/SIGTERM 和完整远程 CI。当前 socket/Docker 权限、离线安装限制均为已知环境约束，不要求据此修改产品代码。
3. **arm64 与 amd64 的发布形态验证资源**：在本提交对应 runtime-spike 镜像执行恢复/坏档/失败清理/资源峰值/信任边界，保留架构、镜像和 commit 标识。ADR-004 的历史记录不能替代本提交新增采样与 trap 的实测；此轮没有推翻原有信任边界决策。
4. **恢复范围与阶段安排决策**：根据真实结果确认完整 Supabase profile 或 beta 的“应用 schema 逻辑备份”范围；若需改变产物格式，按 ADR-003 由负责人决策。若要延期外部实验并允许阶段并行，应显式更新 DoD/ADR。本轮评审没有代为豁免，也没有把“尚未执行外部实验”当成新缺陷。

**最终判定：代码与工程门禁未通过；修复 OPEN 代码项后复核。外部资源补证单独跟踪。**
