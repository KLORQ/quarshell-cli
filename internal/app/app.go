// Package app is the composition root for the quarshell CLI binary.
package app

import (
	"context"
	"log/slog"

	"github.com/KLORQ/quarshell-cli/internal/app/cli"
	"github.com/KLORQ/quarshell-cli/internal/global"
	"github.com/KLORQ/quarshell-cli/internal/logger"
)

// Run initializes logging and executes the CLI.
func Run(ctx context.Context, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	flags, _ := global.Parse(args)
	level := slog.LevelInfo
	if flags.Verbose {
		level = slog.LevelDebug
	}
	log := logger.Setup(ctx, logger.Options{Service: "quarshell", Level: level})
	ctx = logger.ToContext(ctx, log)
	return cli.Execute(ctx, args)
}
