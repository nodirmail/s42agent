package main

import (
	"reflect"
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

