# ternssh desktop

English | [简体中文](README.zh.md)

The desktop client of [ternssh](https://github.com/haradakashiwa/ternssh): a native SSH terminal and server manager for macOS and Windows. Its interface is written in [MyGo](https://github.com/egoist/mygo)'s native UI, with no WebView.

- **Works on its own**: without adding any site, the "Local" tab manages servers and local terminals, with credentials kept in the system keychain.
- **Synced across devices**: once a ternssh site is added, its servers, groups and snippets appear in a tab of their own, and changes made in the client are written back to the site.
- **Every connection is made from this machine**: a site only syncs the SSH details, and no traffic goes through it.
- **Keep vibe coding when you step away**: open the web app on your phone or any browser. It shows the same terminals as the desktop, live.

## Step away, keep vibe coding

Claude Code is still working and you have to leave? Turn on Web in the settings and keep an eye on it from your phone.

- **The same terminal, not a copy**: the terminal in the browser is the window's own session. What you type on your phone shows on the desktop, and every line the agent prints updates both at once.
- **Spot the agents at work**: a terminal running Claude Code is marked ✳ in its tab, and hovering shows what it is doing and whether it waits for you.
- **Made for phones**: drag to scroll back through the output, and a bar of terminal keys (Esc, Tab, Ctrl, Alt, the arrows, ^C…) lets you approve, interrupt or browse history in one tap.
- **Open sessions, run snippets**: your servers, groups and snippets are all there, and a terminal opened in the browser is waiting in the window when you get back.
- **Local network or the internet**: sign in once with a username and password; an address that keeps getting it wrong is locked out. Put it behind HTTPS when it faces the internet.

![A local terminal and its files](docs/screenshot-local.png)

![A server's terminal, files and status](docs/screenshot-server.png)

## Download

Download from [Releases](https://github.com/haradakashiwa/ternssh-desktop/releases):

| System | File |
|---|---|
| macOS (Apple silicon and Intel) | `ternssh-<version>-macos-universal.dmg` |
| Windows x64 | `…-windows-amd64-setup.exe` (installer) or `…-windows-amd64-portable.zip` (portable) |
| Windows ARM64 | `…-windows-arm64-setup.exe` or `…-windows-arm64-portable.zip` |

The builds are not signed with a developer certificate: on macOS, right-click the app and choose "Open" the first time; on Windows, if SmartScreen warns, click "More info" → "Run anyway".

## Features

- **Servers**: servers and groups on this machine or on a ternssh site, arranged by drag and drop, with changes synced both ways.
- **Terminals**: tabs of SSH and local terminals, built on libghostty-vt.
- **Files**: browse files over SFTP and on this machine, upload and download, and double-click to edit in an editor with syntax highlighting.
- **Snippets**: run common commands in the terminal in one click; a site's snippets are shared with its web app.
- **Server status**: live CPU, memory, disk, network and process monitoring (Linux).
- **Web**: see [Step away, keep vibe coding](#step-away-keep-vibe-coding). Turned on in the settings, the app serves itself to a browser while it runs: the same tabs, servers, snippets and terminals, in step with the window both ways. The settings choose the port and who gets in: this machine only, the local network, or the public network; beyond this machine, browsers sign in with a username and password (HTTP basic authentication), and an address that keeps getting it wrong is locked out for longer each time. Put it behind HTTPS when it faces the internet.
- **Tray and global hotkey**: closing the window keeps the app in the tray, and a hotkey brings it back.
- **Languages**: Simplified Chinese and English, following the system by default, switchable in the settings.
- **Updates**: checks this repository's releases once a day, or from the settings and the tray, and installs a new version in the app, then relaunches.
