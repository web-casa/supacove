# Phase 1 第七轮复审（最终确认）

评审日期：2026-10-03。HEAD：`9d7c2e021c68453f95c31bc75cb76a8546e9b77d`。基线链：`db42d83 → 4597964 → b0720cf → 9d7c2e0`。范围：`git diff b0720cf..9d7c2e0`，共 5 个文件，其中一个是第六轮报告；按 `docs/reviews/codex-phase1-review-6.md` 复核本轮指定事项。

**最终判定：Phase 1“代码与工程门禁”未通过。** 本轮确认新增 **P1 × 3、P2 × 1，无新增 P0**：smoke Bash 引号错误、JavaScript MIME 判断错误、Spike 2 清理覆盖原始失败码，以及 P-A schema 失败日志提示失效。前三项直接破坏门禁有效性。表名换行、profile 验证、独立 api-check 前置和 Spike 2 资源／失败清理等历史代码缺口也未全部关闭。真实 Supabase、双架构、联网 CI 均单列为用户资源项，**不作为代码缺陷或本结论的阻断依据**。

本轮仅新增本报告，未修改被审代码、仓库测试或生成物。以下判定针对指定提交，不以修复说明代替实际 diff。

## 五项修复与 P2-04 遗留判定

| 项目 | 判定 | 证据与剩余范围 |
|---|---|---|
| 1. R6-P1-01：首次 HTTP 就绪等待、JS/CSS 资产 | **REGRESSED（就绪等待子项 FIXED）** | `ci.yml:92–96` 已在首次 HTTP 功能断言前轮询，前两次连接拒绝后能继续；持续失败时最终断言非零退出。新增 JS/CSS 循环的 `:103` 引号不配对，原文 `bash -n` 退出 2；仅修引号后，`:105` 匹配 `js` 仍拒绝正常 JavaScript MIME。两项独立回归见 R7-P1-01/02；不能认定双资产 smoke 已通过。 |
| 2. R6-P2-01：严格 umask 下挂载脚本权限 | **FIXED** | `spike2:130–138、166` 在 sed／文件创建后显式 chmod，并检查失败。真实执行 `umask 077` 外层脚本，生成的 inner.sh 为 0644；完整控制流替身执行时 inner.sh/canary.sh 均为 0644。**HEAD 的 OUT 为 0777，并非说明中的 0755**，用于让容器 UID 10001 写证据。真实 Docker 执行受权限阻断，不把替身执行称为容器实跑通过。 |
| 3. P1-18：NUL 枚举、错误传播、独立 stderr | **PARTIALLY_FIXED** | `spike1:45–55、103–111` 使用 `-At -0` 写文件并显式检查 psql 退出状态，schema/role 的换行边界和枚举失败传播已修；受控探针验证单个换行名称保持一个参数、枚举非零时退出 1。四次 restore 的 stderr 现在分别落盘，未再相互覆盖。**表枚举仍按行拆分；profile 共用目标、角色占位和独立验证缺口仍在。** P-A schema 失败提示还指向已不用的日志名，见 R7-P2-01。 |
| 4. P1-19：cleanup、服务端 RSS、同 UID 边界 | **REGRESSED** | stop/rm 失败现在影响 cleanup 返回码，正常路径也避免重复 stop；同 UID 0600 文件可读性断言已加入。**但 EXIT trap 用 cleanup 自身状态覆盖原错误，恢复失败等场景可退出 0 并输出 DONE，见 R7-P1-03。** HEAD 没有 PPid 扫描，仍只采样 postmaster；失败路径的结果断言和持久错误证据未完成；同 UID 进程边界仍无探针。 |
| 5. P1-14：GOBIN 路径、api-check CLI 前置 | **PARTIALLY_FIXED** | `Makefile:9–15、35、40` 已统一探测、安装和调用目录。空 GOBIN 回退到 GOPATH/bin，显式 GOBIN 使用该目录，两分支路径探针通过，未发现所关注的空值分支回归。**HEAD 没有 `node_modules/.bin/openapi-typescript` 探测或 npm 依赖前置**；干净 archive 的实际 `make api-check` 仍退出 2，报 `openapi-typescript: not found`。 |
| P2-04：构建元信息与镜像门禁 | **REGRESSED（与第 1 项交叉，不重复计数）** | runtime target、UID、health 状态与 SIGTERM 断言仍保留，但 smoke 被 R7-P1-01/02 阻断，CSS 与正常停机断言无法在正常 JS 响应下执行。PR BUILD_DATE 仍可回退到 `unknown`，仅满足非空，不提供真实构建日期；这一旧 P2 残留不单独阻断。双架构和联网运行证据另列外部项。 |

