package chapterfile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// acceptInLibreOffice opens a file in LibreOffice through UNO, counts its revisions, accepts them all and saves the
// text; it prints the count of revisions.
const acceptInLibreOffice = `import os, subprocess, sys, time, uno
from com.sun.star.beans import PropertyValue

def pv(name, value):
    item = PropertyValue()
    item.Name, item.Value = name, value
    return item

source, target, profile = sys.argv[1], sys.argv[2], sys.argv[3]
pipe = "ckproofread%d" % os.getpid()
office = subprocess.Popen(["soffice", "--headless", "--invisible", "--norestore", "--nologo",
    "-env:UserInstallation=" + uno.systemPathToFileUrl(profile), "--accept=pipe,name=%s;urp;" % pipe])
local = uno.getComponentContext()
resolver = local.ServiceManager.createInstanceWithContext("com.sun.star.bridge.UnoUrlResolver", local)
context = None
for _ in range(240):
    try:
        context = resolver.resolve("uno:pipe,name=%s;urp;StarOffice.ComponentContext" % pipe)
        break
    except Exception:
        time.sleep(0.25)
manager = context.ServiceManager
desktop = manager.createInstanceWithContext("com.sun.star.frame.Desktop", context)
document = desktop.loadComponentFromURL(uno.systemPathToFileUrl(source), "_blank", 0, (pv("Hidden", True),))
revisions = 0
enumeration = document.getRedlines().createEnumeration()
while enumeration.hasMoreElements():
    enumeration.nextElement()
    revisions += 1
helper = manager.createInstanceWithContext("com.sun.star.frame.DispatchHelper", context)
helper.executeDispatch(document.getCurrentController().getFrame(), ".uno:AcceptAllTrackedChanges", "", 0, ())
document.storeToURL(uno.systemPathToFileUrl(target), (pv("FilterName", "Text (encoded)"), pv("FilterOptions", "UTF8,LF,,,")))
document.close(True)
try:
    desktop.terminate()
except Exception:
    pass
office.wait(timeout=60)
print(revisions)
`

