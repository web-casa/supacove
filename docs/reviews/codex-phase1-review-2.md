# Phase 1 第二轮评审（复审）

评审日期：2026-10-03。前轮基线：`2388a46`；本轮基线：`51d827594145`。已检查 `git diff 2388a46..51d8275` 的全部 39 个文件，包括生成物、测试、容器、CI、ADR 与 spike。下列业务文件行号均指 `51d8275`。

**总体结论：未通过。** 25 项原发现中，**FIXED 13 项、PARTIALLY_FIXED 10 项、NOT_FIXED 0 项、REGRESSED 2 项**。另发现 **5 项新问题：P0 × 0、P1 × 4、P2 × 1**；其中两项分别导致 P1-03、P2-04 判为 REGRESSED，交叉引用而不重复计算新增数量。维护者“全部修复”的声明不成立。

判定含义：FIXED 表示原问题的实质行为已修复，并不表示其所有建议性扩展测试均已完成；PARTIALLY_FIXED 表示已有有效修复，但原问题仍有明确残留；REGRESSED 表示相关修改引入了阻断原有正常使用的新故障。未运行容器或真实 Supabase 实验的部分，不视为本次实测通过。

## 逐项判定

| 编号 | 判定 | 证据、实际结果及缺项 |
|---|---|---|
| P0-01 | **FIXED** | `backend/internal/server/server.go:81` 删除无条件 RealIP，`:84` 仅显式可信 CIDR 启用 XFF，`:431` 默认使用 socket peer；`TestLoginRateLimitIgnoresUntrustedForwardedHeaders`、`TestLoginRateLimit` 通过，轮换三个代理头不能绕过锁定，正确密码也被 429 拒绝；补充 `TestReview2ProxyAndDefaultDeny` 验证可信代理链中轮换伪造链首仍共用右侧真实客户端配额。KDF 另有全局容量 4 的限制。 |
| P0-02 | **PARTIALLY_FIXED** | `auth/store.go:106` 的用户/token 预检查在 KDF 前，事务内仍复核；`auth/password.go:27` 有有界 semaphore；`server/server.go:222` 的 16 KiB MaxBytesReader 可拒绝单个超大 JSON 字符串。`TestReview2BootstrapKDFOrderingAndBudget` 和 `TestAuthBodyLimitAndValidation` 通过。**缺项：** `server.go:238` 只给 login 限流，30 次同源无效 bootstrap 全部 403；`backend/cmd/supabackup/main.go:165` 仍无 ReadTimeout/请求体读取期限；生成器只解码首个值，32,822 字节的合法 JSON 加尾随空格仍 200。需补 bootstrap 解码前限流、body 期限、完整有界读取，以及 login 字段上限。 |
| P1-01 | **PARTIALLY_FIXED** | `server/server.go:314`、`server/httpx.go:34` 已比较 scheme/host/effective port，错误 scheme、9999 端口被拒，默认 80/443 与配置 TLS 外部 origin 正例通过。**缺项：** `Header.Get` 忽略第二个 Origin；空端口、空 query/fragment 分隔符未被拒。`TestReview2OriginMalformed` 复现 `http://review.test:`、`http://review.test?`、`http://review.test#` 和两条 Origin 被接受。需验证完整语法及 `Header.Values` 数量；未将这些非规范输入夸大为已证实的浏览器跨站攻击。 |
| P1-02 | **FIXED** | `server/api.go:116` 会话 INSERT 错误返回 500；`:138` DELETE 失败返回 500；`auth/store.go:145` 将 bootstrap 用户、token 消费和首个 session 放入同一事务。`TestSessionPersistenceFailuresDoNotReportSuccess` 用 SQLite trigger 验证 login/logout 均诚实返回 500，失败退出后旧 session 仍有效；`TestReview2BootstrapInsertRollback` 验证 500、零 cookie、零用户/session、token 未消费，去掉 trigger 后同 token 重试 201。 |
| P1-03 | **REGRESSED** | `auth/store.go:196` 读取 generation，`:227` 条件 INSERT，`:264` reset 同事务递增并撤销；补充 `TestReview2ActualIssueSessionResetInterleaving` 在真实驱动读完旧凭据后暂停实际 IssueSession，完成 ResetPassword 再恢复，旧登录被拒、新密码可用。**但只修改 `db/migrations/0001_auth.sql:7`，未添加升级迁移；2388a46 旧库升级后登录和 CLI reset 均因缺列失败。** `TestReview2UpgradeFrom2388a46` 已复现，见 R2-P1-01。 |
| P1-04 | **PARTIALLY_FIXED** | `backend/cmd/supabackup/main.go:142` 已先持锁，`config/config.go:109` 拒绝 symlink/非 regular file/宽权限，`:142` O_EXCL 阻止覆盖。**缺项：** 仍先创建最终文件再写入，其他读取者可读到空文件；写入失败/崩溃也会留下永久无效的最终文件，且未同步父目录。原仓库 `TestLoadOrCreateSecretAtomicCreation -race -count=100` 实际失败（本次 2 轮），报“must be 64 hex chars”。需临时文件写完并 fsync 后原子无覆盖发布、读取竞争处理与目录 fsync。成功返回不同 key 的原覆盖问题已修复，不应混同残留的读取错误。 |
| P1-05 | **FIXED** | `Dockerfile:49` 以 0700 创建 UID 10001 数据目录；`db/db.go:69` 收紧既有目录，`:73` 收紧 DB/WAL/SHM，`:80` 预建 0600 DB。`TestOpenTightensDataDirPermissions` 通过。新建 WAL/SHM 受 0700 目录边界保护；本轮未运行 named volume/bind mount 容器验证，README 部署说明仍可补充。 |
| P1-06 | **PARTIALLY_FIXED** | `db/migrate.go:21` 拒绝无锁迁移；`backend/cmd/supabackup/main.go:107` 先尝试持锁初始化，锁被占用时仅调用 schema 检查；`TestCLICannotMigrateWhileServerLocked` 通过。**缺项：** `db/migrate.go:89` 只检查版本大于零，没有与当前二进制 migration 版本比较。`TestReview2CLIAcceptsFutureSchema` 注入版本 999，当前仅含版本 1 的 CLI 仍被允许写入；旧版 1 缺 generation 也被视为兼容。 |
| P1-07 | **FIXED** | `db/db.go:99` 使用绝对路径及 `url.URL`/`url.Values`，`db/backup.go:32` 参数绑定 VACUUM INTO，`:21` 限制 basename 并传入 context。`TestOpenEscapesSQLiteURIPath` 验证 `same?a`/`same?b` 不再共享库；`TestBackupNowSpecialCharacters` 及补充 `TestReview2AllSpecialPathCharacters` 覆盖空格、单双引号、反斜杠、`?/#/%`、中文，备份均落在预期位置。 |
| P1-08 | **PARTIALLY_FIXED** | `db/migrate.go:33` 先 HasPending；`TestMigrateNoPendingDoesNotCreateOrPruneBackup` 通过，普通启动确实不再备份/裁剪。**缺项：** `db/backup.go:82` 仍仅按时间命名，`:86` 在迁移成功前裁剪；失败备份也直接写正式文件名。`TestReview2FailedUpgradesPruneRecoveryPoint` 连续执行 5 次失败的 pending migration，schema 保持 1，但历史恢复点已被删除。需成功后按版本/升级批次保留，失败产物隔离，重试不能淘汰历史升级恢复点。 |
| P1-09 | **PARTIALLY_FIXED** | `server/server.go:99`、`:159` 统一解码/响应/panic 错误，缺字段返回 400，session 查询故障返回 503；相应原测试通过。**缺项：** `server.go:226` 接受缺失 Content-Type 和 `application/jsonp`，`api/api.gen.go:836` 只 Decode 一次，尾部垃圾/第二 JSON 值仍可登录 200（`TestReview2AuthRuntimeGaps`）；密码仍按字节计数，bootstrap 新增上限及用户名 pattern 未同步 `api/openapi.yaml:275`，login 无字段上限；spec 仍漏 login Origin 403、me/logout/details 的 storage 503，CSRF header 仅写在说明中而无参数声明。 |
| P1-10 | **FIXED** | `frontend/src/App.tsx:129` 提供明确初始化入口，`:57` 在 bootstrap 409 切回 login，`:22` readiness 错误展示不可用页面，`:67` CLI 路径改为 `/app/supabackup`。前端 lint/build 通过，状态分支代码已核查；本轮未执行浏览器端到端操作及真实镜像命令。 |
| P1-11 | **FIXED** | `frontend/src/api/client.ts:53` 把 me 401 映射为 null，`App.tsx:149` 退出后立即清 me 并删除 health cache。用实际安装的 QueryClient 和转译后的实际 api.me 复测：401 refetch 后 `status=success,data=null`，退出处理后 me=null、health 不存在（`frontend-probes.log`）。原来的“401 查询失败仍保留旧用户”路径已消除；未将该探针称为浏览器端到端测试。 |
| P1-12 | **FIXED** | `api/cfg.yaml:5` 输出已指向仓库内；隔离 checkout 的干净 `make api-check` 退出 0；临时把 uptimeSeconds 改为 reviewUptimeSeconds 后，Go 和 TS 生成物均变化，`make api-check` 退出 2。探针结束已恢复隔离副本这三个文件。当前 CI workflow 自身的语法回归另见 R2-P1-02，不混同本地生成器路径修复。 |
| P1-13 | **FIXED** | `frontend/eslint.config.js:15` 使用插件 flat 配置；本轮 `npm run lint`、`npm run build` 均退出 0。使用已有、与 lockfile 对应的依赖，未宣称执行了干净 npm ci；显式声明 `@eslint/js` 仍是建议性依赖整理。 |
| P1-14 | **PARTIALLY_FIXED** | `Makefile:10` 的 .NOTPARALLEL 与 `:23` 的 frontend/backend 依赖使 build 有实际顺序，Go test/vet 范围已缩至 backend。**缺项：** `git ls-files` 仍跟踪 `backend/internal/web/dist/index.html` 和 `frontend/tsconfig.tsbuildinfo`，没有承诺的 .gitkeep；干净 checkout 只有 index，无 JS/CSS，`TestReview2CleanEmbedAssets` 两个资产 URL 均返回 200 text/html，未触发 `server/server.go:491` 的 503；make frontend 仍覆盖跟踪文件，api-check 也无工具/依赖安装前置。需落实真正的独立占位/生产 embed 策略与干净构建门禁。 |
| P1-15 | **FIXED** | 新增 `.dockerignore:4` 排除所有 node_modules，`:5`/`:7` 排除两处 dist，另排除前端 tsbuildinfo、根 data/bin/.env 和 DB 文件；`Dockerfile:23` 仅从 frontend stage 提供生产 dist。宿主依赖覆盖镜像 npm ci 的主要问题已消除。跟踪的 tsbuildinfo 未移除归入 P1-14；本轮未执行跨架构镜像构建。 |
| P1-16 | **FIXED** | `compose.yaml:16`、`:35`、`:47` 所有端口显式绑定 127.0.0.1；`docker compose config --format json` 实际确认 8080、5433、9000、9001 的 host_ip 均为 loopback。未执行真实容器监听检查。 |
| P1-17 | **FIXED** | `compose.yaml:37` 将 PG18 tmpfs 改为 `/var/lib/postgresql`，渲染配置一致，覆盖配置所用 PG18 默认 PGDATA；原来只挂旧 `/data` 的问题已消除。PG 镜像仍为可变 tag，digest 固定属于后续可复现性加强；本轮未在容器内执行 SHOW data_directory。 |
| P1-18 | **PARTIALLY_FIXED** | `scripts/spike1-supabase-restore.sh:27` 修正 --dbname，`:100` 修正 pg_dumpall，移除 eval，数组参数和持久输出目录已加入。**缺项：** 三 profile 仍共享同一 TARGET_DB_URL；角色仍仅 NOLOGIN 占位；没有每 profile 独立权限/RLS/FK/TOC 验证，stderr 相互覆盖；使用 `-z` 再 tr 换行并非正确的记录 NUL 分隔，schema pattern 未转义；带密码 URI 仍进入 argv。`:169`/`:170` 行数查询失败未检查，mock 两边都失败仍打印 match 并退出 0。新增对象名 SQL 拼接风险见 R2-P1-04。 |
| P1-19 | **PARTIALLY_FIXED** | ADR-004 新增 Debian/UID 10001、arm64/amd64 的维护者实测记录及 runtime-spike target，属于有效进展；但 `docs/adr/ADR-003-supabase-recovery-profile.md:3` 仍“待执行”，dev-plan 的 Phase 1 DoD 未调整。Spike 2 没有峰值 RSS/进程边界与失败清理证据；`spike2-embedded-pg.sh:53` 在已恢复表存在时测试坏档，可因对象已存在而假阳性，`:73` 还吞掉整个 canary 容器失败（注入退出 4，脚本仍退出 0）。不能以补 ADR 表格认定两项 spike 均已验收，本轮也无真实 Supabase/Docker 重跑条件。 |
| P2-01 | **FIXED** | `server/server.go:194` 明确匿名 method/path 白名单，其余 API 默认认证；`:371` HMAC(master secret, session id) 绑定 CSRF；`:38`/`:45` 生产使用 __Host-，前端同步识别，`server/api.go:48` 对缺 user 防御失败。`TestProductionCookieAttributes`、`TestReview2CSRFTiedToSession`、`TestReview2ProxyAndDefaultDeny` 通过。 |
| P2-02 | **PARTIALLY_FIXED** | `auth/password.go:66` 校验 version，`:77` 限定资源参数，`:81`/`:85` 校验 salt/key 长度，现有负例通过。**缺项：** Sscanf 仍只匹配前缀，不要求消耗完整字段；`TestReview2PHCTrailingGarbage` 将有效 hash 改成 `v=19junk` 或 `p=1,unknown=9` 后原密码仍验证成功。需完整格式解析/明确支持的 profile；本轮未将有界参数范围等同于原来的无界分配。 |
| P2-03 | **FIXED** | `limiter/limiter.go:92` 先清理/受控淘汰，`:98` 新条目初始化时间后入表；`TestLimiterCapacityDoesNotDropNewAttempt`、`TestLimiterLockoutExpiry`、`TestLimiterWindowRollover`、`TestLimiterLockoutFlow` 均在 -race 下通过，容量满时新 key 不再丢失计数。 |
| P2-04 | **REGRESSED** | CI/ADR 已对齐 Go 1.26，`Dockerfile:15` 新增 COMMIT/BUILD_DATE 注入，image smoke job 已添加。但 `.github/workflows/ci.yml:80` 新增未加引号的 `name: Smoke: ...` 导致整个 YAML 无法解析，CI 门禁无法运行，见 R2-P1-02；BUILD_DATE 仍未由 CI 传入，smoke 未验证资产、healthcheck 实际状态和 SIGTERM，且未指定 runtime target，见 R2-P1-03。 |

