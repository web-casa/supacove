# Phase 1 第四轮复审（再终验）

评审日期：2026-10-03。评审对象：`db42d83`；比较范围：`git diff ba8b495..db42d83`，覆盖全部 18 个变更文件。历史基线为第三轮报告 `docs/reviews/codex-phase1-review-3.md`。本文业务文件行号均指 `db42d83`。

**总体结论：未通过；仍阻塞 Phase 2。** 启动 guard 的 SQL/Go 迁移版本误判、logout 认证绕过、短 Origin panic、旧库 fixture 污染和 PHC 尾随字段问题已得到有效修复。但旧 v1 持锁 CLI 仍被放行，secret 持久化确认仍可被重试绕过，Spike 1 角色创建仍不可用；另有旧格式恢复点被删除的新回归。不能仅以新增测试通过判定 Phase 1 验收完成。

第三轮全部 OPEN 编号及其交叉编号共 **22 条判定记录：FIXED 10 / PARTIALLY_FIXED 8 / NOT_FIXED 2 / REGRESSED 2**。这些不是 22 个独立根因。新增根因编号为 **R4-P1-01、R4-P2-01**，分别关联 P1-08、P1-18，不重复计算缺陷。

判定口径：FIXED 表示对应缺陷关闭；PARTIALLY_FIXED 表示有有效修复但仍有第三轮要求的实质残留；NOT_FIXED 表示该条残留仍然存在；REGRESSED 表示本轮又引入相关故障。环境未验证不等于代码失败，也不等于验证通过。R3-P1-01 的部分修复判定依据是仍然放行待迁移 CLI，而非 socket 限制。

## 逐项判定

