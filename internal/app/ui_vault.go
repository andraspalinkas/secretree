package app

import (
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andraspalinkas/secretree/internal/config"
	"github.com/andraspalinkas/secretree/internal/gitx"
	"github.com/andraspalinkas/secretree/internal/vault"
)

// ledgerRow and vaultPage feed the two "why trust this" pages.
type ledgerRow struct {
	Seq                        int
	When, Kind, Subject, Actor string
	Note, Expires              string
	Expired                    bool
}

type genRow struct {
	Num        int
	Kind, When string
	Refs       int
	Size       string
	Opaque     bool
}

type hostFile struct {
	Path string
	Size string
}

type vaultPage struct {
	page
	Ledger     []ledgerRow
	Gens       []genRow
	Files      []hostFile
	Meta       string
	VaultURL   string
	VaultID    string
	Members    int
	Signers    int
	TotalSize  string
	LastBackup string
	LastProof  string
	KitPending bool
}

func (s *uiServer) ledger(w http.ResponseWriter, r *http.Request) {
	rp, err := s.app.openRepo(s.work)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	vs, err := s.app.loadVault(rp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	entries, _, err := loadLedger(rp, vs)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	p := &vaultPage{page: page{Kind: "ledger", Title: "disclosure ledger"}}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		row := ledgerRow{Seq: e.Seq, When: e.Created.Format("2006-01-02 15:04"), Kind: e.Kind, Subject: e.Subject, Actor: e.ActorName, Note: e.Note}
		if row.Actor == "" {
			row.Actor = e.Actor
		}
		if e.Expires != nil {
			row.Expires = e.Expires.Format("2006-01-02")
			row.Expired = time.Now().After(*e.Expires)
		}
		p.Ledger = append(p.Ledger, row)
	}
	s.renderVault(w, p)
}

func (s *uiServer) vault(w http.ResponseWriter, r *http.Request) {
	rp, err := s.app.openRepo(s.work)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	vs, err := s.app.loadVault(rp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	p := &vaultPage{page: page{Kind: "vault", Title: "vault"}, VaultURL: rp.Cfg.VaultURL, VaultID: vs.Meta.VaultID,
		Members: len(vs.Meta.Recipients), Signers: len(vs.Signers.Active), TotalSize: humanBytes(vs.Chain.Bytes())}
	for _, g := range vs.Chain.Gens {
		row := genRow{Num: g.Num, Opaque: g.Opaque()}
		if !g.Opaque() {
			var size int64
			for _, f := range g.Manifest.Files {
				size += f.Size
			}
			row.Kind, row.When, row.Refs, row.Size = g.Manifest.Kind, g.Manifest.Created.Format("2006-01-02 15:04"), len(g.Manifest.Refs), humanBytes(size)
		}
		p.Gens = append(p.Gens, row)
	}
	sort.Slice(p.Gens, func(i, j int) bool { return p.Gens[i].Num > p.Gens[j].Num })
	if out, err := gitx.Run(rp.Paths.Cache, "ls-tree", "-r", "-l", "HEAD"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			meta, name, ok := strings.Cut(l, "\t")
			if !ok {
				continue
			}
			f := strings.Fields(meta)
			size := ""
			if len(f) == 4 {
				if n, err := strconv.ParseInt(f[3], 10, 64); err == nil {
					size = humanBytes(n)
				}
			}
			p.Files = append(p.Files, hostFile{Path: name, Size: size})
		}
	}
	if raw, err := vs.Reader.ReadFile(vault.MetaFile); err == nil {
		p.Meta = string(raw)
	}
	if st, err := loadStatusFor(rp); err == nil {
		p.LastBackup = agoShort(st.LastBackup)
		p.LastProof = agoShort(st.LastProof)
		p.KitPending = st.KitPending && st.KitConfirmed == nil
	}
	s.renderVault(w, p)
}

