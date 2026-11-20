# PrintMesh

基于 **Tailscale (`tsnet`)** 的开源异地组网共享打印软件。连接打印机的 Windows 电脑安装后即可共享本机物理打印机（USB / 局域网），组网内的其他电脑无需配置虚拟网卡或路由表，一键连接即可打印。

## 核心特性

- **全版本 Windows 兼容（含 Windows 7 SP1 离线无 WebView2 方案）**：
  - **Win10/11 现代版**：采用官方 Go + Wails v2 原生桌面窗口 + 内置 IPP 免驱通道。
  - **Win7 兼容版**：采用 `XTLS/go-win7` 编译 + **零 WebView2 依赖**的轻量托盘本地 Web 控制台（直接唤起系统已有浏览器访问受 Token 保护的 `127.0.0.1` 界面），彻底解决 Win7 无法联网下载安装 WebView2 Runtime 的问题。
- **双模式组网 + 便捷后台登录**：
  - 内嵌用户态 `tsnet`，同时支持 **Tailscale 官方服务** 与 **自建 Headscale**，不与本机代理或企业 VPN 冲突。
  - 后台服务采用非阻塞启动 + `WatchIPNBus` 实时状态监听，支持浏览器一键授权、复制链接用手机/其他设备代登录、团队邀请口令（`pm://`）及一键注销切换账号。
- **双打印模式并存**：
  - **免驱模式（IPP + PDFium）**：客户端无需安装原厂驱动，捕获标准 PDF 后由服务端通过 `PDFium` 调用宿主机原厂驱动出纸。
  - **原厂驱动模式（RAW 9100 透传）**：支持热敏面单机（ZPL/TSPL/ESC-POS）、票据针式机及专业图文打印；支持服务端打包 `DriverStore` 签名驱动，客户端自动拉取静默安装。
- **免 UAC 权限分离架构**：后台以 Windows Service（`LocalSystem`）运行，UI 以普通用户权限运行，通过带 SDDL 权限控制的 Windows Named Pipe (`\\.\pipe\printmesh-ipc`) 通信。

## 项目结构

```text
printmesh/
├── .github/workflows/release.yml   # 双轨 GitHub Actions（同时产出 Win10/11 与 Win7 安装包）
├── main.go                         # Win10/11 Wails 桌面端入口
├── app.go                          # Win10/11 Wails Go 绑定层
├── wails.json                      # Wails v2 构建配置
├── frontend/
│   ├── embed.go                    # 共享静态资源嵌入（Win10/11 与 Win7 共用同一套前端）
│   └── dist/index.html             # 双适配前端（自动识别 Wails 或 Win7 本地 Web API）
├── cmd/
│   ├── daemon/                     # Windows Service 后台守护进程 + Named Pipe IPC
│   ├── ui-win7/                    # Win7 专用零 WebView2 本地 Web 控制台启动器
│   ├── server/                     # 独立 CLI 打印服务端（快速联调用）
│   └── client/                     # 独立 CLI 打印客户端（快速联调用）
├── internal/
│   ├── netnode/                    # tsnet 非阻塞节点封装、WatchIPNBus 登录与自动发现
│   ├── ippserver/                  # 本地虚拟 IPP 2.0 服务器
│   ├── server/                     # Win32 EnumPrintersW、PDFium 出纸、RAW 直出、驱动打包
│   └── client/                     # 免驱 IPP 注册、RAW 端口桥接、兼容 Win7 的静默装驱动
├── dist/
│   └── setup.iss                   # 双轨 Inno Setup 脚本（兼容 6.2.2 与 6.3+）
├── go.mod
├── LICENSE
└── README.md
```

## 快速发布到 GitHub 并自动出包

```bash
cd printmesh
git init
git branch -M main
git add .
git commit -m "feat: initial open-source release of PrintMesh (Win7 & Win10/11)"
git remote add origin https://github.com/<你的用户名>/printmesh.git
git push -u origin main

# 推送标签触发 GitHub Actions 自动产出双版本安装包与 SHA256SUMS.txt
git tag v1.0.0
git push origin v1.0.0
```

## 开源协议

本项目基于 [MIT License](LICENSE) 开源。
