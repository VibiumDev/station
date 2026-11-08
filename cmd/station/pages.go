package main

import (
	"html/template"
	"unicode"

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
// Roadie's pages are the same. The mark reads Vibium everywhere; a
// station's own name, when set, is the small line under it.
var pages = template.Must(template.New("").Funcs(template.FuncMap{"npub": npub, "dict": dict, "platform": platformName, "initial": initial}).Parse(`
{{define "head"}}<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · {{if .Named}}{{.Name}}{{else}}Vibium Station{{end}}</title><link rel="icon" type="image/png" href="/favicon.png"><link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="stylesheet" href="/viewer/pages.css"><script src="/viewer/pages.js"></script><script src="/viewer/jam.js" defer></script></head><body>{{end}}

{{define "theme"}}<button id="themeBtn" class="iconbtn" type="button" aria-label="Theme"></button>{{end}}

{{define "header"}}<aside class="side" id="side">
<div class="side-top"><a class="brand" href="/"><img class="logo" src="/viewer/vibium-logo.png" alt=""><span class="wordmark">Vibium</span></a>
<button class="iconbtn side-toggle" type="button" aria-label="Collapse the sidebar" aria-expanded="true"><span class="wide">{{template "icon-panel"}}</span><span class="narrow">{{template "icon-close"}}</span></button></div>
{{if .Named}}<p class="station" title="{{.Name}}">{{.Name}}</p>{{end}}
<nav class="side-nav" aria-label="Pages">
{{if .JamsOn}}<a href="/" title="New jam"{{if eq (printf "%v" .Page) "home"}} aria-current="page"{{end}}>{{template "icon-new"}}<span class="label">New jam</span></a>
<a href="/jams" title="Jams"{{if eq (printf "%v" .Page) "jams"}} aria-current="page"{{end}}>{{template "icon-jams"}}<span class="label">Jams</span></a>{{end}}
<a href="/devices" title="Devices"{{if eq (printf "%v" .Page) "index"}} aria-current="page"{{end}}>{{template "icon-devices"}}<span class="label">Devices</span></a>
<a href="/records" title="Records"{{if eq (printf "%v" .Page) "records"}} aria-current="page"{{end}}>{{template "icon-records"}}<span class="label">Records</span></a>
{{if .Principal.Admin}}<a href="/settings" title="Settings"{{if eq (printf "%v" .Page) "settings"}} aria-current="page"{{end}}>{{template "icon-settings"}}<span class="label">Settings</span></a>{{end}}
</nav>
<details class="account"><summary title="{{.Principal.Subject}}"><span class="avatar" aria-hidden="true">{{initial .Principal.Subject}}</span><span class="label">{{.Principal.Subject}}</span></summary>
<div class="menu"><p class="who">{{.Principal.Subject}}</p>
<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="menuitem" type="submit">{{template "icon-signout"}}Sign out</button></form></div></details>
</aside><div class="side-backdrop"></div>
<header class="bar"><button class="iconbtn menu" type="button" aria-label="Menu" aria-controls="side" aria-expanded="false">{{template "icon-menu"}}</button><a class="brand" href="/"><img class="logo" src="/viewer/vibium-logo.png" alt=""><span class="wordmark">Vibium</span></a></header>{{end}}

{{define "icon-new"}}<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7M18.5 2.5a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-jams"}}<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-devices"}}<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="6" y="2" width="12" height="20" rx="2.5" stroke="currentColor" stroke-width="2" fill="none"/><path d="M11 18h2" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>{{end}}
{{define "icon-records"}}<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="2" fill="none"/><circle cx="12" cy="12" r="2.5" stroke="currentColor" stroke-width="2" fill="none"/></svg>{{end}}
{{define "icon-settings"}}<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-panel"}}<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2" stroke="currentColor" stroke-width="2" fill="none"/><path d="M9 4v16" stroke="currentColor" stroke-width="2"/></svg>{{end}}
{{define "icon-menu"}}<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M3 12h18M3 18h18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>{{end}}
{{define "icon-close"}}<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>{{end}}

{{define "foot"}}</body></html>{{end}}

{{define "kicker"}}{{if .Named}}<p class="kicker">{{.Name}}</p>{{end}}{{end}}

{{define "login"}}{{template "head" (dict "Title" "Sign in" "Name" .Name "Named" .Named)}}
<header class="top"><div class="wrap"><a class="brand" href="/"><img class="logo" src="/viewer/vibium-logo.png" alt=""><span class="wordmark">Vibium</span><span class="product">Station</span></a>{{template "theme"}}</div></header>
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
<p>Every device on every host enrolled here. Open takes a device nobody holds, and you drive it: the picture, touch, keys and sound. A device someone else holds opens watching, and Hold in the viewer takes it from them; an agent's run ends when you do. Release in the viewer gives the device back and returns here. A hold also ends after 5 idle minutes, or after 8 hours.</p>
<p><b>Device.</b> The device's label, or its name when the host gave it no label, with its kind and platform under it. The name is the last part of the device's address, rig/name, which holds and the API use; Roadie's <code>--label</code> sets the label and <code>--platform</code> the platform.</p>
<p><b>Host.</b> The machine the device is wired to or runs on, a Roadie rig today: the name it enrolled with, and the id the station gave it. <i>Records sessions</i> means the host records every hold on the device, yours as much as an agent's: each input, and a frame of the screen after each burst of input, go into a recording that is uploaded to the Record Store when the hold ends, where the Records page lists it. That is the host's own setting (Roadie's <code>--record</code>), which the host announces when it connects; the viewer shows REC while such a session is live.</p>
<p><b>Status.</b> Online while the host's tunnel to the station is up, offline otherwise. Under it, available when nobody holds the device, or who holds it and how many agents are waiting their turn.</p>
</div>
{{if not .Devices}}<div class="msg">No hosts yet. Make an enrollment code on <a href="/settings">Settings</a>, then on the host run <code>roadie enroll --code …</code>.</div>{{end}}
{{if .Devices}}<div class="tablewrap"><table><thead><tr><th>Device</th><th>Host</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Devices}}<tr>
<td data-l="Device">{{if .ID}}<b>{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</b><span class="sub">{{.Kind}}{{if .Platform}} · {{platform .Platform}}{{end}}{{if .Label}} · {{.Name}}{{end}}</span>{{else}}<span class="muted">no devices announced</span>{{end}}</td>
<td data-l="Host"><b>{{.Rig.Name}}</b><span class="sub">{{.Rig.ID}}{{if .Recorded}} · <span class="rec" title="This host records every session on the device: each input, and a frame of the screen after each burst of input, kept in the Record Store when the session ends.">records sessions</span>{{end}}</span></td>
<td data-l="Status">{{if .Online}}<span class="dot on"></span>online{{else}}<span class="dot"></span><span class="muted">offline</span>{{end}}<span class="sub">{{if .Hold}}held by {{if .Hold.JamID}}<a href="/jams/{{.Hold.JamID}}">jam {{.Hold.JamID}}</a>{{else}}{{.Hold.Subject}}{{end}}{{else}}available{{end}}{{if .Waiting}} · {{.Waiting}} waiting{{end}}</span></td>
<td class="act">{{if .ID}}<a class="btn small" href="/devices/{{.ID}}/view">Open</a> <a class="btn small" href="/records?device={{.ID}}">Records</a>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "records"}}{{template "head" (dict "Title" "Records" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap{{if .Grid}} wide{{end}}"><p class="lead">{{if .Device}}Recordings of <b>{{if .DeviceLabel}}{{.DeviceLabel}}{{else}}{{.Device}}{{end}}</b> (<a href="/records{{if not .Grid}}?view=list{{end}}">all devices</a>).{{else}}Every recording, screenshot and log kept here.{{end}} {{template "help" "records"}}</p>
{{if .Records}}<p class="views"><a href="/records?view=grid{{if .Device}}&device={{.Device}}{{end}}"{{if .Grid}} aria-current="page"{{end}}>Grid</a><a href="/records?view=list{{if .Device}}&device={{.Device}}{{end}}"{{if not .Grid}} aria-current="page"{{end}}>List</a>{{if .Grid}}<label class="size"><span>Size</span><input type="range" id="cardSize" min="80" max="520" step="10" value="140" aria-label="Thumbnail size"></label>{{end}}</p>{{end}}
<div class="helptext" id="help-records" hidden>
<p>The Record Store keeps the files a session leaves behind: a Vibium <code>record.zip</code> an agent uploads after a run, a recording the rig made of a hold, screenshots and logs. Each file is named by its SHA-256 and tied to the hold it came from, so a recording is always next to who held the device, when, and what the rig said at the time.</p>
<p><b>Who can see what.</b> You, as the owner, see everything. An agent's token sees what it uploaded and what was recorded on its own holds. A file marked public can be fetched by anyone with its link, which is how a recording is handed to someone without an account here; everything else needs a token, a sign-in, or a short-lived authorization the station signs. Make public, in the list view, opens the file's download link to anyone who has it, with no sign-in; the Play page and the thumbnails stay behind sign-in, and someone with the file can drop it into the Record Player at player.vibium.dev themselves. Make private closes the link again, though a copy already downloaded is theirs to keep. Both are written to the audit log.</p>
<p><b>Watching a recording.</b> Play opens a Vibium recording in the <a href="https://player.vibium.dev">Vibium Record Player</a>, which this station carries with it and serves itself, so the file is fetched with your session and never leaves the station. Download keeps the file, which the player at player.vibium.dev also takes by drag and drop.</p>
<p><b>Room.</b> {{.Count}} files, {{.Total}}. A file is at most {{.MaxFile}}; the store keeps {{.MaxTotal}} and {{if .MaxAge}}each file {{.MaxAge}}{{else}}files for ever{{end}}, after which the oldest go. A recording is also kept unpacked beside its zip, which is what the cards and the thumbnails read, so on disk it takes about twice its size. What the station recorded about a file, who uploaded it and on which hold, stays after the file itself is gone. The limits are the station's <code>--records-*</code> flags.</p>
</div>
{{if not .Records}}<div class="msg">Nothing here yet. An agent uploads a recording with <code>POST /v1/records/upload-auth</code> and a Blossom <code>PUT /upload</code>; see the API reference.</div>{{end}}
{{if and .Records .Grid}}<div class="cards">{{range .Records}}<article class="card rec">
<a class="thumb" href="{{if .CanPlay}}/records/{{.SHA256}}/play{{else}}{{.URL}}?download{{end}}" aria-label="{{if .Name}}{{.Name}}{{else}}{{slice .SHA256 0 12}}{{end}}">{{if .HasThumb}}<img src="/records/{{.SHA256}}/thumb.jpg" alt="" loading="lazy">{{else}}<span class="nothumb">{{.Type}}</span>{{end}}</a>
<div class="meta"><b>{{if .Name}}{{.Name}}{{else}}{{slice .SHA256 0 12}}…{{end}}</b><span class="sub">{{.When}} · {{.Size}}{{if .Meta}} · {{.Meta}}{{end}}{{if .Public}} · public{{end}}</span><span class="sub">{{if .DeviceID}}<a href="/records?device={{.DeviceID}}">{{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}}</a> · {{end}}{{.Uploader}}</span>
<span class="act">{{if .CanPlay}}<a class="btn small icon" href="/records/{{.SHA256}}/play" title="Play" aria-label="Play">{{template "icon-play"}}</a> {{end}}<a class="btn small icon" href="{{.URL}}?download" title="Download" aria-label="Download">{{template "icon-download"}}</a></span></div>
</article>{{end}}</div>{{end}}
{{if and .Records (not .Grid)}}<div class="tablewrap"><table><thead><tr><th>When</th><th>Device</th><th>File</th><th>From</th><th>Access</th><th></th></tr></thead><tbody>
{{range .Records}}<tr>
<td data-l="When">{{.When}}</td>
<td data-l="Device">{{if .DeviceID}}<a href="/records?view=list&device={{.DeviceID}}">{{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}}</a>{{else}}<span class="muted">none</span>{{end}}</td>
<td data-l="File"><b>{{if .Name}}{{.Name}}{{else}}{{slice .SHA256 0 12}}…{{end}}</b><span class="sub">{{.Size}} · {{.Type}}{{if .Meta}} · {{.Meta}}{{end}}</span></td>
<td data-l="From">{{.Uploader}}{{if .HoldID}}<span class="sub">{{.HoldID}}</span>{{end}}</td>
<td data-l="Access">{{if .Public}}public{{else}}private{{end}}{{if .CanEdit}} <form class="inline" method="post" action="/records/public"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA256}}"><input type="hidden" name="device" value="{{$.Device}}"><input type="hidden" name="view" value="list"><input type="hidden" name="hold" value="{{$.Hold}}"><input type="hidden" name="public" value="{{if .Public}}0{{else}}1{{end}}"><button class="link" type="submit">{{if .Public}}make private{{else}}make public{{end}}</button></form>{{end}}</td>
<td class="act">{{if .CanPlay}}<a class="btn small icon" href="/records/{{.SHA256}}/play" title="Play" aria-label="Play">{{template "icon-play"}}</a> {{end}}<a class="btn small icon" href="{{.URL}}?download" title="Download" aria-label="Download">{{template "icon-download"}}</a>{{if .CanEdit}} <form class="inline" method="post" action="/records/delete"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA256}}"><input type="hidden" name="device" value="{{$.Device}}"><input type="hidden" name="view" value="list"><input type="hidden" name="hold" value="{{$.Hold}}"><button class="small icon" type="submit" title="Delete" aria-label="Delete">{{template "icon-delete"}}</button></form>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "icon-signout"}}<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-send"}}<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path d="M12 20V5m0 0-6 6m6-6 6 6" stroke="currentColor" stroke-width="2.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}

{{define "home"}}{{template "head" (dict "Title" "New jam" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap home">
{{if .Message}}<div class="msg error" role="alert">{{.Message}}</div>{{end}}
<h1 class="ask">{{.Ask}} {{template "help" "jams"}}</h1>
<div class="helptext" id="help-jams" hidden>
<p>Type something to check or try, and a jam session begins. A model set up on Settings drives the device through Vibium's tools, and the page shows each step as it happens. A check ends with a verdict, PASS, FAIL or REVIEW. A try ends with COMPLETED or NOT COMPLETED. The session's header shows which one it was, and a follow-up of the same kind can change it.</p>
<p><b>Device and model.</b> If more than one device is online, or more than one agent model is set up, pick which to use beside the prompt. The examples under the box match the kind of device that is online.</p>
<p><b>While it runs.</b> The session holds the device, so Devices shows it as the holder. Taking the device from there ends the session at once. If the host records holds, the recording is linked from the session when it ends. A follow-up prompt continues the same conversation from where the model left off, for five minutes after a result.</p>
<p><b>Cost.</b> Model calls go to the provider on the key the model was set up with, and the session's page shows the tokens used. A session ends after thirty minutes or a hundred actions.</p>
</div>
<form class="promptbox" method="post" action="/jams"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea name="prompt" class="prompt" rows="3" placeholder="Something to check, or something to try" required maxlength="4000" aria-label="Prompt">{{.Prompt}}</textarea>
<div class="promptbar">
{{if gt (len .Devices) 1}}<select name="device" aria-label="Device">{{range .Devices}}<option value="{{.ID}}">{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</option>{{end}}</select>{{else}}{{range .Devices}}<input type="hidden" name="device" value="{{.ID}}"><span class="muted">{{if .Label}}{{.Label}}{{else}}{{.Name}}{{end}}</span>{{else}}<span class="muted">no device is online</span>{{end}}{{end}}
{{if gt (len .Models) 1}}<select name="model" aria-label="Model">{{range .Models}}<option value="{{.Name}}"{{if .Default}} selected{{end}}>{{.Name}}</option>{{end}}</select>{{end}}
<button class="primary send" type="submit" aria-label="Send">{{template "icon-send"}}</button>
</div></form>
<p class="examples">{{range .Examples}}<button class="example" type="button">{{.}}</button>{{end}}</p>

</main>{{template "foot" .}}{{end}}

{{define "jams"}}{{template "head" (dict "Title" "Jams" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap jams">
<h2 class="h2">Jams {{template "help" "jams"}}</h2>
<p class="helptext" id="help-jams" hidden>Every jam session on this station, newest first: who asked, on which device and with which model, the prompt, and the result. Open one for its transcript and the recording. A new one starts from the <a href="/">front page</a>.</p>
{{if .Jams}}<div class="tablewrap"><table><thead><tr><th>When</th><th>Who</th><th>Device</th><th>Model</th><th>Prompt</th><th>Result</th></tr></thead><tbody>
{{range .Jams}}<tr><td data-l="When"><a href="/jams/{{.ID}}">{{.When}}</a></td><td data-l="Who">{{.Subject}}</td><td data-l="Device">{{.Label}}</td><td data-l="Model">{{.Model}}</td><td data-l="Prompt">{{.Prompt}}</td>
<td data-l="Result">{{if .Open}}<span class="dot on"></span>running{{else if .Word}}<b class="verdict {{.Result}}">{{.Word}}</b>{{else}}<span class="muted">{{.Status}}</span>{{end}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="muted">No jams yet. <a href="/">Start one.</a></p>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "jam"}}{{template "head" (dict "Title" "Jam" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap jam{{if .Watch}} watching{{end}}">
<p class="lead"><a href="/jams">Jams</a> › <b>{{.Jam.ID}}</b> <span class="muted">{{.When}} · {{.Jam.Subject}} · {{.DeviceLabel}} · {{.Jam.Model}}{{if .Took}} · {{.Took}}{{end}}{{if .Word}} · {{end}}</span>{{if .Word}}<b class="verdict {{.Jam.Result}}">{{.Word}}</b>{{end}}
{{if .Open}} <form class="inline" method="post" action="/jams/{{.Jam.ID}}/stop"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="small" type="submit">Stop</button></form>{{end}}
{{if .Play}} <a class="btn small" href="{{.Play}}">Play the recording</a>{{end}} <a class="btn small" href="/devices/{{.Jam.DeviceID}}/view">Open the device</a></p>
{{if .Message}}<div class="msg error" role="alert">{{.Message}}</div>{{end}}
<div class="jamgrid">
<div class="transcript" id="transcript" data-jam="{{.Jam.ID}}" data-after="{{.Last}}"{{if .Open}} data-open="1"{{end}}>
{{range .Events}}{{template "event" .}}{{end}}
</div>
{{if .Watch}}<iframe class="watch" src="{{.Watch}}" title="Watching {{.DeviceLabel}}" allow="autoplay"></iframe>{{end}}
</div>
{{if .Open}}<form class="promptbox" method="post" action="/jams/{{.Jam.ID}}/turns"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea name="prompt" class="prompt" rows="2" placeholder="A follow-up, once the result is in" required maxlength="4000" aria-label="Follow-up prompt"></textarea>
<div class="promptbar"><button class="primary send" type="submit" aria-label="Send">{{template "icon-send"}}</button></div></form>
{{else}}<p class="muted">{{.Jam.Status}} · {{.Jam.Actions}} actions · {{.Jam.TokensIn}} tokens in, {{.Jam.TokensOut}} out</p>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "event"}}{{if eq .Kind "turn"}}<div class="ev turn"><span class="t">{{.When}}</span><div class="body"><b>Prompt</b><p>{{.Text}}</p></div></div>
{{else if eq .Kind "start"}}<div class="ev start"><span class="t">{{.When}}</span><div class="body muted">{{.Text}}</div></div>
{{else if eq .Kind "text"}}<div class="ev text"><span class="t">{{.When}}</span><div class="body"><p>{{.Text}}</p></div></div>
{{else if eq .Kind "call"}}<div class="ev call"><span class="t">{{.When}}</span><div class="body"><code>{{.Tool}}{{if .Args}} {{.Args}}{{end}}</code></div></div>
{{else if eq .Kind "result"}}<div class="ev result{{if .Error}} error{{end}}"><span class="t">{{.When}}</span><div class="body">{{.Text}}</div></div>
{{else if eq .Kind "shot"}}<div class="ev shot"><span class="t">{{.When}}</span><div class="body">{{if .Image}}<a href="{{.Image}}" target="_blank" rel="noopener"><img src="{{.Image}}" alt="Screenshot {{.Tool}}" loading="lazy"></a>{{end}}</div></div>
{{else if eq .Kind "verdict"}}<div class="ev verdict"><span class="t">{{.When}}</span><div class="body"><b class="verdict {{.Verdict.Status}}">{{.Word}}</b> {{.Verdict.Summary}}{{if .Verdict.Evidence}}<ul>{{range .Verdict.Evidence}}<li>{{.}}</li>{{end}}</ul>{{end}}{{if .Verdict.Reason}}<span class="muted">{{.Verdict.Reason}}</span>{{end}}</div></div>
{{else if eq .Kind "error"}}<div class="ev error"><span class="t">{{.When}}</span><div class="body">{{.Text}}</div></div>
{{else if eq .Kind "end"}}<div class="ev end"><span class="t">{{.When}}</span><div class="body muted">{{.Text}}</div></div>
{{end}}{{end}}

{{define "play"}}{{template "head" (dict "Title" .Title "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap play"><p class="lead"><a href="/records{{if .Device}}?device={{.Device}}{{end}}">Records</a> › <b>{{.Title}}</b> <span class="muted">{{.When}} · {{.Size}} · {{.Uploader}}{{if .Hold}} · {{.Hold}}{{end}}</span> <a class="btn small" href="{{.Download}}">Download</a> <a class="btn small" href="{{.Full}}" target="_blank" rel="noopener">Full page</a></p>
<iframe class="player" src="{{.Player}}" title="{{.Title}} in the Record Player" allowfullscreen></iframe>
</main>{{template "foot" .}}{{end}}

{{define "playfull"}}{{template "head" (dict "Title" .Title "Name" .Name "Named" .Named)}}
<iframe class="player full" src="{{.Player}}" title="{{.Title}} in the Record Player" allowfullscreen></iframe>
</body></html>{{end}}

{{define "icon-play"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M7 4.5v15l12-7.5z" fill="currentColor"/></svg>{{end}}
{{define "icon-delete"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M4 7h16M9 7V4.5h6V7M7 7l1 13h8l1-13M10 11v6M14 11v6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-download"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M12 3v11m0 0-4.5-4.5M12 14l4.5-4.5M4 19.5h16" stroke="currentColor" stroke-width="2.2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}

{{define "help"}}<button class="qhelp" type="button" aria-expanded="false" aria-controls="help-{{.}}" aria-label="What this is">?</button>{{end}}

{{define "settings"}}{{template "head" (dict "Title" "Settings" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap">
{{if .Message}}<div class="msg" role="alert">{{.Message}}</div>{{end}}

<h2 class="h2">Name {{template "help" "name"}}</h2>
<p class="helptext" id="help-name" hidden>What this station is called: under the Vibium mark in the sidebar and in the tab title here, and what a rig records when it enrolls. The <code>--name</code> flag sets it too.</p>
<form class="row" method="post" action="/settings/name"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" value="{{.Name}}" aria-label="Station name" maxlength="60" required> <input type="submit" value="Save"></form>

<h2 class="h2">AI {{template "help" "ai"}}</h2>
<div class="helptext" id="help-ai" hidden>
<p>A model provider turns on Jams: a prompt on the front page that drives the station's devices. Bring your own keys: each model here is a name of your choosing, a role, a provider, the provider's name for the model, and its API key, which is seen once as it is typed and kept in a file only the station's user can read, under <code>ai/</code> in the data directory, in Vibium's own <code>ai.env</code> format.</p>
<p><b>Roles.</b> An <i>agent</i> model is the brain of a jam session: it reasons, sees the screenshots and calls the tools. One is picked when a session starts, the default unless there are several to choose from. A <i>listen</i> model turns the device's sound into text and a <i>say</i> model turns text into speech, for Vibium's listen and say, which make those calls themselves; their settings go to Vibium, never the agent's key.</p>
<p><b>Saving.</b> An agent model is asked for one word when it is saved, so a wrong key or an unknown model is seen here and not in a failed session. Saving a name again changes its settings and keeps its key unless a new one is typed. A model named <i>host</i> is the station's own environment, when it carries Vibium's variables, and is set there.</p>
<p><b>Jams.</b> On by itself once an agent model is set, which puts Jams in the sidebar and the prompt on the front page; off keeps the keys and puts the devices back on the front page. Sessions drive devices through <code>vibium mcp</code>, so the station needs the Vibium binary on its PATH or named with <code>--vibium</code>.</p>
</div>
<p class="muted">A model provider turns on Jams: a prompt on the front page that drives the station's devices. Bring your own keys.</p>
{{if .Models}}<div class="tablewrap"><table><thead><tr><th>Model</th><th>Role</th><th>Provider</th><th>Key</th><th></th></tr></thead><tbody>
{{range .Models}}<tr><td data-l="Model"><b>{{.Name}}</b><span class="sub">{{.Model}}{{if .Default}} · default{{end}}{{if .ReasoningEffort}} · {{.ReasoningEffort}}{{end}}</span></td><td data-l="Role">{{.Role}}</td><td data-l="Provider">{{.Provider}}{{if .BaseURL}}<span class="sub">{{.BaseURL}}</span>{{end}}</td>
<td data-l="Key">{{if .Host}}<span class="muted">from the environment</span>{{else if .HasKey}}set{{else}}<span class="muted">none</span>{{end}}</td>
<td class="act">{{if not .Host}}{{if not .Default}}<form class="inline" method="post" action="/settings/models/default"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small" type="submit">Default</button></form> {{end}}<form class="inline" method="post" action="/settings/models/key"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><input type="password" name="key" placeholder="new key" aria-label="New key for {{.Name}}" autocomplete="off"> <button class="small" type="submit">Replace key</button></form> <form class="inline" method="post" action="/settings/models/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small" type="submit">Remove</button></form>{{end}}</td></tr>{{end}}
</tbody></table></div>{{end}}
<form class="card modelform" method="post" action="/settings/models"><input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="row"><input type="text" name="name" placeholder="name, such as gpt or claude" aria-label="Name" required maxlength="40">
<select name="role" aria-label="Role">{{range .Roles}}<option value="{{.}}">{{.}}</option>{{end}}</select>
<select name="provider" aria-label="Provider">{{range .Providers}}<option value="{{.ID}}">{{.Label}}</option>{{end}}</select></div>
<div class="row"><input type="text" name="model" placeholder="the provider's model name" aria-label="Model" required>
<input type="password" name="key" placeholder="API key" aria-label="API key" autocomplete="off"></div>
<div class="row"><input type="text" name="base_url" placeholder="base URL (optional)" aria-label="Base URL">
<select name="effort" aria-label="Reasoning effort"><option value="">reasoning effort: automatic</option>{{range .Efforts}}{{if .}}<option value="{{.}}">{{.}}</option>{{end}}{{end}}</select>
<label class="check"><input type="checkbox" name="default"> default for its role</label>
<input class="primary" type="submit" value="Add model"></div></form>
<div class="tablewrap"><table class="kv">
<tr><th>Jams</th><td>{{if .JamsPossible}}<form class="inline" method="post" action="/settings/jams"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="on" value="{{if .JamsOn}}0{{else}}1{{end}}">{{if .JamsOn}}on <button class="link" type="submit">turn off</button>{{else}}off <button class="link" type="submit">turn on</button>{{end}}</form>{{else}}<span class="muted">off until an agent model is set</span>{{end}}</td></tr>
<tr><th>Vibium</th><td>{{if .Vibium}}<code>{{.Vibium}}</code>{{else}}<span class="muted">not found ({{.VibiumMissing}}); jam sessions drive devices through <code>vibium mcp</code>, so put the binary on the station's PATH or pass <code>--vibium</code></span>{{end}}</td></tr>
</table></div>

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
<p class="helptext" id="help-records" hidden>Where recordings, screenshots and logs are kept: files by their SHA-256 in the directory below, with an index entry and a signed file event for each in the database, and every recording unpacked once beside its zip under <code>unpacked/</code>, so its steps, frames and last picture are read from files rather than the zip; that doubles what a recording takes on disk. The <a href="/records">Records</a> page lists them. The limits are the station's <code>--records-max-file</code>, <code>--records-max-total</code> and <code>--records-max-age</code> flags; past them the oldest files go, and their events stay.</p>
<div class="tablewrap"><table class="kv">
<tr><th>Files</th><td>{{.RecordsCount}}, {{.RecordsTotal}}</td></tr>
<tr><th>Directory</th><td><code>{{.RecordsDir}}</code></td></tr>
<tr><th>Limits</th><td>{{.RecordsMaxFile}} a file, {{.RecordsMaxTotal}} in all, {{if .RecordsMaxAge}}{{.RecordsMaxAge}} each{{else}}no age limit{{end}}</td></tr>
</table></div>

<h2 class="h2">Appearance {{template "help" "appearance"}}</h2>
<p class="helptext" id="help-appearance" hidden>Light or dark for this browser, or whatever the device is set to. The choice stays in the browser, not on the station, so each browser has its own.</p>
<fieldset class="themepick" id="themePick"><legend class="sr">Theme</legend>
<label><input type="radio" name="theme" value="system"><span>System</span></label>
<label><input type="radio" name="theme" value="light"><span>Light</span></label>
<label><input type="radio" name="theme" value="dark"><span>Dark</span></label></fieldset>
</main>{{template "foot" .}}{{end}}
`))

// initial is the first letter of a name, for the account's avatar.
func initial(s string) string {
	for _, r := range s {
		return string(unicode.ToUpper(r))
	}
	return "?"
}

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}
