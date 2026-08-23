package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// === Структуры данных для OpenAI-совместимого API ===

type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"` // *string позволяет отправлять null, когда есть ToolCalls
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ChatCompletionRequest struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Config struct {
	URL          string `json:"url"`
	Key          string `json:"key"`
	Model        string `json:"model"`
	ContextLimit int    `json:"context_limit,omitempty"`
}

var autoApprove bool
var reader = bufio.NewReader(os.Stdin)

func readLineWithPrompt(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !initTerminal() {
		if prompt != "" {
			fmt.Print(prompt)
		}
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(text, "\r\n"), nil
	}

	state, err := term.MakeRaw(fd)
	if err != nil {
		if prompt != "" {
			fmt.Print(prompt)
		}
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(text, "\r\n"), nil
	}
	defer term.Restore(fd, state)

	prepareRawTerminal(fd)

	screen := struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}

	t := term.NewTerminal(screen, prompt)
	if w, h, err := term.GetSize(fd); err == nil && w > 0 {
		_ = t.SetSize(w, h)
	}
	line, err := t.ReadLine()
	if err != nil {
		return "", err
	}
	return line, nil
}

func readLineWithPromptExitOnError(prompt string) string {
	val, err := readLineWithPrompt(prompt)
	if err != nil {
		if err == io.EOF {
			fmt.Println("\nВыход.")
			os.Exit(0)
		}
		fmt.Printf("\n[ОШИБКА] Ошибка ввода: %v\n", err)
		os.Exit(1)
	}
	return val
}

func readCSISequence(r *bufio.Reader) string {
	var seq []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			break
		}
		seq = append(seq, b)
		if (b >= 0x40 && b <= 0x7E) || len(seq) >= 32 {
			break
		}
	}
	return string(seq)
}

func readMultilineInput(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !initTerminal() {
		if prompt != "" {
			fmt.Print(prompt)
		}
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(text, "\r\n"), nil
	}

	state, err := term.MakeRaw(fd)
	if err != nil {
		if prompt != "" {
			fmt.Print(prompt)
		}
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(text, "\r\n"), nil
	}
	defer term.Restore(fd, state)

	prepareRawTerminal(fd)

	// Включаем Bracketed Paste Mode (безопасная вставка многострочного текста)
	os.Stdout.WriteString("\x1b[?2004h")
	defer os.Stdout.WriteString("\x1b[?2004l")

	if prompt != "" {
		fmt.Print(prompt)
	}

	var runes []rune
	continuationPrompt := "... "
	inPaste := false
	inReader := bufio.NewReader(os.Stdin)

	for {
		r, _, err := inReader.ReadRune()
		if err != nil {
			if len(runes) > 0 {
				fmt.Print("\r\n")
				return string(runes), nil
			}
			return "", err
		}

		// Обработка Escape-последовательностей
		if r == '\x1b' {
			if inReader.Buffered() > 0 {
				next, err := inReader.Peek(1)
				if err == nil && len(next) > 0 {
					if next[0] == '\r' || next[0] == '\n' {
						// Alt+Enter
						_, _ = inReader.ReadByte()
						if next[0] == '\r' && inReader.Buffered() > 0 {
							if next2, err := inReader.Peek(1); err == nil && len(next2) > 0 && next2[0] == '\n' {
								_, _ = inReader.ReadByte()
							}
						}
						runes = append(runes, '\n')
						fmt.Print("\r\n" + continuationPrompt)
						continue
					} else if next[0] == '[' {
						_, _ = inReader.ReadByte() // '['
						seq := readCSISequence(inReader)
						if seq == "200~" {
							inPaste = true
							continue
						} else if seq == "201~" {
							inPaste = false
							continue
						} else if seq == "13;2u" || seq == "27;2;13~" || seq == "13;3u" || seq == "13;5u" {
							// Shift+Enter / Alt+Enter / Ctrl+Enter в терминалах с расширенным протоколом
							runes = append(runes, '\n')
							fmt.Print("\r\n" + continuationPrompt)
							continue
						}
						continue
					} else if next[0] == 'O' {
						_, _ = inReader.ReadByte() // 'O'
						if inReader.Buffered() > 0 {
							finalByte, err := inReader.ReadByte()
							if err == nil && finalByte == 'M' {
								// Keypad Enter / Shift+Enter
								runes = append(runes, '\n')
								fmt.Print("\r\n" + continuationPrompt)
								continue
							}
						}
						continue
					}
				}
			}
			continue
		}

		// Режим вставки (Bracketed Paste)
		if inPaste {
			if r == '\r' {
				if inReader.Buffered() > 0 {
					if next, err := inReader.Peek(1); err == nil && len(next) > 0 && next[0] == '\n' {
						_, _ = inReader.ReadByte()
					}
				}
				runes = append(runes, '\n')
				fmt.Print("\r\n" + continuationPrompt)
				continue
			} else if r == '\n' {
				runes = append(runes, '\n')
				fmt.Print("\r\n" + continuationPrompt)
				continue
			} else if r == '\t' {
				runes = append(runes, '\t')
				fmt.Print("    ")
				continue
			}
			runes = append(runes, r)
			fmt.Print(string(r))
			continue
		}

		// Ручной ввод
		switch r {
		case '\r': // Обычный Enter -> отправка запроса
			if inReader.Buffered() > 0 {
				if next, err := inReader.Peek(1); err == nil && len(next) > 0 && next[0] == '\n' {
					_, _ = inReader.ReadByte()
				}
			}
			fmt.Print("\r\n")
			return string(runes), nil

		case '\n': // Ctrl+J -> перенос строки
			runes = append(runes, '\n')
			fmt.Print("\r\n" + continuationPrompt)

		case '\x03': // Ctrl+C -> очистка текущего ввода
			fmt.Print("^C\r\n")
			runes = nil
			if prompt != "" {
				fmt.Print(prompt)
			}

		case '\x04': // Ctrl+D -> завершение (EOF если пусто, отправка если есть текст)
			if len(runes) == 0 {
				fmt.Print("\r\n")
				return "", io.EOF
			}
			fmt.Print("\r\n")
			return string(runes), nil

		case '\b', '\x7f': // Backspace
			if len(runes) > 0 {
				last := runes[len(runes)-1]
				runes = runes[:len(runes)-1]
				if last == '\n' {
					// Стираем текущую строку '... ' и переходим на предыдущую
					fmt.Print("\r\x1b[K\x1b[A")
					prevLineLen := 0
					for i := len(runes) - 1; i >= 0; i-- {
						if runes[i] == '\n' {
							break
						}
						prevLineLen++
					}
					if !strings.ContainsRune(string(runes), '\n') {
						prevLineLen += len(prompt)
					} else {
						prevLineLen += len(continuationPrompt)
					}
					if prevLineLen > 0 {
						fmt.Printf("\r\x1b[%dC", prevLineLen)
					} else {
						fmt.Print("\r")
					}
				} else {
					fmt.Print("\b \b")
				}
			}

		case '\t':
			runes = append(runes, '\t')
			fmt.Print("    ")

		default:
			if r >= 32 {
				runes = append(runes, r)
				fmt.Print(string(r))
			}
		}
	}
}

