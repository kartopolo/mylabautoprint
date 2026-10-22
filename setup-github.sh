#!/usr/bin/env bash
# ONE-TIME GitHub setup (run manually after reviewing!). Requires gh logged in.
#   SOURCE_REPO   private source repo   (default kartopolo/mylabautoprint)
#   RELEASES_REPO public releases repo  (default kartopolo/mylabautoprint-releases)
#   KEY_FILE      ed25519 private key   (default ../keys/private.key)
#
# What it does:
#   1. creates SOURCE_REPO (private) and RELEASES_REPO (public) if missing
#   2. stores the signing key as Actions secret MYLABAUTOPRINT_SIGNING_KEY (never committed)
#   3. asks you to paste a fine-grained PAT for RELEASES_TOKEN (see README) and stores it
#   4. commits + pushes this source tree (keys are .gitignored), optionally tags v1.0.0
set -euo pipefail
cd "$(dirname "$0")"
SOURCE_REPO="${SOURCE_REPO:-kartopolo/mylabautoprint}"
RELEASES_REPO="${RELEASES_REPO:-kartopolo/mylabautoprint-releases}"
KEY_FILE="${KEY_FILE:-../keys/private.key}"
gh auth status >/dev/null

gh repo view "$RELEASES_REPO" >/dev/null 2>&1 || gh repo create "$RELEASES_REPO" --public \
  --description "MyLab AutoPrint - installer & update releases" --add-readme
gh repo view "$SOURCE_REPO" >/dev/null 2>&1 || gh repo create "$SOURCE_REPO" --private \
  --description "MyLab AutoPrint source"

gh secret set MYLABAUTOPRINT_SIGNING_KEY --repo "$SOURCE_REPO" < "$KEY_FILE"
gh variable set RELEASES_REPO --repo "$SOURCE_REPO" --body "$RELEASES_REPO"

if ! gh secret list --repo "$SOURCE_REPO" | grep -q '^RELEASES_TOKEN'; then
  echo
  echo "Buat fine-grained PAT: https://github.com/settings/personal-access-tokens/new"
  echo "  Repository access: Only select repositories -> $RELEASES_REPO"
  echo "  Permissions: Contents = Read and write"
  read -rsp "Paste token (tidak ditampilkan, Enter kosong = lewati): " TOKEN; echo
  if [[ -n "$TOKEN" ]]; then printf '%s' "$TOKEN" | gh secret set RELEASES_TOKEN --repo "$SOURCE_REPO"; fi
fi

if [[ ! -d .git ]]; then git init -q -b main; fi
git add -A
git -c user.name="${GIT_NAME:-kartopolo}" -c user.email="${GIT_EMAIL:-kartopolo@users.noreply.github.com}" commit -qm "MyLab AutoPrint" || true
git remote get-url origin >/dev/null 2>&1 || git remote add origin "https://github.com/$SOURCE_REPO.git"
git push -u origin main
if [[ "${TAG:-}" != "" ]]; then git tag -a "$TAG" -m "${TAG_NOTES:-Rilis $TAG}"; git push origin "$TAG"; fi
echo "Selesai. Rilis: git tag -a v1.0.1 -m 'catatan' && git push origin v1.0.1"
