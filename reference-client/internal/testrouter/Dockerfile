# Build context is the repository root, because the reference client depends on
# the agmasync module beside it through a replace directive:
#
#   docker build -t agmasync-test-router -f reference-client/internal/testrouter/Dockerfile .
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY agmasync/go.mod agmasync/go.sum ./agmasync/
COPY reference-client/go.mod reference-client/go.sum ./reference-client/
RUN cd reference-client && go mod download

COPY agmasync ./agmasync
COPY reference-client ./reference-client

RUN cd reference-client && CGO_ENABLED=0 GOOS=linux go build -o /test-router ./cmd/testrouter

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=nonroot:nonroot /test-router /test-router

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/test-router"]
