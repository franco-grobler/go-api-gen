# Commands

```
go-api-gen build     Generate every configured output
go-api-gen check     Report whether committed output is up to date (for CI)
go-api-gen unify     Stitch every service spec into one unified document
```

The CLI is built on [Cobra](https://github.com/spf13/cobra), so
`go-api-gen completion <shell>` produces a shell completion script.

Generated output is deterministic, so `check` is a reliable drift gate.
