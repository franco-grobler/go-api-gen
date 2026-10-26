# Changelog

Every release of the generator and the runtime module, which share a version.
This file is written by git-cliff from the commit history; edit cliff.toml,
not the entries.

## [0.2.1](https://github.com/franco-grobler/go-api-gen/compare/v0.2.0...v0.2.1) - 2026-10-26

### Fixes

- **gen** encode unset required nullable properties as null ([d03c635](https://github.com/franco-grobler/go-api-gen/commit/d03c6358009b36bbf2568e709fd8699901e0dd7a))

## [0.2.0](https://github.com/franco-grobler/go-api-gen/compare/v0.1.0...v0.2.0) - 2026-10-26

### Features

- **example** a notification service built on unions ([51fb723](https://github.com/franco-grobler/go-api-gen/commit/51fb723b20000dc57e491bca9b9be65012ef92b4))
- **gen** generate oneOf and anyOf as union types ([890eba4](https://github.com/franco-grobler/go-api-gen/commit/890eba40498712fcd60470d2c168dc56a450391a))

## [0.1.0](https://github.com/franco-grobler/go-api-gen/releases/tag/v0.1.0) - 2026-10-25

### Features

- initial implementation ([0094957](https://github.com/franco-grobler/go-api-gen/commit/0094957a12686c9a0e421537fb5569cfc2ca9deb))
- **gen** document each handler with its summary and examples ([9236fe0](https://github.com/franco-grobler/go-api-gen/commit/9236fe09e797203a1905cdba0d67aa61afb94eca))
- **gen** enforce minProperties and maxProperties ([442a4d7](https://github.com/franco-grobler/go-api-gen/commit/442a4d7f1f7ce55db084a4e939ac647e9c146562))
- **gen** an Authenticator the compiler makes you supply ([3da3318](https://github.com/franco-grobler/go-api-gen/commit/3da3318440d47b4dd970f3420288843f8304651c))
- **spec** read securitySchemes and security ([86f8a87](https://github.com/franco-grobler/go-api-gen/commit/86f8a87043a2347a69ddf7dfd7a973b11a60ede3))
- **runtime** find a credential, and let a problem carry headers ([876ccf3](https://github.com/franco-grobler/go-api-gen/commit/876ccf399ca642acea314e88f62d9351381076d2))
- **example** make all three examples runnable programs ([39744a5](https://github.com/franco-grobler/go-api-gen/commit/39744a51c8b0414cf45746ba7e8e3dfdf624b9f9))
- **example** two services as separately built modules ([b79abe5](https://github.com/franco-grobler/go-api-gen/commit/b79abe554d49efe161dc159d7077edad22bae736))
- **example** add the Swagger Petstore ([1c9dd2d](https://github.com/franco-grobler/go-api-gen/commit/1c9dd2dee039da030e4b49417ab3c14b88631204))
- **example** add the hand-written half of a service ([5ecc8c2](https://github.com/franco-grobler/go-api-gen/commit/5ecc8c216128fa9ab435ac7aae042cd25da39ade))
- poc with claude ([4e7020b](https://github.com/franco-grobler/go-api-gen/commit/4e7020ba0c6558bc8ca974fc441009b2a40564e1))

### Fixes

- **gen** drop an integer bound its Go type cannot cross ([56f12ff](https://github.com/franco-grobler/go-api-gen/commit/56f12ff18dc9f6409e3a0c24f5d57f7da0f5565a))
- **gen** x-go-type replaces an object or array schema too ([3353eeb](https://github.com/franco-grobler/go-api-gen/commit/3353eeb286c6198b7d597613fbc331b666f21fc2))
- **spec** keep security schemes in bundled and unified documents ([18712f1](https://github.com/franco-grobler/go-api-gen/commit/18712f1a1743fa6895fc0ed20705f1a2e9abd709))
- **gen** parse array and object parameters as their style lays them out ([22660b1](https://github.com/franco-grobler/go-api-gen/commit/22660b151853e2e10f6fddb971d7fb49b8849326))
- **gen** a request struct yields its name to a schema that has it ([8e11de2](https://github.com/franco-grobler/go-api-gen/commit/8e11de2aa2452b48d2606ee5fa3b829bf05842c0))
- **gen** type an annotated $ref as the schema it references ([41260c3](https://github.com/franco-grobler/go-api-gen/commit/41260c3e697dd424f47f29e6d30412f0012ee5d2))
- **gen** validate the properties an inline allOf member declares ([724be3b](https://github.com/franco-grobler/go-api-gen/commit/724be3b2926b3af65cc867ac992070de2805842e))
- **gen** decode every field of a struct that embeds a decoder ([d6f57c7](https://github.com/franco-grobler/go-api-gen/commit/d6f57c73e5a6269c16dd5a84a391eb45195ece62))
- **gen** an optional property with only a custom rule compiles ([c4c2fa5](https://github.com/franco-grobler/go-api-gen/commit/c4c2fa55a6f546d882ff18294dd7100557d8fe44))
- **gen** parse an unnamed enum parameter into its own type ([3bfb15b](https://github.com/franco-grobler/go-api-gen/commit/3bfb15bac7034322ca34309bfab1b94d79f2ce7b))
- **gen** validate an aliased schema by value, not through a method ([29c0161](https://github.com/franco-grobler/go-api-gen/commit/29c0161047b49111cfde972ac4277b852f7e6df2))
- a field nobody sent is reported missing and nothing else ([8f6d769](https://github.com/franco-grobler/go-api-gen/commit/8f6d769cdb2210dc78fc74c2fb18151634891050))
- make the errors config reach generated code ([0cfb15c](https://github.com/franco-grobler/go-api-gen/commit/0cfb15c8b9341fba813eba554e338ca561fc01ad))

