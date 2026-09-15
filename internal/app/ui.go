package app

import (
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"secretgit/internal/config"
	"secretgit/internal/gitx"
)

// DefaultUIAddr is where the local UI listens.
const DefaultUIAddr = "127.0.0.1:7391"

// UIOptions configures UI.
type UIOptions struct {
	Dir    string
	Listen string
	Open   bool
}

// UI serves a read-only code browser over the mirror (or, for repositories
// without a mirror, over the local repository). It mirrors the URL shapes
// of hosted forges so links look and behave the way people expect:
// /blob/<ref>/<path>#L10, /tree/<ref>/<path>, /commit/<sha>, /commits/<ref>,
// /blame/<ref>/<path>, /search?q=. Refs containing slashes use a "/-/"
// separator before the path.
func (a *App) UI(o UIOptions) error {
	work, gitDir, err := locateRepo(o.Dir)
	if err != nil {
		return err
	}
	paths := config.NewPaths(gitDir)
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	repoDir := gitDir
	source := "local repository"
	if ms, err := config.LoadMirrorState(paths); err == nil && ms.AppliedGeneration > 0 {
		repoDir = paths.Mirror
		source = "vault mirror"
	}
	if o.Listen == "" {
		o.Listen = DefaultUIAddr
	}
	host, _, _ := net.SplitHostPort(o.Listen)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		a.logf("WARNING: listening on %s exposes plaintext source to everyone who can reach that address; use it only on a private network (Tailscale, WireGuard)", o.Listen)
	}
	s := &uiServer{repo: repoDir, name: cfg.Label, work: work, source: source, app: a, csrf: newToken()}
	mux := http.NewServeMux()
	s.collabRoutes(mux)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /tree/{rest...}", s.tree)
	mux.HandleFunc("GET /blob/{rest...}", s.blob)
	mux.HandleFunc("GET /raw/{rest...}", s.raw)
	mux.HandleFunc("GET /blame/{rest...}", s.blame)
	mux.HandleFunc("GET /commits/{rest...}", s.commits)
	mux.HandleFunc("GET /commit/{sha}", s.commit)
	mux.HandleFunc("GET /search", s.search)
	ln, err := net.Listen("tcp", o.Listen)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	a.logf("secretgit ui: %s  (%s: %s)", url, source, repoDir)
	if o.Open {
		openBrowser(url)
	}
	return http.Serve(ln, mux)
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	}
}

type uiServer struct {
	repo, name, work, source string
	app                      *App
	csrf                     string
}

// splitRefPath resolves "<ref>/<path>" or "<ref-with-slashes>/-/<path>".
func (s *uiServer) splitRefPath(rest string) (ref, p string) {
	if i := strings.Index(rest, "/-/"); i >= 0 {
		return rest[:i], rest[i+3:]
	}
	ref, p, _ = strings.Cut(rest, "/")
	return ref, p
}

type crumb struct{ Name, URL string }

type page struct {
	Repo, Source, Title, Ref, Path, Kind string
	Crumbs                               []crumb
	Branches, Tags                       []refLine
	Entries                              []treeEntry
	Lines                                []codeLine
	Commits                              []commitLine
	Diff                                 template.HTML
	Query                                string
	Hits                                 []hit
	Message                              string
	Blame                                bool
}

type refLine struct{ Name, SHA, Subject, Date string }
type treeEntry struct {
	Name, URL string
	Dir       bool
}
type codeLine struct {
	N           int
	Text        string
	Author, SHA string
}
type commitLine struct{ SHA, Short, Subject, Author, Date string }
type hit struct {
	Path, URL string
	Line      int
	Text      string
}

func (s *uiServer) render(w http.ResponseWriter, p *page) {
	p.Repo, p.Source = s.name, s.source
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	if err := uiTmpl.Execute(w, p); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *uiServer) fail(w http.ResponseWriter, err error) {
	var ge *gitx.Error
	if errors.As(err, &ge) {
		http.Error(w, strings.TrimSpace(ge.Stderr), 404)
		return
	}
	http.Error(w, err.Error(), 500)
}

func crumbs(kind, ref, p string) []crumb {
	base := "/" + kind + "/" + refSeg(ref)
	out := []crumb{{Name: "root", URL: "/tree/" + refSeg(ref)}}
	acc := ""
	for _, part := range strings.Split(p, "/") {
		if part == "" {
			continue
		}
		acc = path.Join(acc, part)
		out = append(out, crumb{Name: part, URL: base + "/" + acc})
	}
	_ = base
	return out
}

