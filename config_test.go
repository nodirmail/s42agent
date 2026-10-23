package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseContextLimit(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"32k", 32768, false},
		{"32K", 32768, false},
		{"32kb", 32768, false},
		{"32kib", 32768, false},
		{"64k", 65536, false},
		{"128k", 131072, false},
		{"256k", 262144, false},
		{"1m", 1048576, false},
		{"1M", 1048576, false},
		{"1mb", 1048576, false},
		{"1.5m", 1572864, false},
		{"131072", 131072, false},
		{"32000", 32000, false},
		{"  64k tokens  ", 65536, false},
		{"128k токенов", 131072, false},
		{"", 0, true},
		{"0", 0, true},
		{"-100", 0, true},
		{"abc", 0, true},
		{"k", 0, true},
	}

	for _, tt := range tests {
		got, err := parseContextLimit(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseContextLimit(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseContextLimit(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFormatContextLimit(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{32768, "32768 (32k)"},
		{65536, "65536 (64k)"},
		{131072, "131072 (128k)"},
		{1048576, "1048576 (1M)"},
		{32000, "32000 (~32k)"},
		{12345, "12345"},
		{0, "0"},
	}

	for _, tt := range tests {
		got := formatContextLimit(tt.input)
		if got != tt.want {
			t.Errorf("formatContextLimit(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	// Default: in language used by user
	defaultPrompt := buildSystemPrompt("")
	if !strings.Contains(defaultPrompt, "in the language used by the user") {
		t.Errorf("default prompt should instruct to use user language, got: %s", defaultPrompt)
	}

	autoPrompt := buildSystemPrompt("auto")
	if !strings.Contains(autoPrompt, "in the language used by the user") {
		t.Errorf("auto prompt should instruct to use user language, got: %s", autoPrompt)
	}

	// Priority Russian: ru
	ruPrompt := buildSystemPrompt("ru")
	if !strings.Contains(ruPrompt, "Russian (language code: ru)") {
		t.Errorf("ru prompt should prioritize Russian, got: %s", ruPrompt)
	}
	if strings.Contains(ruPrompt, "in the language used by the user") {
		t.Errorf("ru prompt should override default language instruction")
	}

	// Priority English: en
	enPrompt := buildSystemPrompt("en")
	if !strings.Contains(enPrompt, "English (language code: en)") {
		t.Errorf("en prompt should prioritize English, got: %s", enPrompt)
	}
}

func TestUpdateSessionSystemPrompt(t *testing.T) {
	prompt := "initial"
	messages := []Message{
		{Role: "system", Content: &prompt},
		{Role: "user", Content: "Hello"},
	}

	updateSessionSystemPrompt(&messages, "ru")
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	sysText := messages[0].GetText()
	if !strings.Contains(sysText, "Russian (language code: ru)") {
		t.Errorf("expected system message to be updated to Russian, got: %s", sysText)
	}

	// Switch to en
	updateSessionSystemPrompt(&messages, "en")
	sysText = messages[0].GetText()
	if !strings.Contains(sysText, "English (language code: en)") {
		t.Errorf("expected system message to be updated to English, got: %s", sysText)
	}

	// Reset to default
	updateSessionSystemPrompt(&messages, "")
	sysText = messages[0].GetText()
	if !strings.Contains(sysText, "in the language used by the user") {
		t.Errorf("expected system message to reset to user language, got: %s", sysText)
	}
}

func TestSaveAndLoadConfigWithContextAndLanguage(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")

	cfg := Config{
		URL:          "http://localhost:1234/v1",
		Key:          "sk-test-key",
		Model:        "test-model",
		ContextLimit: 32768,
		Language:     "ru",
	}

	if err := saveConfigToFile(cfgPath, cfg); err != nil {
		t.Fatalf("saveConfigToFile error: %v", err)
	}

	loaded, err := loadConfigFromFile(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigFromFile error: %v", err)
	}

	if loaded.URL != "http://localhost:1234/v1" {
		t.Errorf("URL mismatch: got %s", loaded.URL)
	}
	if loaded.ContextLimit != 32768 {
		t.Errorf("ContextLimit mismatch: got %d, want 32768", loaded.ContextLimit)
	}
	if loaded.Language != "ru" {
		t.Errorf("Language mismatch: got %s, want ru", loaded.Language)
	}
}

func TestAskConfirmationAutoApprove(t *testing.T) {
	// Backup original state
	origAuto := autoApprove
	origCmd := autoApproveCmd
	origWrite := autoApproveWrite
	origDownload := autoApproveDownload
	defer func() {
		autoApprove = origAuto
		autoApproveCmd = origCmd
		autoApproveWrite = origWrite
		autoApproveDownload = origDownload
	}()

	autoApprove = false
	autoApproveCmd = true
	autoApproveWrite = false
	autoApproveDownload = false

	if !askConfirmation("Разрешить запуск команды? (y/N/A): ") {
		t.Errorf("expected askConfirmation to return true when autoApproveCmd is true")
	}

	autoApproveCmd = false
	autoApproveWrite = true
	if !askConfirmation("Разрешить запись на диск? (y/N/A): ") {
		t.Errorf("expected askConfirmation to return true when autoApproveWrite is true")
	}

	autoApproveWrite = false
	autoApproveDownload = true
	if !askConfirmation("Разрешить скачивание файла? (y/N/A): ") {
		t.Errorf("expected askConfirmation to return true when autoApproveDownload is true")
	}
}

