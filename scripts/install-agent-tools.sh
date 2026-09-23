#!/usr/bin/env bash
#
# Build statectl and state-runner from this repository and install both into
# ~/.local/bin. Run it from anywhere inside the checkout:
#
#   scripts/install-agent-tools.sh
#
# The script never edits shell profiles; it only tells you which line to add.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

if ! command -v go >/dev/null 2>&1; then
  echo "go is not installed or not on PATH" >&2
  exit 1
fi

target_dir="${HOME}/.local/bin"
version="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"

mkdir -p "${target_dir}"

echo "installing statectl and state-runner ${version} into ${target_dir}"
for command in statectl state-runner; do
  go build -trimpath -ldflags "-X main.version=${version}" -o "${target_dir}/${command}" "./cmd/${command}"
  echo "  ${target_dir}/${command}"
done

case ":${PATH}:" in
  *":${target_dir}:"*)
    echo "${target_dir} is already on your PATH"
    ;;
  *)
    echo
    echo "${target_dir} is not on your PATH yet. Add this line to ~/.zshrc and restart the shell:"
    echo
    echo "export PATH=\"\${HOME}/.local/bin:\${PATH}\""
    ;;
esac

echo
echo "next: pair this machine with a runner code from the app (Settings, Runners),"
echo "then run 'state-runner service install' to keep the runner alive across logins."
