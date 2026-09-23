package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// defaultPrimaryColor is the green the screens have always been.
const defaultPrimaryColor = "#1d3320"

// colorPattern is what may reach the stylesheet, since the value is written
// into one: a hex colour or a bare CSS colour name, and nothing that could
// close a declaration and start another.
var colorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3}|#[0-9a-fA-F]{6}|[a-zA-Z]+)$`)

// config is everything one instance needs to be a participant.
//
// The tenant is given rather than discovered, and it is the only agrirouter-side
// identifier that is: the endpoint is created by this process on startup and
// agrirouter answers with its id, so nothing has to be copied out of a screen or
// a log. See onboard.
type config struct {
	// instance names this participant to its user and, through externalID, to
	// agrirouter. Two instances of this program are two participants exactly
	// when their instance names and applications differ.
	instance string

	addr   string
	dbPath string

	baseURL           string
	tenantID          uuid.UUID
	applicationID     uuid.UUID
	softwareVersionID uuid.UUID

	// token authenticates as the application where agrirouter takes a static
	// bearer, which is what the test router does. Empty where oauth is set.
	token string

	// oauth is the real thing: client credentials, exchanged for an access token
	// that expires and renews. Nil where token is set.
	oauth *clientcredentials.Config

	// oauthClientID is kept beside it because the authorization step names the
	// client in a URL rather than presenting a token.
	oauthClientID string

	// authorizeURL is agrirouter's own front end, where a user authorizes this
	// application for their farming business. Empty where there is nothing to
	// authorize against — the test router grants by existing.
	authorizeURL string

	// publicURL is this participant as a browser reaches it, which is where
	// agrirouter sends the person back. It differs from addr wherever a port is
	// published under another number, which is every container.
	publicURL string

	// masterdata is what this participant declares it can exchange, at startup.
	// The screens can restate it afterwards; this is what an instance comes up
	// saying, so a restart does not silently widen what a person narrowed.
	masterdata []agmasync.EntityType

	// primaryColor is what the screens are coloured with, so that two
	// participants open in two tabs can be told apart at a glance.
	primaryColor string
}

// externalID is this participant's own name for its endpoint, and the only
// identifier both this program and a shell working alongside it can construct
// without being told.
//
// It is agrirouter-wide rather than per tenant, so it carries the tenant: two
// instances run by two people against one shared agrirouter would otherwise
// name one endpoint and take it from each other.
func (c config) externalID() string {
	return fmt.Sprintf("refclient:tenant:%s:%s", c.tenantID, c.instance)
}

