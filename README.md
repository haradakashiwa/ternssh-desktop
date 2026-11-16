# ternssh desktop

[ternssh](https://github.com/haradakashiwa/ternssh) 的桌面客户端：一个原生的 SSH 终端与服务器管理工具，支持 macOS 和 Windows。界面用 [MyGo](https://github.com/egoist/mygo) 原生 UI 编写，没有 WebView。

- **本机即可使用**：不添加任何站点，也能在「本机」标签里管理服务器和本地终端，凭据存在系统钥匙串。
- **多设备同步**：添加 ternssh 站点后，站点上的服务器、分组和快捷指令出现在独立的标签里，在客户端的修改会写回站点。
- **所有连接都从本机发起**：站点只用来同步 SSH 信息，不经它转发流量。

## 下载

在 [Releases](https://github.com/haradakashiwa/ternssh-desktop/releases) 下载：

| 系统 | 文件 |
|---|---|
| macOS（Apple 芯片与 Intel） | `ternssh-<版本>-macos-universal.dmg` |
| Windows x64 | `…-windows-amd64-setup.exe`（安装包）或 `…-windows-amd64-portable.zip`（免安装） |
| Windows ARM64 | `…-windows-arm64-setup.exe` 或 `…-windows-arm64-portable.zip` |

安装包没有开发者证书签名：macOS 首次打开时请右键应用选「打开」；Windows 若出现 SmartScreen 提示，点「更多信息」→「仍要运行」。

## 功能

- **服务器管理**：本机或 ternssh 站点的服务器与分组，拖拽整理，站点上的修改双向同步。
- **终端**：多标签 SSH 终端和本地终端，基于 libghostty-vt。
- **文件**：SFTP 和本机文件浏览、上传下载，双击即可在带语法高亮的编辑器中修改。
- **快捷指令**：一键在终端运行常用命令，站点的指令与网页端共享。
- **服务器状态**：CPU、内存、磁盘、网络流量和进程的实时监控（Linux）。
- **托盘与全局快捷键**：关闭窗口后驻留托盘，随时一键唤出。

## 开发

需要 Go 1.27.3+（`GOTOOLCHAIN=auto` 会自动下载）。

```sh
make dev                                # 开发运行（热重载）
make mac                                # 打包 macOS .app 和 .dmg
make windows                            # 交叉编译 Windows x64/ARM64 及安装包（需要 NSIS）
make test                               # vet（含 Windows）和测试
SNAPSHOT_DIR=/tmp/snap go test ./gui    # 输出界面快照
```

## 发布

推送 `v` 开头的 tag，GitHub Actions（`.github/workflows/release.yml`）会测试、打包并创建 Release。版本号取自 tag，无需修改 `mygo.json`；带 `-` 的版本（如 `v0.2.0-beta.1`）标为预发布。

```sh
git tag v0.2.0 && git push origin v0.2.0
```

## 目录

| 路径 | 内容 |
|---|---|
| `main.go` | 窗口、托盘、全局快捷键、系统钥匙串、文件选择等平台相关部分 |
| `gui/` | 原生界面与状态：站点标签、侧栏、终端、面板、编辑器、对话框 |
| `internal/ternssh` | ternssh 站点的 HTTP API 客户端 |
| `internal/sshconn` | SSH 连接、SFTP 与本机文件 |
| `internal/store` | 站点、本地服务器、快捷指令与设置（`config.json`），密钥存系统钥匙串 |