// TestTheFileWithRevisionsOpensInLibreOffice is the check of PR6.6 in LibreOffice itself: the file opens, holds
// revisions, and «Принять все» gives the text now. It needs soffice and the Python bridge of LibreOffice, so it runs
// only with CANONKEEPER_LIBREOFFICE=1 (specs/2026-11-07-writing-proofread.md, Implementation checklist).
func TestTheFileWithRevisionsOpensInLibreOffice(t *testing.T) {
	if os.Getenv("CANONKEEPER_LIBREOFFICE") != "1" {
		t.Skip("set CANONKEEPER_LIBREOFFICE=1 to open the file in LibreOffice")
	}
	title := Paragraph{Runs: []Run{{Text: "Глава 7. Переправа"}}}
	original := Document{Format: FormatDOCX, Title: &title, Paragraphs: append([]Paragraph{
		{Runs: []Run{{Text: "Кайл шол к реке, и вода была "}, {Text: "холодной", Italic: true}, {Text: ". "}, {Text: "Очень", Bold: true}, {Text: " холодной."}}},
		{},
	}, plain("Он сказал что пора идти.", "Этот абзац автор убрал.", "Ольм молчал — и это молчание было громче слов.", "Последний абзац.")...)}
	lines := now(
		"Кайл шёл к реке, и вода была ⟨холодной⟩. ⟪Очень⟫ холодной.", "Он сказал, что пора идти.",
		"Ольм молчал.", "Новый абзац автора.", "Последний абзац!", "И ещё один в конце.",
	)
	directory := t.TempDir()
	source, target, script := filepath.Join(directory, "chapter.docx"), filepath.Join(directory, "chapter.txt"), filepath.Join(directory, "accept.py")
	if err := os.WriteFile(source, trackedFile(t, original, "Глава 7. Брод", lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(acceptInLibreOffice), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command("python3", script, source, target, filepath.Join(directory, "profile"))
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("LibreOffice: %v\n%s", err, output.String())
	}
	fields := strings.Fields(output.String())
	revisions, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || revisions == 0 {
		t.Fatalf("revisions in LibreOffice: %q", output.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var accepted []string
	for _, line := range strings.Split(strings.TrimPrefix(string(saved), "\uFEFF"), "\n") {
		if strings.TrimSpace(line) != "" {
			accepted = append(accepted, line)
		}
	}
	want := append([]string{"Глава 7. Брод"}, texts(lines)...)
	if strings.Join(accepted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("accepted in LibreOffice:\n%q\nwant\n%q", accepted, want)
	}
	t.Logf("LibreOffice opened the file with %d revisions; accepting them all gave the text now", revisions)
}

// lookInLibreOffice prints, for every paragraph with words of a file opened in LibreOffice, its first-line indent (in
// hundredths of a millimetre), its line spacing (percent) and its italic words, one paragraph a line.
const lookInLibreOffice = `
paragraphs = document.getText().createEnumeration()
while paragraphs.hasMoreElements():
    paragraph = paragraphs.nextElement()
    if not paragraph.supportsService("com.sun.star.text.Paragraph") or not paragraph.getString().strip():
        continue
    italic = []
    portions = paragraph.createEnumeration()
    while portions.hasMoreElements():
        portion = portions.nextElement()
        if portion.CharPosture.value != "NONE" and portion.getString().strip():
            italic.append(portion.getString().strip())
    print("look|%s|%d|%d|%s" % (paragraph.getString(), paragraph.ParaFirstLineIndent, paragraph.ParaLineSpacing.Height, ",".join(italic)))
`

// TestTheFileInTheLookOfTheAuthorOpensInLibreOffice is the check of PR6.7 in LibreOffice: the file with revisions of a
// Word file with a look of its own opens with the paragraph's indent and spacing and the italic of its character style,
// and «Принять все» gives the text now.
func TestTheFileInTheLookOfTheAuthorOpensInLibreOffice(t *testing.T) {
	if os.Getenv("CANONKEEPER_LIBREOFFICE") != "1" {
		t.Skip("set CANONKEEPER_LIBREOFFICE=1 to open the file in LibreOffice")
	}
	original, err := Parse("Глава 3.docx", layoutFile(t))
	if err != nil {
		t.Fatal(err)
	}
	lines := now("Кира шла ⟨медленно⟩.", "Ольм ждал у северных ворот.", "Светало.")
	directory := t.TempDir()
	source, target, script := filepath.Join(directory, "chapter.docx"), filepath.Join(directory, "chapter.txt"), filepath.Join(directory, "look.py")
	if err := os.WriteFile(source, trackedFile(t, original, "Глава 3. Брод", lines), 0o600); err != nil {
		t.Fatal(err)
	}
	looking := strings.Replace(acceptInLibreOffice, "revisions = 0\n", lookInLibreOffice+"revisions = 0\n", 1)
	if err := os.WriteFile(script, []byte(looking), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command("python3", script, source, target, filepath.Join(directory, "profile"))
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("LibreOffice: %v\n%s", err, output.String())
	}
	looks := map[string]string{}
	for _, line := range strings.Split(output.String(), "\n") {
		if fields := strings.SplitN(line, "|", 5); len(fields) == 5 && fields[0] == "look" {
			looks[fields[1]] = fields[2] + "|" + fields[3] + "|" + fields[4]
		}
	}
	// 567 twentieths of a point are 1 cm; a line of 360 of 240 is one and a half.
	if looks["Кира шла медленно."] != "1000|150|медленно" {
		t.Fatalf("the look in LibreOffice: %q\n%s", looks, output.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var accepted []string
	for _, line := range strings.Split(strings.TrimPrefix(string(saved), "\uFEFF"), "\n") {
		if strings.TrimSpace(line) != "" {
			accepted = append(accepted, line)
		}
	}
	if want := append([]string{"Глава 3. Брод"}, texts(lines)...); strings.Join(accepted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("accepted in LibreOffice:\n%q\nwant\n%q", accepted, want)
	}
}

// facesInLibreOffice is acceptInLibreOffice that accepts or rejects every revision, by its fourth argument, and then
// prints the italic and bold words of every paragraph as LibreOffice shows them.
var facesInLibreOffice = strings.Replace(acceptInLibreOffice,
	`helper.executeDispatch(document.getCurrentController().getFrame(), ".uno:AcceptAllTrackedChanges", "", 0, ())`,
	`command = ".uno:AcceptAllTrackedChanges" if sys.argv[4] == "accept" else ".uno:RejectAllTrackedChanges"
helper.executeDispatch(document.getCurrentController().getFrame(), command, "", 0, ())
paragraphs = document.getText().createEnumeration()
while paragraphs.hasMoreElements():
    paragraph = paragraphs.nextElement()
    if not paragraph.supportsService("com.sun.star.text.Paragraph"):
        continue
    portions = paragraph.createEnumeration()
    while portions.hasMoreElements():
        portion = portions.nextElement()
        words = portion.getString().strip()
        if words and portion.CharPosture.value != "NONE":
            print("italic|" + words)
        if words and portion.CharWeight > 100:
            print("bold|" + words)`, 1)

// TestAChangedFaceIsAcceptedAndRejectedInLibreOffice is the check of TF7.2 in LibreOffice (specs/2026-11-08-italic-bold-in-text.md):
// «Принять все» gives the faces of the text now and «Отклонить все» those of the original, in a file without a look of
// its own and in the look of the author's Word file.
func TestAChangedFaceIsAcceptedAndRejectedInLibreOffice(t *testing.T) {
	if os.Getenv("CANONKEEPER_LIBREOFFICE") != "1" {
		t.Skip("set CANONKEEPER_LIBREOFFICE=1 to open the file in LibreOffice")
	}
	look, err := Parse("Глава 3.docx", layoutFile(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]struct {
		original           Document
		now                []Paragraph
		accepted, rejected string
	}{
		"a text": {
			original: Document{Format: FormatText, Paragraphs: []Paragraph{
				{Runs: []Run{{Text: "Кайл шёл к реке, и вода была "}, {Text: "холодной", Italic: true}, {Text: "."}}},
			}},
			now:      now("Кайл ⟨шёл⟩ к ⟪реке⟫, и вода была холодной."),
			accepted: "italic|шёл bold|реке", rejected: "italic|холодной",
		},
		"the look of the author": {
			original: look, now: now("Кира ⟨шла⟩ медленно.", "Ольм ждал у ⟪ворот⟫."),
			accepted: "bold|Глава 3. Брод italic|шла bold|ворот", rejected: "bold|Глава 3. Брод italic|медленно",
		},
	} {
		for _, action := range []string{"accept", "reject"} {
			directory := t.TempDir()
			source, target, script := filepath.Join(directory, "chapter.docx"), filepath.Join(directory, "chapter.txt"), filepath.Join(directory, "faces.py")
			title := ""
			if check.original.Title != nil {
				title = check.original.TitleText()
			}
			if err := os.WriteFile(source, trackedFile(t, check.original, title, check.now), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, []byte(facesInLibreOffice), 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			command := exec.Command("python3", script, source, target, filepath.Join(directory, "profile"), action)
			command.Stdout, command.Stderr = &output, &output
			if err := command.Run(); err != nil {
				t.Fatalf("%s, %s in LibreOffice: %v\n%s", name, action, err, output.String())
			}
			var faces []string
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.HasPrefix(line, "italic|") || strings.HasPrefix(line, "bold|") {
					faces = append(faces, line)
				}
			}
			want := check.accepted
			if action == "reject" {
				want = check.rejected
			}
			if got := strings.Join(faces, " "); got != want {
				t.Fatalf("%s, %s in LibreOffice: faces %q, want %q\n%s", name, action, got, want, output.String())
			}
		}
	}
}
