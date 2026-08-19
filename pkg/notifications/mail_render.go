// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-present Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package notifications

import (
	"bufio"
	"bytes"
	"embed"
	"html"
	templatehtml "html/template"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	templatetext "text/template"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/i18n"
	"code.vikunja.io/api/pkg/mail"
	"code.vikunja.io/api/pkg/utils"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

const mailTemplatePlain = `
{{ .Greeting }}
{{ range $line := .IntroLines}}
{{ $line.Text }}
{{ end }}
{{ if .ActionURL }}{{ .ActionText }}:
{{ .ActionURL }}{{end}}
{{ range $line := .OutroLines}}
{{ $line.Text }}
{{ end }}
{{ range $line := .FooterLines}}
{{ $line.Text }}
{{ end }}`

const mailTemplateConversationalPlain = `
{{ if .HeaderLinePlain }}{{ .HeaderLinePlain }}
{{ end }}{{ range $line := .IntroLines}}
{{ $line.Text }}
{{ end }}
{{ if .ActionURL }}{{ .ActionText }}:
{{ .ActionURL }}{{end}}
{{ range $line := .OutroLines}}
{{ $line.Text }}
{{ end }}
{{ range $line := .FooterLines}}
{{ $line.Text }}
{{ end }}`

const mailTemplateHTML = `
<!doctype html>
<html style="width: 100%; height: 100%; padding: 0; margin: 0;">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width">
    <meta name="color-scheme" content="light dark">
    <meta name="supported-color-schemes" content="light dark">
    <style>
        :root {
            color-scheme: light dark;
        }
        @media (prefers-color-scheme: dark) {
            .email-body {
                background: #14141a !important;
            }
            .email-card {
                background: #1c1c24 !important;
                border-color: rgba(255,255,255,0.10) !important;
                color: #eceef4 !important;
            }
            .email-card p {
                color: #eceef4 !important;
            }
            .email-footer {
                border-top-color: rgba(255,255,255,0.10) !important;
                color: #a2a8bc !important;
            }
            .email-footer p {
                color: #a2a8bc !important;
            }
        }
    </style>
</head>
<body class="email-body" style="width: 100%; padding: 0; margin: 0; background: #f7f7fa">
<div style="width: 100%; font-family: Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; text-rendering: optimizeLegibility">
    <div style="width: 600px; max-width: 100%; margin: 0 auto;">
        <div style="text-align: center; margin: 24px 0;">
            <img src="cid:logo.png" style="height: 72px; width: auto; border: 0;" alt="ONE"/>
        </div>
        <div class="email-card" style="border: 1px solid rgba(35,42,69,0.09); color: #171a28; padding: 8px 28px; border-radius: 13px; background: #ffffff; font-size: 15px; line-height: 1.65;">
<p>
	{{ .Greeting }}
</p>

{{ range $line := .IntroLinesHTML}}
	{{ $line }}
{{ end }}

{{ if .ActionURL }}
	<a class="email-button" href="{{ .ActionURL }}" title="{{ .ActionText }}"
		style="display: block;text-decoration: none;border-radius: 10px;cursor: pointer;padding: 12px 16px;width: 280px;margin: 20px auto;text-align: center;white-space: nowrap;border: 0;font-size: 15px;font-weight: 600;color: #ffffff;background-color: #5744cf;background-image: linear-gradient(145deg, #4d6ae0, #5744cf);">
		{{ .ActionText }}
	</a>
{{end}}

{{ range $line := .OutroLinesHTML}}
	{{ $line }}
{{ end }}

{{ if .ActionURL }}
	<div class="email-footer" style="color: #5e6578;font-size:12px;border-top: 1px solid rgba(35,42,69,0.09);margin-top:20px;padding-top:20px;">
	<p>
		{{ .CopyURLText }}<br/>
		{{ .ActionURL }}
	</p>
{{ range $line := .FooterLinesHTML}}
	{{ $line }}
	{{ end }}
	</div>
{{ else }}
{{ if .FooterLinesHTML }}
	<div class="email-footer" style="color: #5e6578;font-size:12px;border-top: 1px solid rgba(35,42,69,0.09);margin-top:20px;padding-top:20px;">
		{{ range $line := .FooterLinesHTML }}
			{{ $line }}
		{{ end }}
	</div>
{{ end }}
{{ end }}
</div>
</div>
</div>
</body>
</html>
`

const mailTemplateConversationalHTML = `
<!doctype html>
<html style="width: 100%; height: 100%; padding: 0; margin: 0;">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width">
    <meta name="color-scheme" content="light dark">
    <meta name="supported-color-schemes" content="light dark">
    <style>
        :root {
            color-scheme: light dark;
        }
        @media (prefers-color-scheme: dark) {
            .email-body {
                background: #1c1c24 !important;
            }
            .email-header {
                color: #a2a8bc !important;
            }
            .email-text, .email-text p {
                color: #eceef4 !important;
            }
            .email-footer {
                border-top-color: rgba(255,255,255,0.10) !important;
                color: #a2a8bc !important;
            }
            .email-link {
                color: #a9b4ff !important;
            }
        }
    </style>
</head>
<body class="email-body" style="width: 100%; padding: 0; margin: 0; background: #ffffff; font-family: Inter, ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
<div style="margin: 0 auto;">

    <div style="padding: 16px 20px 0;">
        <img src="cid:logo.png" style="height: 28px; width: auto; border: 0;" alt="ONE"/>
    </div>

    {{ if .HeaderLineHTML }}
    <div class="email-header" style="padding: 12px 20px 0; color: #5e6578; font-size: 14px; line-height: 1.5;">
        {{ .HeaderLineHTML }}
    </div>
    {{ end }}

    {{ if or .IntroLinesHTML .OutroLinesHTML }}
    <div class="email-text" style="padding-left: 20px; color: #171a28; font-size: 14px; line-height: 1.5;">

        {{ range $line := .IntroLinesHTML}}
            {{ $line }}
        {{ end }}

        {{ range $line := .OutroLinesHTML}}
            {{ $line }}
        {{ end }}
    </div>
    {{ end }}

    {{ if or .ActionURL .FooterLinesHTML }}
    <div class="email-footer" style="padding: 4px 20px 8px 20px; border-top: 1px solid rgba(35,42,69,0.09); padding-top: 6px; font-size: 12px">
        {{ if .ActionURL }}
        <a class="email-link" href="{{ .ActionURL }}" style="color: #4f46d6; text-decoration: none; font-weight: 500; font-size: 12px;">
            {{ .ActionText }} →
        </a>
        {{ end }}
    	<div style="padding-top: 6px; color: #5e6578;">
    	    {{ range $line := .FooterLinesHTML }}
    	        {{ $line }}
    	    {{ end }}
    	</div>
    </div>
    {{ end }}
</div>
</body>
</html>
`

// logo.png is the ONE mark, derived from docs/brand/one-mark-source.png by
// .github/workflows/brand-assets.yml. It is attached inline to every notification
// mail, conversational ones included, and rendered by both templates above.
// Do not edit it by hand: that workflow's path filter covers it, so an edit only
// triggers a regeneration over the top. It is not upstream Vikunja's mark, which
// the AGPL does not license to us (see CLAUDE.md section 7).
//
// The source has an alpha channel, so neither template paints a band behind it.
// The Percy wordmark this replaced had none and needed one (BRA-990).
//
//go:embed logo.png
var logo embed.FS

// newNotificationSanitizer builds the bluemonday policy for all HTML in notification
// emails. Only inline data-URI images (avatars) are allowed: RewriteSrc blanks any
// remote image src so a user-controlled task title, comment or description can't
// smuggle a tracking pixel into a recipient's inbox.
func newNotificationSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowDataURIImages()
	p.AllowAttrs("style").OnElements("img", "div")
	p.AllowStyles("border-radius", "vertical-align", "margin-right").OnElements("img")
	p.AllowStyles("padding-top", "margin-bottom").OnElements("div")
	p.RewriteSrc(func(u *url.URL) {
		if u.Scheme != "data" {
			*u = url.URL{}
		}
	})
	return p
}

