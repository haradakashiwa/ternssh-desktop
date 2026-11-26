package shell

// zsh's prompt marks, as MyGo's terminal plugin sets them for a zsh of its
// own (plugins/terminal/shell_integration.go, MIT): the terminal, told
// where each prompt is, clears its old layout as it resizes, before zsh
// draws it again.

import (
	"os"
	"path/filepath"
	"strings"
)

func zshEnvironment(env []string) ([]string, string, error) {
	dir, err := os.MkdirTemp("", "ternssh-zsh-")
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".zshenv"), []byte(zshIntegration), 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, "", err
	}
	clean := make([]string, 0, len(env)+1)
	var original string
	var hadOriginal bool
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "ZDOTDIR="); ok {
			original, hadOriginal = value, true
		} else if !strings.HasPrefix(entry, "MYGO_TERMINAL_ZDOTDIR=") {
			clean = append(clean, entry)
		}
	}
	if hadOriginal {
		clean = append(clean, "MYGO_TERMINAL_ZDOTDIR="+original)
	}
	return append(clean, "ZDOTDIR="+dir), dir, nil
}

// Restore ZDOTDIR before sourcing any user configuration. Install hooks at
// the first precmd, after startup files and prompt themes have loaded. P10k
// emits its own markers when asked, including asynchronous prompt updates.
const zshIntegration = `if (( ${+MYGO_TERMINAL_ZDOTDIR} )); then
  export ZDOTDIR=$MYGO_TERMINAL_ZDOTDIR
  unset MYGO_TERMINAL_ZDOTDIR
else
  unset ZDOTDIR
fi
[[ ! -r ${ZDOTDIR-$HOME}/.zshenv ]] || source -- "${ZDOTDIR-$HOME}/.zshenv"
[[ -o interactive ]] || return

typeset -gi __p9k_force_term_shell_integration=1

_mygo_mark_prompt() {
  local code=$?
  emulate -L zsh
  if ! zle; then
    printf '\e]133;D;%d\a' $code
  fi
  [[ -o promptpercent ]] || return
  local start=$'%{\e]133;A;cl=line;redraw=1\a%}' input=$'%{\e]133;B\a%}'
  local continuation=$'%{\e]133;P;k=s\a%}'
  if [[ $PS1 != *$'\e]133;'* ]]; then
    PS1=$start${PS1//$'\n'/$'\n'$continuation}$input
  fi
  [[ $PS2 == *$'\e]133;'* ]] || PS2=$continuation$PS2$input
}
_mygo_command_start() {
  printf '\e]133;C\a'
}
_mygo_install_marks() {
  emulate -L zsh
  autoload -Uz add-zsh-hook
  add-zsh-hook -d precmd _mygo_install_marks
  add-zsh-hook precmd _mygo_mark_prompt
  add-zsh-hook preexec _mygo_command_start
  _mygo_mark_prompt
}
typeset -ga precmd_functions
precmd_functions+=(_mygo_install_marks)
`
