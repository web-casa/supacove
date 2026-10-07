# 文档站评审 1

日期：2026-10-07。结论：**NEEDS_FIXES**。

评审提交：`9cbf5e0be609c62f9a139202bdc1849a93b31fc3`（`git log -1`，docs: Fumadocs bilingual user documentation site）。范围：`docs-site/` 全目录、Makefile/CI 的 docs 接线及 `docs/docs-site-plan.md`。代码证据均以该提交为准。评审开始时工作树干净；安装、故意缺页、编译及测试均在 `/tmp/docs-review-x61f4j/` 的 `git archive HEAD` 副本中执行，仓库内只新增本报告。

主要阻断项是操作文档错误：源码构建命令不会构建控制台，密钥事故处置与实现不符，保留策略承诺超过实现，监控引用不存在的指标。另有语言切换器未接通。两种语言大体等价，但同步包含这些错误。

## 判定表

| 项目 | 判定 | 证据/说明 |
|---|---|---|
| 12 个 slug × 2 语言 | 通过 | parity 正常；Fumadocs loader 解析出 24 个页面 |
| index | 需修正 | 六阶段管线及密钥分离基本正确；自动续传保证缺少必要条件（P1-12） |
| quickstart | 需修正 | CLI/bootstrap/recipient 流程正确；“镜像内置 age”不成立（P1-02）；本地 HTTP 示例宜显式说明 cookie 模式 |
| installation | 需修正 | `--target runtime`、compose 开发用途、迁移快照正确；源码构建及依赖描述错误（P1-01/02） |
| configuration | 基本一致，需精确化 | 15 个运行时 SB_* 无缺项、默认值一致；权限与启动续传超时语义见 P2-01 |
| databases | 基本一致 | 对照 pgclient/platform 与前端 connection 预检，5432/6543、Neon `-pooler`、Railway 内网警告与 TLS 规则一致；UI 自动补 sslmode 的边界见 P2-10 |
| scheduling | 需修正 | 游标回滚/补发及双锚点正确；无锚点清理承诺错误（P1-05），暂停说明不完整（P2-03） |
| heartbeat | 基本一致，需精确化 | URL、period、年龄、远端提交四个门控均存在；年龄起点及禁用例外见 P2-02 |
| notifications | 基本一致，需精确化 | 三事件、30/60/120/240 秒、5 次失败后 dead 正确；日志 attempts 与事务保证边界见 P2-04 |
| backups | 需修正 | 六个任务状态、409 去重正确；presigned URL 认证边界错误（P1-08），验证枚举不准确（P2-05） |
| restore | 不通过 | 主密钥丢失、age init 行为错误（P1-03/04）；三场景与原灾备文档不一致，kit 描述有遗漏（P2-08） |
| monitoring | 不通过 | 无 `_total` 的主要名称正确；虚构 `_delivering`、告警阈值错误（P1-06/07），部分统计口径不完整（P2-06） |
| troubleshooting | 不通过 | client_version 成因、resume 日志、delivering 排查有误；删工件未交代后果（P1-09/11/12、P2-07） |
| 双语事实与结构 | 基本通过 | 对读全部同 slug 页面；唯一明确术语漂移是 zh `client` 对 en `client_version`；共性错误不能因 parity 通过而视为正确 |
| 未实现功能红线 | 部分不通过 | 未承诺 `/start` 心跳、redrive API；但列出未实现的 outbox delivering 指标。Trivy 已实际接入现有 CI，不能误判为未实现 |
| 版本钉定/lockfile | 静态检查及依赖展开通过；完整安装未验收 | 核心版本精确钉定、peer 范围匹配、lock v3 完整；缓存离线 `npm ci --ignore-scripts` 成功。完整生命周期脚本受环境限制 |
| 构建产物不入库 | 通过 | 47 个 docs-site 跟踪文件，无 `.next`、`.source`、node_modules、next-env、tsbuildinfo；忽略规则齐备 |
| 搜索 | handler 级通过；HTTP 冒烟未完成 | 实际 MDX 编译与 GET 调用，中英查询均 200；环境禁止监听端口 |
| 语言切换/导航 | 不通过 | 未传 locales，切换入口不显示（P1-10）；meta 格式无效（P2-09） |
| Makefile/CI | 基本通过，有非阻断建议 | docs job 独立安装/校验/构建；image 依赖 docs 是额外发布门禁，不是镜像的技术依赖 |
| 规划一致性 | 部分通过 | 独立站、不部署、页面集合实现；默认语言“一行切换”、验收 URL、灾备场景及危险操作说明未完全落实 |

