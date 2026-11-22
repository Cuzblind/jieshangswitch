# 接上AI Desktop

基于 CC Switch v4.0.20 的精简派生版。仅提供 Claude Code / Codex 的接入、安装、启动、连接检查、修复、全部恢复、余额和客服。上游提交与原文件摘要见 `upstream.json`，MIT 许可证见 `LICENSE`。

## 开发

需要 Node.js 22+、pnpm 10.12.3、Go 1.26、Rust stable，以及对应平台的 Tauri 2 系统依赖。Linux 开发机需要 GTK 3、WebKit2GTK 4.1、pkg-config；正式包仅面向 Windows 10+、macOS 12+。

```sh
cd client/desktop
pnpm install --frozen-lockfile
pnpm dev
```

`pnpm dev` 自动编译随应用打包的 Go 后台程序。业务操作使用私有管道，前端只能调用状态查询和固定动作；本机 47391 端口只保留网站使用的 `/hello` 探测，不提供操作 API。

共享目录不支持符号链接时，仓库的 `.npmrc` 使用平铺依赖和可执行文件包装脚本；也可把仓库复制到本机文件系统构建。

## 构建与测试

```sh
cd client/core && go test ./...
cd ../runtime && go test -race ./...
cd ../app && go test ./...
cd ../desktop
pnpm verify:source
pnpm typecheck
pnpm test
pnpm build:engine
cd src-tauri && cargo fmt --check && cargo test --locked
cd .. && pnpm build
```

Windows 和 Mac 安装包默认在对应系统构建，四个架构由 `.github/workflows/desktop.yml` 验证。工作流只上传构建产物，不发布 Release、不部署网站。正式切换下载入口前必须完成双端实机验收。Windows x64 测试安装包已通过 Linux / MSVC 交叉编译生成，位置与验证边界见 `docs/CLIENT-CCSWITCH-VALIDATION.md`（仓库根目录）。

跨架构编译后台程序可明确指定 `node scripts/build-engine.mjs --target aarch64-apple-darwin`；桌面包通过 `CARGO_BUILD_TARGET` 设置同一目标。不要混用其他目标或其他来源的后台程序。

测试服务只支持构建时指定：

```sh
JIESHANGAI_TEST_ORIGIN=http://192.168.116.22:18080 pnpm build
```

该版本在界面中标记为测试版，不能替换正式网站下载文件。运行时的 `JIESHANGAI_ORIGIN`、`JIESHANGAI_WEB` 以及深链参数均不能修改桌面版服务地址。CLI 使用固定的 npmmirror 国内镜像，GUI 使用微软商店及官方安装包；运行时不能修改安装源。

## 升级与恢复

- 新旧客户端共用现有设备 ID、授权文件和 `~/.jieshangai` 配置与备份记录，不接管原版 CC Switch 的数据。
- 新应用接管 `jieshangai://` 协议。本机探测端口被旧版或其他程序占用时，客户端照常工作，只是网页暂时探测不到它，深链接入不受影响。
- “全部恢复设置”同时恢复两个助手的 CLI 和 Claude GUI 接入配置，不卸载 CLI / GUI，不删除研究文件，不作废网页连接；如需停用，请前往网页账户页。
- 有效备份原样恢复；没有备份时只清理本站设置。接上后发生过手动修改的文件，会在确认框中提醒。
- 每日后台检查更新，用户点击后才安装；接上AI客户端更新验证独立公钥签名和签名内版本，助手沿各自安装来源更新。更新清单尚需随正式发布部署。保留 MIT 许可及上游归属，不沿用 CC Switch 更新服务器或公钥。

## 发布前人工验收

每个目标架构确认：安装与卸载、深链冷启动与重复唤起、WebView2/系统拦截、中文和空格路径、助手安装与终端启动、旧版升级、全部恢复、网页停止设备连接、客服与充值入口。未完成的目标标为待验收，不能宣称正式可用。

## beta.2 安装机制

点击一个助手的“安装应用”会同时安装 CLI 和 GUI；任务在卡片中运行，可独立取消，成功的组件不会因另一组件失败而回滚。CLI 自动准备私有 Node.js 24 LTS，不依赖系统 Node 或修改系统 PATH。下载有超时、断点续传、校验和缓存，不无限重试。Windows 当前用户安装包内带完整 WebView2 安装程序；包体增大用于避免终端用户再从海外拉取 WebView2。

四平台离线资源由对应原生 CI 机器准备：`pnpm build:engine` 后运行 `node scripts/prepare-offline.mjs`，再 `pnpm build --config tauri.offline.conf.json`。也可手动触发桌面 CI 并勾选 `offline`。目录必须不存在，失败会清理不完整输出。完整资源包含已验证的两种 CLI 和官方 GUI 安装文件，可能达到数 GB；不包含用户密钥或构建机的系统工具。正常在线包不包含它们。

开启发布签名时，在 CI secrets 配置 `TAURI_SIGNING_PRIVATE_KEY` / `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`，须与应用内公钥对应。CI 会额外输出 `.sig` 和每个架构的 `latest.fragment.json`；合并各架构清单并发布到本站 `/client-dist/updates/stable/latest.json`，包文件按清单路径放置。测试版独立使用编译测试站的 `beta` 通道。工作流仍然只构建上传，不自动部署。

实现与验收边界见 [安装方案](../../docs/CLIENT-INSTALLER-V2.md)。
