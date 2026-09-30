# ==============================================================================
# SA42Agent Installer for Windows (PowerShell)
# ==============================================================================

$ErrorActionPreference = "Stop"

$repo = "nodirmail/s42agent"
$binaryName = "s42agent"

Write-Host "=== Установка SA42Agent ===" -ForegroundColor Cyan

# 1. Определение архитектуры
$arch = if ([System.Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else {
    Write-Error "32-битные операционные системы не поддерживаются."
    exit 1
}

$assetName = "${binaryName}-windows-${arch}.exe"
$downloadUrl = "https://github.com/${repo}/releases/latest/download/${assetName}"

Write-Host "Платформа: windows/$arch" -ForegroundColor Green
Write-Host "Файл релиза: $assetName" -ForegroundColor Gray

# 2. Каталог установки в профиле пользователя (не требует прав администратора)
$installDir = Join-Path $env:LOCALAPPDATA "Programs" "s42agent"
if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

$targetExe = Join-Path $installDir "${binaryName}.exe"
$aliasExe  = Join-Path $installDir "agent.exe"

# 3. Скачивание
Write-Host "⬇️  Загрузка из GitHub Releases..." -ForegroundColor Gray
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls13

try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $targetExe -UseBasicParsing
    # Дублируем как agent.exe для удобного запуска
    Copy-Item -Path $targetExe -Destination $aliasExe -Force
} catch {
    Write-Error "Ошибка скачивания с $downloadUrl : $_"
    exit 1
}

# 4. Добавление каталога в пользовательский PATH (если еще не добавлен)
$userPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -split ';' -notcontains $installDir) {
    $newUserPath = if ([string]::IsNullOrEmpty($userPath)) { $installDir } else { "$userPath;$installDir" }
    [System.Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    $env:Path = "$env:Path;$installDir"
    Write-Host "ℹ️  Каталог $installDir добавлен в системный PATH пользователя." -ForegroundColor Yellow
}

Write-Host "✅ SA42Agent успешно установлен в: $targetExe" -ForegroundColor Green
Write-Host ""
& $targetExe -version
Write-Host "Для запуска откройте новый терминал и введите: s42agent (или agent)" -ForegroundColor Gray