func readUserInput() (string, error) {
	text, err := readMultilineInput("\033[1;32mUser >>> \033[0m")
	if err != nil {
		return "", err
	}
	textClean := strings.TrimSpace(text)
	if strings.HasPrefix(textClean, `"""`) && strings.HasSuffix(textClean, `"""`) && len(textClean) >= 6 {
		textClean = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(textClean, `"""`), `"""`))
	}
	return textClean, nil
}

func askConfirmation(prompt string) bool {
	if autoApprove {
		fmt.Println(prompt + "Y (Автоматически разрешено флагом -A)")
		return true
	}
	for {
		input, err := readLineWithPrompt(prompt)
		if err != nil {
			if err == io.EOF {
				fmt.Println("\nВыход.")
				os.Exit(0)
			}
			return false
		}
		input = strings.ToLower(strings.TrimSpace(input))
		if input == "y" || input == "yes" {
			return true
		}
		if input == "n" || input == "no" || input == "" {
			return false
		}
		fmt.Println("Пожалуйста, введите Y или N.")
	}
}

// === Определение инструментов (Tools) ===

// === Определение инструментов (Tools) ===

var baseTools = []ToolDefinition{
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "read_file",
			Description: "Прочитать содержимое файла на диске.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Абсолютный путь к файлу.",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "write_file",
			Description: "Создать новый файл или перезаписать существующий. Требует явного подтверждения пользователя.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Абсолютный путь к файлу.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Полное текстовое содержимое файла.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "dir_list",
			Description: "Получить список файлов и папок по указанному пути.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Путь к директории.",
					},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "web_search",
			Description: "Поиск в Интернете по ключевым словам. Возвращает список результатов с заголовками, ссылками и описанием.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Поисковый запрос.",
					},
				},
				"required": []string{"query"},
			},
		},
	},
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "fetch_webpage",
			Description: "Получить содержимое веб-страницы в текстовом виде (очищенный HTML).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL страницы для чтения.",
					},
				},
				"required": []string{"url"},
			},
		},
	},
	{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "download_file",
			Description: "Скачать файл из Интернета по указанному URL и сохранить на диск. Требует подтверждения.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Прямая ссылка на скачивание файла.",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Локальный путь для сохранения файла.",
					},
				},
				"required": []string{"url", "path"},
			},
		},
	},
}

var tools []ToolDefinition

func initTools() {
	tools = append([]ToolDefinition{}, baseTools...)
	tools = append(tools, getSysTools()...)
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n\n... [вывод обрезан: показаны первые %d байт из %d]", maxLen, len(s))
}

// === Реализация базовых системных вызовов ===

func readFile(path string) string {
	fmt.Printf("\n\033[36m[Чтение]\033[0m Открытие файла: %s\n", path)
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("ОШИБКА чтения: %v", err)
	}
	return truncateOutput(string(content), 48000)
}

func writeFile(path string, content string) string {
	if safetyErr := checkWritePathSafety(path); safetyErr != "" {
		return safetyErr
	}

	fmt.Printf("\n\033[33m[БЕЗОПАСНОСТЬ]\033[0m ИИ запрашивает запись в файл: %s\n", path)
	previewLen := 200
	if len(content) < previewLen {
		previewLen = len(content)
	}
	fmt.Printf("Фрагмент содержимого:\n%s\n", content[:previewLen])
	if len(content) > previewLen {
		fmt.Println("... [данные обрезаны для превью]")
	}

	if !askConfirmation("Разрешить запись на диск? (y/N): ") {
		return "ОШИБКА: Запись отклонена пользователем."
	}

	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return fmt.Sprintf("ОШИБКА записи: %v", err)
	}
	return "УСПЕШНО: Файл записан."
}

