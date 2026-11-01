# The README's terminal recording, about 30 seconds: install, add a site, request it over HTTPS.
#
# It runs the real `dotlane install` (sudo, the login service, the System keychain), so record it
# on a throwaway Mac, never a development one: the "demo" workflow (.github/workflows/demo.yml)
# runs it on a GitHub macOS runner and uploads dotlane.gif, to commit next to this file.
# Needs VHS (https://github.com/charmbracelet/vhs), ttyd and ffmpeg, and `dotlane` on the PATH.

Output assets/demo/dotlane.gif

Require dotlane
Require curl
Require python3

Set Shell "bash"
Set FontSize 18
Set Width 1100
Set Height 620
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set WaitTimeout 90s
Set WaitPattern /\$ ?$/

# A dev server on port 5173, standing in for Vite or any other.
Hide
Type `export PS1='$ ' && cd "$(mktemp -d)" && echo 'Hello from myapp' > index.html` Enter
Type "python3 -m http.server 5173 --bind 127.0.0.1 >/dev/null 2>&1 &" Enter
Type "clear" Enter
Wait
Show

Sleep 800ms
Type "dotlane install" Sleep 400ms Enter
Wait
Sleep 2s

Type "dotlane add myapp 5173" Sleep 400ms Enter
Wait
Sleep 2s

Type "curl https://myapp.traix" Sleep 400ms Enter
Wait
Sleep 4s
