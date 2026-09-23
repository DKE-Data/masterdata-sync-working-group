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
	"sort"
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
	mux.HandleFunc("POST /declare", in.postDeclare)
	mux.HandleFunc("GET /objects", in.showObjects)
	mux.HandleFunc("GET /objects/{type}/{localId}", in.showObject)
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

	declared := in.declaredTypes()
	capabilities := make([]capability, 0, len(agmasync.EntityTypes))
	for _, typ := range agmasync.EntityTypes {
		capabilities = append(capabilities, capability{
			Type: typ, Declared: slices.Contains(declared, typ),
		})
	}

	in.render(w, "dashboard.html", struct {
		page
		// Declared is what is configured now, and Capabilities every type with
		// a tick beside it — the reading and the editing of one fact.
		Declared     []agmasync.EntityType
		Capabilities []capability
		Editing      bool
		Entries      []logEntry
	}{
		p, declared, capabilities,
		r.URL.Query().Get("edit") == "capabilities",
		first(in.log.recent(), 12),
	})
}

// capability is one entity type on the declaration form: what this
// participant's software can exchange, and whether it currently says so.
type capability struct {
	Type     agmasync.EntityType
	Declared bool
}

// postDeclare restates the declaration from what was ticked.
//
// Nothing ticked is a declaration of nothing, which is a thing a participant
// may say — and the reason this does not treat the empty form as a mistake.
func (in *instance) postDeclare(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirect(w, r, "/", "", err.Error())
		return
	}

	var types []agmasync.EntityType
	for _, name := range r.PostForm["type"] {
		typ, err := agmasyncType(name)
		if err != nil {
			redirect(w, r, "/", "", err.Error())
			return
		}
		types = append(types, typ)
	}

	if err := in.declare(r.Context(), types); err != nil {
		redirect(w, r, "/", "", err.Error())
		return
	}

	names := make([]string, 0, len(types))
	for _, typ := range agmasync.DependencyClosure(types) {
		names = append(names, string(typ))
	}
	redirect(w, r, "/", "declared: "+orNone(strings.Join(names, ", ")), "")
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
	redirect(w, r, "/", "running the initial load", "")
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
	redirect(w, r, "/", "reconnecting from the beginning", "")
}

// objectsView is the platform's own records beside what agrirouter calls them —
// the two tables the schema draws a line between, shown side by side because
// the line is the thing worth seeing.
type objectsView struct {
	page

	// Types are every type a record may be created as, which is not the same as
	// the types that can be sent. Creating is the platform's own act; sending is
	// what the user routed. NewType is the one the form is currently showing.
	Types   []agmasync.EntityType
	NewType agmasync.EntityType

	// NewRouted says a record of NewType leaves as soon as it is created. It
	// changes what the button promises, so the screen says which it will be
	// before the click rather than after.
	NewRouted bool

	Fields    []formField
	SampleGeo string
	Groups    []objectGroup
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
	Type         agmasync.EntityType
	Name         string
	LocalID      string
	AgrirouterID string
	Revision     string
	Archived     bool
	Bound        bool
	Unbound      bool
	Attributes   []attribute

	// Deletable says the record can be dropped here, and Unbinds that dropping
	// it declares to agrirouter that this platform no longer holds the object.
	// Both follow from the record and the routing together; see
	// [instance.deleteRecord], which decides the same thing again on the post.
	Deletable bool
	Unbinds   bool
}

// deletability works out which of [instance.deleteRecord]'s three cases a
// record is in, so the screen offers the button exactly where the post will
// accept it.
func (row *objectRow) deletability(routed bool) {
	switch {
	case !row.Bound:
		// Never sent, or already unbound: nothing to tell anyone about.
		row.Deletable, row.Unbinds = true, false
	case routed:
		// Deactivation is available and is the operation that fits.
		row.Deletable, row.Unbinds = false, false
	default:
		row.Deletable, row.Unbinds = true, true
	}
}

// defaultNewType picks the type the create form opens on: the first routed one
// in dependency order, so what is typed first is what everything else refers
// to, and an organization where nothing is routed at all.
func defaultNewType(routed []agmasync.EntityType) agmasync.EntityType {
	for _, typ := range agmasync.DependencyOrder {
		if slices.Contains(routed, typ) {
			return typ
		}
	}
	return agmasync.TypeOrganization
}

