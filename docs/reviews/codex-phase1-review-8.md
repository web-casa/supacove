# Phase 1 第八轮复审（收敛确认）

评审日期：2026-10-03。HEAD：`724f7ecf7180c0e9becf78a76619e3a7adc9e8e7`。审查范围：`git diff 9d7c2e0..724f7ec`，共 5 个文件，包含上一轮报告。本轮只判定用户列出的六个具体问题及该 diff 引入的新回归；`FIXED` 表示所述问题关闭，不扩大为父编号全部历史要求的验收。

**最终结论：Phase 1 代码与工程门禁：修改后通过（本轮限定范围）。** 六项中 **FIXED 5 / PARTIALLY 0 / NOT_FIXED 1 / REGRESSED 0**；**新增回归 0**。剩余的是 R7-P2-01：P-A schema 恢复失败提示仍指向不存在的日志。第七轮已明确该 P2 不单独阻断 Phase 1；本轮不将其升级为 P1，但也不能把尚未提交的修复算成已关闭。

工作区仅新增本报告，未修改被审代码、仓库测试或生成物。以下结果来自指定提交的隔离副本及实际执行记录。

| 项目 | 判定 | 代码依据与验证结果 |
|---|---|---|
| 1. R7-P1-01：资产正则引号与 workflow Bash 门禁 | **FIXED** | `.github/workflows/ci.yml:109` 改为单引号正则拼接 `"$EXT"`，引号正确；`:131–152` 遍历该 workflow 所有 job 的 `run` 并执行 `bash -n`。实际提取的 **14 个 run 全部通过**；原样执行新增门禁退出 0。分别向 backend、contract、frontend、image 注入不配对引号，四次门禁均退出 1 并定位出错 step。 |
| 2. R7-P1-02：JS/CSS MIME 判断 | **FIXED** | `ci.yml:106–121` 的 MIME case 与扩展名分支共同约束资产类型。原文 smoke 接受 `text/javascript`、`application/javascript` 和正常 CSS，包括 charset 参数；拒绝 JSON、HTML、JS/CSS 类型互换及 404。正常路径执行到 CSS、health 状态与 stop/退出码断言；所有受控场景均触发 EXIT 清理。按第七轮所述正常 JS 被拒、JSON 被放行的问题关闭。 |
| 3. R7-P1-03：Spike 2 cleanup 覆盖原始退出码 | **FIXED** | `scripts/spike2-embedded-pg.sh:41–55` 在 trap 入口保存 `original=$?`，独立累计 `cleanup_rc`，原任务非零优先，否则返回清理状态；非空 `CLEANUP_ERROR` 复制到 `/spike-out/cleanup-error.log`。提取原函数的 **8 组合矩阵**全部符合预期，清理诊断与导出文件逐字节一致。完整脚本控制流中 initdb/restore 失败保持 9，RSS 缺失保持 6，行数不符保持 2，坏档错误放行保持 3，均不再输出 DONE；restore 与 stop 同时失败保持 9，且清理日志导出成功。 |
| 4. R7-P2-01：P-A schema stderr 文件名一致性 | **NOT_FIXED** | `scripts/spike1-supabase-restore.sh:151` 仍未设置 `restore_stderr`，实际使用 `:29` 的 `restore-unspecified.log`；`:155` 仍提示 `last-restore-error.log`。指定 diff 未修改这些行。注入 schema restore 失败后完整脚本退出 1，提示的文件不存在，诊断实际在 `restore-unspecified.log`；详见下文。 |
| 5. P1-18：表枚举 NUL 边界与退出码 | **FIXED** | `spike1:183–186` 使用 `psql -At -0` 写入 `tables-$SCHEMA_IDX.nul`，显式检查管道失败后才以 `mapfile -d ''` 读取。真实 Bash + 客户端替身中，单个 `line\nbreak` 表名完整保留为一个 `tbl=` 参数；含引号、分号的表名也保持一个参数。表枚举非零时退出 1、不执行计数、不声称行数一致。这里只关闭本轮指定的表枚举子项。 |
| 6. P1-14：独立 api-check 的 TS CLI 前置 | **FIXED** | `Makefile:41` 新增 `test -x frontend/node_modules/.bin/openapi-typescript`，缺失或不可执行时先运行 `npm ci --no-fund --no-audit`。受控探针覆盖缺失、已有、不可执行、安装失败，以及空/显式 GOBIN：需要安装时首次安装、第二次复用；已有 CLI 不重装；安装失败使 make 非零退出且不继续生成。干净 archive 中实际 `make api-check` 也成功安装依赖并运行真实 Go/TS 生成器，生成物无差异；离线配置见验证边界。 |

