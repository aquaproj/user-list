package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aquaproj/user-list/pkg/controller"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	logger := log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel).With().Str("program", "list-aqua-users").Logger()

	if err := core(logger); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func core(logger zerolog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := controller.New()
	return c.Run(ctx, logger) //nolint:wrapcheck
}
