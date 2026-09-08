// The contract, as a module. Both services require it; neither owns it.
module github.com/example/microservices/schema

go 1.24

require github.com/franco-grobler/go-api-gen/runtime v0.0.0

replace github.com/franco-grobler/go-api-gen/runtime => ../../../runtime
