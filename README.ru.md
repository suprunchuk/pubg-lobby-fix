# pubg-lobby-fix

[![Go Version](https://img.shields.io/github/go-mod/go-version/suprunchuk/pubg-lobby-fix)](https://go.dev/)
[![License: MIT](https://img.shields.io/github/license/suprunchuk/pubg-lobby-fix)](./LICENSE)
[![Build Status](https://img.shields.io/github/actions/workflow/status/suprunchuk/pubg-lobby-fix/test.yml?branch=main&label=tests)](https://github.com/suprunchuk/pubg-lobby-fix/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/suprunchuk/pubg-lobby-fix?display_name=tag)](https://github.com/suprunchuk/pubg-lobby-fix/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/suprunchuk/pubg-lobby-fix/total)](https://github.com/suprunchuk/pubg-lobby-fix/releases)
[![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0078D6?logo=windows&logoColor=white)](#требования)

**Языки:** [English](./README.md) · Русский

> Утилита для Windows, живущая в системном трее: принудительно закрывает TCP-сокеты процесса `TslGame` (PUBG), чтобы пропустить чёрный экран на 1–2 минуты при выходе в лобби после матча. Клик по иконке закрывает сокеты, уведомление показывает результат.

<p align="center">
  <a href="#-быстрый-старт"><strong>Скачать и запустить</strong></a>
  ·
  <a href="#-как-это-работает"><strong>Как работает</strong></a>
  ·
  <a href="#-сборка-из-исходников"><strong>Сборка</strong></a>
  ·
  <a href="#-cli-справочник"><strong>CLI</strong></a>
</p>

---

## Оглавление

- [Проблема](#проблема)
- [Быстрый старт](#-быстрый-старт)
- [Как это работает](#-как-это-работает)
- [Требования](#требования)
- [Использование в игре](#-использование-в-игре)
- [CLI-справочник](#-cli-справочник)
- [Сборка из исходников](#-сборка-из-исходников)
- [Архитектура](#-архитектура)
- [Релизы и CI](#-релизы-и-ci)
- [Contributing](#-contributing)
- [License](#-license)

---

## Проблема

Когда матч в PUBG закончился, кнопка **Exit to Lobby** часто оставляет игру на чёрном экране на 1–2 минуты. На практике это выглядит как «зависание», но процесс жив: клиент ждёт закрытия TCP-соединения, которое вовремя не рвётся.

**pubg-lobby-fix** находит сокеты `TslGame` и удаляет их через Windows IP Helper API (`SetTcpEntry` → `DELETE_TCB`), затем проверяет результат; для соединений, которые `SetTcpEntry` удалить не может (например IPv6 — публичного API удаления для них нет), применяется короткая блокировка всего трафика игры через Windows Filtering Platform.

---

## 🚀 Быстрый старт

> [!IMPORTANT]
> Для закрытия сокетов нужны **права администратора**. Если утилита запущена без них, она сама перезапустится через UAC-запрос (отключается флагом `-no-elevate`).

> [!TIP]
> Утилита обновляется сама: при запуске она скачивает свежий релиз, проверяет его sha256-контрольную сумму, подменяет exe и перезапускается уже новой версией. Отключается флагом `-no-update`.

### 1. Скачай релиз

Открой **[Latest Release](https://github.com/suprunchuk/pubg-lobby-fix/releases/latest)** и возьми zip под свою архитектуру:

| ОС | Архитектура | Файл в релизе |
| -- | ----------- | ------------- |
| Windows | x64 (amd64) | `pubg-lobby-fix_*_windows_amd64.zip` |
| Windows | ARM64 | `pubg-lobby-fix_*_windows_arm64.zip` |

Распакуй архив. Внутри: `pubg-lobby-fix.exe`, `LICENSE`, `README.md`.

### 2. Запусти

Правый клик по `pubg-lobby-fix.exe` → **Запуск от имени администратора**, либо из elevated PowerShell / cmd:

```powershell
.\pubg-lobby-fix.exe
```

Утилита живёт в **системном трее** (зелёная иконка с молнией) и ждёт глобальный хоткей **`Ctrl+Shift+L`**. При запуске напрямую (двойной клик или UAC-перезапуск) окно консоли скрывается; с `-no-tray` утилита остаётся обычным консольным приложением. Лог в консоли выглядит так:

```text
time="2026-09-05 18:12:03" level=INFO msg="waiting for hotkey" hotkey=ctrl+shift+l processes=TslGame
time="2026-09-05 18:14:11" level=INFO msg="hotkey pressed, closing lobby connections"
time="2026-09-05 18:14:12" level=INFO msg=done closed=7 already_gone=0 failed=0 survived=0
```

### 3. В матче

1. Матч окончен → нажал **Exit to Lobby**.
2. Нажал **`Ctrl+Shift+L`** (или свой хоткей) — либо кликнул левой кнопкой по иконке в трее.
3. Windows-уведомление покажет результат (`Sockets closed`, `Nothing to close`, …) — чёрный экран обычно пропадает сразу.

Остановка: пункт **Exit** в меню иконки трея или `Ctrl+C` в окне консоли.

<details>
<summary><strong>Одноразовый режим</strong> (закрыть сокеты и выйти)</summary>

```powershell
.\pubg-lobby-fix.exe -once
```

</details>

<details>
<summary><strong>Посмотреть сокеты без закрытия</strong></summary>

```powershell
.\pubg-lobby-fix.exe -list
```

</details>

---

## ⚙️ Как это работает

| Шаг | Что делает программа | WinAPI / пакет |
| --- | -------------------- | -------------- |
| 1 | Ищет процессы `TslGame` и их исполняемые файлы | Toolhelp32 (`internal/process`) |
| 2 | Читает TCP-таблицы **IPv4 и IPv6** с PID | `GetExtendedTcpTable` (`internal/tcp`) |
| 3 | Удаляет живые IPv4 control blocks (`DELETE_TCB`), перечитывает таблицу и в несколько раундов повторяет для «выживших» | `SetTcpEntry` (`internal/tcp`) |
| 4 | Если что-то выжило (для IPv6 API удаления нет), ненадолго блокирует **весь** трафик игры через WFP-фильтры, которые исчезают вместе с утилитой даже при падении | `FwpmFilterAdd0` в dynamic-сессии (`internal/wfp`) |
| 5 | Триггер — глобальный хоткей Windows или клик по иконке в трее | `RegisterHotKey` (`internal/hotkey`), `Shell_NotifyIconW` (`internal/tray`) |

Процесс игры **не убивается**. Рвутся только TCP control blocks выбранного процесса (либо его трафик кратко блокируется) — клиент сам обрабатывает обрыв и уходит в лобби.

---

## Требования

| | |
| -- | -- |
| ОС | **Windows 10 / 11** only (нужны `iphlpapi.dll`, `user32.dll`) |
| Права | Администратор (запрашивается автоматически через UAC) |
| Игра | Запущенный клиент PUBG (`TslGame.exe`) |
| Сборка | Go **1.27+** (только если собираешь сам) |

Linux и macOS не поддерживаются: на них нет нужного WinAPI.

---

## 🎮 Использование в игре

```text
┌─────────────────────────────────────────────────────────┐
│  1. Запусти pubg-lobby-fix.exe от администратора        │
│  2. Играй как обычно                                    │
│  3. Конец матча → Exit to Lobby → чёрный экран          │
│  4. Нажми Ctrl+Shift+L (или кликни иконку в трее)       │
│  5. Лобби без долгого ожидания                          │
└─────────────────────────────────────────────────────────┘
```

> [!TIP]
> Хоткей можно сменить: `-hotkey f9`, `-hotkey alt+shift+q`. Формат: модификаторы `ctrl` / `alt` / `shift` / `win` и клавиша (`a`–`z`, `0`–`9`, `f1`–`f24`, `space`, `esc`, …).

> [!WARNING]
> Закрытие **всех** TCP-соединений `TslGame` в середине живого матча оборвёт сетевой сеанс. Используй инструмент в сценарии «уже выхожу в лобби», а не во время активного боя.

---

## 📖 CLI-справочник

```text
pubg-lobby-fix [flags]
```

| Флаг | По умолчанию | Описание |
| ---- | ------------ | -------- |
| `-hotkey` | `ctrl+shift+l` | Глобальный хоткей |
| `-process` | `TslGame` | Имена процессов через запятую (без `.exe`) |
| `-pause` | `25ms` | Пауза между вызовами `SetTcpEntry` |
| `-rounds` | `4` | Раунды «закрыть + проверить» до отката к блокировке трафика |
| `-block` | `10s` | WFP-фолбэк: блокировать весь трафик игры это время, если соединения выжили (`0` — отключить) |
| `-no-elevate` | `false` | Не перезапускать себя с правами администратора |
| `-no-update` | `false` | Отключить автообновление при запуске |
| `-no-tray` | `false` | Не показывать иконку в трее; оставить окно консоли |
| `-once` | `false` | Закрыть соединения один раз и выйти |
| `-list` | `false` | Только показать сокеты, не закрывать |
| `-json` | `false` | Вместе с `-list`: вывести соединения в JSON на stdout (логи уходят в stderr) |
| `-version` | — | Версия / commit / дата сборки |
| `-v` | `false` | Debug-логи (`slog`) |
| `-h` | — | Справка |

### Примеры

```powershell
# Фон + свой хоткей
.\pubg-lobby-fix.exe -hotkey f9

# Несколько имён процессов
.\pubg-lobby-fix.exe -process TslGame,PUBG

# Диагностика
.\pubg-lobby-fix.exe -list -v

# Список соединений в JSON для скриптов
.\pubg-lobby-fix.exe -list -json

# Версия из релиза
.\pubg-lobby-fix.exe -version
```

---

## 🔨 Сборка из исходников

### Предусловия

- Windows
- [Go 1.27+](https://go.dev/dl/)
- Git

### Сборка

```powershell
git clone https://github.com/suprunchuk/pubg-lobby-fix.git
cd pubg-lobby-fix
go build -o pubg-lobby-fix.exe .
```

### Тесты

```powershell
go test -shuffle=on ./...
```

### Установка через `go install`

```powershell
go install github.com/suprunchuk/pubg-lobby-fix@latest
```

Бинарник попадёт в `%USERPROFILE%\go\bin` (добавь каталог в `PATH`). Права администратора утилита запросит сама через UAC.

### Структура репозитория

```text
pubg-lobby-fix/
├── main.go                 # CLI, флаги, version ldflags
├── internal/
│   ├── app/                # оркестрация: hotkey/tray → list → close → verify
│   ├── tcp/                # GetExtendedTcpTable (v4+v6) + SetTcpEntry
│   ├── wfp/                # WFP-фолбэк: блокировка трафика в dynamic-сессии
│   ├── elevate/            # проверка прав + перезапуск через UAC
│   ├── process/            # поиск PID по имени
│   ├── hotkey/             # RegisterHotKey + message loop
│   └── tray/               # Shell_NotifyIcon: иконка, меню, уведомления
└── .github/workflows/      # test, security, release
```

---

## 🏗 Архитектура

```text
                    ┌─────────────┐
   Ctrl+Shift+L ───►│   hotkey    │
                    └──────┬──────┘
                           │
                           ▼
                    ┌─────────────┐     Toolhelp32
                    │     app     │◄──────────────── process names
                    └──────┬──────┘
              list │       │ close + verify rounds
                   ▼       ▼
              ┌──────────────────────────┐    выжившие?
              │        tcp               │────────────┐
              │  GetExtendedTcpTable     │            │
              │  SetTcpEntry(DELETE_TCB) │            ▼
              └──────────────────────────┘  ┌──────────────────┐
                                            │       wfp        │
                                            │ dynamic-сессия:  │
                                            │ блокируем exe    │
                                            │ игры на секунды  │
                                            └──────────────────┘
```

Пакеты не экспортируются наружу (`internal/`) — это приложение, не библиотека.

---

## 🏷 Релизы и CI

Пуш тега `v*` запускает [GoReleaser](https://goreleaser.com/): Windows-бинарники (amd64/arm64), `checksums.txt`, changelog по коммитам с прошлого тега.

```powershell
git tag v1.0.1
git push origin v1.0.1
```

На `main` / PR работают:

| Workflow | Что проверяет |
| -------- | ------------- |
| [test.yml](./.github/workflows/test.yml) | `go test`, coverage |
| [security.yml](./.github/workflows/security.yml) | govulncheck, gosec, CodeQL, Bearer |
| [release.yml](./.github/workflows/release.yml) | релиз по тегу |

Dependabot раз в неделю обновляет Go-модули и GitHub Actions.

---

## 🤝 Contributing

Базовый цикл:

```powershell
go test -shuffle=on ./...
go build -o pubg-lobby-fix.exe .
```

Подробности — в [CONTRIBUTING.md](./CONTRIBUTING.md) (на английском). Баги и идеи: [Issues](https://github.com/suprunchuk/pubg-lobby-fix/issues).

---

## 📄 License

[MIT](./LICENSE) © 2026 Pasha Suprunchuk

---

<details>
<summary><strong>Disclaimer</strong></summary>

<br>

Утилита меняет состояние сетевых соединений локального процесса Windows. Используй на свой страх и риск. Автор не связан с PUBG / Krafton / Microsoft. Соблюдай правила игры и ToS платформы.

</details>