func dirList(path string) string {
	fmt.Printf("\n\033[36m[Директория]\033[0m Сканирование: %s\n", path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("ОШИБКА сканирования: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Список элементов в %s (всего: %d):\n", path, len(entries)))
	const maxEntries = 150
	for i, entry := range entries {
		if i >= maxEntries {
			sb.WriteString(fmt.Sprintf("... [и ещё %d элементов скрыто]\n", len(entries)-maxEntries))
			break
		}
		info, err := entry.Info()
		size := "-"
		typeStr := "Файл"
		if err == nil {
			if info.IsDir() {
				typeStr = "Папка"
			} else {
				size = fmt.Sprintf("%d байт", info.Size())
			}
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", typeStr, entry.Name(), size))
	}
	return sb.String()
}

// Helper to strip HTML tags from a string
func stripHTML(html string) string {
	// Remove tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	text := reTags.ReplaceAllString(html, "")
	// Unescape HTML entities (basic ones)
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	return strings.TrimSpace(text)
}

func webSearch(query string) string {
	fmt.Printf("\n[Поиск] Выполняется поиск в Интернете: %s...\n", query)
	
	// Используем DuckDuckGo HTML
	reqURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Sprintf("ОШИБКА подготовки запроса: %v", err)
	}

	// Устанавливаем заголовки, чтобы имитировать браузер
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.8,en-US;q=0.5,en;q=0.3")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("ОШИБКА выполнения запроса поиска: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("ОШИБКА: Сервер поиска вернул статус %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("ОШИБКА чтения ответа поиска: %v", err)
	}
	htmlContent := string(bodyBytes)

	// Если сервер выдал CAPTCHA
	if strings.Contains(htmlContent, "anomaly-modal") || strings.Contains(htmlContent, "Verification required") {
		return "ОШИБКА: Запрос заблокирован CAPTCHA. Пожалуйста, попробуйте позже или используйте другой поисковый запрос."
	}

	// Регулярные выражения для поиска ссылок и сниппетов
	reA := regexp.MustCompile(`(?i)<a\s+[^>]*class="[^"]*result__a[^"]*"[^>]*>([\s\S]*?)</a>`)
	reHref := regexp.MustCompile(`(?i)href="([^"]+)"`)
	reSnippet := regexp.MustCompile(`(?i)<(a|div|span)\s+[^>]*class="[^"]*result__snippet[^"]*"[^>]*>([\s\S]*?)</(a|div|span)>`)

	// Разделяем HTML по блокам результатов
	parts := strings.Split(htmlContent, `<div class="result`)
	if len(parts) <= 1 {
		return "Результаты поиска не найдены."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Результаты поиска по запросу \"%s\":\n\n", query))
	
	count := 0
	for _, part := range parts[1:] {
		// Извлекаем ссылку и заголовок
		matchA := reA.FindStringSubmatch(part)
		if len(matchA) < 2 {
			continue
		}
		rawTitle := matchA[1]
		title := stripHTML(rawTitle)

		matchHref := reHref.FindStringSubmatch(matchA[0])
		if len(matchHref) < 2 {
			continue
		}
		rawURL := matchHref[1]

		// Декодируем uddg URL
		targetURL := rawURL
		if strings.Contains(rawURL, "uddg=") {
			u, err := url.Parse(rawURL)
			if err == nil {
				uddg := u.Query().Get("uddg")
				if uddg != "" {
					targetURL = uddg
				}
			}
		} else {
			if strings.HasPrefix(rawURL, "//") {
				targetURL = "https:" + rawURL
			} else if strings.HasPrefix(rawURL, "/") {
				targetURL = "https://duckduckgo.com" + rawURL
			}
		}

		// Извлекаем сниппет (описание)
		snippet := ""
		matchSnippet := reSnippet.FindStringSubmatch(part)
		if len(matchSnippet) >= 3 {
			snippet = stripHTML(matchSnippet[2])
		}

		count++
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   URL: %s\n", count, title, targetURL))
		if snippet != "" {
			sb.WriteString(fmt.Sprintf("   Описание: %s\n", snippet))
		}
		sb.WriteString("\n")

		if count >= 10 { // Ограничиваем первыми 10 результатами
			break
		}
	}

	if count == 0 {
		return "Не удалось распарсить результаты поиска (возможно, изменилась структура страницы)."
	}

	return sb.String()
}