func refSeg(ref string) string {
	if strings.Contains(ref, "/") {
		return ref + "/-"
	}
	return ref
}

func (s *uiServer) home(w http.ResponseWriter, r *http.Request) {
	p := &page{Kind: "home", Title: s.name}
	for _, kind := range []string{"refs/heads/", "refs/tags/"} {
		out, err := gitx.Run(s.repo, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)%09%(objectname:short)%09%(subject)%09%(committerdate:short)", kind)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			f := strings.SplitN(l, "\t", 4)
			if len(f) < 3 {
				continue
			}
			rl := refLine{Name: f[0], SHA: f[1], Subject: f[2]}
			if len(f) == 4 {
				rl.Date = f[3]
			}
			if kind == "refs/heads/" {
				p.Branches = append(p.Branches, rl)
			} else {
				p.Tags = append(p.Tags, rl)
			}
		}
	}
	s.render(w, p)
}

func (s *uiServer) tree(w http.ResponseWriter, r *http.Request) {
	ref, tp := s.splitRefPath(r.PathValue("rest"))
	spec := ref + ":" + tp
	if tp != "" && !strings.HasSuffix(tp, "/") {
		spec += "/"
	}
	out, err := gitx.Run(s.repo, "ls-tree", "--format=%(objecttype)\t%(path)", spec)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &page{Kind: "tree", Title: tp, Ref: ref, Path: tp, Crumbs: crumbs("tree", ref, tp)}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		t, name, ok := strings.Cut(l, "\t")
		if !ok {
			continue
		}
		full := path.Join(tp, name)
		e := treeEntry{Name: name, Dir: t == "tree"}
		if e.Dir {
			e.URL = "/tree/" + refSeg(ref) + "/" + full
		} else {
			e.URL = "/blob/" + refSeg(ref) + "/" + full
		}
		p.Entries = append(p.Entries, e)
	}
	s.render(w, p)
}

func (s *uiServer) blob(w http.ResponseWriter, r *http.Request) {
	ref, bp := s.splitRefPath(r.PathValue("rest"))
	out, err := gitx.Run(s.repo, "show", ref+":"+bp)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &page{Kind: "blob", Title: bp, Ref: ref, Path: bp, Crumbs: crumbs("blob", ref, bp)}
	if strings.IndexByte(out, 0) >= 0 {
		p.Message = "binary file"
	} else {
		for i, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			p.Lines = append(p.Lines, codeLine{N: i + 1, Text: l})
		}
	}
	s.render(w, p)
}

func (s *uiServer) raw(w http.ResponseWriter, r *http.Request) {
	ref, bp := s.splitRefPath(r.PathValue("rest"))
	out, err := gitx.Run(s.repo, "show", ref+":"+bp)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(out))
}

func (s *uiServer) blame(w http.ResponseWriter, r *http.Request) {
	ref, bp := s.splitRefPath(r.PathValue("rest"))
	out, err := gitx.Run(s.repo, "blame", "--line-porcelain", ref, "--", bp)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &page{Kind: "blob", Blame: true, Title: bp, Ref: ref, Path: bp, Crumbs: crumbs("blob", ref, bp)}
	var cur codeLine
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			cur.N = len(p.Lines) + 1
			cur.Text = l[1:]
			p.Lines = append(p.Lines, cur)
		case strings.HasPrefix(l, "author "):
			cur.Author = strings.TrimPrefix(l, "author ")
		case len(l) >= 40 && !strings.Contains(l[:40], " "):
			cur.SHA = l[:8]
		}
	}
	s.render(w, p)
}

func (s *uiServer) commits(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSuffix(r.PathValue("rest"), "/-")
	out, err := gitx.Run(s.repo, "log", "-n", "100", "--format=%H%x09%h%x09%s%x09%an%x09%as", ref, "--")
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &page{Kind: "commits", Title: "commits on " + ref, Ref: ref}
	p.Commits = parseCommits(out)
	s.render(w, p)
}

func parseCommits(out string) []commitLine {
	var cs []commitLine
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(l, "\t", 5)
		if len(f) < 5 {
			continue
		}
		cs = append(cs, commitLine{SHA: f[0], Short: f[1], Subject: f[2], Author: f[3], Date: f[4]})
	}
	return cs
}

