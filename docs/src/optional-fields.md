# Optional fields

Optional and nullable fields use `Optional[T]`, which distinguishes the three
states JSON actually has. A pointer can only express two of them, which is why
pointer-based models cannot implement PATCH correctly.

```go
type UpdateUser struct {
	Name  optional.Optional[string] `json:"name,omitzero"`
	Email optional.Optional[string] `json:"email,omitzero"`
}

req.Name.IsSet()    // was the key present at all?
req.Email.IsNull()  // present and explicitly null: clear the field
v, ok := req.Name.Get()
```
