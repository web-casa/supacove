# Phase 1 第六轮复审（确认轮）

评审日期：2026-10-03。当前 HEAD：`b0720cff7ab847e495c514e7e82ba5562f553f80`。范围：`git diff 4597964..b0720cf`，共 9 个文件，包含第五轮报告。对照 `docs/reviews/codex-phase1-review-5.md`，复核指定的 7 项及本轮引入的回归；此前已关闭问题不因当前环境限制而重开。下文代码行号均指本次 HEAD。

**最终判定：Phase 1“代码与工程门禁”未通过。** 备份保留与 details 契约修复有效，Spike 2 的错误采样归属、缺失采样放行及假 DONE 已修复。但 Spike 1 名称拆分和部分枚举失败仍可假成功；smoke 新增了首次 HTTP 请求的启动竞态；工程入口和 Spike 2 的历史代码缺口仍在。另确认挂载脚本在严格 umask 下不可被容器用户读取的新回归。上述结论不以真实 Supabase、双架构或联网 CI 尚未执行为依据。

## 七项逐项判定

判定以本次请求列出的具体缺陷为单位。**FIXED 4 / PARTIALLY_FIXED 2 / NOT_FIXED 0 / REGRESSED 1**。其中第 6、7 项的具体子问题关闭，**不代表父项 P1-19 的所有历史要求已关闭**；剩余要求另列，避免用一个总编号掩盖已完成的修复。

| 项目 | 判定 | 证据与边界 |
|---|---|---|
| 1. P1-08：legacy 备份误删 | **FIXED** | `backend/internal/db/backup.go:114–119` 对所有不能解析版本的文件设 `keep=true`，最终删除循环不再删除它们。真实裁剪函数探针逐名、逐内容核对三个 legacy/未知名称文件，分别与 0、5、6 个已知版本并存，全部保留；每版本两次快照仍正确去重，超过五个版本时仍只留最新五个版本。旧的 legacy 排序/分组代码与部分注释尚在，但本次复现的误删已消除。提交中的弱数量断言未增强，本轮用独立探针补证，不据此重开已修实现。 |
| 2. P1-18：枚举退出码、换行名称、schema pattern、临时文件名 | **PARTIALLY_FIXED** | 表枚举改为管道写文件并检查退出码（`spike1:183–186`），客户端返回非零时完整脚本退出 1；`tables-$SCHEMA_IDX.txt` 不再使用 schema 组成文件路径；`pattern_literal` 正确生成双引号模式并双写内部引号。**换行仍被拆分，新加 schema 换行拒绝检查执行得太晚；schema/role 枚举的进程替换仍吞退出码。** 详见后文。 |
| 3. P1-09：`/health/details` storage 503 契约 | **FIXED** | `api/openapi.yaml:53–58` 增加 503/Error；Go `GetHealthDetails503JSONResponse` 和 TS `getHealthDetails.responses[503]` 均已生成。重生成逐字节一致。真实 Router 中登录后关闭 SQLite，details 返回 `503`、`storage_unavailable`；生成的 503 响应类型与实际 JSON 的 code/message 一致。`/auth/me` 同样经过会话存储 guard，其既有 503 保留合理。 |
| 4. R5-P1-01：CI smoke 健康竞态、资产及 SIGTERM | **REGRESSED** | `ci.yml:100–106` 的健康状态轮询有效；合法 `starting → starting → healthy` 通过，unhealthy/超时失败并清理；实际请求 JS，404 或 HTML MIME 均失败；stop/退出码断言存在。**删除 sleep 后，首次 healthz 请求在任何就绪等待前执行，正常慢启动仍直接失败。** CSS 未请求的旧缺口也仍在。新回归为 R6-P1-01。 |
| 5. R5-P2-01：`make check` 集合及工具路径 | **PARTIALLY_FIXED** | `Makefile:48–50` 恢复前端 lint 和最终 build；`.NOTPARALLEL` 下 frontend 先装依赖，随后 api-check、race、lint/build，dry-run 确认无原减项。默认 GOPATH 下探测与调用都用 `$(OAPI)`，缺失时有固定版本安装命令。**单独 api-check 仍无 npm 依赖前置；设置 GOBIN 时，安装目录和 OAPI 探测/执行目录仍不一致。** |
| 6. R5-P2-02：RSS 命中 shell、缺失采样放行、setpriv SKIPPED 仍 DONE | **FIXED** | 内层改为挂载脚本文件；`spike2:56` 从本实例 `postmaster.pid` 读取 PID；`:76–79` 拒绝 client 或 postmaster 的零采样。控制流探针给出不存在的 postmaster PID，退出 6且无 DONE；setpriv 不可用时内层退出 7、外层退出 1，输出 `SPIKE2_INCOMPLETE`，无 DONE。这里关闭的是这三个具体缺陷，未把替身进程的 RSS 当作 PostgreSQL 资源实测。 |
| 7. P1-19 指定三项：坏档完整 stderr、自适应截断、清理 trap | **FIXED（指定子项）** | `spike2:89–97` 按实际字节数截半，并把完整 stderr 复制到持久挂载目录。301 字节受控归档截为 150 字节，两行诊断均导出。`:32–36` 保留 EXIT trap；首次 restore 返回 9 时调用 stop、删除暂存文件并保留退出 9。自适应截断和 trap 在第五轮已有效，本轮继续保持；新增有效改动主要是完整 stderr 导出。**失败清理的结果断言、同 UID 独立进程探针等父项残留未关闭，见“历史残留”。** |

