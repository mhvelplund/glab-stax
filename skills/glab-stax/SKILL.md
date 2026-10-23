---
name: glab-stax
description: Use when working with stacked GitLab merge requests via `glab stax` (glab-stax) — creating a stack of branches, restacking after changes, submitting MRs, syncing after merges, or merging a stack.
---

# glab stax

`glab stax` manages a stack of branches on top of a trunk (usually `main`). Each layer is a branch with any number of
commits and one GitLab merge request targeting the layer below it.

## Everyday flow

```sh
glab stax init first-layer            # start a stack on the default branch
git commit -am "..."                  # commit as usual on the current layer
glab stax add -Am "Next step"         # new layer on top, committing all changes to it
glab stax submit --auto               # push and create/update one draft MR per layer
glab stax sync                        # after trunk moves or an MR merges: restack, push, retarget
glab stax merge --yes --squash        # merge bottom-up, restacking between merges
```

## Rules for agents

- Always pass `--auto` to `submit` and `--yes` to `merge`; the interactive editors need a human at a terminal.
- `modify` and `switch` are interactive only. Use `up`, `down`, `top`, `bottom`, `checkout <branch>` to move.
- `add` must run from the top layer. Check with `glab stax view --json` (`current`, `branches[].isCurrent`).
- After editing a lower layer (`git commit --amend`, new commits), run `glab stax rebase` to restack the layers above,
  then `glab stax push` or `submit`.
- On a rebase conflict (exit code 3): resolve the files, `git add` them, run `glab stax rebase --continue`; or
  `glab stax rebase --abort` to restore every branch.
- Never edit the block between `<!-- glab-stax:begin` and `<!-- glab-stax:end -->` in MR descriptions; glab-stax
  rewrites it.

## Exit codes

0 success · 1 error · 2 not in a stack · 3 rebase conflict · 4 GitLab failure · 5 invalid arguments · 6 ambiguous
branch · 7 operation already in progress · 8 stack locked by another process.
