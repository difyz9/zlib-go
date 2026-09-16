package annas

import (
	"bytes"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// node is a thin wrapper over golang.org/x/net/html.Node exposing only the
// handful of operations the search scraper needs. The underlying tree is
// already parsed; this just avoids threading *html.Node everywhere.
type node struct{ n *html.Node }

func parseHTML(body []byte) (*node, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return &node{n: doc}, nil
}

func wrap(n *html.Node) *node {
	if n == nil {
		return nil
	}
	return &node{n: n}
}

func (x *node) parent() *node {
	if x == nil || x.n == nil {
		return nil
	}
	return wrap(x.n.Parent)
}

func (x *node) isElement(tag string) bool {
	return x != nil && x.n != nil && x.n.Type == html.ElementNode && x.n.Data == tag
}

// attr returns the value of the named attribute, or "".
func (x *node) attr(key string) string {
	if x == nil || x.n == nil {
		return ""
	}
	for _, a := range x.n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// classMatches reports whether the node's class attribute contains a substring.
// Anna's Archive uses generated Tailwind classes that change between deploys,
// so class-name selectors are matched by substring rather than equality — and
// the genuinely stable hooks (the mdi icon markers) are preferred elsewhere.
func (x *node) classMatches(substr string) bool {
	return strings.Contains(x.attr("class"), substr)
}

// text returns the concatenated text of the node's subtree, trimmed.
func (x *node) text() string {
	if x == nil || x.n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(x.n)
	return strings.TrimSpace(b.String())
}

// findAll returns every node in the subtree satisfying pred, in document order.
func findAll(root *node, pred func(*node) bool) []*node {
	if root == nil || root.n == nil {
		return nil
	}
	var out []*node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		w := wrap(n)
		if pred(w) {
			out = append(out, w)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root.n)
	return out
}

// iconText reads the text of the anchor wrapping an icon span.
//
// Author and publisher lines carry semantic icon markers (mdi--user-edit,
// mdi--company). Those class names have survived every deploy observed, whereas
// the surrounding layout classes have not, which makes the icon the stable
// place to hang the lookup on.
func iconText(scope *node, iconClass string) string {
	spans := findAll(scope, func(n *node) bool {
		return n.isElement("span") && n.classMatches(iconClass)
	})
	for _, span := range spans {
		// The rendered value sits in the anchor that contains the icon.
		for p := span.parent(); p != nil; p = p.parent() {
			if p.isElement("a") {
				if t := p.text(); t != "" {
					return t
				}
				break
			}
		}
	}
	return ""
}

// The metadata strip on an Anna's result renders as "·"-separated segments:
//
//	English [en] · PDF · 5.6MB · 2008 · 📕 Book (fiction) · 🚀/lgli/nexusstc/zlib
//
// Segments are optional and their order is not guaranteed — year is absent from
// about 9% of results. The strip is therefore matched by pattern, never by
// index: positional parsing silently shifts year into extension on the records
// that omit it.
var (
	sizeRe = regexp.MustCompile(`(?i)^\d+(?:\.\d+)?\s*[KMGT]B$`)
	// Any plausible four-digit year, deliberately including pre-1500 ones:
	// incunabula carry dates like 1492. Widening is safe because size carries a
	// unit suffix, content type carries parentheses, and extension must start
	// with a letter, so nothing else can look like a bare four-digit number.
	yearRe = regexp.MustCompile(`^(?:1[0-9]\d{2}|20\d{2})$`)
	// Extension must START with a letter. A loose [A-Z0-9]{2,5} would also
	// match a four-digit year, which is exactly the corruption to avoid.
	extRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,6}$`)
	langRe = regexp.MustCompile(`\[([a-z]{2,3})\]`)
)

// findMetadata locates and parses the metadata strip near a title anchor.
//
// The strip sits somewhere above the anchor, at a depth that has varied between
// deploys, so a few ancestor levels are tried rather than one hard-coded parent.
func findMetadata(link *node) map[string]string {
	for depth, host := 0, link.parent(); host != nil && depth < 3; depth, host = depth+1, host.parent() {
		if strip := locateStrip(host); strip != "" {
			if parsed := parseStrip(strip); len(parsed) > 0 {
				return parsed
			}
		}
	}
	return map[string]string{}
}

// locateStrip returns the first text node in scope that looks like a metadata
// strip, identified by content rather than by class name.
//
// Recognition must not hinge on any single segment: requiring a size segment
// discarded the whole strip for records that omit it, losing the language,
// extension, year and content type that were present.
func locateStrip(scope *node) string {
	for _, t := range findAll(scope, func(n *node) bool {
		return n.n != nil && n.n.Type == html.TextNode && strings.Contains(n.n.Data, "·")
	}) {
		candidate := strings.TrimSpace(t.n.Data)
		for _, part := range strings.Split(candidate, "·") {
			seg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "🚀"))
			if sizeRe.MatchString(seg) || extRe.MatchString(seg) || langRe.MatchString(seg) {
				return candidate
			}
		}
	}
	return ""
}

// parseStrip splits a metadata strip into whatever segments it carries.
func parseStrip(strip string) map[string]string {
	parsed := map[string]string{}
	for _, raw := range strings.Split(strip, "·") {
		// 🚀 prefixes the provenance segment ("🚀/lgli/nexusstc/zlib").
		seg := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "🚀"))
		if seg == "" {
			continue
		}
		switch {
		case parsed["year"] == "" && yearRe.MatchString(seg):
			parsed["year"] = seg
		case parsed["size"] == "" && sizeRe.MatchString(seg):
			parsed["size"] = strings.ReplaceAll(seg, " ", "")
		case parsed["language"] == "" && langRe.MatchString(seg):
			parsed["language"] = langRe.FindStringSubmatch(seg)[1]
		case parsed["extension"] == "" && extRe.MatchString(seg):
			parsed["extension"] = strings.ToLower(seg)
		case parsed["content_type"] == "" && strings.Contains(seg, "("):
			parsed["content_type"] = seg
		case parsed["provenance"] == "" && (strings.Contains(seg, "/") || seg == "zlib" || seg == "upload"):
			parsed["provenance"] = strings.Trim(seg, "/")
		}
	}
	return parsed
}
