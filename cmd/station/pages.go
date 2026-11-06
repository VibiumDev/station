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
<nav class="pages"><a href="/"{{if eq (printf "%v" .Page) "index"}} aria-current="page"{{end}}>Devices</a><a href="/records"{{if eq (printf "%v" .Page) "records"}} aria-current="page"{{end}}>Records</a>{{if .Principal.Admin}}<a href="/settings"{{if eq (printf "%v" .Page) "settings"}} aria-current="page"{{end}}>Settings</a>{{end}}</nav>
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
<main class="wrap"><p class="lead">Open a device to drive it, or to watch when someone else has it. {{template "help" "devices"}}</p>
<div class="helptext" id="help-devices" hidden>
<p>Every device on every rig enrolled here. Open takes a device nobody holds, and you drive it: the picture, touch, keys and sound. A device someone else holds opens watching, and Hold in the viewer takes it from them; an agent's run ends when you do. Release in the viewer gives the device back and returns here. A hold also ends after 5 idle minutes, or after 8 hours.</p>
<p><b>Device.</b> The device's label, or its name when the rig gave it no label, with its kind and platform under it. The name is the last part of the device's address, rig/name, which holds and the API use; Roadie's <code>--label</code> sets the label and <code>--platform</code> the platform.</p>
<p><b>Rig.</b> The machine the device is wired to: the name it enrolled with, and the id the station gave it.</p>
<p><b>Status.</b> Online while the rig's tunnel to the station is up, offline otherwise. Under it, available when nobody holds the device, or who holds it and how many agents are waiting their turn.</p>
</div>
{{if not .Devices}}<div class="msg">No rigs yet. Make an enrollment code on <a href="/settings">Settings</a>, then on the rig run <code>roadie enroll --code …</code>.</div>{{end}}
{{if .Devices}}<div class="tablewrap"><table><thead><tr><th>Device</th><th>Rig</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Devices}}<tr>
<td data-l="Device">{{if .ID}}<b>{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</b><span class="sub">{{.Kind}}{{if .Platform}} · {{platform .Platform}}{{end}}{{if .Label}} · {{.Name}}{{end}}</span>{{else}}<span class="muted">no devices announced</span>{{end}}</td>
<td data-l="Rig"><b>{{.Rig.Name}}</b><span class="sub">{{.Rig.ID}}</span></td>
<td data-l="Status">{{if .Online}}<span class="dot on"></span>online{{else}}<span class="dot"></span><span class="muted">offline</span>{{end}}<span class="sub">{{if .Hold}}held by {{.Hold.Subject}}{{else}}available{{end}}{{if .Waiting}} · {{.Waiting}} waiting{{end}}</span></td>
<td class="act">{{if .ID}}<a class="btn small" href="/devices/{{.ID}}/view">Open</a> <a class="btn small" href="/records?device={{.ID}}">Records</a>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "records"}}{{template "head" (dict "Title" "Records" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap"><p class="lead">{{if .Device}}Recordings of <b>{{if .DeviceLabel}}{{.DeviceLabel}}{{else}}{{.Device}}{{end}}</b> (<a href="/records">all devices</a>).{{else}}Every recording, screenshot and log kept here.{{end}} {{template "help" "records"}}</p>
<div class="helptext" id="help-records" hidden>
<p>The Record Store keeps the files a session leaves behind: a Vibium <code>record.zip</code> an agent uploads after a run, a recording the rig made of a hold, screenshots and logs. Each file is named by its SHA-256 and tied to the hold it came from, so a recording is always next to who held the device, when, and what the rig said at the time.</p>
<p><b>Who can see what.</b> You, as the owner, see everything. An agent's token sees what it uploaded and what was recorded on its own holds. A file marked public can be fetched by anyone with its link, which is how a recording is handed to someone without an account here; everything else needs a token, a sign-in, or a short-lived authorization the station signs.</p>
<p><b>Watching a recording.</b> Play opens a Vibium recording in the <a href="https://player.vibium.dev">Vibium Record Player</a>, which this station carries with it and serves itself, so the file is fetched with your session and never leaves the station. Download keeps the file, which the player at player.vibium.dev also takes by drag and drop.</p>
<p><b>Room.</b> {{.Count}} files, {{.Total}}. A file is at most {{.MaxFile}}; the store keeps {{.MaxTotal}} and {{if .MaxAge}}each file {{.MaxAge}}{{else}}files for ever{{end}}, after which the oldest go. What the station recorded about a file, who uploaded it and on which hold, stays after the file itself is gone. The limits are the station's <code>--records-*</code> flags.</p>
</div>
{{if not .Records}}<div class="msg">Nothing here yet. An agent uploads a recording with <code>POST /v1/records/upload-auth</code> and a Blossom <code>PUT /upload</code>; see the API reference.</div>{{end}}
{{if .Records}}<div class="tablewrap"><table><thead><tr><th>When</th><th>Device</th><th>File</th><th>From</th><th>Access</th><th></th></tr></thead><tbody>
{{range .Records}}<tr>
<td data-l="When">{{.When}}</td>
<td data-l="Device">{{if .DeviceID}}<a href="/records?device={{.DeviceID}}">{{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}}</a>{{else}}<span class="muted">none</span>{{end}}</td>
<td data-l="File"><b>{{if .Name}}{{.Name}}{{else}}{{slice .SHA256 0 12}}…{{end}}</b><span class="sub">{{.Size}} · {{.Type}}</span></td>
<td data-l="From">{{.Uploader}}{{if .HoldID}}<span class="sub">{{.HoldID}}</span>{{end}}</td>
<td data-l="Access">{{if .Public}}public{{else}}private{{end}}{{if .CanEdit}} <form class="inline" method="post" action="/records/public"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA256}}"><input type="hidden" name="device" value="{{$.Device}}"><input type="hidden" name="public" value="{{if .Public}}0{{else}}1{{end}}"><button class="link" type="submit">{{if .Public}}make private{{else}}make public{{end}}</button></form>{{end}}</td>
<td class="act">{{if .CanPlay}}<a class="btn small" href="/records/{{.SHA256}}/play">Play</a> {{end}}<a class="btn small" href="{{.URL}}?download">Download</a>{{if .CanEdit}} <form class="inline" method="post" action="/records/delete"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA256}}"><input type="hidden" name="device" value="{{$.Device}}"><button class="small" type="submit">Delete</button></form>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "play"}}{{template "head" (dict "Title" .Title "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap play"><p class="lead"><a href="/records{{if .Device}}?device={{.Device}}{{end}}">Records</a> › <b>{{.Title}}</b> <span class="muted">{{.When}} · {{.Size}} · {{.Uploader}}{{if .Hold}} · {{.Hold}}{{end}}</span> <a class="btn small" href="{{.Download}}">Download</a> <a class="btn small" href="{{.Player}}" target="_blank" rel="noopener">Full page</a></p>
<iframe class="player" src="{{.Player}}" title="{{.Title}} in the Record Player" allowfullscreen></iframe>
</main>{{template "foot" .}}{{end}}

