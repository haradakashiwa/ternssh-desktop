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

### 服务器与站点

- 第一个标签「本机」保存在本机，可添加 SSH 服务器和本地终端（本机 shell，可选择起始目录）。
- 每个 ternssh 站点是顶部的一个标签（右键可编辑、刷新、排序、移除），列出站点上的服务器和分组；添加、编辑、删除、移动都会写回站点，网页端和其他设备同步可见。
- 连接站点服务器时，临时从站点获取凭据（需要较新的 ternssh），由本机直接发起 SSH。
- 首次连接时记录主机密钥，之后密钥不一致会拒绝连接。
- 站点认证支持：无、Basic（用户名密码）、Cloudflare Access 服务令牌。
- 分组：可嵌套，拖拽调整顺序和所属分组，一键全部展开或收起。

### 终端

- 由 libghostty-vt 渲染，背景透出窗口的磨玻璃材质；不保存终端输出。
- 多标签：鼠标中键关闭标签；新建会话时在同一台服务器上再开一个。
- 终端下方是「文件」和「快捷指令」面板，SSH 终端右侧还有服务器状态面板，都可以隐藏；它们复用终端的 SSH 连接，无需再次认证。

### 文件

- SSH 终端浏览服务器文件（SFTP），本地终端浏览本机文件，从终端的起始目录开始。
- 上传（选择文件或拖入）、下载、新建文件夹、重命名、删除、复制路径，并显示传输进度。
- 双击文件在新窗口中编辑：
  - 语法高亮，按文件名或 `#!` 行识别语言，可在底栏切换；浅色和深色分别使用 GitHub 配色。
  - 保留原文件的 CRLF 换行和权限。
  - 文件在打开后被别处修改过时，保存前会询问是否覆盖；有未保存的修改时，关闭窗口或退出前会询问。
  - 超过 5 MB、二进制或非 UTF-8 的文件不能编辑，超过 1 MB 的文件不做高亮。

### 快捷指令

- 点击即在当前终端运行；右键可粘贴到终端但不运行，或复制、编辑、删除。
- 「本机」的快捷指令存在本机；站点标签使用站点仪表盘里「快捷指令」组件的命令，修改会写回站点。
- 服务器终端另有与网页版相同的内置命令：磁盘、内存、进程、Docker、端口、日志等。

### 服务器状态

仅支持 Linux 服务器，每 2 秒采集一次：主机名、运行时间、负载、CPU、内存与交换、各挂载点的存储、网络速率和最近 3 分钟的流量图（可切换网卡），以及 CPU 占用最高的 10 个进程。

### 设置与托盘

- 快速唤出：录制一个全局快捷键，在任何应用中按下即显示或隐藏窗口。
- 关闭窗口时默认最小化到托盘（macOS 为菜单栏），终端连接保持；也可以改为直接退出。

### 快捷键

| 操作 | macOS | Windows |
|---|---|---|
| 新建会话 | ⌘T | Ctrl+Shift+T |
| 打开设置 | ⌘, | Ctrl+, |
| 保存文件（编辑器） | ⌘S | Ctrl+S |
| 关闭终端标签 | 鼠标中键点击标签 | 鼠标中键点击标签 |

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