func (in *instance) showObjects(w http.ResponseWriter, r *http.Request) {
	p := in.page("objects")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")

	// Every type can be created. Which of them leaves the platform is the
	// routing, and the form says so rather than hiding the types it cannot send:
	// a farm management system whose user cannot enter a farm because of what
	// agrirouter was told is not a farm management system.
	newType := agmasync.EntityType(r.URL.Query().Get("new"))
	if !newType.Valid() {
		newType = defaultNewType(p.Routed)
	}

	view := objectsView{
		page:      p,
		Types:     agmasync.EntityTypes,
		NewType:   newType,
		NewRouted: slices.Contains(p.Routed, newType),
		SampleGeo: sampleGeometry,
	}
	err := in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		var err error
		if view.Fields, err = fillChoices(tx, formFields(newType)); err != nil {
			return err
		}
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
				row.deletability(group.Routed)
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
	out := objectRow{Type: typ, LocalID: localID, AgrirouterID: "—", Revision: "—"}

	record, err := tx.LoadRecord(typ, localID)
	if err != nil {
		return out, err
	}
	out.Archived = record.Archived
	out.Attributes = attributesOfRecord(record)
	out.Name = nameOfRecord(record)

	switch row, err := tx.SyncRow(typ, localID); {
	case err == nil:
		out.Unbound = row.Unbound
		out.Bound = row.Bound()
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

	if err := r.ParseForm(); err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	attributes, err := attributesFromForm(typ, r.PostForm)
	if err != nil {
		// Back to the form for this type, so what was rejected is in front of
		// the person who typed it.
		redirect(w, r, "/objects?new="+string(typ), "", err.Error())
		return
	}

	localID, sent, err := in.create(r.Context(), typ, attributes)
	if err != nil {
		if localID == "" {
			redirect(w, r, "/objects?new="+string(typ), "", err.Error())
			return
		}
		// The record was written even where the send failed, which is why this
		// says which record rather than only what went wrong.
		redirect(w, r, "/objects", "",
			fmt.Sprintf("%s %s was stored but not sent: %v", typ, localID, err))
		return
	}

	what := fmt.Sprintf("%s %s created and sent", typ, localID)
	if !sent {
		what = fmt.Sprintf("%s %s created; %s is not routed", typ, localID, typ)
	}
	redirect(w, r, objectPath(typ, localID), what, "")
}

// objectPath is where one record lives on these screens.
func objectPath(typ agmasync.EntityType, localID string) string {
	return "/objects/" + url.PathEscape(string(typ)) + "/" + url.PathEscape(localID)
}

// objectView is one record in full: what the platform holds, what agrirouter
// calls it, and what it points at.
type objectView struct {
	page
	Row    objectRow
	Routed bool

	// Modelled is the record's attributes in the order the create form asks for
	// them, labelled the way it labels them. The same fields, read back.
	Modelled []detailAttr

	// Unmodelled is what arrived that this platform has no column for, kept and
	// relayed unchanged. It is shown apart because that distinction is the whole
	// point of the split — and on a record created here it is always empty.
	Unmodelled []attribute
}

// detailAttr is one attribute as the detail screen shows it.
type detailAttr struct {
	Label string
	Value string

	// Link is set where the attribute is a reference, so the target is one
	// click away rather than an identifier to go and find.
	Link string

	// Pretty is set for geometry, which is shown indented over several lines
	// instead of squeezed onto one.
	Pretty string
}

func (in *instance) showObject(w http.ResponseWriter, r *http.Request) {
	typ, err := agmasyncType(r.PathValue("type"))
	if err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	localID := r.PathValue("localId")

	p := in.page("objects")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")
	view := objectView{page: p, Routed: slices.Contains(p.Routed, typ)}

	err = in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		row, err := objectRowOf(tx, typ, localID)
		if err != nil {
			return err
		}
		row.deletability(view.Routed)
		view.Row = row

		record, err := tx.LoadRecord(typ, localID)
		if err != nil {
			return err
		}
		view.Modelled = detailAttrs(tx, record)
		view.Unmodelled = unmodelledAttrs(record)
		return nil
	})
	switch {
	case isNotFound(err):
		redirect(w, r, "/objects", "",
			fmt.Sprintf("no %s called %q here", typ, localID))
		return
	case err != nil:
		view.Problem = err.Error()
	}
	in.render(w, "object.html", view)
}

