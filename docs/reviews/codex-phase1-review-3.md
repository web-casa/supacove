# Phase 1 第三轮复审（终验）

评审日期：2026-10-03。修复基线：`ba8b495`；比较范围：`git diff 51d8275..ba8b495`，已检查全部 27 个变更文件，包括生成物、测试、容器和脚本。历史迁移直接取自 `git show 2388a46:backend/internal/db/migrations/0001_auth.sql`。本文业务代码行号均指 `ba8b495`。

**总体结论：未通过；阻塞进入 Phase 2。** 新增版本检查使所有正常新库、升级库在 `serve` 监听之前退出；认证中间件的新分支绕过会话与 CSRF 校验，使带 JSON body 的 logout 虚报成功而不撤销会话。另有 Spike 1 角色 SQL 和备份排序回归。不能把编译成功、内存 Router 测试通过或 ADR 中既有记录当作交付验收通过。

第二轮 **17 条未决记录（含关联的原编号）**：**FIXED 3 / PARTIALLY_FIXED 10 / NOT_FIXED 1 / REGRESSED 3**。此外，原已 FIXED 的 **P1-02、P2-01 被重新打开**。本轮新增根因编号 **R3-P1-01～04、R3-P2-01～02**，与原编号交叉引用，不重复计算独立缺陷。

判定口径：FIXED 为所述问题已关闭；PARTIALLY_FIXED 为已有有效修复但验收条件仍有实质残留；NOT_FIXED 指第二轮指出的残留未获修复，不否认更早轮次的进展；REGRESSED 指修复引入新的相关故障。环境无法执行的项目单独列为未验证，不冒充成功，也不单凭环境限制判代码失败。

## 逐项判定

### 第二轮新增问题及关联回归

| 编号 | 判定 | 证据与仍需处理的部分 |
|---|---|---|
| R2-P1-01：旧库迁移缺列 | **PARTIALLY_FIXED** | `0001_auth.sql` 已恢复到 `2388a46` 的逐字节原文；新增 `migrations/0003_auth_generation.go:19` 条件加列。真实原版 v1、修改版 v1 经新二进制 `bootstrap` 内的 Migrate 均升到 v3；独立 Store 探针的登录、重置、旧 session 撤销和新密码登录均通过。但同一新二进制 `serve` 均因“v3 newer than binary v1”退出，完整升级使用路径未关闭，见 R3-P1-01；提交自带升级测试另有 R3-P2-02。 |
| R2-P1-02：CI YAML 非法 | **FIXED** | `.github/workflows/ci.yml:82` 的步骤名已引用；Python/PyYAML 6.0.3 `safe_load` 成功，解析出 backend/contract/frontend/image 四个 job。只能确认语法修复，不能宣称远程 CI 全绿；镜像服务启动仍受 R3-P1-01 阻断。 |
| R2-P1-03：实验 stage 混入发布 | **FIXED** | `compose.yaml:12`、CI `:76` 均显式 `target: runtime`，Dockerfile 顶部构建示例也指定 `--target runtime`；`docker compose config --format json` 确认 target。此修法满足第二轮“全部发布入口显式指定 target”的要求。最后 stage 仍是 runtime-spike，裸 `docker build .` 仍构建它；不能将修复描述为默认 stage 顺序已改变。本环境无法构建镜像，未实证 runtime 的实际包集合。 |
| R2-P1-04：Spike 1 对象名 SQL 注入 | **PARTIALLY_FIXED** | 活跃行数查询改用 psql 变量和数据库 `format('%I')`；恶意含引号表名的 mock 捕获显示其被保留在单一标识符内，原拼接逃逸路径已消除。角色路径也停止直接拼接 `$r`，但新生成 SQL 对普通角色名就语法错误，未形成可用的安全角色恢复路径，见 R3-P1-03。对象枚举仍按换行拆分，错误处理仍不完整。未再次证实当前活跃路径存在可执行注入；未调用的旧 `count_rows()` 中仍有不安全拼接，应删除，不能拿死代码充当当前可达漏洞。 |
| R2-P2-01：KDF busy 用户名泄漏 | **FIXED** | `auth/store.go:203` 未知用户分支传播 acquireKDF 错误。确定性占满真实 4 个 slot 后，已知和未知用户名均返回 `ErrKDFBusy`；同一 HTTP handler 分支将两者映射到 503。无 slot 时的 401/503 状态码 oracle 已关闭。 |
| P1-03：重置与在途签发／升级交付 | **PARTIALLY_FIXED** | generation 条件签发逻辑保留；真实两种旧库迁移后的 reset、旧 session 撤销、新登录通过。缺列已修，但新二进制启动回归仍阻断升级后的实际使用；同 R2-P1-01、R3-P1-01，不另算一个根因。 |
| P2-04：工具链、元信息及镜像门禁 | **PARTIALLY_FIXED** | Go 版本、CI YAML、发布 target 已修，CI 增加 BUILD_DATE 参数。但值只取 `github.event.head_commit.timestamp`，pull_request 没有备用来源；smoke 仍只验证 UID、healthz、bootstrap，未验证真实 healthcheck 状态、嵌入资产、SIGTERM 和双架构。当前服务启动必败，镜像 smoke 不能通过；YAML 可解析不代表门禁完成。 |