**唯一未关闭项：R7-P2-01（沿用原编号，不计新增）。**

当前实现仍为：

```bash
local err="${restore_stderr:-$OUT/restore-unspecified.log}"
# ...
if restore "$TARGET_DB_URL" "$OUT/schema.dump" --no-owner; then
  # ...
else
  echo "P-A schema RESULT: FAIL (stderr: $OUT/last-restore-error.log)"
fi
```

完整脚本探针仅让 `schema.dump` 恢复失败，其他候选正常执行。最终退出 1；标准输出包含 `P-A schema RESULT: FAIL (stderr: .../last-restore-error.log)`，该文件不存在；`restore-unspecified.log` 中保留了 `restore diagnostic: schema.dump`。P-B、P-C、P-A data 的日志仍各自独立，没有发现本轮新增覆盖。

关闭所需最小改动：为第 151 行调用显式设置 `restore_stderr="$OUT/restore-P-A-schema.log"`，并让第 155 行引用同一文件；重跑 schema restore 失败探针，核对提示文件确实存在且包含该次错误。此次评审未代改实现。

**关键行为验证记录：**

smoke 使用从 YAML 原样提取的 Bash，仅替换 curl/Docker/sleep 为受控工具，共运行 14 个场景。核心矩阵如下；正常返回均验证执行到 stop，失败均验证执行 EXIT 清理。

| JS MIME | CSS 正常 | CSS 404 |
|---|---|---|
| `text/javascript` | 退出 0，请求 CSS 并正常 stop | 退出 22，拒绝缺失 CSS |
| `application/javascript` | 退出 0，请求 CSS 并正常 stop | 退出 22，拒绝缺失 CSS |
| `application/json` | 退出 1，在 JS 检查拒绝 | 退出 1，在 JS 检查拒绝，未请求 CSS |

附加场景覆盖两种 JS 的 charset 参数、CSS charset、JS/CSS 类型互换、HTML fallback、CSS JSON 和 JS 404，结果均符合第七轮修复要求。

cleanup harness 提取提交内的原函数和 trap，仅将固定路径改到独立临时目录，替换 stop/remove 的返回值；没有改写状态合并逻辑。

| 原任务状态 | stop 状态 | remove 状态 | 最终状态 | 清理错误 artifact |
|---|---|---|---|---|
| 0 | 0 | 0 | 0 | 无错误，不生成 |
| 0 | 0 | 9 | 1 | 存在，内容一致 |
| 0 | 9 | 0 | 1 | 存在，内容一致 |
| 0 | 9 | 9 | 1 | 两条诊断均保留 |
| 9 | 0 | 0 | 9 | 无清理错误，不生成 |
| 9 | 0 | 9 | 9 | 存在，内容一致 |
| 9 | 9 | 0 | 9 | 存在，内容一致 |
| 9 | 9 | 9 | 9 | 两条诊断均保留 |

此外，执行了原外层与内层脚本的完整控制流故障注入，保留先前的 `umask 077`、缺少 setpriv、root/same-UID canary 失败检查。正常路径和严格 umask 路径退出 0；主任务错误不再进入 canary 或报告 DONE；canary 失败仍非零。该替身只证明控制流与文件导出，不证明真实 PostgreSQL RSS、容器权限或进程清理效果。

**验证环境与证据边界：**

隔离副本来自 `git archive 724f7ec`，位于 `/tmp/codex-phase1-review-8/repo`。探针、提取脚本、逐场景参数与日志保留于 `/tmp/codex-phase1-review-8/`，属于本机复核材料，不是提交内永久测试。Go 为 1.26.0 linux/arm64，Node 使用 22.22.2。