func convertLinesToHTML(lines []*mailLine) (linesHTML []templatehtml.HTML, err error) {
	p := newNotificationSanitizer()

	for _, line := range lines {
		if line.isHTML {
			sanitized := p.Sanitize(line.Text)
			if trimmed := strings.TrimSpace(sanitized); trimmed != "" && !startsWithBlockElement(trimmed) {
				sanitized = "<p>" + sanitized + "</p>"
			}
			// #nosec G203 -- the html is sanitized
			linesHTML = append(linesHTML, templatehtml.HTML(ensurePMargins(sanitized)))
			continue
		}

		md := []byte(line.Text)
		var buf bytes.Buffer
		err = goldmark.Convert(md, &buf)
		if err != nil {
			return nil, err
		}
		// #nosec G203 -- the html is sanitized
		linesHTML = append(linesHTML, templatehtml.HTML(ensurePMargins(p.Sanitize(buf.String()))))
	}

	return
}

// sanitizeLinesToHTML sanitizes lines without wrapping in <p> tags or adding margins.
// Used for footer lines and other content that should not have paragraph styling.
func sanitizeLinesToHTML(lines []*mailLine) (linesHTML []templatehtml.HTML, err error) {
	p := newNotificationSanitizer()

	for _, line := range lines {
		if line.isHTML {
			// #nosec G203 -- the html is sanitized
			linesHTML = append(linesHTML, templatehtml.HTML(p.Sanitize(line.Text)))
			continue
		}

		md := []byte(line.Text)
		var buf bytes.Buffer
		err = goldmark.Convert(md, &buf)
		if err != nil {
			return nil, err
		}
		sanitized := p.Sanitize(buf.String())
		// Strip <p> wrapping added by goldmark since the template already provides a container
		sanitized = rePTagOpen.ReplaceAllString(sanitized, "")
		sanitized = strings.ReplaceAll(sanitized, "</p>", "")
		sanitized = strings.TrimSpace(sanitized)
		// #nosec G203 -- the html is sanitized
		linesHTML = append(linesHTML, templatehtml.HTML(sanitized))
	}

	return
}

