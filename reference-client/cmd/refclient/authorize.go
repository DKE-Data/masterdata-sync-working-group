package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Authorizing this application for a farming business.
//
// Client credentials say who the application is, and that is all they say.
// Acting in a tenant's data needs the tenant's user to have said so, and that
// authorization is granted in agrirouter's own screens — a participant cannot
// ask for it over the API, which is the point of it.
//
// So it is a browser step, and the only one in this program: the person is sent
// to agrirouter, logs in, approves the application, and is sent back here with
// the tenant they approved it for. What changes on this side is nothing —
// afterwards the same client-credentials token is accepted for that tenant,
// because the grant is recorded against the pair and not handed out as a
// credential of its own.
//
// Modelled on rac-sample-app, which does the same thing for the remote app
// connection flow. The shape is agrirouter's rather than this sample's:
//
//	GET {app}/api/authorize?client_id=…&redirect_uri=…
//	→ {redirect_uri}?tenant_id=…
//
// The redirect target is this participant's own address, which is what a client
// is registered with. So the tenant lands on the page a person would open
// anyway, and the query parameter is what says which of the two just happened.
//
// It runs before the screens rather than inside them because there is nothing
// to show until it is done: no endpoint, no stream, and a database belonging to
// a tenant this participant may not act in.

// authorizeTimeout bounds how long the process waits at the consent screen. It
// is generous because a person is logging in on the far side of it, and bounded
// because a participant nobody came back to should exit rather than hold a port.
const authorizeTimeout = 15 * time.Minute

// awaitAuthorization serves the consent step and returns the tenant the person
// authorized this application for.
//
// The tenant comes back from agrirouter rather than from configuration, and is
// the answer to "which farming business did they approve", which a participant
// cannot know beforehand. Where one was configured anyway, [run] checks the two
// agree rather than quietly acting in whichever arrived.
func awaitAuthorization(ctx context.Context, c config) (uuid.UUID, error) {
	if c.authorizeURL == "" {
		return uuid.Nil, errors.New(
			"no authorization: pass -authorize-url to be sent to agrirouter for it")
	}

	ctx, cancel := context.WithTimeout(ctx, authorizeTimeout)
	defer cancel()

	// Buffered so the callback's handler is never blocked on a reader that has
	// already given up, which would hold the browser on a spinner.
	granted := make(chan uuid.UUID, 1)
	failed := make(chan error, 1)

	mux := http.NewServeMux()
	// One address, two arrivals. A person opening the participant is asking
	// what to do; agrirouter sending them back carries the tenant, and that is
	// the only thing that tells the two apart — the redirect is registered as
	// this application's address, with no route of its own to land on.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tenant_id") == "" {
			renderConsent(w, c, "")
			return
		}
		tenant, err := tenantFrom(r)
		if err != nil {
			renderConsent(w, c, err.Error())
			return
		}
		select {
		case granted <- tenant:
		default:
		}
		renderGranted(w, tenant)
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, authorizeRedirect(c), http.StatusFound)
	})
	// Anything else while unauthorized is the same story, and a person following
	// a bookmark to /objects should be told it rather than shown a 404.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		renderConsent(w, c, "")
	})

	server := &http.Server{
		Addr: c.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !isClosed(err) {
			failed <- err
		}
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	select {
	case tenant := <-granted:
		return tenant, nil
	case err := <-failed:
		return uuid.Nil, err
	case <-ctx.Done():
		return uuid.Nil, fmt.Errorf("waiting to be authorized: %w", ctx.Err())
	}
}

// authorizeRedirect builds where the person is sent.
//
// client_id is the OAuth client's, not the application's: what is being
// authorized is the client that will present a token, and agrirouter resolves
// the application from it. The redirect target has to be one the client is
// registered with, or agrirouter refuses to send anybody back to it.
func authorizeRedirect(c config) string {
	u, _ := url.Parse(strings.TrimSuffix(c.authorizeURL, "/") + "/api/authorize")
	q := u.Query()
	q.Set("client_id", c.oauthClientID)
	q.Set("redirect_uri", c.callbackURL())
	u.RawQuery = q.Encode()
	return u.String()
}

func tenantFrom(r *http.Request) (uuid.UUID, error) {
	raw := r.URL.Query().Get("tenant_id")
	if raw == "" {
		return uuid.Nil, errors.New(
			"agrirouter sent no tenant_id back, so nothing was authorized")
	}
	tenant, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant_id is not a uuid: %w", err)
	}
	return tenant, nil
}

var consentPage = template.Must(template.New("consent").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<title>{{.Instance}} — authorize</title>
<style>
  body { font: 15px/1.5 system-ui, sans-serif; margin: 0; color: #182018; }
  header { background: #1d3320; color: #fff; padding: 12px 24px; }
  header h1 { font-size: 17px; margin: 0; font-weight: 600; }
  main { padding: 20px 24px; max-width: 680px; }
  .problem { background: #fbeceb; border: 1px solid #e3b6b2; padding: 8px 12px;
             border-radius: 3px; margin-bottom: 14px; }
  .muted { color: #5c6b5c; font-size: 13px; }
  a.button { display: inline-block; background: #2a4a2e; color: #fff; padding: 7px 14px;
             border-radius: 3px; text-decoration: none; }
  table { border-collapse: collapse; margin-top: 18px; }
  td { padding: 3px 14px 3px 0; vertical-align: top; }
  td:first-child { color: #5c6b5c; }
  code { font-family: ui-monospace, monospace; font-size: 13px; }
</style></head><body>
<header><h1>{{.Instance}} <span class="muted">— not authorized yet</span></h1></header>
<main>
{{if .Problem}}<div class="problem">{{.Problem}}</div>{{end}}
<p>
  This application holds no authorization for a farming business. Granting one
  is done in agrirouter, by whoever the data belongs to.
</p>
<p><a class="button" href="/authorize">Authorize with agrirouter</a></p>
<table>
  <tr><td>agrirouter</td><td><code>{{.BaseURL}}</code></td></tr>
  <tr><td>Client</td><td><code>{{.ClientID}}</code></td></tr>
  <tr><td>Comes back to</td><td><code>{{.Callback}}</code></td></tr>
  {{if .Tenant}}<tr><td>Expected tenant</td><td><code>{{.Tenant}}</code></td></tr>{{end}}
</table>
</main></body></html>`))

func renderConsent(w http.ResponseWriter, c config, problem string) {
	tenant := ""
	if c.tenantID != uuid.Nil {
		tenant = c.tenantID.String()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentPage.Execute(w, struct {
		Instance, BaseURL, ClientID, Callback, Tenant, Problem string
	}{c.instance, c.authorizeURL, c.oauthClientID, c.callbackURL(), tenant, problem})
}

func renderGranted(w http.ResponseWriter, tenant uuid.UUID) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="refresh" content="3">
<title>authorized</title>
<style>body { font: 15px/1.5 system-ui, sans-serif; margin: 40px; }</style></head>
<body><p>Authorized for tenant <code>%s</code>.</p>
<p>Onboarding now — this page reloads into the participant's screens.</p>
</body></html>`, template.HTMLEscapeString(tenant.String()))
}
