# Contributing

## Tools

Every tool the project uses is pinned in `devbox.json`, so a checkout builds
the same way on any machine: Go, [just](https://just.systems),
[git-cliff](https://git-cliff.org),
[committed](https://github.com/crate-ci/committed),
[mdBook](https://rust-lang.github.io/mdBook/), and
[pre-commit](https://pre-commit.com).

```console
$ devbox shell        # a shell with every tool on PATH
$ just hooks          # once: install the commit-msg hook
```

Or run one command without entering the shell: `devbox run -- just test`.

The linter is the exception. golangci-lint refuses to lint code that targets a
newer Go than it was built with, so `just lint` builds the pinned version from
source with the toolchain `go.mod` selects, into `.bin`, and rebuilds it when
either the pinned version or that toolchain changes. CI does the same.

## Recipes

Recipes live in a justfile; `just --list` prints them.

```console
$ just              # build, lint, test, and verify generated output
$ just test         # go test -race -shuffle=on, every module
$ just lint         # golangci-lint, the generator and the runtime
$ just generate     # regenerate every example
$ just tidy         # go mod tidy, every module, against its own requirements
$ just fix          # apply the linter's automatic fixes
$ just commits      # lint this branch's commit messages
$ just docs         # serve this book at localhost:3000
```

CI runs four jobs: build and test (`-race -shuffle=on`, with coverage), lint,
module tidiness, and a generated-code gate that regenerates the examples, fails
on any drift, and then compiles the result — because output that diffs clean
but does not build would pass every other check. The test suite also generates
a server from OpenAI's published specification and compiles it; `just
update-openai-spec` refreshes the copy in `testdata`.

## Linting

Linting is strict. Hand-written code is held to 80 columns; generated files are
excluded, since gofmt's struct-tag alignment has no reason to respect a limit
meant for human readers. Three exceptions are configured deliberately, each
with its reason in `.golangci.yml`: `govet`'s `shadow`, `staticcheck`'s ST1005
(several diagnostics here are deliberately two sentences — the problem, then
the fix), and `gosec`'s G304 (reading files the user names is what a generator
does).

The linter version is pinned in `justfile`, `.github/workflows/ci.yml` and
`.forgejo/workflows/ci.yml`; keep them in step.

## CI on two forges

The repository lives on GitHub and on a Forgejo instance, and runs the same
checks on both: `.github/workflows` for GitHub Actions and `.forgejo/workflows`
for Forgejo Actions, one file for one file, with the same jobs and the same
pinned tool versions. Change one, change its copy. The Forgejo jobs run in a
pinned container — the Go image `go.mod` selects, or a Node image where no Go
is needed — and install git-cliff, committed and mdBook from their release
tarballs, checked against pinned checksums.

Two things stay on GitHub. The book is published to GitHub Pages only; the
Forgejo docs workflow builds it and uploads it as an artifact. And only the
GitHub release workflow warms the Go module proxy, since the module path
points at GitHub.

## Commits

Every commit is a [conventional commit](https://www.conventionalcommits.org),
because the changelog is written from them:

```
fix(gen): a request struct yields its name to a schema that has it
```

Two tools check each message, in the commit-msg hook and again in CI:

- **committed** checks its style, per `committed.toml`: an allowed type, a
  subject in lower case and at most 72 characters, body lines of at most 100
  (wrap prose at 72; the limit is for quoted code and URLs),
  and no `fixup!` or WIP commits.
- **git-cliff** checks that the changelog can file it, per `cliff.toml`. It is
  stricter about a type's spelling, and a commit it cannot file would fail the
  release.

The allowed types are `feat`, `fix`, `perf` and `revert`, which reach the
changelog, and `build`, `chore`, `ci`, `docs`, `refactor`, `style` and `test`,
which do not. A `!` after the type, or a `BREAKING CHANGE:` footer, marks a
breaking change. The two configuration files list the types separately; keep
them in step.

A pull request is merged with a merge commit or rebased, never squashed, so
each of its commits reaches `main` and the changelog. A merge commit is titled
`<pull request title> (#<number>)`, so CI holds the title, with that suffix, to
the same rules; the changelog leaves merge commits out, since the commits they
merge are listed already.

## Layout

```
cmd/go-api-gen/          the CLI
internal/                config, spec loading, ownership, emitters
runtime/                 a separate module: what your services import
examples/petstore/       one service, no shared root
examples/unions/         one service whose contract is oneOf unions
examples/monorepo/       two services in one module, sharing a schema root
examples/microservices/  the same two, as three separately built modules
docs/                    this book
testdata/openai/         OpenAI's specification, a real-world test case
```

Each example carries the specs, the generated code, and the handlers you would
write, and all four are built and tested in CI.
