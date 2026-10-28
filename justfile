# Development entry points. CI runs the same commands; see
# .github/workflows/ci.yml and its Forgejo copy in .forgejo/workflows. Every
# tool they use but the linter comes from devbox: `devbox shell`, or prefix a
# recipe with `devbox run --`.

go := env('GO', 'go')
gobin := justfile_directory() / '.bin'
lint := gobin / 'golangci-lint'

monorepo := 'examples/monorepo/go-api-gen.yaml'
petstore := 'examples/petstore/go-api-gen.yaml'
unions := 'examples/unions/go-api-gen.yaml'
micro := 'examples/microservices/go-api-gen.yaml'

# Every module under examples/, which is more than one per example: the
# microservices example is three, which is the point of it. One per line to
# read, flattened to a single word list so a shell `for` can
# take it: a triple-quoted string keeps its newlines.
example_modules := replace('''
examples/microservices/schema
examples/microservices/identity
examples/microservices/billing
examples/monorepo
examples/petstore
examples/unions
''', "\n", " ")

# Keep in step with GOLANGCI_VERSION in .github/workflows/ci.yml and
# .forgejo/workflows/ci.yml, so a local run and a CI run judge the same code
# the same way.
lint_version := 'v2.13.2'

# Build, lint, test, and verify generated output.
all: build lint test generated

# Compile the generator and the runtime module.
build:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ go }} build ./...
    (cd runtime && {{ go }} build ./...)

# Test every module, with the race detector and a shuffled order.
test:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ go }} test -race -shuffle=on ./...
    (cd runtime && {{ go }} test -race -shuffle=on ./...)
    for m in {{ example_modules }}; do
        echo "{{ go }} test ($m)"
        (cd "$m" && {{ go }} test -race -shuffle=on ./...)
    done

# Report coverage of the generator.
cover:
    {{ go }} test -covermode=atomic -coverprofile=coverage.out ./...
    {{ go }} tool cover -func=coverage.out | tail -1

# Install the pinned linter into .bin, unless it is already the right version.
[private]
lint-bin:
    #!/usr/bin/env bash
    set -euo pipefail
    want={{ trim_start_match(lint_version, 'v') }}
    # The linter refuses code that targets a newer Go than it was built with,
    # so it is built with the toolchain go.mod selects, and rebuilt when that
    # changes. `go install pkg@version` would otherwise pick a toolchain from
    # the linter's own go.mod.
    toolchain=$({{ go }} env GOVERSION)
    if [ -x "{{ lint }}" ] &&
        "{{ lint }}" version 2>/dev/null |
        grep -F "$want" | grep -qF "built with $toolchain "; then
        exit 0
    fi
    GOTOOLCHAIN="$toolchain" GOBIN="{{ gobin }}" {{ go }} install \
        github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ lint_version }}

# Lint the generator and the runtime module.
lint: lint-bin
    #!/usr/bin/env bash
    set -euo pipefail
    {{ lint }} run
    (cd runtime && {{ lint }} run --config=../.golangci.yml)

# Apply the linter's automatic fixes.
fix: lint-bin
    #!/usr/bin/env bash
    set -euo pipefail
    {{ lint }} run --fix
    (cd runtime && {{ lint }} run --fix --config=../.golangci.yml)

# Regenerate every example.
generate:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ go }} run ./cmd/go-api-gen build -c {{ monorepo }}
    {{ go }} run ./cmd/go-api-gen unify -c {{ monorepo }}
    {{ go }} run ./cmd/go-api-gen build -c {{ petstore }}
    {{ go }} run ./cmd/go-api-gen build -c {{ unions }}
    {{ go }} run ./cmd/go-api-gen build -c {{ micro }}
    {{ go }} run ./cmd/go-api-gen unify -c {{ micro }}

# Prove the committed output matches the specs and that it compiles.
generated: generate
    #!/usr/bin/env bash
    set -euo pipefail
    # git status rather than git diff: diff only inspects tracked files, so a
    # generated file that was never committed would otherwise slip through.
    changed=$(git status --porcelain -- examples)
    if [ -n "$changed" ]; then
        echo "$changed"
        echo "Generated output is stale or uncommitted; commit the result."
        exit 1
    fi
    for m in {{ example_modules }}; do
        echo "{{ go }} build and vet ($m)"
        (cd "$m" && {{ go }} build ./... && {{ go }} vet ./...)
    done

# Tidy every module against its own requirements, not the workspace's.
tidy:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ go }} mod tidy
    (cd runtime && {{ go }} mod tidy)
    for m in {{ example_modules }}; do
        (cd "$m" && GOWORK=off {{ go }} mod tidy)
    done

# Download the latest OpenAI spec into testdata, a large real-world document
# to try the generator against. It is fetched rather than vendored by hand so
# a refresh is one command and the diff shows what OpenAI changed.
update-openai-spec:
    #!/usr/bin/env bash
    set -euo pipefail
    url=https://raw.githubusercontent.com/openai/openai-openapi/manual_spec/openapi.yaml
    mkdir -p testdata/openai
    # Download beside the target and rename, so a failed fetch never leaves a
    # truncated spec behind.
    tmp=$(mktemp testdata/openai/openapi.yaml.XXXXXX)
    trap 'rm -f "$tmp"' EXIT
    curl -fsSL --retry 3 -o "$tmp" "$url"
    chmod 644 "$tmp"   # mktemp creates it owner-only
    mv "$tmp" testdata/openai/openapi.yaml
    echo "wrote testdata/openai/openapi.yaml ($(wc -l < testdata/openai/openapi.yaml | tr -d ' ') lines)"

# Remove the installed linter and the coverage profile.
clean:
    rm -rf {{ gobin }} coverage.out

# Install the commit-msg hook that runs committed and git-cliff.
hooks:
    pre-commit install

# Lint the commits on this branch that main does not have.
commits base="main":
    committed {{ base }}..HEAD
    git cliff --config cliff.toml {{ base }}..HEAD -o /dev/null

# Serve the documentation at localhost:3000, rebuilding on change.
docs:
    mdbook serve docs --open

# Build the documentation into docs/book.
docs-build:
    mdbook build docs

# Preview the CHANGELOG.md entry a release cut from HEAD would get.
release-notes BUMP="auto":
    git cliff --config cliff.toml --unreleased --bump {{ BUMP }}

# Cut a release: BUMP is auto, patch, minor, or major, or an exact version.
release BUMP="auto":
    #!/usr/bin/env bash
    set -euo pipefail
    # The tags are made by the Prepare release workflow, so a release always
    # comes from main as CI saw it and gets its changelog entry.
    case "{{ BUMP }}" in
        auto | patch | minor | major) input=(-f bump={{ BUMP }}) ;;
        *) input=(-f version={{ BUMP }}) ;;
    esac
    gh workflow run prepare-release.yml --ref main "${input[@]}"
    echo "Started. Follow it with: gh run watch"
