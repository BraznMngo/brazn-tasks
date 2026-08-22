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
<html lang="{{ .Lang }}" xmlns="http://www.w3.org/1999/xhtml" xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="x-apple-disable-message-reformatting">
  <meta name="color-scheme" content="light dark">
  <meta name="supported-color-schemes" content="light dark">
  <title>{{ .Heading }}</title>
  <!--[if mso]>
  <noscript><xml><o:OfficeDocumentSettings><o:PixelsPerInch>96</o:PixelsPerInch></o:OfficeDocumentSettings></xml></noscript>
  <![endif]-->
  <style>
    html, body { margin: 0 !important; padding: 0 !important; width: 100% !important; }
    table { border-collapse: collapse; border-spacing: 0; mso-table-lspace: 0pt; mso-table-rspace: 0pt; }
    img { border: 0; display: block; outline: none; text-decoration: none; }
    a { color: inherit; }
    .preheader { display: none !important; max-height: 0 !important; overflow: hidden !important; opacity: 0 !important; color: transparent !important; mso-hide: all !important; }
    @media screen and (max-width: 640px) {
      .outer { padding: 24px 12px !important; }
      .shell { width: 100% !important; max-width: 100% !important; }
      .content { padding: 34px 24px 30px !important; }
      .title { font-size: 27px !important; line-height: 33px !important; }
      .email-card { border-radius: 34px !important; }
      .button-link { box-sizing: border-box !important; min-width: 0 !important; width: 100% !important; }
      .fallback-url { word-break: break-all !important; }
    }
    @media (prefers-color-scheme: dark) {
      .email-bg { background-color: #080d1a !important; }
      .email-card { background-color: #111827 !important; border-color: #283247 !important; }
      .main-text, .title { color: #f8fafc !important; }
      .muted-text { color: #aeb8ca !important; }
      .divider { background-color: #2b3549 !important; }
      .url-box { background-color: #0b1220 !important; border-color: #2b3549 !important; }
      .url-link { color: #8db1ff !important; }
    }
    [data-ogsc] .email-bg { background-color: #080d1a !important; }
    [data-ogsc] .email-card { background-color: #111827 !important; border-color: #283247 !important; }
    [data-ogsc] .main-text, [data-ogsc] .title { color: #f8fafc !important; }
    [data-ogsc] .muted-text { color: #aeb8ca !important; }
    [data-ogsc] .url-box { background-color: #0b1220 !important; border-color: #2b3549 !important; }
    [data-ogsc] .url-link { color: #8db1ff !important; }
  </style>
  <!--[if mso]>
  <style>
    .email-card { border: 0 !important; background-color: transparent !important; }
  </style>
  <![endif]-->
</head>
<body class="email-bg" style="margin:0; padding:0; background-color:#f3f6fb;">
  {{ if .Preheader }}<div class="preheader">{{ .Preheader }}</div>{{ end }}
  <table class="email-bg" role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f3f6fb" style="width:100%; background-color:#f3f6fb;">
    <tr>
      <td class="outer" align="center" style="padding:42px 18px;">
        <table class="shell" role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px; max-width:600px;">
          <tr>
            <td align="center" style="padding:0 0 23px;">
              <img src="cid:logo.png" width="207" alt="ONE" style="width:207px; max-width:207px; height:auto;">
            </td>
          </tr>
          <tr>
            <td style="padding:0;">
              <!--[if mso]>
              <v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" style="width:600px;" arcsize="14%" strokecolor="#e3e8f2" strokeweight="1px" fillcolor="#ffffff">
                <w:anchorlock/>
                <v:textbox inset="0,0,0,0" style="mso-fit-shape-to-text:true;">
              <![endif]-->
              <table class="email-card" role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" style="width:100%; background-color:#ffffff; border:1px solid #e3e8f2; border-collapse:separate !important; border-spacing:0; border-radius:34px; box-shadow:0 14px 40px rgba(20,37,74,0.09); overflow:hidden;">
                <tr>
                  <td class="content" style="padding:38px 48px 38px; background-color:transparent; border-radius:33px; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif;">
                    <table role="presentation" width="44" cellpadding="0" cellspacing="0" border="0" style="width:44px; border-collapse:separate !important;">
                      <tr><td height="4" bgcolor="#2a6afe" style="height:4px; background-color:#2a6afe; border-radius:2px; font-size:0; line-height:0;">&nbsp;</td></tr>
                    </table>
                    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr><td height="20" style="height:20px; font-size:0; line-height:0;">&nbsp;</td></tr></table>
                    {{ if .Eyebrow }}
                    <div style="color:#2a6afe; font-size:11px; font-weight:800; line-height:16px; letter-spacing:1.7px; text-transform:uppercase;">{{ .Eyebrow }}</div>
                    {{ end }}
                    <h1 class="title" style="margin:10px 0 22px; color:#111827; font-size:32px; font-weight:750; line-height:39px; letter-spacing:-0.8px;">{{ .Heading }}</h1>
                    <p class="main-text" style="margin:0 0 12px; color:#263247; font-size:16px; line-height:26px;">{{ .Greeting }}</p>

                    {{ range $line := .IntroLinesHTML }}
                      {{ $line }}
                    {{ end }}

                    {{ if .ActionURL }}
                    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
                      <tr>
                        <td align="left" style="padding:0; mso-padding-alt:0;">
                          <!--[if mso]>
                          <v:roundrect href="{{ .ActionURL }}" style="height:48px;v-text-anchor:middle;width:250px;" arcsize="29%" stroke="f" fillcolor="#2a6afe">
                            <w:anchorlock xmlns:w="urn:schemas-microsoft-com:office:word"/>
                            <center style="color:#ffffff;font-family:Segoe UI,Arial,sans-serif;font-size:15px;font-weight:bold;">{{ .ActionText }}</center>
                          </v:roundrect>
                          <![endif]-->
                          <!--[if !mso]><!-->
                          <a class="button-link" href="{{ .ActionURL }}" title="{{ .ActionText }}" style="display:inline-block; box-sizing:border-box; width:250px; padding:14px 20px; background-color:#2a6afe; border-radius:14px; box-shadow:0 8px 20px rgba(42,106,254,0.25); color:#ffffff; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif; font-size:15px; font-weight:750; line-height:20px; text-align:center; text-decoration:none; white-space:nowrap;">{{ .ActionText }}</a>
                          <!--<![endif]-->
                        </td>
                      </tr>
                    </table>
                    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr><td height="27" style="height:27px; font-size:0; line-height:0;">&nbsp;</td></tr></table>
                    {{ end }}

                    {{ range $line := .OutroLinesHTML }}
                      {{ $line }}
                    {{ end }}

                    {{ if .ActionURL }}
                    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
                      <tr><td height="28" style="height:28px; font-size:0; line-height:0;">&nbsp;</td></tr>
                      <tr><td class="divider" height="1" bgcolor="#e8ecf3" style="height:1px; background-color:#e8ecf3; font-size:0; line-height:0;">&nbsp;</td></tr>
                      <tr><td height="22" style="height:22px; font-size:0; line-height:0;">&nbsp;</td></tr>
                    </table>
                    <p class="muted-text" style="margin:0 0 10px; color:#7a8496; font-size:12px; line-height:19px;">{{ .CopyURLText }}</p>
                    <a class="url-box url-link fallback-url" href="{{ .ActionURL }}" style="display:block; padding:12px 14px; background-color:#f7f9fc; border:1px solid #e6ebf3; border-radius:14px; color:#2a6afe; font-size:11px; line-height:17px; text-decoration:none; word-break:break-all;">{{ .ActionURL }}</a>
                    {{ range $line := .FooterLinesHTML }}
                      {{ $line }}
                    {{ end }}
                    {{ else }}
                    {{ if .FooterLinesHTML }}
                    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
                      <tr><td height="28" style="height:28px; font-size:0; line-height:0;">&nbsp;</td></tr>
                      <tr><td class="divider" height="1" bgcolor="#e8ecf3" style="height:1px; background-color:#e8ecf3; font-size:0; line-height:0;">&nbsp;</td></tr>
                      <tr><td height="22" style="height:22px; font-size:0; line-height:0;">&nbsp;</td></tr>
                    </table>
                    {{ range $line := .FooterLinesHTML }}
                      {{ $line }}
                    {{ end }}
                    {{ end }}
                    {{ end }}
                  </td>
                </tr>
              </table>
              <!--[if mso]>
                </v:textbox>
              </v:roundrect>
              <![endif]-->
            </td>
          </tr>
          <tr>
            <td align="center" style="padding:23px 20px 0; color:#8993a5; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif; font-size:11px; line-height:18px;">
              ONE&nbsp;&nbsp;&middot;&nbsp;&nbsp;by Brazn&nbsp;&nbsp;&middot;&nbsp;&nbsp;<a href="https://brazn.one" style="color:#8993a5; text-decoration:underline;">brazn.one</a>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>
`

const mailTemplateConversationalHTML = `
<!doctype html>
<html style="width: 100%; height: 100%; padding: 0; margin: 0;">
<head>
    <meta name="viewport" content="width=device-width">
    <meta charset="utf-8">
</head>
<body style="width: 100%; padding: 0; margin: 0; background: #f6f8fa; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif;">
<div style="margin: 0 auto; background: #ffffff;">

    {{ if .HeaderLineHTML }}
    <div style="padding: 12px 20px 0; color: #57606a; font-size: 14px; line-height: 1.5;">
        {{ .HeaderLineHTML }}
    </div>
    {{ end }}

    {{ if or .IntroLinesHTML .OutroLinesHTML }}
    <div style="padding-left: 20px; color: #24292f; font-size: 14px; line-height: 1.5;">

        {{ range $line := .IntroLinesHTML}}
            {{ $line }}
        {{ end }}

        {{ range $line := .OutroLinesHTML}}
            {{ $line }}
        {{ end }}
    </div>
    {{ end }}

    {{ if or .ActionURL .FooterLinesHTML }}
    <div style="padding: 4px 20px 8px 20px; border-top: 1px solid #d1d9e0; padding-top: 6px; font-size: 12px">
        {{ if .ActionURL }}
        <a href="{{ .ActionURL }}" style="color: #2a6afe; text-decoration: none; font-weight: 500; font-size: 12px;">
            {{ .ActionText }} →
        </a>
        {{ end }}
    	<div style="padding-top: 6px; color: #656d76;">
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

// logo.png is the ONE wordmark, derived from docs/brand/one-wordmark-source.png
// by .github/workflows/brand-assets.yml. It is attached inline to every
// non-conversational notification mail and rendered by mailTemplateHTML above.
// Do not edit it by hand: that workflow's path filter covers it, so an edit only
// triggers a regeneration over the top. It is no longer upstream Vikunja's mark,
// nor Percy's, which the AGPL does not license to us (see CLAUDE.md section 7).
//
// Unlike the interim Percy wordmark it replaces (BRA-990), this asset carries a
// real alpha channel, so mailTemplateHTML places it directly on the mail's own
// background -- light or dark -- with no matching colour band behind it. See
// docs/brand/README.md (BRA-1374).
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

func convertLinesToHTML(lines []*mailLine) ([]templatehtml.HTML, error) {
	return convertLinesToStyledHTML(lines, ensurePMargins)
}

// convertLinesToFormalHTML renders lines the same way convertLinesToHTML
// does, but opens each paragraph with pOpenTag instead of the generic 10px
// margin. The formal template's dark-mode CSS targets .main-text/.muted-text
// (BRA-1374) -- text that only ever gets the untargeted, unstyled <p> from
// ensurePMargins keeps its light-mode color on the dark card.
func convertLinesToFormalHTML(lines []*mailLine, pOpenTag string) ([]templatehtml.HTML, error) {
	return convertLinesToStyledHTML(lines, func(html string) string {
		return rePTag.ReplaceAllString(html, pOpenTag)
	})
}

func convertLinesToStyledHTML(lines []*mailLine, style func(string) string) (linesHTML []templatehtml.HTML, err error) {
	p := newNotificationSanitizer()

	for _, line := range lines {
		if line.isHTML {
			sanitized := p.Sanitize(line.Text)
			if trimmed := strings.TrimSpace(sanitized); trimmed != "" && !startsWithBlockElement(trimmed) {
				sanitized = "<p>" + sanitized + "</p>"
			}
			// #nosec G203 -- the html is sanitized
			linesHTML = append(linesHTML, templatehtml.HTML(style(sanitized)))
			continue
		}

		md := []byte(line.Text)
		var buf bytes.Buffer
		err = goldmark.Convert(md, &buf)
		if err != nil {
			return nil, err
		}
		// #nosec G203 -- the html is sanitized
		linesHTML = append(linesHTML, templatehtml.HTML(style(p.Sanitize(buf.String()))))
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

	data["Lang"] = lang
	// The formal template's heading is always the subject line itself
	// (BRA-1374's own "cheapest honest answer": Sebastian's design headline
	// says the same thing the subject does) -- no separate field for
	// messages to set, so nothing that predates this change has to be
	// touched to gain one.
	data["Heading"] = m.subject
	data["Eyebrow"] = m.eyebrow
	data["Preheader"] = m.preheader
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

	if m.conversational {
		data["IntroLinesHTML"], err = convertLinesToHTML(m.introLines)
		if err != nil {
			return nil, err
		}

		data["OutroLinesHTML"], err = convertLinesToHTML(m.outroLines)
		if err != nil {
			return nil, err
		}
	} else {
		data["IntroLinesHTML"], err = convertLinesToFormalHTML(m.introLines,
			`<p class="main-text" style="margin:0 0 12px; color:#263247; font-size:16px; line-height:26px;">`)
		if err != nil {
			return nil, err
		}

		data["OutroLinesHTML"], err = convertLinesToFormalHTML(m.outroLines,
			`<p class="muted-text" style="margin:0 0 12px; color:#667085; font-size:13px; line-height:21px;">`)
		if err != nil {
			return nil, err
		}
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

	if !m.conversational {
		mailOpts.EmbedFS = map[string]*embed.FS{
			"logo.png": &logo,
		}
	}

	return mailOpts, nil
}
