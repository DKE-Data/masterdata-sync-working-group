# Build context is the repository root, not this directory: the reference
# client takes the agmasync module from beside it rather than from a proxy, so
# both have to be in the image's reach.
#
#   docker build -f reference-client/Dockerfile .
FROM golang:1.25 AS build

WORKDIR /src
COPY agmasync/go.mod agmasync/go.sum agmasync/
COPY reference-client/go.mod reference-client/go.sum reference-client/
RUN cd reference-client && go mod download

COPY agmasync/ agmasync/
COPY reference-client/ reference-client/

# CGO_ENABLED=0 needs no explaining away: the SQLite driver is the pure-Go one,
# so this is an ordinary static build rather than a compromise.
RUN cd reference-client && CGO_ENABLED=0 go build -o /out/refclient ./cmd/refclient
RUN cd reference-client && CGO_ENABLED=0 go build -o /out/testrouter ./cmd/testrouter
RUN cd reference-client && CGO_ENABLED=0 go build -o /out/agmactl ./cmd/agmactl

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/refclient /out/testrouter /out/agmactl /
VOLUME /data
ENTRYPOINT ["/refclient"]
