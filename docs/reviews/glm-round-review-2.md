# 第 7 轮确认

结论：**NEEDS_FIXES（真实环境验收未完成；本次增量未发现新增阻塞代码缺陷）**。

日期：2026-10-07。基线：`glm-round-review-1.md` 第 6 轮末尾增量表。修复提交：`54cd01eb6414c501865b9733ce3b573f3b04885b`。本轮选择新文件交付，不改旧报告及代码。全部 Git 命令在 `/tmp` 临时仓库执行；复制 Git 元数据用于只读审阅提交，另建临时仓库模拟守卫。已逐字节确认提交涉及的五个工作区文件与 HEAD 相同。Go 验证统一使用 `GOCACHE=/tmp/r7-cache`。

## R6-02：已修复，定向模拟通过

`.github/workflows/ci.yml` contract checkout 为 `fetch-depth: 0`。守卫先独立执行 `git cat-file -e "$BASE_SHA^{commit}"`，随后在独立赋值中执行禁用改名检测的 diff；两者失败均受 `set -e` 约束。条件覆盖 PR 与 push，tag 在读取 base 对象前退出。

探针从当前 YAML 原样提取 run 脚本，使用真实临时 Git 提交和 `file://` depth=1 clone；事件字段按官方语义映射到环境变量，未声称在 GitHub runner 上执行工作流。

| 场景 | PR 退出码 | push 退出码 | 判定 |
|---|---:|---:|---|
| 基线未变，仅其他文件改变 | 0 | 0 | 正常放行 |
| 基线内容改变 | 1 | 1 | 正确拒绝 |
| depth=1，base 对象不存在 | 128 | 128 | cat-file 失败立即退出，未错误放行 |
| 基线文件改名移走 | 1 | 1 | --no-renames 正确捕获原路径删除 |
| release tag，base 为全零 SHA | — | 0 | 正确提前放行 |
| 分支 push，base 为全零 SHA | — | 128 | fail closed |

探针整体 exit 0；`actionlint .github/workflows/ci.yml` exit 0；PyYAML 解析 exit 0；临时提交副本 `git diff HEAD^ HEAD --check` exit 0。脚本和输出：`/tmp/r7-evidence/guard_probe.py`、`guard.log`。