表内 `auth/...`、`config/...`、`db/...`、`limiter/...`、`server/...`、`api/api.gen.go` 均以 `backend/internal/` 为前缀。`TestReview2*` 为本次隔离副本内的补充探针，不是该提交已有的回归测试。

## 新发现

### P0

无新增 P0。原 P0-02 的请求体期限、bootstrap 限流等残留仍须关闭。

### P1

#### R2-P1-01：原地改写已执行的 0001 迁移，旧库升级后无法登录或重置

**位置：** `backend/internal/db/migrations/0001_auth.sql:7`；`backend/internal/db/migrate.go:37`；`backend/internal/auth/store.go:197`、`:265`。

修复给 users 增加 auth_generation，但只改了已发布的 CREATE TABLE，没有后续 ALTER TABLE 迁移。前一基线的数据库已将版本 1 标为 applied；新二进制 HasPending 返回 false，Migrate 返回成功，readiness 也返回 ready。随后所有登录及 reset 都引用不存在的列。

**复现：** `TestReview2UpgradeFrom2388a46` 直接从 `git show 2388a46:backend/internal/db/migrations/0001_auth.sql` 取得旧迁移，以 goose 建旧库、插入合法 hash，再换为当前嵌入迁移并调用实际 Store 方法。结果：`Migrate=nil, SchemaReady=true`，login/reset 均为 `SQL logic error: no such column: auth_generation (1)`。这不是测试手工删列构造出来的假升级问题。

