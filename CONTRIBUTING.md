# Contributing to pubg-lobby-fix

Thanks for your interest in the project.

## Prerequisites

- Windows 10/11 (the code depends on WinAPI)
- Go **1.27+** (see `go.mod`)

## Quick start

```powershell
git clone https://github.com/suprunchuk/pubg-lobby-fix.git
cd pubg-lobby-fix

go build -o pubg-lobby-fix.exe .
go test -shuffle=on ./...
```

## Development workflow

1. Fork → branch `feat/...` / `fix/...`
2. Make changes and add tests where they help
3. Run `go test ./...`
4. Open a PR against `main`

Prefer [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `refactor:` — release changelogs are built from them.

## Code guidelines

- Windows-first: use `*_windows.go`; do not add other-OS stubs unless needed
- Errors: wrap with `%w`; either log **or** return (not both)
- Use `slog` for logging; avoid `fmt.Println` on hot paths
- Do not commit `.exe` files, secrets, or local build artifacts

## Reporting issues

Use [GitHub Issues](https://github.com/suprunchuk/pubg-lobby-fix/issues). Please include:

- tool version (`-version`) or commit
- `go version` (if you built from source)
- Windows edition / architecture
- whether you ran **as Administrator**
- steps to reproduce and expected vs actual behavior
- a log snippet (`-v`)
