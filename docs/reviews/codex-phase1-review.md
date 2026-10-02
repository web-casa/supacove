# Phase 1 实现评审

评审日期：2026-10-03。基线：`main@2388a46a510d17abef0229172daa19a012465297`。对照：`docs/dev-plan.md:100–118`，包括关键任务和全部四条 DoD。范围为本次指定的后端、迁移、契约及生成物、前端、构建部署、ADR、spike 和测试。行号均指该提交。

**发现 25 项：P0 × 2、P1 × 19、P2 × 4。总体结论：修改后通过；当前不能认定 Phase 1 已完成。** P0 表示必须在对外部署前修复的安全问题；P1 包括需要修正的安全、正确性和验收问题；P2 为边界条件及加固建议。未发现无需 bootstrap token 就能直接注册管理员的路径，也未将尚未实现的 Phase 2–6 功能算成本期缺陷。

## 验证方式与实际结果

仅新增本报告，未修改业务代码。补充探针放在 `/tmp/codex-phase1-review/repo` 的提交快照中；其通过表示成功验证了所写断言，有些断言特意验证当前缺陷存在，**不代表实现正确**。

| 检查 | 实际结果与边界 |
|---|---|
| Go 构建 | 入口程序构建成功；隔离副本的 `CGO_ENABLED=0 go build -buildvcs=false -o /tmp/codex-phase1-review/supabackup-static ./backend/cmd/supabackup` 成功。隔离副本不保留仓库元数据，因此关闭 VCS stamping。 |
| 格式、静态检查 | 原仓库 `gofmt -l backend` 无输出，`go vet ./backend/...` 通过。 |
| 原始全量测试 | 执行了 `go test -race -cover ./...`。auth、db 测试通过；server 因沙箱禁止监听端口而中止；本机 coverage 对无测试包还报 `no such tool "covdata"`，不能宣称原始全量命令全绿。 |
| 内存 HTTP 验证 | 在副本中仅将测试 HTTP transport 改为直接调用 Router 和 `httptest.ResponseRecorder`，并补上测试 DB 清理；业务实现不变。`go test -race -v ./backend/...` 通过，包含原有 19 个测试及第一批补充探针。后续边界探针分别执行通过。不能用它替代真实 TCP、反向代理或浏览器测试。 |
| 安全负例 | 复现代理头绕过限流、错误 Origin 放行、bootstrap 不限流、session INSERT/DELETE 失败仍报成功、密码重置与登录交错、secret 并发生成不一致。 |
| DB 正例 | 同时占用四条连接，均为 `foreign_keys=1`、`busy_timeout=5000`、`synchronous=1`（NORMAL）、`journal_mode=wal`。子进程持锁时第二实例被拒；SIGKILL 后可重新持锁。两个并发有效 bootstrap token 仅创建一个管理员。 |
| 拒绝启动 | 先以 CLI 初始化测试数据，再将 `backups` 路径替换为普通文件，执行真实二进制 `serve`：退出码 1，错误为 pre-migration backup failed，尚未进入监听阶段。 |
| 契约生成 | 当前 Go 生成结果与已提交文件内容相同；TS 重新生成也无差异。但 Go 生成器输出到了仓库外，修改契约后的探针证实仓库内 Go 文件不变，见 P1-12。 |
| 前端 | 使用已有、版本与 lockfile 对应的 node_modules；`npm run gen:api`、`npm run build` 通过；`npm run lint` 可重复失败。未将依赖安装步骤标记为已验证。 |
| Docker / compose | `docker compose config` 通过。Docker daemon socket 不可访问，未能实际 build、up、验证镜像 healthcheck 或 volume 权限。PGDG 和 PG18 数据目录另对照官方资料核查。 |
| spike | 两个脚本 `bash -n` 通过。用本地 PG18 工具验证了 Spike 1 的参数错误，尚未连接任何真实 Supabase 项目，也未重新执行容器 Spike 2。 |

主要复现日志：`/tmp/codex-phase1-review/isolated-tests.log`、`more-tests.log`、`concurrency-tests.log`、`limiter-capacity.log`、`startup-refusal.log`、`generator-path-probe.log`、`frontend-lint.log`、`pg-argument-probes.log`。临时文件仅用于本次审查，不是仓库验收材料；下面保留了关键结果和回归测试要求。

## [P0 必须修复]

### P0-01：任意客户端可伪造代理头绕过登录限流

**位置：** `backend/internal/server/server.go:57`；`backend/internal/server/api.go:93–108,159–166`。

**问题与证据：** Router 无条件使用 `chimw.RealIP`，它会接受 `True-Client-IP`、`X-Real-IP`、`X-Forwarded-For` 并改写 `RemoteAddr`。限流 key 随后取自被改写的值。普通直连请求连续五次失败后第六次返回 429；同一实际客户端带任意一种上述头即重新返回 401、执行密码校验。轮换头值可持续暴力尝试和消耗每次 64 MiB 的 KDF 资源，也可伪造管理员 IP 制造锁定。仓库 compose 直接发布端口，没有可信代理边界；注释不能建立这个边界。当前锁定的 chi v5.3.2 源码已明确将 `RealIP` 标为易受 IP spoofing 影响的 deprecated API。

**修复：** 默认删除 `chimw.RealIP`，以真实 `RemoteAddr` 经 `net.SplitHostPort` / `netip.ParseAddr` 规范化后限流。若支持代理，新增显式可信代理 CIDR 配置，使用当前版本已有的 `chimw.ClientIPFromXFF(trustedCIDRs...)` 和 `chimw.GetClientIP(ctx)`，让直接来源不在可信网段时忽略转发头；边缘代理覆盖而非透传客户端提供的身份头。不要仅替换为同样无条件信任单个 header 的中间件。增加 IP 配额之外的有界全局 KDF 并发限制。

**回归：** `TestLoginRateLimitIgnoresUntrustedForwardedHeaders`、`TestClientIPTrustedProxyChain`，覆盖三个头、伪造链首、IPv4-mapped IPv6 和正确密码在锁定期间也被拒绝。

### P0-02：匿名 bootstrap 在验证 token 前执行昂贵 KDF，且 HTTP 输入无资源上限

**位置：** `backend/internal/auth/store.go:86–119`；`backend/internal/server/api.go:56–68`；`backend/internal/auth/password.go:16–19,29`；`backend/internal/api/api.gen.go:677,708`；`backend/cmd/supabackup/main.go:146–152`。

**问题与证据：** `Bootstrap` 先计算 Argon2id，之后才检查用户是否存在及 token 是否有效。bootstrap 没有限速，初始化前后都能匿名反复触发 KDF；探针连续七次伪造 token 均完整执行并返回 403。64 MiB 是单次 KDF 内存，多个并发请求足以压垮小规格部署；不需要猜中 token。登录的限流也位于生成器 JSON 解码之后，且没有 `MaxBytesReader`；`ReadHeaderTimeout` 不限制请求体读取时间。超大字符串或缓慢 body 可在任何认证、限流决定之前占用资源。

**修复：**

