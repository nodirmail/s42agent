//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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
	return "Linux Recovery AI Agent"
}

func getDefaultSystemPrompt() string {
	return "You are a terminal AI assistant for Linux administration, diagnostics, and recovery. " +
		"Your task is to diagnose and fix system issues using the provided tools. " +
		"Use standard tools like systemctl, journalctl, dmesg, ip, ss, ps, top, and official package managers (apt, dnf, pacman, yum). " +
		"Save all temporary files, scripts, and downloaded packages in /tmp or the current working directory, never directly in / or system binaries paths. " +
		"Never modify critical security or system files like /etc/shadow, /etc/passwd, or /boot directly without strict confirmation. " +
		"Always respond concisely, to the point, and in the language used by the user."
}

func getSysTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: FunctionDefinition{
				Name:        "execute_cmd",
				Description: "Выполнить команду в Linux через bash/sh. Требует явного подтверждения пользователя.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "Команда для выполнения (например, 'systemctl status nginx', 'df -h').",
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
				Description: "Получить системную информацию Linux (дистрибутив, ядро, диски, права root).",
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
	shell := "/bin/bash"
	if _, err := os.Stat(shell); os.IsNotExist(err) {
		shell = "/bin/sh"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	output, err := cmd.CombinedOutput()

	var exitCode int
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return fmt.Sprintf("ОШИБКА: Выполнение команды прервано пользователем (Ctrl+C).\nЧастичный вывод:\n%s", truncateOutput(string(output), 16000))
		}
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
	criticalPaths := []string{"/etc/shadow", "/etc/passwd", "/etc/sudoers", "/boot"}
	for _, cp := range criticalPaths {
		if cleanPath == cp || strings.HasPrefix(cleanPath, cp+"/") {
			return fmt.Sprintf("ОШИБКА: Запись в критический системный путь '%s' заблокирована политикой безопасности.", cleanPath)
		}
	}
	return ""
}

func getSysEnv() string {
	fmt.Println("\n\033[36m[Система]\033[0m Запрос окружения (Linux)...")
	var sb strings.Builder
	sb.WriteString("=== ОС и Права ===\n")

	if os.Geteuid() == 0 {
		sb.WriteString("Права root: Да\n")
	} else {
		sb.WriteString("Права root: Нет (обычный пользователь)\n")
	}

	// Информация о ядре
	cmdKernel := exec.Command("uname", "-a")
	if out, err := cmdKernel.CombinedOutput(); err == nil {
		sb.WriteString("Ядро: " + strings.TrimSpace(string(out)) + "\n")
	}

	// Дистрибутив
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				val := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				sb.WriteString("Дистрибутив: " + val + "\n")
				break
			}
		}
	}

	// Использование дисков
	sb.WriteString("\n=== Использование дисков (df -h) ===\n")
	cmdDf := exec.Command("df", "-h")
	if out, err := cmdDf.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	// Память
	sb.WriteString("\n=== Память (free -h) ===\n")
	cmdFree := exec.Command("free", "-h")
	if out, err := cmdFree.CombinedOutput(); err == nil {
		sb.WriteString(strings.TrimSpace(string(out)) + "\n")
	}

	return sb.String()
}
