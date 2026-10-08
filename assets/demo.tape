# g2g demo recording
# Run from the repository root: vhs assets/demo.tape
#
# Everything runs against throwaway repositories under /tmp created by
# assets/demo-setup.sh, with a bare repository as the remote. No real
# repository or remote is touched, and nothing leaves the machine.
Output assets/demo.gif
Output assets/demo.webm

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Fira Code"
Set FontSize 14
Set Width 1000
Set Height 520
Set Padding 16
Set Framerate 5
Set TypingSpeed 40ms
Set CursorBlink false

# Hidden setup: throwaway repositories, a freshly built g2g, no global config
Hide
Type "bash assets/demo-setup.sh"
Enter
Sleep 8s
Type "export PATH=/tmp/g2g-demo/bin:$PATH GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=Demo GIT_AUTHOR_EMAIL=demo@example.test GIT_COMMITTER_NAME=Demo GIT_COMMITTER_EMAIL=demo@example.test PS1='$ '"
Enter
Type "cd /tmp/g2g-demo/acme-api && printf 'export function auth() {}\n' > auth.js && git add auth.js && clear"
Enter
Sleep 500ms
Show

# Start a branch on main, committing what is staged
Type `g2g create feature/auth -m "Add the auth middleware" --apply`
Sleep 300ms
Enter
Sleep 4s

Hide
Type "printf 'export function login() {}\n' > login.js && git add login.js && clear"
Enter
Sleep 300ms
Show

# Stack a second branch on top of it
Type `g2g create feature/login -m "Add the login form" --apply`
Sleep 300ms
Enter
Sleep 4s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# Where the stack stands, from local refs alone
Type "g2g status"
Sleep 300ms
Enter
Sleep 3.5s

# Meanwhile a colleague lands a change on main
Hide
Type "bash ../trunk-moves.sh && clear"
Enter
Sleep 2s
Show

Type "# a colleague has since landed a change on main"
Sleep 300ms
Enter
Sleep 1.5s

# Every change previews first
Type "g2g pull"
Sleep 300ms
Enter
Sleep 3.5s

# --apply carries it out: main fast-forwards, the stack replays onto it
Type "g2g pull --apply"
Sleep 300ms
Enter
Sleep 4s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# Publish both branches in one atomic push
Type "g2g push --apply"
Sleep 300ms
Enter
Sleep 3.5s

Type "g2g status"
Sleep 300ms
Enter
Sleep 5s