**修复要求：** 恢复 0001 的原定义，新增独立版本迁移添加该列；同时覆盖旧版数据库和已经使用修改版 0001 建库的升级路径，避免后者重复加列。增加从真实旧版本库启动、登录、重置的回归测试，并让 readiness/CLI 校验实际兼容版本。对应原 P1-03 的竞态逻辑在新库有效，交付升级却发生回归。

#### R2-P1-02：新增 smoke 步骤使整个 GitHub Actions workflow 成为非法 YAML

**位置：** `.github/workflows/ci.yml:80`。

`- name: Smoke: non-root, healthz, takeover blocked, healthcheck` 中第二个冒号后有空格，不能作为未引用的 YAML 普通标量。前一基线文件通过解析，本轮文件报 `mapping values are not allowed here`，准确位置为第 80 行第 20 列。影响整个 workflow 的加载，不能只视为 image job 的运行时失败。

**证据：** 对两个提交的文件分别执行 Python `yaml.safe_load`，结果见 `workflow-yaml.log`。本轮没有远程触发 GitHub Actions；本地解析已足以确认语法错误。

**修复要求：** 引用整个 name 字符串，或移除其内部冒号；加入 workflow 语法检查。修复后再验证 job 的实际启动及 smoke 行为。

#### R2-P1-03：新增实验 stage 变成默认发布镜像