## 本轮新增回归

### R6-P1-01：健康轮询之前的首次 HTTP 请求仍有启动竞态

位置：`.github/workflows/ci.yml:85–89、100–106`。

`docker run -d` 返回、`docker exec id -u` 成功，只能说明容器可执行命令；应用此时仍可能正在初始化 SQLite、迁移或建立监听。当前脚本立即执行 `curl -fsS /api/healthz | grep -q ...`，没有重试。`set -euo pipefail` 使首次连接失败直接退出，后面的健康轮询无法补救。

执行工作流 smoke 原文，仅以替身控制 Docker/HTTP 响应：首次 healthz 返回连接拒绝时，脚本退出 **1**，health inspect 次数 **0**，未到达 stop；EXIT trap 确实执行了删除。这是本轮删除启动等待后新引入的更早竞态，不能因后段轮询已正确就关闭整个问题。

修复要求：在第一次 HTTP/认证/资产检查前，有界等待 HTTP 就绪或 Docker healthy，并在失败时输出可诊断信息；随后执行功能断言。资产检查还应覆盖 HTML 引用的 CSS，当前 CSS 缺失探针仍退出 0。SIGTERM 的原文控制流检查已通过，但真实进程停机结果仍需 Docker 环境补证。

### R6-P2-01：挂载脚本权限设置早于文件创建，严格 umask 下不能运行

位置：`scripts/spike2-embedded-pg.sh:16–26、118–124`。

新实现先 `chmod 644 "$OUT"/*.sh`，再创建 `inner.sh`。新输出目录中没有 `.sh` 文件，chmod 失败被忽略。若调用者使用 `umask 077`，随后生成的 `inner.sh` 为 0600，`sed -i` 后仍为 0600。宿主文件属主通常不是容器 UID 10001；将目录设为 777 并不能赋予这个用户读取文件的权限。容器的 `bash /spike-out/inner.sh` 因此无法运行。

实际执行外层脚本的受控探针，在全新 OUT 中测得 `inner.sh mode=0600 owner=501:501`；正常 umask 下为 0644。这里证实的是实际文件权限和镜像声明的 UID 不匹配，**未声称实跑 Docker 得到 Permission denied**。替身以内层文件属主身份运行，所以其退出 0 不能验证跨 UID 可读性。

修复要求：在文件创建、内容替换完成后显式设置所需权限，并检查结果；确保宿主 OUT 对容器用户的访问与写证据权限均可用。新增严格 umask 的验证场景。

本轮新增根因共 **2 项：P1 × 1、P2 × 1；无新增 P0**。下面列出的旧问题不重复计为新回归。

