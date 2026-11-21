# macOS Persistent API Configuration

Use the macOS login Keychain for the API key and `~/.zprofile` to export it for zsh login shells. The Keychain generic-password service name for this skill is `codex-image-gen`; the account is the current macOS user. Never ask the user to paste the key into chat or write the key value into a file or command line.

## Store or update the key

If the entry is not already readable, ask the user to run this in Terminal. The `-w` option without an inline value prompts for the secret:

```zsh
/usr/bin/security add-generic-password -U -a "$(id -un)" -s "codex-image-gen" -w
```

If macOS shows a Keychain access prompt on first read, the user must authorize it. Choosing “Always Allow” is optional and avoids repeated prompts for that requesting app.

## Configure `~/.zprofile`

Open the file with `open -e ~/.zprofile` and add or update these lines:

```zsh
# Codex ImageGen Mac credentials
export CODEX_API_KEY="$(/usr/bin/security find-generic-password -a "$USER" -s "codex-image-gen" -w 2>/dev/null)"
# Replace the placeholder with the provider's exact API base URL before saving.
export CODEX_API_URL="REPLACE_WITH_PROVIDER_API_BASE_URL"
```

Do not leave the URL placeholder in place. Use the API base URL supplied by the provider, not its website homepage. Do not add the API key itself to `.zprofile`.

## Verify without revealing the key

First verify the Keychain item directly; this discards the password output:

```zsh
if /usr/bin/security find-generic-password -a "$(id -un)" -s "codex-image-gen" -w >/dev/null 2>&1; then
  print "Keychain read: OK"
else
  print "Keychain read: failed"
fi
```

Then verify the login shell loads both variables without displaying either value:

```zsh
zsh -lc 'if [[ -n "$CODEX_API_KEY" && -n "$CODEX_API_URL" ]]; then print "API configuration loaded"; else print "API configuration incomplete"; fi'
```

Finally run the CLI's `models` query through that same login shell. This makes a real read-only request and confirms the configured URL and credential can reach the gateway; it does not generate an image or prove that every advertised model has a working generation route.

```zsh
zsh -lc '
skill_dir="${CODEX_HOME:-$HOME/.codex}/skills/codex-image-gen-mac"
case "$(/usr/bin/uname -m)" in
  arm64) cli="$skill_dir/bin/codex-image-gen-mac-darwin-arm64" ;;
  x86_64) cli="$skill_dir/bin/codex-image-gen-mac-darwin-amd64" ;;
  *) print "Unsupported macOS architecture"; exit 1 ;;
esac
"$cli" models
'
```

If Keychain verification passes in Terminal but the login-shell check or `models` call fails inside Codex, report the distinction clearly. Do not print the key or claim the model catalog is reachable. Use the CLI's `doctor` for offline source diagnostics and `models` for the network check. A listed model is advertised by the gateway, not guaranteed to generate successfully; preserve exact HTTP status and provider message when a generation attempt fails.
