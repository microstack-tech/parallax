# bash completion for parallaxd
#
# urfave/cli v1 exposes runtime completion via the --generate-bash-completion
# hidden flag: when that flag is the final argument, the app prints the set
# of valid next-tokens (subcommands, flags, or argument values) instead of
# executing the command. This script just shells out to the binary and
# feeds the result to bash's COMPREPLY.
#
# Install:
#   source build/completion/parallaxd.bash
# or copy to /etc/bash_completion.d/ (root) or ~/.local/share/bash-completion/completions/parallaxd (user).

_parallaxd_bash_autocomplete() {
    local cur opts base
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    if [[ "$cur" == "-"* ]]; then
        opts=$( "${COMP_WORDS[@]:0:$COMP_CWORD}" ${cur} --generate-bash-completion )
    else
        opts=$( "${COMP_WORDS[@]:0:$COMP_CWORD}" --generate-bash-completion )
    fi
    COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
    return 0
}

complete -F _parallaxd_bash_autocomplete -o bashdefault -o default parallaxd
