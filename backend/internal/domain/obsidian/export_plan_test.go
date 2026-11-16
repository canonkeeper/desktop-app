// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func node(key string) FileID { return FileID{Kind: ExportNode, Key: key} }

// entry is the entry of the manifest for a text written at a path.
func entry(id FileID, path, text string) ManifestFile {
	return ManifestFile{Kind: id.Kind, Key: id.Key, Path: path, Print: ExportPrint([]byte(text)), Sum: ExportSum([]byte(text))}
}

// steps are the steps of a plan by the paths of their references, in their order.
func steps(actions []ExportAction) []string {
	var got []string
	for _, action := range actions {
		item := string(action.Step) + " " + action.File.Path
		if action.Skip != "" {
			item += " " + string(action.Skip)
		}
		got = append(got, item)
	}
	return got
}

// specs/2026-11-15-desk-obsidian-export.md OE1.4, OE2.5–OE2.8: what each reference becomes by the manifest, the fresh texts and
// the disk. A reference of the app is replaced only while the disk holds what was written; one the author changed is never
// replaced without his word and, with it, moved — never written over; the references that went leave first.
func TestThePlanOfARunNeverWritesOverTheAuthor(t *testing.T) {
	const old, fresh, edited = "старый текст\n", "свежий текст\n", "старый текст\nмоя мысль\n"
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{
		entry(node("same"), "Герои/Same.md", fresh),
		entry(node("update"), "Герои/Update.md", old),
		entry(node("edited"), "Герои/Edited.md", old),
		entry(node("gone"), "Герои/Gone.md", old),
		entry(node("gone-edited"), "Герои/GoneEdited.md", old),
		entry(node("gone-missing"), "Герои/GoneMissing.md", old),
		entry(node("missing"), "Герои/Missing.md", old),
		entry(node("cloud"), "Герои/Cloud.md", old),
		entry(FileID{Kind: ExportChapter, Key: "no-card"}, "Главы/Том/1 Пролог.md", old),
	}}
	wanted := []Wanted{
		{ID: node("same"), Path: "Герои/Same.md", Text: []byte(fresh)},
		{ID: node("update"), Path: "Герои/Update.md", Text: []byte(fresh)},
		{ID: node("edited"), Path: "Герои/Edited.md", Text: []byte(fresh)},
		{ID: node("missing"), Path: "Герои/Missing.md", Text: []byte(fresh)},
		{ID: node("cloud"), Path: "Герои/Cloud.md", Text: []byte(fresh)},
		{ID: node("new"), Path: "Герои/New.md", Text: []byte(fresh)},
	}
	stay := []FileID{{Kind: ExportChapter, Key: "no-card"}}
	missing := map[string]bool{"Герои/Missing.md": true, "Герои/GoneMissing.md": true}
	// The content of «same» did not change: it is not read; the chapter without a fresh card is not either.
	reads := ExportReads(manifest, wanted, stay, missing)
	if want := []string{"Герои/Update.md", "Герои/Edited.md", "Герои/Gone.md", "Герои/GoneEdited.md", "Герои/Cloud.md"}; !reflect.DeepEqual(reads, want) {
		t.Fatalf("ExportReads = %q, want %q", reads, want)
	}
	disk := map[string]DiskFile{
		"Герои/Update.md":      {Print: ExportPrint([]byte(old))},
		"Герои/Edited.md":      {Print: ExportPrint([]byte(edited))},
		"Герои/Gone.md":        {Print: ExportPrint([]byte(old))},
		"Герои/GoneEdited.md":  {Print: ExportPrint([]byte(edited))},
		"Герои/GoneMissing.md": {Missing: true},
		"Герои/Missing.md":     {Missing: true},
		"Герои/Cloud.md":       {Skip: ExportCloudOnly},
	}
	plan := PlanExport(manifest, wanted, stay, disk, false)
	want := []string{
		"move Герои/Gone.md", "keep Герои/GoneEdited.md", "forget Герои/GoneMissing.md",
		"unchanged Герои/Same.md", "update Герои/Update.md", "keep Герои/Edited.md", "create Герои/Missing.md",
		"skip Герои/Cloud.md cloud_only", "stay Главы/Том/1 Пролог.md", "create Герои/New.md",
	}
	if got := steps(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("the plan:\n%q\nwant:\n%q", got, want)
	}
	for _, action := range plan {
		if (action.Step == StepUpdate || action.Step == StepMove) && action.Expect != ExportPrint([]byte(old)) {
			t.Fatalf("%s %s expects %q", action.Step, action.File.Path, action.Expect)
		}
		if (action.Step == StepCreate || action.Step == StepUpdate) &&
			(action.File.Print != ExportPrint([]byte(fresh)) || action.File.Sum != ExportSum([]byte(fresh))) {
			t.Fatalf("%s %s keeps no fingerprint of what it writes", action.Step, action.File.Path)
		}
	}
	// With the word of the author what he changed is moved into _Удалено/ and the fresh reference written.
	replace := steps(PlanExport(manifest, wanted, stay, disk, true))
	if !slices.Contains(replace, "replace Герои/Edited.md") || !slices.Contains(replace, "move Герои/GoneEdited.md") {
		t.Fatalf("with replaceChanged: %q", replace)
	}
	for _, action := range PlanExport(manifest, wanted, stay, disk, true) {
		if action.Step == StepReplace && action.Expect != ExportPrint([]byte(edited)) {
			t.Fatal("a replacement expects what the author wrote on the disk")
		}
	}
}