**位置：** `Dockerfile:69`；`compose.yaml:10`；`.github/workflows/ci.yml:73`。

runtime-spike 被追加为最后一个 stage，但 compose build、CI build-push-action 和 Dockerfile 顶部示例均未指定 target。默认构建最终 stage，因此这些入口现在输出的是安装了 PostgreSQL 服务端和 procps 的实验镜像，而不是原来的 runtime。这与 Dockerfile 注释“Not used by releases”及 ADR-004“P1 发布镜像仅客户端”直接矛盾，发布内容、镜像体积和包集合都已改变。

**证据与边界：** Dockerfile stage 顺序及渲染后的 compose `build.target` 缺失已确认；没有 Docker daemon，未声称已构建/测量该镜像，也未声称它会自动暴露 PostgreSQL 监听端口。

**修复要求：** 将真正发布 stage 放回最终位置，或在全部构建入口显式指定 `target: runtime`，仅 spike 明确使用 runtime-spike；smoke 应检查实际交付的 stage。

#### R2-P1-04：Spike 1 将数据库对象名未经 SQL 转义拼入目标库查询

**位置：** `scripts/spike1-supabase-restore.sh:109`、`:169`、`:170`、`:175`。

新增角色循环直接将角色名塞入单引号字面量及双引号标识符；新行数检查同样把 schema/table 名直接拼 SQL。Bash 参数数组只能保护 shell 参数边界，不能转义 SQL。合法的含引号对象名会破坏查询；可控制源对象名的人还能让验证连接提交额外 SQL，使用的是操作者提供的数据库权限。该风险限定于运行此 spike、源对象名可受影响的场景，不是 HTTP API 注入。

