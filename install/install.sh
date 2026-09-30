#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# SA42Agent Installer for Linux & macOS
# ==============================================================================

REPO="nodirmail/s42agent"
BINARY_NAME="s42agent"

# Цвета для терминала
C_RESET="\033[0m"
C_RED="\033[31m"
C_GREEN="\033[1;32m"
C_YELLOW="\033[1;33m"
C_CYAN="\033[1;36m"
C_GRAY="\033[90m"

echo -e "${C_CYAN}=== Установка SA42Agent ===${C_RESET}"

# 1. Определение операционной системы
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)  TARGET_OS="linux" ;;
  darwin) TARGET_OS="darwin" ;;
  *)
    echo -e "${C_RED}❌ Ошибка:${C_RESET} Неподдерживаемая операционная система: $OS"
    exit 1
    ;;
esac

# 2. Определение архитектуры процессора
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) TARGET_ARCH="amd64" ;;
  arm64|aarch64) TARGET_ARCH="arm64" ;;
  *)
    echo -e "${C_RED}❌ Ошибка:${C_RESET} Неподдерживаемая архитектура процессора: $ARCH"
    exit 1
    ;;
esac

ASSET_NAME="${BINARY_NAME}-${TARGET_OS}-${TARGET_ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"

echo -e "Платформа: ${C_GREEN}${TARGET_OS}/${TARGET_ARCH}${C_RESET}"
echo -e "Файл релиза: ${C_GRAY}${ASSET_NAME}${C_RESET}"

# 3. Выбор каталога для установки
if [ "$EUID" -eq 0 ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

TEMP_FILE="$(mktemp)"
cleanup() {
  rm -f "$TEMP_FILE"
}
trap cleanup EXIT

# 4. Скачивание бинарника
echo -e "⬇️  Загрузка из GitHub Releases..."
if command -v curl >/dev/null 2>&1; then
  curl -fSL --progress-bar "$DOWNLOAD_URL" -o "$TEMP_FILE"
elif command -v wget >/dev/null 2>&1; then
  wget -q --show-progress -O "$TEMP_FILE" "$DOWNLOAD_URL"
else
  echo -e "${C_RED}❌ Ошибка:${C_RESET} Для установки требуется curl или wget."
  exit 1
fi

chmod +x "$TEMP_FILE"

# 5. Установка в целевую директорию
TARGET_PATH="${INSTALL_DIR}/${BINARY_NAME}"
echo -e "📦 Установка в ${C_CYAN}${TARGET_PATH}${C_RESET}..."

if [ -w "$INSTALL_DIR" ]; then
  mv "$TEMP_FILE" "$TARGET_PATH"
  # Также создаём удобный симлинк 'agent' -> 's42agent'
  ln -sf "$TARGET_PATH" "${INSTALL_DIR}/agent" 2>/dev/null || true
else
  echo -e "${C_YELLOW}🔐 Требуются права sudo для записи в ${INSTALL_DIR}...${C_RESET}"
  sudo mv "$TEMP_FILE" "$TARGET_PATH"
  sudo ln -sf "$TARGET_PATH" "${INSTALL_DIR}/agent" 2>/dev/null || true
fi

echo -e "${C_GREEN}✅ SA42Agent успешно установлен!${C_RESET}"

# 6. Проверка переменной PATH
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo -e "${C_YELLOW}⚠️  Внимание:${C_RESET} Каталог ${INSTALL_DIR} отсутствует в вашем \$PATH."
    echo -e "   Чтобы команда ${BINARY_NAME} была доступна отовсюду, добавьте в ~/.bashrc или ~/.zshrc:"
    echo -e "   ${C_CYAN}export PATH=\"${INSTALL_DIR}:\$PATH\"${C_RESET}"
    ;;
esac

echo ""
"${TARGET_PATH}" -version || true
echo -e "${C_GRAY}Для запуска введите: s42agent (или agent)${C_RESET}"