六条判定记录为 **FIXED 1 / PARTIALLY_FIXED 2 / REGRESSED 3**，包含父项与交叉编号，并非六个独立缺陷。修复说明中的“PPid 扫描”“api-check 精确探测”在本次 HEAD／diff 中均未找到；schema/role 的实现是“结果文件 + 显式检查退出状态”，没有另写退出码文件，但这一实现本身能够正确传播失败。

## 新增回归

### R7-P1-01：资产正则中的双引号破坏 smoke Bash 语法

位置：`.github/workflows/ci.yml:103`。

```bash
ASSET=$(curl -fsS http://127.0.0.1:18080/ | grep -oE "/assets/[^"]+\.$EXT" | head -1)
```

正则字符组中的 ASCII 双引号没有转义，提前结束外层字符串，导致后续引号配对错位。这里的 YAML literal block 不会替 Bash 转义。**YAML 解析成功不等于其中的 run 脚本语法有效。**

从 YAML 原样提取 smoke 的 `run` 后执行 `bash -n`，退出 **2**，报 `unexpected EOF while looking for matching '"'`；actionlint 同时报告新增 SC1072/SC1073。正常 Docker/HTTP 替身执行原文也在资产循环处退出 2，并触发 EXIT 清理。第六轮对应脚本不存在此语法错误。

修复要求：正确引用正则，例如 `grep -oE '/assets/[^"]+\.'"$EXT"`，并对提取出的 workflow Bash 执行语法检查，不能只检查 YAML 和 `scripts/*.sh`。

### R7-P1-02：用扩展名 js 检查 MIME，正常 JS 必然被拒绝

位置：`.github/workflows/ci.yml:105`。

`grep -q "$EXT"` 在 JS 分支搜索连续子串 `js`；`text/javascript`、`application/javascript` 均不含这一子串。因此即使先修复引号错误，正常镜像仍会失败。反而 `application/json` 包含 `js`，会被误当成 JavaScript 接受。

为隔离此问题，仅在 `/tmp` 的脚本副本给上一行正则补一个反斜杠，其他 smoke 原文不变：

| 受控响应 | 结果 |
|---|---|
| JS 为 `text/javascript; charset=utf-8`，CSS 正常 | 退出 1，未请求 CSS，未执行 stop |
| JS 为 `application/javascript`，CSS 正常 | 退出 1，未请求 CSS，未执行 stop |
| JS 错误地为 `application/json`，CSS 正常 | **退出 0**，执行到 health 状态与 stop 断言 |
| JS 为上述可穿过检查的 JSON，CSS 返回 404／text/html | 退出 1；CSS 分支能拒绝这两种错误，但正常 JS 路径到不了这里 |

额外执行真实 Go `http.FileServerFS` + `httptest.ResponseRecorder`，`.js` 返回 `200 text/javascript; charset=utf-8`，`.css` 返回 `200 text/css; charset=utf-8`，与应用 `server.go:556` 使用的文件服务机制一致；该探针不需要 TCP socket，也不是容器 smoke。

修复要求：为 JS/CSS 分别明确允许的 MIME，处理可选 charset 参数；覆盖正常双资产、缺失资产、HTML fallback 和错误 JSON MIME。此项与语法错误需要分别修复。

