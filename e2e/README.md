# e2e

Playwright 浏览器端到端流程。按 dev-plan，P5 起引入 Playwright 并覆盖：
- bootstrap（一次性 token）→ 登录 → 登出
- 添加数据库向导（P5 起）
- 备份记录与恢复包下载（P3/P5 起）

当前 P1 的端到端验证由后端集成测试（`backend/internal/server/server_test.go`，
httptest 全栈）与真实二进制冒烟承担。