var rePTagOpen = regexp.MustCompile(`<p[^>]*>`)

func startsWithBlockElement(html string) bool {
	lower := strings.ToLower(html)
	for _, tag := range []string{"<p", "<div", "<h1", "<h2", "<h3", "<h4", "<h5", "<h6", "<ul", "<ol", "<li", "<table", "<blockquote", "<pre", "<hr"} {
		if strings.HasPrefix(lower, tag) {
			return true
		}
	}
	return false
}

var (
	reLinks    = regexp.MustCompile(`<a[^>]+href="([^"]*)"[^>]*>([^<]*)</a>`)
	reHTMLTags = regexp.MustCompile(`<[^>]+>`)
	rePTag     = regexp.MustCompile(`<p(?:\s[^>]*)?>`)
)

const pMarginStyle = `style="margin-top: 10px; margin-bottom: 10px;"`

// ensurePMargins replaces all <p> and <p ...> tags with a version
// that has fixed 10px top/bottom margins, ensuring consistent spacing
// across email clients.
func ensurePMargins(html string) string {
	return rePTag.ReplaceAllString(html, "<p "+pMarginStyle+">")
}

var markdownTextWriter = goldmarkhtml.NewWriter()

func markdownToPlainText(markdown string) string {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	var plain strings.Builder
	linkStarts := make(map[ast.Node]int)
	listItemIndents := make([]int, 0)
	listItemHasBlocks := make([]bool, 0)

	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n := node.(type) {
		case *ast.Text:
			if !entering {
				return ast.WalkContinue, nil
			}
			writeMarkdownText(&plain, n.Value(source), n.IsRaw())
			if n.SoftLineBreak() || n.HardLineBreak() {
				plain.WriteByte('\n')
				if len(listItemIndents) > 0 {
					plain.WriteString(strings.Repeat(" ", listItemIndents[len(listItemIndents)-1]))
				}
			}
		case *ast.String:
			if entering {
				writeMarkdownText(&plain, n.Value, n.IsRaw() || n.IsCode())
			}
		case *ast.AutoLink:
			if entering {
				plain.Write(n.Label(source))
			}
		case *ast.Link:
			if entering {
				linkStarts[node] = plain.Len()
				return ast.WalkContinue, nil
			}
			start := linkStarts[node]
			label := plain.String()[start:]
			var normalized strings.Builder
			writeMarkdownText(&normalized, n.Destination, false)
			destination := normalized.String()
			if destination != "" && label != destination {
				plain.WriteString(" (")
				plain.WriteString(destination)
				plain.WriteByte(')')
			}
			delete(linkStarts, node)
		case *ast.Image:
			if !entering {
				if len(n.Destination) > 0 {
					plain.WriteString(" (")
					writeMarkdownText(&plain, n.Destination, false)
					plain.WriteByte(')')
				}
			}
		case *ast.ListItem:
			if entering {
				if len(listItemHasBlocks) > 0 {
					listItemHasBlocks[len(listItemHasBlocks)-1] = true
				}
				listItemIndents = append(listItemIndents, writePlainListItem(&plain, n))
				listItemHasBlocks = append(listItemHasBlocks, false)
			} else {
				listItemIndents = listItemIndents[:len(listItemIndents)-1]
				listItemHasBlocks = listItemHasBlocks[:len(listItemHasBlocks)-1]
				writePlainNewline(&plain)
			}
		case *ast.Paragraph, *ast.Heading:
			if entering {
				writePlainListBlockStart(&plain, listItemIndents, listItemHasBlocks)
			} else {
				writePlainNewline(&plain)
			}
		case *ast.CodeBlock, *ast.FencedCodeBlock:
			if entering {
				writePlainListBlockStart(&plain, listItemIndents, listItemHasBlocks)
				writePlainBlock(&plain, node.Lines().Value(source), listItemIndents)
				writePlainNewline(&plain)
				return ast.WalkSkipChildren, nil
			}
		case *ast.ThematicBreak:
			if entering {
				writePlainListBlockStart(&plain, listItemIndents, listItemHasBlocks)
				plain.WriteString("---\n")
			}
		case *ast.RawHTML, *ast.HTMLBlock:
			if entering {
				return ast.WalkSkipChildren, nil
			}
		}

		return ast.WalkContinue, nil
	})

	return strings.TrimSpace(plain.String())
}