### 第二轮 PARTIALLY_FIXED 项的残留复核

| 编号 | 判定 | 本轮有效修复与明确残留 |
|---|---|---|
| P0-02：匿名认证资源约束 | **PARTIALLY_FIXED** | bootstrap 现已在解码前限流：30 次无效 token 得到 **403×5、429×25**；畸形 login 得到 **400×10、429×20**，固定窗口生效。完整 body 经 ReadAll 和 16 KiB 上限，合法 JSON 加 17,000 空格现为 413；`main.go:176` 增加 30 秒 ReadTimeout。**残留：** login 仍仅检查非空，65 字节用户名、129 字节密码、1 字节短字段均进入 KDF／凭据判断而返回 401；新 spec 上限未变成运行时上限。`ContentLength==0` 跳过限流，30 个空 login 全为 400。`Decoder.More()` 不能证明 EOF，尾随 `}`、`]garbage` 仍可登录 200。原无界 KDF 风险已有实质缓解，不将这些残留描述为同等规模的新内存耗尽漏洞；编号沿用原 P0。慢 body 超时只有代码证据，本次未做可监听服务的耗时实测。 |
| P1-01：Origin 严格校验 | **REGRESSED** | 第二轮的空端口、尾随 `?/#`、重复 Origin 均实测返回 403。但 `httpx.go:38` 新增无长度保护的 `origin[8:9]`；`Origin: /` 触发 panic，经 recoverer 返回 500，见 R3-P2-01。没有证明浏览器跨站绕过，当前新增问题属 P2 健壮性问题。 |
| P1-04：secret 原子生成与持久化 | **PARTIALLY_FIXED** | `config.go:145` 临时文件写完、Sync、Close 后以硬链接无覆盖发布；16 路并发原测试 `-race -count=100` 通过，第二轮的半文件读取故障未再出现。**残留：** `:175` 的目录 Open 失败和 `:176` 的目录 Sync 失败均被吞掉，函数仍返回成功；未完成“目录持久化失败就拒绝继续”的错误路径。需传播错误并使并发读取／重试路径也能完成持久化确认。本条失败路径为代码审查证据，未宣称已注入断电或磁盘错误。 |
| P1-06：CLI 与 schema 兼容性 | **REGRESSED** | 新检查只数嵌入 SQL 文件，漏掉注册的 Go v3：正常 v3 被判不兼容，服务和持锁旁路 CLI 被拒，见 R3-P1-01。**其他残留：** 旧 v1 缺 generation 仍通过 `EnsureFreshSchemaForCLI`；只检查过新版本，未检查待迁移版本。`main.go:107` 的独占锁分支迁移后不做兼容性检查，实测无服务持锁的 v999 库执行 `bootstrap` 退出 0 并生成 token，仍允许旧二进制业务写入。`SchemaReady` 仍仅检查版本大于零。 |
| P1-08：迁移备份与恢复点保留 | **PARTIALLY_FIXED** | 备份名加入源版本；裁剪移到升级成功后，连续五次失败迁移期间历史恢复点仍在。**残留：** 成功后仍按文件数量保留五份，不按版本／升级批次去重；五次失败后第六次成功会删除历史 v1 恢复点，留下五份同为 v3 的重试快照。没有目标版本／完成状态，失败 VACUUM 仍直接写正式 `.db` 名称、无失败产物隔离。新增版本前缀还破坏原字典排序的时间语义，v10 可先于 v5～v9 被删除，见 R3-P1-04。 |
| P1-09：OpenAPI 与运行时契约 | **PARTIALLY_FIXED** | 无 Content-Type、application/jsonp 均改为 415；普通尾部垃圾／第二 JSON 值为 400；spec 补 username pattern、长度、login Origin 403、me storage 503、logout CSRF header。**残留：** login 未执行长度校验；密码仍按字节而非字符计数，4 个汉字可 bootstrap 201，50 个汉字却 400；末尾额外 `}`／`]garbage` 仍接受。logout、health/details 的 storage 503 仍未声明。新 header 参数错误走生成器默认 `http.Error`：logout 携带 `{}` 而缺 CSRF header 返回 **400 text/plain**，非契约 JSON Error。 |
| P1-14：占位／生产 embed 与干净构建 | **PARTIALLY_FIXED** | 已删除受跟踪的生产 index 和 tsbuildinfo，新增 `.gitkeep`；干净 archive 的实际 Router `/` 返回 503，旧的 HTML 假资产问题已消除。前端构建后嵌入的 JS/CSS 均返回 200 且 MIME 正确。**残留：** `Makefile:20` 删除整个 dist，`:21` 复制的产物不含 `.gitkeep`，真实构建仍删除受跟踪标记；隔离副本复现。`api-check` 仍无工具／npm 安装前置，干净 archive 实测报 `openapi-typescript: not found`；`make check` 中 npm ci 排在该失败点之后。未完成承诺的干净构建门禁。 |
| P1-18：Spike 1 实验有效性 | **REGRESSED** | 引号对象名的活跃行数 SQL 有改进，但普通自定义角色就触发新增 SQL 语法错误、退出 1（R3-P1-03）。**第二轮残留均须继续处理：** 三 profile 仍共享目标；仅 NOLOGIN 占位且不恢复角色属性／GRANT；没有每 profile 独立权限、RLS、FK、TOC 断言；stderr 被覆盖；schema/role/table 换行分隔仍错误、schema pattern 未转义；密码 URI 仍进入 argv。新“FAIL”标记没有修正最终结论：源／目标计数都失败时两个文件同为 `public.widgets:FAIL`，仍输出 match 并退出 0；表枚举失败、两个空结果也输出 match 并退出 0。 |
| P1-19：两项 spike 的 DoD | **PARTIALLY_FIXED** | Spike 2 先 drop table 再测坏档，消除了已存在对象导致的已知假阳性；故障注入令第二个 canary 容器退出 4，脚本现退出 4，且不输出 SPIKE2_DONE。**残留：** setpriv 不可用仍 SKIPPED+exit 0；无峰值 RSS、独立验证进程的同 UID 文件／进程边界证据、失败清理断言；坏档 stderr 丢弃，仍不能严格排除其他恢复错误。固定 head 200000 也不保证小归档被截断。ADR-003 仍“待执行”，真实 Supabase profile 实验没有结果；ADR-004 沿用旧记录，没有本提交重跑证据；dev-plan 的 Phase 1 DoD 未获正式调整。本环境 Docker 不可用，未重新认定双架构 spike 通过。 |
| P2-02：PHC 完整格式解析 | **NOT_FIXED** | `auth/password.go:66,70` 本轮未变；实测合法 hash 改为 `v=19junk` 或 `p=1,unknown=9` 后，原密码仍验证成功。第二轮要求的完整字段消费／明确支持的 profile 未完成；此前参数与长度边界防护仍在。 |