func (s *uiServer) commit(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	out, err := gitx.Run(s.repo, "show", "--stat", "--patch", "--format=commit %H%nAuthor: %an <%ae>%nDate:   %ad%n%n    %s%n%n%b", sha)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := &page{Kind: "commit", Title: "commit " + sha[:min(8, len(sha))], Ref: sha, Diff: renderDiff(out)}
	s.render(w, p)
}

// renderDiff colours a unified diff; everything is HTML-escaped first.
func renderDiff(text string) template.HTML {
	var b strings.Builder
	for _, l := range strings.Split(text, "\n") {
		cls := ""
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			cls = "h"
		case strings.HasPrefix(l, "+"):
			cls = "a"
		case strings.HasPrefix(l, "-"):
			cls = "d"
		case strings.HasPrefix(l, "@@"):
			cls = "k"
		case strings.HasPrefix(l, "diff "):
			cls = "f"
		}
		b.WriteString("<div class=\"" + cls + "\">" + template.HTMLEscapeString(l) + "</div>")
	}
	return template.HTML(b.String())
}

func (s *uiServer) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = "HEAD"
	}
	p := &page{Kind: "search", Title: "search", Query: q, Ref: ref}
	if q != "" {
		out, err := gitx.Run(s.repo, "grep", "-n", "-I", "--max-count=50", "-e", q, ref, "--")
		if err == nil {
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				// <ref>:<path>:<line>:<text>
				rest := strings.TrimPrefix(l, ref+":")
				fp, rest, ok := strings.Cut(rest, ":")
				if !ok {
					continue
				}
				ln, text, _ := strings.Cut(rest, ":")
				n, _ := strconv.Atoi(ln)
				p.Hits = append(p.Hits, hit{Path: fp, Line: n, Text: text, URL: "/blob/" + refSeg(ref) + "/" + fp + "#L" + ln})
			}
		}
	}
	s.render(w, p)
}

// Link prints a permalink into the local UI for a path and line.
func (a *App) Link(dir, target, ref string) error {
	work, _, err := locateRepo(dir)
	if err != nil {
		return err
	}
	p, line, _ := strings.Cut(target, ":")
	if abs, err := absRel(work, p); err == nil {
		p = abs
	}
	if ref == "" {
		out, err := gitx.Run(work, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		ref = strings.TrimSpace(out)
	}
	url := "http://" + DefaultUIAddr + "/blob/" + refSeg(ref) + "/" + p
	if line != "" {
		url += "#L" + line
	}
	fmt.Fprintln(a.Out, url)
	return nil
}

// absRel turns a path (absolute, or relative to the current directory)
// into a work-tree-relative path, resolving symlinked prefixes.
func absRel(work, p string) (string, error) {
	if !filepath.IsAbs(p) {
		wd, _ := os.Getwd()
		p = filepath.Join(wd, p)
	}
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		p = rp
	}
	if rw, err := filepath.EvalSymlinks(work); err == nil {
		work = rw
	}
	rel, err := filepath.Rel(work, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("outside work tree")
	}
	return filepath.ToSlash(rel), nil
}

// uiCSS is shared by every page.
const uiCSS = `<style>
body{font:14px/1.45 -apple-system,system-ui,sans-serif;margin:0;color:#222;background:#fff}
header{background:#24292f;color:#fff;padding:10px 16px;display:flex;gap:16px;align-items:center}
header a{color:#fff;text-decoration:none;font-weight:600}header a.nav{font-weight:400;opacity:.85}header form{margin-left:auto}
header input{padding:4px 8px;border-radius:4px;border:1px solid #888;width:260px}
header small{opacity:.7}
main{padding:12px 16px;max-width:1200px}
nav.crumbs a{color:#0969da;text-decoration:none}nav.crumbs{margin-bottom:10px}
table{border-collapse:collapse;width:100%}td,th{padding:4px 8px;text-align:left;border-bottom:1px solid #eee;vertical-align:top}
a{color:#0969da}
pre.code{margin:0;border:1px solid #ddd;border-radius:6px;overflow:auto;font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}
pre.code div{display:flex;white-space:pre}
pre.code div:target{background:#fff8c5}
pre.code .n{width:52px;text-align:right;padding:0 8px;color:#999;user-select:none;flex:none}
pre.code .n a{color:#999;text-decoration:none}
pre.code .bl{width:200px;flex:none;color:#666;padding:0 8px;overflow:hidden;text-overflow:ellipsis}
pre.code .t{padding:0 8px}
.diff{font:12px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;border:1px solid #ddd;border-radius:6px;overflow:auto}
.diff div{white-space:pre;padding:0 8px}.diff .a{background:#e6ffec}.diff .d{background:#ffebe9}.diff .k{color:#0969da;background:#f6f8fa}.diff .f{font-weight:600;background:#f6f8fa;margin-top:8px}.diff .h{color:#666}
.sha{font-family:ui-monospace,Menlo,monospace;color:#666}
.muted{color:#666}
</style>
`

