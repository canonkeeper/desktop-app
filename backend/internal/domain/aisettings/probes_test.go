// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// probeTexts are all the words the probes send.
func probeTexts() []string {
	var texts []string
	for _, probe := range []Probe{ProbeShort, ProbeTool, ProbeLongInput, ProbeLongOutput, ProbeCalibrate} {
		question := QuestionOf(probe)
		texts = append(texts, question.Instructions, question.Input)
		if question.Tool != nil {
			texts = append(texts, question.Tool.Description, question.Tool.Parameters)
		}
	}
	return texts
}

// specs/2026-11-13-desk-probes.md PB2.3 (3): the long text is about 20 000 tokens, its fact stands once at its start and
// the made-up sentences after it never say it; the calibration is its own text before the short question.
func TestTheLongTextHoldsItsFactOnceAtTheStart(t *testing.T) {
	long := QuestionOf(ProbeLongInput).Input
	if count := utf8.RuneCountInString(long); count < longInputCharacters || count > longInputCharacters+2_000 {
		t.Fatalf("the long text has %d characters", count)
	}
	lower := strings.ToLower(long)
	if strings.Count(lower, longInputFact) != 1 || strings.Index(lower, longInputFact) > 300 || strings.Count(lower, "сорок семь") != 1 {
		t.Fatal("the fact is not once at the start of the long text")
	}
	calibrate := QuestionOf(ProbeCalibrate).Input
	if !strings.HasSuffix(calibrate, shortQuestion) || utf8.RuneCountInString(calibrate)-utf8.RuneCountInString(shortQuestion) != CalibrationCharacters() ||
		CalibrationCharacters() < calibrationCharacters || strings.Contains(strings.ToLower(calibrate), longInputFact) {
		t.Fatal("the calibration is not its text before the short question")
	}
	if QuestionOf(ProbeShort).Tool != nil || QuestionOf(ProbeTool).Tool == nil || QuestionOf(ProbeTool).Tool.Name != ProbeTool.Name() {
		t.Fatal("only the probe of a tool offers its function")
	}
}

// tags are the markup of a book file, a space between its words.
var tags = regexp.MustCompile(`<[^>]*>`)

// spaces folds the white space of a text into one space.
func spaces(text string) string { return strings.Join(strings.Fields(text), " ") }

// bookTexts are the texts of the test books of the repository: every .fb2, and every file of e2e/testdata — a zipped book
// (.epub, .docx, .fb2.zip) by the texts of its entries.
func bookTexts(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	books := map[string]string{}
	add := func(path string, raw []byte) {
		books[path] += " " + spaces(tags.ReplaceAllString(string(raw), " "))
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if name == "node_modules" || name == ".git" || strings.HasPrefix(name, "dist") || name == ".claude" || name == "design" {
				return filepath.SkipDir
			}
			return nil
		}
		inTestData := strings.Contains(filepath.ToSlash(path), "/e2e/testdata/")
		switch extension := strings.ToLower(filepath.Ext(name)); {
		case extension == ".fb2" || inTestData && (extension == ".txt" || extension == ".md"):
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			add(path, raw)
		case extension == ".epub" || extension == ".docx" || strings.HasSuffix(strings.ToLower(name), ".fb2.zip"):
			if !inTestData && !strings.Contains(filepath.ToSlash(path), "/testdata/") {
				return nil
			}
			archive, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			for _, file := range archive.File {
				if !strings.HasSuffix(file.Name, ".xml") && !strings.HasSuffix(file.Name, ".xhtml") && !strings.HasSuffix(file.Name, ".html") && !strings.HasSuffix(file.Name, ".fb2") {
					continue
				}
				opened, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(opened)
				_ = opened.Close()
				if err != nil {
					t.Fatal(err)
				}
				add(path, raw)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return books
}

// sentences are the pieces of a text between the ends of its sentences and lines, of at least least characters.
func sentences(text string, least int) []string {
	var pieces []string
	for _, piece := range regexp.MustCompile(`[.!?…\n]+`).Split(text, -1) {
		if piece = spaces(piece); utf8.RuneCountInString(piece) >= least {
			pieces = append(pieces, piece)
		}
	}
	return pieces
}

// PB2.2: the probes send no line of the test books of the repository, and no line of those books stands in the probes.
func TestTheProbesSayNoLineOfTheBooks(t *testing.T) {
	books := bookTexts(t)
	if len(books) == 0 {
		t.Fatal("no test book was found")
	}
	characters := 0
	for _, book := range books {
		characters += utf8.RuneCountInString(book)
	}
	t.Logf("%d test books, %d characters", len(books), characters)
	probes := spaces(strings.Join(probeTexts(), "\n"))
	seen := map[string]bool{}
	for _, text := range probeTexts() {
		for _, sentence := range sentences(text, 20) {
			if seen[sentence] {
				continue
			}
			seen[sentence] = true
			for path, book := range books {
				if strings.Contains(book, sentence) {
					t.Errorf("the probes say %q of %s", sentence, path)
				}
			}
		}
	}
	for path, book := range books {
		for _, sentence := range sentences(book, 30) {
			if strings.Contains(probes, sentence) {
				t.Errorf("%s says %q of the probes", path, sentence)
			}
		}
	}
}