| 编号 | 判定 | 本轮证据、残留及验证边界 |
|---|---|---|
| R3-P1-01：启动 guard 回归及统一兼容性防线 | **PARTIALLY_FIXED** | `db/migrate.go:110` 使用的 goose 3.28.0 `GetVersions` 确实返回数据库版本与完整 SQL+Go source 的目标版本，实测当前库为 `(3,3)`，未来库为 `(999,3)`。真实新二进制的干净库、原版 v1、修改版 v1 均迁移至 v3、通过 guard，随后因沙箱禁止 socket 退出，**未取得真实监听成功结果**。当前 v3 持锁 CLI 成功；v999 的 serve、独占锁 CLI、持锁 CLI 均拒绝。**未修残留：** 原版及修改版 v1 持锁时，真实 `bootstrap` 仍退出 0、写入 token，版本仍为 1；待迁移库没有被拒绝。 |
| R3-P1-02：auth fast-path 绕过认证／CSRF | **FIXED** | fast-path 改为精确的匿名 method/path 白名单。真实 Router + SQLite、无 body 与 `{}` 两组：匿名 logout **401**，伪造／缺失 CSRF **403 JSON**，正常 **204**。保存退出前的原 cookie，直接查 `UserForSession` 已失效，重放 `/api/auth/me` 为 **401**；不是仅靠浏览器清 cookie 判断撤销。匿名未来 auth 路由为 401。 |
| R3-P1-03：Spike 1 角色 SQL 不可执行 | **NOT_FIXED** | 旧多层 SQL 已改写，但 `scripts/spike1-supabase-restore.sh:113` 的 shell `printf` 直接包含 `%I`，报 `invalid format character`，只输出 `SELECT format('CREATE ROLE `。把该实际输出送入 **PostgreSQL 18.4 单用户后端**，报 `unterminated quoted string`。普通、含单双引号角色名均在相同分支失败；换行角色名还被 `mapfile -t` 拆成两项。详情见下文。 |
| R3-P1-04：备份版本前缀字典排序 | **FIXED** | 实测 v5～v10 六份快照，保留 v10、删除 v5；五次失败迁移后再成功，历史 v1 与最新 v3 快照均保留。数字版本排序与同源版本重试去重已生效。另一个新缺陷是旧格式名称被错误合并，单列 R4-P1-01，不把原字典排序问题继续算作未修。 |
| R3-P2-01：短 Origin panic | **FIXED** | 已删除无长度保护切片。真实 Router 的 `Origin: /`、短串、尾部 `/ ? # :`、不匹配 IPv6 origin、`null` 均返回 **403**；标准同源值及显式默认端口通过 Origin 校验。原 server Origin 测试亦通过，无此次 panic。 |
| R3-P2-02：旧库 fixture 被全局迁移污染 | **FIXED** | `upgrade_test.go:47` 加 `WithDisableGlobalRegistry(true)`，并在两种旧库升级前断言版本与列状态。独立调用提交自带 helper，升级前为 **v1、无 generation 列**；修改版为 v1、有列。当前 0001 与 `git show 2388a46:.../0001_auth.sql` 逐字节一致。 |
| R2-P1-01：两类历史 v1 缺列／升级使用路径 | **FIXED** | 独立 fixture 直接取原版 `2388a46` 和修改版 `51d8275` 的 SQL，并禁用全局注册；两种库经真实二进制 serve 均升至 v3、有 generation 列并通过启动 guard。独立 Store 测试的登录、reset、旧 session 撤销、新密码登录均通过。真实 HTTP 监听仍受环境限制，不能写成活服务验收通过；旧 schema 的旁路 CLI 残留计入 P1-06。 |
| R2-P1-04：Spike 1 对象名 SQL 安全与可用性 | **PARTIALLY_FIXED** | 活跃行数 SQL 仍用变量与服务端标识符 quoting，含引号表名保持为一个标识符；角色分支增加 `ON_ERROR_STOP` 与创建后查询，方向有效，但 `%I` 被 shell 提前解释使创建失败。换行记录边界、schema pattern 转义仍未处理；未调用的旧 `count_rows()` 仍有拼接，应删除。未把死代码或失败的创建路径描述为已证实的可执行注入。 |
| P0-02：匿名认证资源约束 | **PARTIALLY_FIXED** | 要求的三项复测均通过：login **65 字节 username → 400**，129 字节 password → 400；30 个空 body 为 **400×5、429×25**；合法 JSON 尾部 `}`、`]garbage`、第二个 JSON 值均 **400**，追加 17,000 空格 **413**。未知长度 body 也验证了 400/413。**残留：** login 的 1 字节用户名＋1 字节密码仍进凭据/KDF 分支并返回 401，未执行契约下限；Unicode 长度不一致并入 P1-09。这是有界校验／契约残留，不是原来无界资源风险复发。 |
| P1-01：Origin 严格校验 | **FIXED** | 第三轮留下的短 Origin panic 已关闭，关联 R3-P2-01。原有 scheme、有效端口、跨源拒绝测试通过；未发现本轮新认证绕过。 |
| P1-02：会话持久化结果必须真实 | **FIXED** | logout 的非空 body 不能再跳过 session context 注入。保存旧 session 验证撤销成功；原数据库写入失败不得虚报成功的测试通过，关联 R3-P1-02。 |
| P1-03：重置与在途签发／升级交付 | **FIXED** | generation 条件签发及 reset 相关 race 测试通过；两种真实旧库升级后 reset/revoke/login 全链路通过；启动前版本误判已消除。无监听环境限制单独记录，不据此重开已证实修复的 generation 与缺列问题。 |
| P1-04：secret 原子生成与持久化 | **PARTIALLY_FIXED** | `config.go:177` 起对目录 Open、Sync、Close 错误均传播；16 路并发创建 `-race -count=100` 通过。**残留实证：** 可写可搜索、不可读目录（0300）中，第一次创建在目录 Open 返回错误，但 secret 已发布；目录权限不变，第二次调用从 `:127` 直接返回 32 字节 key 成功，仍未完成目录 fsync。EEXIST 并发读取也走该分支。没有实际注入 Sync 的 EIO 或断电，详见下文。 |
| P1-06：CLI 与 schema 兼容性 | **PARTIALLY_FIXED** | 两条 CLI 路径现在均拒绝 v999，正常 v3 持锁 CLI 可用。`EnsureFreshSchemaForCLI` 仍只检查 `SchemaReady > 0` 加 `current > target`，不检查 pending。真实持锁 v1 CLI 成功写入，第三轮明确要求的“初始化／升级未完成时拒写”未满足。 |
| P1-08：迁移备份与恢复点保留 | **REGRESSED** | 失败重试不挤占历史源版本、数字排序已修；但新去重会删除旧时间戳格式恢复点，见 R4-P1-01。失败产物隔离仍未修：对 64 MiB SQLite 备份在目标创建后取消，`BackupNow` 返回 `context canceled`，留下 **0 字节正式 `pre-migrate-v3-….db`**。仍无目标版本／完成状态，不能把正式名称当成完整备份的证明。 |
| P1-09：OpenAPI／运行时契约 | **PARTIALLY_FIXED** | logout 缺 CSRF header 现在在 guard 返回 **403 application/json**，不再是 400 text/plain；`HandlerWithOptions.ErrorHandlerFunc` 也统一生成层错误。logout storage 503 已补进 spec 与 Go/TS 生成物；JSON EOF 检查与上限已修。**残留：** login 下限未执行，密码按字节计数：4 个汉字 bootstrap 201，50 个汉字 bootstrap/login 400；spec 的 min/maxLength 是字符限制。关闭 SQLite 后 `/api/health/details` 实际 **503 JSON**，spec 仍只有 200/401。 |
| P1-14：embed 占位与干净构建 | **PARTIALLY_FIXED** | `make frontend` 新增 `touch .../.gitkeep`。隔离副本中完整复制步骤执行后标记存在且 0 字节，生产 JS/CSS 构建成功。普通 npm ci 的 esbuild 安装校验受沙箱 EPERM 限制；离线缓存、禁用安装 hook 后 `make frontend` 成功，未伪称默认 CI 安装通过。**仍未修：** 无 node_modules 的 archive 上 `make api-check` 报 `openapi-typescript: not found`；`make check` 的 npm ci 仍排在 api-check 之后，且无 Go 生成器安装前置。 |
| P1-18：Spike 1 实验有效性 | **REGRESSED** | 两端行数都失败现在最终退出 1，原假成功的一条路径已修，尽管日志仍打印 match。**旧残留：** 表枚举失败仍生成两个空文件、打印 match、退出 0；角色创建仍失败，profile 共用目标、仅 NOLOGIN 占位、无各 profile 独立权限/RLS/FK/TOC 断言、stderr 覆盖、URI 含密码进入 argv、记录分隔与 schema pattern 问题仍在。**新增：** 正常对象名含 `FAIL` 时被误判计数失败，见 R4-P2-01。 |
| P1-19：两项 spike 的 DoD | **PARTIALLY_FIXED** | Spike 2 新增 pg_restore 的 `/proc/PID/status` VmHWM 采样、停止后的进程／目录断言；这是有效进展。**残留：** 只采样客户端，不覆盖临时 PG 服务端，也未要求获得有效采样；提前失败没有 trap 清理及断言，独立进程同 UID 文件／进程边界未验证；setpriv 不可用仍 SKIPPED+exit 0；坏档固定截取 200,000 字节、丢弃 stderr 的证据缺口仍在。ADR-003 仍待真实项目，ADR-004 未补此次变更的实测数据。环境限制不是新发现，详见实测与限制。 |
| P2-01：API 默认拒绝与 CSRF | **FIXED** | 非白名单 auth 请求继续验证 session/CSRF。匿名未来 auth POST 为 401；已登录且真 CSRF 为 JSON 404。logout 两种 body 的真／假／缺 CSRF 矩阵通过，关联 R3-P1-02。 |
| P2-02：PHC 完整格式解析 | **FIXED** | `password.go:61` 起的全字段正则关闭 Sscanf 前缀接受问题。实测合法 hash 可验证，改成 `v=19junk`、`p=1,unknown=9` 均被拒；超大 p 参数也拒绝。原格式、参数与长度负例测试通过。 |
| P2-04：工具链、元信息与镜像门禁残留 | **NOT_FIXED** | 本轮没有调整 CI：PR 的 BUILD_DATE 仍仅取 `github.event.head_commit.timestamp`；smoke 仍未断言实际 healthcheck 状态、嵌入资产、SIGTERM 或双架构。启动 guard 修复已计入 R3-P1-01。YAML 再次可解析、runtime target 保留，不等于远程 CI 或容器 smoke 已通过。 |

