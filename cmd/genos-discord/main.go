package main

import (
	"fmt"
	"io"
	"os"

	"github.com/AnthonyPoschen/genos-discord/internal/bot"
	"github.com/AnthonyPoschen/genos-discord/internal/config"
	"github.com/AnthonyPoschen/genos-discord/internal/discordapi"
	"github.com/AnthonyPoschen/genos-discord/internal/gateway"
)

func main() {
	if code := reportMissing(os.Getenv, os.Stdout); code != 0 {
		os.Exit(code)
	}
	cfg, _ := config.Load(os.Getenv)
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "genos-discord: %s\n", err)
		os.Exit(1)
	}
}

func reportMissing(getenv func(string) string, out io.Writer) int {
	_, missing := config.Load(getenv)
	if len(missing) == 0 {
		return 0
	}
	for _, name := range missing {
		fmt.Fprintln(out, name)
	}
	fmt.Fprintln(out, config.HumanGap)
	return 1
}

func run(cfg config.Config) error {
	svc := bot.New(bot.Options{
		GenosOrigin:     cfg.GenosAPIOrigin,
		GenosBotSecret:  cfg.GenosDiscordBotSecret,
		DiscordAPIBase:  discordapi.DefaultAPIBase,
		DiscordBotToken: cfg.DiscordBotToken,
		GuildID:         cfg.DiscordGuildID,
		CustomerRoleID:  cfg.DiscordCustomerRoleID,
	})
	ctx, stop := gateway.NotifyContext()
	defer stop()
	return gateway.Run(ctx, cfg.DiscordBotToken, cfg.DiscordApplicationID, cfg.DiscordGuildID, svc)
}