**无副作用探针：** 用 mock psql 返回表名 `widgets"; SELECT 42 AS review_probe; --`，实际脚本向源、目标两次传入的完整 SQL 均为：

```sql
SELECT count(*) FROM "public"."widgets"; SELECT 42 AS review_probe; --"
```

mock 只捕获参数，没有连接或修改任何数据库。该证据确认用户数据跨出了 SQL 标识符边界；没有将 mock 结果写成真实 PostgreSQL 执行结果。角色名中的单引号亦会未经转义进入 `rolname = '$r'`。

**修复要求：** 在数据库侧用 `format('%I', ...)`/正确字面量转义生成查询，或通过安全的 psql 变量引用生成 SQL；使用正确的 NUL 记录分隔保存任意对象名，检查每个命令退出码。补上含引号、换行、pattern 元字符的 schema/role/table 测试。

### P2

#### R2-P2-01：KDF 满载时，HTTP 状态码暴露用户名是否存在

**位置：** `backend/internal/auth/store.go:199`、`:214`；`backend/internal/server/api.go:108`、`:113`。

未知用户路径忽略 acquireKDF 的错误并返回 ErrBadCredentials；已知用户路径将 ErrKDFBusy 向上传递。当全局 4 个 KDF slot 被占用时，对不存在用户名返回 401，对存在用户名返回 503，无需判断耗时。这违背 OpenAPI 及该函数“错误密码与未知用户不可区分”的约定。

**复现：** `TestReview2BootstrapKDFOrderingAndBudget` 占满真实 semaphore 后调用实际 IssueSession，确定性得到上述两种不同错误；handler 对应状态映射已核对。slot 可由并发认证工作占用，攻击者是否能稳定制造满载取决于限流和部署负载，因此列 P2，不是认证绕过。

**修复要求：** 未知用户分支也传播 KDF busy/context 错误，使相同系统状态下响应一致；增加并发满载时已知/未知用户名的等价响应测试。

## 验证记录与限制

本轮仓库仅新增本报告。补充测试及故障注入均在 `/tmp/codex-phase1-review-2/repo` 的本地克隆内进行；业务实现未修改。为适应禁止监听端口的沙箱，仅将隔离副本已有 HTTP 测试 transport 改为 Router + `httptest.ResponseRecorder`，保留 cookie jar，并补充 Store 清理。`TestReview2*` 部分断言刻意确认缺陷仍然存在，**探针 PASS 不表示被测功能符合要求**。

