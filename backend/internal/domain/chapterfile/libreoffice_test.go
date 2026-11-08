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
	lines := []string{
		"Кайл шёл к реке, и вода была холодной. Очень холодной.", "Он сказал, что пора идти.",
		"Ольм молчал.", "Новый абзац автора.", "Последний абзац!", "И ещё один в конце.",
	}
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
	want := append([]string{"Глава 7. Брод"}, lines...)
	if strings.Join(accepted, "\n") != strings.Join(want, "\n") {
		t.Fatalf("accepted in LibreOffice:\n%q\nwant\n%q", accepted, want)
	}
	t.Logf("LibreOffice opened the file with %d revisions; accepting them all gave the text now", revisions)
}
