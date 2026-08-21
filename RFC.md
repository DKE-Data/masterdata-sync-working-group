---

# This file is simply a wrapper for the actual spec in specification.md. We use this wrapper to configure RFC page generator.

coding: utf-8

title: Agriculture masterdata sync protocol
abbrev: AgmaSync
cat: exp
area: art

ipr: none
submissiontype: independent

wg: DKE-Data
kw:
  - agrirouter
  - agriculture
  - data exchange
  - API
  - REST

pi: [toc, sortrefs, symrefs, compact, comments]

author:
  -
    ins: T. Sultanaev
    name: Timur Sultanaev
    org: Concept Reply GmbH
    email: t.sultanaev@reply.de
#   TBU

--- abstract

This document describes the Masterdata Sync protocol, which focuses on synchronizing farms, fields, and clients' information between agricultural platforms.

{::boilerplate bcp14-tagged}

--- middle

{::include ./specification.md}

--- back

# OpenAPI definition of the Masterdata Sync API

Here we provide the OpenAPI definition (`openapi.yaml`) for
the Masterdata Sync API described in this document. It is informative; where it
differs from the normative behaviour specified in the body, the body prevails.

~~~ yaml
{::include ./openapi.yaml}
~~~