## P1：会误导操作或缺失关键功能

### P1-01：源码安装命令产出缺少控制台的二进制

- 位置：`docs-site/content/docs/zh/installation.mdx:33`，en 同页 `make backend`。
- 文档称 `make backend` 会“构建前端并嵌入”。实际 `Makefile:24` 的 backend 只运行 `go build`；frontend 才执行 npm build 并复制 dist，`build: frontend backend` 才完成完整构建。
- `backend/internal/server/server.go:612` 明确在未构建 index.html 时返回 503。全新 checkout 按文档执行会得到无法使用控制台的安装结果。
- 修正：使用 `make build`，补齐 Node/npm 与 PostgreSQL 客户端等源码部署前提。副本 `make -n backend` 已复核。

### P1-02：把 age CLI 写成已内置的运行依赖

- 位置：zh/en `installation.mdx:15`；zh `quickstart.mdx:14` 与英文对应镜像说明。
- `Dockerfile:38` 起的 runtime 安装列表含 PostgreSQL 客户端和 PG18 服务端，不安装 age CLI；`backend/internal/agekey/agekey.go:63` 使用 Go 库 `filippo.io/age` 加密，并不调用外部 age。
- 独立恢复脚本则在 `backend/internal/recovery/kit.go:131` 附近检查 `age`、`pg_restore`、`psql`，缺工具退出 3。照文档把恢复环境理解为已配齐，会在灾备时失败。
- 修正：区分“服务内置 age 算法库”与“独立恢复主机需自行安装 age CLI、PG 工具和 SHA-256 工具”。不要宣称发布镜像内置 age 可执行文件。

### P1-03：主密钥丢失被错误描述为启动 fail closed

- 位置：zh `restore.mdx:33`–35；en `restore.mdx:35`–38。
- 实际 `config.LoadOrCreateSecret` 在文件不存在时生成新密钥（`backend/internal/config/config.go:224`–229）；文件损坏、权限不合格、符号链接等才拒绝加载。合法但换过内容的密钥也能启动，只是旧凭据解密失败。
- `docs/disaster-recovery.md` 场景 2 已明确区分这三种情况。文档站合并为“丢失/损坏均拒绝启动”会让操作者错误判断密钥丢失后的保护状态。
- 修正：保持原文三分法，先尝试找回原密钥，再说明重建数据库凭据、目的地及绑定；保留“备份密文仍由原 age 私钥恢复”的正确结论。

### P1-04：丢失 age 私钥后，不能在原实例直接用 age init 轮换

- 位置：zh `restore.mdx:37`–42；en `restore.mdx:40`–47。
- `backend/cmd/supabackup/main.go:433` 附近读取现有 `age_recipient`，非空直接报 `age recipient already configured ... rotating requires a deliberate decision`。
- 丢私钥不等于丢 SQLite 中的 recipient；主密钥也丢失仍不保证 recipient 消失。实例会继续用旧公钥产生无法恢复的备份，文档所述 `age init` 不会换掉它。
- 修正：明确目前没有可直接执行的轮换子命令，不把 `init` 当轮换接口；给出经过核验的新实例重建流程及原记录保全要求。已有密文不可恢复的判断成立，但必须与“恢复未来保护”步骤分开。

### P1-05：“无锚点不做破坏性自动清理”遗漏失败工件 TTL 例外

