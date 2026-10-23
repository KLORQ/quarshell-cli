package cli

import (
	"fmt"

	"github.com/KLORQ/quarshell-cli/internal/service"
)

func runDoctor(a *service.App, args []string) error {
	// The doctor command defaults to the check operation.
	if len(args) == 0 {
		return a.Doctor()
	}
	if args[0] == "check" {
		if len(args) != 1 {
			return fmt.Errorf("usage: quarshell doctor check")
		}
		return a.Doctor()
	}
	return fmt.Errorf("usage: quarshell doctor check")
}
