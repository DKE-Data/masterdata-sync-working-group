package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

//go:embed templates/*.html
var templateFiles embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"clock": func(t time.Time) string { return t.Format("15:04:05") },
}).ParseFS(templateFiles, "templates/*.html"))

// handler builds the screens.
//
// They are the whole of what this program adds to the scenarios: the same
// platform, driven by a person rather than by a script, so that the parts of
// the protocol a reader has to see happen — a load stopping for a question, an
// object crossing between two participants — can be watched instead of read
// about.
func (in *instance) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", in.showDashboard)
	mux.HandleFunc("POST /load", in.postLoad)
	mux.HandleFunc("POST /load/replay", in.postReplay)
	mux.HandleFunc("GET /objects", in.showObjects)
	mux.HandleFunc("POST /objects", in.postObject)
	mux.HandleFunc("POST /objects/deactivate", in.postDeactivate)
	mux.HandleFunc("POST /objects/delete", in.postDelete)
	mux.HandleFunc("GET /decisions", in.showDecisions)
	mux.HandleFunc("POST /decisions", in.postDecision)
	mux.HandleFunc("POST /decisions/request", in.postRequest)
	mux.HandleFunc("GET /log", in.showLog)
	mux.HandleFunc("GET /events", in.streamEvents)
	return mux
}

// page is what every screen carries: who this participant is, and the handful
// of facts that say where it stands.
type page struct {
	Instance   string
	Tenant     uuid.UUID
	EndpointID uuid.UUID
	ExternalID string
	BaseURL    string

	Routed    []agmasync.EntityType
	LoadState string
	Position  string
	CaughtUp  bool
	LastErr   string
	Pending   int
	Blocked   []psync.BlockedObject

	// RoutingUnknown is agrirouter saying a load is owed while nothing here says
	// what this endpoint is routed to.
	RoutingUnknown bool

	// Section marks the current screen for the navigation.
	Section string

	// Notice and Problem are the outcome of whatever the person just did.
	Notice  string
	Problem string
}

func (in *instance) page(section string) page {
	in.mu.Lock()
	p := page{
		Instance: in.cfg.instance, Tenant: in.cfg.tenantID,
		EndpointID: in.endpointID, ExternalID: in.cfg.externalID(),
		BaseURL: in.cfg.baseURL,
		// A state of "" is an endpoint routed to nothing, which has no
		// initial-load state at all rather than an empty one.
		LoadState:      orNone(in.state),
		CaughtUp:       in.caughtUp,
		LastErr:        in.lastErr,
		Blocked:        in.blocked,
		RoutingUnknown: in.routingUnknown,
		Section:        section,
	}
	in.mu.Unlock()

	p.Pending = len(in.inbox.Pending())

	// Both of these go through the read pool, and the position in particular:
	// [store.Store.Position] reads through the writer, which is the one
	// connection a reconciliation waiting for a person is holding. A screen that
	// asks the question must not queue behind the answer.
	_ = in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		p.Routed, _ = tx.Route(in.endpointID)
		p.Position, _ = tx.Position()
		return nil
	})
	return p
}

func (in *instance) showDashboard(w http.ResponseWriter, r *http.Request) {
	p := in.page("dashboard")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")
	in.render(w, "dashboard.html", struct {
		page
		Entries []logEntry
	}{p, first(in.log.recent(), 12)})
}

// postLoad runs the initial load now, against the routing this participant
// holds.
//
// Ordinarily nothing has to ask for this: the routing frame starts a load and
// startup runs whatever was owed while the participant was down. It is here for
// when neither happened — a load that failed part way, or one whose signal was
// lost — and it is safe to press at any time, the loader re-entering at
// whatever state the endpoint is actually in and doing nothing where a load is
// already complete.
func (in *instance) postLoad(w http.ResponseWriter, r *http.Request) {
	in.wantLoad(nil)
	redirect(w, r, "/", "running the initial load; watch the log", "")
}

// postReplay asks for the stream from the beginning.
//
// This is the heavier one, and the only recovery for routing that never
// arrived. Routing reaches a participant on the stream and nowhere else, and
// catch-up restates it only above the participant's position — so an endpoint
// that took a position past the frame it failed to record has to go back for
// it.
func (in *instance) postReplay(w http.ResponseWriter, r *http.Request) {
	in.replayFromStart()
	redirect(w, r, "/",
		"reconnecting from the beginning; everything is redelivered and what is "+
			"already held is recognised as older and skipped", "")
}

