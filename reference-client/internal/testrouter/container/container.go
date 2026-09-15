// Package container starts the AgmaSync test router in Docker, for the
// integration tests.
//
// The in-process router is the same implementation and is what the unit tests
// and scenarios use; this exists so that at least one test exercises the client
// over a real network connection, where a long-lived SSE stream, a dropped
// connection, and HTTP buffering behave as they do in production rather than as
// an io.Pipe does.
package container

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const httpPort = "8080/tcp"

// Container is a running test router.
type Container struct {
	testcontainers.Container

	// BaseURL is what an agmasync.Client is pointed at.
	BaseURL string
}

// Run builds the image from the repository root and starts it.
//
// The build context is the repository root because the reference client depends
// on the agmasync module beside it through a replace directive, so a context
// rooted at reference-client/ would not contain it.
func Run(ctx context.Context) (*Container, error) {
	root, err := repositoryRoot()
	if err != nil {
		return nil, err
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    root,
			Dockerfile: "reference-client/internal/testrouter/Dockerfile",
			Repo:       "agmasync-test-router",
			Tag:        "latest",
		},
		ExposedPorts: []string{httpPort},
		// The control plane's observation list is the cheapest thing to ask
		// for, needs no setup, and answers 200 as soon as the router is
		// serving.
		WaitingFor: wait.ForHTTP("/_test/observations").
			WithPort(httpPort).
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting test router: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("test router host: %w", err)
	}
	port, err := c.MappedPort(ctx, httpPort)
	if err != nil {
		return nil, fmt.Errorf("test router port: %w", err)
	}

	return &Container{
		Container: c,
		BaseURL:   fmt.Sprintf("http://%s:%s", host, port.Port()),
	}, nil
}

// repositoryRoot locates the build context from this file's own path rather
// than from the working directory, so the image builds the same whichever
// package's tests ask for it — this one, four levels down, or the scenarios,
// two.
func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locating the repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..")), nil
}

// Terminate stops the container, for use in a test cleanup.
func (c *Container) Terminate(ctx context.Context) error {
	return c.Container.Terminate(ctx)
}
