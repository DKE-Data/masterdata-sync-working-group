package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
	"golang.org/x/oauth2/clientcredentials"
)

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

	// decisionTimeout bounds how long a reconciliation waits for a person before
	// giving up on the object and leaving it for one. Without it a load parks a
	// database transaction for as long as nobody is looking.
	decisionTimeout time.Duration
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
	var tenant, application, softwareVersion string
	var oauthTokenURL, oauthClientID, oauthClientSecret string

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
	flag.DurationVar(&c.decisionTimeout, "decision-timeout",
		envDuration("REFCLIENT_DECISION_TIMEOUT", 2*time.Minute),
		"how long reconciliation waits for a person (REFCLIENT_DECISION_TIMEOUT)")
	flag.Parse()

	if c.instance == "" {
		return c, errors.New("an instance name is required: pass -instance")
	}
	if c.dbPath == "" {
		c.dbPath = c.instance + ".db"
	}

	var err error
	if c.tenantID, err = required("tenant", tenant); err != nil {
		return c, err
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
			// Both are needed: the endpoint is created and routed through one,
			// and everything the participant then exchanges through the other.
			Scopes: []string{"manage_endpoints", "masterdata"},
		}
		c.token = ""
	case c.token == "":
		return c, errors.New(
			"credentials are required: pass -token for the test router, " +
				"or -oauth-token-url with -oauth-client-id and -oauth-client-secret")
	}
	return c, nil
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

func envDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(env(key, ""))
	if err != nil {
		return fallback
	}
	return v
}
