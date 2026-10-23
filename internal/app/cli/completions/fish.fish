complete -c quarshell -f
complete -c quarshell -n "__fish_use_subcommand" -a "version sandbox sb cp exec provider profile policy pol gateway gw logs lg term status init doctor dr whoami workspace ws forward fwd service svc settings rule rl install completions ssh-proxy"
complete -c quarshell -n "__fish_seen_subcommand_from service svc; and test (count (commandline -opc)) -eq 2" -a "expose list get delete"
complete -c quarshell -n "__fish_seen_subcommand_from sandbox sb; and __fish_seen_subcommand_from provider; and test (count (commandline -opc)) -eq 3" -a "list attach detach status"
