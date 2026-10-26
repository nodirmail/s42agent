param(
    [switch]$Legacy
)

# ==============================================================================
# SA42Agent Installer for Windows (PowerShell)
# ==============================================================================

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repo = "nodirmail/s42agent"
$binaryName = "s42agent"

Write-Host "=== Установка SA42Agent ===" -ForegroundColor Cyan

# 1. Определение архитектуры и совместимости с ОС
$is64Bit = [System.Environment]::Is64BitOperatingSystem
$arch = if ($is64Bit) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else {
    "386"
}

# В версиях Windows NT до 10.0 (Windows 7 / Server 2008 R2 / 8 / 2012)
# отсутствует bcryptprimitives.dll, поэтому используется legacy-сборка Go 1.20
$osVersion = [System.Environment]::OSVersion.Version
$isLegacyOS = $Legacy -or ($osVersion.Major -lt 10)

if ($isLegacyOS) {
    $assetName = "${binaryName}-windows-${arch}-legacy.exe"
} else {
    if (-not $is64Bit) {
        Write-Error "32-битные операционные системы поддерживаются только через legacy сборку (Windows 7/2008)."
        exit 1
    }
    $assetName = "${binaryName}-windows-${arch}.exe"
}

$downloadUrl = "https://github.com/${repo}/releases/latest/download/${assetName}"

Write-Host "Платформа: windows/$arch" -ForegroundColor Green
if ($isLegacyOS) {
    Write-Host "Режим совместимости: Legacy (Windows Server 2008 R2 / Win 7 / 2012)" -ForegroundColor Yellow
}
Write-Host "Файл релиза: $assetName" -ForegroundColor Gray

# 2. Каталог установки в профиле пользователя (не требует прав администратора)
$installDir = "$env:LOCALAPPDATA\Programs\s42agent"
if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

$targetExe = "$installDir\${binaryName}.exe"
$aliasExe  = "$installDir\agent.exe"

# 3. Скачивание
Write-Host "⬇️  Загрузка из GitHub Releases..." -ForegroundColor Gray
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

try {
    Invoke-WebRequest -Uri $downloadUrl -OutFile $targetExe -UseBasicParsing
    # Дублируем как agent.exe для удобного запуска
    Copy-Item -Path $targetExe -Destination $aliasExe -Force
} catch {
    Write-Error "Ошибка скачивания с $downloadUrl : $_"
    exit 1
}

# 4. Добавление каталога в PATH
# 4.1. Всегда обновляем PATH текущей сессии, чтобы команды работали сразу же
if (($env:Path -split ';') -notcontains $installDir) {
    $env:Path = "$installDir;$env:Path"
}

# 4.2. Регистрируем алиасы для текущей сессии PowerShell
Set-Alias -Name s42agent -Value "$targetExe" -Scope Global -Force -ErrorAction SilentlyContinue
Set-Alias -Name agent -Value "$aliasExe" -Scope Global -Force -ErrorAction SilentlyContinue

# 4.3. Сохраняем в постоянный User PATH
$userPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
if (-not $userPath) { $userPath = "" }
$paths = $userPath -split ';' | Where-Object { $_ -ne "" }
if ($paths -notcontains $installDir) {
    $newUserPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
    [System.Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    Write-Host "ℹ️  Каталог добавлен в пользовательский PATH." -ForegroundColor Yellow
}

# 4.4. Если скрипт запущен от имени Администратора (например, на Windows Server) — добавляем и в системный Machine PATH
try {
    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsPrincipal]::WindowsBuiltInRole.Administrator)
    if ($isAdmin) {
        $machinePath = [System.Environment]::GetEnvironmentVariable("Path", "Machine")
        if (-not $machinePath) { $machinePath = "" }
        $mPaths = $machinePath -split ';' | Where-Object { $_ -ne "" }
        if ($mPaths -notcontains $installDir) {
            $newMachinePath = if ($machinePath) { "$machinePath;$installDir" } else { $installDir }
            [System.Environment]::SetEnvironmentVariable("Path", $newMachinePath, "Machine")
            Write-Host "ℹ️  Каталог добавлен в общесистемный (Machine) PATH." -ForegroundColor Yellow
        }
    }
} catch {
    # Игнорируем ошибки доступа к системному реестру
}

Write-Host "✅ SA42Agent успешно установлен в: $targetExe" -ForegroundColor Green
Write-Host ""
& $targetExe -version
Write-Host "Теперь вы можете использовать команду: s42agent (или agent)" -ForegroundColor Gray

