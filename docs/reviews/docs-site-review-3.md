# 文档站评审 3（快速收敛确认）

日期：2026-10-07。结论：**NEEDS_FIXES**。

基线：`docs/reviews/docs-site-review-2.md`（14 FIXED / 10 PARTIALLY）。范围：`git diff 1f2e031..HEAD`；HEAD：`1d8579a7bab6e8e39cf19eb7c4a7ec597ecede04`，提交标题 `fix(docs review 2): age-identity rebuild path, DR index accuracy, boundary closings`。

本轮重审的 **10 项：7 FIXED / 3 PARTIALLY / 0 NOT_FIXED**。第二轮点名的三个边界 P1-08、P2-07、P2-11 分别为 FIXED、FIXED、PARTIALLY，不重复计数。仍有内容缺陷，且生产 build 未绿，不符合批准条件。

开始时工作树干净；仅新增本报告。实现及依赖只读核对；构建、脚本和缺页变异均在 `/tmp/docs-review3-Z1ENEx/` 的 HEAD 副本进行，未修改仓库实现。

## 逐项判定

下表页面路径缩写为 `docs-site/content/docs/{zh,en}/<slug>.mdx`。

| 编号 | 判定 | 证据与结论 |
|---|---|---|
| P1-04 | FIXED | zh restore:49–56 / en:57–67 明确旧目录与 recipient 仅供审计、不再使用；新实例空 recipient → age init → 离线保管和 age verify → 重建数据库/目的地/调度 → 新备份与恢复验证。明确场景 1 写回旧 recipient 仅适用于旧私钥仍可用。与 backend/cmd/supabackup/main.go:433 的已有 recipient 拒绝逻辑一致。决定性新实例路径已关闭。 |
| P1-08 | FIXED | zh backups:38 / en:43–44 均明确获取三种下载都需要会话；取得预签名 URL 后，持有者可在 15 分钟内向对象存储下载。与 server.go:210 的 guard 及 api_phase3.go:215 起的下载 URL 接口一致。 |
| P1-12 | FIXED | zh index:22–23、installation:62–63 / en index:24–27、installation:65–67 均补工件文件及 manifest 可读；中英前置条件一致。对应 jobs.go:362–371 的文件存在检查与 manifest 读取失败跳过逻辑。 |
| P2-02 | PARTIALLY | 两语 heartbeat 已补 `-` 不要求正周期、继承 URL 的成功 ping 仍要求正周期；但新增“会把周期清零 / zeroes the period”不符合实现，见下文。 |
| P2-03 | FIXED | zh notifications:9–10 / en:9–11 均覆盖 never-success 和暂停停止产生新过期事件；scheduler.go:150–171、264–271 与之相符。 |
| P2-04 | FIXED | zh notifications:13–15 / en:15–18 均补一次投递轮次可向多个目标 POST，失败重试会重访上一轮成功目标；outbox.go:285 起的 deliver 对所有匹配目标重新遍历，无逐目标成功游标。 |
| P2-07 | FIXED | zh troubleshooting:46–48 已与 en:60–64 对齐 10 秒 HTTP 超时、崩溃/结果未写入、15 分钟租约、启动回收与 dead。outbox.go:141、373 支持超时及租约值。10 秒是单次 HTTP 请求限制，不能当作多目标整轮的总时限。 |
| P2-08 | PARTIALLY | 四个场景名称和场景 2 的“前两步”已纠正；manifest 两处均统一为 job-N，UUID 与对象键区分正确（jobs.go:864）；无 kit 手工恢复选项已恢复。场景 1 的操作指向仍错误，且手册依旧只是代码样式路径，没有可用链接。 |
| P2-11 | PARTIALLY | 语言布局源码现输出 html lang=lang，根布局保留 global.css；规划语言码已改 zh。但规划验收 URL 仍错误，且本轮未取得成功构建的 HTML，无法确认 zh/en 根 lang 及 stylesheet link；详见验证记录。 |
| P2-12 | FIXED | mdx-components.tsx:3–8 明确不自动接入 runtime；删除未接线组件映射与 useMDXComponents，只保留透传 stub。README 与实际 Page 的显式 MDX 导入方式一致。 |

## 剩余内容缺陷

### P2-02：禁用心跳不会自动清零周期（本 diff 新增事实错误）

位置：zh heartbeat:17–18 / en heartbeat:18–20。

两语都新增了“周期清零”的承诺。实际 `backend/internal/server/api_phase7.go:143–170` 仅让 `-` 豁免 URL 校验及正周期要求；周期字段按请求更新，并无强制置零。`backend/internal/jobs/queries_schedule.go:55–67` 原样保存；`frontend/src/components/ScheduleForm.tsx:71–78` 也只 trim URL，未清零周期。`jobs/heartbeat.go:59` 遇到 `-` 直接禁用发送。

例如已有周期 24 小时，将 URL 改成 `-` 后仍可保存 24；之后改回继承 URL，原周期仍可生效。应删除“清零”断言，写为“不要求正周期，禁用期间不发送成功或失败 ping”。

