# pubg-lobby-fix

[![Go Version](https://img.shields.io/github/go-mod/go-version/suprunchuk/pubg-lobby-fix)](https://go.dev/)
[![License: MIT](https://img.shields.io/github/license/suprunchuk/pubg-lobby-fix)](./LICENSE)
[![Build Status](https://img.shields.io/github/actions/workflow/status/suprunchuk/pubg-lobby-fix/test.yml?branch=main&label=tests)](https://github.com/suprunchuk/pubg-lobby-fix/actions/workflows/test.yml)
[![Lint](https://img.shields.io/github/actions/workflow/status/suprunchuk/pubg-lobby-fix/lint.yml?branch=main&label=lint)](https://github.com/suprunchuk/pubg-lobby-fix/actions/workflows/lint.yml)
[![Release](https://img.shields.io/github/v/release/suprunchuk/pubg-lobby-fix?display_name=tag)](https://github.com/suprunchuk/pubg-lobby-fix/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/suprunchuk/pubg-lobby-fix/total)](https://github.com/suprunchuk/pubg-lobby-fix/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/suprunchuk/pubg-lobby-fix)](https://goreportcard.com/report/github.com/suprunchuk/pubg-lobby-fix)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0078D6?logo=windows&logoColor=white)](#requirements)

**Languages:** English · [Русский](./README.ru.md)

> Windows CLI that force-closes `TslGame` (PUBG) TCP sockets so you can skip the 1–2 minute black screen when returning to the lobby after a match.

<p align="center">
  <a href="#-quick-start"><strong>Download & run</strong></a>
  ·
  <a href="#-how-it-works"><strong>How it works</strong></a>
  ·
  <a href="#-build-from-source"><strong>Build</strong></a>
  ·
  <a href="#-cli-reference"><strong>CLI</strong></a>
</p>

---

## Table of contents

- [The problem](#the-problem)
- [Quick start](#-quick-start)
- [How it works](#-how-it-works)
- [Requirements](#requirements)
- [In-game usage](#-in-game-usage)
- [CLI reference](#-cli-reference)
- [Build from source](#-build-from-source)
- [Architecture](#-architecture)
- [Releases & CI](#-releases--ci)
- [Contributing](#-contributing)
- [License](#-license)

---

## The problem

When a PUBG match ends, **Exit to Lobby** often leaves you on a black screen for 1–2 minutes. It looks like a hang, but the process is still running: the client is waiting for a TCP connection that never closes cleanly.

**pubg-lobby-fix** finds `TslGame` sockets and removes them via the Windows IP Helper API (`SetTcpEntry` → `DELETE_TCB`).

---

## 🚀 Quick start

> [!IMPORTANT]
> Run **as Administrator**. Without elevation, `SetTcpEntry` almost always fails with an access error.

### 1. Download a release

Open the **[Latest Release](https://github.com/suprunchuk/pubg-lobby-fix/releases/latest)** and grab the zip for your CPU:

| OS | Architecture | Release asset |
| -- | ------------ | ------------- |
| Windows | x64 (amd64) | `pubg-lobby-fix_*_windows_amd64.zip` |
| Windows | ARM64 | `pubg-lobby-fix_*_windows_arm64.zip` |

Extract the archive. Inside: `pubg-lobby-fix.exe`, `LICENSE`, `README.md`.

### 2. Run it

Right-click `pubg-lobby-fix.exe` → **Run as administrator**, or from an elevated PowerShell / cmd:

```powershell
.\pubg-lobby-fix.exe
```

The tool waits for the global hotkey **`Ctrl+Shift+L`**. Console output looks like:

```text
level=INFO msg="waiting for hotkey" hotkey=ctrl+shift+l processes=TslGame
level=INFO msg="run as administrator — SetTcpEntry needs elevation"
```

### 3. During a match

1. Match over → press **Exit to Lobby**.
2. Press **`Ctrl+Shift+L`** (or your custom hotkey).
3. Watch the log for `closed` / `done` — the black screen usually clears immediately.

Stop the tool with `Ctrl+C` in the console window.

<details>
<summary><strong>One-shot mode</strong> (close sockets and exit)</summary>

```powershell
.\pubg-lobby-fix.exe -once
```

</details>

<details>
<summary><strong>List sockets without closing</strong></summary>

```powershell
.\pubg-lobby-fix.exe -list
```

</details>

---

## ⚙️ How it works

| Step | What the program does | WinAPI / package |
| ---- | --------------------- | ---------------- |
| 1 | Finds `TslGame` processes | Toolhelp32 (`internal/process`) |
| 2 | Reads the TCP table with PIDs | `GetExtendedTcpTable` (`internal/tcp`) |
| 3 | Keeps connections whose remote ≠ `0.0.0.0` | — |
| 4 | Sets each socket to `DELETE_TCB` | `SetTcpEntry` (`internal/tcp`) |
| 5 | Trigger — global Windows hotkey | `RegisterHotKey` (`internal/hotkey`) |

The game process is **not** killed. Only TCP control blocks for the selected process are torn down; the client handles the drop and returns to the lobby.

---

## Requirements

| | |
| -- | -- |
| OS | **Windows 10 / 11** only (`iphlpapi.dll`, `user32.dll`) |
| Privileges | Administrator (for `SetTcpEntry`) |
| Game | Running PUBG client (`TslGame.exe`) |
| Build | Go **1.26+** (only if you build yourself) |

Linux and macOS are not supported — the required WinAPI is missing there.

---

## 🎮 In-game usage

```text
┌─────────────────────────────────────────────────────────┐
│  1. Start pubg-lobby-fix.exe as Administrator          │
│  2. Play as usual                                       │
│  3. Match end → Exit to Lobby → black screen            │
│  4. Press Ctrl+Shift+L                                  │
│  5. Lobby without the long wait                         │
└─────────────────────────────────────────────────────────┘
```

> [!TIP]
> Change the hotkey with `-hotkey f9` or `-hotkey alt+shift+q`. Format: modifiers `ctrl` / `alt` / `shift` / `win` plus a key (`a`–`z`, `0`–`9`, `f1`–`f24`, `space`, `esc`, …).

> [!WARNING]
> Closing **all** `TslGame` TCP connections mid-match will drop your network session. Use this when you are already exiting to the lobby — not during an active fight.

---

## 📖 CLI reference

```text
pubg-lobby-fix [flags]
```

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-hotkey` | `ctrl+shift+l` | Global hotkey |
| `-process` | `TslGame` | Comma-separated process names (no `.exe`) |
| `-pause` | `100ms` | Delay between `SetTcpEntry` calls |
| `-once` | `false` | Close connections once and exit |
| `-list` | `false` | List sockets only; do not close |
| `-version` | — | Version / commit / build date |
| `-v` | `false` | Debug logs (`slog`) |
| `-h` | — | Help |

### Examples

```powershell
# Background mode with a custom hotkey
.\pubg-lobby-fix.exe -hotkey f9

# Multiple process names
.\pubg-lobby-fix.exe -process TslGame,PUBG

# Diagnostics
.\pubg-lobby-fix.exe -list -v

# Release version string
.\pubg-lobby-fix.exe -version
```

---

## 🔨 Build from source

### Prerequisites

- Windows
- [Go 1.26+](https://go.dev/dl/)
- Git

### Build

```powershell
git clone https://github.com/suprunchuk/pubg-lobby-fix.git
cd pubg-lobby-fix
go build -o pubg-lobby-fix.exe .
```

### Tests and lint

```powershell
go test -race -shuffle=on ./...
go vet ./...
golangci-lint run ./...   # if golangci-lint is installed
```

### Install with `go install`

```powershell
go install github.com/suprunchuk/pubg-lobby-fix@latest
```

The binary lands in `%USERPROFILE%\go\bin` (add that folder to `PATH`). You still need to run it **as Administrator**.

### Repository layout

```text
pubg-lobby-fix/
├── main.go                 # CLI, flags, version ldflags
├── internal/
│   ├── app/                # orchestration: hotkey → list → close
│   ├── tcp/                # GetExtendedTcpTable + SetTcpEntry
│   ├── process/            # PID lookup by name
│   └── hotkey/             # RegisterHotKey + message loop
├── LEGACY_DOT_NET/         # original C#/WPF prototype
└── .github/workflows/      # test, lint, security, release
```

---

## 🏗 Architecture

```text
                    ┌─────────────┐
   Ctrl+Shift+L ───►│   hotkey    │
                    └──────┬──────┘
                           │
                           ▼
                    ┌─────────────┐     Toolhelp32
                    │     app     │◄──────────────── process names
                    └──────┬──────┘
              list │       │ close
                   ▼       ▼
              ┌──────────────────────────┐
              │        tcp               │
              │  GetExtendedTcpTable     │
              │  SetTcpEntry(DELETE_TCB) │
              └──────────────────────────┘
```

Packages live under `internal/` — this is an application, not a public library.

---

## 🏷 Releases & CI

Pushing a `v*` tag runs [GoReleaser](https://goreleaser.com/): Windows binaries (amd64/arm64), `checksums.txt`, and a changelog from commits since the previous tag.

```powershell
git tag v1.0.1
git push origin v1.0.1
```

On `main` / PRs:

| Workflow | Checks |
| -------- | ------ |
| [test.yml](./.github/workflows/test.yml) | `go test -race`, coverage |
| [lint.yml](./.github/workflows/lint.yml) | `go vet`, golangci-lint |
| [security.yml](./.github/workflows/security.yml) | govulncheck, gosec, CodeQL, Bearer |
| [release.yml](./.github/workflows/release.yml) | release on tag |

Dependabot updates Go modules and GitHub Actions weekly.

---

## 🤝 Contributing

Basic loop:

```powershell
go test -race ./...
go build -o pubg-lobby-fix.exe .
```

Details: [CONTRIBUTING.md](./CONTRIBUTING.md). Bugs and ideas: [Issues](https://github.com/suprunchuk/pubg-lobby-fix/issues).

---

## 📄 License

[MIT](./LICENSE) © 2026 Pasha Suprunchuk

---

<details>
<summary><strong>Disclaimer</strong></summary>

<br>

This tool changes the network connection state of a local Windows process. Use at your own risk. The author is not affiliated with PUBG / Krafton / Microsoft. Follow the game rules and platform Terms of Service.

</details>
