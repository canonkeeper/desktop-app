// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"testing"
)

// entry is a record of a file a test packs by hand.
type entry struct {
	name   string
	body   []byte
	method uint16
	flags  uint16
	mode   fs.FileMode
}

// unpack lists the records of a file.
func unpack(t *testing.T, file []byte) []entry {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	var entries []entry
	for _, item := range reader.File {
		source, err := item.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(source)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry{name: item.Name, body: body, method: zip.Deflate})
	}
	return entries
}

// pack writes the records; with sign the manifest names the SHA-256 of every record but those in unsigned.
func pack(t *testing.T, entries []entry, sign bool, unsigned ...string) []byte {
	t.Helper()
	var manifest map[string]any
	for _, item := range entries {
		if item.name == EntryManifest {
			if err := json.Unmarshal(item.body, &manifest); err != nil {
				t.Fatal(err)
			}
		}
	}
	if sign && manifest != nil {
		files := map[string]string{}
		for _, item := range entries {
			if item.name == EntryManifest || strings.Contains(strings.Join(unsigned, "\n"), item.name) {
				continue
			}
			sum := sha256.Sum256(item.body)
			files[item.name] = hex.EncodeToString(sum[:])
		}
		manifest["files"] = files
	}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, item := range entries {
		body := item.body
		if item.name == EntryManifest && sign && manifest != nil {
			var err error
			if body, err = json.Marshal(manifest); err != nil {
				t.Fatal(err)
			}
		}
		header := &zip.FileHeader{Name: item.name, Method: item.method, Flags: item.flags}
		if item.mode != 0 {
			header.SetMode(item.mode)
		}
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// changed returns the records with the body of one record changed.
func changed(entries []entry, name string, change func([]byte) []byte) []entry {
	out := make([]entry, len(entries))
	copy(out, entries)
	for index := range out {
		if out[index].name == name {
			out[index].body = change(append([]byte(nil), out[index].body...))
		}
	}
	return out
}

func replaced(old, new string) func([]byte) []byte {
	return func(body []byte) []byte { return bytes.Replace(body, []byte(old), []byte(new), 1) }
}

func withEntry(entries []entry, extra entry) []entry {
	out := append([]entry(nil), entries[:len(entries)-1]...)
	return append(out, extra, entries[len(entries)-1])
}

func manifestField(field string, value any) func([]byte) []byte {
	return func(body []byte) []byte {
		var manifest map[string]any
		_ = json.Unmarshal(body, &manifest)
		manifest[field] = value
		out, _ := json.Marshal(manifest)
		return out
	}
}

// specs/2026-11-13-desk-sagafile.md SF3.1, SF3.2: an evil file is refused with its own code, and no other: the container, the
// names, the sizes, the manifest and the SHA-256, the schemas, the refs and the limits of the tables.
func TestTheReaderRefusesEvilFiles(t *testing.T) {
	good := newFixture(t).write(t, nil)
	base := unpack(t, good)
	zeros := make([]byte, 10<<20)
	cases := map[string]struct {
		file func() []byte
		code string
	}{
		"not a zip": {func() []byte {
			return []byte("Глава 1. Перевал\nМара поднялась к перевалу.")
		}, CodeNotArchive},
		"another format":   {func() []byte { return pack(t, changed(base, EntryManifest, manifestField("format", "other")), true) }, CodeNotArchive},
		"../ in a name":    {func() []byte { return pack(t, withEntry(base, entry{name: "../saga.json", body: []byte("{}")}), true) }, CodeInvalid},
		"an absolute path": {func() []byte { return pack(t, withEntry(base, entry{name: "/saga.json", body: []byte("{}")}), true) }, CodeInvalid},
		"a link": {func() []byte {
			return pack(t, withEntry(base, entry{name: "covers/002.png", body: []byte("covers/001.png"), mode: fs.ModeSymlink | 0o777}), true)
		}, CodeInvalid},
		"a directory": {func() []byte { return pack(t, withEntry(base, entry{name: "covers/", mode: fs.ModeDir | 0o755}), true) }, CodeInvalid},
		"a ciphered record": {func() []byte {
			return pack(t, withEntry(base, entry{name: "workshops/writing/x.jsonl", body: []byte("x"), flags: 1}), true)
		}, CodeInvalid},
		"a record not of the table": {func() []byte { return pack(t, withEntry(base, entry{name: "extra.txt", body: []byte("x")}), true) }, CodeInvalid},
		"a name twice":              {func() []byte { return pack(t, withEntry(base, entry{name: EntrySaga, body: []byte("{}")}), true) }, CodeInvalid},
		"a name in another case":    {func() []byte { return pack(t, withEntry(base, entry{name: "Saga.json", body: []byte("{}")}), true) }, CodeInvalid},
		"a record without its line in files": {func() []byte {
			return pack(t, withEntry(base, entry{name: "workshops/writing/x.jsonl", body: []byte("x")}), true, "workshops/writing/x.jsonl")
		}, CodeDamaged},
		"10 001 records in the end record": {func() []byte { return withEndCount(t, good, 10_001) }, CodeTooLarge},
		"a record that unpacks 1:1000": {func() []byte {
			return pack(t, withEntry(base, entry{name: "workshops/writing/x.jsonl", body: zeros, method: zip.Deflate}), true)
		}, CodeTooLarge},
		"a header that lies of the size": {func() []byte { return withCentralSize(t, good, EntrySaga, 10) }, CodeDamaged},
		"a SHA-256 that does not match": {func() []byte {
			return pack(t, changed(base, EntrySaga, replaced("Северный", "Южный")), false)
		}, CodeDamaged},
		"no manifest": {func() []byte { return pack(t, base[:len(base)-1], false) }, CodeDamaged},
		"cut short":   {func() []byte { return good[:len(good)-30] }, CodeDamaged},
		"an extra field of JSON": {func() []byte {
			return pack(t, changed(base, EntrySaga, replaced(`{"name"`, `{"extra":1,"name"`)), true)
		}, CodeInvalid},
		"a ref twice": {func() []byte {
			return pack(t, changed(base, EntryBooks, func(body []byte) []byte {
				return append(body, bytes.ReplaceAll(bytes.SplitN(body, []byte("\n"), 2)[0], []byte(`"order":2`), []byte(`"order":7`))...)
			}), true)
		}, CodeInvalid},
		"a UUID not canonical": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced(chapterSix, strings.ToUpper(chapterSix))), true)
		}, CodeInvalid},
		"version 2": {func() []byte { return pack(t, changed(base, EntryManifest, manifestField("version", 2)), true) }, CodeNewerVersion},
		"a zero in a text": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("Маяк горел", `Маяк\u0000горел`)), true)
		}, CodeInvalid},
		"bytes not of UTF-8": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("Маяк горел", "Маяк\xff\xfeгорел")), true)
		}, CodeInvalid},
		"a sign of control in a paragraph": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("Маяк горел", `Маяк\u0001горел`)), true)
		}, CodeInvalid},
		"a lost sign in a paragraph": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("Маяк горел", `Маяк\uFFFDгорел`)), true)
		}, CodeInvalid},
		"Markdown out of the subset": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("Маяк горел", "Маяк**горел")), true)
		}, CodeInvalid},
		"an order of a paragraph twice": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced(`"order":2,"kind"`, `"order":1,"kind"`)), true)
		}, CodeInvalid},
		"bookKeyNumbers 2147483647": {func() []byte {
			return pack(t, changed(base, EntrySaga, replaced(`"bookKeyNumbers":3`, `"bookKeyNumbers":2147483647`)), true)
		}, CodeInvalid},
		"a card of a chapter the file does not have": {func() []byte {
			return pack(t, changed(base, EntryCards, replaced(chapterOne, "aaaaaaaa-0000-4000-8000-000000000009")), true)
		}, CodeInvalid},
		"a second card of a chapter": {func() []byte {
			return pack(t, changed(base, EntryCards, func(body []byte) []byte { return append(body, bytes.SplitN(body, []byte("\n"), 2)[0]...) }), true)
		}, CodeInvalid},
		"a name of a material twice": {func() []byte {
			return pack(t, changed(base, EntryMaterials, func(body []byte) []byte {
				return append(body, bytes.ReplaceAll(body, []byte("Северный маяк"), []byte("северный  маяк"))...)
			}), true)
		}, CodeInvalid},
		"a JSONL line over the limit": {func() []byte {
			lineLimit = 256
			return good
		}, CodeTooLarge},
		// Two names of the table that differ only in case: each passes the rule of names, the pair does not.
		"two records whose names differ in case": {func() []byte {
			return pack(t, withEntry(withEntry(base, entry{name: "workshops/writing/x.jsonl", body: []byte("{}\n")}),
				entry{name: "workshops/writing/X.jsonl", body: []byte("{}\n")}), true)
		}, CodeInvalid},
		"a key number of a book twice": {func() []byte {
			return pack(t, changed(base, EntryBooks, replaced(`"keyNumber":3`, `"keyNumber":1`)), true)
		}, CodeInvalid},
		"a place of a book twice": {func() []byte {
			return pack(t, changed(base, EntryBooks, replaced(`"order":1,"title":"Маяк"`, `"order":2,"title":"Маяк"`)), true)
		}, CodeInvalid},
		"a key number over the counter of the saga": {func() []byte {
			return pack(t, changed(base, EntrySaga, replaced(`"bookKeyNumbers":3`, `"bookKeyNumbers":2`)), true)
		}, CodeInvalid},
		"a chapter of a book the file does not have": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced(`"book":"`+bookTwo+`"`, `"book":"99999999-9999-4999-8999-999999999999"`)), true)
		}, CodeInvalid},
		"a record of the memory about a chapter the file does not have": {func() []byte {
			return pack(t, changed(base, EntryMemory, replaced(chapterTwo, "aaaaaaaa-0000-4000-8000-000000000009")), true)
		}, CodeInvalid},
		"a note of a chapter the file does not have": {func() []byte {
			return pack(t, changed(base, EntryNotes, replaced(chapterTwo, "aaaaaaaa-0000-4000-8000-000000000009")), true)
		}, CodeInvalid},
		"an id of a paragraph twice": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced("bbbbbbbb-0000-4000-8000-000000000002", "bbbbbbbb-0000-4000-8000-000000000001")), true)
		}, CodeInvalid},
		"an order of a paragraph below one": {func() []byte {
			return pack(t, changed(base, EntryChapters, replaced(`"order":1,"kind"`, `"order":0,"kind"`)), true)
		}, CodeInvalid},
		"a decision about the map twice": {func() []byte {
			return pack(t, changed(base, EntryMapHidden, func(body []byte) []byte { return append(body, body...) }), true)
		}, CodeInvalid},
		// The base keeps no zero in a text, escaped or not (SF3.1): a card that carries one is refused, never written.
		"a zero escaped in a card": {func() []byte {
			return pack(t, changed(base, EntryCards, replaced("Мара идёт через", `Мара\u0000идёт через`)), true)
		}, CodeInvalid},
		"a card built from its own text": {func() []byte {
			card := newFixture(t).cards[0]
			return pack(t, changed(base, EntryCards, replaced(`"model":"cards-model","card"`,
				`"model":"cards-model","builtFrom":"`+card.ContentHash+`","card"`)), true)
		}, CodeInvalid},
		"turns of other members over an integer": {func() []byte {
			return pack(t, changed(base, EntryChats, replaced(`"othersTurns":2`, `"othersTurns":3000000000`)), true)
		}, CodeInvalid},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() { lineLimit = MaxLineBytes }()
			file := item.file()
			if _, err := read(t, file); code(err) != item.code {
				t.Fatalf("%s = %v; want %s", name, err, item.code)
			}
		})
	}
}