| 检查 | 实际结果 |
|---|---|
| Go 构建 | 原仓库 `go build -o /tmp/codex-phase1-review-2-server ./backend/cmd/supabackup` 成功；另以可写 GOCACHE 执行 `CGO_ENABLED=0 go build` 成功。首次构建有只读 module-cache 元信息写入提示，退出码仍为 0，二进制已生成。 |
| 原仓库 `go test -race ./backend/...` | auth/config/db/limiter 通过，server 因 `listen tcp6 [::1]:0: socket: operation not permitted` 中止，**不能称原始全量测试全绿**。 |
| 原仓库核心包重新执行 | `go test -race -count=1 ./backend/internal/auth ./backend/internal/config ./backend/internal/db ./backend/internal/limiter` 通过，消除首次 cached 结果的歧义。 |
| 隔离 HTTP transport 全量测试 | `GOCACHE=/tmp/codex-phase1-review/go-cache go test -race -count=1 -v ./backend/...` 通过；仅限业务/内存 HTTP 语义，不替代 TCP、反向代理、浏览器或 body deadline 验证。 |
| 额外安全与持久化探针 | `go test -race -count=1 -v ./backend/internal/auth ./backend/internal/db ./backend/internal/server -run '^TestReview2'` 全部完成；覆盖真实 IssueSession/reset 屏障交错、session trigger 回滚、旧库升级、代理链、完整 Origin、尾部 JSON、无 pending 和失败 pending 的备份差异、特殊路径、默认认证、CSRF 绑定。结果见 `probes.log`。 |
| secret 重复并发 | `go test -race -count=100 ./backend/internal/config -run '^TestLoadOrCreateSecretAtomicCreation$'` 失败，最终保存的一次运行中 2 轮失败，含读取未完成 key 的错误。没有输出任何 key 内容。 |
| Go 静态检查 | `go vet ./backend/...` 通过。 |
| 生成门禁 | 干净 `make api-check` 退出 0；修改实际影响类型的字段后退出 2，Go/TS 两份文件均变化。该测试使用已安装 oapi-codegen 及 node_modules，不证明干净机器自动安装流程有效。 |
| 前端 | 原仓库 `npm run lint`、`npm run build` 通过；实际 api.me + QueryClient 探针验证 401 清空用户数据及退出清理缓存。未运行浏览器 E2E，也未重新执行 npm ci。 |
| 干净 checkout 静态资产 | 两个 index 引用的 JS/CSS URL 均返回 200 text/html（443 字节），缺资产仍被 SPA fallback 掩盖。只在干净嵌入目录的隔离副本验证，未用宿主已有 dist 冒充干净构建。 |
| CI 语法 | 2388a46 YAML 可解析，51d8275 YAML 第 80 行失败。未远程运行 CI。 |
| Docker/compose | Docker socket 返回 permission denied，未运行容器/镜像构建；compose config 成功，loopback 和 PG tmpfs 配置已核对。没有尝试绕过沙箱权限。 |
| spike | 两脚本 bash -n 通过。Spike 1 mock 验证正确 --dbname 参数、计数失败假成功及对象名 SQL 边界；Spike 2 mock 验证 canary 容器退出 4 被吞后总体仍 0。未连接真实 Supabase，也未重新执行 ADR-004 的双架构实验。 |

主要证据位于 `/tmp/codex-phase1-review-2/`：`full.diff`、`original-core-tests.log`、`isolated-tests.log`、`probes.log`、`secret-repeat.log`、`api-check-clean.log`、`api-check-mutated.log`、`frontend-lint.log`、`frontend-probes.log`、`workflow-yaml.log`、`compose.json`、`spike-probes.log`、`spike2-fault.log`。这些临时探针不是项目已提交的持续回归门禁；报告中的关键步骤和结果可据此重新实现。

## 总体结论

**Phase 1 未通过。** XFF 伪造、会话失败诚实性、SQLite URI、生成器路径、ESLint、初始化入口及退出缓存等修复有效；但请求资源约束仍不完整，旧库升级和 workflow 产生明确回归，secret 并发及失败迁移恢复点保留仍未关闭，两个 spike 的既定 DoD 仍不满足。应先修复这些问题并补上升级、故障和交付验证，再进行下一轮验收；现阶段不能签署“全部 P0/P1/P2 已修复”。