push 没有 `pull_request.base.sha` 时，`BASE_SHA` 回退到 `github.event.before`；普通分支 `IS_TAG=false`，tag 为字符串 `true`。布尔转换、逻辑运算及 startsWith 语义参照 [GitHub Expressions](https://docs.github.com/en/actions/reference/workflows-and-actions/expressions)。本地模拟检验 Bash 行为，表达式本身由文档及 actionlint 核对。新建分支的全零 before、无法取得的 force-push 旧对象会保守失败，符合本轮要求的 fail closed。merge_group/workflow_dispatch 不进入守卫，这是既有范围，不列为本轮新增缺陷。

## R6-03：代码修复成立，真实容器验收仍受阻

`backend/internal/storage/minio_test.go:71` 在新实例可用时复位 `minioStopped=false`，解决第二个成功启动的实例不执行 cleanup 的原因。cleanup 会记录 `docker rm -f` 失败，包含容器名。两个调用者均未使用 `t.Parallel` 或另起 goroutine 调用 helper；当前串行测试生命周期下，状态复位没有新增数据竞争。

连续两次独立执行 `go test -v -count=1 -timeout=5m ./backend/internal/storage`，**均 exit 1**：两个 MinIO 测试均 SKIP，随后 HTTP 桩因 `socket: operation not permitted` panic。两次测试后均执行 `docker ps -a --filter name=sb-minio --format '{{.Names}}'`，**均 exit 1**，Docker socket permission denied。不能据此声称零残留。日志：`storage-1.log`、`storage-2.log`，位于 `/tmp/r7-evidence`。

边界：rm 失败仅记录日志，之后仍复位状态，没有重试或令测试失败；初始化失败早于 cleanup 注册的问题也仍存在。这些不是本提交引入的问题，不把本次成功初始化路径修复泛化为所有异常路径均保证回收。

## 第 6 轮三项未完成验证的当前状态

| 项目与实际执行 | 退出码 | 当前状态 |
|---|---:|---|
| `SB_METRICS_DUMP=/tmp/r7-evidence/metrics.txt go test -count=1 -v -run '^TestMetricsExpositionFormat$' ./backend/internal/server` | 1 | httptest 监听被沙箱禁止，未生成本轮 handler 输出。 |
| `docker run --rm --entrypoint /bin/promtool prom/prometheus --version` | 1 | Docker socket 拒绝访问，未启动真实 promtool。 |
| `docker run --rm -i --entrypoint /bin/promtool prom/prometheus check metrics < /dev/null` | 1 | 仅确认容器执行仍受阻；因为 dump 未生成，空输入不作为 metrics 验收。 |
| `go test -v -count=1 -run '^TestMinIOAbortIncompleteSweepsPastOnePage$' ./backend/internal/storage` | 0 | **SKIP**：docker run exit 1；101-session 非 skip 验收未完成。 |
| `/home/ivmm/go/bin/govulncheck -version` | 0 | 确认 Scanner v1.8.0、Go 1.26.6、DB https://vuln.go.dev。 |
| `/home/ivmm/go/bin/govulncheck ./backend/...` | 1 | 获取漏洞库失败：DNS UDP socket 被禁止，未得到漏洞扫描结论。 |

证据：`/tmp/r7-evidence/metrics-test.log`、`promtool.log`、`promtool-check.log`、`minio101.log`、`govulncheck.log`。本轮没有使用 `/tmp/q2-bin/promtool` 等替身，也没有复用旧 scrape 冒充当前输出。

用户所述宿主机 Docker 可用不等于本会话有访问权限：实际 Docker 调用被拒绝；`sudo -n docker ps` 同样 exit 1，提示 `no new privileges`。本会话禁止提权，未申请越界执行。提交消息中“已通过”的描述未作为本轮独立执行证据。

## 快速增量扫描与最终判定

已检查完整五文件 diff。可执行变化集中在 CI 守卫和 MinIO cleanup；retention 与 pagination 桩为注释修改，报告为第 6 轮证据补录。未发现新的阻塞代码缺陷。retention 的配置说明已纠正；pagination 注释已强调模拟该失败模式，但不能当作完整提供方协议模型。既有 govulncheck 注释仍把最低 Go 版本与拒绝新版 Go 混为一谈，属于文案准确性备注，不另立阻塞项。

R6-02 可以关闭；R6-03 的代码原因已修复，但尚缺真实服务验收。三项环境验证仍未完成，因此当前结论为 **NEEDS_FIXES**。剩余验收是在允许 Docker、端口监听及联网的执行环境中，对同一提交补跑当前 handler 输出的真实 promtool、两次含两个 MinIO 测试的 storage 包并确认无容器残留，以及 govulncheck v1.8.0 联网扫描。不能把环境失败归为新增产品缺陷，也不能将 SKIP 或提交声明代替通过证据。

## 仓库所有者补记：第 7 轮环境受阻的三项验收在主环境已实际完成

评审沙箱无 docker 与监听权限，上述三项标为"未执行"。在评审同一 HEAD（54cd01e）
的主工作环境中（docker 可用、监听可用）实际执行结果：

| 验收项 | 命令 | 结果 |
|---|---|---|
| 真实 promtool 对当前 exposition | `SB_METRICS_DUMP=… go test -run TestMetricsExpositionFormat` 产出后 `docker run --entrypoint /bin/promtool prom/prometheus:latest check metrics < dump` | **exit 0**；dump 中 `_total` 家族计数为 0 |
| 真实 MinIO 101-session 清扫（非 skip） | `go test ./backend/internal/storage/ -run TestMinIOAbortIncompleteSweepsPastOnePage -count=1 -v` | **PASS 3.38s**（创建 101 session 全清）；storage 包连续三轮 `-count=1` 全绿 |
| R6-03 容器零残留 | 修复前主机有 15 个 sb-minio 残留（泄漏属实）；清理后连续两次整包运行，`docker ps -aq --filter name=sb-minio` 均 **0** | 泄漏闭环 |
| R6-02 守卫语义 | 评审自身 6 场景模拟全过（含 depth=1 fail-closed、改名捕获、tag 放行） | 采纳评审证据 |

因此第 7 轮的 NEEDS_FIXES 仅由评审环境限制构成，不构成代码阻塞；结合其
"未发现新增阻塞代码缺陷"的判定与本补记的实跑证据，GLM/Codex 七轮评审循环
在此收敛。遗留项维持已登记状态：真实三平台恢复演练、CI 首跑（需 remote）。
