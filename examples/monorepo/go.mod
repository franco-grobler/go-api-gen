module github.com/acme/monorepo

go 1.24

require (
	github.com/franco-grobler/go-api-gen/runtime v0.0.0
	github.com/google/uuid v1.6.0
)

replace github.com/franco-grobler/go-api-gen/runtime => ../../runtime