1. 在生成器 handler **之前**给认证端点设置请求体上限，例如 `req.Body = http.MaxBytesReader(w, req.Body, 16<<10)`，为读取 body 设置期限，并把超限映射为 JSON 413；后续备份流式接口单独配置，勿照搬这个上限。
2. bootstrap 与 login 都在解码前进行可信来源限流；为 Hash/Verify 共用容量可配置的 semaphore，以 `select` 尊重 context 取消，队列满时直接 429/503。
3. bootstrap 先执行廉价的初始化状态和 token hash/有效期查询，拒绝明显无效输入；然后在事务外计算 KDF；最后仍以 `BEGIN IMMEDIATE` 事务**再次检查并消费 token、插入管理员**。预检查不能替代事务内检查，也不能先耗用 token 再进行可能失败的哈希。
4. 限制用户名、密码和 token 的输入长度，并同步 OpenAPI；不要把密码策略最小长度检查误当作 body 大小限制。

**回归：** `TestBootstrapInvalidTokenSkipsKDF`、`TestBootstrapInitializedSkipsKDF`、`TestAuthKDFConcurrencyBounded`、`TestAuthBodyLimitBeforeDecode`、`TestAuthBodyReadDeadline`。

## [P1 强烈建议]

### P1-01：Origin 校验没有比较完整 origin，默认端口处理会放行任意端口

**位置：** `backend/internal/server/server.go:172–179`；`backend/internal/server/httpx.go:20–25,30–45`。

**问题与证据：** scheme 被丢弃；只要任意一方省略端口，所有端口都被认为相同。`Host: review.test` 的登录请求，携带 `Origin: https://review.test` 或 `Origin: http://review.test:9999` 及 `Content-Type: text/plain`，都实际返回 200 并设置两枚 cookie。不同 scheme / port 是不同 origin；同站点的其他服务与登录接口之间的边界失效。当前代码还把带路径或 userinfo 的非规范 Origin 当作可比较的 URL。

**修复：** 引入明确的外部 origin 配置，解析为 `(scheme, hostname, effectivePort)`；只接受 http/https，拒绝 userinfo、query、fragment、额外 path 和多值，按 scheme 将省略端口补为 80/443 后严格比较。反向代理场景以配置或经过可信代理验证的 scheme 为准，不无条件信任 `X-Forwarded-Proto`。无 Origin 的 CLI 请求可以明确保留，但受认证、CSRF 和 JSON 内容类型策略约束。`null` 应继续拒绝。

**回归：** `TestSameOriginSchemeAndEffectivePort`、`TestSameOriginMalformedOrigins`，包含 TLS 终止代理的正例。未声称普通其他域网站已能绕过现有 CSRF header 和 SameSiteStrict 直接执行受保护操作；这里复现的是 Origin 门禁本身及匿名登录端点的错误放行。

### P1-02：会话创建和撤销失败仍报告成功

**位置：** `backend/internal/server/api.go:79–85,108–116,119–130`。

**问题与证据：** `CreateSession` 的错误被条件表达式吞掉，login 返回 200 却不设置 cookie。`DeleteSession` 失败只记日志，logout 仍返回 204。用 SQLite `BEFORE INSERT/DELETE ON sessions` 触发器 `RAISE(FAIL, 'review')` 注入故障：登录 200、零 cookie；退出 204 后保存的原 session 仍能查询出用户。退出不能只依靠浏览器删除 cookie 来宣称服务端撤销成功。bootstrap 已创建用户却未建立会话也未告知调用方。

**修复：** 登录必须在持久化 session 成功后返回 200；失败记录内部日志，返回统一 JSON 500/503，不设置认证 cookie。退出删除失败返回非成功状态；即使清除浏览器 cookie，也必须明确撤销失败。bootstrap 可以将管理员和初始 session 写入同一事务，或明确设计“账户已建、请登录”的结果和前端流程。补齐 OpenAPI 对应响应再重新生成，不手改生成物。

**回归：** `TestLoginSessionInsertFailureReturnsServerError`、`TestLogoutDeleteFailureDoesNotReportSuccess`、`TestBootstrapSessionFailureRecovery`。

### P1-03：密码重置与正在进行的旧密码登录之间存在撤销竞态

**位置：** `backend/internal/auth/store.go:135–163,168–201`；`backend/internal/server/api.go:98–111`。

**问题与证据：** 校验密码与插入 session 是两个独立动作。已复现确定性交错：旧密码 `Login` 返回 User → CLI `ResetPassword` 更新密码并删除所有 session → 原请求 `CreateSession` → 新 session 有效。因此密码重置返回后，已开始的旧密码请求仍能获得完整有效期的会话。`-race` 无法检测这个数据库语义竞态。

**修复：** 新增迁移给 users 加 `auth_generation INTEGER NOT NULL DEFAULT 0`。读取 hash 时一起读取 generation，KDF 留在事务外；签发 session 时用短写事务执行带 `WHERE id=? AND auth_generation=?` 的条件插入并检查受影响行数。ResetPassword 在同一事务中增加 generation、更新 hash、删除 sessions。将 Login 与签发会话封装成一个 Store 方法，避免 handler 错过版本检查。

**回归：** `TestResetPasswordRacesLoginSessionCreation`，用同步屏障固定上述交错，断言旧 generation 无法在重置后签发有效 session。

### P1-04：主密钥创建不具备原子性，既有文件权限也未验证

**位置：** `backend/internal/config/config.go:64–93`；`backend/cmd/supabackup/main.go:124–132`。

**问题与证据：** `Stat` 后 `WriteFile` 会以截断方式写入；且 serve 在取得实例锁之前创建 secret。两个首启进程可各持不同 key，最终磁盘文件由其中一个覆盖，随后失败退出的实例也可能已覆盖成功实例的 key。32 个并发调用的隔离探针观察到 **3 个不同成功返回的 key、1 次读取错误**，未输出 key 内容。另外 0644 的既有 secret 被接受，`Stat` 跟随符号链接，也未要求 regular file。当前 Phase 1 尚未用它加密凭据，故不宣称已经发生凭据丢失；这是必须在承接 Phase 2 前修好的生命周期缺陷。

**修复：** serve 先取得数据目录锁，再加载或创建密钥；独立 secret 路径仍需自身原子创建。最小方案使用 `O_CREATE|O_EXCL`、0600，完整写入并检查 `Sync`/`Close`，碰到已存在则重新读取；若支持多个数据目录共享 secret，使用独立锁或临时文件完整写入后以不覆盖既有文件的方式发布，避免读取半写文件。对已存在对象检查类型、有效访问权限，拒绝 group/other 可读写的普通 secret；符号链接是否允许应有明确挂载策略，不能无条件跟随任意目标。

**回归：** `TestLoadOrCreateSecretConcurrent`、`TestLoadOrCreateSecretRejectsWeakPermissions`、`TestLoadOrCreateSecretRejectsInvalidFileType`、`TestServeLocksBeforeSecretCreation`。

### P1-05：Docker 数据目录为 0755，SQLite 文件会以 0644 创建

**位置：** `Dockerfile:47–49`；`backend/internal/db/db.go:49–51,67–80`。

**问题与证据：** Dockerfile 只 chown，没有 chmod；`MkdirAll(...,0700)` 不收紧既有目录。以既有 0755 目录、当前常见 umask 022 启动的探针得到 `supabackup.db` 权限 0644。能访问该目录的其他本地 UID 可读取用户名、密码哈希和控制面状态；secret.key 本身 0600 不能保护这些文件。镜像 named volume 的属主预创建方向正确，但属主正确不等于权限正确。

