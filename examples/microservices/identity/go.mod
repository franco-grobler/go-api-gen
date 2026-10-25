module github.com/example/microservices/identity

go 1.27.1

require (
	github.com/example/microservices/schema v0.0.0
	github.com/franco-grobler/go-api-gen/runtime v0.0.0
)

replace github.com/example/microservices/schema => ../schema

replace github.com/franco-grobler/go-api-gen/runtime => ../../../runtime
