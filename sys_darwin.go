//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func initTerminal() bool {
	return true
}

func prepareRawTerminal(fd int) {
}

func getPlatformName() string {
	return "macOS AI Agent"
}

func getDefaultSystemPrompt() string {
	return "You are a terminal AI assistant for macOS system administration, diagnostics, and recovery. " +
		"Your task is to diagnose and resolve system issues using the provided tools (e.g., launchctl, system_profiler, diskutil, defaults, brew). " +
		"Save all temporary and executable files in $TMPDIR, /tmp, or the current working directory, respecting System Integrity Protection (SIP) boundaries. " +
		"Always respond concisely, to the point, and in the language used by the user."
}

func getSysTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "execute_cmd",
				Description: "Выполнить команду в macOS через /bin/zsh -c (или /bin/bash). Требует явного подтверждения пользователя.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Команда macOS (например, 'diskutil list', 'system_profiler SPHardwareDataType', 'brew doctor').",
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
				Description: "Получить системную информацию macOS (версия macOS, ядро Darwin, диски, память, права root).",
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
				Description: "Псевдоним для get_sys_env (для совместимости с имеющимися сессиями).",
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
	shell := "/bin/zsh"
	if _, err := os.Stat(shell); os.IsNotExist(err) {
		shell = "/bin/bash"
		if _, err := os.Stat(shell); os.IsNotExist(err) {
			shell = "/bin/sh"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-c", command)
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
	if cleanPath == "/" || cleanPath == "." {
		return "ОШИБКА: Запись напрямую в корень / или текущую директорию '.' запрещена. Укажите конкретный файл."
	}
	criticalPaths := []string{
		"/System",
		"/usr/bin",
		"/usr/sbin",
		"/bin",
		"/sbin",
		"/etc/sudoers",
		"/etc/master.passwd",
		"/private/etc/master.passwd",
		"/private/etc/sudoers",
	}
	for _, cp := range criticalPaths {
		if cleanPath == cp || strings.HasPrefix(cleanPath, cp+"/") {
			return fmt.Sprintf("ОШИБКА: Запись в системную директорию macOS '%s' заблокирована политикой безопасности.", cleanPath)
		}
	}
	return ""
}

func getSysEnv() string {
	fmt.Println("\n\033[36m[Система]\033[0m Запрос окружения (macOS)...")
	var sb strings.Builder
	sb.WriteString("=== ОС и Права ===\n")

	if os.Geteuid() == 0 {
		sb.WriteString("Права root: Да\n")
	} else {
		sb.WriteString("Права root: Нет (обычный пользователь)\n")
	}

	// Версия macOS (sw_vers)
	cmdSwVers := exec.Command("sw_vers")
	if out, err := cmdSwVers.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	// Информация о ядре
	cmdKernel := exec.Command("uname", "-a")
	if out, err := cmdKernel.CombinedOutput(); err == nil {
		sb.WriteString("Ядро: " + strings.TrimSpace(string(out)) + "\n")
	}

	// Использование дисков
	sb.WriteString("\n=== Использование дисков (df -h) ===\n")
	cmdDf := exec.Command("df", "-h")
	if out, err := cmdDf.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	// Память и аппаратная часть
	sb.WriteString("\n=== Память и процессор ===\n")
	cmdMem := exec.Command("sysctl", "hw.memsize", "hw.ncpu", "machdep.cpu.brand_string")
	if out, err := cmdMem.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	return sb.String()
}
