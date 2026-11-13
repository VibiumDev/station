package main

import (
	"html/template"
	"unicode"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"

	"github.com/VibiumDev/station/jam"
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
var pages = template.Must(template.New("").Funcs(template.FuncMap{"npub": npub, "dict": dict, "platform": platformName, "initial": initial, "brief": brief, "providerName": providerName}).Parse(`
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
<a class="menuitem" href="/account">{{template "icon-account"}}Account</a>
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
{{template "bare-top"}}
<main class="wrap login">{{template "kicker" .}}<h1 class="h1">Sign in.</h1>
{{if .NoUsers}}<div class="msg">Nobody can sign in yet. On the station's machine, run <code>station passwd</code> to set the owner's password.</div>{{end}}
{{if .Error}}<div class="msg error" role="alert">{{.Error}}</div>{{end}}
<div class="card"><form method="post" action="/login"><input type="hidden" name="next" value="{{.Next}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="login">Login</label><input id="login" type="text" name="login" value="{{.Login}}" autofocus autocomplete="username" autocapitalize="none" spellcheck="false" placeholder="you@example.com">
<label for="password">Password</label><input id="password" type="password" name="password" autocomplete="current-password">
<input class="primary" type="submit" value="Sign in"></form></div></main>
{{template "foot" .}}{{end}}

{{define "bare-top"}}<header class="top"><div class="wrap"><a class="brand" href="/"><img class="logo" src="/viewer/vibium-logo.png" alt=""><span class="wordmark">Vibium</span><span class="product">Station</span></a>{{template "theme"}}</div></header>{{end}}

{{define "code"}}{{template "head" (dict "Title" "Sign in" "Name" .Name "Named" .Named)}}
{{template "bare-top"}}
<main class="wrap login">{{template "kicker" .}}<h1 class="h1">One more step.</h1>
<p class="lead">Enter the six-digit code your authenticator app shows for <b>{{.Login}}</b>, or one of your backup codes.</p>
{{if .Error}}<div class="msg error" role="alert">{{.Error}}</div>{{end}}
<div class="card"><form method="post" action="/login/code"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="code">Code</label><input id="code" type="text" name="code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="none" spellcheck="false" autofocus required>
<input class="primary" type="submit" value="Continue"></form>
<form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link" type="submit">Start over</button></form></div></main>
{{template "foot" .}}{{end}}

{{define "setup"}}{{template "head" (dict "Title" "Set up your sign-in" "Name" .Name "Named" .Named)}}
{{template "bare-top"}}
<main class="wrap login">{{template "kicker" .}}
{{if .Expired}}<h1 class="h1">This link is spent.</h1>
<p class="lead">It was used already or has expired. Ask an admin of this station for a new one.</p>
{{else}}<h1 class="h1">Welcome{{if .UserName}}, {{.UserName}}{{end}}.</h1>
<p class="lead">Choose a password for <b>{{.Login}}</b>: at least 12 characters, and a few unrelated words work well. An authenticator app comes next.</p>
{{if .Error}}<div class="msg error" role="alert">{{.Error}}</div>{{end}}
<div class="card"><form method="post" action="/setup/{{.Token}}"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="login" value="{{.Login}}" autocomplete="username">
<label for="password">Password</label><input id="password" type="password" name="password" autocomplete="new-password" minlength="12" autofocus required>
<label for="again">Again</label><input id="again" type="password" name="again" autocomplete="new-password" required>
<input class="primary" type="submit" value="Continue"></form></div>{{end}}</main>
{{template "foot" .}}{{end}}

{{define "account-setup"}}{{template "head" (dict "Title" "Authenticator" "Name" .Name "Named" .Named)}}
{{template "bare-top"}}
<main class="wrap login">{{template "kicker" .}}<h1 class="h1">{{if .Replacing}}A new authenticator.{{else}}Add an authenticator.{{end}}</h1>
<p class="lead">Scan this with an authenticator app, such as the one in your phone's password manager, then enter the code it shows. Every password sign-in to <b>{{.Login}}</b> asks for one from now on.</p>
{{if .Error}}<div class="msg error" role="alert">{{.Error}}</div>{{end}}
<div class="card">{{.QR}}<p class="muted">Or type the secret in: <span class="secret spaced">{{.Secret}}</span></p>
<form method="post" action="/account/setup"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="code">Code from the app</label><input id="code" type="text" name="code" inputmode="numeric" autocomplete="one-time-code" autocapitalize="none" spellcheck="false" autofocus required>
<input class="primary" type="submit" value="Confirm"></form>
{{if .Replacing}}<form class="inline" method="post" action="/account/setup/cancel"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link" type="submit">Keep the old one</button></form>{{else}}<form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link" type="submit">Sign out</button></form>{{end}}</div></main>
{{template "foot" .}}{{end}}

{{define "account-codes"}}{{template "head" (dict "Title" "Backup codes" "Name" .Name "Named" .Named)}}
{{template "bare-top"}}
<main class="wrap login">{{template "kicker" .}}<h1 class="h1">Your backup codes.</h1>
<p class="lead">{{if .Fresh}}The authenticator is set up. {{end}}Each of these signs you in once when the authenticator is out of reach. Keep them somewhere safe: they are shown this once{{if not .Fresh}}, and the old set no longer works{{end}}.</p>
<div class="card"><ul class="codes">{{range .Codes}}<li>{{.}}</li>{{end}}</ul>
<a class="btn primary" href="{{.Next}}">I have kept them</a></div></main>
{{template "foot" .}}{{end}}

{{define "index"}}{{template "head" (dict "Title" "Devices" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap"><p class="lead">Drive a device, or just watch. {{template "help" "devices"}}</p>
<div class="helptext" id="help-devices" hidden>
<p>Every device on every host enrolled here. Drive, the wheel, takes a device nobody holds and gives you the picture, touch, keys and sound; a device someone else holds opens watching, and Hold in the viewer takes it from them, which ends an agent's run. Release in the viewer gives the device back and returns here, and a hold also ends after 5 idle minutes, or after 8 hours. Watch, the eye, only looks: it never takes the device and sends no input, so it is the safe way to follow someone else's session or a jam.</p>
<p><b>Device.</b> The device's label, else its model, else its name, which opens the watching page, with its kind, its model when the label differs, its platform and version, and its name under it. The name is the last part of the device's address, rig/name, which holds and the API use; Roadie's <code>--label</code>, <code>--model</code>, <code>--os-version</code>, <code>--platform</code> and <code>--device-name</code> set the rest.</p>
<p><b>Host.</b> The machine the device is wired to or runs on, a Roadie rig today: the name it enrolled with, and the id the station gave it. <i>Records sessions</i> means the host records every hold on the device, yours as much as an agent's: each input, and a frame of the screen after each burst of input, go into a recording that is uploaded to the Record Store when the hold ends, where the Records page lists it. A host that can also keeps a video of the screen, what the device played, and what was sent to its microphone, your voice included when the viewer's microphone is on. That is the host's own setting (Roadie's <code>--record</code>), which the host announces when it connects; the viewer shows REC to the person driving such a session.</p>
<p><b>Status.</b> Online while the host's tunnel to the station is up, offline otherwise. Under it, available when nobody holds the device, or who holds it and how many agents are waiting their turn. Someone watching a free device is shown too; they hold it view only, anyone who drives takes it over at once, and the watch ends when they press Stop watching or close the page.</p>
</div>
{{if not .Devices}}<div class="msg">{{if .Principal.Admin}}No hosts yet. Make an enrollment code on <a href="/settings">Settings</a>, then on the host run <code>roadie enroll --code …</code>.{{else}}Nothing is shared with you yet. An admin shares a device with one of your teams on Settings, and it appears here.{{end}}</div>{{end}}
{{if .Devices}}<div class="tablewrap"><table><thead><tr><th>Device</th><th>Host</th><th>Status</th><th></th></tr></thead><tbody>
{{range .Devices}}<tr>
<td data-l="Device">{{if .ID}}<b><a href="/devices/{{.ID}}/view">{{.Title}}</a></b><span class="sub">{{.Kind}}{{if and .Model (ne .Model .Title)}} · {{.Model}}{{end}}{{if .Platform}} · {{platform .Platform}}{{if .OSVersion}} {{.OSVersion}}{{end}}{{end}}{{if ne .Title .Name}} · {{.Name}}{{end}}</span>{{else}}<span class="muted">no devices announced</span>{{end}}</td>
<td data-l="Host"><b>{{.Rig.Name}}</b><span class="sub">{{.Rig.ID}}{{if .Recorded}} · <span class="hint" title="This host records every session on the device: each input, a frame of the screen after each burst of input and, where the host can, a video of the screen, the device's sound and what is sent to its microphone, kept in the Record Store when the session ends.">records sessions</span>{{end}}</span></td>
<td data-l="Status">{{if .Online}}<span class="dot on"></span>online{{else}}<span class="dot"></span><span class="muted">offline</span>{{end}}<span class="sub">{{if and .Hold (not .Hold.Watching)}}held by {{if .Hold.JamID}}<a href="/jams/{{.Hold.JamID}}">jam {{.Hold.JamID}}</a>{{else}}{{.Hold.Subject}}{{end}}{{else}}available{{if .Hold}} · watched by {{.Hold.Subject}}{{end}}{{end}}{{if .Waiting}} · {{.Waiting}} waiting{{end}}</span></td>
<td class="act">{{if .ID}}{{if .CanDrive}}<a class="btn small icon" href="/devices/{{.ID}}/drive" title="Drive" aria-label="Drive {{.Title}}">{{template "icon-drive"}}</a> {{end}}<a class="btn small icon" href="/devices/{{.ID}}/view" title="Watch" aria-label="Watch {{.Title}}">{{template "icon-watch"}}</a> <a class="btn small icon" href="/records?device={{.ID}}" title="Records" aria-label="Records of {{.Title}}">{{template "icon-records"}}</a>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "records"}}{{template "head" (dict "Title" "Records" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap{{if .Grid}} wide{{end}}"><p class="lead">{{if .Device}}Recordings of <b>{{if .DeviceLabel}}{{.DeviceLabel}}{{else}}{{.Device}}{{end}}</b> (<a href="{{.AllURL}}">all devices</a>).{{else}}Every recording, screenshot and log kept here.{{end}} {{template "help" "records"}}</p>
<form class="toolbar" method="get" action="/records">{{range .Keep}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}
<label class="search">{{template "icon-search"}}<input type="search" name="q" value="{{.Query}}" placeholder="Search, or #tag" aria-label="Search the records" autocomplete="off"></label>
<span class="views"><a href="{{.GridURL}}"{{if .Grid}} aria-current="page"{{end}}>Grid</a><a href="{{.ListURL}}"{{if .List}} aria-current="page"{{end}}>List</a><a href="{{.StripURL}}"{{if .Strip}} aria-current="page"{{end}}>Filmstrip</a><a class="btn small icon star{{if .GoldenOnly}} on{{end}}" href="{{.GoldenURL}}" title="{{if .GoldenOnly}}Golden records only. Show all{{else}}Golden records only{{end}}" aria-label="{{if .GoldenOnly}}Golden records only. Show all{{else}}Golden records only{{end}}" aria-pressed="{{if .GoldenOnly}}true{{else}}false{{end}}">{{template "icon-star"}}</a></span>
<label class="per"><span>Show</span><select name="n" data-submit aria-label="Records per page">{{range .Pers}}<option value="{{.N}}"{{if .On}} selected{{end}}>{{.N}}</option>{{end}}</select></label>
{{if .Grid}}<label class="size"><span>Size</span><input type="range" id="cardSize" min="80" max="520" step="10" value="140" aria-label="Thumbnail size"></label>{{end}}{{if .Strip}}<label class="size"><span>Size</span><input type="range" id="stripSize" min="56" max="224" step="8" value="112" aria-label="Filmstrip height"></label>{{end}}
</form>
{{if .Chips}}<p class="tags">{{range .Chips}}<a class="tag{{if .On}} on{{end}}" href="{{.URL}}">#{{.Tag}}<span class="n">{{.N}}</span></a>{{end}}</p>{{end}}
<div class="helptext" id="help-records" hidden>
<p>The Record Store keeps the files a session leaves behind: a Vibium <code>record.zip</code> an agent uploads after a run, a recording the rig made of a hold, screenshots and logs. Each file is named by its SHA-256 and tied to the hold it came from, so a recording is always next to who held the device, when, and what the rig said at the time.</p>
<p><b>Finding one.</b> The search matches words against a file's name and type, who uploaded it, its hold, device and rig, and its tags. A word with a <code>#</code> in front, <code>#flaky</code>, keeps only the records tagged so, and the chips under the box add or drop a tag with a click. Grid shows each record as a card with its last frame, List as a row of a table, and Filmstrip as a row of all its frames from first to last, which scrolls sideways when they are more than fit. The star beside the views shows only the records marked golden, this station's word for pinned or bookmarked, which the star on each record sets. Tags are set on a record's Play page, by whoever uploaded it or by the owner. Show sets how many records a page holds.</p>
<p><b>Who can see what.</b> You, as the owner, see everything. An agent's token sees what it uploaded and what was recorded on its own holds. A shared file is public: anyone with its link can fetch it, which is how a recording is handed to someone without an account here; everything else needs a token, a sign-in, or a short-lived authorization the station signs. Share, in the list view, is held for a moment and opens the file's download link to anyone who has it, with no sign-in; the Play page and the thumbnails stay behind sign-in, and someone with the file can drop it into the Record Player at player.vibium.dev themselves. The icon stays lit while a file is shared, and holding it again makes the file private, which closes the link, though a copy already downloaded is theirs to keep. Both are written to the audit log.</p>
<p><b>Watching a recording.</b> Play opens a Vibium recording in the <a href="https://player.vibium.dev">Vibium Record Player</a>, which this station carries with it and serves itself, so the file is fetched with your session and never leaves the station. A picture, a sound, a video or a text file opens in the browser by its name, where a sound or a video plays. Download keeps the file, which the player at player.vibium.dev also takes by drag and drop.</p>
<p><b>Room.</b> {{.Count}} files, {{.Total}}. A file is at most {{.MaxFile}}; the store keeps {{.MaxTotal}} and {{if .MaxAge}}each file {{.MaxAge}}{{else}}files for ever{{end}}, after which the oldest go. A recording is also kept unpacked beside its zip, which is what the cards and the thumbnails read, so on disk it takes about twice its size. What the station recorded about a file, who uploaded it and on which hold, stays after the file itself is gone. The limits are the station's <code>--records-*</code> flags.</p>
</div>
{{if not .Records}}{{if .Filtered}}<p class="muted">Nothing matches. <a href="{{.ClearURL}}">Clear the search.</a></p>{{else}}<div class="msg">Nothing here yet. An agent uploads a recording with <code>POST /v1/records/upload-auth</code> and a Blossom <code>PUT /upload</code>; see the API reference.</div>{{end}}{{end}}
{{if and .Records .Grid}}<div class="cards">{{range .Records}}<article class="card rec{{if .Golden}} golden{{end}}{{if .Sound}} sound{{end}}">
<a class="thumb" href="{{.Open}}" aria-label="{{.Title}}">{{if .HasThumb}}<img src="/records/{{.SHA256}}/thumb.jpg" alt="" loading="lazy">{{else}}<span class="nothumb">{{.Type}}</span>{{end}}</a>
<div class="meta"><b><a href="{{.Open}}">{{.Title}}</a></b><span class="sub">{{.When}} · {{.Size}}{{if .Meta}} · {{.Meta}}{{end}}{{if .Public}} · shared{{end}}</span><span class="sub">{{if .DeviceID}}{{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}} · {{end}}{{.Uploader}}</span>{{if .TagLinks}}<span class="sub tags">{{range .TagLinks}}<a class="tag{{if .On}} on{{end}}" href="{{.URL}}">#{{.Tag}}</a>{{end}}</span>{{end}}
<span class="act">{{template "goldenbtn" (dict "R" . "CSRF" $.CSRF "Back" $.Back)}}{{if .CanPlay}}<a class="btn small icon" href="/records/{{.SHA256}}/play" title="Play" aria-label="Play">{{template "icon-play"}}</a> {{end}}<a class="btn small icon" href="{{.URL}}?download" title="Download" aria-label="Download">{{template "icon-download"}}</a></span></div>
</article>{{end}}</div>{{end}}
{{if and .Records .Strip}}<div class="strips">{{range .Records}}{{$r := .}}<article class="striprow{{if .Golden}} golden{{end}}{{if .Sound}} sound{{end}}">
<div class="strip"{{if .Strip}} data-strip{{end}}><a href="{{.Open}}" title="{{.OpenWord}}" aria-label="{{.OpenWord}} {{.Title}}">{{if .Strip}}{{range .Strip}}<img src="/records/{{$r.SHA256}}/frames/{{.}}.jpg" alt="" loading="lazy">{{end}}{{else if .HasThumb}}<img src="/records/{{.SHA256}}/thumb.jpg" alt="" loading="lazy">{{else}}<span class="nothumb">{{.Kind}}</span>{{end}}</a></div>
<div class="stripinfo"><span class="what"><b><a href="{{.Open}}">{{.Title}}</a></b><span class="sub">{{.When}}{{if .DeviceID}} · {{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}}{{end}} · {{.Size}}{{if .Meta}} · {{.Meta}}{{end}}{{if .Public}} · shared{{end}}</span>{{if .TagLinks}}<span class="sub tags">{{range .TagLinks}}<a class="tag{{if .On}} on{{end}}" href="{{.URL}}">#{{.Tag}}</a>{{end}}</span>{{end}}</span>
<span class="act">{{template "recordacts" (dict "R" . "CSRF" $.CSRF "Back" $.Back)}}</span></div>
</article>{{end}}</div>{{end}}
{{if and .Records .List}}<div class="tablewrap"><table class="records"><thead><tr><th></th><th>When</th><th>Device</th><th>File</th><th>From</th><th></th></tr></thead><tbody>
{{range .Records}}<tr{{if .Golden}} class="golden"{{end}}>
<td class="thumbcell"><a class="minithumb{{if .Sound}} sound{{end}}" href="{{.Open}}" title="{{.OpenWord}}" aria-label="{{.OpenWord}} {{.Title}}">{{if .HasThumb}}<img src="/records/{{.SHA256}}/thumb.jpg" alt="" loading="lazy">{{else}}<span class="nothumb"></span>{{end}}</a></td>
<td data-l="When">{{.When}}</td>
<td data-l="Device">{{if .DeviceID}}{{if .Label}}{{.Label}}{{else}}{{.DeviceID}}{{end}}{{else}}<span class="muted">none</span>{{end}}</td>
<td data-l="File"><b><a href="{{.Open}}">{{.Title}}</a></b><span class="sub">{{.Size}} · {{.Kind}}{{if .Meta}} · {{.Meta}}{{end}}{{if .Public}} · shared{{end}}</span>{{if .TagLinks}}<span class="sub tags">{{range .TagLinks}}<a class="tag{{if .On}} on{{end}}" href="{{.URL}}">#{{.Tag}}</a>{{end}}</span>{{end}}</td>
<td data-l="From">{{.Uploader}}{{if .HoldID}}<span class="sub">{{.HoldID}}</span>{{end}}</td>
<td class="act">{{template "recordacts" (dict "R" . "CSRF" $.CSRF "Back" $.Back)}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
{{if gt .Pages 1}}<nav class="pager" aria-label="Pages"><span class="muted">{{.From}}–{{.To}} of {{.Found}}</span>{{if .PrevURL}}<a class="btn small" href="{{.PrevURL}}">‹ Newer</a>{{end}}<span class="muted">Page {{.PageNo}} of {{.Pages}}</span>{{if .NextURL}}<a class="btn small" href="{{.NextURL}}">Older ›</a>{{end}}</nav>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "recordacts"}}{{template "goldenbtn" .}}{{if .R.CanPlay}}<a class="btn small icon" href="/records/{{.R.SHA256}}/play" title="Play" aria-label="Play">{{template "icon-play"}}</a> {{end}}<a class="btn small icon" href="{{.R.URL}}?download" title="Download" aria-label="Download">{{template "icon-download"}}</a>{{if .R.CanEdit}} <form class="inline" method="post" action="/records/public"><input type="hidden" name="csrf" value="{{$.CSRF}}">{{range $.Back}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}<input type="hidden" name="sha256" value="{{.R.SHA256}}"><input type="hidden" name="public" value="{{if .R.Public}}0{{else}}1{{end}}"><button class="small icon hold share{{if .R.Public}} on{{end}}" type="button" data-hold title="{{if .R.Public}}Shared: anyone with the link can open it. Hold to make it private{{else}}Hold to share: anyone with the link can open it{{end}}" aria-label="{{if .R.Public}}Shared. Hold to make it private{{else}}Hold to share{{end}}" aria-pressed="{{if .R.Public}}true{{else}}false{{end}}">{{template "icon-share"}}</button></form>{{else if .R.Public}} <span class="btn small icon share on" title="Shared: anyone with the link can open it" aria-label="Shared">{{template "icon-share"}}</span>{{end}}{{if .R.CanEdit}} <form class="inline" method="post" action="/records/delete"><input type="hidden" name="csrf" value="{{$.CSRF}}">{{range $.Back}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}<input type="hidden" name="sha256" value="{{.R.SHA256}}"><button class="small icon hold" type="button" data-hold title="Hold to delete" aria-label="Hold to delete">{{template "icon-delete"}}</button></form>{{end}}{{end}}
{{define "goldenbtn"}}{{if .R.CanEdit}}<form class="inline" method="post" action="/records/golden"><input type="hidden" name="csrf" value="{{.CSRF}}">{{range .Back}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">{{end}}<input type="hidden" name="sha256" value="{{.R.SHA256}}"><input type="hidden" name="make" value="{{if .R.Golden}}plain{{else}}golden{{end}}"><button class="small icon star{{if .R.Golden}} on{{end}}" type="submit" title="{{if .R.Golden}}Golden. Make it plain{{else}}Make it golden{{end}}" aria-label="{{if .R.Golden}}Golden. Make it plain{{else}}Make it golden{{end}}" aria-pressed="{{if .R.Golden}}true{{else}}false{{end}}">{{template "icon-star"}}</button></form> {{else if .R.Golden}}<span class="btn small icon star on" title="Golden" aria-label="Golden">{{template "icon-star"}}</span> {{end}}{{end}}

{{define "account"}}{{template "head" (dict "Title" "Account" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap">
<h2 class="h2">Account {{template "help" "account"}}</h2>
<p class="helptext" id="help-account" hidden>Who you are signed in as, the teams that decide what you see, and how you sign in. An admin sees every device, jam and record and manages the station on Settings; a regular user sees the devices their teams were given, and the jams and records made on them. Changing your password, making new backup codes and replacing the authenticator each ask for your current password.</p>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
<div class="tablewrap"><table class="kv">
<tr><th>Login</th><td>{{.User.Login}}</td></tr>
{{if .User.Name}}<tr><th>Name</th><td>{{.User.Name}}</td></tr>{{end}}
<tr><th>Role</th><td>{{if .User.Admin}}admin{{else}}regular user{{end}}</td></tr>
<tr><th>Teams</th><td>{{if .TeamNames}}{{range $i, $t := .TeamNames}}{{if $i}}, {{end}}{{$t}}{{end}}{{else}}<span class="muted">{{if .User.Admin}}all, as an admin{{else}}none yet{{end}}</span>{{end}}</td></tr>
<tr><th>Second factor</th><td>{{if .User.TwoFactor}}authenticator since {{.User.TwoFactorAt.Local.Format "Jan 2, 2006"}}, {{.BackupLeft}} backup {{if eq .BackupLeft 1}}code{{else}}codes{{end}} left{{else if .User.HasPassword}}<span class="muted">none yet; set up at your next password sign-in</span>{{else}}<span class="muted">no password; you sign in by identity</span>{{end}}</td></tr>
</table></div>
{{if not .Token}}<h2 class="h2">Password</h2>
<form class="row" method="post" action="/account/password"><input type="hidden" name="csrf" value="{{.CSRF}}">
{{if .User.HasPassword}}<input type="password" name="current" placeholder="current password" aria-label="Current password" autocomplete="current-password"> {{end}}<input type="password" name="password" placeholder="new password, 12 characters or more" aria-label="New password" autocomplete="new-password"> <input type="password" name="again" placeholder="again" aria-label="New password again" autocomplete="new-password">
<input type="submit" value="Change"></form>
{{if .User.TwoFactor}}<h2 class="h2">Authenticator</h2>
<form class="row" method="post" action="/account/codes"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="password" name="current" placeholder="current password" aria-label="Current password, for new backup codes" autocomplete="current-password"> <input type="submit" value="New backup codes"></form>
<form class="row" method="post" action="/account/authenticator"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="password" name="current" placeholder="current password" aria-label="Current password, to replace the authenticator" autocomplete="current-password"> <input type="submit" value="Replace the authenticator"></form>{{end}}
{{if .JamsPossible}}<h2 class="h2" id="jam-notes">Jam notes {{template "help" "my-jam-notes"}}</h2>
<p class="helptext" id="help-my-jam-notes" hidden>Your own standing guidance for the model, given to it with every jam you start, after the station's notes. A jam's transcript shows the notes it was given. They travel with every step of a jam, so short notes cost fewer tokens.</p>
<form method="post" action="/account/jam-notes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea class="notes" name="notes" rows="6" maxlength="{{.MaxNotes}}" aria-label="Notes for every jam you start" placeholder="Reload a page before you use its microphone.">{{.JamNotes}}</textarea>
<input type="submit" value="Save notes"></form>
{{end}}<h2 class="h2">API tokens {{template "help" "my-tokens"}}</h2>
<p class="helptext" id="help-my-tokens" hidden>A token lets a script or an agent act as you without signing in, sent as <code>Authorization: Bearer &lt;token&gt;</code>, at a level no higher than your own: watch only looks, drive holds and drives devices and runs jams and agents, admin also manages the station. {{if .TokenLevels}}You may make {{range $i, $l := .TokenLevels}}{{if $i}}, {{end}}{{$l}}{{end}} tokens.{{else}}You have no device access yet, so no token of yours could do anything.{{end}} The secret is shown once; the station keeps only a hash. Revoke stops a token at once.</p>
{{if .NewToken}}<div class="card featured">Token <b>{{.NewTokenName}}</b>, shown once:<p class="secret">{{.NewToken}}</p></div>{{end}}
{{if .Tokens}}<div class="tablewrap"><table><thead><tr><th>Name</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr></thead><tbody>
{{range .Tokens}}<tr><td data-l="Name"><b>{{.Name}}</b></td><td data-l="Access">{{.Level}}</td><td data-l="Created">{{.CreatedAt.Format "Jan 2, 2006"}}</td>
<td data-l="Last used">{{if .UsedAt.IsZero}}<span class="muted">never</span>{{else}}{{.UsedAt.Format "Jan 2 15:04"}}{{end}}</td>
<td class="act"><form class="inline" method="post" action="/account/tokens/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small hold" type="button" data-hold title="Hold to revoke">Revoke</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
{{if .TokenLevels}}<form class="row" method="post" action="/account/tokens"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" placeholder="name" aria-label="Token name" required> <select name="level" aria-label="Access level">{{range .TokenLevels}}<option value="{{.}}"{{if eq . "drive"}} selected{{end}}>{{.}}</option>{{end}}</select>
<input type="submit" value="Create token"></form>{{end}}{{end}}
</main>{{template "foot" .}}{{end}}

{{define "icon-account"}}<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><circle cx="12" cy="8" r="4" stroke="currentColor" stroke-width="2" fill="none"/><path d="M4 20.5c1.5-4 4.5-6 8-6s6.5 2 8 6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round"/></svg>{{end}}
{{define "icon-signout"}}<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-send"}}<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true"><path d="M12 20V5m0 0-6 6m6-6 6 6" stroke="currentColor" stroke-width="2.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}

{{define "home"}}{{template "head" (dict "Title" "New jam" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap home">
{{if .Message}}<div class="msg error" role="alert">{{.Message}}</div>{{end}}
<h1 class="ask">{{.Ask}} {{template "help" "jams"}}</h1>
<div class="helptext" id="help-jams" hidden>
<p>Type something to check or try, and a jam session begins. A model set up on Settings drives the device through Vibium's tools, and the page shows each step as it happens. A check ends with a verdict, PASS, FAIL or REVIEW. A try ends with COMPLETED or NOT COMPLETED. The session's header shows which one it was, and a follow-up of the same kind can change it.</p>
<p><b>Device and model.</b> If more than one device is online, or more than one agent model is set up, pick which to use beside the prompt. The examples under the box match the kind of device that is online.</p>
<p><b>While it runs.</b> The session holds the device, so Devices shows it as the holder. Taking the device from there ends the session at once. If the host records holds, the recording is linked from the session when it ends. A PASS, FAIL or COMPLETED ends the session and frees the device: at once when nobody is watching, and after a fifteen-second countdown when someone has the session's page open, who can keep it open or send a follow-up in that time. A REVIEW or NOT COMPLETED keeps it open for five minutes, so a person can look and send a follow-up, which continues the same conversation from where the model left off.</p>
<p><b>Cost.</b> Model calls go to the provider on the key the model was set up with, and the session's page shows the tokens used. A session ends after thirty minutes or a hundred actions.</p>
</div>
<form class="promptbox" method="post" action="/jams"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea name="prompt" class="prompt" rows="3" placeholder="Something to check, or something to try" required maxlength="4000" aria-label="Prompt">{{.Prompt}}</textarea>
<div class="promptbar">
{{if gt (len .Devices) 1}}<select name="device" aria-label="Device">{{range .Devices}}<option value="{{.ID}}">{{.Title}}</option>{{end}}</select>{{else}}{{range .Devices}}<input type="hidden" name="device" value="{{.ID}}"><span class="muted">{{.Title}}</span>{{else}}<span class="muted">no device is online</span>{{end}}{{end}}
{{if gt (len .Models) 1}}<select name="model" aria-label="Model">{{range .Models}}<option value="{{.Name}}"{{if .Default}} selected{{end}}>{{.Name}}</option>{{end}}</select>{{end}}
<button class="primary send" type="submit" aria-label="Send">{{template "icon-send"}}</button>
</div></form>
<p class="examples">{{range .Examples}}<button class="example" type="button">{{.}}</button>{{end}}</p>

</main>{{template "foot" .}}{{end}}

{{define "jams"}}{{template "head" (dict "Title" "Jams" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap jams">
<h2 class="h2">Jams {{template "help" "jams"}}</h2>
<p class="helptext" id="help-jams" hidden>Every jam session on this station, newest first: a title the AI model gave its prompt, when and who asked, on which device and with which AI model, and the result. A jam from before titles shows the start of its prompt. The title opens the session, with its transcript and the recording, and shows the whole prompt under the pointer. A new one starts from the <a href="/">front page</a>.</p>
{{if .Jams}}<div class="tablewrap"><table><thead><tr><th>Jam</th><th>When</th><th>Who</th><th>Device</th><th>AI model</th><th>Result</th></tr></thead><tbody>
{{range .Jams}}<tr><td data-l="Jam" class="prompt"><a href="/jams/{{.ID}}" title="{{.Prompt}}">{{if .Title}}{{.Title}}{{else}}{{brief .Prompt 120}}{{end}}</a></td><td data-l="When">{{.When}}</td><td data-l="Who">{{.Subject}}</td><td data-l="Device">{{.Label}}</td><td data-l="AI model">{{.Model}}</td>
<td data-l="Result">{{if .Open}}<span class="dot on"></span>running{{else if .Word}}<b class="verdict {{.Result}}">{{.Word}}</b>{{else}}<span class="muted">{{.Status}}</span>{{end}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="muted">No jams yet. <a href="/">Start one.</a></p>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "jam"}}{{template "head" (dict "Title" "Jam" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap jam{{if .Watch}} watching{{end}}">
<header class="jamhead">
<p class="crumb"><a href="/jams">‹ Jams</a></p>
<div class="titlerow"><h1 class="jamtitle" {{if .CanAct}}data-jam="{{.Jam.ID}}" data-csrf="{{.CSRF}}" data-max="{{.MaxTitle}}" data-title="{{.Jam.Title}}" data-prompt="{{.Jam.Prompt}}" role="textbox" aria-label="Title" spellcheck="false" title="Click to change the title"{{else}}title="{{.Jam.Prompt}}"{{end}}>{{if .Jam.Title}}{{.Jam.Title}}{{else}}{{.Jam.Prompt}}{{end}}</h1>
{{if .Word}}<span class="pill {{.Jam.Result}}">{{.Word}}</span>{{else if .Open}}<span class="pill running"><span class="dot on"></span>running</span>{{else}}<span class="pill">{{.Jam.Status}}</span>{{end}}
<span class="act">{{if and .Open (not .Watch)}}<form class="inline" method="post" action="/jams/{{.Jam.ID}}/stop"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="small icon" type="submit" title="Stop the jam" aria-label="Stop the jam">{{template "icon-stop"}}</button></form>{{end}}{{if .Play}}<a class="btn small icon" href="{{.Play}}" title="Play the recording" aria-label="Play the recording">{{template "icon-play"}}</a>{{end}}{{if not .Watch}}<a class="btn small icon" href="/devices/{{.Jam.DeviceID}}/drive" title="Drive the device" aria-label="Drive the device">{{template "icon-drive"}}</a>{{end}}</span></div>
<p class="meta">{{.When}} · {{.Jam.Subject}} · {{.DeviceLabel}} · AI model {{.Jam.Model}}{{if .Took}} · {{.Took}}{{end}} · {{.Jam.ID}}</p>
</header>
{{if .Message}}<div class="msg error" role="alert">{{.Message}}</div>{{end}}
<div class="jamgrid">
<div class="transcript" id="transcript" data-jam="{{.Jam.ID}}" data-after="{{.Last}}"{{if .Open}} data-open="1"{{end}}>
{{range .Events}}{{template "event" .}}{{end}}
</div>
{{if .Watch}}<aside class="watchside"><div class="watchbar"><form class="inline" method="post" action="/jams/{{.Jam.ID}}/stop"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="small icon" type="submit" title="Stop the jam">{{template "icon-stop"}} Stop</button></form><a class="btn small icon" href="/devices/{{.Jam.DeviceID}}/drive" title="Drive the device yourself">{{template "icon-drive"}} Drive</a></div>
<iframe class="watch" src="{{.Watch}}" title="Watching {{.DeviceLabel}}" allow="autoplay"></iframe></aside>{{end}}
</div>
{{if .Open}}<div class="grace" id="grace" data-until="{{.GraceUntil}}" data-total="{{.GraceSeconds}}" hidden><div class="bar"><span></span></div><p>The result is in. The session ends in <b class="left">{{.GraceSeconds}}</b> s unless you keep it open or send a follow-up.</p><form method="post" action="/jams/{{.Jam.ID}}/keep"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="small" type="submit">Keep it open</button></form></div>
<form class="promptbox" method="post" action="/jams/{{.Jam.ID}}/turns"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea name="prompt" class="prompt" rows="2" placeholder="A follow-up, once the result is in" required maxlength="4000" aria-label="Follow-up prompt"></textarea>
<div class="promptbar"><button class="primary send" type="submit" aria-label="Send">{{template "icon-send"}}</button></div></form>
{{else}}<p class="muted">{{.Jam.Status}} · {{.Jam.Actions}} actions · {{.Jam.TokensIn}} tokens in, {{.Jam.TokensOut}} out</p>{{end}}
</main>{{template "foot" .}}{{end}}

{{define "event"}}{{if eq .Kind "turn"}}<div class="ev turn"><span class="t">{{.When}}</span><div class="body"><b>Prompt</b><p>{{.Text}}</p></div></div>
{{else if eq .Kind "start"}}<div class="ev start"><span class="t">{{.When}}</span><div class="body muted">{{.Text}}</div></div>
{{else if eq .Kind "kept"}}<div class="ev kept"><span class="t">{{.When}}</span><div class="body muted">{{.Text}}</div></div>
{{else if eq .Kind "notes"}}<div class="ev notes"><span class="t">{{.When}}</span><details class="body"><summary>Notes given to the model</summary><p>{{.Text}}</p></details></div>
{{else if eq .Kind "text"}}<div class="ev text"><span class="t">{{.When}}</span><div class="body"><p>{{.Text}}</p></div></div>
{{else if eq .Kind "call"}}<div class="ev call"><span class="t">{{.When}}</span><div class="body"><code>{{.Tool}}{{if .Args}} {{.Args}}{{end}}</code></div></div>
{{else if eq .Kind "result"}}<div class="ev result{{if .Error}} error{{end}}"><span class="t">{{.When}}</span><div class="body">{{.Text}}</div></div>
{{else if eq .Kind "shot"}}<div class="ev shot"><span class="t">{{.When}}</span><div class="body">{{if .Image}}<a href="{{.Image}}" target="_blank" rel="noopener"><img src="{{.Image}}" alt="Screenshot {{.Tool}}" loading="lazy"></a>{{end}}</div></div>
{{else if eq .Kind "verdict"}}<div class="ev verdict"><span class="t">{{.When}}</span><div class="body"><b class="verdict {{.Verdict.Status}}">{{.Word}}</b> {{.Verdict.Summary}}{{if .Verdict.Evidence}}<ul>{{range .Verdict.Evidence}}<li>{{.}}</li>{{end}}</ul>{{end}}{{if .Verdict.Reason}}<span class="muted">{{.Verdict.Reason}}</span>{{end}}</div></div>
{{else if eq .Kind "error"}}<div class="ev error"><span class="t">{{.When}}</span><div class="body">{{.Text}}</div></div>
{{else if eq .Kind "brake"}}<div class="ev brake"><span class="t">{{.When}}</span><div class="body">{{.Text}}</div></div>
{{else if eq .Kind "end"}}<div class="ev end"><span class="t">{{.When}}</span><div class="body muted">{{.Text}}</div></div>
{{end}}{{end}}

{{define "play"}}{{template "head" (dict "Title" .Title "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap play">
<header class="jamhead playhead">
<p class="crumb"><a href="/records">‹ Records</a></p>
<div class="titlerow"><h1 class="jamtitle">{{.Title}}</h1>
{{if .CanEdit}}<form class="inline" method="post" action="/records/golden"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA}}"><input type="hidden" name="return" value="{{.Return}}"><input type="hidden" name="make" value="{{if .Golden}}plain{{else}}golden{{end}}"><button class="small icon star{{if .Golden}} on{{end}}" type="submit" title="{{if .Golden}}Golden. Make it plain{{else}}Make it golden{{end}}" aria-label="{{if .Golden}}Golden. Make it plain{{else}}Make it golden{{end}}" aria-pressed="{{if .Golden}}true{{else}}false{{end}}">{{template "icon-star"}}</button></form><form class="inline tagsform" method="post" action="/records/tags"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="sha256" value="{{.SHA}}"><input type="text" name="tags" value="{{.Tags}}" placeholder="#tags" aria-label="Tags" maxlength="900"><button class="small" type="submit">Save</button></form>{{else}}{{if .Golden}}<span class="btn small icon star on" title="Golden" aria-label="Golden">{{template "icon-star"}}</span>{{end}}{{if .TagLinks}}<span class="tags">{{range .TagLinks}}<a class="tag" href="{{.URL}}">#{{.Tag}}</a>{{end}}</span>{{end}}{{end}}
<span class="act"><a class="btn small icon" href="{{.Download}}" title="Download" aria-label="Download">{{template "icon-download"}}</a><a class="btn small icon" href="{{.Full}}" target="_blank" rel="noopener" title="Full page" aria-label="Full page">{{template "icon-open"}}</a></span></div>
<p class="meta">{{.When}} · {{.Size}} · {{.Uploader}}{{if .Hold}} · {{.Hold}}{{end}}{{if .Device}} · {{.Device}}{{end}}</p>
</header>
{{if .Message}}<div class="msg error" role="alert">{{.Message}}</div>{{end}}
<iframe class="player" src="{{.Player}}" title="{{.Title}} in the Record Player" allowfullscreen></iframe>
</main>{{template "foot" .}}{{end}}

{{define "playfull"}}{{template "head" (dict "Title" .Title "Name" .Name "Named" .Named)}}
<iframe class="player full" src="{{.Player}}" title="{{.Title}} in the Record Player" allowfullscreen></iframe>
</body></html>{{end}}

{{define "icon-drive"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="2" fill="none"/><circle cx="12" cy="12" r="2.5" stroke="currentColor" stroke-width="2" fill="none"/><path d="M12 14.5V21M9.8 10.9 4.1 7.6M14.2 10.9l5.7-3.3" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round"/></svg>{{end}}
{{define "icon-watch"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M2 12s3.5-6.5 10-6.5S22 12 22 12s-3.5 6.5-10 6.5S2 12 2 12z" stroke="currentColor" stroke-width="2" fill="none" stroke-linejoin="round"/><circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="2" fill="none"/></svg>{{end}}
{{define "icon-open"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-stop"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><rect x="6" y="6" width="12" height="12" rx="1.5" fill="currentColor"/></svg>{{end}}
{{define "icon-star"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M12 3.6l2.6 5.5 6 .8-4.4 4.2 1.1 6L12 17.2l-5.3 2.9 1.1-6-4.4-4.2 6-.8z" stroke="currentColor" stroke-width="2" fill="none" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-search"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5" stroke="currentColor" stroke-width="2" fill="none"/><path d="M15.5 15.5 21 21" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>{{end}}
{{define "icon-play"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M7 4.5v15l12-7.5z" fill="currentColor"/></svg>{{end}}
{{define "icon-delete"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M4 7h16M9 7V4.5h6V7M7 7l1 13h8l1-13M10 11v6M14 11v6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
{{define "icon-share"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><circle cx="18" cy="5" r="3" stroke="currentColor" stroke-width="2" fill="none"/><circle cx="6" cy="12" r="3" stroke="currentColor" stroke-width="2" fill="none"/><circle cx="18" cy="19" r="3" stroke="currentColor" stroke-width="2" fill="none"/><path d="M8.6 13.5l6.8 4M15.4 6.5l-6.8 4" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>{{end}}
{{define "icon-download"}}<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path d="M12 3v11m0 0-4.5-4.5M12 14l4.5-4.5M4 19.5h16" stroke="currentColor" stroke-width="2.2" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}

{{define "help"}}<button class="qhelp" type="button" aria-expanded="false" aria-controls="help-{{.}}" aria-label="What this is">?</button>{{end}}

{{define "settings"}}{{template "head" (dict "Title" "Settings" "Name" .Name "Named" .Named)}}{{template "header" .}}
<main class="wrap">
<nav class="tabs" aria-label="Settings">{{range .Tabs}}<a href="{{.URL}}"{{if eq .ID $.Tab}} aria-current="page"{{end}}>{{.Title}}</a>{{end}}</nav>
{{if .Message}}<div class="msg" role="alert">{{.Message}}</div>{{end}}
{{if eq .Tab "general"}}{{template "settings-general" .}}{{else if eq .Tab "ai"}}{{template "settings-ai" .}}{{else if eq .Tab "rigs"}}{{template "settings-rigs" .}}{{else}}{{template "settings-access" .}}{{end}}
</main>{{template "foot" .}}{{end}}

{{define "settings-general"}}<h2 class="h2">Name {{template "help" "name"}}</h2>
<p class="helptext" id="help-name" hidden>What this station is called: under the Vibium mark in the sidebar and in the tab title here, and what a rig records when it enrolls. The <code>--name</code> flag sets it too.</p>
<form class="row" method="post" action="/settings/name"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" value="{{.Name}}" aria-label="Station name" maxlength="60" required> <input type="submit" value="Save"></form>

<h2 class="h2">Appearance {{template "help" "appearance"}}</h2>
<p class="helptext" id="help-appearance" hidden>Light or dark for this browser, or whatever the device is set to. The choice stays in the browser, not on the station, so each browser has its own.</p>
<fieldset class="themepick" id="themePick"><legend class="sr">Theme</legend>
<label><input type="radio" name="theme" value="system"><span>System</span></label>
<label><input type="radio" name="theme" value="light"><span>Light</span></label>
<label><input type="radio" name="theme" value="dark"><span>Dark</span></label></fieldset>

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
{{end}}

{{define "settings-ai"}}<h2 class="h2" id="ai">AI models {{template "help" "ai"}}</h2>
<div class="helptext" id="help-ai" hidden>
<p>A jam session can use three kinds of model: an AI chat model that drives the device (Agent), text to speech (Say), and speech to text (Listen). You can add more than one of each. New jam sessions use the one marked <i>in use</i>, and <b>Use</b> switches to another.</p>
<p>Each model is saved as a file in <code>{{.AIDir}}</code>, in Vibium's <code>ai.env</code> format. Only the station's user can read these files, because they hold your API keys. <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md">Set Up Jams</a> walks through it step by step.</p>
</div>
{{range .AIParts}}<section class="aipart" id="ai-{{.Role}}"><h3>{{.Title}} {{template "help" (print "ai-" .Role)}}</h3>
<div class="helptext" id="help-ai-{{.Role}}" hidden>
{{if eq .Role "agent"}}<p>You can use OpenAI, Anthropic, Google Gemini, xAI, or any server with an OpenAI-compatible API, including one running on your own machine. When you add a model, the station sends it a short test message, so a wrong key or model name shows up right away. A model set in the station's environment is listed as <i>host</i>. <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md#2-an-agent-model">How to add an agent model</a></p>
{{else if eq .Role "say"}}<p>Piper runs on this station and needs no API key.{{if $.Piper.URL}} Its server is running at <code>{{$.Piper.URL}}</code>.{{end}} Its voices are the <code>.onnx</code> files in <code>{{if $.Piper.Dir}}{{$.Piper.Dir}}{{else}}piper/ in the data directory{{end}}</code>. To add a voice, download its <code>.onnx</code> and <code>.onnx.json</code> files into that folder and restart the station. If Piper doesn't start, look at <code>server.log</code> in the same folder.</p>
<p>To use OpenAI's text to speech instead, add it with <b>Add a voice</b> and your API key. <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md#4-talk-with-piper-on-this-machine">How to set up Piper</a> · <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md#3-voices-listen-and-say">How to add a cloud voice</a></p>
{{else}}<p>OpenAI's <code>gpt-transcribe</code> is the only option for now. Speech to text on this machine isn't supported yet. <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md#3-voices-listen-and-say">How to add a listen model</a></p>{{end}}
</div>
<p class="muted">{{.About}}</p>
{{if and (eq .Role "say") .Models}}<form class="sayprobe" data-preview-form><input type="hidden" name="csrf" value="{{$.CSRF}}"><label for="sayText">Test phrase</label> <input id="sayText" type="text" name="text" value="Come here, Watson." maxlength="50"> <span class="status" role="status"></span></form>{{end}}
{{if .Models}}<ul class="models">{{range .Models}}{{template "model-row" (dict "M" . "S" $)}}{{end}}</ul>{{else}}<p class="muted">None yet.</p>{{end}}
{{if and (eq .Role "say") (not $.Piper.URL)}}<p class="piperline muted">{{if $.Piper.Python}}Piper: {{if $.Piper.Missing}}{{$.Piper.Missing}}{{else}}starting.{{end}}{{else}}Piper isn't installed. <a href="https://github.com/VibiumDev/station/blob/main/docs/how-to-guides/set-up-jams.md#4-talk-with-piper-on-this-machine">How to install it</a>{{end}}</p>{{end}}
<details class="addmodel"><summary class="btn small">{{.Add}}</summary>
<form class="card modelform" method="post" action="/settings/models"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="role" value="{{.Role}}">
<div class="row"><input type="text" name="name" placeholder="Name, such as {{if eq .Role "agent"}}gpt or claude{{else if eq .Role "say"}}tts{{else}}stt{{end}}" aria-label="Name" required maxlength="40">
<select name="provider" aria-label="Provider">{{range .Providers}}<option value="{{.ID}}">{{.Label}}</option>{{end}}</select></div>
<div class="row"><input type="text" name="model" placeholder="{{if eq .Role "say"}}Model, or a Piper voice name{{else}}Model name{{end}}" aria-label="Model" required>
<input type="password" name="key" placeholder="API key{{if eq .Role "say"}} (not needed for Piper){{end}}" aria-label="API key" autocomplete="off"></div>
<div class="row"><input type="text" name="base_url" placeholder="{{if eq .Role "say"}}Base URL (a Piper server's address){{else}}Base URL (optional){{end}}" aria-label="Base URL">
{{if eq .Role "agent"}}<select name="effort" aria-label="Reasoning effort"><option value="">Reasoning effort: automatic</option>{{range $.Efforts}}{{if .}}<option value="{{.}}">{{.}}</option>{{end}}{{end}}</select>{{end}}
<label class="check"><input type="checkbox" name="default"> Use it now</label>
<input class="primary" type="submit" value="Add"></div></form></details></section>
{{end}}

<h2 class="h2" id="jams">Jam sessions {{template "help" "jams"}}</h2>
<p class="helptext" id="help-jams" hidden>Jam sessions let people type a prompt on the front page and have the AI chat model carry it out on a device. They turn on by themselves once you add an agent model. Turning them off hides the prompt and keeps your models and keys. Each session drives the device through <code>vibium mcp</code>, so the station needs the Vibium binary on its PATH or set with <code>--vibium</code>.</p>
<div class="tablewrap"><table class="kv">
<tr><th>Status</th><td>{{if .JamsPossible}}<form class="inline" method="post" action="/settings/jams"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="on" value="{{if .JamsOn}}0{{else}}1{{end}}">{{if .JamsOn}}on <button class="link" type="submit">turn off</button>{{else}}off <button class="link" type="submit">turn on</button>{{end}}</form>{{else}}<span class="muted">off until you add an agent model</span>{{end}}</td></tr>
{{if .JamsPossible}}<tr><th>Video</th><td><form class="inline" method="post" action="/settings/jam-video"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="on" value="{{if .JamVideo}}0{{else}}1{{end}}">{{if .JamVideo}}on <button class="link" type="submit">turn off</button>{{else}}off <button class="link" type="submit">turn on</button>{{end}} <span class="muted">Saves a video of the device's screen with each session.</span></form></td></tr>
{{end}}<tr><th>Vibium</th><td>{{if .Vibium}}<code>{{.Vibium}}</code>{{else}}<span class="muted">not found ({{.VibiumMissing}}). Put the Vibium binary on the station's PATH or set it with <code>--vibium</code>.</span>{{end}}</td></tr>
</table></div>

{{if .JamsPossible}}<h2 class="h2" id="jam-notes">Jam notes {{template "help" "jam-notes"}}</h2>
<p class="helptext" id="help-jam-notes" hidden>Instructions added to every jam session on this station, whoever starts it. Each person can also add their own on their Account page, and a session's transcript shows the notes it was given. Keep them short, because they're sent with every step of a session. They're guidance, not a rule: a prompt that asks for something else is followed. They're saved as <code>jam-notes.md</code> in the station's data directory, which you can also edit by hand. Changes apply to the next session.</p>
<form method="post" action="/settings/jam-notes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<textarea class="notes" name="notes" rows="6" maxlength="{{.MaxNotes}}" aria-label="Notes for every jam session on this station" placeholder="After a completed run or a passed check, leave the device on its home screen.">{{.JamNotes}}</textarea>
<input type="submit" value="Save notes"></form>
{{end}}{{end}}

{{define "model-row"}}{{$m := .M}}{{$s := .S}}<li{{if $m.Default}} class="inuse"{{end}}>
<div class="who"><b>{{$m.Name}}</b>{{if $m.Default}} <span class="pill inuse">in use</span>{{end}}
<span class="sub">{{if $m.OwnPiper}}Piper, on this station · no API key needed{{else}}{{providerName $m.Provider}} · {{$m.Model}}{{if $m.ReasoningEffort}} · {{$m.ReasoningEffort}} reasoning{{end}}{{if $m.BaseURL}} · {{$m.BaseURL}}{{end}} · {{if $m.Host}}set in the environment{{else if eq $m.Provider "piper-server"}}no API key needed{{else if $m.HasKey}}API key set{{else}}no API key{{end}}{{end}}</span></div>
<div class="acts">{{if $m.OwnPiper}}<form class="inline pipervoice" method="post" action="/settings/piper/voice"><input type="hidden" name="csrf" value="{{$s.CSRF}}">{{if gt (len $s.Piper.Voices) 1}}<select name="voice" aria-label="Piper voice"{{if $m.Default}} data-current="{{$m.Model}}"{{end}}>{{range $s.Piper.Voices}}<option{{if eq . $m.Model}} selected{{end}}>{{.}}</option>{{end}}</select>{{else}}<input type="hidden" name="voice" value="{{$m.Model}}"><span class="voice">{{$m.Model}}</span>{{end}}
<button class="small icon" type="button" data-preview="{{$m.Name}}" title="Hear this voice" aria-label="Hear this voice">{{template "icon-play"}}</button>
<button class="small" type="submit"{{if $m.Default}} hidden{{end}}>Use</button></form>
{{else}}{{if and (eq $m.Role "say") (or (eq $m.Provider "piper-server") (eq $m.Provider "openai"))}}<button class="small icon" type="button" data-preview="{{$m.Name}}" title="Hear this voice" aria-label="Hear {{$m.Name}}">{{template "icon-play"}}</button>
{{end}}{{if and (not $m.Default) (not $m.Host)}}<form class="inline" method="post" action="/settings/models/default"><input type="hidden" name="csrf" value="{{$s.CSRF}}"><input type="hidden" name="name" value="{{$m.Name}}"><button class="small" type="submit">Use</button></form>
{{end}}{{if not $m.Host}}<details class="more"><summary class="btn small icon" title="Key and remove" aria-label="Key and remove for {{$m.Name}}">⋯</summary><div class="menu">
{{if ne $m.Provider "piper-server"}}<form class="row" method="post" action="/settings/models/key"><input type="hidden" name="csrf" value="{{$s.CSRF}}"><input type="hidden" name="name" value="{{$m.Name}}"><input type="password" name="key" placeholder="New API key" aria-label="New API key for {{$m.Name}}" autocomplete="off"> <button class="small" type="submit">Replace key</button></form>
{{end}}<form method="post" action="/settings/models/remove"><input type="hidden" name="csrf" value="{{$s.CSRF}}"><input type="hidden" name="name" value="{{$m.Name}}"><button class="small hold" type="button" data-hold title="Hold to remove">Remove {{$m.Name}}</button></form>
</div></details>{{end}}{{end}}</div></li>{{end}}

{{define "settings-rigs"}}<h2 class="h2" id="enroll">Enroll a rig {{template "help" "enroll"}}</h2>
<p class="helptext" id="help-enroll" hidden>A rig is a machine with devices on it: a Raspberry Pi running Roadie with a phone wired to it. Enrolling joins it to this station. From then on the rig keeps a tunnel open to the station, the station lists the rig's devices, and holds on them go through the tunnel. An enrollment code is used once and expires after 15 minutes. Every code still open is listed here until it is used, cancelled or runs out, and Cancel, held for a moment, ends one at once. The station keeps codes hashed, so a code's text is shown only while the station that made it still runs. A code's last group is this station's key fingerprint, which the rig checks, so it enrolls with the station you meant. On the same network the rig finds the station by mDNS and the address can be left out.</p>
<form class="row" method="post" action="/settings/codes"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input class="primary" type="submit" value="New enrollment code"></form>
{{range $i, $c := .OpenCodes}}<div class="card code{{if and (eq $i 0) $c.Code}} featured{{end}}">
<div class="codehead"><span>{{if $c.Code}}On the rig, until {{$c.Until}}:{{else}}A code made at {{$c.Made}}, good until {{$c.Until}}.{{end}}</span>
<form class="inline" method="post" action="/settings/codes/cancel"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{$c.ID}}"><button class="small hold" type="button" data-hold title="Hold to cancel">Cancel</button></form></div>
{{if $c.Code}}<p class="secret">roadie enroll {{$.PublicURL}} --code {{$c.Code}}</p>{{end}}</div>{{end}}
{{if .OpenCodes}}<p class="muted">On the same network the URL can be left out: the rig finds the station over mDNS. A code ends with this station's key fingerprint, {{.Fingerprint}}, which the rig checks.</p>{{end}}

<h2 class="h2">Rigs {{template "help" "rigs"}}</h2>
<p class="helptext" id="help-rigs" hidden>The rigs enrolled here. Key is the rig's own public key, which signs everything the rig sends. Devices is what the rig announces each time it connects, so a change on the rig shows after its next connection. Remove refuses the rig's key from then on; to come back, it enrolls again with a new code.</p>
{{if not .Rigs}}<p class="muted">None enrolled.</p>{{end}}
{{if .Rigs}}<div class="tablewrap"><table><thead><tr><th>Rig</th><th>Key</th><th>Devices</th><th>Enrolled</th><th></th></tr></thead><tbody>
{{range .Rigs}}<tr><td data-l="Rig"><b>{{.Name}}</b><span class="sub">{{.ID}}</span></td><td data-l="Key"><code>{{npub .PubKey}}</code></td>
<td data-l="Devices">{{range .Devices}}{{.Title}} <span class="muted">({{.Kind}}{{if .Platform}}, {{platform .Platform}}{{if .OSVersion}} {{.OSVersion}}{{end}}{{end}})</span> {{end}}</td><td data-l="Enrolled">{{.EnrolledAt.Format "Jan 2, 2006"}}</td>
<td class="act"><form class="inline" method="post" action="/settings/rigs/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="rig" value="{{.ID}}"><button class="small hold" type="button" data-hold title="Hold to remove">Remove</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
{{end}}

{{define "settings-access"}}<h2 class="h2" id="users">Users {{template "help" "users"}}</h2>
<p class="helptext" id="help-users" hidden>Who can sign in. An admin sees every device, jam and record and manages the station here; a regular user sees the devices their teams were given, and the jams and records made on them, and nothing until then. Adding a user makes a setup link, good for a day, that you pass on: they choose their own password there and add an authenticator app, and every password sign-in asks for a code from it. Lock ends a user's sign-ins and tokens and keeps their history. Reset sign-in forgets their password and authenticator and makes a new link, for a forgotten password or a lost phone. Remove deletes their tokens too. You cannot lock yourself or take your own admin rights away.</p>
{{if .NoPassword}}<div class="msg">Nobody can sign in by password yet; <code>station passwd</code> on the station's machine creates the owner.</div>{{end}}
{{if .SetupURL}}<div class="card featured">Setup link for <b>{{.SetupLogin}}</b>, shown once and good for 24 hours. Pass it on by a channel you trust:<p class="secret">{{.SetupURL}}</p></div>{{end}}
{{if .Users}}<div class="tablewrap"><table><thead><tr><th>Login</th><th>Name</th><th>Role</th><th>Teams</th><th>Sign-in</th><th>Last sign-in</th><th></th></tr></thead><tbody>
{{range .Users}}<tr>
<td data-l="Login"><b>{{.Login}}</b></td>
<td data-l="Name">{{if .Name}}{{.Name}}{{else}}<span class="muted">—</span>{{end}}</td>
<td data-l="Role">{{if .Admin}}admin{{else}}regular{{end}}{{if .Self}} <span class="muted">(you)</span>{{end}}</td>
<td data-l="Teams">{{if .Admin}}<span class="muted">all</span>{{else if .TeamNames}}{{range $i, $t := .TeamNames}}{{if $i}}, {{end}}{{$t}}{{end}}{{else}}<span class="muted">none</span>{{end}}</td>
<td data-l="Sign-in">{{.SignIn}}</td>
<td data-l="Last sign-in">{{if .LastLogin.IsZero}}<span class="muted">never</span>{{else}}{{.LastLogin.Local.Format "Jan 2 15:04"}}{{end}}</td>
<td class="act">{{if not .Self}}<form class="inline" method="post" action="/settings/users/role"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="user" value="{{.ID}}"><input type="hidden" name="admin" value="{{if .Admin}}0{{else}}1{{end}}"><button class="small" type="submit">{{if .Admin}}Make regular{{else}}Make admin{{end}}</button></form> <form class="inline" method="post" action="/settings/users/lock"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="user" value="{{.ID}}"><input type="hidden" name="locked" value="{{if .Locked}}0{{else}}1{{end}}"><button class="small" type="submit">{{if .Locked}}Unlock{{else}}Lock{{end}}</button></form> {{end}}<form class="inline" method="post" action="/settings/users/reset"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="user" value="{{.ID}}"><button class="small hold" type="button" data-hold title="Hold to forget their password and authenticator and make a new setup link">Reset sign-in</button></form>{{if not .Self}} <form class="inline" method="post" action="/settings/users/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="user" value="{{.ID}}"><button class="small hold" type="button" data-hold title="Hold to remove">Remove</button></form>{{end}}</td>
</tr>{{end}}</tbody></table></div>{{end}}
<form class="card modelform" method="post" action="/settings/users"><input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="row"><input type="text" name="login" placeholder="login, such as ann@example.com" aria-label="Login" autocomplete="off" autocapitalize="none" spellcheck="false" required> <input type="text" name="name" placeholder="name" aria-label="Name" autocomplete="off"> <label class="check"><input type="checkbox" name="admin"> admin</label> <input type="submit" value="Add user"></div></form>

<h2 class="h2" id="teams">Teams {{template "help" "teams"}}</h2>
<p class="helptext" id="help-teams" hidden>A team is a group of users; devices are shared with teams, under Device access. A user may be in several teams, and admins are in every team without being listed.</p>
{{if .Teams}}<div class="tablewrap"><table><thead><tr><th>Team</th><th>Members</th><th></th></tr></thead><tbody>
{{range .Teams}}<tr>
<td data-l="Team"><b>{{.Name}}</b><span class="sub">{{.ID}}</span></td>
<td data-l="Members">{{range .Members}}<span class="member">{{.Login}} <form class="inline" method="post" action="/settings/teams/members"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="team" value="{{$.ID}}"><input type="hidden" name="user" value="{{.ID}}"><input type="hidden" name="in" value="0"><button class="link" type="submit" title="Take {{.Login}} out of the team" aria-label="Take {{.Login}} out of the team">×</button></form></span> {{end}}{{if not .Members}}<span class="muted">nobody yet</span>{{end}}{{if .Joinable}} <form class="inline" method="post" action="/settings/teams/members"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="team" value="{{.ID}}"><select name="user" aria-label="Add a member to {{.Name}}">{{range .Joinable}}<option value="{{.ID}}">{{.Login}}</option>{{end}}</select> <button class="small" type="submit">Add</button></form>{{end}}</td>
<td class="act"><form class="inline" method="post" action="/settings/teams/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="team" value="{{.ID}}"><button class="small hold" type="button" data-hold title="Hold to remove">Remove</button></form></td>
</tr>{{end}}</tbody></table></div>{{end}}
<form class="row" method="post" action="/settings/teams"><input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="text" name="name" placeholder="team name" aria-label="Team name" required> <input type="submit" value="Add team"></form>

<h2 class="h2">Device access {{template "help" "access"}}</h2>
<p class="helptext" id="help-access" hidden>What each team may do with each device. Watch opens the watching page and shows the device's jams and records; drive takes the device from the viewer too; jam starts jams and drives it with agents over the API as well. Nothing is shared until it is set here, and a change saves at once.</p>
{{if and .Teams .AccessRows}}<div class="tablewrap"><table class="access"><thead><tr><th>Device</th>{{range .Teams}}<th>{{.Name}}</th>{{end}}</tr></thead><tbody>
{{range .AccessRows}}{{$row := .}}<tr><td data-l="Device"><b>{{.Device.Title}}</b><span class="sub">{{.Device.ID}}</span></td>
{{range .Cells}}<td data-l="{{.Team.Name}}"><form class="inline" method="post" action="/settings/access"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="device" value="{{$row.Device.ID}}"><input type="hidden" name="team" value="{{.Team.ID}}"><select name="level" data-submit aria-label="{{.Team.Name}} on {{$row.Device.Title}}">{{$cell := .}}{{range $.Levels}}<option value="{{.}}"{{if eq . $cell.Level}} selected{{end}}>{{if .}}{{.}}{{else}}none{{end}}</option>{{end}}</select></form></td>{{end}}
</tr>{{end}}</tbody></table></div>{{else}}<p class="muted">{{if not .Teams}}Make a team, and the devices appear here to share.{{else}}No devices yet.{{end}}</p>{{end}}

<h2 class="h2" id="tokens">API tokens {{template "help" "tokens"}}</h2>
<p class="helptext" id="help-tokens" hidden>A token lets an agent or a script use this station without signing in, sent as <code>Authorization: Bearer &lt;token&gt;</code>. It acts as its user, within that user's teams, and within its own level: <b>watch</b> only looks; <b>drive</b> holds and drives devices, starts jams and runs agents, which is what most tokens need; <b>admin</b> also manages the station, users and teams. A token's level is capped by its user's own: admin for an admin, drive for a user with drive or jam on some device, watch for one who may only watch. Everyone makes their own on their Account page; here you make anyone's. The secret is shown once, when the token is made; the station keeps only a hash of it. Revoke stops the token at once.</p>
{{if .NewToken}}<div class="card featured">Token <b>{{.NewTokenName}}</b>, shown once:<p class="secret">{{.NewToken}}</p></div>{{end}}
{{if .Tokens}}<div class="tablewrap"><table><thead><tr><th>Name</th><th>User</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr></thead><tbody>
{{range .Tokens}}<tr><td data-l="Name"><b>{{.Name}}</b></td><td data-l="User">{{with index $.TokenUsers .UserID}}{{.}}{{else}}owner{{end}}</td><td data-l="Access">{{.Level}}</td><td data-l="Created">{{.CreatedAt.Format "Jan 2, 2006"}}</td>
<td data-l="Last used">{{if .UsedAt.IsZero}}<span class="muted">never</span>{{else}}{{.UsedAt.Format "Jan 2 15:04"}}{{end}}</td>
<td class="act"><form class="inline" method="post" action="/settings/tokens/revoke"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="name" value="{{.Name}}"><button class="small hold" type="button" data-hold title="Hold to revoke">Revoke</button></form></td></tr>{{end}}</tbody></table></div>{{end}}
<form class="row" method="post" action="/settings/tokens"><input type="hidden" name="csrf" value="{{.CSRF}}">
<select name="user" aria-label="For which user">{{range .Users}}<option value="{{.ID}}"{{if .Self}} selected{{end}}>{{.Login}}</option>{{end}}</select>
<input type="text" name="name" placeholder="name" aria-label="Token name" required> <select name="level" aria-label="Access level"><option value="watch">watch</option><option value="drive" selected>drive</option><option value="admin">admin</option></select>
<input type="submit" value="Create token"></form>
{{end}}
`))

// providerName is how Settings names a model provider, such as OpenAI.
func providerName(id string) string {
	if p, ok := jam.ProviderByID(id); ok {
		return p.Label
	}
	return id
}

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
