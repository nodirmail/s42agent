# Скрипты быстрой установки SA42Agent

В этой директории находятся скрипты для установки агента одной командой.

## Поддерживаемые ОС и архитектуры

* **Linux**: `amd64` (x86_64), `arm64` (aarch64)
* **macOS**: `amd64` (Intel), `arm64` (Apple Silicon M1/M2/M3/M4)
* **Windows**: `amd64` (x64), `arm64`

Бинарные файлы автоматически подтягиваются из [GitHub Releases](https://github.com/nodirmail/s42agent/releases/latest) под текущую платформу.

---

## 1. Использование (GitHub Raw)

Сразу после коммита и пуша в репозиторий скрипты доступны по ссылкам raw:

### Linux / macOS (Bash):
```bash
curl -fsSL https://raw.githubusercontent.com/nodirmail/s42agent/main/install/install.sh | bash
```

### Windows (PowerShell):
```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; irm https://raw.githubusercontent.com/nodirmail/s42agent/main/install/install.ps1 | iex
```

---

## 2. Использование через GitHub Pages (красивый URL)

Если в настройках репозитория на GitHub включить **Settings -> Pages** (ветка `main`):

Команда установки станет еще удобнее:
```bash
curl -fsSL https://nodirmail.github.io/s42agent/install/install.sh | bash
```
или для Windows:
```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; irm https://nodirmail.github.io/s42agent/install/install.ps1 | iex
```

---

## 3. Использование через собственный домен (например, `antigravity.google`)

Если настроить свой домен (например `cli.yourdomain.com` через Cloudflare / Nginx / Caddy), достаточно настроить редирект или отдачу этих файлов:

* Запрос `GET /install.sh` -> проксируется или редиректит на `install.sh`
* Запрос `GET /install.ps1` -> `install.ps1`

И пользователи смогут запускать установку лаконичной командой:
```bash
curl -fsSL https://cli.yourdomain.com/install.sh | bash
```