### 已关闭项的重新打开

| 编号 | 判定 | 原因 |
|---|---|---|
| P1-02：会话持久化结果必须真实 | **REGRESSED** | logout 带 `{}` 绕过 guard 后没有 session context，直接清 cookie、返回 204，原服务端 session 仍有效；见 R3-P1-02。原来数据库 DELETE 报错时返回 500 的逻辑仍在，但该路径现在可以被完全绕过。 |
| P2-01：API 默认拒绝与 CSRF | **REGRESSED** | `/api/auth/` 下非空 body 的状态变更请求在认证之前直接进入 handler。匿名 logout 配任意非空 X-CSRF-Token 就返回 204，未来同前缀受保护路由也会继承问题；按根因 R3-P1-02 以 P1 处理。 |

其余第二轮已 FIXED 项在本次变更范围和回归检查中未发现新问题；没有重新执行所有外部部署及浏览器端到端验收。

## 新增回归与修复要求

### R3-P1-01：Go 迁移未计入最大版本，正常数据库一律无法启动

**位置：** `backend/internal/db/db.go:46`；`db/migrate.go:122`；`backend/cmd/supabackup/main.go:161`。

`embeddedMigrations` 只嵌入 `migrations/*.sql`，因此 `MaxEmbeddedMigrationVersion()` 得到 1。Go v3 则通过 import/init 注册到 goose，实际迁移成功后数据库版本是 3。新启动 guard 用这两个不同来源的版本集合比较，拒绝本二进制自己刚迁移的库。

