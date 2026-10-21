package adapter

import (
	"archive/zip"
	"embed"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alumican/tailshare/internal/plist"
	"github.com/alumican/tailshare/internal/site"
)

//go:embed templates
var templates embed.FS

// An install page for an iOS app: open it in Safari on an iPhone and tap Install.
// iOS installs only IPAs signed for development or ad hoc, on devices registered for them.
func ipa(input, base string) (fs.FS, string, error) {
	info, err := appInfo(input)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s (%v)", ErrNotAnApp, input, err)
	}
	text := func(key string) string {
		value, _ := info[key].(string)
		return value
	}
	if text("CFBundleIdentifier") == "" {
		return nil, "", fmt.Errorf("%w: %s (no CFBundleIdentifier)", ErrNotAnApp, input)
	}
	stat, err := os.Stat(input)
	if err != nil {
		return nil, "", err
	}

	name := filepath.Base(input)
	title := text("CFBundleDisplayName")
	if title == "" {
		title = text("CFBundleName")
	}
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	version := text("CFBundleShortVersionString")
	modified := stat.ModTime()
	// The modification time busts the device's cache, so a rebuilt IPA is always downloaded
	ipaURL := fmt.Sprintf("%s/%s?v=%d", base, url.PathEscape(name), modified.Unix())
	manifestURL := base + "/manifest.plist"

	manifest, err := fill("manifest.plist", map[string]string{
		"ipa_url":   xmlEscape(ipaURL),
		"bundle_id": xmlEscape(text("CFBundleIdentifier")),
		"version":   xmlEscape(version),
		"title":     xmlEscape(title),
	})
	if err != nil {
		return nil, "", err
	}
	page, err := fill("index.html", map[string]string{
		"title":       html.EscapeString(title),
		"version":     html.EscapeString(version),
		"built":       modified.Local().Format(time.DateTime),
		"install_url": html.EscapeString("itms-services://?action=download-manifest&url=" + manifestURL),
	})
	if err != nil {
		return nil, "", err
	}
	return site.Files{
		name:             {Path: input},
		"index.html":     {Data: page},
		"manifest.plist": {Data: manifest},
	}, "/", nil
}

// The app's Info.plist: Payload/<name>.app/Info.plist inside the archive
func appInfo(input string) (map[string]any, error) {
	archive, err := zip.OpenReader(input)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if !strings.HasPrefix(entry.Name, "Payload/") || strings.Count(entry.Name, "/") != 2 || !strings.HasSuffix(entry.Name, ".app/Info.plist") {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		// An Info.plist is a few kilobytes; refuse anything absurd
		data, err := io.ReadAll(io.LimitReader(reader, 16<<20))
		if err != nil {
			return nil, err
		}
		value, err := plist.Decode(data)
		if err != nil {
			return nil, err
		}
		info, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Info.plist is not a dictionary")
		}
		return info, nil
	}
	return nil, fmt.Errorf("no Payload/<name>.app/Info.plist")
}

// A template with ${name} replaced by its value
func fill(template string, values map[string]string) ([]byte, error) {
	text, err := templates.ReadFile("templates/" + template)
	if err != nil {
		return nil, err
	}
	return []byte(os.Expand(string(text), func(name string) string { return values[name] })), nil
}

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
