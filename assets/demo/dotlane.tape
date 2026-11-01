# The README's terminal recording, about 30 seconds: install, add a site, request it over HTTPS.
#
# It runs the real `dotlane install` (sudo, the login service, the System keychain), so record it
# on a throwaway Mac, never a development one: the "demo" workflow (.github/workflows/demo.yml)
# runs it on a GitHub macOS runner and uploads dotlane.gif, to commit next to this file.
# Needs VHS (https://github.com/charmbracelet/vhs), ttyd and ffmpeg, `dotlane` on the PATH, and a
# dev server answering "Hello from myapp" on port 5173, which the workflow starts first.

Output assets/demo/dotlane.gif

Require dotlane
Require curl

Set Shell "bash"
Set FontSize 17
Set Width 1100
# Tall enough that nothing scrolls: Wait+Screen below only sees the top screenful.
Set Height 800
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set WaitTimeout 90s
Set WaitPattern /\$ ?$/

Hide
Sleep 1s
Type "export PS1='$ ' && clear" Enter
Wait
Show

Sleep 800ms
Type "dotlane install" Sleep 400ms Enter
Wait
Sleep 6s

Type "dotlane add myapp 5173" Sleep 400ms Enter
Wait
Sleep 3s

Type "curl https://myapp.traix" Sleep 400ms Enter
# Fails the recording if the page is Dotlane's 502 instead of the dev server's answer.
Wait+Screen@20s /Hello from myapp/
Wait
Sleep 5s
