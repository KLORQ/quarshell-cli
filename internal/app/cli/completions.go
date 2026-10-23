package cli

import _ "embed"

// Shell completion scripts live separately from command dispatch so each
// shell's syntax stays readable and can be checked with its native parser.
//
//go:embed completions/bash.sh
var completionsBash string

//go:embed completions/zsh.sh
var completionsZsh string

//go:embed completions/fish.fish
var completionsFish string

//go:embed completions/powershell.ps1
var completionsPowerShell string