// OE2.7: a node that changed its name is a new node — a new reference — and the reference before goes by the rule of a node
// that went; its name it frees goes to the new one without « (2)» (OE1.4).
func TestANodeThatChangedItsNameIsANewReference(t *testing.T) {
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{entry(node("анна"), "Герои/Анна.md", "a\n")}}
	files := []ExportFile{{ID: node("анна#2"), Folder: "Герои", Name: "Анна"}}
	gone := Gone(manifest, files, nil)
	if len(gone) != 1 || gone[0].Key != "анна" {
		t.Fatalf("Gone = %+v", gone)
	}
	disk := map[string]DiskFile{"Герои/Анна.md": {Print: ExportPrint([]byte("a\n"))}}
	moving := map[string]bool{}
	for _, file := range gone {
		if Moves(file, disk[file.Path], false) {
			moving[FoldKey(file.Path)] = true
		}
	}
	occupied := func(path string) bool { return !moving[FoldKey(path)] && path == "Герои/Анна.md" }
	paths, _ := AssignPaths(manifest, files, nil, occupied)
	if paths[node("анна#2")] != "Герои/Анна.md" {
		t.Fatalf("the freed name goes to %q", paths[node("анна#2")])
	}
	wanted := []Wanted{{ID: node("анна#2"), Path: paths[node("анна#2")], Text: []byte("b\n")}}
	if got := steps(PlanExport(manifest, wanted, nil, disk, false)); !reflect.DeepEqual(got, []string{"move Герои/Анна.md", "create Герои/Анна.md"}) {
		t.Fatalf("the plan: %q", got)
	}
}

// OE1.4, OE2.1, OE2.6: a path held by a file the manifest does not name, or by another new reference of the run — the case
// ignored —, gives « (2)»; the path the manifest keeps for a key stays; books with one title get their folders « (2)» in
// their order, and a book the manifest knows keeps its folder.
func TestAPathHeldGivesTheNextNumber(t *testing.T) {
	manifest := Manifest{
		Format: ExportFormat, Saga: "saga-1",
		Books: []ManifestBook{{Book: "book-old", Folder: "Главы/Старое имя"}},
		Files: []ManifestFile{
			entry(node("кай"), "Герои/Кай нашёл новое имя.md", "k\n"),
			entry(FileID{Kind: ExportChapter, Key: "ch-old"}, "Главы/Старое имя/1 Начало.md", "c\n"),
		},
	}
	files := []ExportFile{
		{ID: node("кай"), Folder: "Герои", Name: "Кай"},
		{ID: node("ива"), Folder: "Герои", Name: "Ива"},
		{ID: node("ива#2"), Folder: "Герои", Name: "ИВА"},
		{ID: node("мир"), Folder: "Места", Name: "Мир"},
		{ID: FileID{Kind: ExportChapter, Key: "ch-1"}, Book: "book-1", Name: "1 Пролог"},
		{ID: FileID{Kind: ExportChapter, Key: "ch-2"}, Book: "book-2", Name: "1 Пролог"},
		{ID: FileID{Kind: ExportChapter, Key: "ch-old"}, Book: "book-old", Name: "1 Начало"},
		{ID: FileID{Kind: ExportChapter, Key: "ch-old-2"}, Book: "book-old", Name: "2 Конец"},
	}
	books := []ExportBook{{ID: "book-1", Title: "Том"}, {ID: "book-2", Title: "том"}, {ID: "book-old", Title: "Новое имя книги"}}
	foreign := map[string]bool{FoldKey("Места/Мир.md"): true}
	paths, folders := AssignPaths(manifest, files, books, func(path string) bool { return foreign[FoldKey(path)] })
	want := map[FileID]string{
		node("кай"): "Герои/Кай нашёл новое имя.md", node("ива"): "Герои/Ива.md", node("ива#2"): "Герои/ИВА (2).md",
		node("мир"):                        "Места/Мир (2).md",
		{Kind: ExportChapter, Key: "ch-1"}: "Главы/Том/1 Пролог.md", {Kind: ExportChapter, Key: "ch-2"}: "Главы/том (2)/1 Пролог.md",
		{Kind: ExportChapter, Key: "ch-old"}: "Главы/Старое имя/1 Начало.md", {Kind: ExportChapter, Key: "ch-old-2"}: "Главы/Старое имя/2 Конец.md",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("AssignPaths =\n%v\nwant\n%v", paths, want)
	}
	wantBooks := []ManifestBook{{Book: "book-1", Folder: "Главы/Том"}, {Book: "book-2", Folder: "Главы/том (2)"}, {Book: "book-old", Folder: "Главы/Старое имя"}}
	if !reflect.DeepEqual(folders, wantBooks) {
		t.Fatalf("the folders of the books = %v", folders)
	}
}

