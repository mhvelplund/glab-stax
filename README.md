# glab stax

<img src=".img/slop-small.jpg" alt="For robots, by robots. Vibed without love or skill." height="24" />

Stacked merge requests for GitLab, where each layer of the stack is a branch that can hold **any number of commits**.

`glab stax` is a **derivative work of [gh-stack](https://github.com/github/gh-stack)** by GitHub, Inc., released under
the MIT License. It reproduces the command-line interface of **gh-stack v0.2.1** but targets GitLab: branches and
rebases are handled with plain `git`, and merge requests are managed through
[`glab`](https://gitlab.com/gitlab-org/cli). It is not affiliated with or endorsed by GitHub or GitLab.

Unlike the experimental built-in `glab stack`, which allows only one commit per merge request, `glab stax` follows
gh-stack's multi-commit model. It does not use GitLab's stacked diffs UI.

## Status

Work in progress. Available today:

| Command                                        | Description                                                                                                                                                                                        |
|------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `init [branches...]`                           | Start a stack (`-b/--base` sets the trunk)                                                                                                                                                         |
| `add [branch]`                                 | Add a layer on top (`-m`, `-A`, `-u`)                                                                                                                                                              |
| `view`                                         | Show the stack (`--short`, `--json`)                                                                                                                                                               |
| `up [n]`, `down [n]`, `top`, `bottom`, `trunk` | Navigate the stack (`--print-path`)                                                                                                                                                                |
| `push`                                         | Push all unmerged branches with `--force-with-lease` (`--remote`)                                                                                                                                  |
| `submit`                                       | Push and create/update one MR per branch, each targeting the branch below; in a terminal, edit titles, descriptions and draft state in a full-screen editor first (`--auto`, `--open`, `--remote`) |
| `sync`                                         | Fetch, reconcile with the stack on GitLab, rebase if the trunk moved, push, retarget MRs (`--prune`, `--remote`)                                                                                   |
| `merge [<mr>]`                                 | Merge MRs bottom-up to the chosen one, restacking and retargeting between merges (`--squash`, `--merge`, `--rebase`, `--merge-method`, `--yes`, `--timeout`)                                       |
| `switch`                                       | Pick a branch of the current stack in a full-screen picker (`--print-path`)                                                                                                                        |
| `checkout [<mr> \| <url> \| <branch>]`         | Check out a stack, setting it up from GitLab if needed; without arguments, pick from local and GitLab stacks (`--print-path`)                                                                      |
| `modify`                                       | Restructure the stack in a full-screen editor: drop, fold, insert, reorder, rename, undo (`--continue`, `--abort`)                                                                                 |
| `link <branch-or-mr>...`                       | Create or extend a stack on GitLab without local tracking (`--base`, `--open`, `--remote`)                                                                                                         |
| `unstack [<mr>]`                               | Remove a stack locally and its block on GitLab (`--local`; alias `delete`)                                                                                                                         |
| `alias [name]`                                 | Register `glab stax` and install a `gs` wrapper in `~/.local/bin` (`--remove`)                                                                                                                     |
| `feedback [title]`                             | Open an issue for glab-stax in the browser                                                                                                                                                         |
| `rebase [branch]`                              | Cascade-rebase the stack onto the trunk (`--downstack`, `--upstack`, `--no-trunk`, `--continue`, `--abort`, `--remote`, `--preserve-dates`)                                                        |

This covers every command of gh-stack v0.2.1.

## Requirements

- Git 2.36 or later
- [`glab`](https://gitlab.com/gitlab-org/cli), authenticated with `glab auth login`

## Installation

`glab` has no extension mechanism like `gh extension`. Instead, install the `glab-stax` binary and register a `glab`
alias so that it runs as `glab stax`.

1. Install the binary (requires Go, see [Development](#development)):

   ```sh
   go install github.com/mhvelplund/glab-stax/cmd/glab-stax@latest
   ```

   Make sure `$(go env GOPATH)/bin` is on your `PATH`.

2. Register the `glab stax` alias and a `gs` shortcut in `~/.local/bin`:

   ```sh
   glab-stax alias
   ```

   This runs `glab alias set stax 'glab-stax "$@"' --shell` for you.

3. Check that it works:

   ```sh
   glab stax --help
   ```

The name `stax` avoids a clash with the built-in `glab stack` command. You can also run the binary directly as
`glab-stax`.

## Usage

```sh
glab stax init auth-model          # start a stack on the default branch
git commit -am "Add user model"    # commit as much as you like on each layer
glab stax add -Am "Add login API"  # new layer named MM-DD-add_login_api, with a commit
glab stax view                     # show the stack
glab stax down                     # move toward the trunk
git commit --amend                 # rewrite a lower layer...
glab stax rebase                   # ...and restack everything above it
glab stax submit                   # push and open one MR per layer
glab stax sync --prune             # after the bottom MR merges: restack, retarget, clean up
```

`glab stax submit --auto` creates draft MRs titled from the commits; without `--auto` a full-screen editor lets you pick
which branches get MRs and edit each title, description and draft state. Every MR gets a block at the end of its
description that lists the whole stack, so reviewers can move between layers. glab-stax keeps that block up to date;
text outside it is never touched.

If a rebase hits a conflict, resolve it, `git add` the files and run `glab stax rebase --continue`, or undo the whole
operation with `glab stax rebase --abort`.

Stack metadata lives in `.git/glab-stax` (shared by all worktrees) and is never committed.

### Differences from gh-stack

- GitLab has no stack object, so the stack is recorded in a managed block at the end of each MR description.
- Renaming a layer in `modify` cannot rename its MR's source branch on GitLab, so the next `submit` opens a new MR;
  close the old one.
- `merge` cannot be atomic on GitLab: MRs are merged one at a time, and the command stops at the first failure, leaving
  earlier MRs merged.

### Environment variables

| Variable                       | Effect                                                         |
|--------------------------------|----------------------------------------------------------------|
| `GLAB_STAX_NO_UPDATE_NOTIFIER` | Any non-empty value disables the daily check for new releases. |
| `GLAB_STAX_HYPERLINKS`         | `1` or `0` forces clickable MR links in `view` on or off.      |
| `GIT_PAGER`, `PAGER`           | Pager for `view` (default `less -R`).                          |
| `VISUAL`, `EDITOR`             | Editor for MR descriptions in the `submit` editor (`^e`).      |

### Agent skill

[`skills/glab-stax/SKILL.md`](skills/glab-stax/SKILL.md) teaches coding agents how to drive `glab stax`
non-interactively.

### Exit codes

| Code | Meaning                          |
|------|----------------------------------|
| 0    | Success                          |
| 1    | Generic error                    |
| 2    | Not in a stack / stack not found |
| 3    | Rebase conflict                  |
| 4    | GitLab (`glab`) failure          |
| 5    | Invalid arguments or flags       |
| 6    | Disambiguation required          |
| 7    | Operation already in progress    |
| 8    | Stack locked by another process  |

## Development

All development tools (Go, golangci-lint, glab) are managed with [mise](https://mise.jdx.dev) and pinned in `mise.toml`.

```sh
mise install       # install the toolchain
mise run build     # build bin/glab-stax
mise run test      # run the tests
mise run lint      # go vet + golangci-lint
mise run check     # all of the above
```

## License

MIT. See [LICENSE](LICENSE). Contains work derived from gh-stack, Copyright GitHub, Inc.