func writePlainListItem(plain *strings.Builder, item *ast.ListItem) int {
	writePlainNewline(plain)
	prefixStart := plain.Len()
	list := item.Parent().(*ast.List)
	depth := 0
	for parent := list.Parent(); parent != nil; parent = parent.Parent() {
		if _, nested := parent.(*ast.List); nested {
			depth++
		}
	}
	plain.WriteString(strings.Repeat("  ", depth))

	if list.IsOrdered() {
		position := list.Start
		for sibling := item.PreviousSibling(); sibling != nil; sibling = sibling.PreviousSibling() {
			position++
		}
		plain.WriteString(strconv.Itoa(position))
		plain.WriteString(". ")
	} else {
		plain.WriteString("- ")
	}

	return plain.Len() - prefixStart
}

func writePlainListBlockStart(plain *strings.Builder, indents []int, hasBlocks []bool) {
	if len(hasBlocks) == 0 {
		return
	}

	current := len(hasBlocks) - 1
	if hasBlocks[current] {
		writePlainNewline(plain)
		plain.WriteString(strings.Repeat(" ", indents[current]))
	}
	hasBlocks[current] = true
}

func writePlainBlock(plain *strings.Builder, value []byte, indents []int) {
	indent := 0
	if len(indents) > 0 {
		indent = indents[len(indents)-1]
	}

	for i, char := range value {
		plain.WriteByte(char)
		if char == '\n' && i < len(value)-1 {
			plain.WriteString(strings.Repeat(" ", indent))
		}
	}
}

func writeMarkdownText(plain *strings.Builder, value []byte, raw bool) {
	if raw {
		plain.Write(value)
		return
	}

	var escaped bytes.Buffer
	writer := bufio.NewWriter(&escaped)
	markdownTextWriter.Write(writer, value)
	_ = writer.Flush()
	plain.WriteString(html.UnescapeString(escaped.String()))
}

func writePlainNewline(plain *strings.Builder) {
	if plain.Len() == 0 || plain.String()[plain.Len()-1] != '\n' {
		plain.WriteByte('\n')
	}
}