func fetchWebpage(urlStr string) string {
	fmt.Printf("\n[Серфинг] Загрузка страницы: %s...\n", urlStr)

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return fmt.Sprintf("ОШИБКА подготовки запроса: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("ОШИБКА загрузки страницы: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("ОШИБКА: Сервер вернул статус %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("ОШИБКА чтения содержимого страницы: %v", err)
	}
	html := string(bodyBytes)

	// Очищаем HTML
	// 1. Удаляем script и style
	reScript := regexp.MustCompile(`(?i)<script[^>]*>[\s\S]*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?i)<style[^>]*>[\s\S]*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// 2. Блочные теги заменяем на перенос строки
	reBlock := regexp.MustCompile(`(?i)</?(p|div|tr|h[1-6]|br|li|ul|ol|table|section|header|footer|nav|hgroup)[^>]*>`)
	html = reBlock.ReplaceAllString(html, "\n")

	// 3. Удаляем все остальные теги
	reTags := regexp.MustCompile(`<[^>]+>`)
	text := reTags.ReplaceAllString(html, "")

	// 4. Нормализуем пробелы и строки
	lines := strings.Split(text, "\n")
	var cleanLines []string
	reSpaces := regexp.MustCompile(`[ \t]+`)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = reSpaces.ReplaceAllString(trimmed, " ")
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	cleanText := strings.Join(cleanLines, "\n")

	// Декодируем базовые сущности
	cleanText = strings.ReplaceAll(cleanText, "&quot;", "\"")
	cleanText = strings.ReplaceAll(cleanText, "&amp;", "&")
	cleanText = strings.ReplaceAll(cleanText, "&lt;", "<")
	cleanText = strings.ReplaceAll(cleanText, "&gt;", ">")
	cleanText = strings.ReplaceAll(cleanText, "&#39;", "'")
	cleanText = strings.ReplaceAll(cleanText, "&nbsp;", " ")

	// Ограничиваем размер
	const maxLen = 15000
	if len(cleanText) > maxLen {
		return cleanText[:maxLen] + "\n\n... [вывод обрезан из-за превышения лимита]"
	}

	return cleanText
}

func downloadFile(urlStr, path string) string {
	// Безопасность: проверяем путь записи, так же как в writeFile
	normalizedPath := strings.ReplaceAll(path, "/", "\\")
	if strings.HasPrefix(strings.ToLower(normalizedPath), "c:\\") {
		afterDrive := normalizedPath[3:]
		if !strings.Contains(afterDrive, "\\") {
			return "ОШИБКА: Запись файлов напрямую в корень диска C:\\ запрещена. Пожалуйста, используйте папку %TEMP% или текущую рабочую директорию (например, %TEMP%\\имя_файла)."
		}
	}

	fmt.Printf("\n[БЕЗОПАСНОСТЬ] ИИ запрашивает скачивание файла из Интернета:\nURL: %s\nКуда: %s\n", urlStr, path)
	if !askConfirmation("Разрешить скачивание файла? (y/N): ") {
		return "ОШИБКА: Скачивание отклонено пользователем."
	}

	fmt.Println("[Скачивание] Выполняется загрузка...")
	
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return fmt.Sprintf("ОШИБКА подготовки запроса скачивания: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 10 * time.Minute} // Щедрый таймаут для больших файлов/драйверов
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("ОШИБКА соединения при скачивании: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("ОШИБКА: Сервер вернул код состояния %d", resp.StatusCode)
	}

	// Создаем директории, если их нет
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Sprintf("ОШИБКА создания директории %s: %v", dir, err)
		}
	}

	out, err := os.Create(path)
	if err != nil {
		return fmt.Sprintf("ОШИБКА создания файла на диске: %v", err)
	}
	defer out.Close()

	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Sprintf("ОШИБКА записи файла на диск при скачивании: %v", err)
	}

	return fmt.Sprintf("УСПЕШНО: Файл скачан. Записано %d байт в %s.", n, path)
}

// === API вызовы и потоковый вывод (Streaming) ===

type StreamChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

func callAPINonStream(cfg Config, messages []Message, allowTools bool) (*ChatCompletionResponse, error) {
	reqBody := ChatCompletionRequest{
		Model:    cfg.Model,
		Messages: messages,
	}
	if allowTools {
		reqBody.Tools = tools
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.Key != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Key)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("статус %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, err
	}
	return &chatResp, nil
}

func callAPIStream(cfg Config, messages []Message, allowTools bool) (*ChatCompletionResponse, error) {
	reqMap := map[string]interface{}{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	}
	if allowTools {
		reqMap["tools"] = tools
	}

	jsonData, err := json.Marshal(reqMap)
	if err != nil {
		return callAPINonStream(cfg, messages, allowTools)
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return callAPINonStream(cfg, messages, allowTools)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if cfg.Key != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Key)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return callAPINonStream(cfg, messages, allowTools)
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var fullContent strings.Builder
	var toolCallsMap = make(map[int]*ToolCall)
	var finalUsage Usage
	var printedHeader bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			finalUsage = *chunk.Usage
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			if !printedHeader {
				fmt.Print("\n\033[1;35mИИ >>> \033[0m")
				printedHeader = true
			}
			fmt.Print(delta.Content)
			fullContent.WriteString(delta.Content)
		}

		for _, tcChunk := range delta.ToolCalls {
			idx := tcChunk.Index
			tc, exists := toolCallsMap[idx]
			if !exists {
				tc = &ToolCall{}
				toolCallsMap[idx] = tc
			}
			if tcChunk.ID != "" {
				tc.ID += tcChunk.ID
			}
			if tcChunk.Type != "" {
				tc.Type = tcChunk.Type
			}
			if tcChunk.Function.Name != "" {
				tc.Function.Name += tcChunk.Function.Name
			}
			if tcChunk.Function.Arguments != "" {
				tc.Function.Arguments += tcChunk.Function.Arguments
			}
		}
	}

	if printedHeader {
		fmt.Println()
	}

	var orderedToolCalls []ToolCall
	for i := 0; i < len(toolCallsMap); i++ {
		if tc, ok := toolCallsMap[i]; ok {
			if tc.Type == "" {
				tc.Type = "function"
			}
			orderedToolCalls = append(orderedToolCalls, *tc)
		}
	}

	resMsg := Message{
		Role: "assistant",
	}
	if fullContent.Len() > 0 {
		cStr := fullContent.String()
		resMsg.Content = &cStr
	}
	if len(orderedToolCalls) > 0 {
		resMsg.ToolCalls = orderedToolCalls
	}

	if fullContent.Len() == 0 && len(orderedToolCalls) == 0 {
		return callAPINonStream(cfg, messages, allowTools)
	}

	return &ChatCompletionResponse{
		Choices: []struct {
			Message Message `json:"message"`
		}{
			{Message: resMsg},
		},
		Usage: finalUsage,
	}, nil
}

var (
	sessionJSONFile = "session.json"
	sessionMDFile   = "session.md"
)

func initSessionPaths() {
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		sessionJSONFile = filepath.Join(exeDir, "session.json")
		sessionMDFile = filepath.Join(exeDir, "session.md")
	}
}

func saveSession(messages []Message) {
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		fmt.Printf("[ОШИБКА] Не удалось закодировать сессию: %v\n", err)
		return
	}
	err = os.WriteFile(sessionJSONFile, data, 0644)
	if err != nil {
		fmt.Printf("[ОШИБКА] Не удалось сохранить файл сессии: %v\n", err)
	}
}

func loadSession() ([]Message, error) {
	if _, err := os.Stat(sessionJSONFile); os.IsNotExist(err) {
		return nil, err
	}
	data, err := os.ReadFile(sessionJSONFile)
	if err != nil {
		return nil, err
	}
	var messages []Message
	err = json.Unmarshal(data, &messages)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func saveMarkdownLog(messages []Message, model string, url string) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Сессия работы %s\n\n", getPlatformName()))
	sb.WriteString(fmt.Sprintf("- **Модель:** `%s`\n", model))
	sb.WriteString(fmt.Sprintf("- **API:** `%s`\n", url))
	sb.WriteString(fmt.Sprintf("- **Последнее обновление:** %s\n\n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString("---\n\n")

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			sb.WriteString("### ⚙️ Системные настройки (System)\n")
			if msg.Content != nil {
				sb.WriteString(fmt.Sprintf("> %s\n\n", *msg.Content))
			} else {
				sb.WriteString("> [Пустой промпт]\n\n")
			}
		case "user":
			sb.WriteString("### 👤 Пользователь (User)\n")
			if msg.Content != nil {
				sb.WriteString(fmt.Sprintf("%s\n\n", *msg.Content))
			} else {
				sb.WriteString("\n\n")
			}
		case "assistant":
			sb.WriteString("### 🤖 ИИ-Агент (Assistant)\n")
			if msg.Content != nil && *msg.Content != "" {
				sb.WriteString(fmt.Sprintf("%s\n\n", *msg.Content))
			}
			if len(msg.ToolCalls) > 0 {
				sb.WriteString("**Запрошенные действия:**\n")
				for _, tc := range msg.ToolCalls {
					sb.WriteString(fmt.Sprintf("- **Вызов функции:** `%s` (ID: `%s`)\n", tc.Function.Name, tc.ID))
					sb.WriteString("  Аргументы:\n")
					sb.WriteString("  ```json\n")
					var prettyJSON bytes.Buffer
					if err := json.Indent(&prettyJSON, []byte(tc.Function.Arguments), "  ", "  "); err == nil {
						sb.WriteString("  " + prettyJSON.String() + "\n")
					} else {
						sb.WriteString("  " + tc.Function.Arguments + "\n")
					}
					sb.WriteString("  ```\n")
				}
				sb.WriteString("\n")
			}
		case "tool":
			sb.WriteString(fmt.Sprintf("### 🛠️ Результат инструмента `%s` (Tool)\n", msg.Name))
			sb.WriteString(fmt.Sprintf("- **Tool Call ID:** `%s`\n", msg.ToolCallID))
			if msg.Content != nil {
				sb.WriteString("```\n")
				sb.WriteString(*msg.Content)
				if !strings.HasSuffix(*msg.Content, "\n") {
					sb.WriteString("\n")
				}
				sb.WriteString("```\n\n")
			} else {
				sb.WriteString("`Нет вывода`\n\n")
			}
		default:
			sb.WriteString(fmt.Sprintf("### 📝 [%s]\n", strings.ToUpper(msg.Role)))
			if msg.Content != nil {
				sb.WriteString(fmt.Sprintf("%s\n\n", *msg.Content))
			}
		}
	}

	err := os.WriteFile(sessionMDFile, []byte(sb.String()), 0644)
	if err != nil {
		fmt.Printf("[ОШИБКА] Не удалось сохранить markdown лог: %v\n", err)
	}
}

