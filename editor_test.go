package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVisualWidth(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello", 5},
		{"привет", 6},
		{"\033[1;32mUser >>> \033[0m", 9},
		{"\x1b[31mError\x1b[0m: текст", 12}, // "Error: текст" -> 5+1+1+5 = 12
	}

	for _, tt := range tests {
		got := stringVisualWidth(tt.input)
		if got != tt.want {
			t.Errorf("stringVisualWidth(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestLineEditorBasicEditing(t *testing.T) {
	ed := newLineEditor(-1, "> ")

	// Type "привет мир"
	for _, r := range "привет мир" {
		ed.insertRune(r)
	}

	if string(ed.runes) != "привет мир" {
		t.Fatalf("expected 'привет мир', got %q", string(ed.runes))
	}
	if ed.cursorPos != 10 {
		t.Fatalf("expected cursorPos 10, got %d", ed.cursorPos)
	}

	// Move left 3 times -> cursor before "мир" (index 7)
	ed.moveLeft()
	ed.moveLeft()
	ed.moveLeft()

	if ed.cursorPos != 7 {
		t.Fatalf("expected cursorPos 7, got %d", ed.cursorPos)
	}

	// Insert "дорогой " at cursor
	ed.insertString("дорогой ")
	if string(ed.runes) != "привет дорогой мир" {
		t.Fatalf("expected 'привет дорогой мир', got %q", string(ed.runes))
	}

	// Move home
	ed.moveHome()
	if ed.cursorPos != 0 {
		t.Fatalf("expected cursorPos 0, got %d", ed.cursorPos)
	}

	// Move end
	ed.moveEnd()
	if ed.cursorPos != len(ed.runes) {
		t.Fatalf("expected cursorPos at end (%d), got %d", len(ed.runes), ed.cursorPos)
	}

	// Backspace 3 times -> delete "мир"
	ed.deleteCharBefore()
	ed.deleteCharBefore()
	ed.deleteCharBefore()
	if string(ed.runes) != "привет дорогой " {
		t.Fatalf("expected 'привет дорогой ', got %q", string(ed.runes))
	}

	// Home, then Delete 7 times -> delete "привет "
	ed.moveHome()
	for i := 0; i < 7; i++ {
		ed.deleteCharUnder()
	}
	if string(ed.runes) != "дорогой " {
		t.Fatalf("expected 'дорогой ', got %q", string(ed.runes))
	}
}

func TestLineEditorWordNavigation(t *testing.T) {
	ed := newLineEditor(-1, "> ")
	ed.insertString("one two three")

	ed.moveWordLeft()
	if ed.cursorPos != 8 { // at start of "three"
		t.Fatalf("expected cursorPos 8, got %d", ed.cursorPos)
	}

	ed.moveWordLeft()
	if ed.cursorPos != 4 { // at start of "two"
		t.Fatalf("expected cursorPos 4, got %d", ed.cursorPos)
	}

	ed.moveWordRight()
	if ed.cursorPos != 8 { // after "two "
		t.Fatalf("expected cursorPos 8, got %d", ed.cursorPos)
	}

	ed.deleteWordLeft()
	if string(ed.runes) != "one three" {
		t.Fatalf("expected 'one three', got %q", string(ed.runes))
	}
}

func TestLineEditorMultiline(t *testing.T) {
	ed := newLineEditor(-1, "> ")
	ed.insertString("first line\nsecond long line\nthird")

	lines := ed.getLines()
	expectedLines := [][]rune{
		[]rune("first line"),
		[]rune("second long line"),
		[]rune("third"),
	}
	if !reflect.DeepEqual(lines, expectedLines) {
		t.Fatalf("getLines mismatch: %+v vs %+v", lines, expectedLines)
	}

	// Cursor is at end of "third" (line 2, col 5)
	lineIdx, colIdx := ed.getCursorCoords()
	if lineIdx != 2 || colIdx != 5 {
		t.Fatalf("expected coords (2, 5), got (%d, %d)", lineIdx, colIdx)
	}

	// Move Up -> should go to line 1, col 5 (which is in "second")
	ed.moveUp()
	lineIdx, colIdx = ed.getCursorCoords()
	if lineIdx != 1 || colIdx != 5 {
		t.Fatalf("expected coords (1, 5), got (%d, %d)", lineIdx, colIdx)
	}

	// Move Up again -> should go to line 0, col 5
	ed.moveUp()
	lineIdx, colIdx = ed.getCursorCoords()
	if lineIdx != 0 || colIdx != 5 {
		t.Fatalf("expected coords (0, 5), got (%d, %d)", lineIdx, colIdx)
	}

	// Move Down -> should go to line 1, col 5
	ed.moveDown()
	lineIdx, colIdx = ed.getCursorCoords()
	if lineIdx != 1 || colIdx != 5 {
		t.Fatalf("expected coords (1, 5), got (%d, %d)", lineIdx, colIdx)
	}
}

func TestLineEditorHistory(t *testing.T) {
	inputHistory = nil
	addHistory("cmd 1")
	addHistory("cmd 2")
	addHistory("cmd 3")

	ed := newLineEditor(-1, "> ")
	ed.insertString("uncommitted")

	// Press Up on line 0
	ed.moveUp()
	if string(ed.runes) != "cmd 3" {
		t.Fatalf("expected 'cmd 3', got %q", string(ed.runes))
	}

	ed.moveUp()
	if string(ed.runes) != "cmd 2" {
		t.Fatalf("expected 'cmd 2', got %q", string(ed.runes))
	}

	ed.moveDown()
	if string(ed.runes) != "cmd 3" {
		t.Fatalf("expected 'cmd 3', got %q", string(ed.runes))
	}

	// Move down past latest history -> restores uncommitted input
	ed.moveDown()
	if string(ed.runes) != "uncommitted" {
		t.Fatalf("expected 'uncommitted', got %q", string(ed.runes))
	}
}

func TestLineEditorEdgeCases(t *testing.T) {
	ed := newLineEditor(-1, "> ")

	// Backspace on empty
	ed.deleteCharBefore()
	if len(ed.runes) != 0 || ed.cursorPos != 0 {
		t.Fatalf("expected empty buffer after backspace on empty")
	}

	// Delete on empty
	ed.deleteCharUnder()
	if len(ed.runes) != 0 || ed.cursorPos != 0 {
		t.Fatalf("expected empty buffer after delete on empty")
	}

	// Left/Right on empty
	ed.moveLeft()
	ed.moveRight()
	if ed.cursorPos != 0 {
		t.Fatalf("expected cursorPos 0 on empty buffer")
	}

	// Line deletion: Ctrl+K / Ctrl+U
	ed.insertString("Первая строка\nВторая строка")
	ed.moveHome() // cursor at start of "Вторая строка"
	ed.moveRight()
	ed.moveRight()
	ed.moveRight()
	ed.moveRight()
	ed.moveRight()
	ed.moveRight() // after "Вторая"

	// Delete to start of line (Ctrl+U)
	ed.deleteToStartOfLine()
	if string(ed.runes) != "Первая строка\n строка" {
		t.Fatalf("expected 'Первая строка\\n строка', got %q", string(ed.runes))
	}

	// Delete to end of line (Ctrl+K)
	ed.deleteToEndOfLine()
	if string(ed.runes) != "Первая строка\n" {
		t.Fatalf("expected 'Первая строка\\n', got %q", string(ed.runes))
	}

	// Move left to '\n' and delete newline
	ed.moveLeft()
	ed.deleteToEndOfLine()
	if string(ed.runes) != "Первая строка" {
		t.Fatalf("expected 'Первая строка', got %q", string(ed.runes))
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"v1.1.0", "v1.0.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.1", false},
		{"1.2.0", "1.1.9", true},
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
	}

	for _, tt := range tests {
		got := isNewerVersion(tt.latest, tt.current)
		if got != tt.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestMultimodalMessage(t *testing.T) {
	// 1. Text message
	text := "Hello world"
	msgText := Message{Role: "user", Content: &text}
	if msgText.GetText() != "Hello world" {
		t.Fatalf("expected 'Hello world', got %q", msgText.GetText())
	}
	if msgText.HasImages() {
		t.Fatalf("expected HasImages to be false")
	}

	// 2. Multimodal message
	parts := []ContentPart{
		{Type: "text", Text: "What is this?"},
		{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,iVBORw0KGgo="}},
	}
	msgMM := Message{Role: "user", Content: parts}
	if msgMM.GetText() != "What is this?" {
		t.Fatalf("expected 'What is this?', got %q", msgMM.GetText())
	}
	if !msgMM.HasImages() {
		t.Fatalf("expected HasImages to be true")
	}

	// 3. JSON roundtrip
	data, err := json.Marshal(msgMM)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.GetText() != "What is this?" {
		t.Fatalf("expected restored text 'What is this?', got %q", restored.GetText())
	}
	if !restored.HasImages() {
		t.Fatalf("expected restored HasImages to be true")
	}
}

func TestExtractCandidatePaths(t *testing.T) {
	input := `C:\Users\Admin\AppData\Local\ScreenClip\{7C75D918-D6BF-4CA4-905D-B25740C36D03}.png что здесь нужно выбрать?`
	candidates := extractCandidatePaths(input)
	found := false
	for _, c := range candidates {
		if strings.Contains(c, "{7C75D918-D6BF-4CA4-905D-B25740C36D03}.png") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find Windows screenshot path in candidates: %v", candidates)
	}

	inputQuoted := `Посмотри на "my test image.jpg" и скажи ответ`
	candidatesQuoted := extractCandidatePaths(inputQuoted)
	foundQuoted := false
	for _, c := range candidatesQuoted {
		if c == "my test image.jpg" {
			foundQuoted = true
			break
		}
	}
	if !foundQuoted {
		t.Fatalf("expected to find quoted path in candidates: %v", candidatesQuoted)
	}
}

func TestBuildMultimodalMessageContentWithTempFile(t *testing.T) {
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "test_screenshot.png")
	// Minimal valid PNG header
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4")
	if err := os.WriteFile(imgFile, pngData, 0644); err != nil {
		t.Fatalf("failed to write temp png: %v", err)
	}

	prompt := fmt.Sprintf("%s что на этой картинке?", imgFile)
	content, err := buildMultimodalMessageContent(prompt)
	if err != nil {
		t.Fatalf("buildMultimodalMessageContent error: %v", err)
	}

	parts, ok := content.([]ContentPart)
	if !ok {
		t.Fatalf("expected []ContentPart, got %T", content)
	}

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + image), got %d", len(parts))
	}
	if parts[0].Type != "text" {
		t.Fatalf("expected first part to be text, got %q", parts[0].Type)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Fatalf("expected second part to be image_url, got %+v", parts[1])
	}
	if !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("expected data:image/png;base64 prefix, got %q", parts[1].ImageURL.URL)
	}

	// Test readFile protection
	result := readFile(imgFile)
	if !strings.Contains(result, "является изображением") {
		t.Fatalf("expected readFile to detect image, got %q", result)
	}

	// Test viewImage tool
	toolResult, imgPart := viewImage(imgFile)
	if !strings.Contains(toolResult, "успешно загружено") {
		t.Fatalf("expected viewImage to report success, got %q", toolResult)
	}
	if imgPart == nil || imgPart.ImageURL == nil {
		t.Fatalf("expected viewImage to return valid image part")
	}
	if !strings.HasPrefix(imgPart.ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("expected valid data URL in imgPart, got %q", imgPart.ImageURL.URL)
	}
}