- 位置：zh `scheduling.mdx:32`–34；en 同页末尾 Callout。
- 成功备份保留清理的锚点保护确实存在；但 `backend/internal/jobs/upload.go:378`–383 的 `PruneExpiredArtifacts` 只要求工件已提交、任务为 failed/canceled/interrupted 且 finished_at 超过 TTL，**不检查成功锚点**。
- 反例：所有任务上传失败、只有本地密文，默认 72 小时后仍会清理。这正是文档括号内“全部未提交成功”的情况。
- 修正：把无锚点保护限定到成功/远端提交备份的保留清理，单独强调失败工件 TTL 不受该保护，必要时先导出保全或配置 TTL=0。

### P1-06：监控列出不存在的 supabackup_outbox_delivering

- 位置：zh `monitoring.mdx:20`；en `monitoring.mdx:21`。
- `backend/internal/server/metrics.go:176`–184 仅输出 `supabackup_outbox_pending` 和 `supabackup_outbox_dead`；`outbox.Counts` 也只查询这两类。`delivering` 是通知状态，不是现有指标家族。
- 依此配置 PromQL 会得到空向量，而非投递中数量，可能形成静默监控缺口。
- 修正：删去虚构家族；需要查看 delivering 时指向投递日志/API，不承诺新指标。

### P1-07：告警阈值错误地额外加了一次计划周期

- 位置：zh `monitoring.mdx:36`；en `monitoring.mdx:41`–42。
- 页面建议“计划周期 + 新鲜度阈值”。实际 `scheduler.go` 的 tick/lastSuccessAge 与 `metrics.go` 的 protection 判定都是快照年龄直接比较 `max_age_hours`。
- 例如日备、阈值 26h，产品会在 26h 判 EXPIRED，照文档的告警要等 50h。cron 也未必具有固定可相加的周期。
- 修正：说明指标时间是 `started_at`（成功任务的快照近似起点），以 `time() - timestamp` 与配置的最大年龄比较；不要把 heartbeat 的 period+grace 混入 max_age_hours。未成功过的数据库还需监控 protection 的 never 状态。

### P1-08：presigned URL 下载并不要求 supabackup 登录会话

- 位置：zh `backups.mdx:33`–37；en `backups.mdx:39`–42。
- 三个应用 API 确实受会话保护，但 `backend/internal/server/api_phase3.go:213`–245 生成的是 15 分钟有效的 bearer URL，代码注释也明确称其为 bearer secret。
- 获得链接的人可直接访问对象存储，无需 supabackup cookie。“三者都需会话；匿名一律 401”会误导用户把有效链接当成仍受应用登录保护的普通 URL。
- 修正：区分“获取链接需登录”和“持有链接即可下载”，说明 URL 的保密与有效期。

### P1-09：把 client_version 的首要成因指向池化端点

- 位置：zh `troubleshooting.mdx:16`–19；en `troubleshooting.mdx:18`–22。
- `backend/internal/dumper/dumper.go:113`–135 在找不到合适版本或缺少 pg_dump 时产生 client_version；`jobs/remediation.go:31` 对应建议是安装匹配/更新客户端。池化提示是独立警告，不把这一类错误改成池化错误。
- 更换主机/端口无法修复缺客户端或旧版本；“大概率”也没有实现依据。中文还误写为并不存在的 `client` 类。
- 修正：按 network、client_version 分开排查；池化作为独立检查项，保留准确的 Supabase/Neon 规则。

### P1-10：语言切换回调存在，但界面不显示切换器