**实测：** 干净数据目录、真正原版 0001 库、修改版 0001 库的 `serve` 均退出 1：

```text
error: database schema (v3) is newer than this binary (v1); refusing to start with a newer database
```

该错误在监听之前发生，**与本环境禁止 socket 无关**。另用真实 flock 模拟运行中的服务持锁，同版 CLI 被同一误判拒绝。独占锁 CLI 则缺少检查，v999 仍可写。

**修复与验证：** 用实际 migration provider 的完整 SQL+Go 迁移集合计算兼容性，统一 serve 和两条 CLI 路径；迁移前拒绝未来 schema，持锁旁路 CLI 拒绝未完成升级的旧 schema。加入新库、原 v1、修改版 v1、当前 v3、未来 v999 的真实二进制启动／CLI 矩阵，不能只测 Store.Migrate。

### R3-P1-02：JSON 读取分支扩大匿名放行范围，logout 不撤销 session

**位置：** `backend/internal/server/server.go:258,268,275`；`server/api.go:136`。

新分支匹配所有 `/api/auth/` 状态变更请求，只要 body 非空便 `next.ServeHTTP` 并 return，绕过下面的匿名白名单、session 查询、CSRF 校验与 session context 注入。

**实测：** 真实 Router、真实 SQLite session 下，已登录用户发送：

```http
POST /api/auth/logout
Content-Type: application/json
X-CSRF-Token: forged

{}
```

返回 204，清除浏览器 cookie，但保存的原 session 再经 `UserForSession` 查询仍有效。无 cookie 的相同请求也返回 204。未知 `/api/auth/future-protected` 的匿名 POST `{}` 到达路由得到 404，证实默认拒绝边界被穿过；不声称当前存在尚未实现的账户接管接口。Origin 检查仍在，亦不把此结果夸大为已证实的浏览器跨站攻击。

**修复与验证：** 仅对明确的 POST login/bootstrap 提前匿名放行；body 处理完成后，其他请求继续通过认证和 CSRF。覆盖 logout 无 body／非空 body、已登录／匿名、真／假／缺失 CSRF，并保存旧 cookie 确认退出成功后不能继续使用；同时给未来 auth 路由保留默认拒绝测试。

### R3-P1-03：Spike 1 新角色 SQL 对普通角色名即语法错误

**位置：** `scripts/spike1-supabase-restore.sh:112`。

捕获脚本实际 printf 输出，并把 psql 变量安全替换为普通角色字面量后，得到：

```sql
SELECT format('SELECT CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %L) THEN '-- exists' ELSE format('CREATE ROLE %I NOLOGIN', %L) END', 'review_role', 'review_role');
```