// objectsView is the platform's own records beside what agrirouter calls them —
// the two tables the schema draws a line between, shown side by side because
// the line is the thing worth seeing.
type objectsView struct {
	page
	Types  []agmasync.EntityType
	Groups []objectGroup
}

type objectGroup struct {
	Type agmasync.EntityType
	// Routed says whether this type is still one the user routed. Objects of a
	// type routed away from stay held and stay shown — narrowing a routing does
	// not unmake what was already received — but nothing more can be sent about
	// them.
	Routed  bool
	Records []objectRow
}

type objectRow struct {
	LocalID      string
	AgrirouterID string
	Revision     string
	Archived     bool
	Unbound      bool
	Attributes   []attribute
}

func (in *instance) showObjects(w http.ResponseWriter, r *http.Request) {
	p := in.page("objects")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")

	// Offered types are the routed ones, not every type the model has. Sending a
	// type the user has not routed is refused by agrirouter, so a form offering
	// it would be teaching a send that cannot happen.
	view := objectsView{page: p, Types: p.Routed}
	err := in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		for _, typ := range agmasync.DependencyOrder {
			localIDs, err := tx.LocalIDs(typ)
			if err != nil {
				return err
			}
			if len(localIDs) == 0 {
				continue
			}

			group := objectGroup{Type: typ, Routed: slices.Contains(p.Routed, typ)}
			for _, localID := range localIDs {
				row, err := objectRowOf(tx, typ, localID)
				if err != nil {
					return err
				}
				group.Records = append(group.Records, row)
			}
			view.Groups = append(view.Groups, group)
		}
		return nil
	})
	if err != nil {
		view.Problem = err.Error()
	}
	in.render(w, "objects.html", view)
}

func objectRowOf(tx *store.Tx, typ agmasync.EntityType, localID string) (objectRow, error) {
	out := objectRow{LocalID: localID, AgrirouterID: "—", Revision: "—"}

	record, err := tx.LoadRecord(typ, localID)
	if err != nil {
		return out, err
	}
	out.Archived = record.Archived
	out.Attributes = attributesOfRecord(record)

	switch row, err := tx.SyncRow(typ, localID); {
	case err == nil:
		out.Unbound = row.Unbound
		if row.AgrirouterID != nil {
			out.AgrirouterID = row.AgrirouterID.String()
		}
		if row.Revision != nil {
			out.Revision = fmt.Sprint(*row.Revision)
		}
	case !isNotFound(err):
		return out, err
	}
	return out, nil
}

func (in *instance) postObject(w http.ResponseWriter, r *http.Request) {
	typ, err := agmasyncType(r.FormValue("type"))
	if err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}

	// The form only offers routed types, but a page held open across a routing
	// change still posts the old ones. Saying so here is kinder than the 403
	// agrirouter would answer with, and stops the record being written for a
	// send that cannot happen.
	routed, err := in.routedTypes()
	if err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	if !slices.Contains(routed, typ) {
		redirect(w, r, "/objects", "", fmt.Sprintf(
			"this endpoint is not routed to exchange %s: agrirouter refuses it", typ))
		return
	}

	localID, err := in.send(r.Context(), typ,
		strings.TrimSpace(r.FormValue("local_id")),
		[]byte(strings.TrimSpace(r.FormValue("attributes"))))
	if err != nil {
		// The record was written even where the send failed, which is why this
		// says which record rather than only what went wrong.
		redirect(w, r, "/objects", "",
			fmt.Sprintf("%s %s was stored but not sent: %v", typ, localID, err))
		return
	}
	redirect(w, r, "/objects", fmt.Sprintf("%s %s sent", typ, localID), "")
}

func (in *instance) postDeactivate(w http.ResponseWriter, r *http.Request) {
	typ, err := agmasyncType(r.FormValue("type"))
	if err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	localID := r.FormValue("local_id")

	if err := in.deactivate(r.Context(), typ, localID); err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	redirect(w, r, "/objects", fmt.Sprintf("%s %s deactivated", typ, localID), "")
}

func (in *instance) postDelete(w http.ResponseWriter, r *http.Request) {
	typ, err := agmasyncType(r.FormValue("type"))
	if err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	localID := r.FormValue("local_id")

	if err := in.deleteRecord(typ, localID); err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	redirect(w, r, "/objects",
		fmt.Sprintf("%s %s deleted; agrirouter was not told, "+
			"because there is no mapping left to tell it about", typ, localID), "")
}

