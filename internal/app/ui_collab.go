package app

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andraspalinkas/secretree/internal/collab"
	"github.com/andraspalinkas/secretree/internal/gitx"
)

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *uiServer) collabRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /pulls", s.pulls)
	mux.HandleFunc("GET /pulls/new", s.pullNew)
	mux.HandleFunc("POST /pulls/new", s.pullNew)
	mux.HandleFunc("GET /pull/{id}", s.pull)
	mux.HandleFunc("POST /pull/{id}/comment", s.pullAction)
	mux.HandleFunc("POST /pull/{id}/review", s.pullAction)
	mux.HandleFunc("POST /pull/{id}/merge", s.pullAction)
	mux.HandleFunc("POST /pull/{id}/close", s.pullAction)
	mux.HandleFunc("POST /pull/{id}/resolve", s.pullAction)
}

// quiet returns an App whose output is discarded (UI actions report via redirects).
func (s *uiServer) quiet() *App { return &App{Out: io.Discard, Err: io.Discard} }

func (s *uiServer) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.FormValue("csrf") != s.csrf {
		http.Error(w, "bad csrf token; reload the page", http.StatusForbidden)
		return false
	}
	return true
}

type prRow struct {
	ID, Title, Head, Base, Author, State, Date string
	Number                                     int
	Approved, Changes                          []string
	Checks                                     []checkRow
}

type checkRow struct{ Name, Status, Summary, Log string }

type prPage struct {
	Repo, Source, Title, Kind, CSRF, Error string
	All                                    bool
	Rows                                   []prRow
	PR                                     *prRow
	Body                                   string
	HeadSHA, BaseSHA                       string
	Diff                                   template.HTML
	DiffRows                               []diffRow
	Events                                 []eventRow
	Policy                                 collab.Policy
	MergeBlock                             string
	Branches                               []string
}

type eventRow struct {
	ID, Kind, Who, When, Body, Path, Verdict, State, Commit string
	Line                                                    int
	Agent                                                   bool   // written by an agent member
	ResolvedBy                                              string // thread resolved, by whom
	Moved                                                   bool   // followed to a new line on the current head
	Outdated                                                bool   // its line changed since; not shown inline
}

func (s *uiServer) renderPR(w http.ResponseWriter, p *prPage) {
	p.Repo, p.Source, p.CSRF = s.name, s.source, s.csrf
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; form-action 'self'")
	if err := prTmpl.Execute(w, p); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *uiServer) row(c *prContext, pr *collab.PullRequest) prRow {
	head := c.headSHA(pr)
	approved, changes := pr.Approvals(head)
	row := prRow{ID: pr.ID, Number: pr.Number, Title: pr.Title, Head: pr.Head, Base: pr.Base, Author: pr.Author, State: pr.State,
		Date: pr.Created.Format("2006-01-02"), Approved: approved, Changes: changes}
	for name, e := range collab.Checks(c.events, head) {
		row.Checks = append(row.Checks, checkRow{Name: name, Status: e.Status, Summary: e.Summary, Log: e.Log})
	}
	return row
}

func (s *uiServer) pulls(w http.ResponseWriter, r *http.Request) {
	c, err := s.app.openPR(s.work)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	p := &prPage{Kind: "list", Title: "pull requests", All: r.URL.Query().Get("all") == "1"}
	open := 0
	for i := range c.prs {
		if c.prs[i].State == collab.StateOpen {
			open++
		}
	}
	// an empty "open" list next to merged work is confusing: fall back to all
	if open == 0 && len(c.prs) > 0 && !p.All {
		p.All = true
		p.Error = ""
		p.Body = "no open pull requests; showing merged and closed ones"
	}
	for i := range c.prs {
		if !p.All && c.prs[i].State != collab.StateOpen {
			continue
		}
		p.Rows = append(p.Rows, s.row(c, &c.prs[i]))
	}
	if len(c.prs) == 0 {
		p.Body = "no pull requests yet: push a branch and open one here, or with secretree pr open"
	}
	s.renderPR(w, p)
}

