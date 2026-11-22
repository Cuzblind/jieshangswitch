package clientruntime

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var storeMirrorRowPattern = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
var storeMirrorLinkPattern = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']+)["'][^>]*>\s*([^<]+)\s*</a>`)
var sha1Pattern = regexp.MustCompile(`(?i)\b[0-9a-f]{40}\b`)

func allowedMicrosoftCDNHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "dl.delivery.mp.microsoft.com", "tlu.dl.delivery.mp.microsoft.com":
		return true
	default:
		return false
	}
}

func msixIdentityAllowed(name, agent string) bool {
	switch agent {
	case "claude":
		// Current Microsoft Store package identity. Keep the old name for
		// already-published test packages and older Store revisions.
		return name == "AnthropicPBC.Claude" || name == "Claude"
	case "codex":
		return name == "OpenAI.Codex"
	default:
		return false
	}
}

// Exact publishers read from the vendors' real packages on 2026-11-01. Store
// signing gives a GUID; the Claude direct package keeps its EV subject. A
// substring match would accept any trusted certificate naming the vendor, and
// this identity decides what is deployed under an elevated token.
var msixPublishers = map[string][]string{
	"claude": {
		"cn=0ed83b57-1fa9-465c-98f8-41f190d0df7f",
		`cn="anthropic, pbc", o="anthropic, pbc", l=san francisco, s=california, c=us, serialnumber=4860621, oid.2.5.4.15=private organization, oid.1.3.6.1.4.1.311.60.2.1.2=delaware, oid.1.3.6.1.4.1.311.60.2.1.3=us`,
	},
	"codex": {"cn=50bdfd77-8903-4850-9ffe-6e8522f64d5b"},
}

func msixPublisherAllowed(publisher, agent string) bool {
	p := strings.ToLower(strings.TrimSpace(publisher))
	for _, allowed := range msixPublishers[agent] {
		if p == allowed {
			return true
		}
	}
	return false
}

// parseStoreMirrorMSIX parses the temporary links returned by
// store.rg-adguard.net. The converter is only a metadata relay; the package
// must still come from Microsoft's CDN and pass the MSIX identity/signature
// checks before installation.
func parseStoreMirrorMSIX(body []byte, arch string) (guiRelease, error) {
	archToken := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	if archToken == "" {
		return guiRelease{}, errors.New("不支持的桌面版架构")
	}
	for _, rowMatch := range storeMirrorRowPattern.FindAllSubmatch(body, -1) {
		row := rowMatch[1]
		link := storeMirrorLinkPattern.FindSubmatch(row)
		if len(link) != 3 {
			continue
		}
		name := strings.TrimSpace(html.UnescapeString(string(link[2])))
		lowerName := strings.ToLower(name)
		if !strings.HasSuffix(lowerName, ".msix") || !strings.Contains(lowerName, "_"+strings.ToLower(archToken)+"_") {
			continue
		}
		rawURL := strings.TrimSpace(html.UnescapeString(string(link[1])))
		u, err := url.Parse(rawURL)
		if err != nil || u.User != nil || u.Fragment != "" {
			continue
		}
		fallbackURL := ""
		// The converter currently emits http CDN links. Prefer HTTPS, but
		// retain the exact Microsoft CDN URL for networks whose TLS path is
		// blocked. The package is still checked by hash and MSIX signature.
		if allowedMicrosoftCDNHTTP(rawURL) {
			fallbackURL = rawURL
		}
		if u.Scheme == "http" {
			u.Scheme = "https"
		}
		if !allowedDownloadURL(u.String()) {
			continue
		}
		hashes := sha1Pattern.FindAllString(string(row), -1)
		sha1sum := ""
		if len(hashes) > 0 {
			sha1sum = strings.ToLower(hashes[len(hashes)-1])
		}
		return guiRelease{URL: u.String(), FallbackURL: fallbackURL, SHA1: sha1sum, Format: "msix"}, nil
	}
	return guiRelease{}, errors.New("微软商店直连源没有返回适合当前架构的官方安装包")
}

func verifyFileSHA1(path, expected string) error {
	if expected == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha1.New()
	if _, err = io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return errors.New("微软商店安装包校验失败")
	}
	return nil
}

type msixIdentity struct {
	Name      string `xml:"Name,attr"`
	Publisher string `xml:"Publisher,attr"`
	Version   string `xml:"Version,attr"`
	Arch      string `xml:"ProcessorArchitecture,attr"`
}

func readMSIXIdentity(files []*zip.File) (msixIdentity, error) {
	for _, f := range files {
		if f.Name != "AppxManifest.xml" {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return msixIdentity{}, e
		}
		defer r.Close()
		var doc struct {
			Identity msixIdentity `xml:"Identity"`
		}
		if e = xml.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&doc); e != nil {
			return msixIdentity{}, e
		}
		return doc.Identity, nil
	}
	return msixIdentity{}, errors.New("桌面版安装包缺少官方应用清单")
}

func checkMSIXIdentity(identity msixIdentity, agent, arch string) error {
	expected := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	if !msixIdentityAllowed(identity.Name, agent) || !msixPublisherAllowed(identity.Publisher, agent) || identity.Arch != expected {
		return errors.New("桌面版安装包身份或架构不匹配")
	}
	return nil
}

func validateMSIX(file, agent, arch string) error {
	z, e := zip.OpenReader(file)
	if e != nil {
		return errors.New("官方安装包格式无效")
	}
	defer z.Close()
	identity, e := readMSIXIdentity(z.File)
	if e != nil {
		return e
	}
	return checkMSIXIdentity(identity, agent, arch)
}