func convertLinesToPlain(lines []*mailLine) []*mailLine {
	plain := make([]*mailLine, 0, len(lines))
	for _, line := range lines {
		if !line.isHTML {
			text := markdownToPlainText(line.Text)
			if text != "" {
				plain = append(plain, &mailLine{Text: text})
			}
			continue
		}

		text := line.Text
		// Convert <a href="url">text</a> to "text (url)"
		text = reLinks.ReplaceAllString(text, "$2 ($1)")
		// Strip remaining HTML tags
		text = reHTMLTags.ReplaceAllString(text, "")
		// Clean up HTML entities
		text = strings.ReplaceAll(text, "&gt;", ">")
		text = strings.ReplaceAll(text, "&lt;", "<")
		text = strings.ReplaceAll(text, "&amp;", "&")
		text = strings.TrimSpace(text)

		if text != "" {
			plain = append(plain, &mailLine{Text: text})
		}
	}
	return plain
}

// RenderMail takes a precomposed mail message and renders it into a ready to send mail.Opts object
func RenderMail(m *Mail, lang string) (mailOpts *mail.Opts, err error) {

	var htmlContent bytes.Buffer
	var plainContent bytes.Buffer

	// Select template based on conversational flag
	var plainTemplate, htmlTemplate string
	if m.conversational {
		plainTemplate = mailTemplateConversationalPlain
		htmlTemplate = mailTemplateConversationalHTML
	} else {
		plainTemplate = mailTemplatePlain
		htmlTemplate = mailTemplateHTML
	}

	plain, err := templatetext.New("mail-plain").Parse(plainTemplate)
	if err != nil {
		return nil, err
	}

	html, err := templatehtml.New("mail-html").Parse(htmlTemplate)
	if err != nil {
		return nil, err
	}

	boundaryStr, err := utils.CryptoRandomString(13)
	if err != nil {
		return nil, err
	}
	boundary := "np" + boundaryStr

	data := make(map[string]interface{})

	data["Greeting"] = m.greeting
	data["IntroLines"] = convertLinesToPlain(m.introLines)
	data["OutroLines"] = convertLinesToPlain(m.outroLines)
	if m.conversational && m.headerLine != nil {
		plainHeaders := convertLinesToPlain([]*mailLine{m.headerLine})
		if len(plainHeaders) > 0 {
			data["HeaderLinePlain"] = plainHeaders[0].Text
		}
	}
	data["FooterLines"] = convertLinesToPlain(m.footerLines)
	data["ActionText"] = m.actionText
	data["ActionURL"] = m.actionURL
	data["Boundary"] = boundary
	data["FrontendURL"] = config.ServicePublicURL.GetString()
	data["CopyURLText"] = i18n.T(lang, "notifications.common.copy_url")

	if m.headerLine != nil {
		// #nosec G203 -- the html is sanitized
		data["HeaderLineHTML"] = templatehtml.HTML(newNotificationSanitizer().Sanitize(m.headerLine.Text))
	}

	data["IntroLinesHTML"], err = convertLinesToHTML(m.introLines)
	if err != nil {
		return nil, err
	}

	data["OutroLinesHTML"], err = convertLinesToHTML(m.outroLines)
	if err != nil {
		return nil, err
	}

	data["FooterLinesHTML"], err = sanitizeLinesToHTML(m.footerLines)
	if err != nil {
		return nil, err
	}

	err = plain.Execute(&plainContent, data)
	if err != nil {
		return nil, err
	}
	err = html.Execute(&htmlContent, data)
	if err != nil {
		return nil, err
	}

	mailOpts = &mail.Opts{
		From:        m.from,
		To:          m.to,
		Subject:     m.subject,
		ContentType: mail.ContentTypeMultipart,
		Message:     plainContent.String(),
		HTMLMessage: htmlContent.String(),
		Boundary:    boundary,
		ThreadID:    m.threadID,
	}

	// Every notification mail carries the mark, conversational ones included
	// (Sebastian, 2026-08-20). The conversational template renders it small, at
	// 28px, so a reply to a task comment still reads as a reply rather than as an
	// announcement.
	mailOpts.EmbedFS = map[string]*embed.FS{
		"logo.png": &logo,
	}

	return mailOpts, nil
}