| 验证 | 结果与限制 | 本机日志/脚本（上述目录下） |
|---|---|---|
| workflow 与 spike Bash 语法 | 14 个 workflow run、两个 spike 外层、inner/canary 均通过；新增门禁原文及四个故障注入验证通过。 | `logs/bash-n.log`、`logs/workflow-gate.log`、`workflow-gate.sh` |
| smoke 响应矩阵 | 14 场景符合预期；没有运行真实容器或网络请求。 | `targeted-probes.py`、`logs/smoke-matrix.log`、`smoke-matrix/*/calls.jsonl` |
| cleanup 双状态与完整 Spike 2 控制流 | 8 组合退出码与 artifact 校验通过；全脚本故障不再假成功。 | `logs/cleanup-matrix.log`、`spike2-probes.py`、`logs/spike2-probes.log`、`spike2-mock/stop-failure/out/cleanup-error.log` |
| Spike 1 名称、枚举错误、schema 诊断 | 表名 NUL 保真与失败传播通过；再次复现 R7-P2-01。客户端替身不等于真实 Supabase 恢复。 | `spike1-probes.py`、`logs/spike1-probes.log`、`logs/spike1-targeted.log`、`spike-mock/schema-restore-failure/` |
| api-check 前置分支 | 五组探针覆盖 CLI 缺失/已有/不可执行/安装失败、GOBIN 两分支及重复调用，均符合预期。 | `make-cli-probes.py`、`logs/make-cli-matrix.log` |
| 真实干净 api-check | 退出 0；npm 安装 200 个包，真实 Go/TS 重生成与提交一致。使用 `npm_config_offline=true`、`npm_config_ignore_scripts=true` 适配沙箱，未声称默认联网安装或安装 hook 通过；使用只读源仓库索引对比隔离副本生成物。 | `logs/api-check-offline.log` |
| 原样非缓存 race | 默认 Go cache 只读，首次未能开始；改用 `/tmp` 可写缓存后，auth/config/db/limiter 通过，server 因 TCP socket 被沙箱拒绝而失败，整体退出 1。 | `logs/original-race.log`、`logs/original-race-writable-cache.log` |
| Router 替身非缓存 race | 仅在隔离副本修改测试 transport 为真实 Router + ResponseRecorder，保留 cookie jar、RemoteAddr、非 nil Body，并清理 Store；业务代码不变。`go test -race -count=1 ./backend/...` 全部通过。不能替代真实 TCP/超时验证。 | `setup-router-transport.py`、`logs/router-race.log` |
| 静态与构建 | `go vet ./backend/...`、`git diff --check 9d7c2e0..724f7ec` 通过。隔离 archive 的默认构建遇 VCS 状态错误，显式 `-buildvcs=false` 后 CGO_ENABLED=0 后端构建通过。ShellCheck 仅报告既有 SC2015 info，退出 1，不计新增。 | `logs/vet.log`、`logs/build.log`、`logs/build-no-vcs.log`、`logs/shellcheck.log` |

在指定 diff 和本轮验证范围内，**未发现新回归（0）**。真实 Supabase、双架构与联网 CI 本轮未执行，仍作为外部补证事项，不据此重开已修问题。P1-18、P1-19 等父项的其他历史要求不纳入本轮六项的重新判定，也不因本报告的子项 FIXED 被宣称全部完成。

**最终结论：Phase 1 代码与工程门禁：修改后通过。** 本轮需补的改动仅为 R7-P2-01 的日志路径统一及对应失败场景复核；当前提交的该项仍为 NOT_FIXED。

第九轮单项判定（2026-10-03，`5026eaa`）：**R7-P2-01：FIXED**。已核验 `724f7ec..5026eaa` 中 P-A schema restore 的 `restore_stderr` 与失败提示均为 `$OUT/restore-P-A-schema.log`；`bash -n` 通过。提交原脚本的客户端替身探针确认：schema 恢复失败时退出 1，提示文件存在且保留该次诊断；成功时退出 0；P-B、P-C、P-A data 日志各自独立。证据：`/tmp/codex-phase1-review-9-9owd6jzo/result.json` 及同目录场景日志。

**最终结论：Phase 1 代码与工程门禁：通过。** 第八轮唯一未关闭项已关闭；其余判定与验证边界沿用第八轮。