## 关键残留与新增回归

### R4-P1-01：旧格式备份被当成同一个版本删除

位置：`backend/internal/db/backup.go:64–76、90–98`。这是本轮新增，关联 P1-08。

不匹配 `pre-migrate-v%d-%s` 的所有文件都被加入 `version=-1, timestamp="9999"`，随后按 version 去重，并最终删除所有不在 keep 集合中的路径。注释说“无法分类就保留”，实际没有跳过删除。Glob 按名称排序，时间戳又被统一替换，结果保留的是该组最先遇到的旧快照。

独立调用真实裁剪函数得到：

| 输入 | db42d83 的实际结果 |
|---|---|
| 两份旧格式 `pre-migrate-20260101T….db`、`pre-migrate-20260102T….db` | 即使总数小于五份，也删除较新的 20260102，只保留 20260101。旧实现小于等于五份时不裁剪。 |
| 上述两份 + 五个不同版本的新格式快照 | 两份旧格式全部被删除。 |
| v5～v10 六份新格式 | v10 正确保留，v5 删除。 |
| 历史 v1 + 五次失败的 v3→v4 迁移，随后修正迁移并成功 | 历史 v1 保留，仅留最新 v3 重试快照。 |

旧格式是此前提交实际生成的名称，不是虚构损坏输入。修复应将无法分类的文件排除出自动删除集合，或正确解析旧格式时间并定义迁移保留规则；不能把全部未知文件合成一个“升级批次”。补覆盖旧格式混存及少于五份的回归测试。**恢复点丢失，阻塞 Phase 2。**