func loadConfig() (config, error) {
	var c config
	var err error
	var tenant, application, softwareVersion string
	var oauthTokenURL, oauthClientID, oauthClientSecret, oauthScopes string
	var masterdata string

	flag.StringVar(&c.instance, "instance", env("REFCLIENT_INSTANCE", "alpha"),
		"what to call this participant (REFCLIENT_INSTANCE)")
	flag.StringVar(&c.addr, "addr", env("REFCLIENT_ADDR", ":8081"),
		"address to serve the screens on (REFCLIENT_ADDR)")
	flag.StringVar(&c.dbPath, "db", env("REFCLIENT_DB", ""),
		"path to this participant's database (REFCLIENT_DB); default <instance>.db")
	flag.StringVar(&c.baseURL, "url", env("AGMASYNC_URL", "http://localhost:8080"),
		"agrirouter base URL (AGMASYNC_URL)")
	flag.StringVar(&tenant, "tenant", env("AGMASYNC_TENANT_ID", ""),
		"the farming business this participant is onboarded into (AGMASYNC_TENANT_ID)")
	flag.StringVar(&application, "application", env("AGMASYNC_APPLICATION_ID", ""),
		"this participant's application id (AGMASYNC_APPLICATION_ID)")
	flag.StringVar(&softwareVersion, "software-version", env("AGMASYNC_SOFTWARE_VERSION_ID", ""),
		"the release of this software (AGMASYNC_SOFTWARE_VERSION_ID)")
	flag.StringVar(&c.token, "token", env("AGMASYNC_TOKEN", ""),
		"static bearer token, which is what the test router takes (AGMASYNC_TOKEN)")
	flag.StringVar(&oauthTokenURL, "oauth-token-url", env("AGMASYNC_OAUTH_TOKEN_URL", ""),
		"OAuth token endpoint, for a real agrirouter (AGMASYNC_OAUTH_TOKEN_URL)")
	flag.StringVar(&oauthClientID, "oauth-client-id", env("AGMASYNC_OAUTH_CLIENT_ID", ""),
		"OAuth client id (AGMASYNC_OAUTH_CLIENT_ID)")
	flag.StringVar(&oauthClientSecret, "oauth-client-secret", env("AGMASYNC_OAUTH_CLIENT_SECRET", ""),
		"OAuth client secret (AGMASYNC_OAUTH_CLIENT_SECRET)")
	flag.StringVar(&oauthScopes, "oauth-scopes", env("AGMASYNC_OAUTH_SCOPES", ""),
		"comma-separated scopes to request; empty asks for none (AGMASYNC_OAUTH_SCOPES)")
	flag.StringVar(&c.authorizeURL, "authorize-url", env("AGRIROUTER_APP_URL", ""),
		"agrirouter's front end, where a user authorizes this application (AGRIROUTER_APP_URL)")
	flag.StringVar(&c.publicURL, "public-url", env("REFCLIENT_PUBLIC_URL", ""),
		"this participant as a browser reaches it (REFCLIENT_PUBLIC_URL); default http://localhost<addr>")
	flag.StringVar(&masterdata, "masterdata", env("AGMASYNC_MASTERDATA", ""),
		"entity types to declare at startup, comma-separated; empty declares all (AGMASYNC_MASTERDATA)")
	flag.StringVar(&c.primaryColor, "primary-color", env("REFCLIENT_PRIMARY_COLOR", defaultPrimaryColor),
		"colour the screens are themed with, as #rgb, #rrggbb or a CSS colour name (REFCLIENT_PRIMARY_COLOR)")
	flag.Parse()

	if c.instance == "" {
		return c, errors.New("an instance name is required: pass -instance")
	}
	if !colorPattern.MatchString(c.primaryColor) {
		return c, fmt.Errorf("-primary-color is not a colour: %q", c.primaryColor)
	}
	if c.masterdata, err = entityTypeList(masterdata); err != nil {
		return c, err
	}
	if c.dbPath == "" {
		c.dbPath = c.instance + ".db"
	}

	// The tenant is required only where nothing can supply it. Authorizing
	// answers "which farming business" as its whole point, so an instance with
	// somewhere to be authorized may start without one and learn it there.
	switch {
	case tenant == "" && c.authorizeURL != "":
	default:
		if c.tenantID, err = required("tenant", tenant); err != nil {
			return c, err
		}
	}
	if c.applicationID, err = required("application", application); err != nil {
		return c, err
	}
	// The release of the participant's software. It is sent on onboarding and
	// must be the same on every write that follows, so an instance without one
	// derives a stable placeholder from its own identity rather than minting a
	// fresh one each time it starts.
	if softwareVersion == "" {
		c.softwareVersionID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("refclient:"+c.instance))
	} else if c.softwareVersionID, err = required("software-version", softwareVersion); err != nil {
		return c, err
	}

	switch {
	case oauthTokenURL != "":
		if oauthClientID == "" || oauthClientSecret == "" {
			return c, errors.New(
				"-oauth-token-url needs -oauth-client-id and -oauth-client-secret")
		}
		c.oauth = &clientcredentials.Config{
			ClientID:     oauthClientID,
			ClientSecret: oauthClientSecret,
			TokenURL:     oauthTokenURL,
			// Asked for only where a deployment wants them named. openapi.yaml
			// documents two — manage_endpoints to create and configure the
			// endpoint, masterdata for what it then exchanges — but the running
			// agrirouters grant by client registration and answer invalid_scope
			// to a request naming them. Empty is what works, and what the
			// monorepo's own system tests send.
			Scopes: scopeList(oauthScopes),
			// Stated rather than probed. Left unset, the library tries the
			// Authorization header, and on *any* error retries with the
			// credentials in the body and reports that second failure — so a
			// rejected client id comes back as whatever the token endpoint says
			// about a request with no Authorization header, which is a sentence
			// about the wrong request. agrirouter takes client_secret_basic.
			AuthStyle: oauth2.AuthStyleInHeader,
		}
		c.token = ""
		c.oauthClientID = oauthClientID
	case c.token == "":
		return c, errors.New(
			"credentials are required: pass -token for the test router, " +
				"or -oauth-token-url with -oauth-client-id and -oauth-client-secret")
	}
	return c, nil
}

// callbackURL is where agrirouter sends the person back to.
//
// The participant's own address and no path under it: the redirect has to match
// one registered for the OAuth client, and what is registered is the
// application's address rather than a route this sample invented. The tenant
// arrives as a query parameter, so the landing page is the one that reads it.
func (c config) callbackURL() string {
	base := c.publicURL
	if base == "" {
		base = "http://localhost" + c.addr
	}
	return strings.TrimSuffix(base, "/")
}

// httpClient is the client both the agmasync client and onboarding use.
//
// No timeout on it, ever: the live stream and the initial-load stream are
// long-lived responses, and a client timeout cuts them off mid-flight. Ordinary
// requests are bounded by their context instead.
func (c config) httpClient(ctx context.Context) *http.Client {
	if c.oauth == nil {
		return &http.Client{}
	}
	return c.oauth.Client(ctx)
}

// options authenticate an agmasync client as this application.
//
// The two ways are exclusive rather than layered: an OAuth client sets the
// header itself on every request, and a static token added on top would
// overwrite it with something a real agrirouter rejects.
func (c config) options(ctx context.Context) []agmasync.Option {
	opts := []agmasync.Option{agmasync.WithHTTPClient(c.httpClient(ctx))}
	if c.token != "" {
		opts = append(opts, agmasync.WithBearerToken(c.token))
	}
	return opts
}

// entityTypeList reads a comma-separated list of entity types, defaulting to
// every type this platform models — which is what a sample that has columns for
// all of them can honestly say.
func entityTypeList(list string) ([]agmasync.EntityType, error) {
	if strings.TrimSpace(list) == "" {
		return agmasync.EntityTypes, nil
	}
	var out []agmasync.EntityType
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		typ := agmasync.EntityType(name)
		if !typ.Valid() {
			return nil, fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, name)
		}
		out = append(out, typ)
	}
	return out, nil
}

// scopeList reads the scopes to request, if any. A scope named here is one the
// token endpoint must know: an unregistered name is refused outright rather
// than narrowed to what the client may have.
func scopeList(list string) []string {
	var out []string
	for _, scope := range strings.Split(list, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			out = append(out, scope)
		}
	}
	return out
}

func required(name, value string) (uuid.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return uuid.Nil, fmt.Errorf("-%s is required", name)
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("-%s is not a uuid: %w", name, err)
	}
	return id, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
