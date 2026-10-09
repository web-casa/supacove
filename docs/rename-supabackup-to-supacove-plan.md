# 开发方案：supabackup → SupaCove 全库重命名（v5 最终版）

状态：**已执行完毕**（2026-10-09，六步全部落地，各步均经 codex 评审后提交）
日期：2026-10-09
分支：`rename/supabackup-to-supacove`（每步一个提交，独立可构建）

## 1. 背景与现状盘点

GitHub 仓库已改名 `web-casa/supacove`（remote 已指向新地址），docs-site 与前端 UI
已用 SupaCove 品牌（109 处），但代码、构建、文档仍残留 **548 处**（默认扫描，尊重
gitignore；加 `--hidden` 含 `.github/`、`.golangci.yml` 等约 563 处）`supabackup`
字样，三种写法：`supabackup` ×514、`SUPABACKUP` ×20、`Supabackup` ×14；无连字符变体。

排除 `docs/reviews/**`（约 100+ 处历史评审）与 CHANGELOG 历史条目后，活跃范围
约 440+ 处。按处理方式分层：

| 类别 | 位置 | 要点 |
|---|---|---|
| Go 模块路径 | `go.mod` + 45 文件 142 处 import；**`.golangci.yml:53` 的 FQN 豁免** | FQN 漏改会让 lint 豁免失效 |
| CLI/二进制/构建 | `backend/cmd/supabackup/`、main.go、Makefile、Dockerfile(.spike)、ci.yml、scripts/、compose.yaml、ISSUE_TEMPLATE | 版本输出与 CI tag 任务 grep 强耦合 |
| 运行时数据文件 | `supabackup.db{,-wal,-shm}`、`supabackup.lock`（db.go） | 唯一 P0：升级即"静默重置" |
| 对外契约 | metrics（**13 个活跃指标名** + 2 个已弃用快捷名，29 处字样含注释/HELP）、webhook 头（**两条发送路径**：outbox.go:323 与 api_phase7.go:379 的测试投递）、套件环境变量、OpenAPI（**含 :1244 的 CLI 文案**）、GHCR 镜像名、pg_stat_activity 的 `application_name`（pgx.go:48） | 破坏下游 |
| 运行时可见杂项 | kit.go:185 临时目录前缀 `supabackup-restore.`（与 kit_test.go:243 泄漏检测耦合）、s3.go:283 诊断体、remediation.go 双语提示、i18n 串、config.go:23 / netguard.go:2 等注释、agekey_test.go:74 / m1_test.go:290 等测试文案 | 成对/顺带修改 |
| 文档/品牌 | README、CONTRIBUTING、SECURITY、deploy/、docs/*.md、docs-site、frontend 少量 | 低风险 |

## 2. 命名规范（token 映射）

| 场景 | 旧 | 新 |
|---|---|---|
| 二进制 / CLI / Go 包目录 | `supabackup` | `supacove` |
| 展示名（标题/品牌位） | `Supabackup` | `SupaCove` |
| 句中普通首字母大写形式 | `Supabackup` | `Supacove` |
| 套件环境变量 | `SUPABACKUP_PROFILE` / `SUPABACKUP_ALLOW_NONEMPTY` | `SUPACOVE_*`（旧名回退，见 §4.4） |
| Prometheus 指标 | `supabackup_*` | 13 个活跃名改 `supacove_*`；旧名别名见 §4.4 |
| webhook 头 | `X-Supabackup-Event(-ID)` | `X-Supacove-Event(-ID)`，旧头并存 |
| Go module | `github.com/cloudfan/supabackup` | `github.com/web-casa/supacove` |
| 镜像 | `ghcr.io/web-casa/supabackup` | `ghcr.io/web-casa/supacove` |
| 数据文件 | `supabackup.db` / `supabackup.lock` | `supacove.db` / `supacove.lock`（§4.3 迁移协议） |
| 容器用户 | `supabackup`（uid 10001） | `supacove`，**uid 不变**；systemd `User=`/`StateDirectory=` 见 §4.5 升级须知 |
| npm 包名 | `supabackup-frontend` / `supabackup-docs` | `supacove-frontend` / `supacove-docs` |
| OpenAPI | title 及 `:1244` CLI 示例 | `SupaCove API` / `supacove bootstrap` |
| pg application_name | `supabackup` | `supacove` |
| kit 临时目录前缀 | `supabackup-restore.` | `supacove-restore.`（测试同步） |

机械替换规则：`supabackup→supacove`、`Supabackup→Supacove`、`SUPABACKUP→SUPACOVE`。
品牌展示位（标题、句首产品名）人工核对为 `SupaCove`，句中普通大写形式用 `Supacove`。

**替换与扫描一律用字面子串匹配 `supabackup`（忽略大小写），不要用 `\bsupabackup\b`
整词边界**——下划线与数字属于单词字符（`\w`），`\b` 会漏掉 `supabackup_uptime_seconds`、
`SUPABACKUP_PROFILE`、`supabackup_0.1.0` 这些本次最重要的前缀标识；而字面子串
`supabackup` 本身不会误伤 `supabase`（平台名）。

## 3. 明确不改清单（负面清单）

1. `docs/reviews/**`：历史评审记录。同类历史规划/证据文档一并保留原名：
   `docs/dev-plan.md`、`docs/followup-fix-plan.md`、
   `docs/release-scan-evidence.md`、`docs/seo-plan.md`（它们记录的是当时的
   决策与证据，改写即失真；Step 5 起列入白名单）。
2. CHANGELOG 历史条目（以当时名字发布的版本）；仅新增 `[Unreleased]` 条目并更新文件头。
3. **`supabase` 平台名词**：`guides/supabase-backup.mdx` 的文件名与 slug、
   `spike1-supabase-restore.sh`、`ADR-003-supabase-recovery-profile.md`、
   `kit_supabase_test.go`、文档中"备份 Supabase"类语义内容（这些文件内部指向产品
   的 `supabackup` 字样仍要改，文件名/标识不动）。
4. **`SB_` 环境变量前缀**（~15 个）：不含目标字样，改了会破坏所有已部署实例配置；
   如未来要改需双读兼容并单独评审。
5. **compose.yaml 的 `MINIO_ROOT_USER: supabackup`**：这是已保存的开发凭据——
   现有开发实例的 S3 目标配置里存着它，重建环境后旧目标会认证失败。保持凭据不变，
   只更新注释。
6. **`api/openapi-baseline.yaml`**：冻结的契约基线，oasdiff 的 breaking 检查不覆盖
   info.title；按惯例（Makefile:72 注释）在 release tag 时人工刷新，CI 无自动刷新步骤。
7. gitignored 产物（`bin/`、`dist/`、`docs-site/out/`、`.next/`、`data/`）：重建即可。
   本机 `data/supabackup.db` 由 §4.3 迁移代码处理。
8. 本地 checkout 目录名 `cloudfan-supabackup`；git 历史（不重写）。

## 4. 实施步骤

### Step 0 — 基线
`make check && make e2e` 确认起点为绿；记录扫描基线（§6 终检命令）。

### Step 1 — Go 模块路径（`refactor: rename module to github.com/web-casa/supacove`）
- `go.mod` + 142 处 import + **`.golangci.yml:53` 的
  `(*github.com/cloudfan/supabackup/...Store).Close` FQN**（同提交，否则豁免失效）。
- `gofmt -w` 后跑 **`make lint`**（不只是 `go build`）+ `make test`；文档中
  `cloudfan/supabackup` 字面引用一并清零。

### Step 2 — 二进制 / CLI / 构建 / CLI 侧文案（原子提交）
- `git mv backend/cmd/supabackup backend/cmd/supacove`；main.go doc 注释、usage 串、
  `version` 输出 `supacove %s (commit %s, built %s)`。
- **CLI 相关源文案同提交修改**：`api/openapi.yaml`（title、description、`:1244` 的
  `supabackup bootstrap` 示例）→ `make api-gen` 重新生成 `api.gen.go` 与
  `frontend/src/api/schema.d.ts`；`frontend/src/components/AuthScreen.tsx:101` 的
  `/app/supabackup bootstrap` 指引（否则 Step 2 构建出的 UI 在教用户调用不存在的命令）。
- Makefile：`BIN`、构建目标、release 归档名 `supacove_<v>_<os>_<arch>`、SHA256SUMS glob。
- Dockerfile(.spike)：`/out/supacove`、COPY、ENTRYPOINT、healthcheck、
  `useradd supacove --uid 10001`。
- ci.yml：镜像 tag、`/app/supacove bootstrap`、**`grep -F "supacove ${GITHUB_REF_NAME}"`
  （与版本输出同提交）**、release 资产名、OCI label、`ghcr.io/web-casa/supacove`。
- scripts/、compose.yaml 注释（MinIO 凭据不动，见 §3.5）、ISSUE_TEMPLATE。
- **验证**（注意：CI 的版本 grep 只在 tag 发布任务跑，`make check`/e2e 不覆盖）：
  `make check`、`make e2e`、`make image` 后本地容器跑 `bootstrap`；
  `make release VERSION=v0.0.0-verify` 检查归档名/内部二进制名/SHA256SUMS/
  `./supacove version` 输出。

### Step 3 — 运行时数据迁移（P0 正确性工作）

**决策点**：备选简案是"永久保留 `supabackup.db`/`supabackup.lock` 文件名并列入
白名单"（零迁移风险）。本方案默认**改名 + 受控迁移**（一次性成本，避免品牌文件名
永久残留）；若评审认为迁移不值得，可退回简案，仅改 §2 表格并白名单这两个文件名。

`backend/internal/db/db.go` 迁移协议（每条都有对应测试）：

1. **锁顺序**：`open(dir, takeLock=true)` 先获取**新锁** `supacove.lock`
   （LOCK_EX|LOCK_NB，串行化所有新进程）；随后**无条件创建并以排他方式持有旧锁
   `supabackup.lock`**（不管它此前是否存在——条件式获取会漏掉"全新目录/只恢复了
   数据库文件的目录"下新旧 server 并行的场景），**兼容期内（两个 tag 版本）于整个
   进程生命周期持有两把锁**；旧锁获取失败（EWOULDBLOCK）→ 返回明确错误："旧版本
   实例正在运行，请先停止再升级"。
2. **预检先行，任何改名之前完成——显式状态机**：一次性快照六个文件名
   （旧/新 × db/wal/shm）的存在性，只放行三种状态，**其余一律报错拒绝、不动任何
   文件**（错误信息列出实际存在的文件与处置建议）：
   - **A 待迁移**：旧主库存在、新主库不存在、同类侧文件无双份 → 执行/续迁
     （侧文件可处于任意新旧混合，逐文件补齐改名，主库最后——自然涵盖
     "旧主库＋新WAL＋旧SHM"等一切正向中断组合）；
   - **B 已迁移**：新主库存在、旧主库不存在、且无任何旧侧文件 → 跳过迁移直接
     打开（新主库＋旧 WAL 属配对错误 → 拒绝）；
   - **D 全新**：六个文件都不存在 → 直接初始化新库（空目录首装）。
   明确拒绝的组合例如：双主库并存；新主库＋旧侧文件（会把旧 WAL 配到新库）；
   任何"有侧文件而无主库"；同类侧文件双份。迁移完成后断言终态为 B。
3. **迁移仅在持锁路径执行**；顺序：先 `supabackup.db-wal`、`supabackup.db-shm`，
   最后 `supabackup.db`（主库最后落位，保证中断后重跑可完成；先改主库再崩溃会让
   下次启动因新库存在而跳过迁移，打开缺 WAL 的库——已提交未 checkpoint 的数据丢失）。
   逐文件"新名不存在且旧名存在 → os.Rename"；**每次 Rename 后 fsync 数据目录**，
   保证断电后目录项持久，任何中断后状态都落回状态机 A/B/D 之一，不存在静默数据
   丢失路径。
4. **权限贯穿**：db.go 现有的目录 0700 / 文件 0600 收紧循环（db.go:76 附近）
   改为新文件名；迁移在收紧之前执行，重命名后的文件沿用 0600；权限错误要失败得
   大声，不允许半迁移后静默继续。
5. **CLI 无锁路径（`OpenForCLI`）不得迁移**。区分两类：
   - `age` 等直接无锁打开（main.go:467 附近）：检测"旧库存在且未迁移"→ 拒绝并
     提示"先启动一次新 server 完成迁移/先停旧实例"；
   - bootstrap/reset-password 先走持锁 `db.Open`（main.go:135-156）→ 作为合法的
     首次迁移者；仅在 `ErrLocked` 回退到无锁路径时，同样检测未迁移态并拒绝，
     防止在旧实例运行时创建空 `supacove.db` 抢先落位。
6. **旧版进程是锁隔离覆盖不到的残余风险**：旧版 CLI（`age` 等）不加锁，迁移瞬间
   仍可持有旧库连接；rename 后旧进程会继续沿旧路径访问、并可能在其旧名下重建
   WAL/SHM，造成文件配对不一致（参 SQLite howtocorrupt）。技术上无法用锁排除不
   加锁的进程，因此：升级须知（§4.5）与错误信息中**硬性要求"升级前停止所有旧
   版本进程（server 与 CLI）"**，作为已接受的残余风险记录在案。
7. **旧 server 反向误启动的善后**：现有 db.go 在取锁**之前**就预创建主库文件
   （db.go:86 的 `O_CREATE`），因此迁移完成后误启旧二进制，即使被我们持有的旧锁
   拦下，也会留下一个空的 `supabackup.db`，使新 server 下次启动落入状态机"双主库"
   拒绝分支。对策：(a) 本次重构顺手把"预创建主库"挪到持锁之后（收窄新二进制的
   窗口）；(b) "双主库"错误信息附恢复指引（**先停止全部进程再动手**；旧库为 0 字节
   新建文件时直接删除即可；否则按新旧程度或备份判断保留哪个）；(c) 升级须知要求**关闭旧服务
   的自动重启**（systemd restart / docker restart policy），防止旧镜像回涌。
8. **测试矩阵**（upgrade_test.go）：仅旧库→迁移成功且已提交未 checkpoint 数据
   （含 WAL 内容）完整；三件套整体迁移；状态机拒绝组合各一例（双主库——含旧库
   为空文件的情形、新主库＋旧 WAL、有侧文件而无主库、同类侧文件双份）；`age`
   在未迁移目录→拒绝；bootstrap 在无旧实例时→合法完成迁移、旧实例持锁时
   bootstrap 回退路径→拒绝；旧 server 持锁时新 server 拒绝；中途崩溃（模拟 WAL
   已改、主库未改）后重启续迁成功；迁移后误启旧二进制→被旧锁拦截、遗留空旧库后
   新 server 给出含恢复指引的错误；全新目录直接建新库；**旧→新→旧回滚**
   （回滚流程：停新二进制 → 三文件改回旧名 → 旧二进制正常打开）。
9. `docs/disaster-recovery.md:92` 等恢复文档中的目标文件名同步更新。

### Step 4 — 对外契约改名（带退出条件的兼容）

统一退出节奏：**metrics 别名、webhook 旧头、套件旧环境变量回退、旧锁持有，均为
两个 tag 版本后移除**（代码注释标 `DEPRECATED: remove in vX.Y` 并各开 issue 跟踪）。

1. **metrics.go**：**13 个活跃指标名**改 `supacove_*`；旧名保留为 `DEPRECATED
   alias`（仓库先例：`supabackup_jobs_succeeded` 别名），加上 2 个本就 DEPRECATED
   的快捷指标（`jobs_succeeded/failed`），对外旧名共 15 个按各自计划存续，**不**为
   这 2 个快捷指标新增 `supacove_` 版本。测试断言新旧族标签集、数值、故障时的缺失
   行为完全一致；`server_metrics_format_test.go:209-212` 与
   `server_phase7_test.go:209` 需**补新名断言**（不能只验旧别名）。`make check`
   不含 metrics-check（需运行实例 + 登录 cookie），且 `scripts/e2e-run.sh` 退出即
   停实例删目录——**把 promtool 校验并入 e2e 流程（实例存活期间、清理之前）**，
   或单独起临时实例验证。
2. **webhook 头**：**两条发送路径同步**——outbox.go:323（正式投递）与
   api_phase7.go:379（控制台测试投递）都改为同时发送
   `X-Supacove-Event(-ID)` + 旧 `X-Supabackup-Event(-ID)` 四个头；测试断言新旧
   Event-ID 值一致。
3. **恢复套件（recovery/kit.go）**：模板改
   `PROFILE="${SUPACOVE_PROFILE:-${SUPABACKUP_PROFILE:-<default>}}"`、
   `SUPACOVE_ALLOW_NONEMPTY`（同名回退）。已发出的旧套件自包含、不受影响。
   测试覆盖：新变量优先、仅旧变量、两者为空、新 `ALLOW_NONEMPTY=0` 覆盖旧 `1`。
4. **kit 临时目录前缀** `supabackup-restore.` → `supacove-restore.`，
   **与 kit_test.go:243 泄漏检测前缀同一提交修改**，否则泄漏检测失效。
5. **pgx.go:48** `application_name` → `supacove`（pg_stat_activity 可见）；
   s3.go:283 诊断体、remediation.go 双语提示、i18n_msgs.go、jobs.go 错误提示
   （`` run `supacove age init` first``）、platform.go 提示串、config.go:23 /
   netguard.go:2 注释、agekey_test.go:74 / m1_test.go:290 等测试文案。
6. GHCR：**硬切换**到 `ghcr.io/web-casa/supacove`，不双发——CHANGELOG 的 GHCR
   条目仍在 `[Unreleased]`、dist 只有 0.0.0-smoke 本地产物，当前无已发布 tag，
   无存量消费者；发布说明显著标注"镜像地址已变更，旧地址不会重定向、旧 tag 不再
   更新"。**条件双发触发器**：实施期间若发现已有 tag 发布过镜像，则追加一个 push
   旧名 digest 的 CI job，双发**仅维持一个 tag 版本**后移除。
   【执行时记录】终检发现 `v0.1.0` 已推送且确实发布过旧名镜像
   （`ghcr.io/web-casa/supabackup:latest` 可公开拉取）——触发器生效：已在
   ci.yml 的 `image-tags` job 追加 "Mirror tags under the legacy image name
   (one release only)" 步骤，随首个改名 tag 双发后删除。

### Step 5 — 文档 / 品牌 / 元数据收尾
- README（含 "formerly known as supabackup" 便于搜索）、CONTRIBUTING、SECURITY、
  deploy/binary-README.md、docs/deployment.md、disaster-recovery.md 等活文档。
- **升级须知（单独小节，部署文档 + 发布说明都要有；第一条是硬性要求）**：
  - **升级前停止所有旧版本进程（server 与 CLI 都算）**——旧 CLI 无锁，锁机制
    隔离不到（§4.3.6）。
  - Docker 升级**保留原卷**（`supabackup-data`）：卷名只是挂载点标识，改名后新建
    卷是空库，启动迁移也救不了（迁移只看 data dir 内文件名）。新安装才用
    `supacove-data`（showcase.tsx 示例同步）。
  - systemd 升级**不要顺手改** `StateDirectory=supabackup` / `SB_DATA_DIR=/var/lib/
    supabackup` / `User=supabackup`：`SB_DATA_DIR` 不变（§3.4）则数据目录不变，
    迁移自动完成；若确要换用户/目录，提供停机迁移步骤（停服 → cp -a 保留属主权限
    → chown → 改 unit → 启动 → 验证）。新安装的 unit 示例用 `supacove`。
- docs-site：mdx 内容、showcase、smoke.mjs、package.json/lock name（`npm install
  --package-lock-only` 刷新）；frontend：package.json/lock、playwright.config.ts 注释。
- CHANGELOG：`[Unreleased]` 新增重命名条目（新二进制名、镜像地址变更、指标新旧
  对照表、webhook 双头与移除计划、数据目录迁移与"先停旧实例"须知、Docker/systemd
  升级注意事项、回滚步骤）；文件头改 SupaCove。

### Step 6 — 终检与发布
- 终检命令（含隐藏文件，尊重 gitignore）：
  ```sh
  rg -n -i --hidden 'supabackup' . -g '!.git/**' -g '!docs/reviews/**'
  ```
  白名单逐条核对：CHANGELOG 历史条目（含 [Unreleased] 重命名条目中对旧
  卷名/旧环境变量的升级指引）；README "formerly" 行；兼容别名及其测试
  （metrics 旧名、**webhook 旧头（outbox.go 与 api_phase7.go 两条路径）**、套件旧
  环境变量回退）；db.go 迁移代码中的旧文件名及迁移测试；openapi-baseline.yaml
  （待 release tag 刷新）；compose.yaml MinIO 开发凭据；部署文档升级小节中的旧
  卷名/旧目录引用；§3.1 所列历史规划/证据文档；本方案文档自身。
- 全量验证：`make check`、`make e2e`（含并入的 promtool 指标校验）、frontend 构建
  + Playwright、`make docs-build`、`make release` 冒烟。
- 手工场景：旧版本建库存数据 → 新版本启动 → 数据完整；全新安装；回滚演练（新→旧）。
- 发布：tag 后产物 `supacove_*`，镜像 `ghcr.io/web-casa/supacove`；发布说明置顶
  重命名变更与升级指引；下个 release tag 时刷新 openapi-baseline.yaml。

## 5. 风险清单与对策

| # | 风险 | 等级 | 对策 |
|---|---|---|---|
| 1 | 升级后数据库"静默重置" | P0 | §4.3 协议 + 测试矩阵；Docker/systemd 升级须知（卷名/目录名不改） |
| 2 | 迁移中断致 WAL 与主库错位（丢已提交数据） | P0 | 六文件显式状态机（仅全旧/全新/正向中断三态放行，其余拒绝）；WAL/SHM 先行、主库最后；逐文件幂等；每次改名后 fsync 目录；崩溃续迁测试 |
| 3 | 新旧二进制同目录双跑（锁名不同互不互斥） | P1 | 先取新锁，**无条件**整个生命周期持有旧锁，两个 tag 版本后随兼容期一并移除 |
| 4 | **旧版 CLI 无锁连接在迁移瞬间打开库**（锁隔离覆盖不到） | P1 | 硬性升级要求"停止所有旧进程（含 CLI）"，写入部署文档/发布说明/错误信息；残余风险记录在案 |
| 4b | 迁移后旧服务自动重启回涌，预创建空旧库触发"双主库"拒绝 | P1 | 预创建挪到持锁后；"双主库"错误附恢复指引；升级须知关闭旧服务自动重启；反向误启动测试 |
| 5 | CI tag 任务 `grep -F` 与版本输出失锁 | P1 | Step 2 原子提交 + 本地 `make release` 冒烟（grep 仅 tag 任务执行，普通 CI 测不到） |
| 6 | webhook 接收方按旧头过滤 | P1 | 双路径四头并存；Event-ID 一致性测试；两个 tag 版本后移除 |
| 7 | 监控面板按旧指标名 | P1 | 13 个活跃名 deprecated 别名 + 对照表文档 + 移除计划 |
| 8 | Docker 卷 / systemd StateDirectory 换名绕过迁移 → 空库 | P1 | 升级须知明确"保留原卷/原目录"；新装才用新名 |
| 9 | 回滚后旧二进制读不到新库名 | P1 | 回滚流程（停机 + 三文件改回旧名）+ 新→旧回滚测试 |
| 10 | GHCR 更名无重定向 | P2 | 硬切换 + 显著发布说明；若发现已有 tag 则双发一个版本（§4.4.6） |
| 11 | 误改 supabase 平台名词 | P2 | 字面子串匹配 `supabackup`；终检逐条白名单核对 |
| 12 | 生成文件手改漂移（api.gen.go、schema.d.ts、lockfile） | P2 | 一律 `make api-gen` / npm 刷新；CI drift 检查兜底 |
| 13 | `.golangci.yml` FQN 漏改致 lint 豁免失效 | P2 | 并入 Step 1，跑 `make lint` 验证 |
| 14 | kit 临时目录前缀与泄漏检测脱节 | P2 | 同提交成对修改 |
| 15 | `SB_` 前缀 / MinIO 凭据被顺手改掉 | — | 负面清单 §3.4 / §3.5 |

## 7. 执行记录（Step 6 终检时补记）

| 步骤 | 提交 | 内容 |
|---|---|---|
| 方案 | `e98e56b` | 本文档（codex 四轮评审：14+6+3+2 条意见全部吸收） |
| Step 1 | `906d92d` | Go 模块路径 + 142 处 import + .golangci.yml FQN |
| Step 2 | `23e1bb1` | 二进制/CLI/构建/openapi CLI 文案（原子提交） |
| Step 3 | `a525500` | 数据文件迁移协议（六文件状态机/双锁/WAL/SHM 先迁主库最后/逐次 fsync）+ 测试矩阵 |
| Step 4 | `bb1024b` | 对外契约改名 + 兼容别名（指标/webhook 头/套件环境变量） |
| Step 5 | `a23dbb0` | 文档/品牌/元数据收尾 + 升级须知 |
| Step 6 | 本次 | 终检（无代码变更，仅本记录） |

终检结果：`make check` 全绿（lint/api-check 无漂移/api-breaking 无破坏/race 测试/
vitest 31/31/完整构建）；e2e 5/5；镜像构建 + 容器 version/bootstrap/healthcheck
冒烟；`make release VERSION=v0.0.0-final` 四平台归档名/SHA256SUMS/版本输出正确；
真实旧库（12 表 goose v15）升级终态复测数据一致；docs-site 构建 + 路由 smoke
48 检查通过；终检扫描剩余旧名全部归入白名单（兼容别名及其测试、本方案文档、升级指引/过渡说明、CHANGELOG 历史、四处历史规划/证据文档、openapi-baseline、MinIO 凭据、formerly 行）。

**与方案的两处偏差（如实记录）**：
1. promtool 校验未并入 e2e 脚本，而是通过测试钩子 `SB_METRICS_DUMP` 抓取
   带鉴权的真实 `/metrics` 后经 docker promtool 校验（Step 4 与终检各一次，
   新旧双名版本均通过）——即 §4.4.1 允许的"单独起临时实例验证"路径；e2e
   是临时 cookie 会话，shell 侧集成需额外鉴权编排，收益不成比例。
2. 回滚演练的自动化测试以裸 sqlite 按旧文件名直开代替旧二进制
   （`TestRollbackAndReupgradeCycle`）；终检另用 main 分支构建的真实旧二进制
   完成完整 old→new→old→new 四阶段演练（记录见下），两者互为补充。

**真实旧二进制演练记录**：从 main（a218d09，改名前）构建真实 `supabackup`
二进制建库写数据 → 新 `supacove` 二进制启动自动迁移、数据完整 → 按回滚
流程改名回旧名、旧二进制重新启动读写正常 → 再升级回新二进制数据仍完整。

**发布时待办**（首个改名版本 tag 时执行）：
1. 为四处兼容 shim（metrics 别名、webhook 旧头、套件旧环境变量回退、旧锁持有）
   创建移除跟踪 issue（代码注释已标注"两个 tag 版本后移除"）；
2. 刷新 `api/openapi-baseline.yaml`（按 Makefile 惯例随 release tag 人工刷新）；
3. 发布说明置顶重命名变更与升级指引（CHANGELOG `[Unreleased]` 条目已备好）。