func (s *uiServer) renderVault(w http.ResponseWriter, p *vaultPage) {
	p.Repo, p.Source = s.name, s.source
	p.Proof, p.ProofOK = s.proofBadge()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	if err := vaultTmpl.Execute(w, p); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

var vaultTmpl = template.Must(template.New("vault").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Repo}}: {{.Title}}</title>
` + uiCSS + `<style>
.stat{display:inline-block;margin:0 18px 8px 0}.stat b{display:block;font-size:20px;font-weight:600}.stat span{color:var(--fg2);font-size:12px}
.kind{display:inline-block;padding:0 6px;border-radius:8px;font-size:11px;background:var(--bg2);border:1px solid var(--line)}
.kind.share{border-color:var(--purple);color:var(--purple)}.kind.export{border-color:var(--warn);color:var(--warn)}.kind.public-mirror{border-color:var(--bad);color:var(--bad)}
pre.meta{background:var(--bg2);border:1px solid var(--line);border-radius:6px;padding:10px;font-size:12px;overflow:auto}
.expired{color:var(--fg2);text-decoration:line-through}
</style>
` + uiHeader + `
<main>
{{if eq .Kind "ledger"}}
<h3>Disclosure ledger</h3>
<p class="muted">Everything that ever left the key boundary on purpose: share pages, exports to services, public mirrors. Each entry is encrypted to the members, signed by the device that made it, and hash-chained to the previous one, so the list can be neither forged nor silently trimmed.</p>
{{if .Ledger}}<table><tr><th>#</th><th>when</th><th>kind</th><th>what left</th><th>by</th><th>expires</th><th>why</th></tr>
{{range .Ledger}}<tr><td class="muted">{{printf "%06d" .Seq}}</td><td>{{.When}}</td><td><span class="kind {{.Kind}}">{{.Kind}}</span></td><td class="cipher">{{.Subject}}</td><td>{{.Actor}}</td><td{{if .Expired}} class="expired"{{end}}>{{.Expires}}</td><td class="muted">{{.Note}}</td></tr>{{end}}</table>
{{else}}<div class="box"><b>Nothing has left the vault in plaintext.</b> <span class="muted">No share pages, no exports. When a member runs <code>secretree share</code> or an agent sends a diff to a model, it will be listed here.</span></div>{{end}}
{{end}}
{{if eq .Kind "vault"}}
<h3>The vault as the host sees it</h3>
<div class="box">
<div class="stat"><b>{{len .Gens}}</b><span>generations</span></div>
<div class="stat"><b>{{.TotalSize}}</b><span>ciphertext on the remote</span></div>
<div class="stat"><b>{{.Members}}</b><span>recipients</span></div>
<div class="stat"><b>{{.Signers}}</b><span>signers</span></div>
<div class="stat"><b>{{.LastBackup}}</b><span>last backup</span></div>
<div class="stat"><b>{{.LastProof}}</b><span>last restore proof</span></div>
<div class="muted">{{.VaultURL}} · vault id {{.VaultID}}</div>
{{if .KitPending}}<p class="block" style="margin:10px 0 0">The recovery kit of this vault has not been confirmed as printed. <code>secretree kit --confirm</code></p>{{end}}
</div>
<div class="two">
<div>
<h4>Files on the remote</h4>
<p class="muted">Real listing of the vault repository. Nothing here is readable without a member's key; the names carry only generation numbers.</p>
<table><tr><th>path</th><th>size</th></tr>{{range .Files}}<tr><td class="cipher">{{.Path}}</td><td class="muted">{{.Size}}</td></tr>{{end}}</table>
</div>
<div>
<h4>Generations</h4>
<table><tr><th>#</th><th>kind</th><th>written</th><th>refs</th><th>size</th></tr>
{{range .Gens}}<tr><td class="muted">{{printf "%06d" .Num}}</td>{{if .Opaque}}<td colspan="4" class="muted">opaque: written before this key was a member (signature and chain verified)</td>{{else}}<td>{{.Kind}}</td><td>{{.When}}</td><td>{{.Refs}}</td><td class="muted">{{.Size}}</td>{{end}}</tr>{{end}}</table>
<h4>vault.json (the only plaintext metadata)</h4>
<pre class="meta">{{.Meta}}</pre>
</div>
</div>
{{end}}
</main>
`))

func loadStatusFor(r *repo) (*statusView, error) {
	st, err := configLoadStatus(r)
	if err != nil {
		return nil, err
	}
	return st, nil
}

type statusView = config.Status

func configLoadStatus(r *repo) (*config.Status, error) { return config.LoadStatus(r.Paths) }

func agoShort(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t).Round(time.Minute)
	if d < time.Minute {
		return "just now"
	}
	return d.String() + " ago"
}
