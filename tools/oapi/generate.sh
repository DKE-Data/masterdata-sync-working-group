#!/usr/bin/env bash
#
# Regenerates the Go bindings for openapi.yaml.
#
# Run from the repository root:
#
#   tools/oapi/generate.sh
#
# The generator version is pinned here rather than tracked as a module
# dependency, so regenerating needs nothing installed but Go itself. The
# generated files are committed: a change to openapi.yaml then shows the
# resulting Go surface in the same diff, and CI fails if the two drift apart.
#
# openapi.yaml is generated from as-is, with no preprocessing. That is worth
# keeping true: a partner should be able to point their generator straight at
# the published specification.

set -euo pipefail

OAPI_CODEGEN="github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0"

cd "$(dirname "$0")/../.."

go run "$OAPI_CODEGEN" --config agmasync/oapi/oapi_codegen_models.yaml openapi.yaml
go run "$OAPI_CODEGEN" --config agmasync/oapi/oapi_codegen_client.yaml openapi.yaml
go run "$OAPI_CODEGEN" --config reference-client/internal/testrouter/oapi/oapi_codegen_server.yaml openapi.yaml
