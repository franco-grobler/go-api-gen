# Releasing

The generator and the runtime module are released together, under one
version. A release is a pair of tags on one commit, `vX.Y.Z` for the generator
and `runtime/vX.Y.Z` for the runtime, since `runtime/` is a module of its own
and Go finds its versions under tags prefixed with its directory. Go needs
nothing more: the module proxy serves a version straight from its tag.

## The version

[git-cliff](https://git-cliff.org) works the version out from the commits since
the last release, by the `[bump]` rules in `cliff.toml`:

| Since the last release | Before 1.0.0 | From 1.0.0 |
|---|---|---|
| a breaking change | minor | major |
| a `feat` | minor | minor |
| anything else | patch | patch |

The first release is `v0.1.0`. Leaving 0.x is a deliberate choice, made by
asking for `v1.0.0` explicitly.

Preview what a release would say, without making one:

```console
$ just release-notes          # the next entry, version worked out
$ just release-notes minor    # as a minor release
```

## Cutting a release

```console
$ just release                # version from the commits
$ just release minor          # or patch, major, or an exact v1.0.0-rc.1
```

This starts the **Prepare release** workflow on `main`, which:

1. runs CI on the commit;
2. has git-cliff work out the version and add its entry to `CHANGELOG.md`;
3. commits that as `chore(release): vX.Y.Z` and tags it with both tags;
4. pushes the commit and both tags in one atomic push, so if `main` moved
   meanwhile, nothing is half released.

The tags start the **Release** workflow, which checks they are a pair on one
commit on `main`, runs CI against it, and publishes the GitHub release with
that version's changelog entry as its notes.

The push is made with a `RELEASE_TOKEN` secret, because a push made with the
workflow's own token starts no workflows: the tags would land and nothing would
publish them. It needs to be a fine-grained token with read and write access to
the repository's contents, whose owner may push to `main` past any branch
protection.

## The changelog

`CHANGELOG.md` is written by git-cliff from the commit messages, never by
hand; to change how it reads, edit `cliff.toml`. `feat`, `fix`, `perf` and
`revert` commits are listed, grouped, with breaking changes first. The other
types are left out, as work that changes nothing for a user of the generator
or the runtime.
