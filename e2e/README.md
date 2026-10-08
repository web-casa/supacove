# e2e（历史位置说明）

浏览器端到端测试已迁移到 **`frontend/e2e/`**（Playwright specs），运行入口为
**`scripts/e2e-run.sh`**（`make e2e` / CI `e2e` job）：它构建嵌入前端的最终二进制、
起一次性实例、用 CLI bootstrap 令牌完成初始化后对真实产物跑全部 specs。

本目录不再存放测试代码；保留仅为历史引用提供指向。