- 位置：`docs-site/lib/providers.tsx:12`–17、`app/[lang]/docs/layout.tsx:15`。
- Provider 只传 locale/onLocaleChange，没有传 locales。锁定的 fumadocs-ui 16.16.2 中，`dist/contexts/i18n.js:12` 默认 `locales=[]`；`dist/layouts/shared/client.js:77`–87 默认以 `locales.length > 1` 决定是否显示 languageSelect。本站也没有显式启用或提供另一个切换入口。
- 因此这不是“回调应移到 client”的问题，而是用户根本没有按钮可点击，必须手动改 URL。
- 修正：按锁定版本 API 提供 zh/en 的 locale 列表/显示名或官方 i18nProvider 结果，并验证同 slug 来回切换。中文 UI 翻译也应配置；仅传 locale 不会自动翻译 Search 等界面文案。参考 [Fumadocs Translations](https://www.fumadocs.dev/docs/ui/translations)。

### P1-11：紧急删 failed 工件未说明不可逆的恢复损失

- 位置：zh `troubleshooting.mdx:29`–31；en `troubleshooting.mdx:33`–36。
- 文档授权手动删 failed 的密文，却只警告不要删 succeeded 锚点。`jobs.go:917` 起的上传失败路径恰恰会保留已经成功导出的密文；该文件可能是唯一可恢复副本。failed 不等于密文无用，也不会自动进入 interrupted 的启动续传路径。
- 这违反计划末尾“清暂存给出后果说明”的明确要求。
- 修正：说明删除会永久失去该副本的手动恢复能力，先核对 job/工件并保全仍有价值的密文、manifest；勿按通配符盲删，不能把 failed 状态当安全删除依据。

### P1-12：把“本地密文已提交”当作自动续传的充分条件

- 位置：zh/en `index.mdx` 的重启保证、`installation.mdx` 升级段、`troubleshooting.mdx` interrupted 段。
- `jobs.go:324`–330 的查询还要求 `status='interrupted'`、`remote_state IN ('uploading','committed')`；随后要求目的地可用、工件及 manifest 可读。
- 在本地提交后、记录远端 intent 前停机，不满足 remote_state 条件，不会因存在密文就自动续传；local-only 也不走该续传路径。失败任务更不会因重启自动重传。
- 排障要求寻找 `resume:` 行也不正确：这些主要是错误/跳过日志，成功日志为 `resumed remote commit completed`（`jobs.go:449`）。没有 `resume:` 不能说明没执行续传。
- 修正：列出续传资格和成功/失败日志，说明不能续传时仍可能手动恢复；TTL 从 finished_at 计，并且启动先尝试续传、再做过期清理，不是超过 72h 就在启动前必然消失。

## P2：措辞、口径与规划偏差

| 编号 | 文档位置 | 实现证据与应改内容 |
|---|---|---|
| P2-01 | zh/en `configuration.mdx:35,43` | “强制 0600”应为普通文件、非符号链接、无 group/other 权限并可读；`config.go:165` 也允许 0400。jobTimeout 普通运行到期按 network 失败正确，但启动续传超时在 `jobs.go:409`–425 恢复 interrupted、不发失败通知，表述不能无条件涵盖两条路径。quota=0 仍保留 64 MiB 文件系统空闲检查（`dumper.go:249`），建议在“不限”后说明。 |
| P2-02 | zh `heartbeat.mdx:17,24` 及 en 对应段 | 年龄门控用本次 `dumpStart` 到现在（`heartbeat.go:86`），不是完成时间，也不是定时查询“最新成功记录”后持续 ping；长耗时成功仍可能不 ping。URL 的 `-` 是 period 必填校验的例外（`api_phase7.go`）；空 URL 继承兜底时即使 API 接受 period=0，成功 ping 仍被禁止。说明这些边界，并明确外部 monitor 仍需单独配置周期。 |
| P2-03 | zh `scheduling.mdx:14`–16；notifications 的 backup_expired 定义 | `scheduler.go:150` 起在 freshness 检查前跳过 paused，所以暂停也停止该库新生成的过期事件，不只是停止 enqueue；现有 outbox 仍会投递。never-success 库在 lastSuccessAge 返回 1e9，也会产生 backup_expired，不能限定成“曾有最新成功且过期”。另建议交代首次 cursor=0 会立即调度，而非等下一 cron 时刻。 |
| P2-04 | zh `notifications.mdx:17,25` 及 en 对应段 | `attempts` 是累计失败轮数，succeed 不加一（`outbox.go:338`–368），不是实际 HTTP 请求总次数；一次轮询还可能向多个目标 POST，重试会再次访问已成功目标。普通失败终态/outbox 原子写入正确，但 `jobs.fail` 有仅保存失败状态的补偿分支，`scheduler.reconcileNotifications` 后补事件；“不会出现状态变了但没通知”应表述为持久补偿保证，不能承诺任何时刻都同事务完成。 |
| P2-05 | zh `backups.mdx:20`–28；en 同段 | verifyStatus 实际有 pending/running/verified/failed/unsupported/skipped；`not verified` 是 UI 标签，不是枚举（`jobs/verify.go:89`、`frontend/src/lib/status.ts:58`–82）。七个已分类错误之外还有 unknown（`pgclient.go:28`），且缺 pg_dump 也属于 client_version，不仅仅是“比服务端旧”。 |
| P2-06 | zh `monitoring.mdx:17,19,28`，en 对应段 | last_success_timestamp 取 MAX(started_at)，不是完成时刻。remote 两项是查询 jobs 得出的 gauge（committed/deleted；failed+storage_upload），不是独立终身累计计数器；补上无 `_total` 改名/禁止按 counter 理解的说明。export 分母还限定终态，运行中的任务不参与（`api_phase7.go:436`–459）；归档样本小计、平均耗时仅成功且正值/无样本为空的核心描述正确。 |
| P2-07 | zh `troubleshooting.mdx:40`；en 同段 | “delivering 卡住通常是接收端不返回”遗漏 10 秒 HTTP timeout（`outbox.go:141`），长时间滞留还需查崩溃/结果写入失败、15 分钟租约及启动回收。死队列说明和事件 ID 去重建议正确。不要让操作者无限等待无返回的接收端。 |
| P2-08 | zh `restore.mdx:16,27,30` 及 en 对应段 | kit 不止“输出表数”：hash 通过后先拒绝非空目标，再解密/恢复，最后在 manifest 表数已知时做相等检查；失败可能部分写入（`recovery/kit.go:169` 起）。manifest 的 backupId 当前是 `job-N`（`jobs.go:866`），不能称为远端对象使用的 UUID。站点“三类密钥事故”也不是原灾备文档前三场景：缺 SQLite 损坏/数据卷丢失、保留原 recipient 的重建入口；只有无法点击的仓库路径文字引用。应恢复原场景导航，可另列 age 丢失场景。 |
| P2-09 | `content/docs/{zh,en}/meta.json:1` | Fumadocs metaSchema 接受 title/pages 等字段，不接受 slug→标题对象；实际 schema.parse 两份文件都得到 `{}`。loader 导航变成 index 后按文件名排序，quickstart 排第 9 项，未遵循文件里写的顺序/短标题。改成合法 pages 数组，标题按受支持方式提供；当前 README 声称 meta 定义侧栏标题不成立。 |
| P2-10 | zh/en `databases.mdx` TLS 段；`installation.mdx` 目录/工具链段 | 后端确实拒绝远端缺 sslmode，但 UI `frontend/src/lib/connection.ts` 会补选定 sslmode、过滤不支持参数；“未指定必被注册拒绝”应限定 API 原始输入，本地 prefer 也只是后端 ParseURI 缺参数时默认。UID 10001 仅容器成立，源码/systemd 由运行用户决定；“更低工具链直接拒绝”也需考虑 Go 自动工具链选择，准确要求是最终使用 Go 1.26.6+。 |
| P2-11 | `app/page.tsx:4`、`app/layout.tsx:8`、`docs/docs-site-plan.md:13,17,62` | 默认入口硬编码 `/zh`，修改 lib/i18n.ts 一行无法切换默认语言。根 html 总是默认语言，英文只在内层 div 标 lang。计划写 zh-CN、实际 zh；计划验收 `/zh/quickstart` 和 `/en/quickstart`，实际合法地址是 `/{lang}/docs/quickstart`。统一语言标识及验收地址，根入口读取配置，页面 html 语言随路由。 |
| P2-12 | `docs-site/README.md:11`–13、`mdx-components.tsx:1`、文档 Page 的 `<MDX />` | “Fumadocs v16 has no global component map”不准确：锁定 UI 包仍导出 `fumadocs-ui/mdx`。本站定义 getMDXComponents/useMDXComponents，却既没传给 MDX 的 components，也没在 source.config 设置对应 providerImportSource；不能把该文件当成已自动接入的 MDX v3 功能。显式使用组件映射并更正文案，或删除无效描述/闲置映射；当前逐页显式导入的 Callout/Tabs 等可以编译。 |

以上 P2 表中“建议交代”的边界不单独升级为 P1；P1 项均有具体错误操作、错误监控或关键入口缺失的后果。没有把无法在本环境完成的测试当作代码缺陷。

## configuration 逐项核对

依据：`backend/internal/config/config.go:80`–168、`backend/cmd/supabackup/main.go:96`–107、232–238 及 Dockerfile 的 ENV。表中“通过”不表示所有边界已完整写入页面。

| 变量 | 实际默认/语义 | 判定 |
|---|---|---|
| SB_DATA_DIR | `./data`；镜像 `/app/data` | 通过 |
| SB_ADDR | `:8080` | 通过 |
| SB_LOG_LEVEL | info；debug/warn/error 显式分支，未知值亦回落 info | 通过 |
| SB_SECRET_FILE | `<SB_DATA_DIR>/secret.key` | 页面“数据目录内”不矛盾，建议写全文件名 |
| SB_PUBLIC_ORIGIN | 空；有配置时精确比较 origin，否则按请求 scheme/host | 通过 |
| SB_TRUSTED_PROXIES | 空；逗号分隔 CIDR，默认不信任转发来源 | 通过 |
| SB_INSECURE_COOKIE | false；控制 Secure 及 cookie 命名 | 通过，开发警示存在 |
| SB_LOCAL_KEEP | 5；必须正整数；成功备份保留窗口，最新成功和最新 verified 额外保护 | 通过，不是所有状态工件的数量上限 |
| SB_STAGING_QUOTA_BYTES | 0；非负；按暂存使用量限制新写入，0 仍有空闲空间底线 | P2-01 补充 |
| SB_JOB_TIMEOUT | 6h；Go duration、非负、0 禁用；普通运行与启动续传结算不同 | P2-01 |
| SB_FAILED_ARTIFACT_TTL_HOURS | 72；非负、0 禁用该 sweep；从 finished_at 起算 | 默认通过；与锚点关系见 P1-05 |
| SB_VERIFY_ENABLED | false；开启内嵌 PG 恢复验证 | 通过 |
| SB_VERIFY_IDENTITY_FILE | 空；开启验证时必填；普通文件、禁止符号链接/宽权限 | P2-01 |
| SB_VERIFY_PGBIN | 空；默认探测 `/usr/lib/postgresql/{18..14}/bin` 的服务端工具 | 通过 |
| SB_HEARTBEAT_URL | 空；库级为空才继承；`-` 禁用；继承也受 period/年龄门控 | 通过，四门控需连同 heartbeat 页阅读 |

bootstrap TTL=15 分钟、session TTL=7 天均为固定值，不是缺失的环境变量。

## 其余重点核验与范围边界

- **四平台**：pgclient/platform/connection 的池化与端口规则一致。Railway 私有域警告、Supabase IPv6 提示、Neon 冷启动提示均可在现有前端/排查文案对应到；未执行真实供应商连接测试，不把代码里的提示升级为本次已验证的平台兼容性承诺。`host.docker.internal` 对 Linux Docker 还可能需要 host-gateway 配置，页面宜交代运行环境。
- **保留/游标**：远端 newest committed + newest verified，按数据库与目的地组合保护；本地 newest succeeded + newest verified，另跳过 pending/running 验证工件。调度 enqueue 与 cursor 同事务；冲突回滚 cursor，结束后补发一次，页面这一点准确。
- **心跳**：实现检查有效 URL、未禁用、远端提交或 local-only、正 period、dumpStart 年龄不超 period+grace；文档列举顺序不同不影响逻辑。失败 ping 跳过年龄/period gate、仍尊重禁用，`/fail` 插在 query 前。没有 `/start` 功能承诺。
- **通知**：Backoff 计算为 30/60/120/240 秒，失败五轮变 dead，不会自行 redrive；无匹配 webhook 时直接标 delivered。接口有同步测试、通知列表，没有本次文档声称存在的 redrive API。SSRF 的 link-local/metadata 拦截与 LAN/loopback 允许相符。
- **任务/kit**：succeeded 与恢复验证分离正确；同库 pending/running 冲突返回 409。kit 的 SHA-256 门禁、绝对 AGE_IDENTITY_FILE、PGPASSWORD 示例正确；恢复不依赖 SQLite，但需要可用工具及预建目标，表数检查也不是完整业务数据一致性证明。
- **监控**：认证 guard 对 `/metrics` 生效且 no-store；jobs、verification、last_success_timestamp、databases_protection、remote、staging、outbox pending/dead、scrape_errors 的已实现名称均无误。scrape_errors 仅异常时出现，staging 失败仍输出尽力值。页面未列 uptime/goroutines/heap、旧 jobs 别名不单独视为错误，但不能暗示列出的表已穷尽所有家族。
- **双语**：12 对页面的主题、表格、步骤顺序基本等价。英文 quickstart 省略令牌输出示例，时区示例不同，均不构成事实漂移。zh 的 `client` 是明确不等价点（P1-09）。
- **Trivy**：`.github/workflows/ci.yml` 的 image job 已配置 `aquasecurity/trivy-action@0.33.0`，CRITICAL/HIGH、exit-code=1、ignore-unfixed=true；本次并未运行镜像扫描，不能称扫描结果已通过。

## Fumadocs、构建和 CI 评估

核心依赖锁为 Next 16.4.0、React/ReactDOM 19.3.0、fumadocs-core/ui 16.16.2、fumadocs-mdx 15.4.6。实查包声明：UI 要求相同 core 版本、Next 16.x、React ^19.2；MDX 接受 core ^16.15.3、Next ^15.3 或 ^16、React ^19.2。组合满足 peer 范围，MDX 与 UI major 不同并不构成错误。其余依赖允许 caret，但 lockfile 已固定解析版本；378 个非根包均有 integrity。未用“最新版本”代替兼容性证据，也未宣称完成线上 npm registry 重新解析或全平台安装。

source 的 `parser: dir`、带语言前缀的 `/docs` baseUrl、static params、getPage(lang)、search createFromSource 接线合理。搜索客户端取得 locale、服务端按 locale 过滤，handler 级实验没有发现中英文串页。官方 [Search UI](https://www.fumadocs.dev/docs/ui/search) 也采用 locale 驱动客户端查询；本报告的运行结论仍以锁定包和实际 handler 实验为准。

Makefile 的 docs/dev、docs-build 两个目标都先 npm ci；后者走包含 parity 的 npm run build。CI docs job 使用 Node 22、独立 npm cache key 路径，先 parity 再 next build，没有混入 Go/Docker 依赖，符合规划。image 的 `needs: [..., docs]` 不服务于镜像构建输入，属于“文档也必须过才构建镜像”的额外质量策略：可以保留为明确发布门禁，也可移除以减少耦合/避免纯文档故障阻塞镜像验证。本次不将这一策略选择列为阻断缺陷。

`check-parity.mjs` 对当前 .mdx 目录结构有效；它只保证 slug 集合一致，不保证译文语义、侧栏或语言按钮正确。本站暂无 .md 页面，故未把不枚举 .md 作为当前缺陷。

## 验证记录

环境：Linux arm64，Go 1.26.6；主要 Node 为 `/home/ivmm/.nvm/versions/node/v22.22.2/bin/node`，另以 Node 20.19.2复核安装失败。网络及端口监听受沙箱限制。没有请求越权或使用 Docker。

| 验证 | 结果 | 记录/边界 |
|---|---|---|
| git log/status/diff/ls-files | 通过 | HEAD 与上述 SHA 一致；初始工作树干净，47 个 docs-site 跟踪文件，无构建产物 |
| 在线 `npm ci`，独立空缓存 | 环境失败 | registry.npmjs.org DNS `EAI_AGAIN`；日志在 `/tmp/docs-review-npm-cache/_logs/` |
| 复制本机对应缓存后，Node22 `npm ci --offline` | 环境失败 | esbuild install.js 的 spawnSync 返回 EPERM（其 stdout 已输出 0.28.2）；Node20 同样失败。未归因于 lockfile |
| `npm ci --offline --ignore-scripts --no-audit --no-fund` | 通过 | 干净副本 added 290 packages；只能证明当前平台依赖展开与 lock 一致，不能冒充完整 npm ci 成功 |
| 正常 parity | 通过 | `parity ok: 12 slugs in both locales` |
| 副本移走 en/heartbeat.mdx 后 parity | 通过负向测试 | 退出 1，`en missing: heartbeat`；恢复后再次退出 0。仓库原页未动 |
| `npx --no-install next build` | 未成功 | 前次生命周期安装不完整，不能据此判站点构建失败 |
| 补齐依赖后直接 `node .../next build` | 未验收 | Next 16.4、MDX 文件生成成功，停留 optimized production build，等待数分钟无完成输出后中止；没有宣称构建绿 |
| 诊断性 `next build --webpack` | 未验收 | `Could not parse output from TypeScript's --showConfig`；未定位到站点代码，不能作为确定产品缺陷 |
| `node node_modules/typescript/bin/tsc --noEmit` | 通过 | 退出 0，日志为空；不替代完整 Next build |
| `next start --hostname 127.0.0.1 --port 4317` | 环境阻塞 | `listen EPERM 127.0.0.1:4317`；没有 `/`、两语页面的真实 HTTP/browser 冒烟结果 |
| Fumadocs runtime 编译真实 24 篇 MDX、loader + search GET | 通过 | 中文“心跳”200/7 项、SB_JOB_TIMEOUT 200/2 项；英文 heartbeat 200/7 项、restore 200/22 项，前列 URL 均属请求语言。直接 Request→Response，无 TCP，不能替代 next start 冒烟 |
| metaSchema 与 pageTree | 揭示 P2-09 | 两份 meta parse 都是 `{}`；真实 loader 顺序中 quickstart 在第 9 项 |
| `make -n docs docs-build backend` | 通过检查 | 确认 docs 接线及 backend 不构建前端 |
| Go config/platform/pgclient/recovery 测试 | 通过 | 在副本运行；scheduler 包无测试文件 |
| Go outbox 整包测试 | 环境阻塞 | TestDeliverySuccessAndRetry 的 httptest 监听 `tcp6 [::1]:0` 报 operation not permitted |
| Go outbox 定向 `-run 'TestBackoff\|TestEnqueue'` | 通过实际匹配测试 | 当前匹配到 TestEnqueueDedup；未据此宣称 Backoff 动态测试已运行，退避序列按实现核算 |

补充实验代码与输出保留在 `/tmp/docs-review-x61f4j/validation/docs-site/review-search.mjs`、`search.log`、`npm-ci.log`、`npm-ci-node20.log`、`next-build-direct.log`、`next-build-webpack.log`、`tsc.log`、`go-tests.log`（日志位于临时根目录，脚本位于副本）。

复评应先修正上述事实与 i18n 接线，再在允许正常生命周期脚本和监听端口的环境完成 **原样 npm ci → npx next build → next start**，访问 `/`、`/zh/docs/quickstart`、`/en/docs/quickstart`，实际点击语言切换并从搜索结果进入页面。当前存在独立于环境限制的确定 P1，故结论为 **NEEDS_FIXES**。
