# 接上AI 薄壳客户端（Win + Mac，内测）

> 新版客户端位于 [`desktop/`](desktop/README.md)，基于 CC Switch v4.0.20。此处保留旧薄壳适配器；两者共用 `runtime/` 接入内核与 `core/` 配置语义。正式下载入口待双端验收后切换。

- `core/`：共用写入 / 清除 / 备份 + 指纹（与 `/i` `/u` 逐字节对齐，见 core_test.go）。
- `app/`：旧版客户端适配器；业务已迁至 `runtime/`。单实例（127.0.0.1:47391 + 每次启动随机口令）、`jieshangai://install?ticket=&agent=` 深链、
  claim（`client:true`）→ 自动装助手 → 写入 → `GET /v1/models` 轻检 → 进度回报 → 成功 / 一屏一按钮出错 / 客服 / 余额一行。
  - Mac / Linux：写入走 `core`；Windows：直接跑内置的 `install.ps1` / `uninstall.ps1`（`go generate` 同步，TestScriptsInSync 防漂移）。
  - Windows 窗口：WebView2（无运行时则用默认浏览器）；Mac：AppKit Apple Event 收深链（purego，无 cgo），页面开在默认浏览器。
- 构建：`bash client/build-beta.sh`（ORIGIN=… VERSION=… 可覆盖）→ `dist/beta/`
  - 测试机 Windows 包：`WINDOWS_ONLY=1 ORIGIN=http://192.168.116.22:18080 VERSION=0.1.0-beta.test.20261026 OUT_DIR="$PWD/dist/test" bash client/build-beta.sh`。网站与网关经同一个入口访问，包内地址必须与测试网页一致。安装测试包前先退出正在运行的旧客户端。
- 测试：`cd client/runtime && GOTOOLCHAIN=auto go test -race ./...`；`bash e2e/client_e2e.sh`（需本机 gateway :3000）
- 客服专用面板：在「联系客服」页按 Ctrl+Shift+D，或三击说明句。
- 客服只用 QQ：号码在 `web-app/src/lib/shared-copy.json` 的 `support.qq`，改完跑 `go generate` 同步到 `app/`。