**修复：** Dockerfile 使用 `install -d -o 10001 -g 10001 -m 0700 /app/data`；Go 启动校验专用 DataDir 的权限和可写性，按明确策略收紧或拒绝。SQLite 首次创建前预建 0600 DB 文件，或在单线程启动阶段设置严格 umask，保证 WAL/SHM 也受私有目录保护。文档注明既有 named volume / bind mount 的 UID 10001、权限要求及本地文件系统约束，不能假设镜像 chown 能修复所有外部卷。

**回归：** `TestOpenExistingDataDirPermissions`；容器中分别用新 named volume、既有 root 属主 volume 和 bind mount 验证正常路径及明确错误。

### P1-06：CLI 绕过实例锁后仍执行迁移，破坏“持锁后迁移”约束

**位置：** `backend/internal/db/db.go:39–46`；`backend/cmd/supabackup/main.go:192–201,217–225`；`backend/internal/db/migrate.go:17–25`。

**问题与证据：** 允许 bootstrap/reset 与服务器共存的 CLI 连接完全不参与 flock，却调用 `Migrate`。探针在 server Store 持有排他锁时，CLI 仍成功执行备份和 goose 路径。当前只有一个 schema 版本，通常表现为额外备份；升级期或两个命令初始化时，DDL 可与在线服务并发，备份检查失败也只能阻止 CLI，不能阻止已经服务中的其他写入。WAL 和 busy_timeout 保证 SQLite 的数据库锁规则，不保证应用级迁移互斥。

**修复：** 将“服务实例锁”与“普通短事务 CLI”权限分开。CLI 连到运行中的实例时只验证 schema 与当前二进制兼容，执行业务短事务，**不迁移**；空库需要迁移时必须先取得与 serve 相同的排他锁。可采用“尝试 Open 成功则在持锁状态初始化，否则以 OpenForCLI 打开并只校验 schema”的流程。`Store.Migrate` 自身检查 `lockFile != nil`，防止今后调用者误用；禁止跨版本 CLI 在线写入。

**回归：** `TestCLICannotMigrateWhileServerLocked`、`TestCLIFreshDBMigrationTakesLock`、`TestCLIRejectsSchemaVersionMismatch`、`TestConcurrentCLIInitialization`。

### P1-07：SQLite URI 未转义，VACUUM 使用 Go 字符串转义作为 SQL 引号

**位置：** `backend/internal/db/db.go:67–68`；`backend/internal/db/backup.go:21–25`。

**问题与证据：** `file:%s?...` 直接拼路径，含 `?` 的合法目录名变成 URI 参数。探针同时 `Open(root/"same?a")`、`Open(root/"same?b")`，得到两个不同锁文件，却能从第二个连接读到第一个创建的表：两个实例落到了同一 SQLite 文件。`VACUUM INTO %q` 使用 Go 的 `\"` / `\\`，不是 SQLite 字符串转义；含双引号路径直接 SQL syntax error，含反斜杠时写出的文件名与后续 Chmod 的名字不同。这是受本地配置触发的路径正确性和锁隔离问题，不夸大为匿名远程 SQL 注入。

**修复：** 将 DB 路径先 `filepath.Abs`，通过 `url.URL{Scheme:"file", Path:absPath}` 和 `url.Values` 构造 DSN，逐项 `Add("_pragma", ...)` 后编码；确保锁和 DB 指向同一个预期数据目录。备份改为参数绑定：

```go
_, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", dest)
```

把 ctx 传入 BackupNow 和备份前检查；限制 name 为单个 basename，不能带目录穿越。补测空格、单/双引号、反斜杠、`?`、`#`、`%` 和中文目录。

**回归：** `TestOpenEscapesSQLiteURIPath`、`TestDifferentDataDirsNeverAliasDB`、`TestBackupNowSpecialCharacters`。

### P1-08：无迁移的启动和 CLI 操作也生成并裁剪备份，会淘汰真正的升级前恢复点

**位置：** `backend/internal/db/migrate.go:17–25`；`backend/internal/db/backup.go:33–42,61–74`。

**问题：** 备份发生在 goose 检查 pending migration 之前。每次重启、bootstrap、reset-password 都产生 `pre-migrate-*` 并只留五份。升级后的几次普通重启即可删除唯一的升级前快照；清理还发生在迁移成功之前。名称声称是迁移前备份，实际保留规则却是最近五次命令运行。大库上也带来无必要全量 VACUUM 和磁盘依赖。

**修复：** 使用当前 goose v3.28.0 的实例 Provider：`fs.Sub(migrationsFS,"migrations")` → `goose.NewProvider(goose.DialectSQLite3, s.DB, fsys)` → `HasPending(ctx)`。仅真正升级前备份；将源版本、目标版本关联到备份记录或命名，并在成功迁移后按升级批次清理，至少保留上一已验证版本的恢复点。不要因重复无迁移启动淘汰它。清理错误需可观察；失败产物用临时文件名隔离，不进入成功快照集合。

**回归：** `TestMigrateNoPendingDoesNotCreateOrPruneBackup`、`TestFailedMigrationPreservesRollbackSnapshot`、`TestPreMigrationRetentionByVersion`。

### P1-09：运行时校验和错误响应没有实现 OpenAPI 声明的契约

**位置：** `backend/internal/server/server.go:65,140–155,183–192`；`backend/internal/api/api.gen.go:651–658,673–681,704–712`；`backend/internal/server/api.go:74–76,104–106`；`api/openapi.yaml:105–153,213–246`。

**问题与证据：**

- 非法 JSON `{` 返回 `text/plain` 400，而契约要求 `{code,message}` JSON；默认 Recoverer 的 panic 路径也不使用统一 Error。
- login 的 `{}` 和 `null` 都返回 401 并计算 KDF，没有验证 required 字段；合法 JSON 后追加垃圾仍可登录成功；`text/plain` 被当作 JSON。生成 Go struct 无法区分缺失字段与零值，不等于运行时 schema 校验。
- bootstrap 用户名的 ASCII 规则、密码按字节计数与契约中的字符长度没有统一表达；没有上限。
- guard 会对 logout 返回 CSRF / Origin 403，对 login 返回 Origin 403，但 spec 未声明这些响应和 CSRF header。服务内部错误被标为 400；session 数据库查询失败又被 `sessionUser` 折叠成 401。

**修复：** 在生成 handler 前实现有界请求校验：严格 JSON media type、对象非 null、required 字段存在、完整单 JSON 值及统一长度/字符规则；也可以加入经过配置的 OpenAPI validator，但需明确路由 base URL、认证与错误适配。schema 与后端采用同一字符长度定义，ASCII 用户名增加 `pattern`。注册 `api.NewStrictHandlerWithOptions` 的 request/response error callbacks，统一用 `writeError`；自定义 Recoverer 也遵守该形状。将 session 查询签名改成返回 `(*User,error)`，区分无效 session 的 401 与数据库故障的 500/503。补齐 403/413/415/429/500/503 响应及受保护 mutation 的 CSRF 约定，再重生成两端。

**语义基线：** 无有效会话/错误密码 → 401；有效会话但 CSRF 无效、Origin 不允许、bootstrap token 无效 → 403；已初始化 → 409；限流 → 429；数据库或持久化错误 → 5xx。当前普通 session/CSRF 分支的 401/403 区分基本正确，缺陷在遗漏响应、校验和错误折叠。

**回归：** `TestAuthRequestRuntimeValidation`、`TestMalformedJSONUsesErrorSchema`、`TestAllGuardErrorsMatchOpenAPI`、`TestSessionLookupDBFailureIsNot401`。

