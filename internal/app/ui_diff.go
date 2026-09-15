package app

import (
	"html/template"
	"strconv"
	"strings"

	"secretgit/internal/collab"
)

// diffRow is one rendered line of a unified diff with enough context to
// anchor a comment: the new-file path and line number.
type diffRow struct {
	Class    string // f (file header), k (hunk), a, d, "" (context), h (---/+++)
	Text     string
	Path     string
	NewLine  int // 0 when the line has no new-file number (deleted lines, headers)
	Comments []eventRow
}

// parseDiff walks a unified diff and attaches comments keyed by
// "path:line" to the rows they belong to.
func parseDiff(text string, comments map[string][]eventRow) []diffRow {
	var rows []diffRow
	path := ""
	newLine := 0
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		row := diffRow{Text: l}
		switch {
		case strings.HasPrefix(l, "diff --git "):
			row.Class = "f"
			path = ""
		case strings.HasPrefix(l, "+++ "):
			row.Class = "h"
			p := strings.TrimPrefix(l, "+++ ")
			p = strings.TrimPrefix(p, "b/")
			if p != "/dev/null" {
				path = p
			}
		case strings.HasPrefix(l, "--- "):
			row.Class = "h"
		case strings.HasPrefix(l, "@@"):
			row.Class = "k"
			// @@ -a,b +c,d @@
			if i := strings.Index(l, "+"); i >= 0 {
				num := l[i+1:]
				if j := strings.IndexAny(num, ", @"); j >= 0 {
					num = num[:j]
				}
				newLine, _ = strconv.Atoi(num)
			}
			rows = append(rows, row)
			continue
		case strings.HasPrefix(l, "+"):
			row.Class = "a"
			row.Path, row.NewLine = path, newLine
			newLine++
		case strings.HasPrefix(l, "-"):
			row.Class = "d"
			row.Path = path
		case strings.HasPrefix(l, "\\"):
			row.Class = "h"
		default:
			if path != "" && newLine > 0 && !strings.HasPrefix(l, "index ") && !strings.HasPrefix(l, "new file") && !strings.HasPrefix(l, "deleted file") && !strings.HasPrefix(l, "similarity") && !strings.HasPrefix(l, "rename") && !strings.HasPrefix(l, "old mode") && !strings.HasPrefix(l, "new mode") {
				row.Path, row.NewLine = path, newLine
				newLine++
			} else {
				row.Class = "h"
			}
		}
		if row.NewLine > 0 {
			row.Comments = comments[row.Path+":"+strconv.Itoa(row.NewLine)]
		}
		rows = append(rows, row)
	}
	return rows
}

// inlineComments indexes comments anchored at the current head by path:line.
func inlineComments(pr *collab.PullRequest, head string) map[string][]eventRow {
	out := map[string][]eventRow{}
	for _, e := range pr.Events {
		if e.Kind != collab.KindComment || e.Path == "" || e.Line == 0 {
			continue
		}
		if e.Commit != "" && e.Commit != head {
			continue // anchored on an older head; shown in the conversation with its commit
		}
		who := e.ActorName
		if who == "" {
			who = e.Actor
		}
		k := e.Path + ":" + strconv.Itoa(e.Line)
		out[k] = append(out[k], eventRow{Kind: e.Kind, Who: who, When: e.Created.Format("2006-01-02 15:04"), Body: e.Body, Path: e.Path, Line: e.Line, Commit: short(e.Commit)})
	}
	return out
}

var _ template.HTML