{{define "help"}}<button class="qhelp" type="button" aria-expanded="false" aria-controls="help-{{.}}" aria-label="What this is">?</button>{{end}}

{{define "settings"}}{{template "head" (dict "Title" "Settings" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap">
{{if .Message}}<div class="msg" role="alert">{{.Message}}</div>{{end}}

<h2 class="h2">Name {{template "help" "name"}}</h2>
<p class="helptext" id="help-name" hidden>What this station is called: in the top bar and the tab title here, and what a rig records when it enrolls. The <code>--name</code> flag sets it too.</p>
<form class="row" method="post" action="/settings/name"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" value="{{.Name}}" aria-label="Station name" maxlength="60" required> <input type="submit" value="Save"></form>

<h2 class="h2">Enroll a rig {{template "help" "enroll"}}</h2>
<p class="helptext" id="help-enroll" hidden>A rig is a machine with devices on it: a Raspberry Pi running Roadie with a phone wired to it. Enrolling joins it to this station. From then on the rig keeps a tunnel open to the station, the station lists the rig's devices, and holds on them go through the tunnel. An enrollment code is used once and expires after 15 minutes. Its last group is this station's key fingerprint, which the rig checks, so it enrolls with the station you meant. On the same network the rig finds the station by mDNS and the address can be left out.</p>
{{if .NewCode}}<div class="card featured">On the rig, within 15 minutes (until {{.CodeExpires}}):
<p class="secret">roadie enroll {{.PublicURL}} --code {{.NewCode}}</p>
<p class="muted">On the same network the URL can be left out: the rig finds the station over mDNS. The code ends with this station's key fingerprint, {{.Fingerprint}}, which the rig checks.</p></div>{{end}}
<form class="row" method="post" action="/settings/codes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input class="primary" type="submit" value="New enrollment code"> <span class="muted">{{.OpenCodes}} unused</span></form>

<h2 class="h2">Rigs {{template "help" "rigs"}}</h2>
<p class="helptext" id="help-rigs" hidden>The rigs enrolled here. Key is the rig's own public key, which signs everything the rig sends. Devices is what the rig announces each time it connects, so a change on the rig shows after its next connection. Remove refuses the rig's key from then on; to come back, it enrolls again with a new code.</p>
{{if not .Rigs}}<p class="muted">None enrolled.</p>{{end}}
{{if .Rigs}}<div class="tablewrap"><table><thead><tr><th>Rig</th><th>Key</th><th>Devices</th><th>Enrolled</th><th></th></tr></thead><tbody>
{{range .Rigs}}<tr><td data-l="Rig"><b>{{.Name}}</b><span class="sub">{{.ID}}</span></td><td data-l="Key"><code>{{npub .PubKey}}</code></td>
<td data-l="Devices">{{range .Devices}}{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}} <span class="muted">({{.Kind}}{{if .Platform}}, {{platform .Platform}}{{end}})</span> {{end}}</td><td data-l="Enrolled">{{.EnrolledAt.Format "Jan 2, 2006"}}</td>
<td class="act"><form class="inline" method="post" action="/settings/rigs/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="rig" value="{{.ID}}"><button class="small" type="submit">Remove</button></form></td></tr>{{end}}</tbody></table></div>{{end}}

<h2 class="h2">API tokens {{template "help" "tokens"}}</h2>
<p class="helptext" id="help-tokens" hidden>A token lets an agent or a script use this station without the password, sent as <code>Authorization: Bearer &lt;token&gt;</code>. A control token can hold a device and drive it; a read-only token can only watch. The secret is shown once, when the token is made; the station keeps only a hash of it. Revoke stops the token at once.</p>
{{if .NewToken}}<div class="card featured">Token <b>{{.NewTokenName}}</b>, shown once:<p class="secret">{{.NewToken}}</p></div>{{end}}
{{if .Tokens}}<div class="tablewrap"><table><thead><tr><th>Name</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr></thead><tbody>
{{range .Tokens}}<tr><td data-l="Name"><b>{{.Name}}</b></td><td data-l="Access">{{if .ReadOnly}}read-only{{else}}control{{end}}</td><td data-l="Created">{{.CreatedAt.Format "Jan 2, 2006"}}</td>
<td data-l="Last used">{{if .UsedAt.IsZero}}<span class="muted">never</span>{{else}}{{.UsedAt.Format "Jan 2 15:04"}}{{end}}</td>
<td class="act"><form class="inline" method="post" action="/settings/tokens/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small" type="submit">Revoke</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
<form class="row" method="post" action="/settings/tokens"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" placeholder="name" aria-label="Token name" required> <label class="check"><input type="checkbox" name="read_only"> read-only</label>
<input type="submit" value="Create token"></form>

<h2 class="h2">Password {{template "help" "password"}}</h2>
<p class="helptext" id="help-password" hidden>The owner's password for signing in to these pages, kept as a hash in the station's config file. In tailnet mode the people under Tailnet identities sign in by their Tailscale identity and need no password, but one still works. Sign-ins are rate-limited, and <code>station passwd</code> sets the password from the command line.</p>
{{if .NoPassword}}<div class="msg">No password is set; signing in by password is off until one is.</div>{{end}}
<form class="row" method="post" action="/settings/password"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="password" name="password" placeholder="new password" aria-label="New password" autocomplete="new-password"> <input type="password" name="again" placeholder="again" aria-label="New password again" autocomplete="new-password">
<input type="submit" value="Change"></form>

<h2 class="h2">Network {{template "help" "network"}}</h2>
<div class="helptext" id="help-network" hidden>
<p>How this station is reached, set by the flags it was started with.</p>
<p><b>Mode.</b> In <code>lan</code> mode the station listens on this machine's own network with a certificate it made itself. In <code>tailnet</code> mode it joins your Tailscale network as a node of its own, with HTTPS from Tailscale and sign-in by identity. In <code>public</code> mode it listens on a public address with a Let's Encrypt certificate, or behind a proxy such as Fly.io.</p>
<p><b>Address.</b> Where browsers and rigs reach this station. Rigs are told it when they enroll, and every link the station gives out starts with it.</p>
<p><b>Tunnel.</b> The connection every enrolled rig keeps open to this station, a WebSocket the rig opens outward. Through it the station reaches the rig's devices wherever the rig is, behind any router, with nothing opened on the rig's side: holds, the picture, input and the device's API all travel through the tunnel. A rig that drops reconnects on its own.</p>
<p><b>mDNS.</b> In lan mode the station announces itself on the local network as <code>_vibium-station._tcp</code>, so a rig on the same network can enroll without being given the address. Off with <code>--no-mdns</code>, and in the other modes.</p>
<p><b>Station key.</b> This station's identity: a key pair of its own, made the first time it ran and kept in its data directory. Rigs record the public key when they enroll and accept only what it signs, such as the grant a browser or an agent presents to hold a device. The fingerprint is the key's first four characters, the last group of every enrollment code.</p>
<p><b>Tailnet identities.</b> In tailnet mode, the Tailscale logins and tags allowed to sign in by identity, with no password, as given to <code>--allow</code>. Anyone else on the tailnet gets the sign-in page.</p>
<p><b>TURN.</b> A relay for the picture and sound when a browser and a rig cannot reach each other directly, as across two home routers. Each hold gets its own short-lived credentials, and relaying to private addresses is refused. On by itself in public mode, off in tailnet mode, where the tailnet already joins both ends; <code>--turn on</code> or <code>off</code> decides otherwise.</p>
</div>
<div class="tablewrap"><table class="kv">
<tr><th>Mode</th><td>{{.Mode}}</td></tr>
<tr><th>Address</th><td>{{.PublicURL}}</td></tr>
<tr><th>Tunnel</th><td>{{.TunnelURL}}</td></tr>
<tr><th>mDNS</th><td>{{if .MDNS}}advertised on the LAN as _vibium-station._tcp{{else}}off{{end}}</td></tr>
<tr><th>Station key</th><td><code>{{.Npub}}</code> <span class="muted">fingerprint {{.Fingerprint}}</span></td></tr>
<tr><th>Tailnet identities</th><td>{{if .Allow}}{{range .Allow}}{{.}} {{end}}{{else}}<span class="muted">none (--allow)</span>{{end}}</td></tr>
<tr><th>TURN</th><td>{{if .TURN}}{{.TURN}}{{else}}<span class="muted">off (--turn on)</span>{{end}}</td></tr>
</table></div>

<h2 class="h2">Record Store {{template "help" "records"}}</h2>
<p class="helptext" id="help-records" hidden>Where recordings, screenshots and logs are kept: files by their SHA-256 in the directory below, with an index entry and a signed file event for each in the database. The <a href="/records">Records</a> page lists them. The limits are the station's <code>--records-max-file</code>, <code>--records-max-total</code> and <code>--records-max-age</code> flags; past them the oldest files go, and their events stay.</p>
<div class="tablewrap"><table class="kv">
<tr><th>Files</th><td>{{.RecordsCount}}, {{.RecordsTotal}}</td></tr>
<tr><th>Directory</th><td><code>{{.RecordsDir}}</code></td></tr>
<tr><th>Limits</th><td>{{.RecordsMaxFile}} a file, {{.RecordsMaxTotal}} in all, {{if .RecordsMaxAge}}{{.RecordsMaxAge}} each{{else}}no age limit{{end}}</td></tr>
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