### R3-P1-03 残留：角色 SQL 在到达数据库前已被 printf 截断

位置：`scripts/spike1-supabase-restore.sh:113`。

实际执行的 shell 格式串为：

```bash
printf "SELECT format('CREATE ROLE %I NOLOGIN', :'role');"
```

Bash 把 `%I` 当成自己的格式指令，退出 1，输出仅为 `SELECT format('CREATE ROLE `。真实 PostgreSQL 18.4 单用户后端解析该输出报：

```text
ERROR: unterminated quoted string at or near "'CREATE ROLE " at character 15
STATEMENT: SELECT format('CREATE ROLE
```

完整脚本的受控 psql 替身分别提供普通、含单双引号、含换行的角色名，前三种输入路径均退出 1；换行名称在枚举阶段就被拆分。这里的脚本控制流测试是 mock，SQL 解析是实际 PG18；**没有把 mock 当成真实 Supabase 恢复，也没有把单用户后端退出码 0 当成 SQL 成功**。

可将 SQL 作为 `printf '%s'` 的参数，或把 `%I` 转义为 `%%I`；同时修复记录边界。继续使用 `:'role'` 与服务端 `format('%I')`，在真实可监听 PG18 上覆盖不存在／已存在、单双引号／换行角色，并直接查询 `pg_roles` 验证。目标角色全部已存在只会跳过坏分支，不能证明创建路径可用。**现行 Phase 1 spike 前置条件仍被阻断。**

