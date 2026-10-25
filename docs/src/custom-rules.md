# Custom rules

`x-validate` on a schema or property declares a rule you write in Go. The
generator emits an interface and asserts the type satisfies it, so a rule named
in the spec but never written is a compile error:

```yaml
email:
  type: string
  x-validate: [corporate_domain]
```

```go
// generated
type UserValidator interface {
	ValidateEmailCorporateDomain(v *validate.V)
}
var _ UserValidator = User{}

// yours, in a file the generator does not own
func (u User) ValidateEmailCorporateDomain(v *validate.V) {
	if !strings.HasSuffix(u.Email, "@acme.com") {
		v.Fail("corporate_domain", "Email must be on the acme.com domain")
	}
}
```

Rules are pure functions of the value. A check that needs a database belongs in
the handler, using the same error builder so the response is identical:

```go
if taken, _ := db.EmailTaken(ctx, req.Body.Email); taken {
	return nil, apierr.Validation().
		Field("body", "email").
		Fail("email_taken", "already registered")
}
```
