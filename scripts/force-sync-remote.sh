#!/usr/bin/env bash
set -Eeuo pipefail

remote=origin
branch=main
assume_yes=false

usage() {
  cat <<'EOF'
Usage: ./scripts/force-sync-remote.sh [--yes] [--remote NAME] [--branch NAME]

Fetches the selected remote branch and makes the current checkout exactly match
that remote commit. All tracked local changes, staged changes, and unpushed
commits on the selected branch are permanently discarded.

Untracked and ignored files are preserved.

Options:
  --yes          Skip the destructive-action confirmation.
  --remote NAME  Git remote to use (default: origin).
  --branch NAME  Remote branch to use (default: main).
  -h, --help     Show this help.
EOF
}

while (($# > 0)); do
  case "$1" in
    --yes)
      assume_yes=true
      shift
      ;;
    --remote)
      [[ $# -ge 2 ]] || { echo "ERROR: --remote requires a value" >&2; exit 2; }
      remote=$2
      shift 2
      ;;
    --branch)
      [[ $# -ge 2 ]] || { echo "ERROR: --branch requires a value" >&2; exit 2; }
      branch=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "ERROR: run this script inside a Git repository" >&2
  exit 1
}
cd -- "$repo_root"

git remote get-url "$remote" >/dev/null 2>&1 || {
  echo "ERROR: Git remote does not exist: $remote" >&2
  exit 1
}
git check-ref-format --branch "$branch" >/dev/null 2>&1 || {
  echo "ERROR: invalid Git branch name: $branch" >&2
  exit 1
}

current_branch=$(git branch --show-current)
if [[ -z "$current_branch" ]]; then
  echo "ERROR: detached HEAD is not supported; check out a branch first" >&2
  exit 1
fi
if [[ "$current_branch" != "$branch" ]]; then
  echo "ERROR: current branch is '$current_branch', not requested branch '$branch'" >&2
  echo "Check out '$branch' first or pass --branch '$current_branch'." >&2
  exit 1
fi

echo "Repository: $repo_root"
echo "Remote target: $remote/$branch"
echo
echo "WARNING: this permanently discards:"
echo "  - all modified or staged tracked files"
echo "  - all local commits not present in $remote/$branch"
echo "Untracked and ignored files will be preserved."
echo
git status --short

if [[ "$assume_yes" != true ]]; then
  read -r -p "Type OVERWRITE to continue: " confirmation
  if [[ "$confirmation" != "OVERWRITE" ]]; then
    echo "Cancelled; no repository files were changed."
    exit 1
  fi
fi

echo "Fetching $remote/$branch..."
git fetch --prune "$remote" "$branch"
git rev-parse --verify "refs/remotes/$remote/$branch^{commit}" >/dev/null || {
  echo "ERROR: fetched remote branch is unavailable: $remote/$branch" >&2
  exit 1
}

echo "Resetting tracked files and local branch to $remote/$branch..."
git reset --hard "$remote/$branch"
git branch --set-upstream-to="$remote/$branch" "$branch" >/dev/null

echo "Confirming the checkout is current..."
git pull --ff-only "$remote" "$branch"

echo
echo "Remote synchronization complete."
echo "HEAD: $(git rev-parse --short HEAD)"
if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "Preserved untracked/ignored-local state:"
  git status --short
else
  echo "Working tree is clean."
fi
