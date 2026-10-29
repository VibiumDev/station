package rig

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"fiatjaf.com/nostr"
	"golang.org/x/time/rate"

	"github.com/VibiumDev/station"
	"github.com/VibiumDev/station/token"
)

// Rule allows one endpoint to relayed requests holding a scope.
type Rule struct {
	Method string        // empty means any
	Path   string        // exact, or a prefix when it ends with "/"
	Scope  station.Scope // what the grant must carry
	// FirstMessage marks a WebSocket whose grant arrives in its first
	// message rather than a header: the request passes without one and the
	// handler checks it through FirstMessageCheck.
	FirstMessage bool
	// Max caps the connections one hold may have open on this rule at
	// once, for streams and sockets; zero means no cap.
	Max int
}

// Allowlist is what a rig lets the station reach. Anything not listed is
// refused: the rig's admin endpoints never get a rule for user scopes.
type Allowlist []Rule

// Match finds the rule for a request.
func (a Allowlist) Match(r *http.Request) (Rule, bool) {
	for _, rule := range a {
		if rule.Method != "" && rule.Method != r.Method {
			continue
		}
		if strings.HasSuffix(rule.Path, "/") {
			if strings.HasPrefix(r.URL.Path, rule.Path) {
				return rule, true
			}
			continue
		}
		if r.URL.Path == rule.Path {
			return rule, true
		}
	}
	return Rule{}, false
}

type ctxKey int

const (
	ctxGrant ctxKey = iota
	ctxCheck
	ctxLimiter
)

// LimiterFrom returns the hold's rate limiter for a relayed request, so
// a handler reading a stream of messages can pace them with Wait; nil
// when the request did not come through the tunnel.
func LimiterFrom(ctx context.Context) *rate.Limiter {
	l, _ := ctx.Value(ctxLimiter).(*rate.Limiter)
	return l
}

// GrantFrom returns the grant a relayed request was admitted with.
func GrantFrom(ctx context.Context) (token.Grant, bool) {
	g, ok := ctx.Value(ctxGrant).(token.Grant)
	return g, ok
}

// FirstMessageCheck returns the check a WebSocket handler runs on the
// grant in its first message, or nil when the request did not come
// through the tunnel and needs none.
func FirstMessageCheck(ctx context.Context) func(ev nostr.Event) (token.Grant, error) {
	f, _ := ctx.Value(ctxCheck).(func(ev nostr.Event) (token.Grant, error))
	return f
}

func refuse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Middleware admits relayed requests: the endpoint must be in the
// allowlist and the grant in the Authorization header must pass the gate
// with the rule's scope.
func (g *Gate) Middleware(allow Allowlist, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rule, ok := allow.Match(r)
		if !ok {
			refuse(w, http.StatusForbidden, "not available through the station")
			return
		}
		if rule.FirstMessage {
			check := func(ev nostr.Event) (token.Grant, error) { return g.Check(ev, rule.Scope) }
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxCheck, check)))
			return
		}
		h := r.Header.Get("Authorization")
		if h == "" {
			refuse(w, http.StatusUnauthorized, "a hold grant is required: Authorization: "+token.Scheme+" <grant>")
			return
		}
		ev, err := token.FromHeader(h)
		if err != nil {
			refuse(w, http.StatusUnauthorized, err.Error())
			return
		}
		grant, err := g.Check(ev, rule.Scope)
		if err != nil {
			refuse(w, http.StatusForbidden, err.Error())
			return
		}
		// The hold's limits: requests and messages per second, and how
		// many connections it may hold open on this endpoint. Every
		// connection is tracked, so the hold's end closes it.
		lim := g.limiter(grant.HoldID)
		if !lim.Allow() {
			refuse(w, http.StatusTooManyRequests, "too many requests on this hold")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		reg := g.track(grant.HoldID, rule, cancel)
		if reg == nil {
			refuse(w, http.StatusTooManyRequests, "this hold already has that many open")
			return
		}
		defer g.untrack(grant.HoldID, reg)
		ctx = context.WithValue(ctx, ctxGrant, grant)
		ctx = context.WithValue(ctx, ctxLimiter, lim)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
