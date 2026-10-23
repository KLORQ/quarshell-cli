Register-ArgumentCompleter -CommandName quarshell -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $elements = $commandAst.CommandElements
    $candidates = if ($elements.Count -eq 2 -and $elements[1].Value -in @('service', 'svc')) {
        @('expose', 'list', 'get', 'delete')
    } elseif ($elements.Count -eq 3 -and $elements[1].Value -in @('sandbox', 'sb') -and $elements[2].Value -eq 'provider') {
        @('list', 'attach', 'detach', 'status')
    } else { @(
        'version', 'sandbox', 'sb', 'exec', 'provider', 'profile', 'policy', 'pol',
        'gateway', 'gw', 'logs', 'lg', 'term', 'status', 'init', 'doctor', 'dr',
        'whoami', 'workspace', 'ws', 'forward', 'fwd', 'service', 'svc', 'settings',
        'rule', 'rl', 'install', 'completions', 'ssh-proxy', 'cp'
    ) }

    $candidates |
        Where-Object { $_ -like "$wordToComplete*" } |
        ForEach-Object {
            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
}