内层引号未按 SQL 字符串规则转义，`--` 进入注释。**本地 PostgreSQL 18.4 `postgres --single` 实际解析报 `syntax error at end of input`**，无需 Supabase 或网络即可确认。单用户后端进程退出码不能代表 SQL 成功，本结论依据其 ERROR/STATEMENT 日志。脚本 mock 控制流另确认空 stage_sql 导致退出 1。

即便只修引号，外层 format 还有内层 `%I` 未转义、参数数目不匹配，以及生成 SELECT 再返回 CREATE 文本而未执行最终 DDL 的层次问题，不能靠多补一个反斜杠关闭。

**修复与验证：** 简化为一层服务器端安全生成 DDL 并显式执行，使用 `ON_ERROR_STOP`，覆盖角色已存在／不存在、引号／换行角色名，并查询目标 `pg_roles` 验证结果。不能把命令退出 0 当作角色已创建。

### R3-P1-04：备份名增加版本前缀后，字典排序会先删新恢复点

**位置：** `backend/internal/db/backup.go:44,49,84`。

原排序依赖文件名开头的时间戳。本轮改成 `pre-migrate-v%d-时间` 后仍直接用 Glob 的字典顺序，`v10` 排在 `v5` 前。文件系统探针放入 v5～v10 六个恢复点，调用真实 prune 后 **v10 被删除，v5～v9 保留**。这是合法未来版本名称的确定性复现，不声称当前产品已经发布了 v10。

**修复与验证：** 按结构化版本、升级批次和明确时间排序／保留；失败重试不得占满成功升级恢复点配额。覆盖多位版本号以及“多次失败后成功”场景，后者本轮实测仍会删除历史恢复点。

### R3-P2-01：短 Origin 触发越界 panic

**位置：** `backend/internal/server/httpx.go:38`。

`origin[8:9]` 对短于 9 字节、以 `/` 结尾的值越界。真实 Router 发送 `Origin: /` 得到 500；recoverer 捕获 panic，进程未退出。该路径在限流之前，还会生成带 stack 的错误日志。没有认证绕过证据。

**修复与验证：** 删除无必要切片，直接验证允许的原始语法；对短字符串、分隔符、IPv6、标准 Origin 做边界测试，非法输入稳定为 403 且不 panic。

### R3-P2-02：新增“旧库升级测试”在准备旧库时已运行新迁移

**位置：** `backend/internal/db/upgrade_test.go:44,48,70`。

goose v3.28.0 Provider 默认合并全局注册的 Go migration。本测试包已导入注册 v3 的 migrations 包，因此 `buildLegacyDB()` 即便只传入旧 0001 文件，也同时运行 v3。

**实测：** 调用提交自带 helper 后、尚未调用被测 Store.Migrate 时，数据库已是 **v3，auth_generation 列已存在**。三个新测试不能证明从真正 v1 升级，也没有启动二进制，因而漏掉 R3-P1-01。

**修复与验证：** 历史建库 Provider 加 `goose.WithDisableGlobalRegistry(true)`；取固定历史 SQL，先断言 v1 及列缺失／已存在两种前置状态，再切换当前迁移。补真实二进制 serve/CLI 检查。本次外部探针正是按此方式构建，不复用这个失真的前置条件。

## 残留修复边界补充

