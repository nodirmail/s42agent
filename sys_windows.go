//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func initTerminal() bool {
	handleOut := windows.Handle(os.Stdout.Fd())
	var modeOut uint32
	if err := windows.GetConsoleMode(handleOut, &modeOut); err != nil {
		return false
	}
	modeOut |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(handleOut, modeOut); err != nil {
		return false
	}

	handleIn := windows.Handle(os.Stdin.Fd())
	var modeIn uint32
	if err := windows.GetConsoleMode(handleIn, &modeIn); err == nil {
		modeIn |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
		_ = windows.SetConsoleMode(handleIn, modeIn)
	}
	return true
}

func prepareRawTerminal(fd int) {
	handleIn := windows.Handle(fd)
	var modeIn uint32
	if err := windows.GetConsoleMode(handleIn, &modeIn); err == nil {
		modeIn |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
		_ = windows.SetConsoleMode(handleIn, modeIn)
	}
}

func getPlatformName() string {
	return "Windows Recovery AI Agent"
}

func getDefaultSystemPrompt() string {
	return "You are a terminal AI assistant for Windows administration, diagnostics, and recovery. " +
		"Your task is to diagnose and fix system issues using the provided tools. " +
		"Look for and download drivers and software only from official vendor sources (e.g., Intel, Realtek, AMD, NVIDIA) or official Microsoft resources (such as Microsoft Update Catalog). " +
		"Save all temporary and executable files (scripts, .bat, .cmd, .ps1, .exe) in the standard temp folder (%TEMP%) or the current working directory, never directly in the root of C:\\ or System32. " +
		"Always respond concisely, to the point, and in the language used by the user."
}

func getSysTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "execute_cmd",
				Description: "Выполнить команду в Windows через cmd.exe /c. Требует явного подтверждения пользователя.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Команда для выполнения (например, 'chkdsk c:', 'sfc /scannow').",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "get_sys_env",
				Description: "Получить системную информацию (диски, версию ОС, права администратора).",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "get_windows_env",
				Description: "Псевдоним для get_sys_env для обратной совместимости.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
	}
}

func executeCmd(command string) string {
	fmt.Printf("\n\033[33m[БЕЗОПАСНОСТЬ]\033[0m ИИ запрашивает выполнение команды:\n>>> \033[1m%s\033[0m\n", command)
	if !askConfirmation("Разрешить запуск команды? (y/N): ") {
		return "ОШИБКА: Запуск отклонен пользователем."
	}

	fmt.Println("\033[36m[Запуск]\033[0m Выполняется...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", command)
	output, err := cmd.CombinedOutput()

	var exitCode int
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("ОШИБКА: Превышен таймаут выполнения команды (2 мин).\nЧастичный вывод:\n%s", truncateOutput(string(output), 16000))
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	} else {
		exitCode = 0
	}

	return fmt.Sprintf("Код завершения: %d\nВывод:\n%s", exitCode, truncateOutput(string(output), 32000))
}

func checkWritePathSafety(path string) string {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	normalizedPath := strings.ToLower(filepath.ToSlash(cleanPath))
	if normalizedPath == "c:/" || normalizedPath == "c:" {
		return "ОШИБКА: Запись напрямую в корень диска C:\\ запрещена. Используйте папку %TEMP% или рабочую директорию."
	}
	if strings.HasPrefix(normalizedPath, "c:/windows/system32") || strings.HasPrefix(normalizedPath, "c:/windows/syswow64") {
		return "ОШИБКА: Запись в системную директорию Windows System32 заблокирована политикой безопасности."
	}
	return ""
}

func getSysEnv() string {
	fmt.Println("\n\033[36m[Система]\033[0m Запрос окружения (Windows)...")
	var sb strings.Builder
	sb.WriteString("=== ОС и Права ===\n")

	cmdVer := exec.Command("cmd.exe", "/c", "ver")
	if out, err := cmdVer.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	cmdAdmin := exec.Command("cmd.exe", "/c", "net session")
	if _, err := cmdAdmin.CombinedOutput(); err == nil {
		sb.WriteString("Права администратора: Да\n")
	} else {
		sb.WriteString("Права администратора: Нет или WinPE (ограниченный доступ)\n")
	}

	sb.WriteString("\n=== Список дисков ===\n")
	cmdDrives := exec.Command("cmd.exe", "/c", "fsutil fsinfo drives")
	if out, err := cmdDrives.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	} else {
		cmdWmic := exec.Command("cmd.exe", "/c", "wmic logicaldisk get caption")
		if out, err := cmdWmic.CombinedOutput(); err == nil {
			sb.Write(out)
		}
	}

	return sb.String()
}
