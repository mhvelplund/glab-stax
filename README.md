# glab stax

Stacked merge requests for GitLab, where each layer of the stack is a branch
that can hold **any number of commits**.

`glab stax` is a **derivative work of [gh-stack](https://github.com/github/gh-stack)**
by GitHub, Inc., released under the MIT License. It reproduces the command-line
interface of **gh-stack v0.2.1** but targets GitLab: branches and rebases are
handled with plain `git`, and merge requests are managed through
[`glab`](https://gitlab.com/gitlab-org/cli). It is not affiliated with or
endorsed by GitHub or GitLab.

Unlike the experimental built-in `glab stack`, which allows only one commit per
merge request, `glab stax` follows gh-stack's multi-commit model. It does not
use GitLab's stacked diffs UI.

## Status

Work in progress. Available today:

| Command | Description |
|---|---|
| `init [branches...]` | Start a stack (`-b/--base` sets the trunk) |
| `add [branch]` | Add a layer on top (`-m`, `-A`, `-u`) |
| `view` | Show the stack (`--short`, `--json`) |
| `up [n]`, `down [n]`, `top`, `bottom`, `trunk` | Navigate the stack (`--print-path`) |

Planned, following gh-stack v0.2.1: `rebase`, `sync`, `push`, `submit`, `link`,
`merge`, `checkout`, `switch`, `modify`, `unstack`, `alias`, `feedback`.

## Requirements

- Git 2.36 or later
- [`glab`](https://gitlab.com/gitlab-org/cli), authenticated with `glab auth login`

## Installation

`glab` has no extension mechanism like `gh extension`. Instead, install the
`glab-stax` binary and register a `glab` alias so that it runs as `glab stax`.

1. Install the binary (requires Go, see [Development](#development)):

   ```sh
   go install github.com/mhvelplund/glab-stax/cmd/glab-stax@latest
   ```

   Make sure `$(go env GOPATH)/bin` is on your `PATH`.

2. Register the alias:

   ```sh
   glab alias set stax 'glab-stax "$@"' --shell
   ```

3. Check that it works:

   ```sh
   glab stax --help
   ```

The name `stax` avoids a clash with the built-in `glab stack` command.
You can also run the binary directly as `glab-stax`.

## Usage

```sh
glab stax init auth-model          # start a stack on the default branch
git commit -am "Add user model"    # commit as much as you like on each layer
glab stax add -Am "Add login API"  # new layer named MM-DD-add_login_api, with a commit
glab stax view                     # show the stack
glab stax down                     # move toward the trunk
```

Stack metadata lives in `.git/glab-stax` (shared by all worktrees) and is never
committed.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Generic error |
| 2 | Not in a stack / stack not found |
| 3 | Rebase conflict |
| 4 | GitLab (`glab`) failure |
| 5 | Invalid arguments or flags |
| 6 | Disambiguation required |
| 7 | Rebase already in progress |
| 8 | Stack locked by another process |

## Development

All development tools (Go, golangci-lint, glab) are managed with
[mise](https://mise.jdx.dev) and pinned in `mise.toml`.

```sh
mise install       # install the toolchain
mise run build     # build bin/glab-stax
mise run test      # run the tests
mise run lint      # go vet + golangci-lint
mise run check     # all of the above
```

## License

MIT. See [LICENSE](LICENSE). Contains work derived from gh-stack,
Copyright GitHub, Inc.