### P1-10：新实例永远进入登录页，bootstrap UI 无正常入口

**位置：** `frontend/src/App.tsx:12–27,75–79`；`backend/internal/server/api.go:25–41`；`backend/cmd/supabackup/main.go:137–144`；`Dockerfile:50–62`。

**问题：** 前端用 readiness 503 判断“未创建管理员”；但服务监听前已迁移，GetReady 只检查 schema 与 DB，空用户表也返回 200。于是新实例只显示 Login，无法在 UI 输入 CLI token。数据库故障的 503 反而展示初始化表单。注释中的“409 转登录”也没有实现。此外页面给出的 `docker exec <container> supabackup bootstrap` 与镜像不一致：二进制在 `/app/supabackup`，`/app` 未加入 PATH。

**修复：** readiness 只表示运行准备状态。最小改动是在登录页提供明确“使用本地 token 初始化”入口，通过显式页面状态或 `/setup` 路由进入表单；收到 409 切回登录。无需为了页面分流新增暴露内部状态的匿名健康接口。503 展示服务不可用和重试。CLI 指引改为 `docker exec <container> /app/supabackup bootstrap`。

**回归：** `testFreshInstanceCanReachBootstrap`、`testInitializedBootstrap409ShowsLogin`、`testReadiness503DoesNotOfferInitialization`；真实镜像中验证页面显示的命令。

### P1-11：退出后的查询错误保留旧 me.data，界面继续显示已登录

**位置：** `frontend/src/App.tsx:19–20,105–108`；`frontend/src/api/client.ts:38–41`。

**问题与证据：** logout 成功只 invalidate `me`；TanStack Query 后续 401 refetch 失败会保留此前的 data。App 首先判断 `me.data`，因此继续渲染 Dashboard。用当前安装版本 QueryClient 复现：refetch 返回 401 异常后 `status=error`、`data={username:"admin"}` 仍存在。session 自然过期也有同样表现。这不绕过后端授权，但退出/过期 UI 与安全状态不一致。

**修复：** auth 查询将 401 转为显式 `null`（`User | null`），将其他错误保留为故障；logout 成功后取消活动认证查询，`qc.setQueryData(["me"], null)`，清除健康详情等受保护缓存，再跳转登录。App 按显式认证状态渲染，不用“存在历史 data”判断当前登录成功。

**回归：** `testLogoutClearsAuthenticatedView`、`testExpiredSessionLeavesDashboard`，断言无需刷新页面即可退出，后续用户不会看到前一会话的缓存。

### P1-12：Go 生成器输出路径错误，CI 的契约漂移检查可假绿

**位置：** `api/cfg.yaml:4`；`Makefile:22–30`；`.github/workflows/ci.yml:36–39`。

**问题与证据：** oapi-codegen v2.5.0 的 output 相对执行目录，而非配置文件目录。当前从仓库根执行会写 `../backend/internal/api/api.gen.go`，真正被 git diff 检查的 `backend/internal/api/api.gen.go` 没有变化。在隔离副本把 `uptimeSeconds` 改名后，生成器退出 0，仓库内输出 unchanged=true，仓库外输出 changed=true。当前提交恰好同步，不代表门禁有效；CI 的兄弟目录可写时会静默通过，受限环境下则直接失败。

**修复：** 将 cfg 的 output 改成 `backend/internal/api/api.gen.go`，统一从仓库根运行；或者所有调用统一 `cd api`，但不要混用两套相对路径。增加一次“修改 schema 必须改变已跟踪 Go/TS 文件并令 api-check 失败”的门禁自测。生成器版本固定为 v2.5.0，提供可复现安装 target。

**回归：** `testGeneratedGoDriftFailsCI`、`testGeneratedTSDriftFailsCI`；先在干净工作树运行生成检查，再临时改一个实际影响类型的字段确认检查失败。

### P1-13：React Hooks 的旧式 ESLint 配置使前端 CI 必失败

**位置：** `frontend/eslint.config.js:12–16`；`.github/workflows/ci.yml:58–60`。

**问题与证据：** `reactHooks.configs["recommended-latest"]` 含 `plugins: ["react-hooks"]`，是旧式配置；ESLint 9 的 flat config 要求 plugins 为对象。本地 `npm run lint` 已复现该配置错误；它在分析任何业务文件前失败，因此不能认定“CI 全绿”。

**修复：** 改用已安装插件实际提供的 `reactHooks.configs.flat["recommended-latest"]`，再执行 `npm run lint` 和 `npm run build`。`@eslint/js` 被配置直接 import，却只依赖传递安装，建议将与 ESLint 匹配的版本加入 devDependencies 并更新 lockfile。

**回归：** 干净安装后 `npm ci && npm run lint && npm run build`，由现有 CI 继续执行即可，无需为这一行配置另造单元测试。

### P1-14：make build 是空目标，提交的 embed“占位页”实际引用不存在的产物

**位置：** `Makefile:9,14–20,26–30,42–46`；`.gitignore:6–9`；`backend/internal/web/web.go:1–9`；`backend/internal/web/dist/index.html:8–9`。

**问题与证据：**

- `build` 仅声明为 PHONY，没有依赖或 recipe；实测 `make build` 返回成功并提示 Nothing to be done。
- 干净提交仅有 index.html，没有它引用的 hashed JS/CSS。这并非可独立展示的 placeholder。`make backend` 可编译出打开即空白的前端；缺失资产会被 SPA fallback 返回 HTML。
- `make frontend` 覆写被跟踪的 index，同时新 hashed assets 被忽略，下一次提交很容易再次只有新 index。
- `make check` 的 api-check 在 recipe 里的 `npm ci` 之前运行；干净环境且无本地 oapi-codegen 时没有自动准备工具和 node_modules。`go test ./...` / `go vet ./...` 在已安装 frontend/node_modules 时还会扫描其中附带的 Go 源码，本次发现了 `flatted/golang/pkg/flatted`。

**修复：** 补上保证先前端后后端的 build 顺序，例如 `build: frontend` 配合 recipe `$(MAKE) backend`；不要写可被 `make -j` 并发执行的两个无顺序 prerequisite。选择一致的 embed 策略：开发占位内容独立存放，生产 build 必须嵌入完整 dist；或用 build tag 分离占位 FS 与生产 embed，让生成目录完全忽略。不要让真实构建覆写被跟踪的 placeholder。将 npm/tool 安装作为 api-check 的显式前置，Go 包范围改为 `./backend/...`。

**回归：** `testCleanCheckoutBuildEmbedsAllAssets`：全新 checkout 执行 make build，解析 index 中每个脚本/样式 URL，确认都有真实内容与正确 Content-Type；不能只检查页面含有产品名。现有 `TestStaticSPA` 无法发现该问题。

### P1-15：缺少 .dockerignore，宿主 node_modules 会覆盖容器安装结果

**位置：** `Dockerfile:9–12,18–21`；仓库根缺少 `.dockerignore`。