type decisionsView struct {
	page
	// Questions rather than Pending: the embedded page carries a Pending count
	// for the navigation, and a field of the same name here would shadow it and
	// print a slice where the count belongs.
	Questions []*decision
	Answered  []answered
	Timeout   time.Duration
}

func (in *instance) showDecisions(w http.ResponseWriter, r *http.Request) {
	p := in.page("decisions")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")
	in.render(w, "decisions.html", decisionsView{
		page:      p,
		Questions: in.inbox.Pending(),
		Answered:  in.inbox.Answered(),
		Timeout:   in.cfg.decisionTimeout,
	})
}

func (in *instance) postDecision(w http.ResponseWriter, r *http.Request) {
	a := answer{Kind: r.FormValue("kind"), LocalID: r.FormValue("local_id")}
	if a.Kind == "match" && a.LocalID == "" {
		redirect(w, r, "/decisions", "", "no record was chosen")
		return
	}
	if err := in.inbox.Answer(r.FormValue("id"), a); err != nil {
		redirect(w, r, "/decisions", "", err.Error())
		return
	}
	redirect(w, r, "/decisions", "answered", "")
}

// postRequest asks agrirouter for a blocked object again.
//
// The canonical set is delivered once, so an object nobody could decide during
// the load does not come back on its own. Asking puts it on the live stream,
// where it is applied like any other delivery.
func (in *instance) postRequest(w http.ResponseWriter, r *http.Request) {
	typ, err := agmasyncType(r.FormValue("type"))
	if err != nil {
		redirect(w, r, "/decisions", "", err.Error())
		return
	}
	id, err := uuid.Parse(r.FormValue("agrirouter_id"))
	if err != nil {
		redirect(w, r, "/decisions", "", "not a uuid: "+err.Error())
		return
	}
	if err := in.request(r.Context(), typ, id); err != nil {
		redirect(w, r, "/decisions", "", err.Error())
		return
	}
	redirect(w, r, "/decisions", "asked for it again; watch the log", "")
}

func (in *instance) showLog(w http.ResponseWriter, r *http.Request) {
	in.render(w, "log.html", struct {
		page
		Entries []logEntry
	}{in.page("log"), in.log.recent()})
}

// streamEvents is the log as it happens.
//
// Server-sent events, the same shape agrirouter uses for the master-data
// stream — which is convenient rather than meaningful: this one carries
// narration, no position, and nothing resumes from it.
func (in *instance) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	entries, stop := in.log.watch()
	defer stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			payload, err := json.Marshal(map[string]string{
				"at": entry.At.Format("15:04:05"), "kind": entry.Kind, "text": entry.Text,
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (in *instance) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		// Too late for a status code: the template has already written some of
		// the page. Saying so in the log is all that is left.
		in.log.say("error", "rendering "+name+": "+err.Error())
	}
}

// redirect answers a form post with a fresh GET, so that a reload does not
// repeat whatever it did.
func redirect(w http.ResponseWriter, r *http.Request, path, notice, problem string) {
	query := ""
	switch {
	case problem != "":
		query = "?err=" + urlValue(problem)
	case notice != "":
		query = "?ok=" + urlValue(notice)
	}
	http.Redirect(w, r, path+query, http.StatusSeeOther)
}

func urlValue(s string) string { return url.QueryEscape(s) }

func agmasyncType(name string) (agmasync.EntityType, error) {
	typ := agmasync.EntityType(name)
	if !typ.Valid() {
		return "", fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, name)
	}
	return typ, nil
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

func first(entries []logEntry, n int) []logEntry {
	if len(entries) > n {
		return entries[:n]
	}
	return entries
}

// serve runs the screens until the context is cancelled.
func (in *instance) serve(ctx context.Context) error {
	server := &http.Server{
		Addr:    in.cfg.addr,
		Handler: in.handler(),
		// No write timeout: /events is a long-lived response, exactly as the
		// master-data stream is on the other side of this program.
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	in.log.say("serving", "screens on "+in.cfg.addr)
	if err := server.ListenAndServe(); err != nil && !isClosed(err) {
		return err
	}
	return nil
}

func isClosed(err error) bool { return err == http.ErrServerClosed }