### R4-P2-01：把正常对象名中的 FAIL 当成计数失败

位置：`scripts/spike1-supabase-restore.sh:195–197`。这是本轮新增，关联 P1-18。

新增 `grep -q "FAIL"` 搜索整份记录，包含 schema／table 名。受控工具返回源、目标计数均为 `public.FAIL_invoices:7`，全部候选恢复返回成功，脚本却打印 `COUNT ERRORS PRESENT` 并退出 1。换成 `public.widgets:7` 则退出 0。应在命令失败处记录独立状态，校验数字结果，同时捕获表枚举退出码；不能从对象名与计数拼成的文本推断执行状态。该 P2 单项不独立阻塞 Phase 2，但应与 Spike 1 现有错误判定残留一起修复。

### P1-04／P1-06：成功返回仍不代表前置条件已完成

- **secret：** 对目录 Open/Sync/Close 的直接错误传播已修。无读权限目录中首次创建返回错误，但硬链接已经发布；再次读取直接成功，没有补做目录持久化确认。并发 EEXIST 分支递归读取也同样绕过。实测使用自建 0300 目录，没有修改生产文件；此次未注入 fsync EIO 或模拟断电。应让新发布文件的重试／并发消费者也完成所要求的目录同步确认，保留可恢复的同一 key。
- **CLI：** provider 的 `current > target` 是未来版本防线，不是“全部迁移完成”检查。真实 flock 持有 v1 数据目录时，CLI bootstrap 返回 token 并新增记录，generation 列仍缺失。应在持锁旁路拒绝 pending／未就绪 schema，并覆盖原版 v1、修改版 v1、当前 v3、未来 v999；不得由旁路 CLI 自行迁移。

## 本轮重点回归检查

| 变更点 | 结论 |
|---|---|
| provider `GetVersions` 语义 | 查阅本地锁定版本源码 `github.com/pressly/goose/v3@v3.28.0/provider.go:518`：target 来自 provider migrations 最大版本，包含注册 Go 迁移；current 来自数据库 store。独立实测与二进制矩阵一致。`Migrate` 仍先判断 IsFresh，未因此将新库误判为需备份。没有发现这个 API 本身引入新的版本上限回归；遗漏 pending 检查是延续的 P1-06。 |
| 匿名限流移动 | 精确匹配 POST login/bootstrap；零长度 body 也先计入 Allow，解码返回 400 会计入 Fail；畸形 JSON 在 readWholeJSON 返回后仍受固定窗口约束。30 次空 login 为 5/25，畸形 login 为 10/20。原限流与 KDF 并发测试通过。媒体类型／Origin 提前拒绝不进入 KDF，不将其当作本轮新的资源绕过。 |
| `readWholeJSON` 移动与 EOF 检查 | login/bootstrap 的尾随内容、未知长度和大 body 均有确定性拒绝结果。受保护路由不再被 body 分支提前放行。logout 契约未声明 requestBody，生成 handler 不读 body；真 session+CSRF 下附加垃圾或大 body 会被忽略并返回 204，已记录此行为边界，不能误报为认证绕过。未来有 JSON body 的受保护接口需显式采用完整读取／验证，不应依赖这个匿名分支。 |
| `HandlerWithOptions` 切换 | 缺 CSRF header 为 JSON 403；匿名未知 API 401，已认证未知路由 JSON 404、错误方法 JSON 405，cookie 写入和正常 logout 保持正确。Go/TS 生成物重生成与提交一致。没有发现新路由／参数错误格式回归；health/details 503 等既有契约残留仍在。 |
| 备份与 spike 脚本 | 数字排序和重试去重通过，但出现 R4-P1-01；角色分支仍失败，计数错误处理出现 R4-P2-01。`bash -n` 不能发现本次 `%I` 运行时格式错误。 |

## 实测记录与环境限制

