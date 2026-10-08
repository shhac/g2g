# g2g demo recording: adopting branches made with plain git
# Run from the repository root: vhs assets/demo-adopt.tape
#
# Everything runs against throwaway repositories under /tmp created by
# assets/demo-setup.sh, with a bare repository as the remote. No real
# repository or remote is touched, and nothing leaves the machine.
Output assets/demo-adopt.gif
Output assets/demo-adopt.webm

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Fira Code"
Set FontSize 14
Set Width 1080
Set Height 600
Set Padding 16
Set Framerate 5
Set TypingSpeed 40ms
Set CursorBlink false

# Hidden setup: throwaway repositories, a freshly built g2g, no global config
Hide
Type "bash assets/demo-setup.sh"
Enter
Sleep 12s
Type "export PATH=/tmp/g2g-demo/bin:$PATH GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=Demo GIT_AUTHOR_EMAIL=demo@example.test GIT_COMMITTER_NAME=Demo GIT_COMMITTER_EMAIL=demo@example.test PS1='$ '"
Enter
Type "cd /tmp/g2g-demo/acme-api && clear"
Enter
Sleep 500ms
Show

# Branches made the ordinary way, two of them on the same parent
Type `git switch -qc feature/api && echo api > api.js && git add . && git commit -qm "Add the API"`
Sleep 300ms
Enter
Sleep 800ms
Type `git switch -qc feature/client && echo client > client.js && git add . && git commit -qm "Add a client"`
Sleep 300ms
Enter
Sleep 800ms
Type `git switch -qc feature/docs feature/api && echo docs > docs.md && git add . && git commit -qm "Add docs"`
Sleep 300ms
Enter
Sleep 800ms
Type "git switch -q feature/api"
Sleep 300ms
Enter
Sleep 800ms
Type "g2g status"
Sleep 300ms
Enter
Sleep 3.5s
Hide
Type "clear"
Enter
Sleep 300ms
Show

# One command records them all, ordered by commit ancestry
Type "g2g adopt --trunk main --apply"
Sleep 300ms
Enter
Sleep 4s
Hide
Type "clear"
Enter
Sleep 300ms
Show

# The fork is recorded and drawn as one
Type "g2g status"
Sleep 300ms
Enter
Sleep 3.5s

# Both arms publish in one atomic push
Type "g2g push --apply"
Sleep 300ms
Enter
Sleep 3.5s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "g2g status"
Sleep 300ms
Enter
Sleep 5s
