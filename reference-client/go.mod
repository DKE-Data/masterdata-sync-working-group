module github.com/DKE-Data/masterdata-sync-working-group/reference-client

go 1.25.7

require (
	github.com/labstack/echo/v4 v4.15.1
	github.com/oapi-codegen/runtime v1.7.0
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

// The protocol layer is developed alongside the sample in this repository. A
// vendor importing it takes it by version instead.
replace github.com/DKE-Data/masterdata-sync-working-group/agmasync => ../agmasync
