# bash completion for the parallax multi-call wrapper.
#
# The wrapper has a fixed subcommand set (node, rpc, help, version) and
# delegates argument completion to parallaxd / parallax-cli via their
# --generate-bash-completion hook (urfave/cli v1 runtime completion).
#
# Install:
#   source build/completion/parallax.bash
# or copy to /etc/bash_completion.d/ (root) or ~/.local/share/bash-completion/completions/parallax (user).

_parallax_bash_autocomplete() {
    local cur sub rest opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    if (( COMP_CWORD == 1 )); then
        COMPREPLY=( $(compgen -W "node rpc help version --help -h --version -v" -- "$cur") )
        return 0
    fi

    sub="${COMP_WORDS[1]}"
    case "$sub" in
        node)
            rest=( "${COMP_WORDS[@]:2:$((COMP_CWORD - 2))}" )
            if [[ "$cur" == "-"* ]]; then
                opts=$( parallaxd "${rest[@]}" "$cur" --generate-bash-completion 2>/dev/null )
            else
                opts=$( parallaxd "${rest[@]}" --generate-bash-completion 2>/dev/null )
            fi
            COMPREPLY=( $(compgen -W "${opts}" -- "$cur") )
            ;;
        rpc)
            rest=( "${COMP_WORDS[@]:2:$((COMP_CWORD - 2))}" )
            if [[ "$cur" == "-"* ]]; then
                opts=$( parallax-cli "${rest[@]}" "$cur" --generate-bash-completion 2>/dev/null )
            else
                opts=$( parallax-cli "${rest[@]}" --generate-bash-completion 2>/dev/null )
            fi
            COMPREPLY=( $(compgen -W "${opts}" -- "$cur") )
            ;;
        *)
            ;;
    esac
    return 0
}

complete -F _parallax_bash_autocomplete -o bashdefault -o default parallax