### P2-08：场景 1 仍混入场景 4 的恢复步骤，手工入口导航未闭合

位置：zh restore:66–73 / en restore:79–91；对照 `docs/disaster-recovery.md`。

| 手册场景 | 本轮索引核验 |
|---|---|
| 1：SQLite 损坏/数据卷丢失 | 名称正确，但“pre-migrate 快照与 schema 重建”不是该场景的步骤。实际为以空目录启动、bootstrap、旧私钥仍可用时写回旧 recipient、重新注册数据库/目的地及调度。卷丢失时不能假设卷内快照还存在。 |
| 2：应用主密钥不可用 | 正确；对应本站前两个主密钥条目。 |
| 3：整台实例丢失，仅剩桶内密文与离线私钥 | 正确；独立恢复及无 kit 手动替代入口均存在。 |
| 4：升级失败/迁移中断 | 正确；pre-migrate 快照、隔离 WAL/SHM、启动旧版本的回滚流程属于这里。 |

应把场景 1 的指向改为真实重建步骤，快照回滚留在场景 4。两语目前仍仅显示反引号包裹的 `docs/disaster-recovery.md`；手动命令不在本站，需提供可点击的仓库手册/场景 3 链接或站内等价命令，关闭第二轮明确提出的导航缺口。

### P2-11：规划验收地址仍不符合实际路由

`docs/docs-site-plan.md:62` 仍为 `/zh/quickstart`、`/en/quickstart`；实际页面为 `/zh/docs/quickstart`、`/en/docs/quickstart`。`app/[lang]/page.tsx` 只处理语言首页重定向，未提供这两个错误地址的页面。提交说明声称修复验收 URL，但本 diff 只改了规划的语言码。

## 构建与验证

副本由 `git archive HEAD docs-site` 创建，复制本地现有 node_modules；没有执行或宣称干净 npm ci。主要使用 Node 22.22.2；webpack 另用 Node 24.18.0 和系统 Node 20.19.2 复核。

| 验证 | 结果 | 证据及限制 |
|---|---|---|
| 正常 parity | PASS | `parity ok: 12 slugs in both locales`，见 parity.log。 |
| 副本缺页变异 | PASS（负向） | 临时移走 en/heartbeat.mdx，退出 1：`locale parity broken: en missing: heartbeat`；还原后退出 0。见 parity-missing.log。 |
| `npm run build` → 原样 `next build` | 未绿 | parity 通过，Next 16.4.0 / MDX 生成成功，停在 `Creating an optimized production build ...`，无完成输出后中止，退出 130。见 build.log；不把停滞臆断为 MDX 内容错误。 |
| `next build --webpack` | FAIL | Node 24 与 Node 20 均退出 1：`Could not parse output from TypeScript's --showConfig`，cause 为 `Unexpected end of JSON input`。见 build-webpack.log、build-webpack-node20.log。 |
| 构建失败定位 | 验证限制 | 直接 `node .../typescript/bin/tsc --showConfig` 得到 2045 字节 JSON；同一 Next runTypeScriptCli 捕获 stdout 长度却为 0。独立 spawn 子进程执行 console.log 也返回空 stdout。提示执行环境/子进程输出异常，不能据此判定仓库代码有编译错误，也不能记作 build 通过。 |
| `next start --hostname 127.0.0.1 --port 4339` | 环境阻塞 | `listen EPERM`，退出 1，见 start.log。 |
| `.next/server` HTML 替代验收 | 未完成 | 本轮构建未生成 server HTML，无法 grep 根 html lang 或 link rel=stylesheet。源码确实含 zh/en 路由 lang 和根 CSS 导入，但不能替代产物证据。 |
| Fumadocs runtime 编译 | PASS | 逐一加载当前 24 页，均返回 body 函数；`MDX compiled bodies 24`，见 runtime.log。它只补充 MDX 语法/导入验证，不替代 Next build。 |
| 真实 loader/搜索补充检查 | PASS | 两语各 12 页、quickstart 位于第二；zh 心跳/SB_JOB_TIMEOUT、en heartbeat/restore 的直接 Request→Response 均 200。不是 HTTP 监听测试。 |
| diff 格式检查 | PASS | `git diff --check 1f2e031..HEAD` 无输出。 |

已扫描完整 diff 并对读改动涉及的中英文。除上述问题外，未发现其他新增实质内容缺陷或 MDX 编译问题。非阻断清理：根布局重复导入 global.css；语言布局注释声称根仍有 html、Next 将 html 提升到 head，与当前根布局返回 fragment 不符；restore 的 description 仍称“三类”，正文已改四种情形。

浏览器语言菜单点击、干净 npm ci 仍属未完成验收；生产构建及 HTML 检查也未通过。本轮并非仅剩浏览器/安装环境限制：P2-02、P2-08、P2-11 仍有明确内容问题，因此结论为 **NEEDS_FIXES**。