var modelContextMap = make(map[string]int)

type ModelsResponse struct {
	Data []struct {
		ID                  string      `json:"id"`
		Name                string      `json:"name"`
		OwnedBy             string      `json:"owned_by"`
		ContextLength       interface{} `json:"context_length"`
		MaxContextLength    interface{} `json:"max_context_length"`
		LoadedContextLength interface{} `json:"loaded_context_length"`
		MaxModelLen         interface{} `json:"max_model_len"`
		ContextWindow       interface{} `json:"context_window"`
	} `json:"data"`
	Models []struct {
		Name          string      `json:"name"`
		Model         string      `json:"model"`
		ContextLength interface{} `json:"context_length"`
	} `json:"models"`
}

func parseIntVal(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return 0
}

func detectContextLimit(modelName string, detectedCtx int) int {
	if detectedCtx > 0 {
		return detectedCtx
	}
	m := strings.ToLower(modelName)
	switch {
	case strings.Contains(m, "196608") || strings.Contains(m, "196k"):
		return 196608
	case strings.Contains(m, "gemini-1.5") || strings.Contains(m, "gemini-2") || strings.Contains(m, "1m"):
		return 1048576
	case strings.Contains(m, "claude-3") || strings.Contains(m, "200k"):
		return 200000
	case strings.Contains(m, "gpt-4o") || strings.Contains(m, "gpt-4-turbo") || strings.Contains(m, "128k"):
		return 128000
	case strings.Contains(m, "qwen2.5") || strings.Contains(m, "131072") || strings.Contains(m, "131k"):
		return 131072
	case strings.Contains(m, "deepseek") || strings.Contains(m, "64k"):
		return 65536
	case strings.Contains(m, "32k"):
		return 32768
	case strings.Contains(m, "llama-3.1") || strings.Contains(m, "llama-3.2") || strings.Contains(m, "llama-3.3"):
		return 131072
	case strings.Contains(m, "llama-3") || strings.Contains(m, "llama3") || strings.Contains(m, "8k"):
		return 8192
	case strings.Contains(m, "mistral") || strings.Contains(m, "codestral"):
		return 32768
	default:
		return 131072
	}
}

func resolveContextLimit(cfg *Config) int {
	if cfg.ContextLimit > 0 {
		return cfg.ContextLimit
	}
	if detected, ok := modelContextMap[cfg.Model]; ok && detected > 0 {
		cfg.ContextLimit = detected
		return detected
	}
	cfg.ContextLimit = detectContextLimit(cfg.Model, 0)
	return cfg.ContextLimit
}

func getModelsEndpoint(apiURL string) string {
	u := strings.TrimSpace(apiURL)
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/chat/completions")
	u = strings.TrimRight(u, "/")
	if strings.HasSuffix(u, "/models") {
		return u
	}
	if strings.HasSuffix(u, "/v1") {
		return u + "/models"
	}
	return u + "/v1/models"
}

