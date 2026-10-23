#compdef quarshell
_quarshell() {
    if (( CURRENT == 3 )) && [[ "${words[2]}" == "service" || "${words[2]}" == "svc" ]]; then
        local -a service_commands=(expose list get delete)
        _describe 'service subcommand' service_commands
        return
    fi
    if (( CURRENT == 4 )) && [[ "${words[2]}" == "sandbox" || "${words[2]}" == "sb" ]] && [[ "${words[3]}" == "provider" ]]; then
        local -a provider_commands=(list attach detach status)
        _describe 'sandbox provider subcommand' provider_commands
        return
    fi
    local -a commands=(
        version sandbox sb cp exec provider profile policy pol gateway gw logs lg term status
        init doctor dr whoami workspace ws forward fwd service svc settings
        rule rl install completions ssh-proxy
    )

    _describe 'command' commands
}

compdef _quarshell quarshell
