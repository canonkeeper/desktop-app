package library

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

// epubLink is a link of the file to a place of one of its documents: the path in the archive and the fragment.
type epubLink struct {
	path     string
	fragment string
}

// resolveEPUBLink resolves a link written in the document at base. A link to another site, out of the archive or to
// nothing is not one of the book: it is only looked up among the documents of the book, never read.
func resolveEPUBLink(base, href string) (epubLink, bool) {
	target, fragment, _ := strings.Cut(strings.TrimSpace(href), "#")
	if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	if unescaped, err := url.PathUnescape(fragment); err == nil {
		fragment = unescaped
	}
	if target == "" {
		return epubLink{path: base, fragment: fragment}, fragment != ""
	}
	if strings.ContainsAny(target, "\\\x00:") || path.IsAbs(target) {
		return epubLink{}, false
	}
	resolved, ok := normalizeEPUBArchivePath(path.Join(path.Dir(base), target))
	return epubLink{path: resolved, fragment: fragment}, ok
}

// epubTypes are the words of the epub:type of an element, in lower case.
func epubTypes(attributes []xml.Attr) []string {
	for _, attribute := range attributes {
		if attribute.Name.Local == "type" && attribute.Name.Space != "" {
			return strings.Fields(strings.ToLower(attribute.Value))
		}
	}
	return nil
}

func epubAttribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if attribute.Name.Local == name && attribute.Name.Space == "" {
			return attribute.Value
		}
	}
	return ""
}

// epubContents is what the file says of its own documents (specs/2026-11-10-ux1-names.md N7.1, N7.3): the name its
// contents give a document — of the first entry leading into it, empty when that name cannot be a chapter title — and
// which documents are pages of the publisher, not chapters.
type epubContents struct {
	labels  map[string]string
	service map[string]bool
}

// epubContentsEntry is an entry of the contents or a landmark: where it leads, its words, and what kind of page the
// file calls it.
type epubContentsEntry struct {
	link  epubLink
	label string
	kinds []string
}

// A document the file calls one of these is a page of the publisher; one it also calls the text of the book stays.
var (
	epubServiceKinds = map[string]bool{"cover": true, "titlepage": true, "title-page": true, "toc": true, "copyright-page": true, "colophon": true, "imprint": true}
	epubTextKinds    = map[string]bool{"bodymatter": true, "text": true}
)

// readEPUBContents reads the navigation document of EPUB 3, otherwise the NCX of EPUB 2, and the landmarks and the
// guide. A file without them, or with broken ones, is imported as before: nothing here fails the import (N7.4).
func readEPUBContents(files map[string]*zip.File, opfPath string, opf epubOPF, budget *epubReadBudget) epubContents {
	contents := epubContents{labels: map[string]string{}, service: map[string]bool{}}
	var navPath, ncxPath string
	for _, item := range opf.Manifest.Items {
		itemPath, err := resolveEPUBHref(opfPath, item.Href)
		if err != nil {
			continue
		}
		switch {
		case navPath == "" && strings.Contains(" "+item.Properties+" ", " nav "):
			navPath = itemPath
		case ncxPath == "" && (item.MediaType == "application/x-dtbncx+xml" || item.ID != "" && item.ID == opf.Spine.Toc):
			ncxPath = itemPath
		}
	}
	read := func(name string) []byte {
		data, err := readEPUBFile(files, name, budget)
		if err != nil {
			return nil
		}
		return data
	}
	var entries, landmarks []epubContentsEntry
	if navPath != "" {
		// The navigation document is the contents of the book, not a chapter of it, even in the spine.
		contents.service[navPath] = true
		entries, landmarks = readEPUBNav(read(navPath), navPath)
	}
	if len(entries) == 0 && ncxPath != "" {
		entries = readEPUBNCX(read(ncxPath), ncxPath)
	}
	for _, entry := range entries {
		if _, named := contents.labels[entry.link.path]; named {
			continue
		}
		label := normalizeImportText(entry.label)
		if utf8.RuneCountInString(label) > maxImportedChapterTitleLength {
			label = ""
		}
		contents.labels[entry.link.path] = label
	}
	for _, reference := range opf.Guide {
		if link, ok := resolveEPUBLink(opfPath, reference.Href); ok {
			landmarks = append(landmarks, epubContentsEntry{link: link, kinds: strings.Fields(strings.ToLower(reference.Type))})
		}
	}
	text := map[string]bool{}
	for _, landmark := range landmarks {
		for _, kind := range landmark.kinds {
			text[landmark.link.path] = text[landmark.link.path] || epubTextKinds[kind]
		}
	}
	for _, landmark := range landmarks {
		for _, kind := range landmark.kinds {
			if epubServiceKinds[kind] && !text[landmark.link.path] {
				contents.service[landmark.link.path] = true
			}
		}
	}
	return contents
}

// readEPUBNav reads the links of the contents (`nav epub:type="toc"`) and of the landmarks of a navigation document,
// in order; what a broken document gave before it broke is kept.
func readEPUBNav(data []byte, base string) (entries, landmarks []epubContentsEntry) {
	reader := newImportXMLReader(bytes.NewReader(data))
	// part is the nav being read: "toc", "landmarks" or none; depth counts the elements open inside it.
	part, depth := "", 0
	var link *epubContentsEntry
	linkDepth := 0
	for {
		token, err := reader.Token()
		if err != nil {
			return entries, landmarks
		}
		switch token := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(token.Name.Local)
			if part == "" {
				if name == "nav" {
					for _, kind := range epubTypes(token.Attr) {
						if kind == "toc" || kind == "landmarks" {
							part, depth = kind, 1
						}
					}
				}
				continue
			}
			depth++
			if link == nil && name == "a" {
				if target, ok := resolveEPUBLink(base, epubAttribute(token.Attr, "href")); ok {
					link, linkDepth = &epubContentsEntry{link: target, kinds: epubTypes(token.Attr)}, depth
				}
			}
		case xml.CharData:
			if link != nil {
				link.label += string(token)
			}
		case xml.EndElement:
			if part == "" {
				continue
			}
			if link != nil && depth == linkDepth {
				if part == "toc" {
					entries = append(entries, *link)
				} else {
					landmarks = append(landmarks, *link)
				}
				link = nil
			}
			if depth--; depth == 0 {
				part = ""
			}
		}
	}
}

// readEPUBNCX reads the entries of the contents of EPUB 2: the label of each navPoint and where it leads, in order.
func readEPUBNCX(data []byte, base string) []epubContentsEntry {
	reader := newImportXMLReader(bytes.NewReader(data))
	var entries []epubContentsEntry
	// labels are the labels of the open navPoints, the innermost last.
	var labels []string
	inLabel, inText := false, false
	for {
		token, err := reader.Token()
		if err != nil {
			return entries
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "navPoint":
				labels = append(labels, "")
			case "navLabel":
				inLabel = true
			case "text":
				inText = inLabel
			case "content":
				if len(labels) == 0 {
					continue
				}
				if link, ok := resolveEPUBLink(base, epubAttribute(token.Attr, "src")); ok {
					entries = append(entries, epubContentsEntry{link: link, label: labels[len(labels)-1]})
				}
			}
		case xml.CharData:
			if inText && len(labels) > 0 {
				labels[len(labels)-1] += string(token)
			}
		case xml.EndElement:
			switch token.Name.Local {
			case "navPoint":
				if len(labels) > 0 {
					labels = labels[:len(labels)-1]
				}
			case "navLabel":
				inLabel = false
			case "text":
				inText = false
			}
		}
	}
}
