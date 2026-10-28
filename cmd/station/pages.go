package main

import (
	"html/template"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

func npub(pk nostr.PubKey) string { return nip19.EncodeNpub(pk) }

var stationCSS = []byte(`
:root { --fg: #111; --bg: #fff; --muted: #666; --line: #ddd; --accent: #0a66c2; --bar: #f4f4f4; --ok: #2a7; }
@media (prefers-color-scheme: dark) { :root { --fg: #eee; --bg: #111; --muted: #999; --line: #333; --accent: #4ea1ff; --bar: #1b1b1b; --ok: #5c9; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 15px/1.5 system-ui, sans-serif; }
a { color: var(--accent); }
header { display: flex; gap: 16px; align-items: center; padding: 10px 20px; background: var(--bar); border-bottom: 1px solid var(--line); }
header .brand { font-weight: 600; }
header nav { display: flex; gap: 14px; margin-left: auto; align-items: center; }
header .who { color: var(--muted); }
main { max-width: 900px; margin: 0 auto; padding: 20px; }
h1 { font-size: 20px; margin: 0 0 12px; }
h2 { font-size: 16px; margin: 28px 0 8px; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--line); vertical-align: top; }
th { color: var(--muted); font-weight: 500; }
.muted { color: var(--muted); }
.ok { color: var(--ok); }
button, input[type=submit] { font: inherit; color: var(--fg); background: var(--bg); border: 1px solid var(--line); border-radius: 4px; padding: 4px 10px; cursor: pointer; }
button.primary, input[type=submit].primary { background: var(--accent); color: #fff; border-color: var(--accent); }
button.link { border: none; background: none; color: var(--accent); padding: 0; }
input[type=text], input[type=password] { font: inherit; color: var(--fg); background: var(--bg); border: 1px solid var(--line); border-radius: 4px; padding: 5px 8px; }
form.inline { display: inline; }
form.row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin: 8px 0; }
.card { border: 1px solid var(--line); border-radius: 6px; padding: 12px 14px; margin: 10px 0; }
.secret { font-family: monospace; font-size: 16px; padding: 8px 10px; background: var(--bar); border-radius: 4px; word-break: break-all; }
.msg { padding: 8px 12px; border-left: 3px solid var(--accent); background: var(--bar); margin: 10px 0; }
.login { max-width: 360px; margin: 80px auto; }
.login input[type=password] { width: 100%; margin: 8px 0; }
code { font-family: monospace; background: var(--bar); padding: 1px 4px; border-radius: 3px; }
`)

var pages = template.Must(template.New("").Funcs(template.FuncMap{"npub": npub, "dict": dict}).Parse(`
{{define "head"}}<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title><link rel="stylesheet" href="/static/station.css"></head><body>{{end}}

{{define "header"}}<header><span class="brand">{{.Name}}</span>
<nav><a href="/">Devices</a>{{if .Principal.Admin}}<a href="/settings">Settings</a>{{end}}
<span class="who">{{.Principal.Subject}}</span>
<form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link" type="submit">Sign out</button></form></nav></header>{{end}}

{{define "login"}}{{template "head" (dict "Title" "Sign in")}}
<main class="login"><h1>Vibium Station</h1>
{{if .NoPassword}}<p class="msg">No password is set yet. On the station's machine, run <code>station passwd</code>.</p>{{end}}
{{if .Error}}<p class="msg">{{.Error}}</p>{{end}}
<form method="post" action="/login"><input type="hidden" name="next" value="{{.Next}}">
<label>Password<br><input type="password" name="password" autofocus autocomplete="current-password"></label>
<input class="primary" type="submit" value="Sign in"></form></main></body></html>{{end}}

{{define "index"}}{{template "head" (dict "Title" .Name)}}{{template "header" .}}
<main><h1>Devices</h1>
{{if not .Devices}}<p class="muted">No rigs yet. Make an enrollment code on <a href="/settings">Settings</a>, then on the rig run <code>roadie enroll --code …</code>.</p>{{end}}
{{if .Devices}}<table><tr><th>Device</th><th>Rig</th><th>Kind</th><th>Status</th><th>Controller</th><th></th></tr>
{{range .Devices}}<tr>
<td>{{if .ID}}{{.Name}}{{else}}<span class="muted">no devices announced</span>{{end}}</td>
<td>{{.Rig.Name}} <span class="muted">{{.Rig.ID}}</span></td>
<td>{{.Kind}}</td>
<td>{{if .Online}}<span class="ok">online</span>{{else}}<span class="muted">offline</span>{{end}}</td>
<td>{{if .Lease}}{{.Lease.Subject}} <span class="muted">lease {{.Lease.Seq}}</span>{{else}}<span class="muted">free</span>{{end}}</td>
<td>{{if .ID}}<a href="/devices/{{.ID}}/view">Open</a>{{end}}</td>
</tr>{{end}}</table>{{end}}
</main></body></html>{{end}}

{{define "settings"}}{{template "head" (dict "Title" "Settings")}}{{template "header" .}}
<main><h1>Settings</h1>
{{if .Message}}<p class="msg">{{.Message}}</p>{{end}}

<h2>Enroll a rig</h2>
{{if .NewCode}}<div class="card">On the rig, within 15 minutes (until {{.CodeExpires}}):
<p class="secret">roadie enroll {{.PublicURL}} --code {{.NewCode}}</p>
<p class="muted">On the same network the URL can be left out: the rig finds the station over mDNS. The code ends with this station's key fingerprint, {{.Fingerprint}}, which the rig checks.</p></div>{{end}}
<form class="row" method="post" action="/settings/codes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input class="primary" type="submit" value="New enrollment code"> <span class="muted">{{.OpenCodes}} unused</span></form>

<h2>Rigs</h2>
{{if not .Rigs}}<p class="muted">None enrolled.</p>{{end}}
{{if .Rigs}}<table><tr><th>Rig</th><th>Key</th><th>Devices</th><th>Enrolled</th><th></th></tr>
{{range .Rigs}}<tr><td>{{.Name}} <span class="muted">{{.ID}}</span></td><td><code>{{npub .PubKey}}</code></td>
<td>{{range .Devices}}{{.Name}} ({{.Kind}}) {{end}}</td><td>{{.EnrolledAt.Format "Jan 2, 2006"}}</td>
<td><form class="inline" method="post" action="/settings/rigs/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="rig" value="{{.ID}}"><button type="submit">Remove</button></form></td></tr>{{end}}</table>{{end}}

<h2>API tokens</h2>
<p class="muted">For agents and scripts: <code>Authorization: Bearer &lt;token&gt;</code>. A read-only token may watch, never control.</p>
{{if .NewToken}}<div class="card">Token <b>{{.NewTokenName}}</b>, shown once:<p class="secret">{{.NewToken}}</p></div>{{end}}
{{if .Tokens}}<table><tr><th>Name</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr>
{{range .Tokens}}<tr><td>{{.Name}}</td><td>{{if .ReadOnly}}read-only{{else}}control{{end}}</td><td>{{.CreatedAt.Format "Jan 2, 2006"}}</td>
<td>{{if .UsedAt.IsZero}}<span class="muted">never</span>{{else}}{{.UsedAt.Format "Jan 2 15:04"}}{{end}}</td>
<td><form class="inline" method="post" action="/settings/tokens/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button type="submit">Revoke</button></form></td></tr>{{end}}</table>{{end}}
<form class="row" method="post" action="/settings/tokens"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" placeholder="name" required> <label><input type="checkbox" name="read_only"> read-only</label>
<input type="submit" value="Create token"></form>

<h2>Password</h2>
{{if .NoPassword}}<p class="msg">No password is set; signing in by password is off until one is.</p>{{end}}
<form class="row" method="post" action="/settings/password"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="password" name="password" placeholder="new password" autocomplete="new-password"> <input type="password" name="again" placeholder="again" autocomplete="new-password">
<input type="submit" value="Change"></form>

<h2>Network</h2>
<table>
<tr><th>Mode</th><td>{{.Mode}}</td></tr>
<tr><th>Address</th><td>{{.PublicURL}}</td></tr>
<tr><th>Tunnel</th><td>{{.TunnelURL}}</td></tr>
<tr><th>mDNS</th><td>{{if .MDNS}}advertised on the LAN as _vibium-station._tcp{{else}}off{{end}}</td></tr>
<tr><th>Station key</th><td><code>{{.Npub}}</code> <span class="muted">fingerprint {{.Fingerprint}}</span></td></tr>
<tr><th>Tailnet identities</th><td>{{if .Allow}}{{range .Allow}}{{.}} {{end}}{{else}}<span class="muted">none (--allow)</span>{{end}}</td></tr>
<tr><th>TURN</th><td><span class="muted">not yet (R4)</span></td></tr>
</table>
</main></body></html>{{end}}
`))

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}
