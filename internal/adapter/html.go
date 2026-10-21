package adapter

import (
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alumican/tailshare/internal/site"
)

// An HTML page with the local files it refers to, and nothing else from its directory.
//
// References are followed in src, href, srcset, poster, data and inline styles, in <style>, and on
// into the style sheets the page uses (url(...) and @import), as long as they stay inside the page's
// directory. Files a script loads by itself (fetch, import maps, ...) are not found: serve the
// directory instead.
func htmlPage(input, _ string) (fs.FS, string, error) {
	top, err := filepath.EvalSymlinks(filepath.Dir(input))
	if err != nil {
		return nil, "", err
	}
	name := filepath.Base(input)
	// The page as seen from its directory, even if the page itself is a link to somewhere else
	page := filepath.Join(top, name)
	text, err := os.ReadFile(page)
	if err != nil {
		return nil, "", err
	}
	resolvedPage, err := filepath.EvalSymlinks(page)
	if err != nil {
		return nil, "", err
	}

	files := site.Files{name: {Path: resolvedPage}}
	var follow func(referrer string, references []string)
	follow = func(referrer string, references []string) {
		for _, reference := range references {
			target, ok := resolve(top, referrer, reference)
			if !ok {
				continue
			}
			key := filepath.ToSlash(strings.TrimPrefix(target, top+string(filepath.Separator)))
			if _, done := files[key]; done {
				continue
			}
			files[key] = site.Entry{Path: target}
			if strings.EqualFold(filepath.Ext(target), ".css") {
				if css, err := os.ReadFile(target); err == nil {
					follow(target, cssReferences(string(css)))
				}
			}
		}
	}
	follow(page, htmlReferences(string(text)))
	return files, "/" + name, nil
}

// The local file a reference points to, if it is a file inside top
func resolve(top, referrer, reference string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
		return "", false
	}
	var target string
	if strings.HasPrefix(parsed.Path, "/") {
		target = filepath.Join(top, filepath.FromSlash(parsed.Path))
	} else {
		target = filepath.Join(filepath.Dir(referrer), filepath.FromSlash(parsed.Path))
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil || !strings.HasPrefix(target, top+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(target)
	return target, err == nil && info.Mode().IsRegular()
}

// MARK: - Finding references

var cssReference = regexp.MustCompile(`url\(\s*['"]?([^'")]+?)['"]?\s*\)|@import\s+['"]([^'"]+)['"]`)

func cssReferences(text string) []string {
	var found []string
	for _, match := range cssReference.FindAllStringSubmatch(text, -1) {
		found = append(found, match[1]+match[2])
	}
	return found
}

// The references in a page's tags and styles. This is a scanner, not a full HTML parser: it reads
// tags and their attributes, the contents of <style>, and skips comments and the contents of <script>.
func htmlReferences(text string) []string {
	var found []string
	for i := 0; i < len(text); {
		start := strings.IndexByte(text[i:], '<')
		if start < 0 {
			break
		}
		i += start
		rest := text[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			i = skipPast(text, i+4, "-->")
			continue
		case len(rest) < 2 || !isLetter(rest[1]):
			// A declaration (<!doctype>, <?xml>), an end tag, or a lone <
			i = skipPast(text, i+1, ">")
			continue
		}
		tag, attributes, end := scanTag(text, i+1)
		i = end
		for _, attribute := range attributes {
			switch attribute[0] {
			case "src", "href", "poster", "data":
				found = append(found, attribute[1])
			case "srcset", "imagesrcset":
				for _, candidate := range strings.Split(attribute[1], ",") {
					if fields := strings.Fields(candidate); len(fields) > 0 {
						found = append(found, fields[0])
					}
				}
			case "style":
				found = append(found, cssReferences(attribute[1])...)
			}
		}
		switch tag {
		case "style":
			close := indexFold(text[i:], "</style")
			if close < 0 {
				close = len(text) - i
			}
			found = append(found, cssReferences(text[i:i+close])...)
			i += close
		case "script":
			if close := indexFold(text[i:], "</script"); close >= 0 {
				i += close
			} else {
				i = len(text)
			}
		}
	}
	return found
}

// Read a start tag from just after its <: its name and attributes (names lower-cased, values
// unescaped), and where the text after the tag starts
func scanTag(text string, i int) (tag string, attributes [][2]string, end int) {
	start := i
	for i < len(text) && !isSpace(text[i]) && text[i] != '>' && text[i] != '/' {
		i++
	}
	tag = strings.ToLower(text[start:i])
	for i < len(text) {
		for i < len(text) && (isSpace(text[i]) || text[i] == '/') {
			i++
		}
		if i >= len(text) || text[i] == '>' {
			break
		}
		start := i
		for i < len(text) && !isSpace(text[i]) && text[i] != '=' && text[i] != '>' && text[i] != '/' {
			i++
		}
		name := strings.ToLower(text[start:i])
		for i < len(text) && isSpace(text[i]) {
			i++
		}
		value := ""
		if i < len(text) && text[i] == '=' {
			i++
			for i < len(text) && isSpace(text[i]) {
				i++
			}
			if i < len(text) && (text[i] == '"' || text[i] == '\'') {
				quote := text[i]
				close := strings.IndexByte(text[i+1:], quote)
				if close < 0 {
					close = len(text) - i - 1
				}
				value = text[i+1 : i+1+close]
				i += close + 2
			} else {
				start := i
				for i < len(text) && !isSpace(text[i]) && text[i] != '>' {
					i++
				}
				value = text[start:i]
			}
		}
		if name != "" {
			attributes = append(attributes, [2]string{name, html.UnescapeString(value)})
		} else {
			i++
		}
	}
	return tag, attributes, min(i+1, len(text))
}

func skipPast(text string, i int, marker string) int {
	if found := strings.Index(text[min(i, len(text)):], marker); found >= 0 {
		return i + found + len(marker)
	}
	return len(text)
}

// Where an ASCII marker (in lower case) starts in text, in any case; -1 when it is not there
func indexFold(text, marker string) int {
	for i := 0; i+len(marker) <= len(text); i++ {
		j := 0
		for j < len(marker) && (text[i+j] == marker[j] || text[i+j]|0x20 == marker[j] && isLetter(marker[j])) {
			j++
		}
		if j == len(marker) {
			return i
		}
	}
	return -1
}

func isLetter(c byte) bool { return 'a' <= c|0x20 && c|0x20 <= 'z' }
func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
