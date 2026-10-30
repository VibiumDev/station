package main

import (
	"html/template"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

func npub(pk nostr.PubKey) string { return nip19.EncodeNpub(pk) }

// platformName is a platform as people write it.
func platformName(p string) string {
	switch p {
	case "ios":
		return "iOS"
	case "android":
		return "Android"
	case "macos", "mac":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return p
}

// The station's pages, in the look of Vibium's web style guide: the
// shared stylesheet and theme switch come from the viewer package, so a
// Roadie's pages are the same. Signed in, the top bar is the station's
// own name; the brand is on the sign-in page and in every foot.
var pages = template.Must(template.New("").Funcs(template.FuncMap{"npub": npub, "dict": dict, "platform": platformName}).Parse(`
{{define "head"}}<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · {{if .Named}}{{.Name}}{{else}}Vibium Station{{end}}</title><link rel="icon" href="data:,">
<link rel="stylesheet" href="/viewer/pages.css"><script src="/viewer/pages.js"></script></head><body>{{end}}

{{define "theme"}}<button id="themeBtn" class="iconbtn" type="button" aria-label="Switch theme"></button>{{end}}

{{define "header"}}<header class="top"><div class="wrap">
<a class="brand" href="/"><span class="wordmark">{{if .Named}}{{.Name}}{{else}}Station{{end}}</span></a>
<nav class="pages"><a href="/"{{if eq (printf "%v" .Page) "index"}} aria-current="page"{{end}}>Devices</a>{{if .Principal.Admin}}<a href="/settings"{{if eq (printf "%v" .Page) "settings"}} aria-current="page"{{end}}>Settings</a>{{end}}</nav>
<div class="account"><span class="who">{{.Principal.Subject}}</span>
<form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link" type="submit">Sign out</button></form></div>
{{template "theme"}}<span class="break"></span></div></header>{{end}}

{{define "foot"}}<footer class="foot"><div class="wrap"><span class="wordmark">Vibium</span>
<span class="line">{{.Host}}</span>
<span><a href="https://vibium.dev">Open source ↗</a></span></div></footer></body></html>{{end}}

{{define "kicker"}}{{if .Named}}<p class="kicker">{{.Name}}</p>{{end}}{{end}}

{{define "login"}}{{template "head" (dict "Title" "Sign in" "Name" .Name "Named" .Named)}}
<header class="top"><div class="wrap"><a class="brand" href="/"><span class="wordmark">Vibium</span><span class="product">Station</span></a>{{template "theme"}}</div></header>
<main class="wrap login">{{template "kicker" .}}<h1 class="h1">Sign in.</h1>
{{if .NoPassword}}<div class="msg">No password is set yet. On the station's machine, run <code>station passwd</code>.</div>{{end}}
{{if .Error}}<div class="msg error" role="alert">{{.Error}}</div>{{end}}
<div class="card"><form method="post" action="/login"><input type="hidden" name="next" value="{{.Next}}">
<label for="password">Password</label><input id="password" type="password" name="password" autofocus autocomplete="current-password">
<input class="primary" type="submit" value="Sign in"></form></div></main>
{{template "foot" .}}{{end}}

{{define "index"}}{{template "head" (dict "Title" "Devices" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap"><h1 class="h1">Devices.</h1>
<p class="lead">Open a device to drive it, or to watch when someone else has it.</p>
{{if not .Devices}}<div class="msg">No rigs yet. Make an enrollment code on <a href="/settings">Settings</a>, then on the rig run <code>roadie enroll --code …</code>.</div>{{end}}
{{if .Devices}}<div class="tablewrap"><table><thead><tr><th>Device</th><th>Rig</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Devices}}<tr>
<td data-l="Device">{{if .ID}}<b>{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</b><span class="sub">{{.Kind}}{{if .Platform}} · {{platform .Platform}}{{end}}{{if .Label}} · {{.Name}}{{end}}</span>{{else}}<span class="muted">no devices announced</span>{{end}}</td>
<td data-l="Rig"><b>{{.Rig.Name}}</b><span class="sub">{{.Rig.ID}}</span></td>
<td data-l="Status">{{if .Online}}<span class="dot on"></span>online{{else}}<span class="dot"></span><span class="muted">offline</span>{{end}}<span class="sub">{{if .Hold}}held by {{.Hold.Subject}}{{else}}available{{end}}{{if .Waiting}} · {{.Waiting}} waiting{{end}}</span></td>
<td class="act">{{if .ID}}<a class="btn small" href="/devices/{{.ID}}/view">Open</a>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "settings"}}{{template "head" (dict "Title" "Settings" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap"><h1 class="h1">Settings.</h1>
{{if .Message}}<div class="msg" role="alert">{{.Message}}</div>{{end}}

<h2 class="h2">Name</h2>
<p class="muted">What this station is called: on these pages, and to rigs when they enroll.</p>
<form class="row" method="post" action="/settings/name"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" value="{{.Name}}" aria-label="Station name" maxlength="60" required> <input type="submit" value="Save"></form>

<h2 class="h2">Enroll a rig</h2>
{{if .NewCode}}<div class="card featured">On the rig, within 15 minutes (until {{.CodeExpires}}):
<p class="secret">roadie enroll {{.PublicURL}} --code {{.NewCode}}</p>
<p class="muted">On the same network the URL can be left out: the rig finds the station over mDNS. The code ends with this station's key fingerprint, {{.Fingerprint}}, which the rig checks.</p></div>{{end}}
<form class="row" method="post" action="/settings/codes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input class="primary" type="submit" value="New enrollment code"> <span class="muted">{{.OpenCodes}} unused</span></form>

<h2 class="h2">Rigs</h2>
{{if not .Rigs}}<p class="muted">None enrolled.</p>{{end}}
{{if .Rigs}}<div class="tablewrap"><table><thead><tr><th>Rig</th><th>Key</th><th>Devices</th><th>Enrolled</th><th></th></tr></thead><tbody>
{{range .Rigs}}<tr><td data-l="Rig"><b>{{.Name}}</b><span class="sub">{{.ID}}</span></td><td data-l="Key"><code>{{npub .PubKey}}</code></td>
<td data-l="Devices">{{range .Devices}}{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}} <span class="muted">({{.Kind}}{{if .Platform}}, {{platform .Platform}}{{end}})</span> {{end}}</td><td data-l="Enrolled">{{.EnrolledAt.Format "Jan 2, 2006"}}</td>
<td class="act"><form class="inline" method="post" action="/settings/rigs/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="rig" value="{{.ID}}"><button class="small" type="submit">Remove</button></form></td></tr>{{end}}</tbody></table></div>{{end}}

<h2 class="h2">API tokens</h2>
<p class="muted">For agents and scripts: <code>Authorization: Bearer &lt;token&gt;</code>. A read-only token may watch, never control.</p>
{{if .NewToken}}<div class="card featured">Token <b>{{.NewTokenName}}</b>, shown once:<p class="secret">{{.NewToken}}</p></div>{{end}}
{{if .Tokens}}<div class="tablewrap"><table><thead><tr><th>Name</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr></thead><tbody>
{{range .Tokens}}<tr><td data-l="Name"><b>{{.Name}}</b></td><td data-l="Access">{{if .ReadOnly}}read-only{{else}}control{{end}}</td><td data-l="Created">{{.CreatedAt.Format "Jan 2, 2006"}}</td>
<td data-l="Last used">{{if .UsedAt.IsZero}}<span class="muted">never</span>{{else}}{{.UsedAt.Format "Jan 2 15:04"}}{{end}}</td>
<td class="act"><form class="inline" method="post" action="/settings/tokens/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small" type="submit">Revoke</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
<form class="row" method="post" action="/settings/tokens"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" placeholder="name" aria-label="Token name" required> <label class="check"><input type="checkbox" name="read_only"> read-only</label>
<input type="submit" value="Create token"></form>

<h2 class="h2">Password</h2>
{{if .NoPassword}}<div class="msg">No password is set; signing in by password is off until one is.</div>{{end}}
<form class="row" method="post" action="/settings/password"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="password" name="password" placeholder="new password" aria-label="New password" autocomplete="new-password"> <input type="password" name="again" placeholder="again" aria-label="New password again" autocomplete="new-password">
<input type="submit" value="Change"></form>

<h2 class="h2">Network</h2>
<div class="tablewrap"><table class="kv">
<tr><th>Mode</th><td>{{.Mode}}</td></tr>
<tr><th>Address</th><td>{{.PublicURL}}</td></tr>
<tr><th>Tunnel</th><td>{{.TunnelURL}}</td></tr>
<tr><th>mDNS</th><td>{{if .MDNS}}advertised on the LAN as _vibium-station._tcp{{else}}off{{end}}</td></tr>
<tr><th>Station key</th><td><code>{{.Npub}}</code> <span class="muted">fingerprint {{.Fingerprint}}</span></td></tr>
<tr><th>Tailnet identities</th><td>{{if .Allow}}{{range .Allow}}{{.}} {{end}}{{else}}<span class="muted">none (--allow)</span>{{end}}</td></tr>
<tr><th>TURN</th><td>{{if .TURN}}{{.TURN}}{{else}}<span class="muted">off (--turn on)</span>{{end}}</td></tr>
</table></div>
</main>{{template "foot" .}}{{end}}
`))

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}
