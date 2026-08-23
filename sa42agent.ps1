# ==============================================================================
# SA42Agent - PowerShell Quick Launcher
# ==============================================================================

$ErrorActionPreference = "Stop"

# --- Конфигурация доступа к серверу загрузки ---
$downloadUrl = "http://qwerty.ddns.me:280/agent.exe"
$authLogin   = "test"
$authPass    = "test"

# --- Конфигурация ИИ (Передается только в оперативную память процесса) ---
$apiUrl   = "https://api.openai.com/v1"      # Замените на ваш URL API (например, http://localhost:8080/v1 или прокси)
$apiKey   = "YOUR_AI_API_KEY_HERE"           # Замените на ваш API Key
$aiModel  = "gpt-4o"                         # Замените на вашу модель

# --- Директория установки в профиле пользователя ---
$installDir = Join-Path $env:LOCALAPPDATA "sa42agent"
$exePath    = Join-Path $installDir "agent.exe"

# Создаем папку в %LOCALAPPDATA%\sa42agent, если не существует
if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

# --- Скачивание / обновление агента ---
Write-Host "[SA42Agent] Проверка и загрузка агента в $installDir..." -ForegroundColor Cyan

# Поддержка TLS 1.2+
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls13

# Авторизация HTTP Basic Auth
$pair = "${authLogin}:${authPass}"
$encodedAuth = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes($pair))
$headers = @{
    Authorization = "Basic $encodedAuth"
}

try {
    # Загружаем актуальный agent.exe с сервера
    Invoke-WebRequest -Uri $downloadUrl -Headers $headers -OutFile $exePath -UseBasicParsing
    Write-Host "[SA42Agent] Исполняемый файл успешно обновлен." -ForegroundColor Green
} catch {
    if (Test-Path $exePath) {
        Write-Host "[SA42Agent] Предупреждение: Не удалось обновить файл с сервера. Используем локальную копию." -ForegroundColor Yellow
    } else {
        Write-Error "[SA42Agent] Ошибка скачивания агента с $downloadUrl : $_"
        exit 1
    }
}

# --- Запуск агента ---
Write-Host "[SA42Agent] Запуск агента..." -ForegroundColor Green
Set-Location -Path $installDir

# Передаем параметры напрямую в процесс agent.exe (без записи config.json на диск)
& $exePath -url $apiUrl -key $apiKey -model $aiModel
