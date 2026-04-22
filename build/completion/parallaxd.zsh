# zsh completion for parallaxd
#
# Runtime-driven via urfave/cli v1's --generate-bash-completion hook —
# always matches the installed binary, no regeneration required.
#
# Install:
#   source build/completion/parallaxd.zsh
# or drop under a directory on $fpath (e.g. ~/.zsh/completions/_parallaxd)
# after wrapping the body in a #compdef directive.

_parallaxd() {
    local -a opts
    local cur="${words[CURRENT]}"
    if [[ "$cur" == "-"* ]]; then
        opts=( ${(f)"$(${words[@]:0:$((CURRENT - 1))} $cur --generate-bash-completion 2>/dev/null)"} )
    else
        opts=( ${(f)"$(${words[@]:0:$((CURRENT - 1))} --generate-bash-completion 2>/dev/null)"} )
    fi
    if [[ ${#opts[@]} -eq 0 ]]; then
        _default
    else
        _describe 'values' opts
    fi
    return
}

compdef _parallaxd parallaxd
