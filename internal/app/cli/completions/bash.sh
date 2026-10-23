# cautem bash completions
_cautem() {
    local current_word="${COMP_WORDS[COMP_CWORD]}"
    local -a commands=(
        version sandbox sb cp exec provider profile policy pol gateway gw logs lg term status
        init doctor dr whoami workspace ws forward fwd service svc settings
        rule rl install completions ssh-proxy
    )

    COMPREPLY=()
    if (( COMP_CWORD == 2 )) && [[ "${COMP_WORDS[1]}" == "service" || "${COMP_WORDS[1]}" == "svc" ]]; then
        COMPREPLY=( $(compgen -W "expose list get delete" -- "$current_word") )
        return
    fi
    if (( COMP_CWORD == 3 )) && [[ "${COMP_WORDS[1]}" == "sandbox" || "${COMP_WORDS[1]}" == "sb" ]] && [[ "${COMP_WORDS[2]}" == "provider" ]]; then
        COMPREPLY=( $(compgen -W "list attach detach status" -- "$current_word") )
        return
    fi
    if (( COMP_CWORD == 1 )); then
        # Command substitution keeps this compatible with macOS's Bash 3.2.
        COMPREPLY=( $(compgen -W "${commands[*]}" -- "$current_word") )
    fi
}

complete -F _cautem cautem