// OE2.6: a reference whose file went from the path of the manifest is found where the author moved it in the folder — the
// one note whose header names this saga and this reference; two such notes or a note of another saga find nothing.
func TestAReferenceTheAuthorMovedIsFoundByItsKey(t *testing.T) {
	text := "---\ncanonkeeper-saga: \"saga-1\"\ncanonkeeper-key: \"анна\"\ncanonkeeper-kind: \"node\"\n---\nтекст\n"
	header, ok := HeaderOf("Герои/Анна Сватова.md", []byte(text))
	if !ok || header.Saga != "saga-1" || header.ID != node("анна") {
		t.Fatalf("HeaderOf = %+v, %v", header, ok)
	}
	if _, ok := HeaderOf("Мои/Заметка.md", []byte("просто заметка\n")); ok {
		t.Fatal("a note of the author has no header of the app")
	}
	other := ExportHeader{Path: "Герои/Чужая.md", Saga: "saga-2", ID: node("анна")}
	if path, ok := Relocated("saga-1", node("анна"), []ExportHeader{header, other}); !ok || path != "Герои/Анна Сватова.md" {
		t.Fatalf("Relocated = %q, %v", path, ok)
	}
	copy := ExportHeader{Path: "Герои/Копия.md", Saga: "saga-1", ID: node("анна")}
	if _, ok := Relocated("saga-1", node("анна"), []ExportHeader{header, copy}); ok {
		t.Fatal("two notes of one key find nothing")
	}
	if _, ok := Relocated("saga-1", node("анна"), []ExportHeader{other}); ok {
		t.Fatal("a note of another saga is not this reference")
	}
}

