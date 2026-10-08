# g2g demo recording: building and publishing a stack
# Run from the repository root: vhs assets/demo-create.tape
#
# Everything runs against throwaway repositories under /tmp created by
# assets/demo-setup.sh, with a bare repository as the remote. No real
# repository or remote is touched, and nothing leaves the machine.
Output assets/demo-create.gif
Output assets/demo-create.webm

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontFamily "Fira Code"
Set FontSize 14
Set Width 1080
Set Height 560
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
Hide
Type "printf 'export function auth() {}\n' > auth.js && git add auth.js && clear"
Enter
Sleep 300ms
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

# Publish both in one atomic push
Type "g2g push --apply"
Sleep 300ms
Enter
Sleep 3.5s
Type "g2g status"
Sleep 300ms
Enter
Sleep 5s