**问题：** Docker 先在镜像里 npm ci，随后 `COPY frontend/ ./` 将宿主 node_modules 一起复制。开发机已有依赖时会覆盖镜像内平台相关依赖；跨架构构建尤其不可复现，还会带入旧 dist、tsbuildinfo。backend 本地 embed 产物也可能随 COPY 混入新 dist。Git 忽略规则不等于 Docker build context 过滤规则，运行数据和本地 env 也没有明确排除。[Docker build context 文档](https://docs.docker.com/build/concepts/context/)。

**修复：** 新增 `.dockerignore`，至少排除 `.git`、`**/node_modules`、`frontend/dist`、`backend/internal/web/dist`、`**/*.tsbuildinfo`、`bin`、`data`、`.env*`、`**/*.db*`；确保最终 embed 仅来自 frontend stage。按实际目录继续维护本地 secret/运行数据排除规则。将 tsbuildinfo 从 Git 跟踪移除并加入 .gitignore。

**回归：** 在宿主装过依赖/构建过前端的工作树与干净树分别构建 amd64、arm64 镜像，均运行镜像内 smoke test；确认不会复制宿主依赖。

### P1-16：标注为本地开发的 compose 会把明文认证和固定凭据服务发布到所有网卡

**位置：** `compose.yaml:6–11,21–25,33–38`。

**问题：** `8080:8080` 等简写默认不是 loopback 限定；与此同时 app 显式关闭 Secure Cookie，PostgreSQL/MinIO 使用仓库公开的开发密码。在当前“本地服务器”或云主机上执行 make dev，会把这些服务暴露到服务器可达的网络。bootstrap token 防抢占不保护传输中的密码/session，也不保护旁边直接公开的数据库和对象存储。

**修复：** 开发 compose 将所有发布端口绑定 `127.0.0.1`，例如 `127.0.0.1:8080:8080`、`127.0.0.1:5433:5432`、`127.0.0.1:9000:9000`、`127.0.0.1:9001:9001`。远程访问通过明确的 TLS 反向代理或 SSH 转发；生产配置保留 Secure Cookie，开发配置不默认承担公网入口。

**回归：** 检查 compose 渲染后的 HostIp，并在可运行 Docker 的 CI 中确认端口绑定范围。

### P1-17：PostgreSQL 18 的 tmpfs 挂载到了旧数据目录

**位置：** `compose.yaml:19–27`。

**问题：** `postgres:18-alpine` 默认 `PGDATA=/var/lib/postgresql/18/docker`、VOLUME 为 `/var/lib/postgresql`；当前 tmpfs `/var/lib/postgresql/data` 不覆盖实际集群目录。所谓 throwaway 数据会落到另一层 volume，而非预期 tmpfs；对重建、占用空间和数据清理的假设不成立。该默认值来自 [PG18 官方镜像 Dockerfile](https://raw.githubusercontent.com/docker-library/postgres/master/18/alpine3.23/Dockerfile)。

**修复：** tmpfs 改为 `/var/lib/postgresql`，或显式设置 PGDATA 并挂载它；文档和检查脚本统一。为 spike/test 镜像固定测试过的版本或 digest，避免 latest/tag 漂移改变验证前提。

**回归：** 容器内 `SHOW data_directory`，结合 mount 信息确认确实在 tmpfs；重建后验证测试数据不保留。

### P1-18：Spike 1 的命令和实验方法不足以得出有效恢复结论

**位置：** `scripts/spike1-supabase-restore.sh:21–24,32–44,49–79`；`docs/adr/ADR-003-supabase-recovery-profile.md:23–27`。

**问题与证据：**

- 全部 `pg_restore "$TARGET_DB_URL" ... "$OUT/...dump"` 都漏了 `--dbname`。本地 PG18 直接报 too many command-line arguments；P-B 的 FAIL 可能仅是命令写错，不能算恢复到 Supabase 的失败对照。[pg_restore 官方参数](https://www.postgresql.org/docs/18/app-pgrestore.html)。
- `pg_dumpall --connection` 不存在，fallback 的 `pg_dump --roles-only` 也不存在；两者实测均报 unrecognized option，错误又被丢弃。pg_dumpall 应用 `--dbname`/`-d` 配合 `--roles-only`。[pg_dumpall 官方参数](https://www.postgresql.org/docs/18/app-pg-dumpall.html)。
- `quote_literal` 产出的 SQL 引号通过未加引号的命令替换，不会成为 shell 参数分组；P-A 的 `-N 'auth'` 会把单引号作为 schema pattern 的实际字符，无法按预期排除。P-C 使用 eval 后再次分词，也不能正确处理含空格等字符的 schema。
- 三种 profile 连续恢复到同一目标，先前部分成功会污染后续实验；role 导出内容未被恢复，只按名字创建 NOLOGIN 角色，未验证角色属性、授权和依赖。
- `n_live_tup` 是统计估计，脚本即便 count diff 或各恢复失败也打印 DONE 并可能退出 0，且真实结果文件由 EXIT trap 删除。auth/storage 数据排除后，也无法回答 ADR 中跨 schema FK、auth 用户等问题。

**修复：** 所有恢复改用 `pg_restore --dbname="$TARGET_DB_URL" --exit-on-error ... "$archive"`；用正确的 pg_dumpall 选项并按 profile 审核角色 SQL。将 schema 名以 NUL 分隔读取进 Bash 数组，构造 `args+=(--schema "$schema")` / `--exclude-schema`，用 `"${args[@]}"` 传参并正确处理 PG pattern 引号；删除 eval。每个 profile 使用独立新建目标或经明确验证的等价初始状态。每步保留退出码、脱敏 stderr、TOC、精确 COUNT/数据指纹与 RLS/FK/扩展断言；已知对照失败与候选失败分别记录，候选失败应使最终退出码非零。连接参数拆分并用 0600 PGPASSFILE，避免把带密码 URI 放进工具 argv。不要在修脚本的过程中连接或清理任何现有生产目标。

**回归：** `testSpike1PgToolArguments`、`testSpike1SchemaArgumentBoundaries`、`testSpike1CandidateFailureExitCode`；随后才是真实 Supabase profile 实验。

### P1-19：两个 spike 的 DoD 尚未达到，ADR“存在”不能代替所要求的实证

**位置：** `docs/dev-plan.md:110–111,118`；`docs/adr/ADR-003-supabase-recovery-profile.md:3,29–41`；`docs/adr/ADR-004-embedded-verifier-trust-boundary.md:9–21,35`；`scripts/spike2-embedded-pg.sh:6–8`。

**问题：** ADR-003 明确“待执行”，没有真实 Supabase 新项目恢复结论。ADR-004 有 arm64、postgres:18-alpine、UID 70 的记录，支持非 root 运行临时 PG 的可行性和同 UID 不隔离恶意 dump 的判断；但不是发布用 Debian slim/UID 10001 镜像，没有 amd64、发布镜像依赖/权限、峰值内存或所要求的文件/进程边界实测。Dockerfile 当前只装客户端，ADR 自己把发布形态复测后移到 P6，与 P1 计划不一致。

**修复：** 修正 Spike 1 后，在明确的真实测试项目上执行并把版本、profile、恢复退出码、数据/权限断言和限制写入 ADR-003。为 Spike 2 增加从实际 runtime stage 派生的验证 target，安装将来使用的 PG 服务端依赖、保持 UID 10001 与发布约束，在 amd64/arm64 跑同一脚本，记录 RSS、磁盘、耗时、清理和同 UID 可达文件的无真实 secret canary 实验。可以保留独立 test target，毋须强行提前发布 P6 功能。若负责人决定后移某项，需同步 dev-plan 的范围与 DoD，不能仍声称原定 P1 已通过。

**回归/验收：** `testSpike2ReleaseRuntimeAMD64`、`testSpike2ReleaseRuntimeARM64`、`testSpike2CleanupOnFailure`。本次评审没有可用 Supabase 测试项目和 Docker daemon，故未执行这些外部实验。

## [P2 可考虑]

### P2-01：授权策略默认放行，CSRF token 没有绑定到 session

**位置：** `backend/internal/server/server.go:121–146,196–201,246–265`；`backend/internal/server/api.go:44–45`；`api/openapi.yaml:15–16`。

**问题与边界：** spec 全局声明 cookieAuth，但 guard 只保护手写的三个路径，新增已声明受保护的 API 会默认匿名通过。GetHealthDetails 本身不检查 user。当前检查的重复斜线、末尾斜线、编码字符和 dot segment 没有取得匿名 diagnostics；编码 `/api%2f...` 落到 SPA 并非鉴权成功，因此不报“已发现路径绕过”。CSRF 采用未签名的双提交 cookie，只比较两个客户端值；没有 `__Host-` 前缀或服务端绑定。现有严格 Origin、SameSite、无 CORS 仍是有效额外防线，不能仅因未签名就宣称已可跨站退出。

**修复：** 在加入下一批业务 API 前改为明确的匿名 `(method,path)` 白名单、其余 API 默认要求 session，或根据生成 operation 的 security metadata 做认证。受保护 handler 对缺失 user 也安全失败。生产采用 `__Host-` cookie 名，保持 Secure/Path=/、无 Domain；CSRF 可持久化 session 关联值，或使用有服务端密钥的 HMAC 绑定 session 和 nonce。若更改名称，同步 OpenAPI、前端读 cookie 和测试；开发 HTTP 模式需要明确的名称/属性策略。

**回归：** `TestNewProtectedOperationDefaultsToAuthenticated`、`TestGuardPathVariants`、`TestCSRFTiedToSession`、`TestProductionCookieAttributes`。

### P2-02：密码 hash 解析器没有真正对未知格式 fail closed

**位置：** `backend/internal/auth/password.go:38–62`；`backend/internal/auth/password_test.go:24–33`。

**问题与证据：** 解析 version 但不比较 argon2.Version；把有效 hash 中 v=19 改成 v=999，原密码仍能验证。`t=0` 的可解析 hash 会 panic，超大 m/t 可引起资源耗尽；salt/hash 长度也不验证。正常 API 不允许用户提供 PHC，故前提是存量数据损坏、导入或未来算法迁移，不是当前匿名直接输入路径。现有“v=999”测试因字段数量不对提前返回，没覆盖真正的 version 判断。

**修复：** 为当前唯一支持的 PHC profile 严格校验 version、m/t/p、salt/key 长度和完整参数格式；只在明确支持的有限范围内执行 KDF。解析失败返回普通验证失败并可记录不含 hash 的数据异常。将来需要兼容旧参数时用明确 profile 表，不接受无界参数。

**回归：** `TestVerifyPasswordRejectsUnsupportedVersion`、`TestVerifyPasswordRejectsZeroAndExcessiveParameters`、`FuzzVerifyPasswordMalformedHash`，断言不 panic、不执行无界分配。

### P2-03：限流表达到 10,000 项后，新 key 在首次记录前被清理

**位置：** `backend/internal/limiter/limiter.go:79–94`。

**问题与证据：** get 先插入 `&attempt{}`，再按 windowStart 过旧清理；新条目的零时间立刻满足条件，被删除，但 Allow/Fail 继续修改已脱离 map 的指针。预置 10,000 个近期 key 的探针中，新 key 连续 20 次 Allow+Fail 全放行，最终仍不在 map 中。这是容量边界下真实的配额丢失，不是仅仅内存增长风险。

**修复：** 创建时赋 `windowStart: now`，清理放在插入之前，并定义达到硬上限时的行为：拒绝新 key、受控 LRU 淘汰或回退全局预算；不能悄悄丢掉刚创建的配额。记录 lastSeen/到期时间，避免每次请求持全局互斥锁遍历整张表。

**回归：** `TestLimiterCapacityDoesNotDropNewAttempt`、`TestLimiterLockoutExpiry`、`TestLimiterWindowRollover`、`TestLimiterConcurrentBudget`，用注入时钟，不依赖真实 sleep。

### P2-04：Go 工具链、构建元信息和交付检查没有对齐

**位置：** `go.mod:3`；`.github/workflows/ci.yml:15,32`；`docs/adr/ADR-001-tech-stack.md:9`；`Dockerfile:5,15,22–24`；`backend/cmd/supabackup/main.go:30–34`。

**问题：** go.mod / Docker 使用 1.26，CI 与 ADR 仍为 1.24。默认 GOTOOLCHAIN=auto 可以切换到新工具链，所以**不据此断言 CI 一定编译失败**；实际风险是额外网络/缓存依赖、格式工具与真实构建版本不清楚。Docker 只设置 version，commit/buildDate 保持 unknown，详情诊断无法对应提交。CI 没有 production image smoke 或架构矩阵，无法自动发现本报告的 compose/卷/embed 问题。[Go 工具链选择规则](https://go.dev/doc/toolchain)。

**修复：** setup-go 改 `go-version-file: go.mod`，同步 ADR；Docker 增加 COMMIT/BUILD_DATE args，传入对应 `-X main.commit=... -X main.buildDate=...`。增加最小镜像检查：非 root UID、干净 volume 启动、`/api/healthz` 与 healthcheck、完整嵌入资产、CLI bootstrap、SIGTERM 退出。需要双架构承诺的 spike/release job 明确列出 amd64 和 arm64。

**回归：** `testImageBuildMetadata`、`testImageHealthcheckAndVolume`、`testImageGracefulShutdown`；不要求 Phase 1 提前实现多版本 PG 备份矩阵。

## 安全与存储中已确认成立的部分

- **bootstrap 防直接抢占成立：** 32 字节随机 token、仅存 SHA-256、有效期与 used_at 检查；在 `_txlock=immediate` 事务中检查管理员数量、消费 token、插入用户。原有未持 token/伪造/过期负例，以及补充的两个有效 token 并发正例均通过。昂贵操作顺序和限流问题见 P0，不等于 token 机制失效。
- **会话基本设计成立：** 32 字节随机值、库内只存 hash；查询检查 expires_at；普通退出成功会删除服务端记录。密码重置的更新和已存在 session 删除同一事务，问题是 P1-03 的在途签发，不是该事务内部非原子。
- **Cookie 的正常属性成立：** session 默认 Secure、HttpOnly、SameSiteStrict、Path=/，无 Domain；CSRF cookie 刻意非 HttpOnly 以供前端回传；清除时使用相同 Path/Secure/SameSite、MaxAge=-1。是否使用 Expires 不是当前缺陷，MaxAge 足以表达期限。正式属性目前缺乏仓库测试，所有 HTTP 测试都启用了 InsecureCookie。
- **常规 CSRF 检查成立：** 受保护写请求缺 header 或不匹配返回 403，匿名受保护资源返回 401；不同 host 的恶意 Origin 被拒。未添加 CORS 放行规则。剩余问题是完整 origin 比较、策略默认值和 session 绑定。
- **PRAGMA 和短事务方向正确：** 设置在 driver DSN 上，每条新连接都会执行；本次实测四连接均符合配置。WAL + NORMAL 是明确策略，不是“忘记开 FULL”；NORMAL 的掉电提交耐久性取舍应写进文档，不能把进程崩溃与电源故障混为一谈。
- **锁不是检查文件是否存在：** Linux flock 保持在 Store 生命周期，进程死亡释放；本次子进程验证通过。该设计只支持本地单机文件系统；NFS/RWX 排除虽在 dev-plan 中，但应进入部署 README。CLI/路径例外见 P1-06/07。
- **VACUUM INTO 可以生成在线一致快照：** 它是 SQLite 官方认可的 backup API 替代方案，当前测试也确实从快照读到 WAL 模式下插入的数据。不能因为未用 sqlite3_backup 就将其判为“不一致拷贝”。当前 SQLite 文档还明确 NORMAL/FULL 会同步 VACUUM INTO 输出，本仓库驱动源码也继承该安全级别，故不采用旧资料中的“VACUUM INTO 永不 fsync”断言。仍需修复路径绑定、失败产物和保留策略，并在 ADR/dev-plan 明确接受这一替代实现。[SQLite VACUUM 文档](https://www.sqlite.org/lang_vacuum.html)。
- **迁移失败会阻止 serve 启动：** 备份错误从 Migrate 冒泡到 runServe，监听在之后；实际二进制故障探针印证这一点。但现有仓库测试只检查函数返回和旧 schema 完好，并未完整覆盖进程拒写及真实待执行迁移。
- **goose 基础集成有效：** embed 迁移、sqlite3 dialect、Up/Down SQL 顺序及 FK cascade 没发现基础用法错误；初始迁移和现有测试通过。建议用实例 Provider 避免 `SetBaseFS`/`SetDialect` 全局状态，并落实独占迁移，不能把 SQLite 正确串行写入等同于 goose 已有跨进程迁移门禁。
- **Docker PGDG 写法并非缺陷：** Debian bookworm 支持 `.asc` 的 signed-by keyring，官方也给出相同 key 路径；无需强制 dearmor 成 `.gpg`。PGDG 支持 bookworm 的 amd64/arm64，`postgresql-client-18` 是有效包名。Dockerfile 中续行间的整行注释由 Dockerfile parser 处理，不能按原始 shell 注释推断 PG 包未安装。由于未能 build，不把这项静态核查写成镜像构建已通过。[PostgreSQL Debian 安装文档](https://www.postgresql.org/download/linux/debian/)。
- **healthcheck 和卷方向合理：** healthcheck 调用已有子命令，只探测 `/api/healthz`，不依赖被 purge 的 curl、不触达桶；新空 volume 的预置 mountpoint 属主方案合理。既有 volume/bind mount 不会自动变成所需属主，0700 权限也需另补。不能误报为“所有 anonymous volume 都必然 root:root，镜像必起不来”。[Docker volume 初始化行为](https://docs.docker.com/engine/storage/volumes/)。

## Phase 1 DoD 逐条核对

| dev-plan DoD | 代码与测试证据 | 判定 | 仍需补齐 |
|---|---|---|---|
| `:115` compose 一键起开发环境；CI 全绿含契约同步 | compose 可解析；Go 可构建、TS 可构建；CI 有三类 job | **未满足** | 前端 lint 必失败、Go 契约检查写错路径；新实例 UI 无 bootstrap 入口；镜像/compose 尚无成功实测证据。见 P1-10/12/13/14/15/17。 |
| `:116` bootstrap 抢占测试：公网首访不能成为管理员 | `TestBootstrapCannotBeTakenOver`；`TestBootstrapLifecycle`；`TestBootstrapRejectsBadTokens`；补充并发探针仅一人成功 | **核心防抢占已覆盖，认证整体不能据此过关** | 补 HTTP 并发/过期边界/消费回滚、限流来源与资源上限测试；原有测试不覆盖 P0 两项。 |
| `:117` advisory lock 拦截双实例；迁移前备份与失败拒写实测 | 原有 `TestAdvisoryLockBlocksSecondInstance` 是同进程两次 Open；备份一致性、失败返回测试已存在；本次补测跨进程+SIGKILL、真实二进制备份失败退出 | **正常 server 路径成立，持久回归与例外不完整** | 将进程级测试纳入仓库；CLI 禁迁移；路径转义；真实 pending migration 的“不改变版本、不启动 HTTP”断言；升级恢复点保留。 |
| `:118` 两个 spike 有书面结论与 ADR | ADR-003 待执行；ADR-004 有 Alpine/arm64 可行性记录 | **未满足** | 修复 Spike 1 脚本并实测；Spike 2 补实际 runtime、amd64/arm64、边界与资源数据，或正式调整计划。 |

关键任务也存在以下范围偏差，不另重复计数：

| Phase 1 任务 | 核对结果 |
|---|---|
| Monorepo、ADR（`:105`） | backend/frontend/docs/ADR 已有；deploy、e2e 目录未见。根目录放 Dockerfile 本身不是架构错误，但应在计划说明布局调整。 |
| chi、health、slog、优雅停机（`:106`） | 已实现。真实 endpoint 为 `/api/healthz`；root `/healthz` 会走 SPA，文档应统一。没有桶检查，符合 liveness 原则。缺停机时在途请求、超时与清理的进程回归。 |
| 前端、embed、契约、运行时校验（`:107`） | 骨架和生成物存在，当前内容同步；使用链和门禁尚有 P1-09/10/12/13/14 的真实缺口。 |
| SQLite / goose / sqlc、锁、备份（`:108`） | SQLite/goose/PRAGMA/正常锁与拒绝启动有依据；sqlc 未引入，ADR-001 明确后移到 P2，应同步 dev-plan。VACUUM INTO 的替代选择需要记录，不能误称调用了 backup API。 |
| 认证与匿名表面（`:109`） | KDF、session、CSRF、token 已实现，但存在上述安全缺陷。`/api/ready` 公开是最小状态、无详细运行信息，仍超出“匿名仅最小 liveness”的原文字面范围；应明确是否接受并统一文档。metrics 尚无接口，未发现现成匿名 metrics 泄露。 |
| 许可证与 ADR（`:112`） | LICENSE、ADR-000/001/002 已有；ADR-000 明确不阻止合规托管，符合该条核心要求。没有将许可证文案当作法律保证作扩展结论。 |

## 测试缺口：具体到建议函数名

原仓库共 19 个 `Test*`：auth 9、db 4、server 6。config、limiter、入口程序没有测试；也没有前端 auth 流程或镜像 smoke 测试。以下为应纳入 Phase 1 回归的具体清单；本次临时探针不替代仓库测试。

| 建议位置 / 函数名 | 必须验证的行为 |
|---|---|
| auth：`TestBootstrapConcurrentTokensSingleAdmin` | 同 token、不同有效 token 并发；恰好一个用户、一枚 token 被消费，其余为确定业务错误。 |
| auth：`TestBootstrapConsumptionRollback` | 用户 INSERT 失败后，token 消费回滚；恢复故障后可重试。 |
| auth：`TestBootstrapTokenExpiryBoundary` | expires_at 等于 now 时拒绝；已使用但未过期也拒绝。用注入时钟。 |
| auth：`TestResetPasswordRacesLoginSessionCreation` | 旧密码验证后、session 插入前发生 reset，不能在 reset 完成后签发旧 generation。 |
| auth：`TestSessionExpiryWithoutPurge`、`TestDeleteSessionRevokesReplay` | 未运行清理任务也不接受过期 session；保存原 cookie 再用仍被拒，不能只检查浏览器 cookie 已清除。 |
| auth：`TestPurgeExpiredBootstrapTokens`、`TestResetPasswordUnknownUser` | token 清理确实删过期行；用户名不存在不会误删他人 session。当前 Purge 测试只实查 session。 |
| auth：`TestTokenHashesNeverPersistRaw` | 精确检查 session/bootstrap hash 格式与对应 SHA-256，不只确认一条 `WHERE id_hash=raw` 查不到。 |
| auth：`TestVerifyPasswordRejectsUnsupportedVersion`、`TestVerifyPasswordRejectsZeroAndExcessiveParameters`、`FuzzVerifyPasswordMalformedHash` | PHC 完整结构、参数资源边界、损坏数据不 panic。 |
| server：`TestLoginRateLimitIgnoresUntrustedForwardedHeaders`、`TestClientIPTrustedProxyChain` | 直连不信任任何代理头；可信链正确且不会受链首伪造控制。 |
| server/auth：`TestBootstrapInvalidTokenSkipsKDF`、`TestAuthKDFConcurrencyBounded` | 廉价拒绝、全局并发上限、取消/排队行为；已初始化端点也不能无限 KDF。 |
| server：`TestAuthBodyLimitBeforeDecode`、`TestAuthBodyReadDeadline` | 超限返回 JSON 413 且不进 KDF；慢 body 到期释放处理资源。 |
| server：`TestSameOriginSchemeAndEffectivePort`、`TestSameOriginMalformedOrigins` | scheme、默认/非默认端口、IPv6、userinfo、path、null、重复 Origin、可信 TLS 终止代理。 |
| server：`TestCSRFFailureMatrix`、`TestProductionCookieAttributes` | 无 cookie/无 header/不匹配、正常值、退出清除属性；Secure/HttpOnly/SameSite/Domain/Path/MaxAge 生产与开发配置都断言。 |
| server：`TestLoginSessionInsertFailureReturnsServerError`、`TestLogoutDeleteFailureDoesNotReportSuccess`、`TestSessionLookupDBFailureIsNot401` | 使用触发器、关闭 DB 等注入故障，检查状态、错误 schema、cookie 以及 session 是否真的被撤销。 |
| server：`TestAuthRequestRuntimeValidation`、`TestMalformedJSONUsesErrorSchema`、`TestAllGuardErrorsMatchOpenAPI` | empty/null/missing/wrong-type/trailing JSON/media type/字符边界；所有 4xx/5xx 遵守同一 Error。 |
| server：`TestGuardPathVariants`、`TestNewProtectedOperationDefaultsToAuthenticated` | 路径变体不暴露详情；以后增加受保护 operation 不因忘写路径表而匿名通过。 |
| db：`TestAllConnectionsHaveRequiredPragmas` | 同时占满四条连接，含连接重建后状态与 FK 实际拒绝行为。不能四次顺序查询复用同一连接。 |
| db：`TestAdvisoryLockAcrossProcesses`、`TestAdvisoryLockReleasedAfterSIGKILL` | 真正第二进程启动被拒，进程死亡留下 lock 文件不妨碍重启。 |
| db/CLI：`TestCLICannotMigrateWhileServerLocked`、`TestCLIRejectsSchemaVersionMismatch` | utility 可以正常短事务操作，但不能在在线服务旁迁移或跨版本写入。 |
| db：`TestOpenEscapesSQLiteURIPath`、`TestBackupNowSpecialCharacters` | 特殊目录名、文件名不会导致数据库别名或备份错误。 |
| db：`TestBackupSnapshotUnderConcurrentWrites` | WAL 活跃写入下用事务不变量验证快照，不只插入一行后备份；对输出执行 integrity_check/FK 检查。 |
| db/入口：`TestServeRefusesWritesWhenBackupFails`、`TestPendingMigrationNotAppliedOnBackupFailure` | 存在实际待执行迁移时注入权限/磁盘写入错误；版本未变、未监听、没有提供写 API。 |
| db：`TestMigrateNoPendingDoesNotCreateOrPruneBackup`、`TestFailedMigrationPreservesRollbackSnapshot` | 无迁移不刷掉旧快照；升级失败仍保留正确版本恢复点，失败产物不算成功。 |
| config：`TestLoadOrCreateSecretConcurrent`、`TestLoadOrCreateSecretRejectsWeakPermissions`、`TestLoadOrCreateSecretRejectsInvalidFileType` | 首次创建只有一个稳定 key；既有权限/类型、损坏和并发错误均明确处理。 |
| config/db：`TestOpenExistingDataDirPermissions`、`TestConfigSecureCookieDefault` | 专用数据目录、DB/WAL/SHM、secret 权限；默认安全 cookie，错误配置有确定行为。 |
| limiter：`TestLimiterCapacityDoesNotDropNewAttempt`、`TestLimiterWindowRollover`、`TestLimiterLockoutExpiry`、`TestLimiterConcurrentBudget` | 容量阈值、时钟边界、连续失败、成功重置和并发总额，全部使用 fake clock。 |
| 入口：`TestServeGracefulShutdown`、`TestHealthcheckTargetsConfiguredListener` | SIGTERM 等待在途请求、超时退出、释放锁；healthcheck 的默认地址、自定义 host/port、IPv6 与失败退出码。 |
| 前端：`testFreshInstanceCanReachBootstrap`、`testInitializedBootstrap409ShowsLogin`、`testLogoutClearsAuthenticatedView`、`testExpiredSessionLeavesDashboard` | 完整初始化/登录/过期/退出状态转换，包含服务 503 与授权 401 的区分。 |
| 构建/交付：`testCleanCheckoutBuildEmbedsAllAssets`、`testGeneratedGoDriftFailsCI`、`testImageHealthcheckAndVolume` | 干净 checkout、完整 JS/CSS、生成 drift 故意注入、新/旧/bind 卷、正确 UID、按页面命令 bootstrap。 |
| spike：`testSpike1PgToolArguments`、`testSpike1SchemaArgumentBoundaries`、`testSpike1CandidateFailureExitCode`；`testSpike2ReleaseRuntimeAMD64/ARM64` | 参数和失败语义先本地验证，再将真实 Supabase / 发布形态双架构结果作为单独集成验收产物。 |

## 总体结论与最关键的三件事

**结论：修改后通过。** 架构底座可继续沿用：没有必要重做 chi、SQLite、服务端 session 或 token bootstrap。当前存在可复现安全缺陷、错误成功响应、交付门禁失效和未完成的 spike，不能将“核心单元测试通过”作为 Phase 1 已完成的依据。

1. **先封住匿名资源攻击面。** 修复可信 IP 来源、bootstrap 的 KDF 顺序与限流，补 body 上限和全局 KDF 并发限制；同时修正完整 Origin 比较及会话持久化/撤销语义。
2. **让持久化与交付门禁可信。** 修复 secret 原子创建、CLI 迁移互斥、SQLite 路径和升级备份保留；纠正生成器路径、ESLint、make/embed 和新实例初始化流程，在干净 checkout 与真实镜像上验收。
3. **补完两项 spike 的真实结论。** Spike 1 先修脚本，再对新建 Supabase 目标验证；Spike 2 使用发布形态完成双架构与信任边界实测。把结果和持续回归写入仓库，按四条 DoD 重新验收。