var uiTmpl = template.Must(template.New("ui").Parse(`<!doctype html>
<meta charset="utf-8"><title>{{.Repo}}: {{.Title}}</title>
` + uiCSS + `<header><a href="/">{{.Repo}}</a><a href="/pulls" class="nav">pull requests</a><small>{{.Source}}</small>
<form action="/search"><input name="q" placeholder="search code" value="{{.Query}}"><input type="hidden" name="ref" value="{{if .Ref}}{{.Ref}}{{else}}HEAD{{end}}"></form></header>
<main>
{{if .Crumbs}}<nav class="crumbs">{{range $i,$c := .Crumbs}}{{if $i}} / {{end}}<a href="{{$c.URL}}">{{$c.Name}}</a>{{end}}
{{if eq .Kind "blob"}} &nbsp;<span class="muted">{{if .Blame}}<a href="/blob/{{.Ref}}/{{.Path}}">normal</a>{{else}}<a href="/blame/{{.Ref}}/{{.Path}}">blame</a>{{end}} · <a href="/raw/{{.Ref}}/{{.Path}}">raw</a> · <a href="/commits/{{.Ref}}">history</a></span>{{end}}</nav>{{end}}
{{if eq .Kind "home"}}
<h3>Branches</h3><table>{{range .Branches}}<tr><td><a href="/tree/{{.Name}}">{{.Name}}</a></td><td><a class="sha" href="/commit/{{.SHA}}">{{.SHA}}</a></td><td>{{.Subject}}</td><td class="muted">{{.Date}}</td><td><a href="/commits/{{.Name}}">commits</a></td></tr>{{end}}</table>
{{if .Tags}}<h3>Tags</h3><table>{{range .Tags}}<tr><td><a href="/tree/{{.Name}}">{{.Name}}</a></td><td><a class="sha" href="/commit/{{.SHA}}">{{.SHA}}</a></td><td>{{.Subject}}</td><td class="muted">{{.Date}}</td></tr>{{end}}</table>{{end}}
{{end}}
{{if eq .Kind "tree"}}<table>{{range .Entries}}<tr><td>{{if .Dir}}📁{{else}}📄{{end}} <a href="{{.URL}}">{{.Name}}</a></td></tr>{{end}}</table>{{end}}
{{if eq .Kind "blob"}}{{if .Message}}<p class="muted">{{.Message}}</p>{{else}}<pre class="code">{{range .Lines}}<div id="L{{.N}}"><span class="n"><a href="#L{{.N}}">{{.N}}</a></span>{{if $.Blame}}<span class="bl">{{.SHA}} {{.Author}}</span>{{end}}<span class="t">{{.Text}}</span></div>{{end}}</pre>{{end}}{{end}}
{{if eq .Kind "commits"}}<h3>{{.Title}}</h3><table>{{range .Commits}}<tr><td><a class="sha" href="/commit/{{.SHA}}">{{.Short}}</a></td><td>{{.Subject}}</td><td class="muted">{{.Author}}</td><td class="muted">{{.Date}}</td></tr>{{end}}</table>{{end}}
{{if eq .Kind "commit"}}<div class="diff">{{.Diff}}</div>{{end}}
{{if eq .Kind "search"}}<h3>{{if .Query}}results for “{{.Query}}” in {{.Ref}}{{else}}search{{end}}</h3><table>{{range .Hits}}<tr><td><a href="{{.URL}}">{{.Path}}:{{.Line}}</a></td><td><code>{{.Text}}</code></td></tr>{{end}}</table>{{end}}
</main>
<script>if(location.hash){var e=document.getElementById(location.hash.slice(1));e&&e.scrollIntoView({block:"center"})}</script>
`))