## 仍未关闭的历史代码问题

### P1-18 / R2-P1-04：名称边界与实验结果仍不可靠

`spike1:43–50` 仍是 `psql -At -z` 后 `tr '\n' '\0'`；`-z` 指定字段分隔符，真正的 NUL 记录分隔符是 `-0`。名称中的换行在 `mapfile` 之前已被转换成记录边界。因此 `:56–61` 新增的换行检查永远看不到被拆掉的换行，无法实现其声称的拒绝行为。角色 `:105–111`、表 `:186` 仍使用按行读取的 `mapfile -t`。这一语义已核对 [PostgreSQL 18 psql 选项](https://www.postgresql.org/docs/18/app-psql.html)。

| 完整 Spike 1 脚本探针 | 本次结果 |
|---|---|
| 表枚举返回非零 | **退出 1**，不再报告计数一致；该修复关闭。 |
| schema 枚举先输出一条记录，再返回 9 | **退出 0**；失败状态仍被进程替换吞掉。 |
| role 枚举返回 9 | **退出 0**；当作没有自定义角色继续执行。 |
| 单个 schema `line\nbreak` | pg_dump 实收两个模式 `"line"`、`"break"`；拒绝检查未触发。 |
| 单个 table `line\nbreak` | 计数变量变成两次 `tbl=line`、`tbl=break`。 |
| 单个 role `review_line\nbreak` | 角色变量变成两次 `role=review_line`、`role=break`。 |
| schema `App*prod`、`App"*?[]name` | 模式分别为 `"App*prod"`、`"App""*?[]name"`，正确保护大小写、通配符和内部引号。 |
| schema `../../strange/path` | 临时表清单仍写 `tables-1.txt`，不再由 schema 组成路径。 |
| `FAIL_invoices`、计数失败 | 前者成功；后者退出 1；历史 FAIL sentinel 修复保持有效。 |

这些是客户端替身执行原 Bash 的结果，用来验证退出码、记录和 argv 边界；不代表真实数据库恢复成功。修正后的双引号模式符合 [pg_dump schema 参数](https://www.postgresql.org/docs/18/app-pgdump.html)与 [psql Patterns](https://www.postgresql.org/docs/18/app-psql.html#APP-PSQL-PATTERNS)；本轮没有完成联机 pg_dump 名称匹配实验。

应把 schema、role、table 枚举统一为“显式检查 psql 成功，再读取 NUL 记录”的流程。若选择不支持换行名称，也必须在丢失名称边界之前拒绝，而不能检查拆分后的字符串。

第五轮已有的 profile 证据缺口仍在：P-B/P-C/P-A 共用目标；只在末尾做一次行数比较；P-A 的 schema/data 分别记为 candidate passed，P-B 意外成功也记为 candidate passed；角色仅创建 NOLOGIN 占位，没有恢复完整属性/成员关系；缺少各 profile 独立的权限、RLS、FK、TOC 断言；restore stderr 反复覆盖 `last-restore-error.log`。这些是脚本自身的可验证性问题，不能全部列为“等待用户提供 Supabase”后豁免。

### P1-14 / R5-P2-01：工程入口剩余两个前置问题

1. **独立 api-check 没有 Node 依赖前置。** 在 `git archive b0720cf` 的干净副本中执行 `make api-check`，Go 生成器成功，随后 `openapi-typescript: not found`，make 退出 2。经 `make check` 的 frontend 前置可解决该路径，但没有解决单独调用 api-check。应提供明确的依赖准备规则，或一致地落实受支持入口的前置条件。
2. **GOBIN 与 OAPI 仍可错位。** `Makefile:7` 固定 `$(go env GOPATH)/bin/oapi-codegen`，`:32` 使用未固定安装目录的 `go install`。本机 `go help install` 确认安装会优先使用 GOBIN。按该语义模拟安装到自定义 GOBIN 后，实际 make 仍调用 GOPATH/bin，报 `No such file or directory`、退出 2。默认路径已有改善；“安装位置与探测路径统一”尚不覆盖合法的 GOBIN 配置。真实缺失工具安装另受网络限制，未把模拟安装报告为联网安装通过。

前端 lint 与最终 build 的恢复已关闭；不再将原先“check 集合缩水”列为 OPEN，也不要求添加此前未承诺的新检查来关闭该问题。

### P1-19 父项：指定三项已修，完整实验门禁仍有残留

- **服务端资源范围不足。** 从 postmaster.pid 取 PID 已正确，但 `spike2:56–79` 只读取 pg_restore 客户端和 postmaster 的 `/proc/.../status`，不采样执行恢复 SQL 的 backend 及其子进程。现在的数字准确标作 postmaster，仍不能作为整个临时 PostgreSQL 恢复负载的峰值。需要定义并记录覆盖工作进程的资源口径；本项不是否定已修的 PID 归属。
- **失败清理只调用，未确认结果。** `:32–36` 的 trap 对 stop 失败仍 `|| true`，随后删目录；`:99–111` 的清理结果断言只在成功路径执行。注入首次 restore 返回 9、stop 同时返回 9，完整内层控制流保留 restore 的退出 9并删除目录，但模拟的 running 标记仍在，也没有清理失败诊断。此探针证明断言遗漏，未声称 Docker 容器销毁后真实进程仍存活。应在失败路径同样确认已记录 PID 退出、目录/socket 清理结果，并保留原错误与清理错误证据。
- **独立同 UID 文件/进程边界探针仍缺失。** 本轮删除了原先只创建 app-owned secret 的片段，保留的 canary 仅验证另一个 root 容器中 root 0600 文件对 UID 10001 的访问。它不能替代已要求的应用与恢复进程同 UID 边界实测。这里不要求改变 ADR-004 的“仅可信来源”决策，要求的是可重复的证据。

完整坏档 stderr 导出、自适应截断、trap 调用、缺失 RSS 拒绝、setpriv INCOMPLETE 均已关闭，不再列为这些残留的修复要求。新挂载文件权限问题按 R6-P2-01 单独跟踪。

## 本轮验证及证据边界

工作区仅新增本报告，没有修改业务代码、自带测试或生成物。隔离副本来自 `git archive b0720cf`，目录为 `/tmp/codex-phase1-review-6/repo`；探针脚本、逐场景 argv 与日志保留在 `/tmp/codex-phase1-review-6/`，属于本机复核材料，未作为仓库永久测试提交。部分探针专门证实缺陷，探针执行成功不等于被评审项通过。

| 验证 | 结果 | 主要证据 |
|---|---|---|
| 原样 `go test -race -count=1 ./backend/...` | 默认 GOCACHE 首先遇只读文件系统；改用可写缓存后，auth/config/db/limiter 通过，server 因 httptest 创建 TCP socket 被沙箱拒绝而失败。**原样全量测试没有通过。** | `logs/original-race.log`、`logs/original-race-writable-cache.log` |
| 隔离副本全量 race 与 Review6 探针 | 全部通过。仅替换测试 transport 为真实 Router + ResponseRecorder，保留 cookie jar、非 nil Body、RemoteAddr，并加 Store cleanup；业务代码不变。新增逐名备份保留、实际/生成 503 响应一致性探针通过。不能替代 TCP/超时/真实停机测试。 | `setup-probes.py`、`logs/isolated-race.log` |
| Go 静态与构建 | 业务 gofmt、`go vet ./backend/...`、CGO_ENABLED=0 后端构建通过。隔离归档的带资产构建因无可用 VCS 状态，使用 `-buildvcs=false` 后通过；未改产品构建规则。 | `logs/vet.log`；`supabackup`、`supabackup-with-assets` |
| Bash、YAML、差异检查 | 两个脚本分别 bash -n，生成的 inner/canary 也 bash -n；Python YAML 解析 CI/OpenAPI/compose，通过。`git diff --check 4597964..b0720cf` 通过。 | `logs/syntax.log` |
| 附加 lint | actionlint 报 CI 第 37 行既有 SC2046；ShellCheck 报 Spike 1 第 93 行既有 SC2015。两者均非本轮新增，不当作新增发现，也不宣称全绿。 | 本轮工具输出，原行未变 |
| 契约与前端 | Go/TS 重生成与提交逐字节一致；Node 22.22.2、现有本地依赖副本下 ESLint、TypeScript/Vite build 通过。没有执行成功的干净联网 npm ci。 | 隔离生成物及构建产物 |
| 嵌入资产实际路由 | 本轮构建的 JS/CSS 经真实 Router 均返回 200、正确 MIME；JS 263313 字节、CSS 1569 字节。说明当前产物可服务，不能替代 smoke 对缺失 CSS 的拦截。 | `logs/assets.log` |
| 工程入口与 smoke | make dry-run 确认完整集合；无 node_modules 的 api-check 失败；GOBIN 语义模拟重现路径错位。smoke 原文覆盖延迟启动、starting→healthy、JS 缺失/错误 MIME、CSS 缺失、unhealthy、超时。所有退出路径都触发 cleanup。 | `engineering-probes.py`、`logs/engineering.log`、`logs/make-check-plan.log`、`logs/api-check-*.log`、`ci-mock/` |
| Spike 1 | 完整原 Bash + PostgreSQL 客户端替身验证枚举退出码、名称边界、模式参数和清单路径；未连接真实 Supabase。 | `spike-probes.py`、`logs/spike1-probes.log`、`logs/identifier-boundaries.log` |
| Spike 2 控制流与文件权限 | 实际外层/内层 Bash，替换 PG/Docker 工具；仅将固定路径隔离到场景目录，移除非 root 替身无法执行的 chown。验证截断/完整 stderr、restore 失败、stop 失败、RSS 缺失、setpriv 缺失、边界失败及 umask 077。替身 PID/RSS 和身份均不作为 PG/UID 10001 实测。 | `spike2-probes.py`、`logs/spike2-probes.log`、`logs/umask-permissions.log`、`spike2-mock/` |
| Docker runtime 构建与 Spike 2 实跑 | 已尝试 `docker build --target runtime ...` 和原脚本；均因 `/var/run/docker.sock` 权限拒绝而未能运行。**容器 smoke、真实 PG RSS、容器跨 UID 权限、双架构本轮未验证。** | `logs/docker-build.log`、`logs/spike2-real.log` |

## 需要用户资源或决策的外部项

以下事项单列，不计新增缺陷，也不能替代上述代码修复。

1. **真实 Supabase Spike 1：** 提供代表性源测试项目及各 profile 可独立初始化的空目标，覆盖 Auth、自定义角色、权限/RLS、跨 schema FK 与声明支持的扩展；提供允许恢复操作的直连或 Session pooler 连接。先修脚本，再执行并将每个 profile 的 TOC、独立 stderr、对象/权限/数据核对写入 ADR-003。
2. **双架构实测：** 提供可运行本提交 `runtime-spike` 镜像的 arm64/amd64 环境，保留 commit、镜像、架构及是否模拟执行的标识。验证恢复、坏档、失败清理、完整资源口径、root/same-UID 文件和进程边界。历史 ADR 结果不能代替本提交改写脚本后的证据。
3. **联网 CI 与容器资源：** 在有网络、可创建 socket、可访问 Docker 的 runner 上执行干净依赖安装、原样 race/HTTP 测试、契约检查、runtime 构建及真实 healthcheck/资产/SIGTERM smoke，并取得本提交对应的 CI 全绿记录。本轮没有启动远程 CI。
4. **范围与阶段决策：** 根据真实恢复结果确认完整 Supabase profile 或 beta“应用 schema 逻辑备份”范围；产物格式变化依 ADR-003 决策。若决定延期外部验证，应明确更新 DoD/ADR；本评审不代为豁免。

**结论保持：未通过。** 关闭 Spike 1 名称/枚举与 profile 证据缺口、首次 HTTP 就绪竞态、工程入口前置问题、Spike 2 历史实验门禁残留及新挂载权限回归后，才具备确认“代码与工程门禁通过”的条件；外部实测另行补证。
