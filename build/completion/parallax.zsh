# zsh completion for the parallax multi-call wrapper.
#
# The wrapper's first argument is always a subcommand name; `node` and `rpc`
# delegate further completion to parallaxd / parallax-cli respectively via
# urfave/cli v1's --generate-bash-completion hook.
#
# Install:
#   source build/completion/parallax.zsh
# or drop under a directory on $fpath.

_parallax() {
    local -a opts
    local cur="${words[CURRENT]}"

    if (( CURRENT == 2 )); then
        _describe 'subcommand' '(node:"run the parallaxd daemon" rpc:"send an RPC via parallax-cli" help:"show wrapper help" version:"print version")'
        return
    fi

    local sub="${words[2]}"
    local -a rest
    rest=( "${words[@]:2:$((CURRENT - 2))}" )
    case "$sub" in
        node)
            if [[ "$cur" == "-"* ]]; then
                opts=( ${(f)"$(parallaxd ${rest[@]} $cur --generate-bash-completion 2>/dev/null)"} )
            else
                opts=( ${(f)"$(parallaxd ${rest[@]} --generate-bash-completion 2>/dev/null)"} )
            fi
            ;;
        rpc)
            if [[ "$cur" == "-"* ]]; then
                opts=( ${(f)"$(parallax-cli ${rest[@]} $cur --generate-bash-completion 2>/dev/null)"} )
            else
                opts=( ${(f)"$(parallax-cli ${rest[@]} --generate-bash-completion 2>/dev/null)"} )
            fi
            ;;
    esac
    if [[ ${#opts[@]} -gt 0 ]]; then
        _describe 'values' opts
    else
        _default
    fi
    return
}

compdef _parallax parallax