仓库只新增本报告；业务实现、提交自带测试、生成物均未修改。隔离副本为 `git archive db42d83`，位于 `/tmp/codex-phase1-review-4/repo`，探针和日志在 `/tmp/codex-phase1-review-4/`。日志是本地复核证据，未作为受跟踪产物提交。

原生 HTTP 测试因沙箱禁止 socket 失败。隔离副本仅将测试 transport 改为真实 Router + `httptest.ResponseRecorder`，保留 cookie jar、RemoteAddr、服务端非 nil body 语义，并补测试 Store cleanup；业务实现未改变。原测试及新增探针用 `-race -count=1` 运行通过。**部分探针断言的是已观察到的坏行为，探针 PASS 不代表该验收项通过。**

| 检查 | 实际结果／证据 |
|---|---|
| 原始后端测试 | 设置可写 GOCACHE 后，auth/config/db/limiter 通过；server 在首个 httptest listen 报 `socket: operation not permitted`，全命令退出 1。`logs/original-race.log`。 |
| 隔离 Router／Store 测试 | 原测试与 Review4 探针 `go test -race -count=1 -v ./backend/...` 通过；含真正 v1 升级、generation/reset、logout 原 session 重放、Origin、输入边界、PHC、版本排序、旧格式裁剪。`logs/isolated-race.log`。额外 chunked/503、取消备份测试分别见 `additional-http.log`、`backup-cancel.log`。 |
| 真实二进制矩阵 | `go build -buildvcs=false` 成功；只关闭 archive 缺 Git 元数据时的 VCS stamping。三种库 serve 通过迁移与 guard，均在实际 listen 被环境拒绝，**不是已启动服务**。正常 v3 持锁 CLI 成功；v999 三路径拒绝；旧 v1 持锁 CLI 错误放行。`logs/binary-matrix.log`。 |
| 静态／并发检查 | `go vet ./backend/...`、`git diff --check ba8b495..db42d83`、两个脚本 `bash -n` 通过；secret 16 路并发创建 `-race -count=100` 通过。`logs/secret-repeat.log`。 |
| 生成物、前端、embed | oapi-codegen 与 openapi-typescript 重生成的 Go/TS 均逐字节匹配 db42d83；npm lint、build 成功。正常离线 npm ci 在 esbuild 安装校验处 EPERM；禁用安装 hook 后离线 `make frontend` 成功，真实复制步骤保留 `.gitkeep`，随后带资产的 Go 构建成功；JS/CSS 经真实 Router 检查。`logs/frontend.log`、`frontend-no-install-hooks.log`、`assets.log`。不宣称原样联网 npm ci 或 CI 通过。 |
| 干净 api-check | 安装依赖前实测失败：`openapi-typescript: not found`。`logs/api-check-clean.log`。这是 Makefile 的既有前置顺序缺陷，区别于联网安装不可执行。 |
| Spike 1 | 真 Bash `%I` 格式错误；捕获输出经实际 PG18.4 单用户后端解析失败，`logs/pg-role-actual.log`。受控脚本测试中两边 count 失败最终退出 1，表枚举失败仍退出 0，正常含 FAIL 名称退出 1，`logs/spike1-probes.log`。未连接真实 Supabase 项目。 |
| Spike 2 实际执行 | 真脚本在 docker run 即因 Docker socket 权限失败，`logs/spike2-real.log`；sudo 亦受 no-new-privileges 限制。未实测本提交容器的 RSS、实际 PG 恢复、双架构或边界隔离。 |
| Spike 2 控制流探针 | 替身运行实际内层 shell，固定 `/tmp` 路径改到隔离目录：正常流程到达新增清理断言；注入首次 restore 退出 9，脚本退出 9，停止／删除／断言均未执行，模型的 running 标记与 pgdata 留存；setpriv 不可用时仍打印 SPIKE2_DONE 并退出 0。`logs/spike2-probes.log`。这是流程证据，**替身的 RSS 不是 PostgreSQL RSS，替身残留也不是实际容器泄漏证据**；真 `docker --rm` 会清理容器，缺口是失败路径没有完成所声称的清理验证。 |
| CI／外部实验声明 | CI YAML 再次可解析，backend/contract/frontend/image 四个 job 保留。没有运行远程 CI、Docker 镜像 smoke 或 Supabase profile。ADR-003 明确写“待执行”，README 仍是 Phase 1 开发中；第三轮报告已明确外部执行限制。ADR-004 是此前维护者提供的历史实测记录，本轮 diff 未补新数据，不能当作 db42d83 重跑证据。 |