// OE2.8: before its first write a run names in the manifest each reference it writes with the text it writes; the next run
// settles a write the run did not say done by what lies at its path — its text is written, the text before is not replaced, a
// file of this reference otherwise is the author's change, nothing there or a file of someone else is no reference at all.
func TestARunNamesItsWritesBeforeAndTheNextOneSettlesThem(t *testing.T) {
	const old, fresh, edited = "старый текст\n", "свежий текст\n", "свежий текст\nмоя мысль\n"
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{
		entry(node("same"), "Герои/Same.md", fresh), entry(node("update"), "Герои/Update.md", old), entry(node("missing"), "Герои/Missing.md", old),
	}}
	plan := []ExportAction{
		{Step: StepUnchanged, File: manifest.Files[0]},
		{Step: StepUpdate, File: entry(node("update"), "Герои/Update.md", fresh), Expect: ExportPrint([]byte(old))},
		{Step: StepCreate, File: entry(node("missing"), "Герои/Missing.md", fresh)},
		{Step: StepCreate, File: entry(node("new"), "Герои/New.md", fresh)},
	}
	intended := Intend(manifest, plan)
	writing := &ManifestWrite{Print: ExportPrint([]byte(fresh)), Sum: ExportSum([]byte(fresh))}
	want := []ManifestFile{
		manifest.Files[0],
		{Kind: ExportNode, Key: "update", Path: "Герои/Update.md", Print: ExportPrint([]byte(old)), Sum: ExportSum([]byte(old)), Writing: writing},
		{Kind: ExportNode, Key: "missing", Path: "Герои/Missing.md", Writing: writing},
		{Kind: ExportNode, Key: "new", Path: "Герои/New.md", Writing: writing},
	}
	if !reflect.DeepEqual(intended.Files, want) || len(manifest.Files) != 3 || manifest.Files[1].Writing != nil {
		t.Fatalf("Intend = %+v", intended.Files)
	}
	if read, err := ReadManifest(intended.Encode()); err != nil || !reflect.DeepEqual(read.Files, want) {
		t.Fatalf("the manifest with its writes reads back as %+v, %v", read.Files, err)
	}
	for _, bad := range []ManifestFile{
		{Kind: ExportNode, Key: "x", Path: "Герои/X.md"},
		{Kind: ExportNode, Key: "x", Path: "Герои/X.md", Print: "p"},
		{Kind: ExportNode, Key: "x", Path: "Герои/X.md", Writing: &ManifestWrite{Print: "p"}},
	} {
		broken := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{bad}}
		if _, err := ReadManifest(broken.Encode()); err == nil {
			t.Fatalf("a manifest with %+v reads", bad)
		}
	}
	update, created := intended.Files[1], intended.Files[3]
	printOf := func(text string) DiskFile {
		return DiskFile{Print: ExportPrint([]byte(text)), Sum: ExportSum([]byte(text))}
	}
	for _, test := range []struct {
		name string
		file ManifestFile
		disk DiskFile
		ours bool
		want *ManifestFile
	}{
		{"the replacement is on the disk", update, printOf(fresh), true, ptr(entry(node("update"), "Герои/Update.md", fresh))},
		{"the replacement did not happen", update, printOf(old), true, ptr(entry(node("update"), "Герои/Update.md", old))},
		{"the author changed the replaced reference", update, printOf(edited), true, ptr(entry(node("update"), "Герои/Update.md", old))},
		{"the replaced reference went", update, DiskFile{Missing: true}, false, ptr(entry(node("update"), "Герои/Update.md", old))},
		{"the new reference is on the disk", created, printOf(fresh), true, ptr(entry(node("new"), "Герои/New.md", fresh))},
		{"the author changed the new reference", created, printOf(edited), true, ptr(entry(node("new"), "Герои/New.md", fresh))},
		{"the new reference was not written", created, DiskFile{Missing: true}, false, nil},
		{"a file of someone else at the path of the new one", created, printOf(edited), false, nil},
		{"a file that does not read", created, DiskFile{Skip: ExportBusy}, false, &created},
		// The bytes of the disk differ from the write while the text reads the same: a synchronizer rewrote the ends of the
		// lines, or a write that changed only the target of a link did not rename — the entry takes the bytes of the disk,
		// so the plan writes the fresh text again.
		{"a synchronizer rewrote the ends of the lines", update, printOf(strings.ReplaceAll(fresh, "\n", "\r\n")), false,
			&ManifestFile{Kind: ExportNode, Key: "update", Path: "Герои/Update.md", Print: ExportPrint([]byte(fresh)), Sum: ExportSum([]byte(strings.ReplaceAll(fresh, "\n", "\r\n")))}},
		{"a reference no run is writing", manifest.Files[0], printOf(edited), false, &manifest.Files[0]},
	} {
		got, ok := Settle(test.file, test.disk, test.ours)
		switch {
		case test.want == nil && ok:
			t.Errorf("%s: Settle = %+v, want none", test.name, got)
		case test.want != nil && (!ok || !reflect.DeepEqual(got, *test.want)):
			t.Errorf("%s: Settle = %+v %v, want %+v", test.name, got, ok, *test.want)
		}
	}
}

func ptr[T any](value T) *T { return &value }