func (s *uiServer) pull(w http.ResponseWriter, r *http.Request) {
	c, err := s.app.openPR(s.work)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	pr, err := collab.Resolve(c.prs, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	row := s.row(c, pr)
	head, base := c.headSHA(pr), c.baseSHA(pr)
	p := &prPage{Kind: "pr", Title: "#" + strconv.Itoa(pr.Number) + " " + pr.Title, PR: &row, Body: pr.Body, HeadSHA: head, BaseSHA: base,
		Policy: collab.LoadPolicy(c.r.Work, base), Error: r.URL.Query().Get("error")}
	if pr.State == collab.StateOpen {
		p.MergeBlock = c.mergeCheck(pr, head, base)
		if out, err := gitx.Run(c.r.Work, "diff", base+"..."+head); err == nil {
			p.DiffRows = parseDiff(out, inlineComments(c, pr, head))
		}
	} else if pr.MergeCommit != "" {
		if out, err := gitx.Run(c.r.Work, "show", "--stat", "--format=merged as %h", pr.MergeCommit); err == nil {
			p.Diff = renderDiff(out)
		}
	}
	resolved := pr.Resolved()
	for _, e := range pr.Events {
		who := e.ActorName
		if who == "" {
			who = e.Actor
		}
		row := eventRow{ID: e.ID, Kind: e.Kind, Who: who, When: e.Created.Format("2006-01-02 15:04"), Body: e.Body,
			Path: e.Path, Line: e.Line, Verdict: e.Verdict, State: e.State, Commit: short(e.Commit), Agent: c.agents[e.Actor], ResolvedBy: resolved[e.ID]}
		if e.Kind == collab.KindComment && e.Path != "" && e.Commit != "" && e.Commit != head {
			if _, ok := remapLine(c.r.Work, e.Commit, head, e.Path, e.Line); !ok {
				row.Outdated = true
			}
		}
		p.Events = append(p.Events, row)
	}
	s.renderPR(w, p)
}

func (s *uiServer) pullNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !s.checkCSRF(w, r) {
			return
		}
		err := s.quiet().PROpen(PROpenOptions{Dir: s.work, Title: r.FormValue("title"), Body: r.FormValue("body"), Head: r.FormValue("head"), Base: r.FormValue("base")})
		if err != nil {
			http.Redirect(w, r, "/pulls/new?error="+template.URLQueryEscaper(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/pulls", http.StatusSeeOther)
		return
	}
	p := &prPage{Kind: "new", Title: "new pull request", Error: r.URL.Query().Get("error")}
	remote := secretreeRemote(s.work)
	pattern := "refs/heads/"
	if remote != "" {
		pattern = "refs/remotes/" + remote + "/"
	}
	if out, err := gitx.Run(s.work, "for-each-ref", "--format=%(refname:short)", pattern); err == nil {
		for _, b := range strings.Fields(out) {
			b = strings.TrimPrefix(b, remote+"/")
			if b != "HEAD" {
				p.Branches = append(p.Branches, b)
			}
		}
	}
	s.renderPR(w, p)
}

func (s *uiServer) pullAction(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	id := r.PathValue("id")
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	a := s.quiet()
	var err error
	switch action {
	case "comment":
		line, _ := strconv.Atoi(r.FormValue("line"))
		err = a.PRComment(s.work, id, r.FormValue("body"), r.FormValue("path"), line)
	case "review":
		err = a.PRReview(s.work, id, r.FormValue("verdict"), r.FormValue("body"))
	case "merge":
		err = a.PRMerge(s.work, id, r.FormValue("method"))
	case "close":
		err = a.PRClose(s.work, id)
	case "resolve":
		err = a.PRResolve(s.work, id, r.FormValue("comment"))
	}
	target := "/pull/" + id
	if err != nil {
		target += "?error=" + template.URLQueryEscaper(err.Error())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

var prTmpl = template.Must(template.New("pr").Parse(`<!doctype html>
<meta charset="utf-8"><title>{{.Repo}}: {{.Title}}</title>
` + uiCSS + `<style>
.pill{display:inline-block;padding:1px 8px;border-radius:10px;font-size:12px;color:#fff;background:#8250df}.pill.open{background:#1a7f37}.pill.merged{background:#8250df}.pill.closed{background:#cf222e}
.pill.success{background:#1a7f37}.pill.failure{background:#cf222e}.pill.pending{background:#9a6700}
.box{background:#fff;border:1px solid #ddd;border-radius:6px;padding:10px 12px;margin:10px 0}
.ev{border-left:3px solid #ddd;padding:4px 10px;margin:8px 0}.ev.review.approve{border-color:#1a7f37}.ev.review.request_changes{border-color:#cf222e}.ev .who{font-weight:600}.ev .when{color:#666;font-size:12px;margin-left:6px}
textarea,input[type=text],select{width:100%;box-sizing:border-box;padding:6px;font:inherit}textarea{min-height:70px}
form.inline{display:inline}button{padding:5px 12px;margin-top:6px}button.primary{background:#1a7f37;color:#fff;border:0;border-radius:4px}button.danger{background:#cf222e;color:#fff;border:0;border-radius:4px}
.err{background:#ffebe9;border:1px solid #cf222e;padding:8px;border-radius:6px}.block{background:#fff8c5;border:1px solid #d4a72c;padding:8px;border-radius:6px}
details summary{cursor:pointer}pre.log{font:11px/1.4 ui-monospace,Menlo,monospace;background:#f6f8fa;padding:8px;overflow:auto;max-height:300px}
.two{display:flex;gap:12px;flex-wrap:wrap}.two>div{flex:1;min-width:280px}
.diff.rows div{display:flex;align-items:flex-start;padding:0}.diff.rows .ln{width:44px;flex:none;text-align:right;padding-right:8px;color:#999;user-select:none}.diff.rows .tx{padding-left:8px;white-space:pre;flex:1}
.diff.rows .add{width:16px;flex:none;text-align:center;color:transparent;text-decoration:none;font-weight:700}.diff.rows div:hover .add{color:#0969da}
.diff.rows .ic{display:block;white-space:normal;background:#fff8c5;border-top:1px solid #e0c800;border-bottom:1px solid #e0c800;padding:6px 10px 6px 68px;font:13px/1.45 -apple-system,system-ui,sans-serif}.ic .who{font-weight:600}.ic .when{color:#666;font-size:12px;margin-left:6px}
.pill.agent{background:#6e40c9;font-size:11px}.ev.done,.ic.done{opacity:.7}.ic.done details summary{color:#666;cursor:pointer}
button.small{padding:1px 8px;font-size:12px;margin-top:4px}
.diff.rows form.ic{background:#f6f8fa}.diff.rows form.ic textarea{width:100%;box-sizing:border-box;font:13px/1.4 -apple-system,system-ui,sans-serif;min-height:60px}
</style>
<script>
document.addEventListener("click", function (ev) {
  var a = ev.target.closest && ev.target.closest("a.add"); if (!a) return;
  ev.preventDefault();
  var row = a.parentNode; var existing = row.nextElementSibling;
  if (existing && existing.tagName === "FORM") { existing.remove(); return; }
  var f = document.createElement("form"); f.method = "post"; f.className = "ic";
  f.action = location.pathname.replace(/\/$/, "") + "/comment";
  f.innerHTML = '<input type="hidden" name="csrf" value="' + document.querySelector('input[name=csrf]').value + '">' +
    '<input type="hidden" name="path" value="' + a.dataset.path.replace(/"/g, "&quot;") + '"><input type="hidden" name="line" value="' + a.dataset.line + '">' +
    '<div style="margin-bottom:4px;color:#666">' + a.dataset.path + ':' + a.dataset.line + '</div><textarea name="body" required placeholder="comment on this line"></textarea><div><button>Comment</button> <button type="button" class="cancel">Cancel</button></div>';
  f.querySelector(".cancel").onclick = function () { f.remove(); };
  row.insertAdjacentElement("afterend", f); f.querySelector("textarea").focus();
});
</script>
<header><a href="/">{{.Repo}}</a><a href="/pulls" class="nav">pull requests</a><small>{{.Source}}</small></header>
<main>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
{{if eq .Kind "list"}}
<h3>Pull requests <a href="/pulls/new" style="font-weight:400;font-size:13px">+ new</a> <span class="muted" style="font-weight:400;font-size:13px">· {{if .All}}<a href="/pulls">open only</a>{{else}}<a href="/pulls?all=1">show all</a>{{end}}</span></h3>
<table>{{range .Rows}}<tr><td>#{{.Number}}</td><td><a href="/pull/{{.ID}}">{{.Title}}</a><br><span class="muted">{{.Head}} → {{.Base}} · {{.Author}} · {{.Date}}</span></td>
<td><span class="pill {{.State}}">{{.State}}</span></td><td>{{range .Approved}}<span class="pill success">✓ {{.}}</span> {{end}}{{range .Changes}}<span class="pill failure">✗ {{.}}</span> {{end}}</td>
<td>{{range .Checks}}<span class="pill {{.Status}}" title="{{.Summary}}">{{.Name}}</span> {{end}}</td></tr>{{end}}</table>
{{if .Body}}<p class="muted" style="margin-top:10px">{{.Body}}</p>{{end}}
{{end}}
{{if eq .Kind "new"}}
<h3>New pull request</h3>
<form method="post" action="/pulls/new" class="box"><input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="two"><div><label>head (your branch)<br><select name="head">{{range .Branches}}<option>{{.}}</option>{{end}}</select></label></div>
<div><label>base<br><select name="base">{{range .Branches}}<option {{if eq . "main"}}selected{{end}}>{{.}}</option>{{end}}</select></label></div></div>
<p><label>title<br><input type="text" name="title" required></label></p>
<p><label>description<br><textarea name="body"></textarea></label></p>
<button class="primary">Open pull request</button></form>
{{end}}
{{if eq .Kind "pr"}}
<h3>#{{.PR.Number}} {{.PR.Title}} <span class="pill {{.PR.State}}">{{.PR.State}}</span></h3>
<p class="muted">{{.PR.Head}} ({{slice .HeadSHA 0 8}}) → {{.PR.Base}} ({{slice .BaseSHA 0 8}}) · opened by {{.PR.Author}} on {{.PR.Date}}</p>
{{if .Body}}<div class="box">{{.Body}}</div>{{end}}
<div class="two">
<div class="box"><b>Reviews</b> (policy: {{.Policy.RequiredApprovals}} approval(s){{range .Policy.RequiredChecks}}, check {{.}}{{end}})<br>
{{range .PR.Approved}}<span class="pill success">✓ {{.}}</span> {{end}}{{range .PR.Changes}}<span class="pill failure">✗ {{.}}</span> {{end}}{{if and (not .PR.Approved) (not .PR.Changes)}}<span class="muted">no reviews of the current head yet</span>{{end}}</div>
<div class="box"><b>Checks</b><br>{{range .PR.Checks}}<span class="pill {{.Status}}">{{.Name}}: {{.Status}}</span> <span class="muted">{{.Summary}}</span>{{if .Log}}<details><summary>log</summary><pre class="log">{{.Log}}</pre></details>{{end}}<br>{{else}}<span class="muted">no checks yet</span>{{end}}</div>
</div>
{{if eq .PR.State "open"}}
<div class="box">{{if .MergeBlock}}<p class="block">Cannot merge yet: {{.MergeBlock}}</p>{{else}}<p>Ready to merge.</p>{{end}}
<form method="post" action="/pull/{{.PR.ID}}/merge" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}">
<select name="method" style="width:auto"><option value="merge">merge commit</option><option value="squash">squash</option><option value="ff">fast-forward</option></select>
<button class="primary" {{if .MergeBlock}}disabled{{end}}>Merge</button></form>
<form method="post" action="/pull/{{.PR.ID}}/close" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="danger">Close</button></form></div>
{{end}}
<h4>Changes</h4>
{{if .DiffRows}}<div class="diff rows">{{range $i,$r := .DiffRows}}<div class="{{$r.Class}}"{{if $r.NewLine}} id="{{$r.Path}}-L{{$r.NewLine}}"{{end}}>{{if $r.NewLine}}<a class="add" href="#" data-path="{{$r.Path}}" data-line="{{$r.NewLine}}" title="comment on {{$r.Path}}:{{$r.NewLine}}">+</a><span class="ln">{{$r.NewLine}}</span>{{else}}<span class="ln"></span>{{end}}<span class="tx">{{$r.Text}}</span></div>{{range $r.Comments}}<div class="ic{{if .ResolvedBy}} done{{end}}"><span class="who">{{.Who}}</span>{{if .Agent}} <span class="pill agent">agent</span>{{end}}<span class="when">{{.When}}{{if .Moved}} · followed from line {{.Line}} of {{.Commit}}{{end}}</span>
{{if .ResolvedBy}}<span class="when">resolved by {{.ResolvedBy}}</span><details><summary>show</summary><div>{{.Body}}</div></details>{{else}}<div>{{.Body}}</div><form method="post" action="/pull/{{$.PR.ID}}/resolve" class="inline"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="comment" value="{{.ID}}"><button class="small">Resolve</button></form>{{end}}</div>{{end}}{{end}}</div>
{{else}}<div class="diff">{{.Diff}}</div>{{end}}
<h4>Conversation</h4>
{{range .Events}}{{if eq .Kind "comment"}}<div class="ev{{if .ResolvedBy}} done{{end}}"><span class="who">{{.Who}}</span>{{if .Agent}} <span class="pill agent">agent</span>{{end}}<span class="when">{{.When}}{{if .Path}} · {{.Path}}:{{.Line}} @{{.Commit}}{{if .Outdated}} · outdated{{end}}{{end}}{{if .ResolvedBy}} · resolved by {{.ResolvedBy}}{{end}}</span><div>{{.Body}}</div></div>
{{else if eq .Kind "review"}}<div class="ev review {{.Verdict}}"><span class="who">{{.Who}}</span>{{if .Agent}} <span class="pill agent">agent</span>{{end}} <span class="pill {{if eq .Verdict "approve"}}success{{else if eq .Verdict "request_changes"}}failure{{else}}pending{{end}}">{{.Verdict}}</span><span class="when">{{.When}} @{{.Commit}}</span>{{if .Body}}<div>{{.Body}}</div>{{end}}</div>
{{else if eq .Kind "state"}}<div class="ev"><span class="who">{{.Who}}</span> marked it <b>{{.State}}</b> <span class="when">{{.When}} {{.Commit}}</span></div>{{end}}{{end}}
{{if eq .PR.State "open"}}
<div class="two">
<form method="post" action="/pull/{{.PR.ID}}/comment" class="box"><input type="hidden" name="csrf" value="{{.CSRF}}"><b>Comment</b>
<textarea name="body" required placeholder="say something"></textarea>
<div class="two"><div><input type="text" name="path" placeholder="path (optional)"></div><div><input type="text" name="line" placeholder="line"></div></div>
<button>Comment</button></form>
<form method="post" action="/pull/{{.PR.ID}}/review" class="box"><input type="hidden" name="csrf" value="{{.CSRF}}"><b>Review {{slice .HeadSHA 0 8}}</b>
<textarea name="body" placeholder="summary (optional)"></textarea>
<select name="verdict"><option value="approve">approve</option><option value="request_changes">request changes</option><option value="comment">comment only</option></select>
<button class="primary">Submit review</button></form>
</div>
{{end}}
{{end}}
</main>
`))