### R7-P1-03：cleanup 覆盖原始错误，失败的 Spike 2 可报告 DONE

位置：`scripts/spike2-embedded-pg.sh:36–48`，外层成功传播路径为 `:141–143、174`。

EXIT trap 进入后直接设置 `local rc=0`，没有保存入口 `$?`；末尾 `exit $rc` 只返回 stop/rm 的状态。因此正常清理会把 `initdb`、restore、行数核对、RSS 或坏档断言的失败全部改写为成功。外层只检查 `docker run` 返回码，canary 成功后即可输出 `SPIKE2_DONE`，无需出现 `SPIKE_OK`。

执行原内层／外层 Bash 控制流，仅替换 PG、Docker 工具并隔离固定文件路径：

| 故障注入 | 当前完整脚本结果 |
|---|---|
| initdb 返回 9 | **退出 0，DONE** |
| 首次 pg_restore 返回 9，stop/rm 成功 | **退出 0，DONE** |
| RSS 缺失，内层主动 exit 6 | **退出 0，DONE** |
| 行数不符，内层主动 exit 2 | **退出 0，DONE** |
| 截断归档错误地恢复成功，内层主动 exit 3 | **退出 0，DONE** |
| restore 返回 9，stop 也返回 9 | 退出 1，无 DONE；原始 9 丢失，cleanup 错误仅写容器 /tmp |

独立提取 cleanup 原函数的矩阵也确认：`原错误=9 / stop=0 / rm=0 → 最终0`；`9/9/0 → 1`；`0/0/9 → 1`；`9/9/9 → 1`。说明清理失败能够阻断，但主任务与清理两种状态没有被正确合并。正常路径的 stop 只调用一次；不把“已停止后不再 stop”误判为错误。

修复要求：在 trap 入口立即保存原始状态，独立记录 cleanup 状态；主任务失败必须保留非零，主任务成功而 cleanup 失败也必须非零。两种错误均需有持久诊断，并让失败路径执行清理结果核对。仅用 `grep SPIKE_OK` 补丁掩盖退出码问题不足以关闭本项。

### R7-P2-01：P-A schema 失败提示引用不存在的 stderr 文件

位置：`scripts/spike1-supabase-restore.sh:29–30、151–155`。

restore 默认文件已由 `last-restore-error.log` 改为 `restore-unspecified.log`，P-A schema 调用没有设置 `restore_stderr`，失败消息却仍打印旧文件名。注入 schema restore 失败，脚本正确退出 1；提示中的旧文件不存在，真实诊断保存在 `restore-unspecified.log`。这是本轮修改默认路径引入的诊断回归，**不等于 stderr 仍被其他 candidate 覆盖**。

修复要求：为该调用设置明确的 `restore-P-A-schema.log`，并让失败消息引用同一文件。本项不单独阻断 Phase 1。

## 仍未关闭的历史代码问题

**P1-18：只修了 schema/role，尚未修完整实验有效性。**

- `spike1:183–186` 的表枚举仍为 `-At` + `mapfile -t`。单个表名 `line\nbreak` 在完整脚本探针中变成 `tbl=line`、`tbl=break` 两次计数调用；替身返回相同计数时，脚本仍退出 0 并声称一致。真实数据库可能查询失败，也可能存在被错误核对的同名普通表；均不能视为验证了原表。应同样先检查 psql 成功，再读取 NUL 记录。
- P-B/P-C/P-A 仍复用同一个 `TARGET_DB_URL`，前一 profile 的部分恢复可能污染下一次实验；没有各 profile 的干净目标或重置流程。
- 自定义角色仍只创建 NOLOGIN 占位，导出的 roles.sql 未用于恢复属性／成员关系；仍缺每个 profile 的权限、RLS、FK、TOC 独立断言，只有全部尝试后的行数对比。P-A schema/data 和 P-B 意外成功仍分别增加 passed 计数，不能代表完整候选 profile 经独立验证成功。

