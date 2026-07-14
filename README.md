# Masterdata Sync Working Group

This repository is the home for designs and discussions related to Masterdata Sync in agrirouter.

The goal of this group is to define a standard for the exchange of agricultural master data on the basis of the agrirouter platform.
The desired outcome is then a specification describing data formats, protocols and expected behaviors of the involved parties.
Implementation details might be mentioned in discussions, however any such mention (f.e code snippets with SQL statements, database schemas, etc) are not supposed to be binding and would always be included as an example only.

## Structure of this repository

- [./openapi.yaml](./openapi.yaml) - The OpenAPI specification for the Masterdata Sync API.
- [./specification.md](./specification.md) - The specification for the Masterdata Sync API, explaining the details and reasoning that are not covered by the OpenAPI specification.

Other:

- [./RFC.md](./RFC.md) - The RFC wrapper for the specification, used to generate the RFC page.
- [tools/](./tools/) - Tooling to automate routine tasks.