- **完整 JSON：** `Decoder.More()` 只回答当前数组／对象是否还有元素，不是 EOF 检查。当前已经有完整有界 raw body，可用完整 JSON 验证，或要求第二次 Decode 精确返回 `io.EOF`；同时执行 handler 字段校验。参考 [Go 1.26 Decoder.More 文档](https://pkg.go.dev/encoding/json@go1.26.0#Decoder.More)。
- **Spike 1：** 行数失败必须记入失败状态，两个 FAIL 或两个空文件不能视为相等数据；检查枚举进程退出码及非空、数字结果。`-z` 是字段分隔，`-0` 才是记录 NUL 分隔；角色和表名也需保留记录边界，schema 模式匹配需单独转义。stdin SQL 要显式 `ON_ERROR_STOP`，避免 SQL 出错而 shell 仍得到成功。参考 [PostgreSQL 18 psql 参数与错误处理](https://www.postgresql.org/docs/18/app-psql.html)。
- **Spike 2：** 本轮已修复“整个 canary 容器失败被吞”这一具体残留；不能因此关闭资源、边界和失败清理证据缺口。mock Docker 只用于退出码传播验证，不是容器恢复实验。

## 实测记录与限制

仓库仅新增本报告；业务代码、原测试、生成物均未修改。隔离副本来自 `git archive ba8b495`，放在 `/tmp/codex-phase1-review-3/repo`。`TestReview3*` 与 fixture、mock 脚本均在该临时目录；部分探针断言已发现的坏行为，**探针 PASS 不表示产品验收通过**。

为补测本环境禁止监听的 HTTP 测试，仅在隔离副本将测试 transport 改为真实 Router + `httptest.ResponseRecorder`，保留 cookie jar、RemoteAddr 和服务端非 nil body 语义，并补 store cleanup；业务实现没有改变。首次探针发现 transport 的 nil body 与真实 net/http 不同，已修正后重测；本文采用修正后的 `server-probes.log`，不把适配错误的 500 当产品缺陷。

| 检查 | 实际结果与证据 |
|---|---|
| 原仓库 `go test -race ./backend/...` | auth/config/db/limiter 通过；server 首个测试在 httptest 启动时报 `socket: operation not permitted`，整个命令退出 1。见 `go-race.log`。**不是全套通过。** |
| 隔离副本原测试套件，socket-free transport | `go test -race ./backend/...` 全部通过；server 用时约 30.4 秒，见 `isolated-race.log`。这是 Store/Router 级替代验证，不包括 TCP、HTTP server 期限、信号或 Docker。 |
| 新探针 | DB、auth 探针见 `review3-probes.log`；修正 transport 后的全部 HTTP 探针见 `server-probes.log`；参数错误见 `parameter-error.log`；以上均使用 `-race`。 |
| 真正历史升级 | `0001-original.sql` 来自指定 git show；当前 0001 与它 SHA-256 完全相同。历史 Provider 禁用全局迁移；升级前原版 v1 无列、修改版 v1 有列。实际新二进制 `bootstrap` 触发迁移后，两者均 v3、有列；Store 层登录/reset/revoke 再登录通过。`serve`、持锁 CLI 失败，独占锁 v999 CLI 错误放行。见 `binary-upgrade.log`。 |
| 构建、curl 起服务 | 隔离 archive 用 `go build -buildvcs=false` 构建新二进制成功；该参数只关闭 archive 缺少 Git 元数据时的 VCS stamping。新库 `serve` 在 schema guard 退出 1，curl healthz 退出 7，见 `serve.log`、`curl.log`。**未取得活服务 HTTP 成功结果。** 另带完整前端的 `CGO_ENABLED=0` 构建成功。 |
| 静态与并发检查 | 原仓库 `go vet ./backend/...`、gofmt 检查、`git diff --check 51d8275..ba8b495` 通过；secret 16 路并发创建测试 `-race -count=100` 通过，见 `secret-repeat.log`。无断电／fsync 错误注入。 |
| CI YAML／compose | Python PyYAML safe_load 成功；compose 渲染成功，build.target=runtime。没有触发远程 GitHub Actions。 |
| 前端和生成物 | 使用与仓库 lockfile 对应的已有 node_modules 副本；npm lint、build、gen:api 成功；oapi-codegen 重生成成功，Go/TS 文件均与提交逐字节一致。未声称完成干净联网 npm ci。 |
| 干净构建与资产 | 无 node_modules 的独立 archive 中 `make api-check` 退出 2，底层 `openapi-typescript: not found`，见 `api-check-clean.log`。占位 Router `/` 为 503；构建并复制真实 dist 后，JS 为 `200 text/javascript`（263,313 字节）、CSS 为 `200 text/css`（1,569 字节），见 `assets.log`；复制步骤同时删除 `.gitkeep`。 |
| Docker 两个 target／runtime 无 PG 服务端 | **未执行构建或镜像内核查。** `docker info` 被 `/var/run/docker.sock` 权限拒绝；`sudo -n docker info` 被 no-new-privileges 禁止。仅源码确认 runtime 安装 client、runtime-spike 安装 server。不能将此写成镜像内容实测通过。 |
| Spike 2 | 真脚本启动后即因 Docker 权限失败，见 `spike2-real.log`。mock 注入第二容器退出 4 后脚本退出 4，无 SPIKE2_DONE，见 `spike2-fault.log`。未实测双架构、RSS、实际截断归档恢复、失败清理或 canary 隔离。 |
| Spike 1 | `bash -n` 通过；mock 实测双边 count 失败、表枚举失败仍退出 0；带引号表名保持在标识符内；自定义角色导致退出 1。捕获的角色 SQL 经本地 PostgreSQL 18.4 单用户后端实际解析报错，见 `spike-probes.log`、`role-sql-actual.log`。普通 PG 监听 Unix socket 同样被沙箱禁止，单用户后端不需要 socket。没有连接真实 Supabase 项目。 |

本轮未验证：真实 HTTP 慢 body 期限、production image 两个 target 的实际内容、镜像 healthcheck/SIGTERM、真实 Supabase 恢复、双架构 Spike 2。上述执行环境限制不影响已经实证的启动 guard 错误、认证绕过、SQL 语法错误和备份裁剪错误。

## 仍然 OPEN 的清单及 Phase 2 决策

按根因合并交叉编号，避免重复计算；原编号的继承优先级和本轮实际残留严重度有差别时注明。

### P0

- **P0-02：** login 运行时字段上限、空 body 限流、完整 JSON 边界仍未闭合。无界 KDF 的主要风险已缓解，但原 P0 验收记录不能标记关闭；上线前补齐。

### P1

- **R3-P1-01／P1-06／R2-P1-01／P1-03：** 同版新库与升级库不能 serve，正常 v3 持锁旁路 CLI 被拒；旧 v1／独占锁 v999 的兼容性防线仍不完整。**直接阻塞 Phase 2。**
- **R3-P1-02／重开 P1-02、P2-01：** auth 前缀非空 body 绕过会话和 CSRF，logout 不撤销旧 session。**直接阻塞 Phase 2。**
- **P1-08／R3-P1-04：** 重试挤占历史恢复点、失败备份未隔离、版本字典排序删新留旧。**阻塞依赖状态库可靠升级的 Phase 2。**
- **P1-04：** master secret 目录 Open/Sync 失败仍可报告成功；并发创建问题虽修，持久化错误契约未完成。**进入 Phase 2 凭据加密前关闭。**
- **P1-09：** 字段长度、Unicode 字符语义、尾部 JSON、storage 503 和参数错误 JSON 契约仍不一致。**应在扩展 API 之前关闭。**
- **P1-14：** 构建删除跟踪标记、干净 api-check/check 缺依赖前置。**Phase 1 工程门禁未完成。**
- **P1-18／R2-P1-04／R3-P1-03：** 可用且安全的角色恢复未完成；profile 相互污染、对象名边界、权限验证和错误成功判定仍不可靠。**不满足现行 Phase 1 spike 前置条件。**
- **P1-19：** ADR-003 待执行，Spike 2 资源／边界／失败清理证据不足。**按现行 dev-plan 阻塞验收**；只有负责人明确调整范围与 DoD 后才能后移，当前没有这种授权或文档变更。

### P2

- **P2-02：** PHC 尾随字段仍接受，第二轮残留未修。
- **P2-04：** PR 构建日期、完整镜像 smoke 和双架构证据缺口；实际启动阻断已计入 R3-P1-01。
- **R3-P2-01（关联原 P1-01）：** Origin 短值 panic；原跨 origin 比较与重复 header 残留已修，当前 OPEN 部分为健壮性问题。
- **R3-P2-02：** 升级测试前置库已被新迁移污染，不能防止历史升级回归；须与 R3-P1-01 一并修正测试证据。

P2 单项不必一律阻断下一阶段，但不能覆盖本轮 P1 启动、认证和持久化阻断项。现行四条 Phase 1 DoD 中，“compose 一键启动／CI 全绿”确定失败；认证保证出现回归；迁移备份保留仍不满足；两项 spike 未完成。故本次不是“修改后通过”，而是 **Phase 1 验收未通过，修复并完成上述关键实测后重新验收**。