// detailAttrs reads a record back through the same field list the create form
// is built from, so a person sees what they typed under the label they typed it
// under. An attribute the form does not know about cannot occur here: both come
// from [formFields].
func detailAttrs(tx *store.Tx, record store.Record) []detailAttr {
	var out []detailAttr
	for _, field := range formFields(record.EntityType) {
		raw, ok := valueAt(record.Modelled, field.Name)
		if !ok {
			continue
		}

		attr := detailAttr{Label: field.Label, Value: compact(raw)}
		switch field.Kind {
		case "ref":
			targetType, targetID := refTarget(raw)
			if targetID == "" {
				break
			}
			attr.Value = targetID
			if targetType.Valid() {
				attr.Link = objectPath(targetType, targetID)
				if name, err := displayName(tx, targetType, targetID); err == nil && name != "" {
					attr.Value = fmt.Sprintf("%s — %s", name, targetID)
				}
			}
		case "geometry":
			// Summarised, then shown. Indenting a polygon puts every coordinate
			// on a line of its own, which is a lot of screen for the one thing
			// nobody reads off a page — while the shape and the size of it are
			// what a person actually wants to know at a glance.
			attr.Value = geometrySummary(raw)
			attr.Pretty = string(raw)
		}
		out = append(out, attr)
	}
	return out
}

// valueAt reads an attribute by the same dotted path the form writes it under.
func valueAt(modelled map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	parent, leaf, nested := strings.Cut(path, ".")
	raw, ok := modelled[parent]
	if !ok || !nested {
		return raw, ok
	}
	var child map[string]json.RawMessage
	if err := json.Unmarshal(raw, &child); err != nil {
		return nil, false
	}
	value, ok := child[leaf]
	return value, ok
}

// geometrySummary says what shape a boundary is and how big the geometry is,
// which is what can usefully be read off a screen. The coordinates themselves
// are shown underneath, unaltered, because this is a sample and what went over
// the wire is the point of it.
func geometrySummary(raw json.RawMessage) string {
	var geometry struct {
		Type        string `json:"type"`
		Coordinates any    `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &geometry); err != nil {
		return ""
	}
	name := geometry.Type
	if name == "" {
		name = "geometry"
	}
	points := countPoints(geometry.Coordinates)
	if points == 0 {
		return name
	}
	return fmt.Sprintf("%s — %d points", name, points)
}

// countPoints walks a GeoJSON coordinate array, whose nesting depth is the
// geometry's own: a position is a pair of numbers however deep it sits.
func countPoints(coordinates any) int {
	list, ok := coordinates.([]any)
	if !ok {
		return 0
	}
	if len(list) > 0 {
		if _, isNumber := list[0].(float64); isNumber {
			return 1
		}
	}
	total := 0
	for _, item := range list {
		total += countPoints(item)
	}
	return total
}

func refTarget(raw json.RawMessage) (agmasync.EntityType, string) {
	var ref struct {
		Type    string `json:"type"`
		LocalID string `json:"local_id"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", ""
	}
	return agmasync.EntityType(ref.Type), ref.LocalID
}

func unmodelledAttrs(record store.Record) []attribute {
	var out []attribute
	names := make([]string, 0, len(record.Unmodelled))
	for name := range record.Unmodelled {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, attribute{Name: name, Value: compact(record.Unmodelled[name])})
	}
	return out
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

	if err := in.deleteRecord(r.Context(), typ, localID); err != nil {
		redirect(w, r, "/objects", "", err.Error())
		return
	}
	redirect(w, r, "/objects",
		fmt.Sprintf("%s %s deleted", typ, localID), "")
}

type decisionsView struct {
	page
	// Questions rather than Pending: the embedded page carries a Pending count
	// for the navigation, and a field of the same name here would shadow it and
	// print a slice where the count belongs.
	Questions []*decision
	Answered  []answered
}

func (in *instance) showDecisions(w http.ResponseWriter, r *http.Request) {
	p := in.page("decisions")
	p.Notice, p.Problem = r.URL.Query().Get("ok"), r.URL.Query().Get("err")
	in.render(w, "decisions.html", decisionsView{
		page:      p,
		Questions: in.inbox.Pending(),
		Answered:  in.inbox.Answered(),
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
// redirect sends the browser on with the outcome of what it just did.
//
// The path may already carry a query — the create form comes back to the type
// it was filled in for — so the separator is whichever of ? and & the path has
// not used yet.
func redirect(w http.ResponseWriter, r *http.Request, path, notice, problem string) {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	query := ""
	switch {
	case problem != "":
		query = separator + "err=" + urlValue(problem)
	case notice != "":
		query = separator + "ok=" + urlValue(notice)
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