// OE2.5 and the decision of 15.11: «the fresh text is what was written» is the SHA-256 of the bytes, not the fingerprint — a
// text whose only change is the target of a link (the reference it leads to moved) is read and written again, while the file
// on the disk with its target rewritten by Obsidian is still not a change of the author.
func TestATextWhoseOnlyChangeIsATargetOfALinkIsWrittenAgain(t *testing.T) {
	const before, after = "- [[CanonKeeper/Сага/Герои/Сава|Сава]]\n", "- [[CanonKeeper/Сага/Герои/Сава (2)|Сава]]\n"
	if ExportPrint([]byte(before)) != ExportPrint([]byte(after)) {
		t.Fatal("the fingerprint counts the target")
	}
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{entry(node("мара"), "Герои/Мара.md", before)}}
	wanted := []Wanted{{ID: node("мара"), Path: "Герои/Мара.md", Text: []byte(after)}}
	if reads := ExportReads(manifest, wanted, nil, nil); !reflect.DeepEqual(reads, []string{"Герои/Мара.md"}) {
		t.Fatalf("ExportReads = %q", reads)
	}
	rewritten := "- [[Сава|Сава]]\n"
	plan := PlanExport(manifest, wanted, nil, map[string]DiskFile{"Герои/Мара.md": {Print: ExportPrint([]byte(rewritten))}}, false)
	if got := steps(plan); !reflect.DeepEqual(got, []string{"update Герои/Мара.md"}) {
		t.Fatalf("the plan = %q", got)
	}
}

// OE2.8, OE1.4: a plan whose reference that went frees its path — moved into _Удалено/ or forgotten, its file gone — while a
// new reference takes it keeps a manifest of intents that reads: the reference that went stays named at its path until its
// move is said, and the write that takes the path is named only once it is free (by the run, right before its rename).
func TestTheManifestOfIntentsReadsWhenAFreedPathIsTaken(t *testing.T) {
	const old, fresh = "старый текст\n", "свежий текст\n"
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{
		entry(node("сава"), "Герои/Сава.md", old), entry(node("мара"), "Герои/Мара.md", old),
	}}
	for _, step := range []ExportStep{StepMove, StepForget} {
		plan := []ExportAction{
			{Step: step, File: manifest.Files[0], Expect: ExportPrint([]byte(old))},
			{Step: StepUpdate, File: entry(node("мара"), "Герои/Мара.md", fresh), Expect: ExportPrint([]byte(old))},
			{Step: StepCreate, File: entry(node("сава#"), "герои/сава.md", fresh)},
			{Step: StepCreate, File: entry(node("глеб"), "Герои/Глеб.md", fresh)},
		}
		intended := Intend(manifest, plan)
		if _, err := ReadManifest(intended.Encode()); err != nil {
			t.Fatalf("%s and a create at the freed path: the manifest of intents does not read: %v", step, err)
		}
		var named []string
		for _, file := range intended.Files {
			named = append(named, file.Key+":"+file.Path+":"+map[bool]string{true: "writing", false: "written"}[file.Writing != nil])
		}
		want := []string{"сава:Герои/Сава.md:written", "мара:Герои/Мара.md:writing", "глеб:Герои/Глеб.md:writing"}
		if !reflect.DeepEqual(named, want) {
			t.Fatalf("%s: the manifest of intents names %q, want %q", step, named, want)
		}
	}
}

// OE2.8: a write that changes only the target of a link reads as the text before it; cut before its rename, the next run
// tells it not written by the SHA-256 of the bytes, and the plan writes it then.
func TestALinkOnlyWriteCutBeforeItsRenameIsNotTakenAsWritten(t *testing.T) {
	const before, after = "- [[CanonKeeper/Сага/Герои/Мара|Мара]]\n", "- [[CanonKeeper/Сага/Мои/Мара — справка|Мара]]\n"
	index := FileID{Kind: ExportIndex, Key: ExportIndexKey}
	manifest := Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{entry(index, "Сага.md", before)}}
	update := ExportAction{Step: StepUpdate, File: entry(index, "Сага.md", after), Expect: ExportPrint([]byte(before))}
	intended := Intend(manifest, []ExportAction{update})
	disk := DiskFile{Print: ExportPrint([]byte(before)), Sum: ExportSum([]byte(before))}
	settled, ok := Settle(intended.Files[0], disk, true)
	if !ok || settled.Sum != ExportSum([]byte(before)) || settled.Writing != nil {
		t.Fatalf("Settle = %+v %v", settled, ok)
	}
	plan := PlanExport(Manifest{Format: ExportFormat, Saga: "saga-1", Files: []ManifestFile{settled}},
		[]Wanted{{ID: index, Path: "Сага.md", Text: []byte(after)}}, nil, map[string]DiskFile{"Сага.md": disk}, false)
	if got := steps(plan); !reflect.DeepEqual(got, []string{"update Сага.md"}) {
		t.Fatalf("the plan after the cut = %q", got)
	}
}
