# Masterdata Sync Working Group

This repository is the home for designs and discussions related to Masterdata Sync in agrirouter.

## Goal

The goal of this group is to define a standard for the exchange of agricultural master data on the basis of the agrirouter platform.
The desired outcome is then a specification describing data formats, protocols and expected behaviors of the involved parties.
Implementation details might be mentioned in discussions, however any such mention (f.e code snippets with SQL statements, database schemas, etc) are not supposed to be binding and would always be included as an example only.

## Usage

Use [issues](https://github.com/DKE-Data/masterdata-sync-working-group/issues) if you want to discuss a topic, ask a question, check other already discussed issues or propose a change that you cannot make as PR.
Use [pull requests](https://github.com/DKE-Data/masterdata-sync-working-group/pulls) if you want to propose a specific change.

## Structure of this repository

- [./openapi.yaml](./openapi.yaml) - The OpenAPI specification for the Masterdata Sync API.
- [./specification.md](./specification.md) - The specification for the Masterdata Sync API, explaining the details and reasoning that are not covered by the OpenAPI specification.

Other:

- [./RFC.md](./RFC.md) - The RFC wrapper for the specification, used to generate the RFC page.
- [tools/](./tools/) - Tooling to automate routine tasks.

