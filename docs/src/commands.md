# Commands

```
go-api-gen build     Generate every configured output
go-api-gen check     Report whether committed output is up to date (for CI)
go-api-gen unify     Stitch every service spec into one unified document
```

The CLI is built on [Cobra](https://github.com/spf13/cobra), so
`go-api-gen completion <shell>` produces a shell completion script.

`build` writes everything the config describes: each service's code and
document, each shared root's package, and, when `unified.to` is set, the
[unified spec](unified-spec.md). A file whose content is unchanged is left
alone.

Generated output is deterministic, so `check` is a reliable drift gate. It
compares every file `build` would write, the unified spec included, and exits
non-zero naming each one that is missing or differs.

`unify` writes the unified spec on its own: to `unified.to`, as `build` does,
to another path with `--output`, or to standard output with `--stdout`.