func fetchModels(apiURL string, apiKey string) ([]string, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("URL API не задан")
	}
	endpoint := getModelsEndpoint(apiURL)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("User-Agent", "sa42agent")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil || (resp != nil && resp.StatusCode == http.StatusNotFound && strings.HasSuffix(endpoint, "/v1/models")) {
		if resp != nil {
			resp.Body.Close()
		}
		fallbackEndpoint := strings.TrimSuffix(strings.TrimRight(apiURL, "/"), "/chat/completions") + "/models"
		if fallbackEndpoint != endpoint {
			if req2, err2 := http.NewRequest("GET", fallbackEndpoint, nil); err2 == nil {
				if apiKey != "" {
					req2.Header.Set("Authorization", "Bearer "+apiKey)
				}
				req2.Header.Set("User-Agent", "sa42agent")
				if resp2, err2 := client.Do(req2); err2 == nil {
					resp = resp2
					endpoint = fallbackEndpoint
				}
			}
		}
	}

	if resp == nil {
		return nil, fmt.Errorf("не удалось подключиться к серверу %s: %v", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, endpoint, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var mResp ModelsResponse
	if err := json.Unmarshal(bodyBytes, &mResp); err == nil {
		var list []string
		for _, d := range mResp.Data {
			mID := d.ID
			if mID == "" {
				mID = d.Name
			}
			if mID != "" {
				list = append(list, mID)
				ctxLen := parseIntVal(d.LoadedContextLength)
				if ctxLen <= 0 {
					ctxLen = parseIntVal(d.ContextLength)
				}
				if ctxLen <= 0 {
					ctxLen = parseIntVal(d.MaxContextLength)
				}
				if ctxLen <= 0 {
					ctxLen = parseIntVal(d.MaxModelLen)
				}
				if ctxLen <= 0 {
					ctxLen = parseIntVal(d.ContextWindow)
				}
				if ctxLen > 0 {
					modelContextMap[mID] = ctxLen
				}
			}
		}
		if len(list) == 0 {
			for _, m := range mResp.Models {
				mID := m.Name
				if mID == "" {
					mID = m.Model
				}
				if mID != "" {
					list = append(list, mID)
					ctxLen := parseIntVal(m.ContextLength)
					if ctxLen > 0 {
						modelContextMap[mID] = ctxLen
					}
				}
			}
		}
		if len(list) > 0 {
			return list, nil
		}
	}

	var strList []string
	if err := json.Unmarshal(bodyBytes, &strList); err == nil && len(strList) > 0 {
		return strList, nil
	}

	return nil, fmt.Errorf("не удалось разобрать список моделей из ответа сервера %s", endpoint)
}

func loadConfigFromFile(path string) (Config, error) {
	var cfg Config
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()
	err = json.NewDecoder(file).Decode(&cfg)
	return cfg, err
}

func saveConfigToFile(path string, cfg Config) error {
	saveCfg := cfg
	saveCfg.URL = strings.TrimSuffix(saveCfg.URL, "/chat/completions")
	saveCfg.URL = strings.TrimRight(saveCfg.URL, "/")
	data, err := json.MarshalIndent(saveCfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func pruneContext(messages []Message, contextLimit int, currentTokens int, force bool) ([]Message, bool) {
	if len(messages) <= 5 {
		return messages, false
	}

	// Автоматическое сжатие запускается ТОЛЬКО если контекст заполнен более чем на 75%
	if !force {
		if contextLimit <= 0 {
			contextLimit = 131072
		}
		threshold := contextLimit * 75 / 100
		if currentTokens < threshold {
			return messages, false
		}
	}

	prunedCount := 0
	endIdx := len(messages) - 3
	if endIdx < 1 {
		endIdx = 1
	}

	for i := 1; i < endIdx; i++ {
		if messages[i].Role == "tool" && messages[i].Content != nil {
			if len(*messages[i].Content) > 200 {
				newContent := (*messages[i].Content)[:120] + "\n... [вывод инструмента сжат для экономии контекста]"
				messages[i].Content = &newContent
				prunedCount++
			}
		}
	}

	if prunedCount > 0 {
		fmt.Printf("\n\033[33m[Контекст]\033[0m Сжато %d старых результатов вызова инструментов (заполнено >75%%).\n", prunedCount)
		return messages, true
	}
	return messages, false
}

func handleSlashCommand(input string, cfg *Config, cfgPath string, messages *[]Message, lastUsage *Usage) bool {
	cmd := strings.TrimSpace(input)
	if !strings.HasPrefix(cmd, "/") {
		return false
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	switch parts[0] {
	case "/help":
		fmt.Println("\n\033[1;36m================ Доступные команды ================\033[0m")
		fmt.Println("  \033[1m? <текст>\033[0m       - Режим консультации: только ответ без выполнения действий")
		fmt.Println("  \033[1m/help\033[0m           - Показать эту справку")
		fmt.Println("  \033[1m/clear, /reset\033[0m  - Очистить историю диалога и начать заново")
		fmt.Println("  \033[1m/model\033[0m          - Показать список моделей или сменить (/model <имя|номер>)")
		fmt.Println("  \033[1m/compact\033[0m        - Вручную сжать старые вызовы инструментов в контексте")
		fmt.Println("  \033[1m/tokens, /stats\033[0m - Показать статистику использования контекста и токенов")
		fmt.Println("  \033[1m/exit, /quit\033[0m    - Выход из программы")
		fmt.Println("\033[1;36m====================================================\033[0m\n")
		return true

	case "/clear", "/reset":
		systemPrompt := getDefaultSystemPrompt()
		*messages = []Message{
			{Role: "system", Content: &systemPrompt},
		}
		_ = os.Remove(sessionJSONFile)
		saveSession(*messages)
		saveMarkdownLog(*messages, cfg.Model, cfg.URL)
		fmt.Println("\n\033[1;32m[Сессия]\033[0m История сообщений очищена. Новая сессия начата.")
		return true

	case "/model":
		if len(parts) > 1 {
			newModel := parts[1]
			var num int
			if _, err := fmt.Sscanf(newModel, "%d", &num); err == nil && num > 0 {
				models, err := fetchModels(cfg.URL, cfg.Key)
				if err == nil && num <= len(models) {
					newModel = models[num-1]
				}
			}
			cfg.Model = newModel
			_ = saveConfigToFile(cfgPath, *cfg)
			fmt.Printf("\n\033[1;32m[Модель]\033[0m Модель успешно переключена на: \033[1m%s\033[0m\n", cfg.Model)
		} else {
			fmt.Printf("\nТекущая модель: \033[1m%s\033[0m\n", cfg.Model)
			fmt.Println("Получение доступных моделей с сервера...")
			models, err := fetchModels(cfg.URL, cfg.Key)
			if err != nil {
				fmt.Printf("\033[31m[ОШИБКА]\033[0m Не удалось получить список: %v\n", err)
			} else {
				fmt.Println("\nДоступные модели:")
				for i, m := range models {
					marker := " "
					if m == cfg.Model {
						marker = "*"
					}
					fmt.Printf(" %s %2d) %s\n", marker, i+1, m)
				}
				fmt.Println("\nИспользуйте: /model <номер или имя_модели> для переключения.")
			}
		}
		return true

	case "/compact":
		limit := resolveContextLimit(cfg)
		*messages, _ = pruneContext(*messages, limit, lastUsage.TotalTokens, true)
		saveSession(*messages)
		saveMarkdownLog(*messages, cfg.Model, cfg.URL)
		return true

	case "/tokens", "/stats":
		limit := cfg.ContextLimit
		if limit <= 0 {
			limit = 262144
		}
		fmt.Printf("\n\033[1;36m[Статистика]\033[0m Сообщений в сессии: %d\n", len(*messages))
		if lastUsage != nil && lastUsage.TotalTokens > 0 {
			percent := float64(lastUsage.TotalTokens) / float64(limit) * 100
			fmt.Printf("Токены промпта: %d | Генерации: %d | Всего: %d/%d (%.1f%%)\n",
				lastUsage.PromptTokens, lastUsage.CompletionTokens, lastUsage.TotalTokens, limit, percent)
		} else {
			fmt.Printf("Лимит контекста: %d токенов.\n", limit)
		}
		return true

	case "/exit", "/quit":
		fmt.Println("Выход.")
		os.Exit(0)
		return true

	default:
		fmt.Printf("\033[33m[Команда]\033[0m Неизвестная команда '%s'. Введите \033[1m/help\033[0m для справки.\n", parts[0])
		return true
	}
}

func main() {
	initTools()

	var listModelsRequested bool
	var sanitizedArgs []string

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "-models" || arg == "--models" {
			listModelsRequested = true
			continue
		}
		if arg == "-model" || arg == "--model" {
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				sanitizedArgs = append(sanitizedArgs, arg, os.Args[i+1])
				i++
			} else {
				listModelsRequested = true
			}
			continue
		}
		if strings.HasPrefix(arg, "-model=") || strings.HasPrefix(arg, "--model=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
				sanitizedArgs = append(sanitizedArgs, arg)
			} else {
				listModelsRequested = true
			}
			continue
		}
		sanitizedArgs = append(sanitizedArgs, arg)
	}

	urlFlag := flag.String("url", "", "API URL (например, http://localhost:8080/v1)")
	keyFlag := flag.String("key", "", "API Key")
	modelFlag := flag.String("model", "", "Model name")
	configFlag := flag.String("config", "config.json", "Config file name")
	limitFlag := flag.Int("context-limit", 0, "Max context window limit (default: 262144)")
	autoApproveFlag := flag.Bool("A", false, "Автоматически одобрять все действия без подтверждения")
	cleanFlag := flag.Bool("clean", false, "Начать новую чистую сессию, игнорируя сохраненную")
	_ = flag.CommandLine.Parse(sanitizedArgs)

	initSessionPaths()

	autoApprove = *autoApproveFlag

	var cfg Config

	// Проверяем config.json
	cfgPath := *configFlag
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if exePath, err := os.Executable(); err == nil {
			cfgPath = filepath.Join(filepath.Dir(exePath), *configFlag)
		}
	}

	if _, err := os.Stat(cfgPath); err == nil {
		if fileCfg, err := loadConfigFromFile(cfgPath); err == nil {
			cfg = fileCfg
		}
	}

	// Перекрываем переменными окружения
	if envURL := os.Getenv("AI_API_URL"); envURL != "" && cfg.URL == "" {
		cfg.URL = envURL
	}
	if envKey := os.Getenv("AI_API_KEY"); envKey != "" && cfg.Key == "" {
		cfg.Key = envKey
	}
	if envModel := os.Getenv("AI_MODEL"); envModel != "" && cfg.Model == "" {
		cfg.Model = envModel
	}

	// Перекрываем флагами
	if *urlFlag != "" {
		cfg.URL = *urlFlag
	}
	if *keyFlag != "" {
		cfg.Key = *keyFlag
	}
	if *modelFlag != "" {
		cfg.Model = *modelFlag
	}
	if *limitFlag > 0 {
		cfg.ContextLimit = *limitFlag
	}

	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Key = strings.TrimSpace(cfg.Key)
	cfg.Model = strings.TrimSpace(cfg.Model)

	if listModelsRequested {
		if cfg.URL == "" {
			cfg.URL = readLineWithPromptExitOnError("Введите адрес API: ")
			cfg.URL = strings.TrimSpace(cfg.URL)
		}
		fmt.Printf("\nПолучение списка моделей с %s...\n", cfg.URL)
		models, err := fetchModels(cfg.URL, cfg.Key)
		if err != nil {
			fmt.Printf("\033[31m[ОШИБКА]\033[0m Не удалось получить список моделей: %v\n\n", err)
			os.Exit(1)
		}
		fmt.Println("\n=======================================================")
		fmt.Println("   Доступные модели на сервере")
		fmt.Printf("   URL: %s\n", cfg.URL)
		fmt.Println("=======================================================")
		for i, m := range models {
			fmt.Printf("  %2d) %s\n", i+1, m)
		}
		fmt.Println("=======================================================")
		fmt.Println("\nИспользование:")
		fmt.Println("  ./agent -model <имя_модели или номер>")
		fmt.Println("=======================================================\n")
		os.Exit(0)
	}

	if cfg.URL == "" {
		cfg.URL = readLineWithPromptExitOnError("Введите адрес API: ")
		cfg.URL = strings.TrimSpace(cfg.URL)
	}

	// Если передана цифра/номер модели, преобразуем её в имя модели из списка
	var modelNum int
	if _, err := fmt.Sscanf(cfg.Model, "%d", &modelNum); err == nil && modelNum > 0 && strconv.Itoa(modelNum) == cfg.Model {
		fmt.Printf("Получение списка моделей для разрешения номера #%d...\n", modelNum)
		models, err := fetchModels(cfg.URL, cfg.Key)
		if err != nil {
			fmt.Printf("\033[31m[ОШИБКА]\033[0m Не удалось получить список моделей для выбора #%d: %v\n", modelNum, err)
			os.Exit(1)
		}
		if modelNum > len(models) {
			fmt.Printf("\033[31m[ОШИБКА]\033[0m Номер модели #%d вне диапазона (доступно: 1-%d)\n", modelNum, len(models))
			os.Exit(1)
		}
		cfg.Model = models[modelNum-1]
		fmt.Printf("[Модель] Выбрана модель #%d -> %s\n", modelNum, cfg.Model)
	}

	if cfg.Model == "" {
		models, err := fetchModels(cfg.URL, cfg.Key)
		if err == nil && len(models) > 0 {
			fmt.Println("\n=======================================================")
			fmt.Println("   Доступные модели на сервере:")
			fmt.Println("=======================================================")
			for i, m := range models {
				fmt.Printf("  %2d) %s\n", i+1, m)
			}
			fmt.Println("=======================================================")
			input := readLineWithPromptExitOnError(fmt.Sprintf("Введите имя модели или номер (1-%d): ", len(models)))
			input = strings.TrimSpace(input)
			var chosenIdx int
			if n, err := fmt.Sscanf(input, "%d", &chosenIdx); err == nil && n == 1 && chosenIdx >= 1 && chosenIdx <= len(models) {
				cfg.Model = models[chosenIdx-1]
			} else {
				cfg.Model = input
			}
		} else {
			cfg.Model = readLineWithPromptExitOnError("Введите имя модели: ")
		}
		cfg.Model = strings.TrimSpace(cfg.Model)
	}

	// Сохраняем выбранную модель в файл конфигурации
	if cfg.Model != "" {
		_ = saveConfigToFile(cfgPath, cfg)
	}

	if !strings.HasSuffix(cfg.URL, "/chat/completions") {
		if strings.HasSuffix(cfg.URL, "/") {
			cfg.URL += "chat/completions"
		} else {
			cfg.URL += "/chat/completions"
		}
	}

	if modelContextMap[cfg.Model] == 0 && cfg.URL != "" {
		_, _ = fetchModels(cfg.URL, cfg.Key)
	}
	activeContextLimit := resolveContextLimit(&cfg)

	fmt.Println("\n=======================================================")
	fmt.Printf("   \033[1;36m%s\033[0m (Go Standalone)\n", getPlatformName())
	fmt.Printf("   API: %s | Model: %s | Контекст: %d токенов\n", cfg.URL, cfg.Model, activeContextLimit)
	fmt.Println("   (Введите \033[1m/help\033[0m для списка доступных команд)")
	fmt.Println("=======================================================")

	var messages []Message
	if !*cleanFlag {
		loadedMessages, err := loadSession()
		if err == nil && len(loadedMessages) > 0 {
			fmt.Printf("\n\033[36m[Сессия]\033[0m Обнаружена сохраненная сессия (%d сообщений).\n", len(loadedMessages))
			if askConfirmation("Хотите восстановить и продолжить эту сессию? (Y/n): ") {
				messages = loadedMessages
				fmt.Println("\n--- Восстановление истории сообщений ---")
				for _, m := range messages {
					if m.Role == "user" && m.Content != nil {
						fmt.Printf("\033[1;32mUser >>>\033[0m %s\n", *m.Content)
					} else if m.Role == "assistant" && m.Content != nil && *m.Content != "" {
						fmt.Printf("\033[1;35mИИ >>>\033[0m %s\n", *m.Content)
					}
				}
				fmt.Println("--- Сессия успешно восстановлена ---\n")
			}
		}
	}

	if len(messages) == 0 {
		systemPrompt := getDefaultSystemPrompt()
		messages = []Message{
			{Role: "system", Content: &systemPrompt},
		}
		saveSession(messages)
		saveMarkdownLog(messages, cfg.Model, cfg.URL)
	}

	var lastUsage Usage

	for {
		fmt.Println()
		userInput, err := readUserInput()
		if err != nil {
			if err == io.EOF {
				fmt.Println("Выход.")
				break
			}
			fmt.Printf("\n\033[31m[ОШИБКА]\033[0m Ошибка ввода: %v\n", err)
			continue
		}
		if userInput == "" {
			continue
		}

		// Обработка слэш-команд (/help, /clear, /model, /compact, /tokens, /exit)
		if handleSlashCommand(userInput, &cfg, cfgPath, &messages, &lastUsage) {
			continue
		}

		// Режим консультации: если запрос начинается с '?', отключаем инструменты на уровне софта
		allowTools := !strings.HasPrefix(strings.TrimSpace(userInput), "?")
		if !allowTools {
			fmt.Println("\033[36m[Режим консультации]\033[0m Инструменты отключены (только текстовый ответ).")
		}

		messages = append(messages, Message{Role: "user", Content: &userInput})
		saveSession(messages)
		saveMarkdownLog(messages, cfg.Model, cfg.URL)

		for {
			limit := resolveContextLimit(&cfg)

			// Автоматическая оптимизация контекста перед отправкой (только если >75%)
			messages, _ = pruneContext(messages, limit, lastUsage.TotalTokens, false)

			fmt.Println("\033[2m[ИИ] Думает...\033[0m")
			response, err := callAPIStream(cfg, messages, allowTools)
			if err != nil {
				fmt.Printf("\033[31m[ОШИБКА]\033[0m Не удалось связаться с API: %v\n", err)
				break
			}

			if len(response.Choices) == 0 {
				fmt.Println("\033[31m[ИИ]\033[0m Не вернул вариантов ответа.")
				break
			}

			assistantMessage := response.Choices[0].Message
			messages = append(messages, assistantMessage)
			saveSession(messages)
			saveMarkdownLog(messages, cfg.Model, cfg.URL)

			if response.Usage.TotalTokens > 0 {
				lastUsage = response.Usage
			}

			if lastUsage.TotalTokens > 0 {
				percent := float64(lastUsage.TotalTokens) / float64(limit) * 100
				fmt.Printf("\033[2m[Контекст: %d/%d (%.1f%%)]\033[0m\n", lastUsage.TotalTokens, limit, percent)
			}

			// Если модель не запрашивает вызовы инструментов или инструменты отключены, выходим к вводу пользователя
			if !allowTools || len(assistantMessage.ToolCalls) == 0 {
				break
			}

			// Обработка запрошенных инструментов
			for _, toolCall := range assistantMessage.ToolCalls {
				var toolResult string

				switch toolCall.Function.Name {
				case "execute_cmd":
					var args struct {
						Command string `json:"command"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = executeCmd(args.Command)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "read_file":
					var args struct {
						Path string `json:"path"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = readFile(args.Path)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "write_file":
					var args struct {
						Path    string `json:"path"`
						Content string `json:"content"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = writeFile(args.Path, args.Content)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "dir_list":
					var args struct {
						Path string `json:"path"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = dirList(args.Path)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "get_sys_env", "get_windows_env":
					toolResult = getSysEnv()

				case "web_search":
					var args struct {
						Query string `json:"query"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = webSearch(args.Query)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "fetch_webpage":
					var args struct {
						URL string `json:"url"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = fetchWebpage(args.URL)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				case "download_file":
					var args struct {
						URL  string `json:"url"`
						Path string `json:"path"`
					}
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err == nil {
						toolResult = downloadFile(args.URL, args.Path)
					} else {
						toolResult = "ОШИБКА: Неверные параметры функции."
					}

				default:
					toolResult = fmt.Sprintf("ОШИБКА: Инструмент %s не поддерживается.", toolCall.Function.Name)
				}

				messages = append(messages, Message{
					Role:       "tool",
					Content:    &toolResult,
					ToolCallID: toolCall.ID,
					Name:       toolCall.Function.Name,
				})
				saveSession(messages)
				saveMarkdownLog(messages, cfg.Model, cfg.URL)
			}
		}
	}
}