schema/role 的 `-0` 改动方向正确。本机 `psql 18.4 --help` 与 [PostgreSQL 18 psql 官方选项说明](https://www.postgresql.org/docs/18/app-psql.html)一致：`-0` 是 NUL **记录**分隔，`-z` 是 NUL 字段分隔。探针传入真实 NUL 字节，验证 Bash `mapfile -d ''` 保留名称中的换行／引号，没有重开已修的 schema pattern 和清单路径问题。真实本地 PG 启动因 Unix socket 被沙箱拒绝而失败，**未把客户端替身当作真实 psql 查询或 Supabase 恢复实证**。

**P1-19：资源范围、失败清理结果和进程边界仍未完整落地。**

- `spike2:68–83` 仍只读取 restore 客户端和 postmaster 两个 PID 的 VmHWM，整个文件无 PPid 扫描，也无 backend／其他子进程聚合。当前数字仍只能称为 postmaster RSS，不能宣称全部 PG 服务端恢复负载峰值。
- `:118–123` 的进程／目录断言仅在正常路径；trap 未对失败后的 PID、目录、socket 做同等结果检查。`CLEANUP_ERROR` 只在容器 `/tmp`，未复制到挂载的 `/spike-out`，`docker run --rm` 后不能保留；也没有说明中所称的最终错误文件断言。受控 stop 失败时 running 标记仍在、数据目录已删除；这证明检查缺口，不声称真实容器销毁后仍有 PostgreSQL 存活。
- `:161–164` 已新增由另一个进程读取同 UID 0600 文件的断言，root 文件拒绝与同 UID 文件可读两面均有代码证据；同 UID 文件读取失败、setpriv 缺失、root canary 失守的控制流均非零退出。**同 UID 进程访问／信号边界未验证**。不要求推翻 ADR-004 的“仅可信来源”决策，只保留第六轮已要求的进程证据缺口。

**P1-14：独立工程入口仍不具备干净依赖前置。** `make check` 经 frontend 先装依赖的路径保持有效；单独 `make api-check` 的 `:42` 仍直接执行 npm 脚本。干净 archive 中真实 Go 生成器成功且生成物与提交逐字节一致，随后缺失 TS CLI、make 退出 2。应落实声明中的 CLI 探测／依赖准备；这与是否提供联网 CI 资源是两个问题。GOBIN 空值分支使用 `:=` 正常求值，显式目录也正确，不能因 npm 前置仍缺失而否定这部分已修结果。

## 验证记录与边界

隔离副本来自 `git archive 9d7c2e0`，位于 `/tmp/codex-phase1-review-7/repo`。脚本、逐场景 argv、原文／隔离变体和日志保留于 `/tmp/codex-phase1-review-7/`。探针不是提交到仓库的新增测试；受控替身用于验证 Bash 的数据边界和错误传播，不能替代真实容器身份、PG 恢复和资源测量。

| 验证 | 实测结果 | 本机证据 |
|---|---|---|
| `go test -race ./backend/...` | 返回 0，但包结果均为缓存命中；不据此认定本轮重新执行通过。 | 首次命令输出 |
| `go test -race -count=1 ./backend/...` | auth/config/db/limiter 通过；server 在 httptest 创建 TCP socket 时被沙箱拒绝，整体退出 1。未修改测试规避限制。 | `logs/race.log` |
| `go vet ./backend/...`、CGO_ENABLED=0 后端构建 | 均退出 0，生成本机二进制；Go 同时提示一次模块 stat cache 写入只读目录失败，未阻断构建。 | 工具输出、`supabackup` |
| 两个 spike 外层与提取出的 inner/canary `bash -n` | 通过。 | 提取的 `inner-original.sh`、`canary-original.sh` |
| Python YAML | CI、OpenAPI、API 生成配置、compose 全部可解析。 | 本轮命令输出 |
| workflow smoke 原文 `bash -n`／actionlint | **失败**，新增引号语法错误；actionlint 第 37 行原有 SC2046 另列，未计新增。 | `logs/smoke-bash-n.log`、`logs/actionlint.log` |
| ShellCheck、提交差异格式 | 两个 spike 只有既有 SC2015；`git diff --check b0720cf..9d7c2e0` 通过。 | `logs/shellcheck.log`、本轮命令输出 |
| smoke 原文与仅修引号副本 | 原文正常路径退出 2；仅修引号后正常 MIME 退出 1、JSON MIME 错误放行。前两次 healthz 失败后第三次就绪并通过第四次功能请求；一直失败共调用 61 次（60 次轮询＋最终断言）后退出，所有场景触发清理。sleep 为替身，非墙钟耗时测试。 | `logs/ci-probes.log`、`logs/ci-probes-quote-only.log`、`ci-mock/`、`ci-quote-only/` |
| Go 文件服务 MIME | 无 socket 的标准库真实 FileServerFS 返回正常 JS/CSS MIME，印证错误匹配。 | `logs/mime.log`、`mime.go` |
| Spike 1 全脚本探针 | schema/role 枚举非零拒绝、NUL 名称保真、引号 pattern 保持有效；表换行仍拆分；四次 restore 日志独立，P-A schema 提示失效。 | `logs/spike1-probes.log`、`logs/identifier-and-stderr.log`、`logs/schema-restore-stderr.log` |
| 真实 psql/PG 尝试 | 本机 PG 18.4 initdb 成功，pg_ctl 因 Unix socket bind `Operation not permitted` 失败，未执行真实查询。 | `logs/psql-real.log`、`pg-real/server.log` |
| Spike 2 全脚本故障注入、cleanup 双状态矩阵 | 五种主任务失败被改为成功；清理失败仍非零；setpriv/root/same-UID canary 失败被拒绝。采样使用替身进程，不宣称真实 PG RSS。 | `logs/spike2-probes.log`、`logs/cleanup-matrix.log`、`spike2-mock/` |
| `umask 077` 原脚本 | 已实际调用；OUT=0777、inner.sh=0644，随后 Docker socket 权限拒绝，未实跑容器。替身完整执行额外确认 canary.sh=0644。 | `logs/spike2-real.log`、`spike2-mock/umask077/mounts.log` |
| Makefile GOBIN 与干净 api-check | 空值／显式值 dry-run 正确；模拟 go install 语义的两分支均首次安装、第二次复用，探测／安装／执行一致。真实干净 api-check 仍在 TS CLI 处失败；未宣称联网工具安装成功。 | `logs/make-paths.log`、`make-mock/`、`logs/api-check-clean.log` |
| Docker runtime 构建、真实 smoke | 已尝试 `docker build --target runtime`，因 `/var/run/docker.sock` 权限拒绝失败；`sudo -n docker version` 也受 no-new-privileges 限制。无法继续构建后 smoke，未申请提权或启动远程 CI。 | `logs/docker-build.log`、本轮 Docker/sudo 输出 |

## 外部资源项（不计代码缺陷）

1. **真实 Supabase Spike 1：** 需代表性源项目、可为候选 profile 独立初始化的目标项目及授权连接；代码修复后记录对象、角色／权限、RLS、FK、TOC、数据核对和各自 stderr，更新 ADR-003。本轮未连接真实项目。
2. **arm64／amd64 双架构：** 需可运行目标镜像的环境，记录 commit、镜像、架构和模拟／原生方式，执行恢复、坏档、资源与信任边界实验。ADR-004 历史结果不等于本提交新脚本已复测。
3. **联网 CI／容器 runner：** 需可安装依赖、创建 socket、访问 Docker 的 runner，取得本提交对应的非缓存全量 race、契约、构建和真实镜像 smoke 结果。当前沙箱限制属于验证限制，不记为代码回归。

**最终结论：未通过。** 修复新增门禁回归并关闭上列历史代码缺口后，才能确认 Phase 1“代码与工程门禁”达标；外部资源项独立补证，不用于替代或扩大代码缺陷判定。