**已知环境限制已诚实标注：** 无真实 Supabase 项目、无可用网络 CI，以及本沙箱的 socket／Docker 限制，均不列为新发现。本次没有把不可执行写成实验失败，也没有把历史 ADR 或 mock 写成当前真实验证成功。真实 serve 的 HTTP 健康检查、慢 body 超时、镜像实际 healthcheck/SIGTERM、双架构 Spike 2 仍须在可执行环境补证。

## 仍然 OPEN 的清单及 Phase 2 决策

按根因合并编号，避免将关联记录重复计算。

| OPEN 项 | 必须完成的工作 | 是否阻塞 Phase 2 |
|---|---|---|
| R3-P1-01／P1-06 | 持锁旁路拒绝 pending／旧 schema；补真正可监听环境的 serve/CLI 矩阵。启动版本误判已修，不再按该旧根因阻断。 | **是**：尚有明确要求的旧 schema 拒写缺口；监听补证是环境待验证项。 |
| P1-04 | secret 发布后的重试／并发读取也完成持久化确认；补目录同步失败验证。 | **是**：进入凭据加密前必须闭合 key 持久化契约。 |
| P1-08／R4-P1-01 | 旧格式恢复点不得错误去重／删除；失败 VACUUM 产物隔离，完整备份再发布正式名称。 | **是**：恢复点删除与失败备份混入正式集合。 |
| P0-02／P1-09 | 统一 login 下限与 Unicode 字符语义；补 health/details 503 契约。三个指定的匿名资源探针已通过。 | 当前残留**不单独视为 P0 安全阻断**；Phase 1 契约任务未完成，应在扩展 API 前关闭。 |
| P1-14 | api-check/check 对 Go 生成器、前端依赖提供干净构建前置；`.gitkeep` 删除问题已关闭。 | **是，工程门禁**：干净 checkout 的承诺检查仍确定性失败。 |
| R3-P1-03／R2-P1-04／P1-18／R4-P2-01 | 修正 shell `%I`、名称记录边界与 pattern 转义；捕获枚举失败、独立计数状态；各 profile 使用干净目标并有完整断言和独立日志，完善角色权限恢复。 | **是，按现行 Phase 1 DoD**：当前脚本不能可靠证伪／证实 profile。R4-P2-01 单项不独立阻断。 |
| P1-19 | 补覆盖 PG 服务端的资源证据、失败清理与同 UID 独立进程边界验证；不得以 setpriv SKIPPED 宣称完成；完善坏档证据。真实 Supabase／双架构实验另列待执行。 | **现行 DoD 尚未满足**。代码／证据缺口独立于环境；没有负责人调整 DoD 的记录，不擅自视为豁免。 |
| P2-04 | PR build date 备用来源；完整镜像 smoke 和双架构门禁证据。 | P2 残留**不单独阻断**，但不能宣称 CI／发布验收已完成。 |

上述缺陷可在没有真实 Supabase、没有网络 CI 的条件下复现，环境限制不能解释或关闭它们。即使暂不计外部实验待执行项，旧 schema CLI、secret 持久化、备份裁剪及角色 SQL 等仍足以否决本轮验收。

**最终结论：Phase 1 验收未通过。** 修复明确阻断项并补齐相应验证后再验收；本轮不判定为“修改后通过”。