// withEndCount says in the end record of the archive that it has so many records.
func withEndCount(t *testing.T, file []byte, count uint16) []byte {
	t.Helper()
	out := append([]byte(nil), file...)
	at := bytes.LastIndex(out, []byte("PK\x05\x06"))
	if at < 0 {
		t.Fatal("no end record")
	}
	binary.LittleEndian.PutUint16(out[at+8:], count)
	binary.LittleEndian.PutUint16(out[at+10:], count)
	return out
}

// withCentralSize says in the directory that a record unpacks into fewer bytes than it does.
func withCentralSize(t *testing.T, file []byte, name string, size uint32) []byte {
	t.Helper()
	out := append([]byte(nil), file...)
	for at := 0; ; {
		next := bytes.Index(out[at:], []byte("PK\x01\x02"))
		if next < 0 {
			t.Fatalf("no record %s in the directory", name)
		}
		at += next
		nameLength := int(binary.LittleEndian.Uint16(out[at+28:]))
		if string(out[at+46:at+46+nameLength]) == name {
			binary.LittleEndian.PutUint32(out[at+24:], size)
			return out
		}
		at += 4
	}
}

// SF3.10: a record of a workshop this build does not read is kept in the list of sections, the file opens.
func TestARecordOfAWorkshopIsNamedNotRead(t *testing.T) {
	base := unpack(t, newFixture(t).write(t, nil))
	archive, err := read(t, pack(t, withEntry(base, entry{name: "workshops/writing/x.jsonl", body: []byte("{}\n"), method: zip.Deflate}), true))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Sections) != 1 || archive.Sections[0] != "workshops/writing/x.jsonl" {
		t.Fatalf("the sections = %v", archive.Sections)
	}
}
