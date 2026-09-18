package app

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// md renders user text (pull request bodies, comments) as Markdown.
// Raw HTML is never passed through, so user content cannot script the UI.
var mdEngine = goldmark.New(goldmark.WithExtensions(extension.GFM))

func md(text string) template.HTML {
	var buf bytes.Buffer
	if err := mdEngine.Convert([]byte(text), &buf); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(text) + "</p>")
	}
	return template.HTML(buf.String())
}
