# g2g demo recording: a commit in the middle of a stack
# Run from the repository root: vhs assets/demo-restack.tape
#
# Everything runs against throwaway repositories under /tmp created by
# assets/demo-setup.sh, with a bare repository as the remote. No real
# repository or remote is touched, and nothing leaves the machine.
Output assets/demo-restack.gif
Output assets/demo-restack.webm

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
Type "bash assets/demo-setup.sh published"
Enter
Sleep 12s
Type "export PATH=/tmp/g2g-demo/bin:$PATH GIT_CONFIG_GLOBAL=/dev/null GIT_AUTHOR_NAME=Demo GIT_AUTHOR_EMAIL=demo@example.test GIT_COMMITTER_NAME=Demo GIT_COMMITTER_EMAIL=demo@example.test PS1='$ '"
Enter
Type "cd /tmp/g2g-demo/acme-api && clear"
Enter
Sleep 500ms
Show

# A published stack, in step with origin
Type "g2g status"
Sleep 300ms
Enter
Sleep 3s

# Step down to the bottom branch and commit there
Type "g2g down 2"
Sleep 300ms
Enter
Sleep 1.5s
Type "echo 'export const strict = true' >> auth.js"
Sleep 300ms
Enter
Sleep 500ms
Type `git commit -qam "Make auth strict"`
Sleep 300ms
Enter
Sleep 1s
Type "g2g status"
Sleep 300ms
Enter
Sleep 4s
Hide
Type "clear"
Enter
Sleep 300ms
Show

# Every change previews first; --apply carries it out
Type "g2g restack"
Sleep 300ms
Enter
Sleep 3s
Type "g2g restack --apply"
Sleep 300ms
Enter
Sleep 3.5s
Hide
Type "clear"
Enter
Sleep 300ms
Show

# Publish the replayed stack
Type "g2g push --apply"
Sleep 300ms
Enter
Sleep 3.5s
Type "g2g status"
Sleep 300ms
Enter
Sleep 5s
